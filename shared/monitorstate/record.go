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
// It carries the subset of columns produced outside the worker (agent and push
// ingestion, plus their stale workers). The worker has its own richer insert
// (HTTP body matching, etc.) and remains the reference implementation of the
// lock→insert→Apply→update transaction this mirrors.
type Result struct {
	MonitorID    uuid.UUID
	TenantID     uuid.UUID
	JobID        uuid.UUID
	Status       string
	ResultSource string
	HTTPStatus   *int
	LatencyMs    *int64
	ErrorMessage *string
	MetricsData  json.RawMessage
	StartedAt    time.Time
	CompletedAt  time.Time
}

// Record inserts a check result and advances the monitor's state machine in a
// single transaction. The monitor row is locked FOR UPDATE so concurrent
// writers (ingestion vs. the stale worker) serialize their transitions, then
// Apply derives the new state. It returns the applied transition so callers can
// react to state changes (zero value on error).
//
// This is the missing piece for agent and push monitors: their results used to
// be inserted without ever advancing monitors.current_state, leaving dashboard
// health frozen at the seed value.
func Record(ctx context.Context, db *sql.DB, r Result) (Transition, error) {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return Transition{}, fmt.Errorf("begin result transaction: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	var snap Snapshot
	var threshold int
	err = tx.QueryRowContext(ctx, `
		SELECT current_state, consecutive_failures, consecutive_failures_threshold
		FROM monitors
		WHERE id = $1
		FOR UPDATE
	`, r.MonitorID).Scan(&snap.State, &snap.ConsecutiveFailures, &threshold)
	if err != nil {
		return Transition{}, fmt.Errorf("lock monitor state: %w", err)
	}

	var metricsData interface{}
	if len(r.MetricsData) > 0 {
		metricsData = []byte(r.MetricsData)
	}

	if _, err := tx.ExecContext(ctx, `
		INSERT INTO check_results (
			id, monitor_id, tenant_id, job_id, status, result_source, http_status,
			latency_ms, error_message, metrics_data, created_at, started_at, completed_at
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13)
	`, uuid.New(), r.MonitorID, r.TenantID, r.JobID, r.Status, r.ResultSource,
		r.HTTPStatus, r.LatencyMs, r.ErrorMessage, metricsData,
		r.StartedAt, r.StartedAt, r.CompletedAt); err != nil {
		return Transition{}, fmt.Errorf("insert check result: %w", err)
	}

	transition := Apply(snap, IsFailureStatus(r.Status), threshold)
	if _, err := tx.ExecContext(ctx, `
		UPDATE monitors
		SET current_state = $1,
			consecutive_failures = $2,
			last_state_change_at = CASE WHEN $3 THEN NOW() ELSE last_state_change_at END,
			updated_at = NOW()
		WHERE id = $4
	`, string(transition.To), transition.ConsecutiveFailures, transition.Changed, r.MonitorID); err != nil {
		return Transition{}, fmt.Errorf("update monitor state: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return Transition{}, fmt.Errorf("commit result transaction: %w", err)
	}
	return transition, nil
}
