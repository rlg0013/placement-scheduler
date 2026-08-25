package scheduler

import (
	"sort"
	"testing"
	"time"

	"placement-scheduler/pkg/generator"
	"placement-scheduler/pkg/models"
)

func TestGeneratedScheduleMaintainsCoreInvariants(t *testing.T) {
	data := generator.Generate(generator.DefaultConfig())
	state := runGeneratedSchedule(data)

	if len(state.Interviews) != len(data.Shortlists) {
		t.Fatalf("got %d interview records, want one per shortlist entry %d", len(state.Interviews), len(data.Shortlists))
	}

	assertNoStudentDoubleBooking(t, state.Interviews)
	assertNoRoomDoubleBooking(t, state.Interviews)
	assertPanelRoomStickiness(t, state.Interviews)
	assertUnscheduledRowsExplainThemselves(t, state.Interviews)
}

func runGeneratedSchedule(data models.ShortlistData) *ScheduleState {
	state := &ScheduleState{
		Interviews:    []models.Interview{},
		BusyUntil:     map[string]time.Time{},
		RoomBusyUntil: map[string]time.Time{},
	}
	for _, wave := range buildWaves(data) {
		state.Interviews = append(
			state.Interviews,
			RunWave(data, wave, len(data.Rooms), state.BusyUntil, state.RoomBusyUntil)...,
		)
	}
	return state
}

func buildWaves(data models.ShortlistData) []WaveSpec {
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

	waves := make([]WaveSpec, 0, len(grouped))
	for key, companyIDs := range grouped {
		sort.Strings(companyIDs)
		waves = append(waves, WaveSpec{Day: key.day, Start: key.start, End: key.end, CompanyIDs: companyIDs})
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

func wavePriority(data models.ShortlistData, wave WaveSpec) int {
	priority := 999
	for _, companyID := range wave.CompanyIDs {
		company, exists := data.Companies[companyID]
		if !exists {
			continue
		}
		if rank := PriorityRank(company.Tier); rank < priority {
			priority = rank
		}
	}
	return priority
}

func assertNoStudentDoubleBooking(t *testing.T, interviews []models.Interview) {
	t.Helper()
	byStudent := map[string][]models.Interview{}
	for _, iv := range interviews {
		if iv.Status == models.StatusScheduled {
			byStudent[iv.StudentID] = append(byStudent[iv.StudentID], iv)
		}
	}
	for studentID, ivs := range byStudent {
		sort.Slice(ivs, func(i, j int) bool { return ivs[i].StartTime.Before(ivs[j].StartTime) })
		for i := 1; i < len(ivs); i++ {
			prev := ivs[i-1]
			if ivs[i].StartTime.Before(prev.StartTime.Add(prev.Duration)) {
				t.Fatalf("student %s double-booked: %s overlaps %s", studentID, prev.ID, ivs[i].ID)
			}
		}
	}
}

func assertNoRoomDoubleBooking(t *testing.T, interviews []models.Interview) {
	t.Helper()
	byRoom := map[string][]models.Interview{}
	for _, iv := range interviews {
		if iv.Status == models.StatusScheduled {
			byRoom[iv.RoomID] = append(byRoom[iv.RoomID], iv)
		}
	}
	for roomID, ivs := range byRoom {
		sort.Slice(ivs, func(i, j int) bool { return ivs[i].StartTime.Before(ivs[j].StartTime) })
		for i := 1; i < len(ivs); i++ {
			prev := ivs[i-1]
			if ivs[i].StartTime.Before(prev.StartTime.Add(prev.Duration)) {
				t.Fatalf("room %s double-booked: %s overlaps %s", roomID, prev.ID, ivs[i].ID)
			}
		}
	}
}

func assertPanelRoomStickiness(t *testing.T, interviews []models.Interview) {
	t.Helper()
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

func assertUnscheduledRowsExplainThemselves(t *testing.T, interviews []models.Interview) {
	t.Helper()
	for _, iv := range interviews {
		if iv.Status == models.StatusUnscheduled && iv.UnscheduledReason == "" {
			t.Fatalf("unscheduled row %s has no reason", iv.ID)
		}
	}
}
