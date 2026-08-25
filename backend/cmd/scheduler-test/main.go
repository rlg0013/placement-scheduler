package main

import (
	"fmt"
	"sort"
	"time"

	"placement-scheduler/pkg/generator"
	"placement-scheduler/pkg/models"
	"placement-scheduler/pkg/scheduler"
)

// wavePriority returns the highest-priority company represented in the
// wave. Lower number = higher priority.
func wavePriority(data models.ShortlistData, wave scheduler.WaveSpec) int {
	priority := 999
	for _, companyID := range wave.CompanyIDs {
		company, exists := data.Companies[companyID]
		if !exists {
			continue
		}
		rank := scheduler.PriorityRank(company.Tier)
		if rank < priority {
			priority = rank
		}
	}
	return priority
}

// buildWaves groups companies that share the exact same start/end
// window into one wave.
func buildWaves(data models.ShortlistData) []scheduler.WaveSpec {
	type waveKey struct {
		day   time.Time
		start time.Time
		end   time.Time
	}

	grouped := map[waveKey][]string{}
	for companyID, company := range data.Companies {
		if len(company.Slots) == 0 {
			continue
		}
		slot := company.Slots[0]
		key := waveKey{day: slot.Start.Truncate(24 * time.Hour), start: slot.Start, end: slot.End}
		grouped[key] = append(grouped[key], companyID)
	}

	waves := make([]scheduler.WaveSpec, 0, len(grouped))
	for key, companyIDs := range grouped {
		sort.Strings(companyIDs)
		waves = append(waves, scheduler.WaveSpec{Day: key.day, Start: key.start, End: key.end, CompanyIDs: companyIDs})
	}

	// Sort by day, then by priority when start times tie (so a
	// higher-priority overlapping wave — e.g. Day 3 niche vs mid, both
	// starting 9:00 — books its students FIRST, correctly winning
	// contested availability), then by start time.
	sort.Slice(waves, func(i, j int) bool {
		if !waves[i].Day.Equal(waves[j].Day) {
			return waves[i].Day.Before(waves[j].Day)
		}
		if waves[i].Start.Equal(waves[j].Start) {
			pi := wavePriority(data, waves[i])
			pj := wavePriority(data, waves[j])
			if pi != pj {
				return pi < pj
			}
			return waves[i].End.Before(waves[j].End)
		}
		return waves[i].Start.Before(waves[j].Start)
	})

	return waves
}

func main() {
	data := generator.Generate(generator.DefaultConfig())
	waves := buildWaves(data)

	// Shared across ALL waves so a student or room can never be
	// double-booked, even by waves that overlap in real clock time but
	// aren't the exact same WaveSpec (e.g. Day 3's mid vs niche windows).
	busyUntil := make(map[string]time.Time)
	roomBusyUntil := make(map[string]time.Time)

	var allInterviews []models.Interview

	fmt.Println("========================================")
	fmt.Println("PLACEMENT SCHEDULER TEST")
	fmt.Println("========================================")
	fmt.Println("\nWaves found:")

	for _, wave := range waves {
		priority := wavePriority(data, wave)
		fmt.Printf("  %s %s-%s | companies=%d | priority=%d\n",
			wave.Day.Format("Jan 2"), wave.Start.Format("15:04"), wave.End.Format("15:04"), len(wave.CompanyIDs), priority)

		result := scheduler.RunWave(data, wave, len(data.Rooms), busyUntil, roomBusyUntil)
		allInterviews = append(allInterviews, result...)
	}

	scheduled, unscheduled := 0, 0
	reasons := map[string]int{}
	for _, iv := range allInterviews {
		if iv.Status == models.StatusScheduled {
			scheduled++
		} else {
			unscheduled++
			reasons[iv.UnscheduledReason]++
		}
	}

	fmt.Println("\n========================================")
	fmt.Println("RESULTS")
	fmt.Println("========================================")
	fmt.Printf("Total shortlist entries : %d\n", len(data.Shortlists))
	fmt.Printf("Total interview records : %d\n", len(allInterviews))
	fmt.Printf("Scheduled               : %d\n", scheduled)
	fmt.Printf("Unscheduled             : %d\n", unscheduled)
	fmt.Println("\nUnscheduled reasons:")
	for reason, count := range reasons {
		fmt.Printf("  %-40s %d\n", reason, count)
	}

	// ---- Student double-booking check ----
	fmt.Println("\n========================================")
	fmt.Println("STUDENT DOUBLE-BOOKING CHECK")
	fmt.Println("========================================")
	byStudent := map[string][]models.Interview{}
	for _, iv := range allInterviews {
		if iv.Status == models.StatusScheduled {
			byStudent[iv.StudentID] = append(byStudent[iv.StudentID], iv)
		}
	}
	studentOverlaps := 0
	for studentID, ivs := range byStudent {
		sort.Slice(ivs, func(i, j int) bool { return ivs[i].StartTime.Before(ivs[j].StartTime) })
		for i := 1; i < len(ivs); i++ {
			prevEnd := ivs[i-1].StartTime.Add(ivs[i-1].Duration)
			if ivs[i].StartTime.Before(prevEnd) {
				studentOverlaps++
				fmt.Printf("❌ Student %s: %s overlaps %s\n", studentID, ivs[i-1].CompanyID, ivs[i].CompanyID)
			}
		}
	}
	if studentOverlaps == 0 {
		fmt.Println("✅ Student double-booking check PASSED")
	} else {
		fmt.Printf("❌ Student double-booking check FAILED: %d overlaps\n", studentOverlaps)
	}

	// ---- Room double-booking check ----
	fmt.Println("\n========================================")
	fmt.Println("ROOM DOUBLE-BOOKING CHECK")
	fmt.Println("========================================")
	byRoom := map[string][]models.Interview{}
	for _, iv := range allInterviews {
		if iv.Status == models.StatusScheduled {
			byRoom[iv.RoomID] = append(byRoom[iv.RoomID], iv)
		}
	}
	roomOverlaps := 0
	for roomID, ivs := range byRoom {
		sort.Slice(ivs, func(i, j int) bool { return ivs[i].StartTime.Before(ivs[j].StartTime) })
		for i := 1; i < len(ivs); i++ {
			prevEnd := ivs[i-1].StartTime.Add(ivs[i-1].Duration)
			if ivs[i].StartTime.Before(prevEnd) {
				roomOverlaps++
				fmt.Printf("❌ Room %s: %s overlaps %s\n", roomID, ivs[i-1].CompanyID, ivs[i].CompanyID)
			}
		}
	}
	if roomOverlaps == 0 {
		fmt.Println("✅ Room double-booking check PASSED")
	} else {
		fmt.Printf("❌ Room double-booking check FAILED: %d overlaps\n", roomOverlaps)
	}

	// ---- NEW: panel room-stickiness check ----
	// A single panel must use the SAME room for every interview it runs
	// in a day — this is the bug we just fixed. If this ever fails, the
	// stickiness logic in RunWave has regressed.
	fmt.Println("\n========================================")
	fmt.Println("PANEL ROOM-STICKINESS CHECK")
	fmt.Println("========================================")
	roomsByPanel := map[string]map[string]bool{}
	for _, iv := range allInterviews {
		if iv.Status != models.StatusScheduled {
			continue
		}
		if roomsByPanel[iv.PanelID] == nil {
			roomsByPanel[iv.PanelID] = map[string]bool{}
		}
		roomsByPanel[iv.PanelID][iv.RoomID] = true
	}
	stickinessFailures := 0
	panelIDs := make([]string, 0, len(roomsByPanel))
	for pid := range roomsByPanel {
		panelIDs = append(panelIDs, pid)
	}
	sort.Strings(panelIDs)
	for _, pid := range panelIDs {
		rooms := roomsByPanel[pid]
		if len(rooms) > 1 {
			stickinessFailures++
			roomList := make([]string, 0, len(rooms))
			for r := range rooms {
				roomList = append(roomList, r)
			}
			sort.Strings(roomList)
			fmt.Printf("❌ Panel %s used multiple rooms: %v\n", pid, roomList)
		}
	}
	if stickinessFailures == 0 {
		fmt.Println("✅ Panel room-stickiness check PASSED")
	} else {
		fmt.Printf("❌ Panel room-stickiness check FAILED: %d panels used >1 room\n", stickinessFailures)
	}

	// ---- Per-company results ----
	type companyStat struct {
		id          string
		scheduled   int
		unscheduled int
	}
	statsByCompany := map[string]*companyStat{}
	for _, iv := range allInterviews {
		s, exists := statsByCompany[iv.CompanyID]
		if !exists {
			s = &companyStat{id: iv.CompanyID}
			statsByCompany[iv.CompanyID] = s
		}
		if iv.Status == models.StatusScheduled {
			s.scheduled++
		} else {
			s.unscheduled++
		}
	}
	stats := make([]*companyStat, 0, len(statsByCompany))
	for _, s := range statsByCompany {
		stats = append(stats, s)
	}
	sort.Slice(stats, func(i, j int) bool {
		ti, tj := stats[i].scheduled+stats[i].unscheduled, stats[j].scheduled+stats[j].unscheduled
		ri, rj := 0.0, 0.0
		if ti > 0 {
			ri = float64(stats[i].scheduled) / float64(ti)
		}
		if tj > 0 {
			rj = float64(stats[j].scheduled) / float64(tj)
		}
		return ri < rj
	})

	fmt.Println("\n========================================")
	fmt.Println("PER-COMPANY RESULTS")
	fmt.Println("========================================")
	for _, s := range stats {
		total := s.scheduled + s.unscheduled
		pct := 0.0
		if total > 0 {
			pct = float64(s.scheduled) / float64(total) * 100
		}
		fmt.Printf("%-12s scheduled=%-4d unscheduled=%-4d (%5.1f%% scheduled)\n", s.id, s.scheduled, s.unscheduled, pct)
	}
}
