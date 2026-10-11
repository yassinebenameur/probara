package airca

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/sirupsen/logrus"

	"github.com/yassinebenameur/probara/shared/ai"
	"github.com/yassinebenameur/probara/shared/config"
	"github.com/yassinebenameur/probara/shared/db"
	"github.com/yassinebenameur/probara/shared/logger"
	"github.com/yassinebenameur/probara/shared/queue"
)

// Automatic alert triage is DISPLAY ONLY: it writes alert_triage and nothing
// else. It never reads or writes alert status, severity, routing or
// notification state, so a model outage or a bug here cannot change who is
// paged or when. The relay (relay.go) claims rows and publishes TriageJob;
// this consumer runs the same analyzer as incident analyses over
// single-monitor evidence (buildAlertInput).

// TriageJobVersion is the current TriageJob schema version.
const TriageJobVersion = 1

// triageMaxAge is the AI_TRIAGE stream's MaxAge. A triage is only worth
// showing while the alert is fresh; the relay fails rows on its own clock
// (triageNotStartedAfter), so an expired message never strands a row.
const triageMaxAge = time.Hour

// triageDuplicatesWindow is the AI_TRIAGE stream's Nats-Msg-Id window. The
// relay publishes with the triage id as message id, and every republish of a
// row lands inside this window after its first stored publish, so a
// republish of a job still queued, in delivery, or recently consumed is
// de-duplicated instead of adding a copy; only a publish that never reached
// the stream (or a store the broker lost) is restored. JetStream keeps ids
// after the message is consumed (TestWorkQueueDuplicatesWindowOutlivesConsumption).
const triageDuplicatesWindow = 30 * time.Minute

// TriageJob is the AI_TRIAGE payload. Worker-internal: only the relay
// publishes it, so it does not live in shared/ai.
type TriageJob struct {
	V        int       `json:"v"`
	TriageID uuid.UUID `json:"triage_id"`
	AlertID  uuid.UUID `json:"alert_id"`
	TenantID uuid.UUID `json:"tenant_id"`
}

// Operator-facing triage messages. Fixed strings only (never error text, which
// may carry infrastructure detail) and no trailing period: the UI composes
// them into its own sentence.
const (
	triageUnavailableMessage   = "local AI unavailable or returned invalid decisions"
	triageFinalDeliveryMessage = "the triage failed after repeated errors"
	triageTimeoutMessage       = "the triage timed out"               // relay, started but pending past triageFailAfter
	triageNotStartedMessage    = "the triage was not started in time" // relay, never delivered within triageNotStartedAfter
)

// Skip reasons, mirrored by the alert_triage.skip_reason CHECK.
const (
	triageSkipResolved   = "resolved"
	triageSkipCooldown   = "cooldown"
	triageSkipNotModeled = "not_modeled"
	triageSkipDisabled   = "disabled"
)

// TriageConsumer runs alert triage jobs pulled from the AI_TRIAGE stream, with
// the same hardening as the AI_RCA Consumer (heartbeat, per-delivery deadline,
// RetryAfter, final-delivery failure, lost-race detection).
type TriageConsumer struct {
	cfg         *config.WorkerConfig
	logger      *logger.Logger
	db          *db.Client
	queue       *queue.Client
	envAnalyzer ai.RootCauseAnalyzer // may be nil when no local AI is configured

	heartbeatEvery     time.Duration // heartbeatInterval; shortened by tests
	deliveryHeadroom   time.Duration // ackWaitHeadroom; shortened by tests
	consumerCheckEvery time.Duration // consumerCheckInterval; shortened by tests
}

// NewTriageConsumer constructs the triage consumer. A nil analyzer records
// failed triages; alerting is unaffected either way.
func NewTriageConsumer(cfg *config.WorkerConfig, log *logger.Logger, dbc *db.Client, q *queue.Client, envAnalyzer ai.RootCauseAnalyzer) *TriageConsumer {
	return &TriageConsumer{cfg: cfg, logger: log, db: dbc, queue: q, envAnalyzer: envAnalyzer,
		heartbeatEvery: heartbeatInterval, deliveryHeadroom: ackWaitHeadroom, consumerCheckEvery: consumerCheckInterval}
}

// triageSpec names the AI_TRIAGE stream and durable.
func triageSpec(cfg *config.WorkerConfig) streamSpec {
	return streamSpec{Stream: cfg.AITriageStream, Subject: cfg.AITriageSubject, Consumer: cfg.AITriageConsumerName,
		Label: "AI triage", MaxAge: triageMaxAge, Duplicates: triageDuplicatesWindow}
}

// ensureTriageStream provisions the AI_TRIAGE work-queue stream. The relay and
// the consumer both call it with triageSpec, so whichever starts (or
// restarts) last leaves the same config.
func ensureTriageStream(ctx context.Context, q *queue.Client, cfg *config.WorkerConfig) error {
	_, err := ensureStream(ctx, q, triageSpec(cfg))
	return err
}

// Start ensures the AI_TRIAGE stream + consumer exist, then blocks on Consume
// until ctx is cancelled. Callers run it under RunWithRestart.
func (c *TriageConsumer) Start(ctx context.Context) error {
	return runConsumer(ctx, c.queue, c.logger, c.cfg.LocalAI.Timeout, c.consumerCheckEvery, triageSpec(c.cfg), c.handle)
}

// handle decodes one job, runs it under an InProgress heartbeat and one
// delivery deadline, and settles the outcome like the AI_RCA consumer.
func (c *TriageConsumer) handle(ctx context.Context, msg *queue.Message) error {
	var job TriageJob
	if err := json.Unmarshal(msg.Data, &job); err != nil {
		c.logger.WithError(err).WithField("subject", msg.Subject).Error("Failed to unmarshal AI triage job; dropping")
		return nil
	}
	if job.V != TriageJobVersion {
		c.logger.WithField("version", job.V).Error("Unsupported AI triage job version; dropping")
		return nil
	}
	entry := c.logger.WithFields(logrus.Fields{
		"triage_id": job.TriageID,
		"alert_id":  job.AlertID,
		"tenant_id": job.TenantID,
		"delivery":  msg.NumDelivered,
	})
	// Same single delivery deadline as Consumer.handle: the lock, evidence
	// queries, model call and persistence all run under it, which bounds how
	// long a delivery holds the row (the relay's triageFailAfter relies on it).
	procCtx, cancel := context.WithTimeout(ctx, c.cfg.LocalAI.Timeout+c.deliveryHeadroom)
	stopHeartbeat := startHeartbeat(procCtx, msg, c.heartbeatEvery, entry)
	err := c.processTriage(procCtx, entry, job)
	stopHeartbeat()
	cancel()
	return settleDelivery(ctx, entry, msg, err, func(markCtx context.Context) (bool, error) {
		return c.markTriageFailed(markCtx, job, triageFinalDeliveryMessage)
	})
}

// triageRow is what the consumer needs to decide whether to run a triage.
type triageRow struct {
	status      string
	createdAt   time.Time
	alertStatus string
	kind        string
	monitorID   uuid.NullUUID
	resolvedAt  sql.NullTime
	aiEnabled   bool
	autoEnabled bool
	cooldown    int
}

// loadTriage reads the row bound to the job's tenant and alert. A job whose
// ids do not all match one row (forged or cross-tenant) finds nothing.
func (c *TriageConsumer) loadTriage(ctx context.Context, job TriageJob) (triageRow, error) {
	var r triageRow
	err := c.db.QueryRowContext(ctx, `
		SELECT t.status, t.created_at, a.status::text, a.kind, a.monitor_id, a.resolved_at,
		       COALESCE(s.enabled, TRUE), COALESCE(s.auto_triage_enabled, FALSE), COALESCE(s.triage_cooldown_seconds, 900)
		FROM alert_triage t
		JOIN alerts a ON a.id = t.alert_id AND a.tenant_id = t.tenant_id
		LEFT JOIN ai_settings s ON s.tenant_id = t.tenant_id
		WHERE t.id = $1 AND t.tenant_id = $2 AND t.alert_id = $3`,
		job.TriageID, job.TenantID, job.AlertID).Scan(&r.status, &r.createdAt, &r.alertStatus, &r.kind,
		&r.monitorID, &r.resolvedAt, &r.aiEnabled, &r.autoEnabled, &r.cooldown)
	return r, err
}

// processTriage runs one triage under its advisory lock. Any returned error is
// transient (retry) except errJobBusy; terminal outcomes are recorded on the
// row and return nil.
func (c *TriageConsumer) processTriage(ctx context.Context, entry *logrus.Entry, job TriageJob) error {
	return withJobLock(ctx, c.db, job.TriageID, entry, func() error { return c.processTriageLocked(ctx, entry, job) })
}

func (c *TriageConsumer) processTriageLocked(ctx context.Context, entry *logrus.Entry, job TriageJob) error {
	row, err := c.loadTriage(ctx, job)
	if errors.Is(err, sql.ErrNoRows) {
		entry.Warn("Triage row not found for this tenant and alert; dropping")
		return nil
	}
	if err != nil {
		entry.WithError(err).Error("Failed to load triage")
		return err
	}
	if row.status != string(pendingStatus) {
		entry.WithField("status", row.status).Debug("Triage already processed; dropping")
		return nil
	}

	// Mark the first delivery: the relay's run timeout (triageFailAfter)
	// counts from here, so time spent queued behind a backlog never reads as
	// a timed-out analysis.
	if err := c.markTriageStarted(ctx, job); err != nil {
		entry.WithError(err).Error("Failed to mark triage started")
		return err
	}

	// AI_TRIAGE_ENABLED=false stops new claims in the relay and drains rows
	// already queued here without running the model.
	if !c.cfg.AITriageEnabled || c.cfg.LocalAI.Disabled || !row.aiEnabled || !row.autoEnabled {
		return c.finishSkipped(ctx, entry, job, triageSkipDisabled)
	}
	if row.alertStatus == "resolved" || row.resolvedAt.Valid {
		return c.finishSkipped(ctx, entry, job, triageSkipResolved)
	}
	if row.kind == "mesh_edge" || !row.monitorID.Valid {
		return c.finishSkipped(ctx, entry, job, triageSkipNotModeled)
	}
	if row.cooldown > 0 {
		// Two alerts on the same monitor (different kinds) triaged at the same
		// moment can both pass this check, since neither is ready yet. That
		// costs one extra analysis, never a wrong result, so no lock is taken.
		var cooling bool
		if err := c.db.QueryRowContext(ctx, `
			SELECT EXISTS(SELECT 1 FROM alert_triage p WHERE p.tenant_id=$1 AND p.monitor_id=$2 AND p.id<>$3
			  AND p.status='ready' AND p.completed_at >= NOW() - make_interval(secs => $4))`,
			job.TenantID, row.monitorID.UUID, job.TriageID, row.cooldown).Scan(&cooling); err != nil {
			entry.WithError(err).Error("Failed to check triage cooldown")
			return err
		}
		if cooling {
			return c.finishSkipped(ctx, entry, job, triageSkipCooldown)
		}
	}
	if c.envAnalyzer == nil {
		entry.Warn("No local AI analyzer configured; marking triage failed")
		return c.finishTriageFailed(ctx, entry, job, triageUnavailableMessage)
	}

	// The claim time is the evidence request time, so a redelivery or a
	// republish analyses the same window as the first attempt.
	input, err := buildAlertInput(ctx, c.db, job.TenantID, job.AlertID, row.createdAt)
	if errors.Is(err, errNotModeled) {
		return c.finishSkipped(ctx, entry, job, triageSkipNotModeled)
	}
	if errors.Is(err, sql.ErrNoRows) {
		entry.Warn("Alert vanished while building triage evidence; dropping")
		return nil
	}
	if err != nil {
		entry.WithError(err).Error("Failed to build triage evidence")
		return err
	}

	analysisCtx, cancel := context.WithTimeout(ctx, c.cfg.LocalAI.Timeout)
	defer cancel()
	result, err := c.envAnalyzer.Analyze(analysisCtx, input)
	if err != nil {
		if ctx.Err() != nil {
			// Shutdown or the delivery deadline cut the call short; that says
			// nothing about the local AI, so retry rather than record a verdict.
			return ctx.Err()
		}
		entry.WithError(err).Warn("Triage analysis failed")
		return c.finishTriageFailed(ctx, entry, job, triageUnavailableMessage)
	}

	updated, err := c.markTriageReady(ctx, job, result)
	if err != nil {
		entry.WithError(err).Error("Failed to persist triage result")
		return err
	}
	if !updated {
		logLostRace(entry, "ready")
		return nil
	}
	entry.WithFields(logrus.Fields{"hypothesis": result.Metadata.Hypothesis, "confidence": result.Confidence}).Info("Triage completed")
	return nil
}

func (c *TriageConsumer) finishSkipped(ctx context.Context, entry *logrus.Entry, job TriageJob, reason string) error {
	updated, err := c.markTriageSkipped(ctx, job, reason)
	if err != nil {
		entry.WithError(err).Error("Failed to persist triage skip")
		return err
	}
	if !updated {
		logLostRace(entry, "skipped")
		return nil
	}
	entry.WithField("skip_reason", reason).Debug("Triage skipped")
	return nil
}

func (c *TriageConsumer) finishTriageFailed(ctx context.Context, entry *logrus.Entry, job TriageJob, message string) error {
	updated, err := c.markTriageFailed(ctx, job, message)
	if err != nil {
		entry.WithError(err).Error("Failed to persist triage failure")
		return err
	}
	if !updated {
		logLostRace(entry, "failed")
	}
	return nil
}
