package api

import (
	"strings"
	"testing"
	"time"

	"placement-scheduler/internal/replan"
)

func TestDisruptionEnvelopeValidation(t *testing.T) {
	at := time.Date(2026, 9, 1, 9, 0, 0, 0, time.UTC)
	tests := []struct {
		name    string
		env     disruptionEnvelope
		wantErr string
	}{
		{"missing at", disruptionEnvelope{Kind: "student_dropout", StudentID: "S0001"}, "at is required"},
		{"student id required", disruptionEnvelope{Kind: "student_dropout", At: at}, "student_id is required"},
		{"panel id required", disruptionEnvelope{Kind: "panel_dropout", At: at}, "panel_id is required"},
		{"company id required", disruptionEnvelope{Kind: "late_company", At: at, DelayMins: 30}, "company_id is required"},
		{"positive delay required", disruptionEnvelope{Kind: "late_company", At: at, CompanyID: "MASS-01"}, "delay_minutes must be greater than 0"},
		{"room id required", disruptionEnvelope{Kind: "room_unavailable", At: at}, "room_id is required"},
		{"unknown kind", disruptionEnvelope{Kind: "mystery", At: at}, "unknown disruption kind"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := tt.env.toDisruption()
			if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("got err %v, want containing %q", err, tt.wantErr)
			}
		})
	}
}

func TestDisruptionEnvelopeBuildsConcreteType(t *testing.T) {
	at := time.Date(2026, 9, 1, 9, 0, 0, 0, time.UTC)
	disruption, err := (disruptionEnvelope{
		Kind:      "late_company",
		At:        at,
		CompanyID: "MASS-01",
		DelayMins: 45,
	}).toDisruption()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	late, ok := disruption.(replan.LateCompanyDisruption)
	if !ok {
		t.Fatalf("got %T, want LateCompanyDisruption", disruption)
	}
	if late.CompanyID != "MASS-01" || late.Delay != 45*time.Minute || !late.At.Equal(at) {
		t.Fatalf("unexpected disruption payload: %+v", late)
	}
}
