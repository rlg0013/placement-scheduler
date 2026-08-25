package main

import (
	"fmt"
	"sort"
	"time"

	"placement-scheduler/pkg/generator"
)

func main() {
	data := generator.Generate(generator.DefaultConfig())

	fmt.Printf("Rooms: %d\n", len(data.Rooms))
	fmt.Printf("Students: %d\n", len(data.Students))
	fmt.Printf("Companies: %d\n", len(data.Companies))
	fmt.Printf("Panels: %d\n", len(data.Panels))
	fmt.Printf("Shortlist entries: %d\n\n", len(data.Shortlists))

	// How many companies shortlisted each student? This is the "top
	// students appear on many overlapping lists" claim — let's see if
	// it actually holds.
	countByStudent := map[string]int{}
	for _, sl := range data.Shortlists {
		countByStudent[sl.StudentID]++
	}

	zeroShortlists := 0
	maxShortlists := 0
	totalShortlisted := 0
	for _, s := range data.Students {
		c := countByStudent[s.ID]
		if c == 0 {
			zeroShortlists++
		} else {
			totalShortlisted++
		}
		if c > maxShortlists {
			maxShortlists = c
		}
	}
	fmt.Printf("Students with 0 shortlists: %d\n", zeroShortlists)
	fmt.Printf("Students with 1+ shortlists: %d\n", totalShortlisted)
	fmt.Printf("Max companies shortlisting a single student: %d\n\n", maxShortlists)

	// Does CGPA correlate with shortlist count? Print top 5 and bottom 5
	// CGPA students alongside their shortlist counts.
	type row struct {
		id    string
		cgpa  float64
		count int
	}
	rows := make([]row, 0, len(data.Students))
	for _, s := range data.Students {
		rows = append(rows, row{s.ID, s.CGPA, countByStudent[s.ID]})
	}
	sort.Slice(rows, func(i, j int) bool { return rows[i].cgpa > rows[j].cgpa })

	fmt.Println("Top 5 CGPA students:")
	for _, r := range rows[:5] {
		fmt.Printf("  %s  CGPA %.1f  -> %d shortlists\n", r.id, r.cgpa, r.count)
	}
	fmt.Println("Bottom 5 CGPA students:")
	for _, r := range rows[len(rows)-5:] {
		fmt.Printf("  %s  CGPA %.1f  -> %d shortlists\n", r.id, r.cgpa, r.count)
	}
	fmt.Println()

	// Per-company shortlist size and cutoff, sorted by ID, so you can
	// eyeball whether mass/mid/niche tiers look distinct.
	companyIDs := make([]string, 0, len(data.Companies))
	for id := range data.Companies {
		companyIDs = append(companyIDs, id)
	}
	sort.Strings(companyIDs)

	shortlistSizeByCompany := map[string]int{}
	for _, sl := range data.Shortlists {
		shortlistSizeByCompany[sl.CompanyID]++
	}

	fmt.Println("Per-company: tier, CGPA cutoff, panels, shortlist size")
	for _, id := range companyIDs {
		c := data.Companies[id]
		fmt.Printf("  %-10s tier=%-14v cutoff=%.1f panels=%d shortlisted=%d\n",
			c.ID, c.Tier, c.CGPACutoff, len(c.Panels), shortlistSizeByCompany[c.ID])
	}
	fmt.Println()

	// Capacity check: for each company, how many interviews COULD its
	// panels physically run in its slot window, vs how many it actually
	// shortlisted? >100% utilization means that company alone cannot
	// get through its own shortlist even with perfect scheduling —
	// which is fine in moderation (that's the "infeasible" the
	// assignment wants) but shouldn't be true for almost everyone.
	fmt.Println("Capacity check: shortlisted vs interview-slots available (window / duration * panels)")
	overCapacity := 0
	for _, id := range companyIDs {
		c := data.Companies[id]
		windowMinutes := c.Slots[0].End.Sub(c.Slots[0].Start).Minutes()
		capacityPerPanel := int(windowMinutes) / int(c.InterviewDuration.Minutes())
		totalCapacity := capacityPerPanel * len(c.Panels)
		shortlisted := shortlistSizeByCompany[c.ID]
		utilization := float64(shortlisted) / float64(totalCapacity) * 100
		flag := ""
		if utilization > 100 {
			flag = "  <-- OVER CAPACITY"
			overCapacity++
		}
		fmt.Printf("  %-10s capacity=%-4d shortlisted=%-4d utilization=%.0f%%%s\n",
			c.ID, totalCapacity, shortlisted, utilization, flag)
	}
	fmt.Printf("\n%d/%d companies are over their own interview capacity\n", overCapacity, len(companyIDs))

	// Room contention check: within Day 1's AM wave, PM wave, and each
	// other day, sum up total panels wanting a room at once. This
	// should stay at or below NumRooms (20) most of the time.
	fmt.Println("\nRoom contention by day+wave (panels needing a room simultaneously):")
	type waveKey struct {
		day  time.Time
		wave string
	}
	panelsByWave := map[waveKey]int{}
	for _, id := range companyIDs {
		c := data.Companies[id]
		day := c.Slots[0].Start.Truncate(24 * time.Hour)
		wave := fmt.Sprintf("%02d:00-%02d:00", c.Slots[0].Start.Hour(), c.Slots[0].End.Hour())
		panelsByWave[waveKey{day, wave}] += len(c.Panels)
	}
	waveKeys := make([]waveKey, 0, len(panelsByWave))
	for k := range panelsByWave {
		waveKeys = append(waveKeys, k)
	}
	sort.Slice(waveKeys, func(i, j int) bool {
		if !waveKeys[i].day.Equal(waveKeys[j].day) {
			return waveKeys[i].day.Before(waveKeys[j].day)
		}
		return waveKeys[i].wave < waveKeys[j].wave
	})
	for _, k := range waveKeys {
		flag := ""
		if panelsByWave[k] > 20 {
			flag = "  <-- EXCEEDS 20 ROOMS"
		}
		fmt.Printf("  %s %s: %d panels%s\n", k.day.Format("Jan 2"), k.wave, panelsByWave[k], flag)
	}
}
