package api

import (
	"fmt"
	"time"

	"placement-scheduler/internal/replan"
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
	switch e.Kind {
	case "student_dropout":
		return replan.StudentDropoutDisruption{StudentID: e.StudentID, At: e.At}, nil
	case "panel_dropout":
		return replan.PanelDropoutDisruption{PanelID: e.PanelID, At: e.At}, nil
	case "late_company":
		return replan.LateCompanyDisruption{
			CompanyID: e.CompanyID,
			Delay:     time.Duration(e.DelayMins) * time.Minute,
			At:        e.At,
		}, nil
	case "room_unavailable":
		return replan.RoomUnavailableDisruption{RoomID: e.RoomID, At: e.At}, nil
	default:
		return nil, fmt.Errorf("unknown disruption kind: %q", e.Kind)
	}
}
