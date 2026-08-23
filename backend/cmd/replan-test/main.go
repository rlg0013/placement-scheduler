package main

import (
	"fmt"
	"os"
	"sort"
	"time"

	"placement-scheduler/internal/generator"
	"placement-scheduler/internal/models"
	"placement-scheduler/internal/replan"
	"placement-scheduler/internal/scheduler"
)

// ---------- duplicated from cmd/scheduler-test/main.go (package main
// can't import another package main) ----------

func wavePriority(data models.ShortlistData, wave scheduler.WaveSpec) int {
	priority := 999
	for _, companyID := range wave.CompanyIDs {
		company, exists := data.Companies[companyID]
		if !exists {
			continue
		}
		if rank := scheduler.PriorityRank(company.Tier); rank < priority {
			priority = rank
		}
	}
	return priority
}

func buildWaves(data models.ShortlistData) []scheduler.WaveSpec {
	type waveKey struct{ day, start, end time.Time }
	grouped := map[waveKey][]string{}
	for companyID, company := range data.Companies {
		if len(company.Slots) == 0 {
			continue
		}
		slot := company.Slots[0]
		key := waveKey{slot.Start.Truncate(24 * time.Hour), slot.Start, slot.End}
		grouped[key] = append(grouped[key], companyID)
	}
	waves := make([]scheduler.WaveSpec, 0, len(grouped))
	for key, companyIDs := range grouped {
		sort.Strings(companyIDs)
		waves = append(waves, scheduler.WaveSpec{Day: key.day, Start: key.start, End: key.end, CompanyIDs: companyIDs})
	}
	sort.Slice(waves, func(i, j int) bool {
		if !waves[i].Day.Equal(waves[j].Day) {
			return waves[i].Day.Before(waves[j].Day)
		}
		if waves[i].Start.Equal(waves[j].Start) {
			pi, pj := wavePriority(data, waves[i]), wavePriority(data, waves[j])
			if pi != pj {
				return pi < pj
			}
			return waves[i].End.Before(waves[j].End)
		}
		return waves[i].Start.Before(waves[j].Start)
	})
	return waves
}

// ---------- replan test ----------

func main() {
	data := generator.Generate(generator.DefaultConfig())
	waves := buildWaves(data)

	state := &scheduler.ScheduleState{
		Interviews:    []models.Interview{},
		BusyUntil:     map[string]time.Time{},
		RoomBusyUntil: map[string]time.Time{},
	}
	for _, w := range waves {
		result := scheduler.RunWave(data, w, len(data.Rooms), state.BusyUntil, state.RoomBusyUntil)
		state.Interviews = append(state.Interviews, result...)
	}

	fmt.Printf("Baseline schedule: %d interview records\n", len(state.Interviews))
	if !runInvariants("baseline", state.Interviews) {
		os.Exit(1)
	}

	sampleStudent, sampleTime, ok := firstScheduled(state.Interviews)
	if !ok {
		fmt.Println("no scheduled interviews found — cannot test replan")
		os.Exit(1)
	}
	samplePanel, panelTime := sampleFieldFor(state.Interviews, func(iv models.Interview) (string, time.Time) { return iv.PanelID, iv.StartTime })
	sampleCompany, companyTime := sampleFieldFor(state.Interviews, func(iv models.Interview) (string, time.Time) { return iv.CompanyID, iv.StartTime })
	sampleRoom, roomTime := sampleFieldFor(state.Interviews, func(iv models.Interview) (string, time.Time) { return iv.RoomID, iv.StartTime })

	dayEnd := lastInterviewEnd(state.Interviews)

	studentDisrupt := replan.StudentDropoutDisruption{StudentID: sampleStudent, At: sampleTime}
	panelDisrupt := replan.PanelDropoutDisruption{PanelID: samplePanel, At: panelTime}
	lateDisrupt := replan.LateCompanyDisruption{CompanyID: sampleCompany, Delay: 2 * time.Hour, At: companyTime}
	roomDisrupt := replan.RoomUnavailableDisruption{RoomID: sampleRoom, At: roomTime}

	if next, diff, err := replan.ReplanStudentDropout(state, studentDisrupt); err != nil {
		fmt.Println("StudentDropout error:", err)
		os.Exit(1)
	} else {
		fmt.Printf("\n[StudentDropout] %d change(s)\n", len(diff.Changes))
		runInvariants("after StudentDropout", next.Interviews)
		checkDiffMatchesReality(next.Interviews, diff)
	}

	if next, diff, err := replan.ReplanPanelDropout(data, state, panelDisrupt, dayEnd); err != nil {
		fmt.Println("PanelDropout error:", err)
		os.Exit(1)
	} else {
		fmt.Printf("\n[PanelDropout] %d change(s)\n", len(diff.Changes))
		runInvariants("after PanelDropout", next.Interviews)
		checkDiffMatchesReality(next.Interviews, diff)
		checkNoInterviewsOnDroppedPanel(next.Interviews, panelDisrupt.PanelID, panelDisrupt.At)
	}

	if next, diff, err := replan.ReplanLateCompany(data, state, lateDisrupt, dayEnd); err != nil {
		fmt.Println("LateCompany error:", err)
		os.Exit(1)
	} else {
		fmt.Printf("\n[LateCompany] %d change(s)\n", len(diff.Changes))
		runInvariants("after LateCompany", next.Interviews)
		checkDiffMatchesReality(next.Interviews, diff)
	}

	if next, diff, err := replan.ReplanRoomUnavailable(data, state, roomDisrupt, dayEnd); err != nil {
		fmt.Println("RoomUnavailable error:", err)
		os.Exit(1)
	} else {
		fmt.Printf("\n[RoomUnavailable] %d change(s)\n", len(diff.Changes))
		runInvariants("after RoomUnavailable", next.Interviews)
		checkDiffMatchesReality(next.Interviews, diff)
		checkNoInterviewsInDroppedRoom(next.Interviews, roomDisrupt.RoomID, roomDisrupt.At)
	}

	fmt.Println("\nAll replan checks passed.")
}

// ---------- invariants (same 3 as Day 2, plus diff-honesty check) ----------

func runInvariants(label string, interviews []models.Interview) bool {
	ok := true
	if !checkNoStudentDoubleBooking(interviews) {
		fmt.Printf("[%s] FAILED: student double-booking\n", label)
		ok = false
	}
	if !checkNoRoomDoubleBooking(interviews) {
		fmt.Printf("[%s] FAILED: room double-booking\n", label)
		ok = false
	}
	if !checkPanelRoomStickiness(interviews) {
		fmt.Printf("[%s] FAILED: panel room stickiness\n", label)
		ok = false
	}
	if ok {
		fmt.Printf("[%s] OK — all invariants hold\n", label)
	}
	return ok
}

func checkNoStudentDoubleBooking(interviews []models.Interview) bool {
	byStudent := map[string][]models.Interview{}
	for _, iv := range interviews {
		if iv.Status == models.StatusScheduled {
			byStudent[iv.StudentID] = append(byStudent[iv.StudentID], iv)
		}
	}
	ok := true
	for student, list := range byStudent {
		for i := 0; i < len(list); i++ {
			for j := i + 1; j < len(list); j++ {
				if overlaps(list[i], list[j]) {
					fmt.Printf("  student %s double-booked: %s vs %s\n", student, list[i].ID, list[j].ID)
					ok = false
				}
			}
		}
	}
	return ok
}

func checkNoRoomDoubleBooking(interviews []models.Interview) bool {
	byRoom := map[string][]models.Interview{}
	for _, iv := range interviews {
		if iv.Status == models.StatusScheduled {
			byRoom[iv.RoomID] = append(byRoom[iv.RoomID], iv)
		}
	}
	ok := true
	for room, list := range byRoom {
		for i := 0; i < len(list); i++ {
			for j := i + 1; j < len(list); j++ {
				if overlaps(list[i], list[j]) {
					fmt.Printf("  room %s double-booked: %s vs %s\n", room, list[i].ID, list[j].ID)
					ok = false
				}
			}
		}
	}
	return ok
}

func checkPanelRoomStickiness(interviews []models.Interview) bool {
	roomsByPanel := map[string]map[string]bool{}
	for _, iv := range interviews {
		if iv.Status != models.StatusScheduled {
			continue
		}
		if roomsByPanel[iv.PanelID] == nil {
			roomsByPanel[iv.PanelID] = map[string]bool{}
		}
		roomsByPanel[iv.PanelID][iv.RoomID] = true
	}
	ok := true
	for pid, rooms := range roomsByPanel {
		if len(rooms) > 1 {
			fmt.Printf("  panel %s used multiple rooms\n", pid)
			ok = false
		}
	}
	return ok
}

func checkDiffMatchesReality(interviews []models.Interview, diff *replan.Diff) bool {
	ok := true
	for _, c := range diff.Changes {
		if c.StudentID == "" || c.After.IsEmpty() {
			continue
		}
		found := false
		for _, iv := range interviews {
			if iv.StudentID == c.StudentID && iv.PanelID == c.After.PanelID &&
				iv.RoomID == c.After.RoomID && iv.StartTime.Equal(c.After.At) &&
				iv.Status == models.StatusScheduled {
				found = true
				break
			}
		}
		if !found {
			fmt.Printf("  DIFF MISMATCH: claims %s -> %+v but no matching scheduled interview exists\n", c.StudentID, c.After)
			ok = false
		}
	}
	return ok
}

func checkNoInterviewsOnDroppedPanel(interviews []models.Interview, panelID string, at time.Time) bool {
	ok := true
	for _, iv := range interviews {
		if iv.PanelID == panelID && iv.Status == models.StatusScheduled && !iv.StartTime.Before(at) {
			fmt.Printf("  dropped panel %s still has a scheduled interview at %v\n", panelID, iv.StartTime)
			ok = false
		}
	}
	return ok
}

func checkNoInterviewsInDroppedRoom(interviews []models.Interview, roomID string, at time.Time) bool {
	ok := true
	for _, iv := range interviews {
		if iv.RoomID == roomID && iv.Status == models.StatusScheduled && !iv.StartTime.Before(at) {
			fmt.Printf("  dropped room %s still has a scheduled interview at %v\n", roomID, iv.StartTime)
			ok = false
		}
	}
	return ok
}

func overlaps(a, b models.Interview) bool {
	aEnd := a.StartTime.Add(a.Duration)
	bEnd := b.StartTime.Add(b.Duration)
	return a.StartTime.Before(bEnd) && b.StartTime.Before(aEnd)
}

// ---------- pick real disruption targets from the baseline ----------

func firstScheduled(interviews []models.Interview) (studentID string, at time.Time, ok bool) {
	for _, iv := range interviews {
		if iv.Status == models.StatusScheduled {
			return iv.StudentID, iv.StartTime, true
		}
	}
	return "", time.Time{}, false
}

func sampleFieldFor(interviews []models.Interview, extract func(models.Interview) (string, time.Time)) (string, time.Time) {
	for _, iv := range interviews {
		if iv.Status == models.StatusScheduled {
			return extract(iv)
		}
	}
	return "", time.Time{}
}

func lastInterviewEnd(interviews []models.Interview) time.Time {
	var latest time.Time
	for _, iv := range interviews {
		if iv.Status == models.StatusScheduled {
			if end := iv.StartTime.Add(iv.Duration); end.After(latest) {
				latest = end
			}
		}
	}
	return latest.Add(1) // nudge past the last end so dayEnd never excludes it
}
