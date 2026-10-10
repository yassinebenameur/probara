package ingest

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/yassinebenameur/probara/shared/monitorstate"
)

// persistAtLocation writes one location-pinned result: apply the temporal
// state machine to the (monitor, location) row, then aggregate across the
// monitor's selected locations into monitors.current_state via the quorum
// rule — all in one transaction.
//
// Lock ordering: the monitor row is ALWAYS locked first (FOR UPDATE), before
// any monitor_location_state access, so concurrent per-location results for
// the same monitor serialize on the monitor lock and can never deadlock.
// Location-set mutations (monitors.SetLocations, locations Delete) follow the
// same order — see docs/state-semantics.md S-O1.
func (i *Ingest) persistAtLocation(ctx context.Context, r monitorstate.Result) (outcome, error) {
	tx, err := i.db.BeginTx(ctx, nil)
	if err != nil {
		return outcome{}, fmt.Errorf("begin result transaction: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	var monitorState string
	var threshold, quorum, intervalSeconds int
	err = tx.QueryRowContext(ctx, `
		SELECT current_state, consecutive_failures_threshold, location_quorum, interval_seconds
		FROM monitors
		WHERE id = $1
		FOR UPDATE
	`, r.MonitorID).Scan(&monitorState, &threshold, &quorum, &intervalSeconds)
	if err != nil {
		return outcome{}, fmt.Errorf("lock monitor state: %w", err)
	}

	inserted, err := monitorstate.InsertOnlyTx(ctx, tx, r)
	if err != nil {
		return outcome{}, err
	}
	if !inserted {
		if err := tx.Commit(); err != nil {
			return outcome{}, fmt.Errorf("commit result transaction: %w", err)
		}
		return outcome{duplicate: true}, nil
	}

	// A job published before the monitor's location set was edited may arrive
	// for a location that is no longer selected: keep the history row, but
	// never resurrect state for it.
	var selected bool
	err = tx.QueryRowContext(ctx, `
		SELECT EXISTS (
			SELECT 1 FROM monitor_locations
			WHERE monitor_id = $1 AND location_id = $2
		)
	`, r.MonitorID, r.LocationID).Scan(&selected)
	if err != nil {
		return outcome{}, fmt.Errorf("check location selection: %w", err)
	}
	if !selected {
		if err := tx.Commit(); err != nil {
			return outcome{}, fmt.Errorf("commit result transaction: %w", err)
		}
		return outcome{}, nil
	}

	// Per-location temporal state machine (same rules as the global one).
	var locSnap monitorstate.Snapshot
	var lastStarted sql.NullTime
	err = tx.QueryRowContext(ctx, `
		SELECT current_state, consecutive_failures, last_result_started_at
		FROM monitor_location_state
		WHERE monitor_id = $1 AND location_id = $2
	`, r.MonitorID, r.LocationID).Scan(&locSnap.State, &locSnap.ConsecutiveFailures, &lastStarted)
	if err == sql.ErrNoRows {
		locSnap = monitorstate.Snapshot{State: monitorstate.StateUnknown}
	} else if err != nil {
		return outcome{}, fmt.Errorf("read location state: %w", err)
	}

	// S-O2 late-result guard: evidence older than the newest already applied
	// on this location's stream keeps its history row but never moves state.
	if lastStarted.Valid && r.StartedAt.Before(lastStarted.Time) {
		if err := tx.Commit(); err != nil {
			return outcome{}, fmt.Errorf("commit result transaction: %w", err)
		}
		return outcome{}, nil
	}

	locTransition := monitorstate.Apply(locSnap, monitorstate.IsFailureStatus(r.Status), threshold)
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO monitor_location_state (
			monitor_id, location_id, tenant_id, current_state,
			consecutive_failures, last_latency_ms, last_check_at,
			last_result_started_at, last_state_change_at, updated_at
		) VALUES ($1, $2, $3, $4, $5, $6, NOW(), $8, CASE WHEN $7 THEN NOW() END, NOW())
		ON CONFLICT (monitor_id, location_id) DO UPDATE SET
			current_state = EXCLUDED.current_state,
			consecutive_failures = EXCLUDED.consecutive_failures,
			last_latency_ms = EXCLUDED.last_latency_ms,
			last_check_at = NOW(),
			last_result_started_at = EXCLUDED.last_result_started_at,
			last_state_change_at = CASE WHEN $7 THEN NOW() ELSE monitor_location_state.last_state_change_at END,
			updated_at = NOW()
	`, r.MonitorID, r.LocationID, r.TenantID, string(locTransition.To),
		locTransition.ConsecutiveFailures, r.LatencyMs, locTransition.Changed, r.StartedAt); err != nil {
		return outcome{}, fmt.Errorf("upsert location state: %w", err)
	}

	// Spatial aggregation over the currently selected, live locations with
	// FRESH evidence (S-A10) — shared with the absence watchdog.
	counts, maxFailures, err := monitorstate.LoadFreshLocationCounts(
		ctx, tx, r.MonitorID, monitorstate.FreshnessHorizonSeconds(intervalSeconds))
	if err != nil {
		return outcome{}, err
	}

	newState := monitorstate.AggregateLocations(counts, quorum)
	changed := string(newState) != monitorState
	if _, err := tx.ExecContext(ctx, `
		UPDATE monitors
		SET current_state = $1,
			consecutive_failures = $2,
			last_state_change_at = CASE WHEN $3 THEN NOW() ELSE last_state_change_at END,
			last_result_at = NOW(),
			updated_at = NOW()
		WHERE id = $4
	`, string(newState), maxFailures, changed, r.MonitorID); err != nil {
		return outcome{}, fmt.Errorf("update monitor state: %w", err)
	}
	if changed {
		if err := monitorstate.RecordIntervalTx(ctx, tx, r.TenantID, r.MonitorID, newState, monitorstate.IntervalReasonResult); err != nil {
			return outcome{}, err
		}
	}

	if err := tx.Commit(); err != nil {
		return outcome{}, fmt.Errorf("commit result transaction: %w", err)
	}
	return outcome{stateChanged: changed}, nil
}
