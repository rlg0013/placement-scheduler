package main

import (
	"fmt"
	"sort"
	"time"

	"placement-scheduler/internal/generator"
	"placement-scheduler/internal/models"
	"placement-scheduler/internal/scheduler"
)

// wavePriority returns the highest-priority company represented
// in the wave.
//
// Lower number = higher priority:
//
// Niche         = 0
// Mid-Tier      = 1
// Mass Recruiter = 2
func wavePriority(
	data models.ShortlistData,
	wave scheduler.WaveSpec,
) int {

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

// buildWaves groups companies that have the exact same
// start/end window.
func buildWaves(
	data models.ShortlistData,
) []scheduler.WaveSpec {

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

		key := waveKey{
			day:   slot.Start.Truncate(24 * time.Hour),
			start: slot.Start,
			end:   slot.End,
		}

		grouped[key] = append(
			grouped[key],
			companyID,
		)
	}

	waves := make(
		[]scheduler.WaveSpec,
		0,
		len(grouped),
	)

	for key, companyIDs := range grouped {

		sort.Strings(companyIDs)

		waves = append(
			waves,
			scheduler.WaveSpec{
				Day:        key.day,
				Start:      key.start,
				End:        key.end,
				CompanyIDs: companyIDs,
			},
		)
	}

	// Sort by:
	// 1. Day
	// 2. Higher priority if same start time
	// 3. Start time
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

			return waves[i].End.Before(
				waves[j].End,
			)
		}

		return waves[i].Start.Before(
			waves[j].Start,
		)
	})

	return waves
}

func main() {

	// ------------------------------------------------------------
	// Generate test data
	// ------------------------------------------------------------

	data := generator.Generate(
		generator.DefaultConfig(),
	)

	// ------------------------------------------------------------
	// Build waves
	// ------------------------------------------------------------

	waves := buildWaves(data)

	// ------------------------------------------------------------
	// IMPORTANT:
	//
	// These maps MUST exist outside the wave loop.
	//
	// They are shared between all waves so that:
	//
	// 1. A student cannot be booked twice.
	// 2. A room cannot be booked twice.
	// ------------------------------------------------------------

	busyUntil := make(
		map[string]time.Time,
	)

	roomBusyUntil := make(
		map[string]time.Time,
	)

	var allInterviews []models.Interview

	// ------------------------------------------------------------
	// Run every wave
	// ------------------------------------------------------------

	fmt.Println("========================================")
	fmt.Println("PLACEMENT SCHEDULER TEST")
	fmt.Println("========================================")

	fmt.Println("\nWaves found:")

	for _, wave := range waves {

		priority := wavePriority(
			data,
			wave,
		)

		fmt.Printf(
			"  %s %s-%s | companies=%d | priority=%d\n",
			wave.Day.Format("Jan 2"),
			wave.Start.Format("15:04"),
			wave.End.Format("15:04"),
			len(wave.CompanyIDs),
			priority,
		)

		result := scheduler.RunWave(
			data,
			wave,
			len(data.Rooms),

			// Shared student state.
			busyUntil,

			// Shared room state.
			roomBusyUntil,
		)

		allInterviews = append(
			allInterviews,
			result...,
		)
	}

	// ------------------------------------------------------------
	// Overall statistics
	// ------------------------------------------------------------

	scheduled := 0
	unscheduled := 0

	reasons := map[string]int{}

	for _, interview := range allInterviews {

		if interview.Status ==
			models.StatusScheduled {

			scheduled++

		} else {

			unscheduled++

			reasons[interview.UnscheduledReason]++
		}
	}

	fmt.Println("\n========================================")
	fmt.Println("RESULTS")
	fmt.Println("========================================")

	fmt.Printf(
		"Total shortlist entries : %d\n",
		len(data.Shortlists),
	)

	fmt.Printf(
		"Total interview records : %d\n",
		len(allInterviews),
	)

	fmt.Printf(
		"Scheduled               : %d\n",
		scheduled,
	)

	fmt.Printf(
		"Unscheduled             : %d\n",
		unscheduled,
	)

	fmt.Println("\nUnscheduled reasons:")

	for reason, count := range reasons {

		fmt.Printf(
			"  %-40s %d\n",
			reason,
			count,
		)
	}

	// ------------------------------------------------------------
	// STUDENT DOUBLE-BOOKING CHECK
	// ------------------------------------------------------------

	fmt.Println("\n========================================")
	fmt.Println("STUDENT DOUBLE-BOOKING CHECK")
	fmt.Println("========================================")

	byStudent :=
		map[string][]models.Interview{}

	for _, interview := range allInterviews {

		if interview.Status !=
			models.StatusScheduled {
			continue
		}

		byStudent[interview.StudentID] = append(
			byStudent[interview.StudentID],
			interview,
		)
	}

	studentOverlaps := 0

	for studentID, interviews := range byStudent {

		sort.Slice(
			interviews,
			func(i, j int) bool {
				return interviews[i].StartTime.Before(
					interviews[j].StartTime,
				)
			},
		)

		for i := 1; i < len(interviews); i++ {

			previous :=
				interviews[i-1]

			current :=
				interviews[i]

			previousEnd :=
				previous.StartTime.Add(
					previous.Duration,
				)

			if current.StartTime.Before(
				previousEnd,
			) {

				studentOverlaps++

				fmt.Printf(
					"❌ Student %s: %s overlaps %s\n",
					studentID,
					previous.CompanyID,
					current.CompanyID,
				)
			}
		}
	}

	if studentOverlaps == 0 {

		fmt.Println(
			"✅ Student double-booking check PASSED",
		)

	} else {

		fmt.Printf(
			"❌ Student double-booking check FAILED: %d overlaps\n",
			studentOverlaps,
		)
	}

	// ------------------------------------------------------------
	// ROOM DOUBLE-BOOKING CHECK
	// ------------------------------------------------------------

	fmt.Println("\n========================================")
	fmt.Println("ROOM DOUBLE-BOOKING CHECK")
	fmt.Println("========================================")

	byRoom :=
		map[string][]models.Interview{}

	for _, interview := range allInterviews {

		if interview.Status !=
			models.StatusScheduled {
			continue
		}

		byRoom[interview.RoomID] = append(
			byRoom[interview.RoomID],
			interview,
		)
	}

	roomOverlaps := 0

	for roomID, interviews := range byRoom {

		sort.Slice(
			interviews,
			func(i, j int) bool {
				return interviews[i].StartTime.Before(
					interviews[j].StartTime,
				)
			},
		)

		for i := 1; i < len(interviews); i++ {

			previous :=
				interviews[i-1]

			current :=
				interviews[i]

			previousEnd :=
				previous.StartTime.Add(
					previous.Duration,
				)

			if current.StartTime.Before(
				previousEnd,
			) {

				roomOverlaps++

				fmt.Printf(
					"❌ Room %s: %s overlaps %s\n",
					roomID,
					previous.CompanyID,
					current.CompanyID,
				)
			}
		}
	}

	if roomOverlaps == 0 {

		fmt.Println(
			"✅ Room double-booking check PASSED",
		)

	} else {

		fmt.Printf(
			"❌ Room double-booking check FAILED: %d overlaps\n",
			roomOverlaps,
		)
	}

	// ------------------------------------------------------------
	// PER-COMPANY RESULTS
	// ------------------------------------------------------------

	type companyStat struct {
		id          string
		scheduled   int
		unscheduled int
	}

	statsByCompany :=
		map[string]*companyStat{}

	for _, interview := range allInterviews {

		stat, exists :=
			statsByCompany[interview.CompanyID]

		if !exists {

			stat = &companyStat{
				id: interview.CompanyID,
			}

			statsByCompany[interview.CompanyID] = stat
		}

		if interview.Status ==
			models.StatusScheduled {

			stat.scheduled++

		} else {

			stat.unscheduled++
		}
	}

	stats :=
		make(
			[]*companyStat,
			0,
			len(statsByCompany),
		)

	for _, stat := range statsByCompany {

		stats = append(
			stats,
			stat,
		)
	}

	sort.Slice(
		stats,
		func(i, j int) bool {

			totalI :=
				stats[i].scheduled +
					stats[i].unscheduled

			totalJ :=
				stats[j].scheduled +
					stats[j].unscheduled

			rateI := 0.0
			rateJ := 0.0

			if totalI > 0 {
				rateI =
					float64(stats[i].scheduled) /
						float64(totalI)
			}

			if totalJ > 0 {
				rateJ =
					float64(stats[j].scheduled) /
						float64(totalJ)
			}

			return rateI < rateJ
		},
	)

	fmt.Println("\n========================================")
	fmt.Println("PER-COMPANY RESULTS")
	fmt.Println("========================================")

	for _, stat := range stats {

		total :=
			stat.scheduled +
				stat.unscheduled

		percentage := 0.0

		if total > 0 {
			percentage =
				float64(stat.scheduled) /
					float64(total) *
					100
		}

		fmt.Printf(
			"%-12s scheduled=%-4d unscheduled=%-4d (%5.1f%% scheduled)\n",
			stat.id,
			stat.scheduled,
			stat.unscheduled,
			percentage,
		)
	}
}
