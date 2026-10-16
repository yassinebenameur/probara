package airca

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/lib/pq"
	"github.com/sirupsen/logrus"

	"github.com/yassinebenameur/probara/shared/ai"
	"github.com/yassinebenameur/probara/shared/config"
	"github.com/yassinebenameur/probara/shared/db"
	"github.com/yassinebenameur/probara/shared/logger"
	"github.com/yassinebenameur/probara/shared/queue"
)

// The triage relay turns open alerts into AI_TRIAGE jobs. It runs only in
// DB-connected platform workers and touches only alert_triage: claiming an
// alert never changes it, its routing or its pages.

const (
	// RelayInterval is how often the relay ticks.
	RelayInterval = 15 * time.Second

	// triageRepublishAfter is how long a queued row (never delivered) waits
	// before the relay publishes its job again. Republishes carry the same
	// Nats-Msg-Id and land inside the stream's triageDuplicatesWindow, so
	// they restore only a publish that never reached the stream (a crash
	// between commit and publish, an ambiguous NATS error, a broker that
	// lost its store); a job still queued or in delivery is de-duplicated,
	// never copied.
	triageRepublishAfter = 3 * time.Minute

	// maxTriageAttempts caps publishes that reached NATS per row (the claim +
	// 2 republishes). A failed publish is undone, so a NATS outage delays a
	// row instead of using up its attempts.
	maxTriageAttempts = 3

	// triageFailAfter fails a row whose first delivery (started_at) is this
	// old and that is still pending. Invariant: it exceeds the longest
	// delivery chain — the AckWait BackOff gaps (1x and 2x
	// maxLocalAITimeout+ackWaitHeadroom) plus a last full run, with the retry
	// delays (at most unavailableRetryMax each, for a local AI that is
	// loading or saturated), ≈26m. Queue wait does not count (triageNotStartedAfter
	// covers it). The consumer's guarded writes treat a row failed here as a
	// lost race, so the relay takes no per-job lock.
	triageFailAfter = 30 * time.Minute

	// triageNotStartedAfter fails a row that no delivery ever reached. An
	// unconsumed job leaves the stream triageMaxAge after its first stored
	// publish and de-duplicated republishes never renew it, so past this no
	// delivery can come: the backlog (or a NATS outage) outlasted the job.
	triageNotStartedAfter = triageMaxAge + 10*time.Minute

	// triageLookback bounds which alerts are claimed (ai.TriageLookback,
	// shared with the API's SLA cause breakdown).
	triageLookback = ai.TriageLookback

	// triageMaxIntervalWait caps the two-check-intervals part of the claim
	// wait, so alerts of monitors checking less often than every 30 minutes
	// are claimed an hour after they open rather than never (two intervals of
	// a daily monitor would outlast triageLookback).
	triageMaxIntervalWait = time.Hour

	// maxTriageDelay mirrors the ai_settings_triage_delay_range upper bound.
	maxTriageDelay = time.Hour

	// relayMaxPending bounds the platform-wide backlog: a tick claims only
	// while fewer rows than this are pending, so an alert storm cannot queue
	// more work than the local AI can get through before the jobs expire.
	// Tenants share it oldest-alert first.
	relayMaxPending = 200
	relayMaintBatch = 500

	// relayTxTimeout bounds a tick's transaction, and relayStatementTimeout
	// is set on the server inside it (statement and idle-in-transaction), so
	// a tick stalled on a dead connection cannot keep the platform-wide relay
	// lock (and stop triage everywhere) until TCP keepalive gives up.
	relayTxTimeout        = 2 * RelayInterval
	relayStatementTimeout = "20s"

	// relayPublishTimeout bounds one publish.
	relayPublishTimeout = 5 * time.Second

	// relayLockWarnTicks: consecutive ticks that found the relay lock held
	// before Run logs a warning (no tick holds it longer than relayTxTimeout).
	relayLockWarnTicks = 20
)

// Compile-time guards for the invariants above (array length goes negative,
// a build error, when one breaks). Counted in seconds to stay within int on
// 32-bit targets.
var (
	_ [(triageFailAfter-4*(maxLocalAITimeout+ackWaitHeadroom)-(maxDeliver-1)*unavailableRetryMax)/time.Second - 1]struct{}
	_ [(triageNotStartedAfter-triageMaxAge-(maxTriageAttempts-1)*triageRepublishAfter-RelayInterval)/time.Second - 1]struct{}
	_ [(triageDuplicatesWindow-(maxTriageAttempts-1)*triageRepublishAfter-2*RelayInterval)/time.Second - 1]struct{}
	_ [(triageMaxAge-triageDuplicatesWindow)/time.Second + 1]struct{} // JetStream: Duplicates <= MaxAge
	_ [(triageLookback-triageMaxIntervalWait-maxTriageDelay)/time.Second - 1]struct{}
	_ [(relayTxTimeout-RelayInterval)/time.Second - 1]struct{}
)

// relayLockSQL serializes relay ticks across worker replicas: a tick that
// cannot take it does nothing. That makes the per-tenant hourly cap and the
// backlog bound exact; ON CONFLICT (alert_id) DO NOTHING keeps one row per
// alert regardless.
var relayLockSQL = `SELECT pg_try_advisory_xact_lock(hashtextextended('probara.ai_triage_relay', 0))`

// relayTimeoutsSQL makes the server abort a stalled tick and free the lock.
var relayTimeoutsSQL = []string{
	`SET LOCAL statement_timeout = '` + relayStatementTimeout + `'`,
	`SET LOCAL idle_in_transaction_session_timeout = '` + relayStatementTimeout + `'`,
}

// relayClaimSQL claims open alerts of tenants with auto-triage on, oldest
// first, within each tenant's hourly cap and the platform backlog bound.
// "Open" includes acknowledged: an operator who acks in the first minute
// still gets a triage. The wait is the tenant delay but at least two check
// intervals (capped at triageMaxIntervalWait), so the alert's own checks are
// in the window; alerts that resolve within it are never claimed. Mesh
// alerts (no monitor) are claimed and skipped by the consumer as not
// modeled; alerts of deleted monitors are not claimed. Skipped rows do not
// count against the cap. Eligibility (open, within the lookback, live
// monitor, workspace AI and auto-triage on) is built from the shared/ai
// fragments TriageClaimableAlertSQL and TriageSettingsEnabledSQL, which the
// API's SLA "Triage pending" bucket (triageview.ClaimableExpr) uses too.
var relayClaimSQL = `
WITH settings AS (
    SELECT ais.tenant_id, ais.triage_delay_seconds, ais.triage_max_per_hour
    FROM ai_settings ais WHERE ` + ai.TriageSettingsEnabledSQL("ais") + `
), used AS (
    SELECT t.tenant_id, COUNT(*) AS n
    FROM alert_triage t JOIN settings s ON s.tenant_id = t.tenant_id
    WHERE t.created_at > NOW() - INTERVAL '1 hour' AND t.status <> 'skipped'
    GROUP BY t.tenant_id
), candidates AS (
    SELECT a.id AS alert_id, a.tenant_id, a.monitor_id, a.triggered_at,
           ROW_NUMBER() OVER (PARTITION BY a.tenant_id ORDER BY a.triggered_at, a.id) AS rn,
           s.triage_max_per_hour - COALESCE(u.n, 0) AS room
    FROM alerts a
    JOIN settings s ON s.tenant_id = a.tenant_id
    LEFT JOIN used u ON u.tenant_id = a.tenant_id
    LEFT JOIN monitors m ON m.id = a.monitor_id AND m.tenant_id = a.tenant_id
    WHERE ` + ai.TriageClaimableAlertSQL("a", "NOW() - make_interval(secs => $1)") + `
      AND a.triggered_at <= NOW() - make_interval(secs =>
            GREATEST(s.triage_delay_seconds, LEAST(2 * COALESCE(m.interval_seconds, 0), $3::int)))
      AND NOT EXISTS (SELECT 1 FROM alert_triage x WHERE x.alert_id = a.id)
)
INSERT INTO alert_triage (tenant_id, alert_id, monitor_id, status, enqueued_at, attempts)
SELECT tenant_id, alert_id, monitor_id, 'pending', NOW(), 1
FROM candidates WHERE rn <= room
ORDER BY triggered_at, alert_id
LIMIT GREATEST(0, $2::int - (SELECT COUNT(*) FROM alert_triage WHERE status = 'pending'))
ON CONFLICT (alert_id) DO NOTHING
RETURNING id, tenant_id, alert_id`

// relayRepublishSQL re-arms queued rows (no delivery yet) whose last publish
// is older than triageRepublishAfter, up to maxTriageAttempts publishes and
// only while the job could still be delivered.
var relayRepublishSQL = `
UPDATE alert_triage t SET enqueued_at = NOW(), attempts = t.attempts + 1
FROM (SELECT id FROM alert_triage
      WHERE status = 'pending' AND started_at IS NULL
        AND enqueued_at < NOW() - make_interval(secs => $1)
        AND attempts < $2 AND created_at >= NOW() - make_interval(secs => $3)
      ORDER BY enqueued_at LIMIT $4 FOR UPDATE SKIP LOCKED) c
WHERE t.id = c.id AND t.status = 'pending'
RETURNING t.id, t.tenant_id, t.alert_id`

// relayUndoPublishSQL gives back the attempt of rows whose publish failed.
// enqueued_at stays, so the row is retried once triageRepublishAfter passes.
var relayUndoPublishSQL = `
UPDATE alert_triage SET attempts = attempts - 1
WHERE id = ANY($1::uuid[]) AND status = 'pending' AND attempts > 0`

// relayFailSQL fails rows that started but are still pending
// triageFailAfter later, and rows no delivery reached within
// triageNotStartedAfter of their claim, so no alert shows "Triaging…"
// forever. Each gets the message for what actually happened.
var relayFailSQL = `
UPDATE alert_triage t
SET status = 'failed', metadata = $2::jsonb, completed_at = NOW(),
    error_message = CASE WHEN t.started_at IS NULL THEN $5 ELSE $1 END
FROM (SELECT id FROM alert_triage
      WHERE status = 'pending'
        AND (started_at < NOW() - make_interval(secs => $3)
             OR (started_at IS NULL AND created_at < NOW() - make_interval(secs => $6)))
      ORDER BY created_at LIMIT $4 FOR UPDATE SKIP LOCKED) c
WHERE t.id = c.id AND t.status = 'pending'`

// triagePublisher is the slice of queue.Client the relay needs; tests swap it.
type triagePublisher interface {
	Publish(ctx context.Context, subject string, data []byte, headers map[string][]string) error
}

// Relay claims alerts for triage and publishes their jobs.
type Relay struct {
	cfg      *config.WorkerConfig
	log      *logger.Logger
	db       *db.Client
	queue    *queue.Client
	pub      triagePublisher
	interval time.Duration

	maxPending int // relayMaxPending; lowered by tests
}

// TickResult reports one relay tick.
type TickResult struct {
	LockHeld      bool // another replica's tick held the relay lock; nothing done
	Claimed       int
	Republished   int
	Failed        int
	Published     int
	PublishErrors int
}

// NewRelay constructs the relay.
func NewRelay(cfg *config.WorkerConfig, log *logger.Logger, dbc *db.Client, q *queue.Client) *Relay {
	r := &Relay{cfg: cfg, log: log, db: dbc, queue: q, interval: RelayInterval, maxPending: relayMaxPending}
	if q != nil {
		r.pub = q
	}
	return r
}

// Run ensures the AI_TRIAGE stream, then ticks at once and every interval
// until ctx is cancelled. A failed tick is logged and retried on the next one.
// It keeps running with AI_TRIAGE_ENABLED=false or LOCAL_AI_DISABLED=true:
// claims stop, but republish and the timeout still drain pending rows.
func (r *Relay) Run(ctx context.Context) error {
	if r.queue != nil {
		if err := ensureTriageStream(ctx, r.queue, r.cfg); err != nil {
			return err
		}
	}
	ticker := time.NewTicker(r.interval)
	defer ticker.Stop()
	lockHeldTicks := 0
	for {
		res, err := r.Tick(ctx)
		if res.LockHeld {
			lockHeldTicks++
		} else {
			lockHeldTicks = 0
		}
		switch {
		case ctx.Err() != nil:
			return ctx.Err()
		case err != nil:
			r.log.WithError(err).Warn("AI triage relay tick failed")
		case lockHeldTicks > 0 && lockHeldTicks%relayLockWarnTicks == 0:
			// No healthy tick holds the lock this long: a session stuck
			// holding it means no workspace gets triaged.
			r.log.WithField("ticks", lockHeldTicks).Warn("AI triage relay lock has been held by another session for many ticks; no alerts are being claimed")
		case res.Claimed+res.Republished+res.Failed+res.PublishErrors > 0:
			r.log.WithFields(logrus.Fields{
				"claimed": res.Claimed, "republished": res.Republished, "failed": res.Failed,
				"published": res.Published, "publish_errors": res.PublishErrors,
			}).Info("AI triage relay tick")
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
		}
	}
}

type triageRef struct{ id, tenant, alert uuid.UUID }

// Tick runs one relay pass: lock, claim (unless disabled), republish, fail,
// commit, then publish. Publishing only after commit means a consumer never
// sees a job whose row is not yet visible; a crash between commit and publish
// leaves the row pending for the republish to pick up.
func (r *Relay) Tick(ctx context.Context) (TickResult, error) {
	publish, res, err := r.claimTx(ctx)
	if err != nil || res.LockHeld {
		return res, err
	}

	var undo []string
	for i, ref := range publish {
		pubCtx, cancel := context.WithTimeout(ctx, relayPublishTimeout)
		err := r.publish(pubCtx, ref)
		cancel()
		if err != nil {
			// NATS is down or unreachable: stop for this tick rather than
			// wait out a timeout per row, and give every unpublished row its
			// attempt back so the outage only delays it.
			res.PublishErrors = len(publish) - i
			for _, rest := range publish[i:] {
				undo = append(undo, rest.id.String())
			}
			r.log.WithError(err).WithFields(logrus.Fields{"triage_id": ref.id, "alert_id": ref.alert, "tenant_id": ref.tenant, "unpublished": res.PublishErrors}).
				Warn("Failed to publish AI triage jobs; they will be republished")
			break
		}
		res.Published++
	}
	if len(undo) > 0 {
		undoCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
		defer cancel()
		if _, err := r.db.ExecContext(undoCtx, relayUndoPublishSQL, pq.StringArray(undo)); err != nil {
			// The rows stay pending with the attempt spent; the republish
			// still recovers them while attempts remain.
			r.log.WithError(err).Warn("Failed to return attempts of unpublished AI triage jobs")
		}
	}
	return res, nil
}

// claimTx runs the transactional part of a tick under the relay lock and the
// tick's time bounds, and returns the jobs to publish once it committed.
func (r *Relay) claimTx(ctx context.Context) ([]triageRef, TickResult, error) {
	var res TickResult
	ctx, cancel := context.WithTimeout(ctx, relayTxTimeout)
	defer cancel()
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, res, err
	}
	defer func() { _ = tx.Rollback() }()

	for _, q := range relayTimeoutsSQL {
		if _, err := tx.ExecContext(ctx, q); err != nil {
			return nil, res, err
		}
	}
	var locked bool
	if err := tx.QueryRowContext(ctx, relayLockSQL).Scan(&locked); err != nil {
		return nil, res, err
	}
	if !locked {
		res.LockHeld = true
		return nil, res, nil
	}

	var publish []triageRef
	if r.cfg.AITriageEnabled && !r.cfg.LocalAI.Disabled {
		claimed, err := queryRefs(ctx, tx, relayClaimSQL, triageLookback.Seconds(), r.maxPending, int(triageMaxIntervalWait.Seconds()))
		if err != nil {
			return nil, res, err
		}
		res.Claimed = len(claimed)
		publish = append(publish, claimed...)
	}
	republished, err := queryRefs(ctx, tx, relayRepublishSQL, triageRepublishAfter.Seconds(), maxTriageAttempts, triageNotStartedAfter.Seconds(), relayMaintBatch)
	if err != nil {
		return nil, res, err
	}
	res.Republished = len(republished)
	publish = append(publish, republished...)

	failed, err := tx.ExecContext(ctx, relayFailSQL, triageTimeoutMessage, failedAnalysisMetadata, triageFailAfter.Seconds(),
		relayMaintBatch, triageNotStartedMessage, triageNotStartedAfter.Seconds())
	if err != nil {
		return nil, res, err
	}
	n, err := failed.RowsAffected()
	if err != nil {
		return nil, res, err
	}
	res.Failed = int(n)

	if err := tx.Commit(); err != nil {
		return nil, res, err
	}
	return publish, res, nil
}

func (r *Relay) publish(ctx context.Context, ref triageRef) error {
	if r.pub == nil {
		return errors.New("no triage publisher configured")
	}
	data, err := json.Marshal(TriageJob{V: TriageJobVersion, TriageID: ref.id, AlertID: ref.alert, TenantID: ref.tenant})
	if err != nil {
		return err
	}
	// The triage id as Nats-Msg-Id: the stream de-duplicates every publish
	// of one row inside triageDuplicatesWindow.
	return r.pub.Publish(ctx, r.cfg.AITriageSubject, data, queue.MsgIDHeaders(ref.id.String()))
}

// queryRefs runs a statement returning (id, tenant_id, alert_id) rows.
func queryRefs(ctx context.Context, tx *sql.Tx, query string, args ...any) ([]triageRef, error) {
	rows, err := tx.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	refs := []triageRef{}
	for rows.Next() {
		var ref triageRef
		if err := rows.Scan(&ref.id, &ref.tenant, &ref.alert); err != nil {
			return nil, err
		}
		refs = append(refs, ref)
	}
	return refs, rows.Err()
}
