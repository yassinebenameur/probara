package monitorstate

import (
	"context"
	"fmt"

	"github.com/google/uuid"
	"github.com/lib/pq"
)

// Interval reasons: what opened a monitor_state_intervals row. Mirrors the
// CHECK constraint in migration 000082 and the reason vocabulary in
// docs/state-semantics.md.
const (
	IntervalReasonResult         = "result"
	IntervalReasonWatchdogStale  = "watchdog_stale"
	IntervalReasonPause          = "pause"
	IntervalReasonResume         = "resume"
	IntervalReasonLocationChange = "location_change"
	IntervalReasonCreated        = "created"
)

// RecordIntervalTx closes the monitor's open state interval and opens a new
// one for state, inside the caller's transaction (S-U4: the timeline is
// written in the same transaction as the transition it records).
//
// Callers MUST hold the monitor row lock (S-O1); that serialization is what
// makes close-then-open safe against the unique open-interval index.
//
// The lock serializes writers, but NOW() is the *transaction start* time,
// which predates the lock wait. A transaction that began earlier yet acquired
// the lock second would otherwise close an interval opened later than its
// own NOW(), violating `ended_at >= started_at` (seen as a transient ingest
// error under concurrent multi-location results). So the close is clamped at
// the interval's own start, and the new interval begins exactly where the old
// one ended — the timeline stays monotonic and gap-free; a transition that
// lost the race by less than the lock wait records a zero-length interval.
// Both writes happen in one statement so the open interval never has two
// rows, even transiently.
func RecordIntervalTx(ctx context.Context, tx execer, tenantID, monitorID uuid.UUID, state State, reason string) error {
	if _, err := tx.ExecContext(ctx, `
		WITH closed AS (
			UPDATE monitor_state_intervals
			SET ended_at = GREATEST(NOW(), started_at)
			WHERE monitor_id = $1 AND ended_at IS NULL
			RETURNING ended_at
		)
		INSERT INTO monitor_state_intervals (tenant_id, monitor_id, state, reason, started_at)
		SELECT $2, $1, $3, $4, COALESCE((SELECT ended_at FROM closed), NOW())
	`, monitorID, tenantID, string(state), reason); err != nil {
		return fmt.Errorf("close state interval: %w", err)
	}
	return nil
}

// RecordIntervalsTx is RecordIntervalTx for a set of monitors at once, with
// each monitor's tenant read from the monitors table. It carries the same
// monotonic-timeline clamp, so bulk resets (location deletion) queued behind
// an ingest transaction on the S-O1 lock cannot trip the interval check either.
func RecordIntervalsTx(ctx context.Context, tx execer, monitorIDs []uuid.UUID, state State, reason string) error {
	if len(monitorIDs) == 0 {
		return nil
	}
	if _, err := tx.ExecContext(ctx, `
		WITH closed AS (
			UPDATE monitor_state_intervals
			SET ended_at = GREATEST(NOW(), started_at)
			WHERE monitor_id = ANY($1) AND ended_at IS NULL
			RETURNING monitor_id, ended_at
		)
		INSERT INTO monitor_state_intervals (tenant_id, monitor_id, state, reason, started_at)
		SELECT m.tenant_id, m.id, $2, $3, COALESCE(c.ended_at, NOW())
		FROM monitors m
		LEFT JOIN closed c ON c.monitor_id = m.id
		WHERE m.id = ANY($1)
	`, pq.Array(monitorIDs), string(state), reason); err != nil {
		return fmt.Errorf("close state intervals: %w", err)
	}
	return nil
}
