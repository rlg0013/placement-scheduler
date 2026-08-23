package replan

import (
	"container/heap"
	"fmt"
	"sort"
	"time"

	"placement-scheduler/internal/models"
	"placement-scheduler/internal/scheduler"
)

// ---------- Disruption: sealed interface, one concrete type per kind ----------

type Disruption interface {
	isDisruption()
	EffectiveAt() time.Time
}

type StudentDropoutDisruption struct {
	StudentID string
	At        time.Time
}

func (StudentDropoutDisruption) isDisruption()            {}
func (d StudentDropoutDisruption) EffectiveAt() time.Time { return d.At }

type PanelDropoutDisruption struct {
	PanelID string
	At      time.Time
}

func (PanelDropoutDisruption) isDisruption()            {}
func (d PanelDropoutDisruption) EffectiveAt() time.Time { return d.At }

type LateCompanyDisruption struct {
	CompanyID string
	Delay     time.Duration
	At        time.Time
}

func (LateCompanyDisruption) isDisruption()            {}
func (d LateCompanyDisruption) EffectiveAt() time.Time { return d.At }

type RoomUnavailableDisruption struct {
	RoomID string
	At     time.Time
}

func (RoomUnavailableDisruption) isDisruption()            {}
func (d RoomUnavailableDisruption) EffectiveAt() time.Time { return d.At }

// ---------- Interview change model ----------

type InterviewSlot struct {
	PanelID string
	RoomID  string
	At      time.Time
}

func (s InterviewSlot) IsEmpty() bool {
	return s.PanelID == "" && s.RoomID == ""
}

type NotifyTarget struct {
	Kind    string // "student", "panel", or "coordinator"
	ID      string
	Message string
}

type ChangeEntry struct {
	StudentID string
	Before    InterviewSlot
	After     InterviewSlot
	Notify    []NotifyTarget
}

type Diff struct {
	Cause     Disruption
	Changes   []ChangeEntry
	Timestamp time.Time
}

func interviewToSlot(iv models.Interview) InterviewSlot {
	return InterviewSlot{PanelID: iv.PanelID, RoomID: iv.RoomID, At: iv.StartTime}
}

// ---------- Student dropout ----------

// hasConflict does a real interval-overlap check against the live
// interview list, unlike the busyUntil-based checks the other three
// disruption types use. This one specifically needs it because
// compaction can move a student's time EARLIER, and busyUntil (a single
// "busy until X" value) only ever moves forward for a student under
// normal scheduling — it was never designed to answer "is this earlier
// window also free."
func hasConflict(interviews []models.Interview, studentID string, start, end time.Time, excludeID string) bool {
	for _, iv := range interviews {
		if iv.StudentID != studentID || iv.Status != models.StatusScheduled || iv.ID == excludeID {
			continue
		}
		ivEnd := iv.StartTime.Add(iv.Duration)
		if start.Before(ivEnd) && iv.StartTime.Before(end) {
			return true
		}
	}
	return false
}

// ReplanStudentDropout removes a student's future interview, then
// compacts every LATER interview on that SAME panel forward by the
// vacated duration — skipping (leaving untouched) any student for whom
// the shift would create a real conflict elsewhere. Scope is strictly
// this one panel's remaining day.
func ReplanStudentDropout(state *scheduler.ScheduleState, d StudentDropoutDisruption) (*scheduler.ScheduleState, *Diff, error) {
	next := state.Clone()

	idx := findInterviewIndex(next.Interviews, func(iv models.Interview) bool {
		return iv.StudentID == d.StudentID && iv.Status == models.StatusScheduled && !iv.StartTime.Before(d.At)
	})
	if idx == -1 {
		return nil, nil, fmt.Errorf("no future scheduled interview found for student %s", d.StudentID)
	}

	vacated := next.Interviews[idx]
	panelID := vacated.PanelID
	before := interviewToSlot(vacated)

	next.Interviews[idx] = scheduler.MakeUnscheduledInterview(vacated.CompanyID, vacated.StudentID, "student withdrew")

	changes := []ChangeEntry{{
		StudentID: d.StudentID,
		Before:    before,
		After:     InterviewSlot{},
		Notify:    []NotifyTarget{{Kind: "student", ID: d.StudentID, Message: "your interview has been cancelled"}},
	}}

	var laterIdxs []int
	for i, iv := range next.Interviews {
		if iv.PanelID == panelID && iv.Status == models.StatusScheduled && iv.StartTime.After(vacated.StartTime) {
			laterIdxs = append(laterIdxs, i)
		}
	}
	sort.Slice(laterIdxs, func(i, j int) bool {
		return next.Interviews[laterIdxs[i]].StartTime.Before(next.Interviews[laterIdxs[j]].StartTime)
	})

	cursor := vacated.StartTime
	for _, i := range laterIdxs {
		iv := next.Interviews[i]
		candidateStart := cursor
		candidateEnd := candidateStart.Add(iv.Duration)

		if candidateStart.Equal(iv.StartTime) {
			cursor = candidateEnd
			continue
		}

		if hasConflict(next.Interviews, iv.StudentID, candidateStart, candidateEnd, iv.ID) {
			cursor = iv.StartTime.Add(iv.Duration) // skip: leave this student where they were
			continue
		}

		oldSlot := interviewToSlot(iv)
		iv.StartTime = candidateStart
		next.Interviews[i] = iv
		next.BusyUntil[iv.StudentID] = candidateEnd
		next.RoomBusyUntil[iv.RoomID] = candidateEnd
		cursor = candidateEnd

		changes = append(changes, ChangeEntry{
			StudentID: iv.StudentID,
			Before:    oldSlot,
			After:     interviewToSlot(iv),
			Notify:    []NotifyTarget{{Kind: "student", ID: iv.StudentID, Message: "your interview time moved earlier"}},
		})
	}

	return next, &Diff{Cause: d, Changes: changes, Timestamp: time.Now()}, nil
}

// ---------- Panel dropout ----------

// ReplanPanelDropout removes a panel entirely. Every future interview on
// it is displaced into the SAME company's surviving panels — fed into
// each surviving panel's TRAILING idle time only (after its own last
// currently-scheduled interview), never interleaved into its existing
// mid-day schedule. Anyone who can't be absorbed before dayEnd is left
// as an explicit gap.
func ReplanPanelDropout(data models.ShortlistData, state *scheduler.ScheduleState, d PanelDropoutDisruption, dayEnd time.Time) (*scheduler.ScheduleState, *Diff, error) {
	next := state.Clone()

	droppedPanel, ok := data.Panels[d.PanelID]
	if !ok {
		return nil, nil, fmt.Errorf("unknown panel %s", d.PanelID)
	}
	companyID := droppedPanel.CompanyID

	var displaced []models.Interview
	for i, iv := range next.Interviews {
		if iv.PanelID == d.PanelID && iv.Status == models.StatusScheduled && !iv.StartTime.Before(d.At) {
			displaced = append(displaced, iv)
			next.Interviews[i] = scheduler.MakeUnscheduledInterview(iv.CompanyID, iv.StudentID, "panel dropped out")
		}
	}
	sort.Slice(displaced, func(i, j int) bool { return displaced[i].StartTime.Before(displaced[j].StartTime) })

	changes := []ChangeEntry{{
		StudentID: "",
		Before:    InterviewSlot{PanelID: d.PanelID},
		After:     InterviewSlot{},
		Notify:    []NotifyTarget{{Kind: "panel", ID: d.PanelID, Message: "this panel has been marked dropped"}},
	}}

	if len(displaced) == 0 {
		return next, &Diff{Cause: d, Changes: changes, Timestamp: time.Now()}, nil
	}

	company, ok := data.Companies[companyID]
	if !ok {
		return nil, nil, fmt.Errorf("unknown company %s", companyID)
	}

	panels := &scheduler.PanelHeap{}
	heap.Init(panels)
	panelRoom := make(map[string]string)

	for _, pid := range company.Panels {
		if pid == d.PanelID {
			continue
		}
		panel, exists := data.Panels[pid]
		if !exists {
			continue
		}

		nextFree := d.At
		for _, iv := range next.Interviews {
			if iv.PanelID == pid && iv.Status == models.StatusScheduled {
				if end := iv.StartTime.Add(iv.Duration); end.After(nextFree) {
					nextFree = end
				}
			}
		}
		if room, found := panelRoomFor(next.Interviews, pid); found {
			panelRoom[pid] = room
		}
		heap.Push(panels, scheduler.PanelHeapItem{NextFree: nextFree, Panel: panel})
	}

	queue := &scheduler.CompanyQueue{CompanyID: companyID, Remaining: make([]string, len(displaced))}
	for i, iv := range displaced {
		queue.Remaining[i] = iv.StudentID
	}
	queues := map[string]*scheduler.CompanyQueue{companyID: queue}

	newInterviews := scheduler.RunMatching(panels, queues, panelRoom, data, dayEnd, next.BusyUntil, next.RoomBusyUntil)
	assigned := make(map[string]models.Interview, len(newInterviews))
	for _, iv := range newInterviews {
		assigned[iv.StudentID] = iv
	}
	next.Interviews = append(next.Interviews, newInterviews...)

	for _, orig := range displaced {
		before := interviewToSlot(orig)
		if newIv, ok := assigned[orig.StudentID]; ok {
			changes = append(changes, ChangeEntry{
				StudentID: orig.StudentID, Before: before, After: interviewToSlot(newIv),
				Notify: []NotifyTarget{
					{Kind: "student", ID: orig.StudentID, Message: "your panel changed"},
					{Kind: "panel", ID: newIv.PanelID, Message: "an additional student has been added to your queue"},
				},
			})
			continue
		}
		idx := findInterviewIndex(next.Interviews, func(iv models.Interview) bool {
			return iv.StudentID == orig.StudentID && iv.CompanyID == orig.CompanyID && iv.Status == models.StatusUnscheduled
		})
		if idx != -1 {
			next.Interviews[idx].UnscheduledReason = "panel dropped, no remaining capacity"
		}
		changes = append(changes, ChangeEntry{
			StudentID: orig.StudentID, Before: before, After: InterviewSlot{},
			Notify: []NotifyTarget{{Kind: "student", ID: orig.StudentID, Message: "your interview could not be rescheduled today"}},
		})
	}

	return next, &Diff{Cause: d, Changes: changes, Timestamp: time.Now()}, nil
}

// ---------- (your ReplanLateCompany and ReplanRoomUnavailable go here, unchanged) ----------
// ---------- Late company ----------

// ReplanLateCompany handles a company arriving late.
//
// Every one of the company's not-yet-started interviews from d.At onward
// is pulled back into that company's shared queue and re-fed through the
// SAME event-driven matching loop used by scheduler.RunWave.
//
// The replan starts no earlier than d.At + d.Delay.
//
// This is deliberately NOT a flat time-shift. The schedule is re-derived,
// allowing the heap to compress idle gaps instead of forcing every later
// interview to inherit the entire delay.
func ReplanLateCompany(
	data models.ShortlistData,
	state *scheduler.ScheduleState,
	d LateCompanyDisruption,
	dayEnd time.Time,
) (*scheduler.ScheduleState, *Diff, error) {
	next := state.Clone()

	company, ok := data.Companies[d.CompanyID]
	if !ok {
		return nil, nil, fmt.Errorf("unknown company %s", d.CompanyID)
	}

	arrival := d.At.Add(d.Delay)

	var displaced []models.Interview

	for i, iv := range next.Interviews {
		if iv.CompanyID == d.CompanyID &&
			iv.Status == models.StatusScheduled &&
			!iv.StartTime.Before(d.At) {

			displaced = append(displaced, iv)

			next.Interviews[i] = scheduler.MakeUnscheduledInterview(
				iv.CompanyID,
				iv.StudentID,
				"company running late",
			)
		}
	}

	sort.Slice(displaced, func(i, j int) bool {
		return displaced[i].StartTime.Before(displaced[j].StartTime)
	})

	changes := []ChangeEntry{{
		StudentID: "",
		Before:    InterviewSlot{},
		After:     InterviewSlot{},
		Notify: []NotifyTarget{{
			Kind:    "coordinator",
			ID:      d.CompanyID,
			Message: fmt.Sprintf("company arriving %s late", d.Delay),
		}},
	}}

	if len(displaced) == 0 {
		return next, &Diff{
			Cause:     d,
			Changes:   changes,
			Timestamp: time.Now(),
		}, nil
	}

	panels := &scheduler.PanelHeap{}
	heap.Init(panels)

	panelRoom := make(map[string]string)

	for _, pid := range company.Panels {
		panel, exists := data.Panels[pid]
		if !exists {
			continue
		}

		nextFree := arrival

		for _, iv := range next.Interviews {
			if iv.PanelID != pid ||
				iv.Status != models.StatusScheduled {
				continue
			}

			end := iv.StartTime.Add(iv.Duration)

			if end.After(nextFree) {
				nextFree = end
			}
		}

		if room, found := panelRoomFor(next.Interviews, pid); found {
			panelRoom[pid] = room
		}

		heap.Push(
			panels,
			scheduler.PanelHeapItem{
				NextFree: nextFree,
				Panel:    panel,
			},
		)
	}

	queue := &scheduler.CompanyQueue{
		CompanyID: d.CompanyID,
		Remaining: make([]string, len(displaced)),
	}

	for i, iv := range displaced {
		queue.Remaining[i] = iv.StudentID
	}

	queues := map[string]*scheduler.CompanyQueue{
		d.CompanyID: queue,
	}

	newInterviews := scheduler.RunMatching(
		panels,
		queues,
		panelRoom,
		data,
		dayEnd,
		next.BusyUntil,
		next.RoomBusyUntil,
	)

	assigned := make(map[string]models.Interview, len(newInterviews))

	for _, iv := range newInterviews {
		assigned[iv.StudentID] = iv
	}

	next.Interviews = append(next.Interviews, newInterviews...)

	for _, orig := range displaced {
		before := interviewToSlot(orig)

		if newIv, ok := assigned[orig.StudentID]; ok {
			changes = append(changes, ChangeEntry{
				StudentID: orig.StudentID,
				Before:    before,
				After:     interviewToSlot(newIv),
				Notify: []NotifyTarget{{
					Kind:    "student",
					ID:      orig.StudentID,
					Message: "your interview time changed due to a company delay",
				}},
			})

			continue
		}

		idx := findInterviewIndex(
			next.Interviews,
			func(iv models.Interview) bool {
				return iv.StudentID == orig.StudentID &&
					iv.CompanyID == orig.CompanyID &&
					iv.Status == models.StatusUnscheduled
			},
		)

		if idx != -1 {
			next.Interviews[idx].UnscheduledReason =
				"company delay left no remaining capacity"
		}

		changes = append(changes, ChangeEntry{
			StudentID: orig.StudentID,
			Before:    before,
			After:     InterviewSlot{},
			Notify: []NotifyTarget{{
				Kind:    "student",
				ID:      orig.StudentID,
				Message: "your interview could not be rescheduled today due to a company delay",
			}},
		})
	}

	return next, &Diff{
		Cause:     d,
		Changes:   changes,
		Timestamp: time.Now(),
	}, nil
}

// ---------- Room unavailable ----------

// ReplanRoomUnavailable handles one room going offline from d.At onward.
//
// Interviews are grouped BY PANEL because the same physical room may have
// belonged to different panels across different waves earlier in the day.
//
// Each affected panel therefore gets its own replacement room and its own
// re-matching run. Interviews from different panels are never merged.
func ReplanRoomUnavailable(
	data models.ShortlistData,
	state *scheduler.ScheduleState,
	d RoomUnavailableDisruption,
	dayEnd time.Time,
) (*scheduler.ScheduleState, *Diff, error) {
	next := state.Clone()

	displacedByPanel := make(map[string][]models.Interview)

	for i, iv := range next.Interviews {
		if iv.RoomID == d.RoomID &&
			iv.Status == models.StatusScheduled &&
			!iv.StartTime.Before(d.At) {

			displacedByPanel[iv.PanelID] =
				append(displacedByPanel[iv.PanelID], iv)

			next.Interviews[i] = scheduler.MakeUnscheduledInterview(
				iv.CompanyID,
				iv.StudentID,
				"room unavailable",
			)
		}
	}

	changes := []ChangeEntry{{
		StudentID: "",
		Before: InterviewSlot{
			RoomID: d.RoomID,
		},
		After: InterviewSlot{},
		Notify: []NotifyTarget{{
			Kind:    "coordinator",
			ID:      d.RoomID,
			Message: "this room is no longer available",
		}},
	}}

	if len(displacedByPanel) == 0 {
		return next, &Diff{
			Cause:     d,
			Changes:   changes,
			Timestamp: time.Now(),
		}, nil
	}

	panelIDs := make([]string, 0, len(displacedByPanel))

	for pid := range displacedByPanel {
		panelIDs = append(panelIDs, pid)
	}

	sort.Strings(panelIDs)

	for _, pid := range panelIDs {
		displaced := displacedByPanel[pid]

		sort.Slice(displaced, func(i, j int) bool {
			return displaced[i].StartTime.Before(displaced[j].StartTime)
		})

		panel, exists := data.Panels[pid]
		if !exists {
			for _, orig := range displaced {
				changes = append(changes, ChangeEntry{
					StudentID: orig.StudentID,
					Before:    interviewToSlot(orig),
					After:     InterviewSlot{},
					Notify: []NotifyTarget{{
						Kind:    "student",
						ID:      orig.StudentID,
						Message: "your interview could not be rescheduled because its panel is unavailable",
					}},
				})
			}

			continue
		}

		candidateRooms := make(map[string]models.Room, len(data.Rooms))

		for rid, room := range data.Rooms {
			if rid == d.RoomID {
				continue
			}

			candidateRooms[rid] = room
		}

		newRoomID, _, found := scheduler.FindAvailableRoom(
			candidateRooms,
			next.RoomBusyUntil,
			displaced[0].StartTime,
		)

		if !found {
			for _, orig := range displaced {
				changes = append(changes, ChangeEntry{
					StudentID: orig.StudentID,
					Before:    interviewToSlot(orig),
					After:     InterviewSlot{},
					Notify: []NotifyTarget{{
						Kind:    "student",
						ID:      orig.StudentID,
						Message: "your interview could not be rescheduled today — no room available",
					}},
				})
			}

			continue
		}

		panels := &scheduler.PanelHeap{}
		heap.Init(panels)

		heap.Push(
			panels,
			scheduler.PanelHeapItem{
				NextFree: displaced[0].StartTime,
				Panel:    panel,
			},
		)

		panelRoom := map[string]string{
			pid: newRoomID,
		}

		queue := &scheduler.CompanyQueue{
			CompanyID: panel.CompanyID,
			Remaining: make([]string, len(displaced)),
		}

		for i, iv := range displaced {
			queue.Remaining[i] = iv.StudentID
		}

		queues := map[string]*scheduler.CompanyQueue{
			panel.CompanyID: queue,
		}

		newInterviews := scheduler.RunMatching(
			panels,
			queues,
			panelRoom,
			data,
			dayEnd,
			next.BusyUntil,
			next.RoomBusyUntil,
		)

		assigned := make(map[string]models.Interview, len(newInterviews))

		for _, iv := range newInterviews {
			assigned[iv.StudentID] = iv
		}

		next.Interviews = append(next.Interviews, newInterviews...)

		for _, orig := range displaced {
			before := interviewToSlot(orig)

			if newIv, ok := assigned[orig.StudentID]; ok {
				changes = append(changes, ChangeEntry{
					StudentID: orig.StudentID,
					Before:    before,
					After:     interviewToSlot(newIv),
					Notify: []NotifyTarget{{
						Kind:    "student",
						ID:      orig.StudentID,
						Message: "your interview room changed",
					}},
				})

				continue
			}

			idx := findInterviewIndex(
				next.Interviews,
				func(iv models.Interview) bool {
					return iv.StudentID == orig.StudentID &&
						iv.PanelID == pid &&
						iv.Status == models.StatusUnscheduled
				},
			)

			if idx != -1 {
				next.Interviews[idx].UnscheduledReason =
					"room unavailable, no remaining capacity"
			}

			changes = append(changes, ChangeEntry{
				StudentID: orig.StudentID,
				Before:    before,
				After:     InterviewSlot{},
				Notify: []NotifyTarget{{
					Kind:    "student",
					ID:      orig.StudentID,
					Message: "your interview could not be rescheduled today — no room available",
				}},
			})
		}
	}

	return next, &Diff{
		Cause:     d,
		Changes:   changes,
		Timestamp: time.Now(),
	}, nil
}

// ---------- Helpers ----------

func panelRoomFor(interviews []models.Interview, panelID string) (string, bool) {
	for _, iv := range interviews {
		if iv.PanelID == panelID && iv.Status == models.StatusScheduled {
			return iv.RoomID, true
		}
	}
	return "", false
}

func findInterviewIndex(interviews []models.Interview, match func(models.Interview) bool) int {
	for i, iv := range interviews {
		if match(iv) {
			return i
		}
	}
	return -1
}
