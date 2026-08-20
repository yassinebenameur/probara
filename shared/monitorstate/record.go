package monitorstate

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"time"

	"github.com/google/uuid"
)

// Result holds the columns written for one check_results row by Record.
//
// It is shared by agent and push ingestion (plus their stale workers) and the
// NATS results-ingest consumer, which persists what workers execute. Record is
// the reference implementation of the lock→insert→Apply→update transaction.
type Result struct {
	MonitorID            uuid.UUID
	TenantID             uuid.UUID
	JobID                uuid.UUID
	LocationID           *uuid.UUID // nil = default fleet / location-less ingestion
	Status               string
	ResultSource         string
	HTTPStatus           *int
	LatencyMs            *int64
	ErrorMessage         *string
	MatchedBodySubstring bool
	MetricsData          json.RawMessage
	StartedAt            time.Time
	CompletedAt          time.Time
	// SyntheticAbsence marks watchdog-generated failure evidence for silent
	// push/agent monitors. Its StartedAt is the SERVER clock, while real
	// evidence for those types carries the reporter's clock — so it must
	// neither advance the S-O2 evidence watermark nor be rejected by it
	// (a lagging agent clock would otherwise be locked out after recovery).
	SyntheticAbsence bool
}

// Record inserts a check result and advances the monitor's state machine in a
// single transaction. The monitor row is locked FOR UPDATE so concurrent
// writers serialize their transitions, then Apply derives the new state. It
// returns the applied transition so callers can react to state changes (zero
// value on error).
//
// The insert is idempotent on (job_id, result_source): a redelivered result
// inserts nothing and returns a no-op transition with Duplicate=true, so
// at-least-once NATS delivery can never double-advance the state machine.
//
// Two results are recorded as history without running the machine
// (HistoryOnly=true):
//   - a location-less result for a monitor that has selected locations — the
//     quorum aggregate owns its state and a stale default-fleet job must not
//     clobber it (S-E5, docs/state-semantics.md);
//   - evidence older than the newest already applied on this stream — late
//     redeliveries must never move state backwards (S-O2).
func Record(ctx context.Context, db *sql.DB, r Result) (Transition, error) {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return Transition{}, fmt.Errorf("begin result transaction: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	var snap Snapshot
	var threshold int
	var lastStarted sql.NullTime
	err = tx.QueryRowContext(ctx, `
		SELECT current_state, consecutive_failures, consecutive_failures_threshold, last_result_started_at
		FROM monitors
		WHERE id = $1
		FOR UPDATE
	`, r.MonitorID).Scan(&snap.State, &snap.ConsecutiveFailures, &threshold, &lastStarted)
	if err != nil {
		return Transition{}, fmt.Errorf("lock monitor state: %w", err)
	}

	historyOnly := Transition{
		From:                snap.State,
		To:                  snap.State,
		ConsecutiveFailures: snap.ConsecutiveFailures,
		HistoryOnly:         true,
	}

	// S-E5: the monitor lock serializes this check against SetLocations, so
	// the answer cannot change under us.
	var locationBound bool
	if err := tx.QueryRowContext(ctx, `
		SELECT EXISTS (SELECT 1 FROM monitor_locations WHERE monitor_id = $1)
	`, r.MonitorID).Scan(&locationBound); err != nil {
		return Transition{}, fmt.Errorf("check location binding: %w", err)
	}
	if locationBound {
		if _, err := insertResult(ctx, tx, r); err != nil {
			return Transition{}, err
		}
		if err := tx.Commit(); err != nil {
			return Transition{}, fmt.Errorf("commit result transaction: %w", err)
		}
		return historyOnly, nil
	}

	inserted, err := insertResult(ctx, tx, r)
	if err != nil {
		return Transition{}, err
	}
	if !inserted {
		if err := tx.Commit(); err != nil {
			return Transition{}, fmt.Errorf("commit result transaction: %w", err)
		}
		return Transition{
			From:                snap.State,
			To:                  snap.State,
			ConsecutiveFailures: snap.ConsecutiveFailures,
			Duplicate:           true,
		}, nil
	}

	// S-O2 late-result guard (synthetic absence bypasses it: server clock,
	// not comparable with the reporter's evidence clock).
	if !r.SyntheticAbsence && lastStarted.Valid && r.StartedAt.Before(lastStarted.Time) {
		if err := tx.Commit(); err != nil {
			return Transition{}, fmt.Errorf("commit result transaction: %w", err)
		}
		return historyOnly, nil
	}

	transition := Apply(snap, IsFailureStatus(r.Status), threshold)
	if _, err := tx.ExecContext(ctx, `
		UPDATE monitors
		SET current_state = $1,
			consecutive_failures = $2,
			last_state_change_at = CASE WHEN $3 THEN NOW() ELSE last_state_change_at END,
			last_result_at = NOW(),
			last_result_started_at = CASE WHEN $6 THEN last_result_started_at ELSE $5::timestamptz END,
			updated_at = NOW()
		WHERE id = $4
	`, string(transition.To), transition.ConsecutiveFailures, transition.Changed, r.MonitorID, r.StartedAt, r.SyntheticAbsence); err != nil {
		return Transition{}, fmt.Errorf("update monitor state: %w", err)
	}
	if transition.Changed {
		if err := RecordIntervalTx(ctx, tx, r.TenantID, r.MonitorID, transition.To, IntervalReasonResult); err != nil {
			return Transition{}, err
		}
	}

	if err := tx.Commit(); err != nil {
		return Transition{}, fmt.Errorf("commit result transaction: %w", err)
	}
	return transition, nil
}

// InsertOnly inserts a check result without advancing any state machine.
// Used for platform-sourced rows (e.g. jobs that expired before processing),
// which never represent an observation of the target. Idempotent on
// (job_id, result_source); returns whether a row was actually inserted.
func InsertOnly(ctx context.Context, db *sql.DB, r Result) (bool, error) {
	return insertResult(ctx, db, r)
}

// InsertOnlyTx is InsertOnly inside an existing transaction, for callers that
// combine the insert with their own state handling (the per-location ingest).
func InsertOnlyTx(ctx context.Context, tx *sql.Tx, r Result) (bool, error) {
	return insertResult(ctx, tx, r)
}

// execer is satisfied by *sql.DB and *sql.Tx.
type execer interface {
	ExecContext(ctx context.Context, query string, args ...interface{}) (sql.Result, error)
}

func insertResult(ctx context.Context, db execer, r Result) (bool, error) {
	var metricsData interface{}
	if len(r.MetricsData) > 0 {
		metricsData = []byte(r.MetricsData)
	}

	res, err := db.ExecContext(ctx, `
		INSERT INTO check_results (
			id, monitor_id, tenant_id, job_id, location_id, status, result_source,
			http_status, latency_ms, error_message, matched_body_substring,
			metrics_data, created_at, started_at, completed_at
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15)
		ON CONFLICT (job_id, result_source) DO NOTHING
	`, uuid.New(), r.MonitorID, r.TenantID, r.JobID, r.LocationID, r.Status, r.ResultSource,
		r.HTTPStatus, r.LatencyMs, r.ErrorMessage, r.MatchedBodySubstring, metricsData,
		r.StartedAt, r.StartedAt, r.CompletedAt)
	if err != nil {
		return false, fmt.Errorf("insert check result: %w", err)
	}
	rows, err := res.RowsAffected()
	if err != nil {
		return false, fmt.Errorf("insert check result rows affected: %w", err)
	}
	return rows > 0, nil
}
