package api

import (
	"encoding/json"
	"net/http"
	"sync"
	"time"

	"placement-scheduler/internal/models"
	"placement-scheduler/internal/replan"
	"placement-scheduler/internal/scheduler"
)

type Server struct {
	Data             models.ShortlistData
	State            *scheduler.ScheduleState
	DayEnd           time.Time
	OperatingEndHour int
	OperatingEndMin  int
	History          []*scheduler.ScheduleState
	MaxHistory       int
	historyLock      sync.Mutex
}

func NewServer(data models.ShortlistData, state *scheduler.ScheduleState, dayEnd time.Time) *Server {
	return &Server{
		Data:             data,
		State:            state,
		DayEnd:           dayEnd,
		OperatingEndHour: 19,
		OperatingEndMin:  0,
		MaxHistory:       10,
	}
}

func (s *Server) Routes() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /schedule", s.handleGetSchedule)
	mux.HandleFunc("GET /status", s.handleGetStatus)
	mux.HandleFunc("POST /disruptions", s.handlePostDisruption)
	mux.HandleFunc("POST /undo", s.handlePostUndo)
	return withCORS(mux)
}

// withCORS allows the Vite dev server (a different origin/port) to call
// this API from the browser. Fine for a local dev demo; a real deployment
// would restrict this to a specific origin instead of "*".
func withCORS(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type")

		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusOK)
			return
		}

		next.ServeHTTP(w, r)
	})
}

func (s *Server) handleGetSchedule(w http.ResponseWriter, r *http.Request) {
	s.historyLock.Lock()
	defer s.historyLock.Unlock()

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(s.State.Interviews)
}

type statusResponse struct {
	Summary            replan.ScheduleSummary
	UndoLeft           int
	OperatingDayEnd    string
	OperatingEndHour   int
	OperatingEndMinute int
}

func (s *Server) handleGetStatus(w http.ResponseWriter, r *http.Request) {
	s.historyLock.Lock()
	defer s.historyLock.Unlock()

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(statusResponse{
		Summary:            summarizeSchedule(s.State.Interviews),
		UndoLeft:           len(s.History),
		OperatingDayEnd:    "19:00",
		OperatingEndHour:   s.OperatingEndHour,
		OperatingEndMinute: s.OperatingEndMin,
	})
}

func (s *Server) disruptionDayEnd(at time.Time) time.Time {
	return time.Date(
		at.Year(),
		at.Month(),
		at.Day(),
		s.OperatingEndHour,
		s.OperatingEndMin,
		0,
		0,
		at.Location(),
	)
}

func summarizeSchedule(interviews []models.Interview) replan.ScheduleSummary {
	summary := replan.ScheduleSummary{
		Total:              len(interviews),
		UnscheduledReasons: map[string]int{},
	}

	for _, iv := range interviews {
		switch iv.Status {
		case models.StatusScheduled:
			summary.Scheduled++
		case models.StatusUnscheduled:
			summary.Unscheduled++
			reason := iv.UnscheduledReason
			if reason == "" {
				reason = "unspecified"
			}
			summary.UnscheduledReasons[reason]++
		case models.StatusCancelled:
			summary.Cancelled++
		}
	}

	return summary
}

func (s *Server) handlePostDisruption(w http.ResponseWriter, r *http.Request) {
	var env disruptionEnvelope
	if err := json.NewDecoder(r.Body).Decode(&env); err != nil {
		http.Error(w, "invalid JSON: "+err.Error(), http.StatusBadRequest)
		return
	}

	disruption, err := env.toDisruption()
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	s.historyLock.Lock()
	defer s.historyLock.Unlock()

	beforeSummary := summarizeSchedule(s.State.Interviews)

	next, diff, err := replan.Apply(s.Data, s.State, disruption, s.disruptionDayEnd(disruption.EffectiveAt()))
	if err != nil {
		http.Error(w, err.Error(), http.StatusUnprocessableEntity)
		return
	}

	afterSummary := summarizeSchedule(next.Interviews)
	diff.BeforeSummary = &beforeSummary
	diff.AfterSummary = &afterSummary

	s.History = append(s.History, s.State.Clone())
	if s.MaxHistory > 0 && len(s.History) > s.MaxHistory {
		s.History = s.History[len(s.History)-s.MaxHistory:]
	}
	s.State = next

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(diff)
}

func (s *Server) handlePostUndo(w http.ResponseWriter, r *http.Request) {
	s.historyLock.Lock()
	defer s.historyLock.Unlock()

	if len(s.History) == 0 {
		http.Error(w, "nothing to undo", http.StatusConflict)
		return
	}

	last := s.History[len(s.History)-1]
	s.History = s.History[:len(s.History)-1]
	s.State = last

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(statusResponse{
		Summary:            summarizeSchedule(s.State.Interviews),
		UndoLeft:           len(s.History),
		OperatingDayEnd:    "19:00",
		OperatingEndHour:   s.OperatingEndHour,
		OperatingEndMinute: s.OperatingEndMin,
	})
}
