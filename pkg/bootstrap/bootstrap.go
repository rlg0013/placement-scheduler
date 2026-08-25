package bootstrap

import (
	"sort"
	"time"

	"placement-scheduler/pkg/api"
	"placement-scheduler/pkg/generator"
	"placement-scheduler/pkg/models"
	"placement-scheduler/pkg/scheduler"
)

func NewServer() *api.Server {
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

	return api.NewServer(data, state, lastInterviewEnd(state.Interviews))
}

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
