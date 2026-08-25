package api

import (
	"fmt"
	"strings"
	"time"

	"placement-scheduler/pkg/replan"
)

type disruptionEnvelope struct {
	Kind      string    `json:"kind"`
	At        time.Time `json:"at"`
	StudentID string    `json:"student_id,omitempty"`
	PanelID   string    `json:"panel_id,omitempty"`
	CompanyID string    `json:"company_id,omitempty"`
	RoomID    string    `json:"room_id,omitempty"`
	DelayMins int       `json:"delay_minutes,omitempty"`
}

func (e disruptionEnvelope) toDisruption() (replan.Disruption, error) {
	if e.At.IsZero() {
		return nil, fmt.Errorf("at is required")
	}

	switch e.Kind {
	case "student_dropout":
		if strings.TrimSpace(e.StudentID) == "" {
			return nil, fmt.Errorf("student_id is required for student_dropout")
		}
		return replan.StudentDropoutDisruption{StudentID: e.StudentID, At: e.At}, nil
	case "panel_dropout":
		if strings.TrimSpace(e.PanelID) == "" {
			return nil, fmt.Errorf("panel_id is required for panel_dropout")
		}
		return replan.PanelDropoutDisruption{PanelID: e.PanelID, At: e.At}, nil
	case "late_company":
		if strings.TrimSpace(e.CompanyID) == "" {
			return nil, fmt.Errorf("company_id is required for late_company")
		}
		if e.DelayMins <= 0 {
			return nil, fmt.Errorf("delay_minutes must be greater than 0 for late_company")
		}
		return replan.LateCompanyDisruption{
			CompanyID: e.CompanyID,
			Delay:     time.Duration(e.DelayMins) * time.Minute,
			At:        e.At,
		}, nil
	case "room_unavailable":
		if strings.TrimSpace(e.RoomID) == "" {
			return nil, fmt.Errorf("room_id is required for room_unavailable")
		}
		return replan.RoomUnavailableDisruption{RoomID: e.RoomID, At: e.At}, nil
	default:
		return nil, fmt.Errorf("unknown disruption kind: %q", e.Kind)
	}
}
