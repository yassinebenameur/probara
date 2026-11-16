package monitorstate

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/google/uuid"
)

// Recovery confirmation helpers (S-T7, docs/state-semantics.md).

// FastRecheckInterval is the short cadence that settles a state in progress:
// confirming a failure (suspect) or confirming a recovery (down while passes
// are being counted). The scheduler never stretches a monitor's own interval
// with it, and SIP test calls never get it (S-A9).
const FastRecheckInterval = 20 * time.Second

// RecoveringPredicate returns a SQL boolean that is TRUE when the monitor
// aliased by alias is down while one of its streams counts passes toward a
// recovery: the monitor itself (location-less) or one of its locations. It
// decides the recovering fast recheck, at dispatch and when a pass lands.
func RecoveringPredicate(alias string) string {
	return fmt.Sprintf(`(%[1]s.current_state = 'down' AND (%[1]s.consecutive_successes > 0
		OR EXISTS (SELECT 1 FROM monitor_location_state rls
			WHERE rls.monitor_id = %[1]s.id AND rls.current_state = 'down' AND rls.consecutive_successes > 0)))`, alias)
}

// AfterSilence is the snapshot a stream re-enters the machine from once its
// evidence went stale (S-F2): unknown, with no counts, and owing a recovery
// confirmation when it was down or already owed one (S-T7).
func AfterSilence(s Snapshot) Snapshot {
	return Snapshot{State: StateUnknown, RecoveryPending: s.State == StateDown || s.RecoveryPending}
}

// txQueryExecer is satisfied by *sql.Tx.
type txQueryExecer interface {
	QueryRowContext(ctx context.Context, query string, args ...interface{}) *sql.Row
	ExecContext(ctx context.Context, query string, args ...interface{}) (sql.Result, error)
}

// ResetStaleDownLocationsTx applies AfterSilence to the location rows of one
// monitor that were down when their evidence went stale: unknown, counts
// cleared, the confirmation still owed. Other stale rows keep their state;
// S-A10 already stops counting them. The caller holds the monitor lock (S-O1).
func ResetStaleDownLocationsTx(ctx context.Context, tx txQueryExecer, monitorID uuid.UUID, freshnessSeconds int) error {
	if _, err := tx.ExecContext(ctx, `
		UPDATE monitor_location_state
		SET current_state = 'unknown', consecutive_failures = 0, consecutive_successes = 0,
			recovery_pending = TRUE, last_state_change_at = NOW(), updated_at = NOW()
		WHERE monitor_id = $1 AND current_state = 'down'
		  AND last_check_at < NOW() - make_interval(secs => $2)
	`, monitorID, freshnessSeconds); err != nil {
		return fmt.Errorf("reset stale down locations: %w", err)
	}
	return nil
}

// RefreshRecoveryProgressTx recomputes a location-bound monitor's display-only
// consecutive_successes (RecoveryProgress) from its fresh locations, for paths
// that change the location set or its freshness without re-running the
// machine. The caller holds the monitor lock (S-O1).
func RefreshRecoveryProgressTx(ctx context.Context, tx txQueryExecer, monitorID uuid.UUID) error {
	var quorum, intervalSeconds int
	if err := tx.QueryRowContext(ctx, `
		SELECT location_quorum, interval_seconds FROM monitors WHERE id = $1
	`, monitorID).Scan(&quorum, &intervalSeconds); err != nil {
		return fmt.Errorf("read monitor quorum: %w", err)
	}
	fresh, err := LoadFreshLocationCounts(ctx, tx, monitorID, FreshnessHorizonSeconds(intervalSeconds))
	if err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `
		UPDATE monitors SET consecutive_successes = $2
		WHERE id = $1 AND consecutive_successes <> $2
	`, monitorID, RecoveryProgress(fresh.DownSuccesses, quorum, fresh.Counts.Selected)); err != nil {
		return fmt.Errorf("refresh recovery progress: %w", err)
	}
	return nil
}
