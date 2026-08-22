package scheduler

import (
	"container/heap"
	"sort"
	"time"

	"placement-scheduler/internal/models"
)

// PriorityRank converts a company tier into a sortable priority.
//
// Lower number = higher priority.
//
// Niche        = 0
// Mid-Tier     = 1
// Mass Recruiter = 2
func PriorityRank(t models.PriorityTier) int {
	switch t {
	case models.TierNiche:
		return 0
	case models.TierMidTier:
		return 1
	case models.TierMassRecruiter:
		return 2
	default:
		return 2
	}
}

// WaveSpec represents one scheduling window.
//
// Example:
//
//	Day 1, 09:00 - 13:00
//
// Companies whose slots share the same window are placed into
// the same wave.
type WaveSpec struct {
	Day        time.Time
	Start      time.Time
	End        time.Time
	CompanyIDs []string
}

// companyQueue stores the students still waiting to be interviewed
// by one company.
type companyQueue struct {
	CompanyID string
	Remaining []string
}

// panelHeapItem represents a panel's next available time.
type panelHeapItem struct {
	NextFree time.Time
	Panel    models.Panel
}

// panelHeap is a min-heap ordered by:
//
// 1. Earliest NextFree
// 2. Panel ID as deterministic tie-breaker
type panelHeap []panelHeapItem

func (h panelHeap) Len() int {
	return len(h)
}

func (h panelHeap) Less(i, j int) bool {
	if !h[i].NextFree.Equal(h[j].NextFree) {
		return h[i].NextFree.Before(h[j].NextFree)
	}

	return h[i].Panel.ID < h[j].Panel.ID
}

func (h panelHeap) Swap(i, j int) {
	h[i], h[j] = h[j], h[i]
}

func (h *panelHeap) Push(x interface{}) {
	*h = append(*h, x.(panelHeapItem))
}

func (h *panelHeap) Pop() interface{} {
	old := *h
	n := len(old)

	item := old[n-1]

	*h = old[:n-1]

	return item
}

// buildCompanyQueues creates one queue for every company participating
// in the current wave.
//
// Shortlist order is preserved.
func buildCompanyQueues(
	data models.ShortlistData,
	wave WaveSpec,
) map[string]*companyQueue {

	inWave := make(
		map[string]bool,
		len(wave.CompanyIDs),
	)

	for _, companyID := range wave.CompanyIDs {
		inWave[companyID] = true
	}

	queues := make(
		map[string]*companyQueue,
		len(wave.CompanyIDs),
	)

	for _, companyID := range wave.CompanyIDs {
		queues[companyID] = &companyQueue{
			CompanyID: companyID,
			Remaining: []string{},
		}
	}

	for _, shortlist := range data.Shortlists {

		if !inWave[shortlist.CompanyID] {
			continue
		}

		queue, exists := queues[shortlist.CompanyID]

		if !exists {
			continue
		}

		queue.Remaining = append(
			queue.Remaining,
			shortlist.StudentID,
		)
	}

	return queues
}

// admitPanels decides which panels receive rooms for this wave.
//
// If there are enough rooms for all panels, all panels are seated.
//
// If demand exceeds available rooms, panels are sorted by:
//
//  1. Company priority
//  2. Company shortlist size, descending
//  3. Panel ID, ascending
//
// A company is fully locked out only if none of its panels receives
// a room.
func admitPanels(
	data models.ShortlistData,
	wave WaveSpec,
	numRooms int,
) (
	seated []models.Panel,
	fullyLockedOutCompanies []string,
) {

	if numRooms <= 0 {

		seen := make(
			map[string]bool,
		)

		for _, companyID := range wave.CompanyIDs {

			if seen[companyID] {
				continue
			}

			seen[companyID] = true

			fullyLockedOutCompanies =
				append(
					fullyLockedOutCompanies,
					companyID,
				)
		}

		return nil, fullyLockedOutCompanies
	}

	queues := buildCompanyQueues(
		data,
		wave,
	)

	type panelCandidate struct {
		panel         models.Panel
		priority      int
		shortlistSize int
	}

	candidates := make(
		[]panelCandidate,
		0,
	)

	// Build candidate panels.
	for _, companyID := range wave.CompanyIDs {

		company, exists :=
			data.Companies[companyID]

		if !exists {
			continue
		}

		shortlistSize := 0

		if queue, exists :=
			queues[companyID]; exists {

			shortlistSize =
				len(queue.Remaining)
		}

		for _, panelID := range company.Panels {

			panel, exists :=
				data.Panels[panelID]

			if !exists {
				continue
			}

			candidates = append(
				candidates,
				panelCandidate{
					panel: panel,

					priority: PriorityRank(
						company.Tier,
					),

					shortlistSize: shortlistSize,
				},
			)
		}
	}

	// If every panel fits, seat everyone.
	if len(candidates) <= numRooms {

		for _, candidate := range candidates {

			seated = append(
				seated,
				candidate.panel,
			)
		}

	} else {

		// Otherwise apply deterministic priority ordering.
		sort.Slice(
			candidates,
			func(i, j int) bool {

				// Higher company priority first.
				if candidates[i].priority !=
					candidates[j].priority {

					return candidates[i].priority <
						candidates[j].priority
				}

				// Larger shortlist first.
				if candidates[i].shortlistSize !=
					candidates[j].shortlistSize {

					return candidates[i].shortlistSize >
						candidates[j].shortlistSize
				}

				// Deterministic tie-break.
				return candidates[i].panel.ID <
					candidates[j].panel.ID
			},
		)

		limit := numRooms

		if limit > len(candidates) {
			limit = len(candidates)
		}

		for i := 0; i < limit; i++ {

			seated = append(
				seated,
				candidates[i].panel,
			)
		}
	}

	// Determine which companies received at least one panel.
	seatedCompanies := make(
		map[string]bool,
	)

	for _, panel := range seated {
		seatedCompanies[panel.CompanyID] = true
	}

	// Any company with zero seated panels is fully locked out.
	seenCompanies := make(
		map[string]bool,
	)

	for _, companyID := range wave.CompanyIDs {

		if seenCompanies[companyID] {
			continue
		}

		seenCompanies[companyID] = true

		if !seatedCompanies[companyID] {

			fullyLockedOutCompanies =
				append(
					fullyLockedOutCompanies,
					companyID,
				)
		}
	}

	return seated, fullyLockedOutCompanies
}

// makeUnscheduledInterview creates a consistent unscheduled record.
func makeUnscheduledInterview(
	companyID string,
	studentID string,
	reason string,
) models.Interview {

	return models.Interview{
		ID: "unscheduled-" +
			companyID +
			"-" +
			studentID,

		CompanyID: companyID,

		StudentID: studentID,

		Status: models.StatusUnscheduled,

		UnscheduledReason: reason,
	}
}

// findAvailableStudent searches a company's queue for the first student
// who is free at the requested time.
//
// If nobody is currently available, the function returns the earliest
// time any remaining student becomes available.
func findAvailableStudent(
	queue *companyQueue,
	busyUntil map[string]time.Time,
	at time.Time,
) (
	studentID string,
	nextAvailable time.Time,
	found bool,
) {

	var earliest time.Time

	for _, studentID := range queue.Remaining {

		busyTime, isBusy :=
			busyUntil[studentID]

		// Student is currently available.
		if !isBusy ||
			!busyTime.After(at) {

			return studentID, at, true
		}

		// Track earliest future availability.
		if earliest.IsZero() ||
			busyTime.Before(earliest) {

			earliest = busyTime
		}
	}

	return "", earliest, false
}

// findAvailableRoom searches for a physical room that is available
// at the requested time.
//
// Room IDs are sorted first so the same input always produces the
// same schedule.
func findAvailableRoom(
	rooms map[string]models.Room,
	roomBusyUntil map[string]time.Time,
	at time.Time,
) (
	roomID string,
	nextAvailable time.Time,
	found bool,
) {

	roomIDs := make(
		[]string,
		0,
		len(rooms),
	)

	for roomID := range rooms {

		roomIDs = append(
			roomIDs,
			roomID,
		)
	}

	sort.Strings(roomIDs)

	var earliest time.Time

	for _, roomID := range roomIDs {

		busyTime, isBusy :=
			roomBusyUntil[roomID]

		// Room is free.
		if !isBusy ||
			!busyTime.After(at) {

			return roomID, at, true
		}

		// Track earliest room availability.
		if earliest.IsZero() ||
			busyTime.Before(earliest) {

			earliest = busyTime
		}
	}

	return "", earliest, false
}

// removeStudent removes a successfully scheduled student from
// the company's remaining queue.
func removeStudent(
	queue *companyQueue,
	studentID string,
) {

	for i, id := range queue.Remaining {

		if id != studentID {
			continue
		}

		queue.Remaining =
			append(
				queue.Remaining[:i],
				queue.Remaining[i+1:]...,
			)

		return
	}
}

// RunWave executes one scheduling wave.
//
// IMPORTANT:
//
// busyUntil and roomBusyUntil MUST be shared across all waves.
//
// They are deliberately passed into this function rather than created
// here. This guarantees that overlapping waves cannot independently
// book the same student or physical room.
//
// The function returns BOTH scheduled and unscheduled interviews.
func RunWave(
	data models.ShortlistData,
	wave WaveSpec,
	numRooms int,
	busyUntil map[string]time.Time,
	roomBusyUntil map[string]time.Time,
) []models.Interview {

	queues := buildCompanyQueues(
		data,
		wave,
	)

	results := make(
		[]models.Interview,
		0,
	)

	// ------------------------------------------------------------
	// 1. Admit panels.
	// ------------------------------------------------------------

	seatedPanels, lockedOutCompanies :=
		admitPanels(
			data,
			wave,
			numRooms,
		)

	// ------------------------------------------------------------
	// 2. Mark completely locked-out companies.
	// ------------------------------------------------------------

	lockedOut := make(
		map[string]bool,
	)

	for _, companyID := range lockedOutCompanies {

		lockedOut[companyID] = true

		queue, exists :=
			queues[companyID]

		if !exists {
			continue
		}

		for _, studentID := range queue.Remaining {

			results = append(
				results,
				makeUnscheduledInterview(
					companyID,
					studentID,
					"no room available in wave",
				),
			)
		}

		queue.Remaining = nil
	}

	// ------------------------------------------------------------
	// 3. Initialize panel event heap.
	// ------------------------------------------------------------

	panels := &panelHeap{}

	heap.Init(panels)

	for _, panel := range seatedPanels {

		queue, exists :=
			queues[panel.CompanyID]

		if !exists ||
			len(queue.Remaining) == 0 {

			continue
		}

		heap.Push(
			panels,
			panelHeapItem{
				NextFree: wave.Start,

				Panel: panel,
			},
		)
	}

	// ------------------------------------------------------------
	// 4. Event-driven scheduling loop.
	// ------------------------------------------------------------

	for panels.Len() > 0 {

		item :=
			heap.Pop(panels).(panelHeapItem)

		panel :=
			item.Panel

		currentTime :=
			item.NextFree

		// Panel cannot start another interview after
		// the wave has ended.
		if !currentTime.Before(
			wave.End,
		) {
			continue
		}

		company, exists :=
			data.Companies[panel.CompanyID]

		if !exists {
			continue
		}

		queue, exists :=
			queues[panel.CompanyID]

		if !exists ||
			len(queue.Remaining) == 0 {

			continue
		}

		// --------------------------------------------------------
		// 5. Find available student.
		// --------------------------------------------------------

		studentID,
			nextStudentTime,
			studentFound :=
			findAvailableStudent(
				queue,
				busyUntil,
				currentTime,
			)

		if !studentFound {

			// No student is currently available.
			//
			// Jump directly to the earliest student availability
			// instead of polling at arbitrary intervals.
			if nextStudentTime.IsZero() ||
				!nextStudentTime.Before(
					wave.End,
				) {

				continue
			}

			heap.Push(
				panels,
				panelHeapItem{
					NextFree: nextStudentTime,

					Panel: panel,
				},
			)

			continue
		}

		// --------------------------------------------------------
		// 6. Find available room.
		// --------------------------------------------------------

		roomID,
			nextRoomTime,
			roomFound :=
			findAvailableRoom(
				data.Rooms,
				roomBusyUntil,
				currentTime,
			)

		if !roomFound {

			// Every physical room is currently occupied.
			//
			// Jump directly to the earliest room availability.
			if nextRoomTime.IsZero() ||
				!nextRoomTime.Before(
					wave.End,
				) {

				continue
			}

			heap.Push(
				panels,
				panelHeapItem{
					NextFree: nextRoomTime,

					Panel: panel,
				},
			)

			continue
		}

		// --------------------------------------------------------
		// 7. Check whether the complete interview fits.
		// --------------------------------------------------------

		endTime :=
			currentTime.Add(
				company.InterviewDuration,
			)

		if endTime.After(
			wave.End,
		) {
			continue
		}

		// --------------------------------------------------------
		// 8. Create scheduled interview.
		// --------------------------------------------------------

		interviewID :=
			"interview-" +
				company.ID +
				"-" +
				studentID +
				"-" +
				currentTime.Format(
					"20060102150405",
				)

		interview :=
			models.Interview{

				ID: interviewID,

				CompanyID: company.ID,

				StudentID: studentID,

				PanelID: panel.ID,

				RoomID: roomID,

				StartTime: currentTime,

				Duration: company.InterviewDuration,

				Status: models.StatusScheduled,
			}

		results = append(
			results,
			interview,
		)

		// --------------------------------------------------------
		// 9. Update GLOBAL student availability.
		// --------------------------------------------------------

		busyUntil[studentID] =
			endTime

		// --------------------------------------------------------
		// 10. Update GLOBAL room availability.
		// --------------------------------------------------------

		roomBusyUntil[roomID] =
			endTime

		// --------------------------------------------------------
		// 11. Remove student from this company's queue.
		// --------------------------------------------------------

		removeStudent(
			queue,
			studentID,
		)

		// --------------------------------------------------------
		// 12. Panel becomes available after this interview.
		// --------------------------------------------------------

		heap.Push(
			panels,
			panelHeapItem{
				NextFree: endTime,

				Panel: panel,
			},
		)
	}

	// ------------------------------------------------------------
	// 13. Remaining students could not be scheduled.
	// ------------------------------------------------------------

	for _, queue := range queues {

		if lockedOut[queue.CompanyID] {
			continue
		}

		for _, studentID := range queue.Remaining {

			results = append(
				results,
				makeUnscheduledInterview(
					queue.CompanyID,
					studentID,
					"ran out of interview time in wave",
				),
			)
		}
	}

	return results
}
