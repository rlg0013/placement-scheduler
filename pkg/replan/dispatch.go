package replan

import (
	"fmt"
	"time"

	"placement-scheduler/pkg/models"
	"placement-scheduler/pkg/scheduler"
)

// Apply dispatches a Disruption to its matching Replan* function based on
// its concrete type. The sealed Disruption interface (isDisruption() is
// unexported) makes this switch exhaustive by construction — adding a
// new disruption kind without a case here won't compile-fail, but any
// missing case is caught immediately by the default branch below rather
// than silently doing nothing.
func Apply(
	data models.ShortlistData,
	state *scheduler.ScheduleState,
	d Disruption,
	dayEnd time.Time,
) (*scheduler.ScheduleState, *Diff, error) {
	switch dis := d.(type) {
	case StudentDropoutDisruption:
		return ReplanStudentDropout(state, dis)

	case PanelDropoutDisruption:
		return ReplanPanelDropout(data, state, dis, dayEnd)

	case LateCompanyDisruption:
		return ReplanLateCompany(data, state, dis, dayEnd)

	case RoomUnavailableDisruption:
		return ReplanRoomUnavailable(data, state, dis, dayEnd)

	default:
		return nil, nil, fmt.Errorf("unhandled disruption type: %T", d)
	}
}
