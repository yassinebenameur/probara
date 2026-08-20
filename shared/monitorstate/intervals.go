package monitorstate

import (
	"context"
	"fmt"

	"github.com/google/uuid"
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
// written in the same transaction as the transition it records). NOW() is
// constant within a transaction, so the closed interval's ended_at equals the
// new interval's started_at — the timeline has no gaps.
//
// Callers MUST hold the monitor row lock (S-O1); that serialization is what
// makes close-then-open safe against the unique open-interval index.
func RecordIntervalTx(ctx context.Context, tx execer, tenantID, monitorID uuid.UUID, state State, reason string) error {
	if _, err := tx.ExecContext(ctx, `
		UPDATE monitor_state_intervals
		SET ended_at = NOW()
		WHERE monitor_id = $1 AND ended_at IS NULL
	`, monitorID); err != nil {
		return fmt.Errorf("close state interval: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO monitor_state_intervals (tenant_id, monitor_id, state, reason, started_at)
		VALUES ($1, $2, $3, $4, NOW())
	`, tenantID, monitorID, string(state), reason); err != nil {
		return fmt.Errorf("open state interval: %w", err)
	}
	return nil
}
