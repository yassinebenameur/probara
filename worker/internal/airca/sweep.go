package airca

import (
	"context"
	"fmt"
	"time"

	"github.com/sirupsen/logrus"

	"github.com/yassinebenameur/probara/shared/db"
	"github.com/yassinebenameur/probara/shared/logger"
)

const (
	// staleAfter is how old a pending analysis must be before the sweep fails
	// it. Invariant: it exceeds the longest a single delivery can legitimately
	// hold the row — the local AI deadline (at most maxLocalAITimeout) plus
	// ackWaitHeadroom for evidence loading and persistence — so a run that is
	// within its deadline is never swept. Rows a run is actively processing
	// are additionally protected by the advisory lock (see sweepStaleSQL),
	// which makes this a bound on queue wait, not a race against the model.
	staleAfter = 10 * time.Minute

	// lockCeiling is the age past which the sweep fails a pending row even
	// while its advisory lock is held. A delivery can only take the lock on a
	// row the sweep has not failed yet (any unlocked row older than
	// staleAfter goes on the next tick) and the handler's deadline releases
	// it within maxLocalAITimeout+ackWaitHeadroom, so a lock still held this
	// late belongs to a Postgres session that outlived its worker (node lost
	// without FIN/RST; the server keeps the backend until TCP keepalive gives
	// up, ~2h by default). Failing a row a run still holds is safe anyway:
	// markReady/markFailed treat a non-pending row as a lost race.
	// TestLockCeilingCoversLongestDeliveryChain pins the margin.
	lockCeiling = 30 * time.Minute

	// SweepInterval is how often RunStaleSweep looks for stale pending rows.
	SweepInterval = time.Minute

	// maxLocalAITimeout mirrors the LOCAL_AI_TIMEOUT_SECONDS upper bound (300)
	// enforced by loadLocalAIConfig in shared/config; TestLocalAITimeoutBound
	// pins the pair.
	maxLocalAITimeout = 300 * time.Second

	// sweepBatch caps one sweep statement; leftovers go on the next tick.
	sweepBatch = 500

	staleFailureMessage = "the analysis timed out"
)

// Compile-time guard for the staleAfter invariant: the array length goes
// negative (a build error) if staleAfter <= maxLocalAITimeout+ackWaitHeadroom.
// Counted in seconds: a nanosecond length overflows int on 32-bit targets.
var _ [(staleAfter-maxLocalAITimeout-ackWaitHeadroom)/time.Second - 1]struct{}

// ValidateSweepWindow is the startup twin of the compile-time guard, checked
// against the configured (not just the maximum) local AI timeout.
func ValidateSweepWindow(localAITimeout time.Duration) error {
	if localAITimeout+ackWaitHeadroom >= staleAfter {
		return fmt.Errorf("local AI timeout %s plus %s ack headroom must stay under the %s stale-pending window", localAITimeout, ackWaitHeadroom, staleAfter)
	}
	return nil
}

// sweepStaleSQL fails stale pending analyses across all tenants (maintenance,
// like retention cleanup: it updates by primary key and never reads tenant
// data). Safe with several worker replicas: FOR UPDATE SKIP LOCKED splits the
// candidates between concurrent sweeps, the advisory try-lock skips rows a
// consumer currently holds unless they are past lockCeiling (the CTE is
// materialized because it locks rows and calls a volatile function, so the
// try-lock only runs on candidates), and the status='pending' re-check keeps
// the write guarded like markFailed.
var sweepStaleSQL = `
WITH candidates AS (
	SELECT id, created_at FROM incident_ai_analyses
	WHERE status = 'pending' AND created_at < NOW() - make_interval(secs => $1)
	ORDER BY created_at
	LIMIT $2
	FOR UPDATE SKIP LOCKED
), unclaimed AS (
	SELECT id FROM candidates
	WHERE created_at < NOW() - make_interval(secs => $6)
	   OR pg_try_advisory_xact_lock(` + jobLockKeySQL("id::text") + `)
)
UPDATE incident_ai_analyses a
SET status = 'failed', error_message = $3, completed_at = NOW(),
    metadata = $4::jsonb, recommended_actions = $5::jsonb
FROM unclaimed u
WHERE a.id = u.id AND a.status = 'pending'`

// SweepStale fails pending analyses older than staleAfter and returns how many
// rows it updated. It replaces the timeout the API used to apply on read, with
// one gap: it runs only inside DB-connected workers, so while none is running
// (scaled to zero, crashlooping, no POSTGRES_URL) pending rows stay pending
// until one comes back and sweeps them.
func SweepStale(ctx context.Context, dbc *db.Client) (int64, error) {
	tx, err := dbc.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer func() { _ = tx.Rollback() }()
	res, err := tx.ExecContext(ctx, sweepStaleSQL, staleAfter.Seconds(), sweepBatch,
		staleFailureMessage, failedAnalysisMetadata, failedAnalysisActions, lockCeiling.Seconds())
	if err != nil {
		return 0, err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return 0, err
	}
	// Commit releases the transaction-scoped advisory locks.
	return n, tx.Commit()
}

// RunStaleSweep sweeps every interval until ctx is cancelled. A failed sweep
// is logged and retried on the next tick; it returns only on cancellation.
func RunStaleSweep(ctx context.Context, log *logger.Logger, dbc *db.Client, interval time.Duration) error {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		n, err := SweepStale(ctx, dbc)
		switch {
		case ctx.Err() != nil:
			return ctx.Err()
		case err != nil:
			log.WithError(err).Warn("Stale AI analysis sweep failed")
		case n > 0:
			log.WithFields(logrus.Fields{"failed": n, "stale_after": staleAfter.String()}).Info("Failed stale pending AI analyses")
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
		}
	}
}
