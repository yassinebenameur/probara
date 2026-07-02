package ingest

import (
	"context"
	"database/sql"
	"errors"
	"strings"

	"github.com/yassinebenameur/probara/shared/models"
	"github.com/yassinebenameur/probara/shared/monitorstate"
)

// outcome reports what persisting one result actually did.
type outcome struct {
	duplicate    bool // row already existed (redelivery); nothing applied
	stateChanged bool // the monitor's current_state changed
}

// persist writes one result message. Platform-sourced rows (jobs that expired
// before processing) are insert-only — they never observed the target, so the
// state machine must not run. Monitor-sourced rows go through the shared
// lock→insert→Apply→update transaction.
func (i *Ingest) persist(ctx context.Context, m models.CheckResultMessage, ids monitorResult) (outcome, error) {
	result := monitorstate.Result{
		MonitorID:            ids.MonitorID,
		TenantID:             ids.TenantID,
		JobID:                ids.JobID,
		LocationID:           ids.LocationID,
		Status:               m.Status,
		ResultSource:         m.ResultSource,
		HTTPStatus:           m.HTTPStatus,
		LatencyMs:            m.LatencyMs,
		ErrorMessage:         m.ErrorMessage,
		MatchedBodySubstring: m.MatchedBodySubstring,
		MetricsData:          m.MetricsData,
		StartedAt:            m.StartedAt,
		CompletedAt:          m.CompletedAt,
	}

	if m.ResultSource == string(models.ResultSourcePlatform) {
		inserted, err := monitorstate.InsertOnly(ctx, i.db.DB, result)
		if err != nil {
			return outcome{}, err
		}
		return outcome{duplicate: !inserted}, nil
	}

	// Location-pinned results run the per-location machine + quorum
	// aggregation; location-less results keep the legacy global machine.
	if ids.LocationID != nil {
		return i.persistAtLocation(ctx, result)
	}

	transition, err := monitorstate.Record(ctx, i.db.DB, result)
	if err != nil {
		return outcome{}, err
	}
	return outcome{duplicate: transition.Duplicate, stateChanged: transition.Changed}, nil
}

// isMonitorGone reports whether persisting failed because the monitor row no
// longer exists (purged while jobs were in flight). Such results are dropped:
// the FOR UPDATE lock finds no row, or the insert violates the monitors FK.
func isMonitorGone(err error) bool {
	if errors.Is(err, sql.ErrNoRows) {
		return true
	}
	return strings.Contains(err.Error(), "violates foreign key constraint")
}
