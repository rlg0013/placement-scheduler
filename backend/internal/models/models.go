package models

import "time"

// PriorityTier reflects how the college ranks a company's importance —
// used both for generation realism (Day-1 mass recruiters) and for
// deciding who gets bent first when a schedule goes infeasible.
type PriorityTier int

const (
	TierMassRecruiter PriorityTier = iota // Day-1, shortlists hundreds, many panels
	TierMidTier                           // Day-2/3, moderate shortlist size
	TierNiche                             // Day-3/4, small shortlist, picky CGPA cutoff
)

func (t PriorityTier) String() string {
	switch t {
	case TierMassRecruiter:
		return "MassRecruiter"
	case TierMidTier:
		return "MidTier"
	case TierNiche:
		return "Niche"
	default:
		return "Unknown"
	}
}

// Branch is deliberately a plain string, not an enum — real branch lists
// vary by college and you don't want to hardcode them.
type Branch string

// TimeSlot is a company's allotted window on a given day (Start/End are
// full time.Time, so the date is implicit — no separate Day field needed
// across a 4-day placement week).
type TimeSlot struct {
	Start time.Time
	End   time.Time
}

// Company is static input data: what a company brings to the table.
// This does NOT change during replanning — only the Schedule does.
type Company struct {
	ID                string
	Name              string
	CGPACutoff        float64
	Tier              PriorityTier
	InterviewDuration time.Duration // per-candidate; varies by company, hence continuous time
	Panels            []string      // Panel IDs belonging to this company
	Slots             []TimeSlot    // allotted windows this company has to interview in
}

// Panel is a fixed interviewing team for one company. Rooms are a scarce
// shared resource (20 rooms, up to 35 companies) — WHICH room a panel
// sits in is a scheduling decision, not input data, so Panel does not
// carry a room field. See PanelRoomAssignment on Schedule.
type Panel struct {
	ID        string
	CompanyID string
}

// Room is a physical interview room. 20 of them, per the scenario.
type Room struct {
	ID       string
	Capacity int // usually 1 (one panel per room), kept explicit rather than assumed
}

// Student is static input data: who they are. Their eventual interview
// assignment is NOT stored here — see Schedule.
type Student struct {
	ID     string
	Name   string
	CGPA   float64
	Branch Branch
}

// ShortlistEntry records that a company shortlisted a student.
// Generated once, stays fixed — the source of truth every replan must
// respect. NOT the same as an actual scheduled interview.
type ShortlistEntry struct {
	CompanyID string
	StudentID string
}

// ShortlistData is the full static input: the "shape" of placement week
// before any scheduling decision has been made.
type ShortlistData struct {
	Companies  map[string]Company
	Panels     map[string]Panel
	Rooms      map[string]Room
	Students   map[string]Student
	Shortlists []ShortlistEntry
}

// InterviewStatus lets you distinguish a clean assignment from one that
// couldn't be placed — "never fail silently" per the assignment brief.
type InterviewStatus int

const (
	StatusScheduled   InterviewStatus = iota
	StatusUnscheduled                 // couldn't fit — reason is mandatory
	StatusCancelled                   // was scheduled, then removed by a disruption/replan
)

// Interview is the SCHEDULER'S OUTPUT — one unit of "this student sits in
// front of this panel, starting at this time." Room is deliberately NOT
// repeated here — a panel's room for that day is looked up via
// PanelRoomAssignment, so there's one source of truth for "where."
// Nothing about the schedule is stored on Student or Company either, so
// diffing two schedules is a simple set-comparison over []Interview.
type Interview struct {
	ID                string
	CompanyID         string
	StudentID         string
	PanelID           string
	RoomID            string
	StartTime         time.Time
	Duration          time.Duration
	Status            InterviewStatus
	UnscheduledReason string // required if Status == StatusUnscheduled
}

// PanelRoomAssignment is the scheduler's decision of which room a panel
// occupies for a given day. One entry per (panel, day) — this is what
// enforces "a panel doesn't move rooms mid-day" as a scheduling output
// constraint, not an input assumption.
type PanelRoomAssignment struct {
	PanelID string
	RoomID  string
	Day     time.Time // truncated to the day; the specific date this assignment applies to
}

// Schedule is a full snapshot: every interview decision made so far, plus
// which room each panel is using each day. A replan produces a NEW
// Schedule; diff old vs new for the change summary the coordinator needs.
type Schedule struct {
	Interviews      []Interview
	RoomAssignments []PanelRoomAssignment
	GeneratedAt     time.Time
}
