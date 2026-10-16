package airca

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"time"

	"github.com/sirupsen/logrus"

	"github.com/yassinebenameur/probara/shared/ai"
	"github.com/yassinebenameur/probara/shared/config"
	"github.com/yassinebenameur/probara/shared/db"
	"github.com/yassinebenameur/probara/shared/logger"
	"github.com/yassinebenameur/probara/shared/queue"
)

// Max delivery covers transient persistence failures and a local AI that is
// temporarily unavailable (ai.ErrUnavailable); other analyzer failures are
// recorded once (incident analyses then offer an operator retry).
const maxDeliver = 3

// ackWaitHeadroom is added to the local AI timeout to form the consumer's
// AckWait: evidence loading and persistence run outside the model deadline.
const ackWaitHeadroom = 30 * time.Second

// consumerCheckInterval is how often a running Start confirms its durable
// still exists on the server (see watchConsumer).
const consumerCheckInterval = 30 * time.Second

// heartbeatInterval keeps JetStream from redelivering a job mid-analysis. It
// must stay well under the smallest AckWait (1s minimum timeout + headroom).
const heartbeatInterval = 15 * time.Second

// Operator-facing failure messages. Like every markFailed message they never
// carry error text, which may hold infrastructure detail.
const (
	// The web panel renders "Analysis failed: <message>. Try again.", so
	// failure messages carry no trailing period or retry instruction.
	finalDeliveryFailureMessage = "the analysis failed after repeated errors"
	analysisUnavailableMessage  = "Local AI service unavailable or returned invalid decisions; check service readiness"
)

// Consumer runs AI root cause analyses pulled from the AI_RCA stream. The
// workspace enablement is resolved per job; the installation-owned analyzer
// handles every enabled workspace.
type Consumer struct {
	cfg         *config.WorkerConfig
	logger      *logger.Logger
	db          *db.Client
	queue       *queue.Client
	envAnalyzer ai.RootCauseAnalyzer // may be nil when no env local AI configured

	heartbeatEvery     time.Duration // heartbeatInterval; shortened by tests
	deliveryHeadroom   time.Duration // ackWaitHeadroom; shortened by tests
	consumerCheckEvery time.Duration // consumerCheckInterval; shortened by tests
}

// New constructs the consumer. A nil installation analyzer produces failed
// analyses with a retry action while check execution continues.
func New(cfg *config.WorkerConfig, log *logger.Logger, dbClient *db.Client, queueClient *queue.Client, envAnalyzer ai.RootCauseAnalyzer) *Consumer {
	return &Consumer{cfg: cfg, logger: log, db: dbClient, queue: queueClient, envAnalyzer: envAnalyzer,
		heartbeatEvery: heartbeatInterval, deliveryHeadroom: ackWaitHeadroom, consumerCheckEvery: consumerCheckInterval}
}

// rcaSpec names the AI_RCA stream and durable.
func rcaSpec(cfg *config.WorkerConfig) streamSpec {
	return streamSpec{Stream: cfg.AIRCAStream, Subject: cfg.AIRCASubject, Consumer: cfg.AIRCAConsumerName, Label: "AI RCA", MaxAge: 24 * time.Hour}
}

// Start ensures the AI_RCA stream + consumer exist, then blocks on Consume
// until ctx is cancelled. It returns on any Consume failure; callers run it
// under RunWithRestart.
func (c *Consumer) Start(ctx context.Context) error {
	return runConsumer(ctx, c.queue, c.logger, c.cfg.LocalAI.Timeout, c.consumerCheckEvery, rcaSpec(c.cfg), c.handle)
}

// handle decodes one job, runs it under an InProgress heartbeat, and turns the
// outcome into an ack (nil), a delayed retry, or — on the final delivery — a
// recorded failure, so no row is left pending once JetStream stops retrying.
func (c *Consumer) handle(ctx context.Context, msg *queue.Message) error {
	var job ai.AnalysisJob
	if err := json.Unmarshal(msg.Data, &job); err != nil {
		c.logger.WithError(err).WithField("subject", msg.Subject).Error("Failed to unmarshal AI RCA job; dropping")
		return nil
	}
	if job.V != ai.AnalysisJobVersion {
		c.logger.WithField("version", job.V).Error("Unsupported AI RCA job version; dropping")
		return nil
	}

	entry := c.logger.WithFields(logrus.Fields{
		"analysis_id": job.AnalysisID,
		"incident_id": job.IncidentID,
		"delivery":    msg.NumDelivered,
	})

	// One deadline for the whole delivery, not just the model call: the
	// advisory lock, evidence queries and persistence all run under it. This
	// is what makes "a delivery holds the row for at most LocalAI.Timeout +
	// ackWaitHeadroom" true, which the sweep's staleAfter window relies on.
	// The heartbeat stops at the same deadline, so even a driver call that
	// outlives cancellation (lib/pq waits for the server to confirm a cancel
	// request) cannot keep the message in progress: JetStream redelivers it
	// after AckWait.
	procCtx, cancel := context.WithTimeout(ctx, c.cfg.LocalAI.Timeout+c.deliveryHeadroom)
	stopHeartbeat := startHeartbeat(procCtx, msg, c.heartbeatEvery, entry)
	err := c.process(procCtx, entry, job)
	stopHeartbeat()
	cancel()
	return c.settle(ctx, entry, msg, job, err)
}

// settle maps a process error onto the queue outcome; on the final delivery
// the analysis is recorded failed with finalDeliveryFailureMessage, or
// analysisUnavailableMessage when the local AI was still unavailable.
func (c *Consumer) settle(ctx context.Context, entry *logrus.Entry, msg *queue.Message, job ai.AnalysisJob, err error) error {
	return settleDelivery(ctx, entry, msg, err, func(markCtx context.Context) (bool, error) {
		message := finalDeliveryFailureMessage
		if errors.Is(err, ai.ErrUnavailable) {
			message = analysisUnavailableMessage
		}
		return c.markFailed(markCtx, job, message)
	})
}

// process runs one analysis. Any returned error is transient (retry) except
// errAnalysisBusy; terminal outcomes are recorded on the row and return nil.
func (c *Consumer) process(ctx context.Context, entry *logrus.Entry, job ai.AnalysisJob) error {
	// Bind queued IDs to one tenant/incident before gathering any evidence.
	// Serialize duplicate deliveries with an advisory lock on one pinned
	// connection. The stale-pending sweep takes the same lock key
	// (jobLockKeySQL), so it can never fail a row while a run holds it.
	return withJobLock(ctx, c.db, job.AnalysisID, entry, func() error { return c.processLocked(ctx, entry, job) })
}

// processLocked is process under the analysis lock.
func (c *Consumer) processLocked(ctx context.Context, entry *logrus.Entry, job ai.AnalysisJob) error {
	status, requestedAt, err := c.loadStatus(ctx, job)
	if errors.Is(err, sql.ErrNoRows) {
		entry.Warn("Analysis row no longer exists; dropping")
		return nil
	}
	if err != nil {
		entry.WithError(err).Error("Failed to load analysis status")
		return err // infra error — retry
	}
	if status != string(pendingStatus) {
		entry.WithField("status", status).Debug("Analysis already processed; dropping")
		return nil
	}

	// Resolve shared workspace enablement before gathering any evidence.
	analyzer, err := c.resolveAnalyzer(ctx, job.TenantID)
	if err != nil {
		entry.WithError(err).Error("Failed to resolve analyzer config")
		return err // transient database error — retry
	}
	if analyzer == nil {
		entry.Warn("No local AI configured for tenant; marking analysis failed")
		return c.finishFailed(ctx, entry, job, "Local AI is disabled for this workspace or installation")
	}

	input, err := buildAnalysisInput(ctx, c.db, job.TenantID, job.IncidentID, requestedAt)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			// Incident or evidence vanished — terminal, record and ack.
			entry.WithError(err).Warn("Incident not found while building analysis context")
			return c.finishFailed(ctx, entry, job, "incident not found")
		}
		entry.WithError(err).Error("Failed to build analysis context")
		return err // likely transient DB error — retry
	}

	analysisCtx, cancel := context.WithTimeout(ctx, c.cfg.LocalAI.Timeout)
	defer cancel()
	result, err := analyzer.Analyze(analysisCtx, input)
	if err != nil {
		if ctx.Err() != nil {
			// Shutdown or the delivery deadline cut the call short; that says
			// nothing about the local AI, so retry rather than record a verdict.
			return ctx.Err()
		}
		if errors.Is(err, ai.ErrUnavailable) {
			// Queue full, model loading or the pod restarting: retried with
			// deliveryRetryDelay so a restart needs no operator click.
			return err
		}
		// Other analyzer failures (invalid responses, a model that timed
		// out) are terminal for this run; the operator retries from the UI.
		entry.WithError(err).Warn("Analysis failed")
		return c.finishFailed(ctx, entry, job, analysisUnavailableMessage)
	}

	updated, err := c.markReady(ctx, job, result)
	if err != nil {
		entry.WithError(err).Error("Failed to persist analysis result")
		return err // retry the persist
	}
	if !updated {
		logLostRace(entry, "ready")
		return nil
	}
	entry.WithField("confidence", result.Confidence).Info("Analysis completed")
	return nil
}

// finishFailed records a terminal failure and acks (returns nil). If the write
// itself fails it returns the error so JetStream retries.
func (c *Consumer) finishFailed(ctx context.Context, entry *logrus.Entry, job ai.AnalysisJob, message string) error {
	updated, err := c.markFailed(ctx, job, message)
	if err != nil {
		entry.WithError(err).Error("Failed to persist analysis failure")
		return err
	}
	if !updated {
		logLostRace(entry, "failed")
	}
	return nil
}
