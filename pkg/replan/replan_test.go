package replan

import (
	"testing"
	"time"

	"placement-scheduler/pkg/generator"
	"placement-scheduler/pkg/models"
	"placement-scheduler/pkg/scheduler"
)

func TestReplansMaintainInvariantsAndScopedDisruptions(t *testing.T) {
	data := generator.Generate(generator.DefaultConfig())
	state := runGeneratedSchedule(data)
	assertScheduleInvariants(t, state.Interviews)

	student, studentAt := firstScheduled(t, state.Interviews)
	panel, panelAt := firstField(t, state.Interviews, func(iv models.Interview) (string, time.Time) {
		return iv.PanelID, iv.StartTime
	})
	company, companyAt := firstField(t, state.Interviews, func(iv models.Interview) (string, time.Time) {
		return iv.CompanyID, iv.StartTime
	})
	room, roomAt := firstField(t, state.Interviews, func(iv models.Interview) (string, time.Time) {
		return iv.RoomID, iv.StartTime
	})
	dayEnd := lastInterviewEnd(state.Interviews)

	next, diff, err := ReplanStudentDropout(state, StudentDropoutDisruption{StudentID: student, At: studentAt})
	if err != nil {
		t.Fatalf("student dropout failed: %v", err)
	}
	assertScheduleInvariants(t, next.Interviews)
	assertDiffMatchesReality(t, next.Interviews, diff)

	next, diff, err = ReplanPanelDropout(data, state, PanelDropoutDisruption{PanelID: panel, At: panelAt}, dayEnd)
	if err != nil {
		t.Fatalf("panel dropout failed: %v", err)
	}
	assertScheduleInvariants(t, next.Interviews)
	assertDiffMatchesReality(t, next.Interviews, diff)
	for _, iv := range next.Interviews {
		if iv.PanelID == panel && iv.Status == models.StatusScheduled && !iv.StartTime.Before(panelAt) {
			t.Fatalf("dropped panel %s still has scheduled interview %s", panel, iv.ID)
		}
	}

	next, diff, err = ReplanLateCompany(data, state, LateCompanyDisruption{CompanyID: company, Delay: 2 * time.Hour, At: companyAt}, dayEnd)
	if err != nil {
		t.Fatalf("late company failed: %v", err)
	}
	assertScheduleInvariants(t, next.Interviews)
	assertDiffMatchesReality(t, next.Interviews, diff)

	next, diff, err = ReplanRoomUnavailable(data, state, RoomUnavailableDisruption{RoomID: room, At: roomAt}, dayEnd)
	if err != nil {
		t.Fatalf("room unavailable failed: %v", err)
	}
	assertScheduleInvariants(t, next.Interviews)
	assertDiffMatchesReality(t, next.Interviews, diff)
	for _, iv := range next.Interviews {
		if iv.RoomID == room && iv.Status == models.StatusScheduled && !iv.StartTime.Before(roomAt) {
			t.Fatalf("unavailable room %s still has scheduled interview %s", room, iv.ID)
		}
	}
}

func runGeneratedSchedule(data models.ShortlistData) *scheduler.ScheduleState {
	state := &scheduler.ScheduleState{
		Interviews:    []models.Interview{},
		BusyUntil:     map[string]time.Time{},
		RoomBusyUntil: map[string]time.Time{},
	}
	for _, wave := range schedulerTestWaves(data) {
		state.Interviews = append(
			state.Interviews,
			scheduler.RunWave(data, wave, len(data.Rooms), state.BusyUntil, state.RoomBusyUntil)...,
		)
	}
	return state
}

func schedulerTestWaves(data models.ShortlistData) []scheduler.WaveSpec {
	// Reuse the production command's deterministic wave rules through the
	// scheduler package tests by keeping this smoke schedule intentionally
	// simple: companies only have one generated slot and RunWave remains the
	// invariant boundary being exercised.
	return buildMinimalSortedWaves(data)
}

func buildMinimalSortedWaves(data models.ShortlistData) []scheduler.WaveSpec {
	type waveKey struct{ day, start, end time.Time }
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
		waves = append(waves, scheduler.WaveSpec{Day: key.day, Start: key.start, End: key.end, CompanyIDs: companyIDs})
	}
	sortWaves(data, waves)
	return waves
}

func sortWaves(data models.ShortlistData, waves []scheduler.WaveSpec) {
	for i := 0; i < len(waves)-1; i++ {
		for j := i + 1; j < len(waves); j++ {
			if waveAfter(data, waves[i], waves[j]) {
				waves[i], waves[j] = waves[j], waves[i]
			}
		}
	}
}

func waveAfter(data models.ShortlistData, a, b scheduler.WaveSpec) bool {
	if !a.Day.Equal(b.Day) {
		return b.Day.Before(a.Day)
	}
	if a.Start.Equal(b.Start) {
		pa, pb := wavePriority(data, a), wavePriority(data, b)
		if pa != pb {
			return pb < pa
		}
		return b.End.Before(a.End)
	}
	return b.Start.Before(a.Start)
}

func wavePriority(data models.ShortlistData, wave scheduler.WaveSpec) int {
	priority := 999
	for _, companyID := range wave.CompanyIDs {
		if company, exists := data.Companies[companyID]; exists {
			if rank := scheduler.PriorityRank(company.Tier); rank < priority {
				priority = rank
			}
		}
	}
	return priority
}

func assertScheduleInvariants(t *testing.T, interviews []models.Interview) {
	t.Helper()
	assertNoOverlap(t, interviews, func(iv models.Interview) string { return iv.StudentID }, "student")
	assertNoOverlap(t, interviews, func(iv models.Interview) string { return iv.RoomID }, "room")
	roomsByPanel := map[string]string{}
	for _, iv := range interviews {
		if iv.Status != models.StatusScheduled {
			continue
		}
		if room, exists := roomsByPanel[iv.PanelID]; exists && room != iv.RoomID {
			t.Fatalf("panel %s moved rooms: %s then %s", iv.PanelID, room, iv.RoomID)
		}
		roomsByPanel[iv.PanelID] = iv.RoomID
	}
}

func assertNoOverlap(t *testing.T, interviews []models.Interview, key func(models.Interview) string, label string) {
	t.Helper()
	for i, a := range interviews {
		if a.Status != models.StatusScheduled {
			continue
		}
		for _, b := range interviews[i+1:] {
			if b.Status != models.StatusScheduled || key(a) != key(b) {
				continue
			}
			if a.StartTime.Before(b.StartTime.Add(b.Duration)) && b.StartTime.Before(a.StartTime.Add(a.Duration)) {
				t.Fatalf("%s %s overlap: %s and %s", label, key(a), a.ID, b.ID)
			}
		}
	}
}

func assertDiffMatchesReality(t *testing.T, interviews []models.Interview, diff *Diff) {
	t.Helper()
	for _, change := range diff.Changes {
		if change.StudentID == "" || change.After.IsEmpty() {
			continue
		}
		found := false
		for _, iv := range interviews {
			if iv.Status == models.StatusScheduled &&
				iv.StudentID == change.StudentID &&
				iv.PanelID == change.After.PanelID &&
				iv.RoomID == change.After.RoomID &&
				iv.StartTime.Equal(change.After.At) {
				found = true
				break
			}
		}
		if !found {
			t.Fatalf("diff claims %s moved to %+v, but schedule has no matching interview", change.StudentID, change.After)
		}
	}
}

func firstScheduled(t *testing.T, interviews []models.Interview) (string, time.Time) {
	t.Helper()
	return firstField(t, interviews, func(iv models.Interview) (string, time.Time) {
		return iv.StudentID, iv.StartTime
	})
}

func firstField(t *testing.T, interviews []models.Interview, extract func(models.Interview) (string, time.Time)) (string, time.Time) {
	t.Helper()
	for _, iv := range interviews {
		if iv.Status == models.StatusScheduled {
			return extract(iv)
		}
	}
	t.Fatal("no scheduled interviews found")
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
	return latest.Add(1)
}
