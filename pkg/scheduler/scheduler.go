package scheduler

import (
	"container/heap"
	"sort"
	"time"

	"placement-scheduler/pkg/models"
)

// PriorityRank converts a company tier into a sortable priority.
// Lower number = higher priority. Niche=0, Mid=1, Mass=2.
func PriorityRank(t models.PriorityTier) int {
	switch t {
	case models.TierNiche:
		return 0
	case models.TierMidTier:
		return 1
	default:
		return 2
	}
}

// WaveSpec represents one scheduling window — companies whose slots
// share the same (day, start, end) are grouped into the same wave.
type WaveSpec struct {
	Day        time.Time
	Start      time.Time
	End        time.Time
	CompanyIDs []string
}

// ScheduleState bundles the flat interview list with the two busy-until
// maps RunWave threads through by reference. Bundling them means one
// Clone() call protects all three together — a replan can attempt a
// change and discard it cleanly if it turns out invalid, without any
// risk of forgetting to copy one of the three pieces first.
type ScheduleState struct {
	Interviews    []models.Interview
	BusyUntil     map[string]time.Time
	RoomBusyUntil map[string]time.Time
}

// Clone deep-copies all three fields so replan functions can mutate
// freely and only commit the result after Validate() passes.
func (s *ScheduleState) Clone() *ScheduleState {
	interviews := make([]models.Interview, len(s.Interviews))
	copy(interviews, s.Interviews)

	busyUntil := make(map[string]time.Time, len(s.BusyUntil))
	for k, v := range s.BusyUntil {
		busyUntil[k] = v
	}

	roomBusyUntil := make(map[string]time.Time, len(s.RoomBusyUntil))
	for k, v := range s.RoomBusyUntil {
		roomBusyUntil[k] = v
	}

	return &ScheduleState{
		Interviews:    interviews,
		BusyUntil:     busyUntil,
		RoomBusyUntil: roomBusyUntil,
	}
}

// panelRoomFor scans already-scheduled interviews for the room a panel
// is already sitting in. A replan MUST seed panelRoom with this before
// running the matching loop for a surviving panel — otherwise a fresh
// room search could move a panel to a different physical room mid-day,
// which is exactly the stickiness bug the Day-2 check was built to catch.
func panelRoomFor(interviews []models.Interview, panelID string) (string, bool) {
	for _, iv := range interviews {
		if iv.PanelID == panelID && iv.Status == models.StatusScheduled {
			return iv.RoomID, true
		}
	}

	return "", false
}

// CompanyQueue stores the students still waiting to be interviewed by
// one company.
type CompanyQueue struct {
	CompanyID string
	Remaining []string
}

// PanelHeapItem represents a panel's next available time.
type PanelHeapItem struct {
	NextFree time.Time
	Panel    models.Panel
}

// PanelHeap is a min-heap ordered by earliest NextFree, with Panel ID
// as a deterministic tie-breaker.
type PanelHeap []PanelHeapItem

func (h PanelHeap) Len() int {
	return len(h)
}

func (h PanelHeap) Less(i, j int) bool {
	if !h[i].NextFree.Equal(h[j].NextFree) {
		return h[i].NextFree.Before(h[j].NextFree)
	}

	return h[i].Panel.ID < h[j].Panel.ID
}

func (h PanelHeap) Swap(i, j int) {
	h[i], h[j] = h[j], h[i]
}

func (h *PanelHeap) Push(x interface{}) {
	*h = append(*h, x.(PanelHeapItem))
}

func (h *PanelHeap) Pop() interface{} {
	old := *h
	n := len(old)
	item := old[n-1]
	*h = old[:n-1]
	return item
}

// buildCompanyQueues creates one queue per company participating in the
// current wave, preserving shortlist order.
func buildCompanyQueues(data models.ShortlistData, wave WaveSpec) map[string]*CompanyQueue {
	inWave := make(map[string]bool, len(wave.CompanyIDs))

	for _, cid := range wave.CompanyIDs {
		inWave[cid] = true
	}

	queues := make(map[string]*CompanyQueue, len(wave.CompanyIDs))

	for _, cid := range wave.CompanyIDs {
		queues[cid] = &CompanyQueue{
			CompanyID: cid,
			Remaining: []string{},
		}
	}

	for _, sl := range data.Shortlists {
		if !inWave[sl.CompanyID] {
			continue
		}

		q, exists := queues[sl.CompanyID]
		if !exists {
			continue
		}

		q.Remaining = append(q.Remaining, sl.StudentID)
	}

	return queues
}

// admitPanels decides which panels receive rooms for this wave.
//
// Fairness rule (deliberate policy call): if a company is present in
// this wave at all, it gets AT LEAST ONE seated panel before any
// company gets a second one.
//
// This is a two-phase allocation:
//
//  1. Guarantee round: sort companies by priority, then shortlist size
//     descending, then company ID. Seat ONE panel per company until
//     rooms run out.
//
//  2. Fill round: remaining rooms go to the companies' remaining panels
//     using the same priority ordering.
func admitPanels(
	data models.ShortlistData,
	wave WaveSpec,
	numRooms int,
) (seated []models.Panel, fullyLockedOutCompanies []string) {
	if numRooms <= 0 {
		seen := make(map[string]bool)

		for _, companyID := range wave.CompanyIDs {
			if !seen[companyID] {
				seen[companyID] = true
				fullyLockedOutCompanies = append(
					fullyLockedOutCompanies,
					companyID,
				)
			}
		}

		return nil, fullyLockedOutCompanies
	}

	queues := buildCompanyQueues(data, wave)

	type companyInfo struct {
		id            string
		priority      int
		shortlistSize int
		panelIDs      []string
	}

	companies := make([]companyInfo, 0, len(wave.CompanyIDs))
	totalPanels := 0

	for _, companyID := range wave.CompanyIDs {
		company, exists := data.Companies[companyID]
		if !exists {
			continue
		}

		shortlistSize := 0

		if queue, exists := queues[companyID]; exists {
			shortlistSize = len(queue.Remaining)
		}

		panelIDs := make([]string, len(company.Panels))
		copy(panelIDs, company.Panels)
		sort.Strings(panelIDs)

		companies = append(companies, companyInfo{
			id:            companyID,
			priority:      PriorityRank(company.Tier),
			shortlistSize: shortlistSize,
			panelIDs:      panelIDs,
		})

		totalPanels += len(panelIDs)
	}

	sort.Slice(companies, func(i, j int) bool {
		if companies[i].priority != companies[j].priority {
			return companies[i].priority < companies[j].priority
		}

		if companies[i].shortlistSize != companies[j].shortlistSize {
			return companies[i].shortlistSize > companies[j].shortlistSize
		}

		return companies[i].id < companies[j].id
	})

	// Fast path: everyone fits.
	if totalPanels <= numRooms {
		for _, c := range companies {
			for _, pid := range c.panelIDs {
				if panel, exists := data.Panels[pid]; exists {
					seated = append(seated, panel)
				}
			}
		}
	} else {
		seatedPanelIDs := make(map[string]bool)
		roomsLeft := numRooms

		// Phase 1: guarantee one panel per company.
		for i := range companies {
			if roomsLeft <= 0 {
				break
			}

			c := &companies[i]

			if len(c.panelIDs) == 0 {
				continue
			}

			firstPanelID := c.panelIDs[0]

			if panel, exists := data.Panels[firstPanelID]; exists {
				seated = append(seated, panel)
				seatedPanelIDs[firstPanelID] = true
				roomsLeft--
			}
		}

		// Phase 2: fill remaining rooms with leftover panels.
		type leftoverCandidate struct {
			panel         models.Panel
			priority      int
			shortlistSize int
		}

		leftovers := make([]leftoverCandidate, 0)

		for _, c := range companies {
			for _, pid := range c.panelIDs {
				if seatedPanelIDs[pid] {
					continue
				}

				if panel, exists := data.Panels[pid]; exists {
					leftovers = append(leftovers, leftoverCandidate{
						panel:         panel,
						priority:      c.priority,
						shortlistSize: c.shortlistSize,
					})
				}
			}
		}

		sort.Slice(leftovers, func(i, j int) bool {
			if leftovers[i].priority != leftovers[j].priority {
				return leftovers[i].priority < leftovers[j].priority
			}

			if leftovers[i].shortlistSize != leftovers[j].shortlistSize {
				return leftovers[i].shortlistSize > leftovers[j].shortlistSize
			}

			return leftovers[i].panel.ID < leftovers[j].panel.ID
		})

		limit := roomsLeft
		if limit > len(leftovers) {
			limit = len(leftovers)
		}

		for i := 0; i < limit; i++ {
			seated = append(seated, leftovers[i].panel)
		}
	}

	seatedCompanies := make(map[string]bool)

	for _, panel := range seated {
		seatedCompanies[panel.CompanyID] = true
	}

	seenCompanies := make(map[string]bool)

	for _, companyID := range wave.CompanyIDs {
		if seenCompanies[companyID] {
			continue
		}

		seenCompanies[companyID] = true

		if !seatedCompanies[companyID] {
			fullyLockedOutCompanies = append(
				fullyLockedOutCompanies,
				companyID,
			)
		}
	}

	return seated, fullyLockedOutCompanies
}

// MakeUnscheduledInterview creates a consistent unscheduled record.
func MakeUnscheduledInterview(
	companyID string,
	studentID string,
	reason string,
) models.Interview {
	return models.Interview{
		ID:                "unscheduled-" + companyID + "-" + studentID,
		CompanyID:         companyID,
		StudentID:         studentID,
		Status:            models.StatusUnscheduled,
		UnscheduledReason: reason,
	}
}

// findAvailableStudent searches a company's queue for the first student
// free at the requested time. If nobody is available now, it also
// returns the earliest time anyone in the queue becomes free.
func findAvailableStudent(
	queue *CompanyQueue,
	busyUntil map[string]time.Time,
	at time.Time,
) (studentID string, nextAvailable time.Time, found bool) {
	var earliest time.Time

	for _, sid := range queue.Remaining {
		busy, isBusy := busyUntil[sid]

		if !isBusy || !busy.After(at) {
			return sid, at, true
		}

		if earliest.IsZero() || busy.Before(earliest) {
			earliest = busy
		}
	}

	return "", earliest, false
}

// FindAvailableRoom searches for a physical room free at the requested
// time. Room IDs are sorted first so the same input always produces the
// same schedule.
//
// This is only ever called once per panel per wave. Once a panel gets a
// room, panelRoom keeps it pinned to that room.
func FindAvailableRoom(
	rooms map[string]models.Room,
	roomBusyUntil map[string]time.Time,
	at time.Time,
) (roomID string, nextAvailable time.Time, found bool) {
	roomIDs := make([]string, 0, len(rooms))

	for id := range rooms {
		roomIDs = append(roomIDs, id)
	}

	sort.Strings(roomIDs)

	var earliest time.Time

	for _, id := range roomIDs {
		busy, isBusy := roomBusyUntil[id]

		if !isBusy || !busy.After(at) {
			return id, at, true
		}

		if earliest.IsZero() || busy.Before(earliest) {
			earliest = busy
		}
	}

	return "", earliest, false
}

// removeStudent removes a successfully scheduled student from the
// company's remaining queue.
func removeStudent(queue *CompanyQueue, studentID string) {
	for i, id := range queue.Remaining {
		if id != studentID {
			continue
		}

		queue.Remaining = append(
			queue.Remaining[:i],
			queue.Remaining[i+1:]...,
		)

		return
	}
}

// RunMatching is the event-driven core previously inlined in RunWave.
//
// It drains the panel heap, matches each freed panel to the next
// available student in its company's queue, and honors panelRoom
// stickiness. Existing entries in panelRoom are reused as-is; empty
// entries trigger a fresh room search.
func RunMatching(
	panels *PanelHeap,
	queues map[string]*CompanyQueue,
	panelRoom map[string]string,
	data models.ShortlistData,
	waveEnd time.Time,
	busyUntil map[string]time.Time,
	roomBusyUntil map[string]time.Time,
) []models.Interview {
	results := make([]models.Interview, 0)

	for panels.Len() > 0 {
		item := heap.Pop(panels).(PanelHeapItem)
		panel := item.Panel
		currentTime := item.NextFree

		if !currentTime.Before(waveEnd) {
			continue
		}

		company, companyExists := data.Companies[panel.CompanyID]
		if !companyExists {
			continue
		}

		queue, queueExists := queues[panel.CompanyID]
		if !queueExists || len(queue.Remaining) == 0 {
			continue
		}

		studentID, nextStudentTime, studentFound :=
			findAvailableStudent(queue, busyUntil, currentTime)

		if !studentFound {
			if nextStudentTime.IsZero() ||
				!nextStudentTime.Before(waveEnd) {
				continue
			}

			heap.Push(
				panels,
				PanelHeapItem{
					NextFree: nextStudentTime,
					Panel:    panel,
				},
			)

			continue
		}

		// Room stickiness:
		// - If the panel already has a room, reuse it.
		// - Otherwise find and assign its first room.
		roomID, alreadyAssigned := panelRoom[panel.ID]
		if alreadyAssigned {
			if busy, isBusy := roomBusyUntil[roomID]; isBusy && busy.After(currentTime) {
				// Sticky room is legitimately in use by someone else right now —
				// this panel must WAIT for it, never switch rooms or barge in.
				heap.Push(panels, PanelHeapItem{NextFree: busy, Panel: panel})
				continue
			}
		} else {
			foundRoomID, nextRoomTime, roomFound := FindAvailableRoom(data.Rooms, roomBusyUntil, currentTime)
			if !roomFound {
				if nextRoomTime.IsZero() || !nextRoomTime.Before(waveEnd) {
					continue
				}
				heap.Push(panels, PanelHeapItem{NextFree: nextRoomTime, Panel: panel})
				continue
			}
			roomID = foundRoomID
			panelRoom[panel.ID] = roomID
		}

		endTime := currentTime.Add(company.InterviewDuration)

		if endTime.After(waveEnd) {
			continue
		}

		interview := models.Interview{
			ID:        "interview-" + company.ID + "-" + studentID + "-" + currentTime.Format("20060102150405"),
			CompanyID: company.ID,
			StudentID: studentID,
			PanelID:   panel.ID,
			RoomID:    roomID,
			StartTime: currentTime,
			Duration:  company.InterviewDuration,
			Status:    models.StatusScheduled,
		}

		results = append(results, interview)

		busyUntil[studentID] = endTime
		roomBusyUntil[roomID] = endTime

		removeStudent(queue, studentID)

		heap.Push(
			panels,
			PanelHeapItem{
				NextFree: endTime,
				Panel:    panel,
			},
		)
	}

	return results
}

// RunWave executes one scheduling wave.
//
// busyUntil and roomBusyUntil MUST be shared across all waves the caller
// runs. They are passed in rather than created here so overlapping waves
// cannot independently double-book the same student or room.
//
// panelRoom is local to this call. A full wave starts with an empty map;
// replans can instead seed it using panelRoomFor before calling
// RunMatching.
func RunWave(
	data models.ShortlistData,
	wave WaveSpec,
	numRooms int,
	busyUntil map[string]time.Time,
	roomBusyUntil map[string]time.Time,
) []models.Interview {
	queues := buildCompanyQueues(data, wave)

	seatedPanels, lockedOutCompanies :=
		admitPanels(data, wave, numRooms)

	results := make([]models.Interview, 0)

	lockedOut := make(map[string]bool)

	for _, companyID := range lockedOutCompanies {
		lockedOut[companyID] = true

		queue, exists := queues[companyID]
		if !exists {
			continue
		}

		for _, studentID := range queue.Remaining {
			results = append(
				results,
				MakeUnscheduledInterview(
					companyID,
					studentID,
					"no room available in wave",
				),
			)
		}

		queue.Remaining = nil
	}

	panels := &PanelHeap{}
	heap.Init(panels)

	for _, panel := range seatedPanels {
		queue, exists := queues[panel.CompanyID]
		if !exists || len(queue.Remaining) == 0 {
			continue
		}

		heap.Push(
			panels,
			PanelHeapItem{
				NextFree: wave.Start,
				Panel:    panel,
			},
		)
	}

	// Full waves have no pre-existing panel-room assignments.
	// Replans can seed this map before calling RunMatching.
	panelRoom := make(map[string]string)

	results = append(
		results,
		RunMatching(
			panels,
			queues,
			panelRoom,
			data,
			wave.End,
			busyUntil,
			roomBusyUntil,
		)...,
	)

	for _, queue := range queues {
		if lockedOut[queue.CompanyID] {
			continue
		}

		for _, studentID := range queue.Remaining {
			results = append(
				results,
				MakeUnscheduledInterview(
					queue.CompanyID,
					studentID,
					"ran out of interview time in wave",
				),
			)
		}
	}

	return results
}
