package airca

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/sirupsen/logrus"

	"github.com/yassinebenameur/probara/shared/ai"
	"github.com/yassinebenameur/probara/shared/config"
	"github.com/yassinebenameur/probara/shared/db"
	"github.com/yassinebenameur/probara/shared/logger"
	"github.com/yassinebenameur/probara/shared/queue"
)

// Max delivery covers transient persistence failures; service failures are
// recorded once with an operator retry action.
const maxDeliver = 3

// Consumer runs AI root cause analyses pulled from the AI_RCA stream. The
// workspace enablement is resolved per job; the installation-owned analyzer
// handles every enabled workspace.
type Consumer struct {
	cfg         *config.WorkerConfig
	logger      *logger.Logger
	db          *db.Client
	queue       *queue.Client
	envAnalyzer ai.RootCauseAnalyzer // may be nil when no env local AI configured
}

// New constructs the consumer. A nil installation analyzer produces failed
// analyses with a retry action while check execution continues.
func New(cfg *config.WorkerConfig, log *logger.Logger, dbClient *db.Client, queueClient *queue.Client, envAnalyzer ai.RootCauseAnalyzer) *Consumer {
	return &Consumer{cfg: cfg, logger: log, db: dbClient, queue: queueClient, envAnalyzer: envAnalyzer}
}

// Start ensures the AI_RCA stream + consumer exist, then blocks on Consume
// until ctx is cancelled.
func (c *Consumer) Start(ctx context.Context) error {
	stream := c.cfg.AIRCAStream
	subject := c.cfg.AIRCASubject
	consumerName := c.cfg.AIRCAConsumerName

	if _, err := c.queue.EnsureWorkQueueStream(ctx, stream, []string{subject}, 24*time.Hour); err != nil {
		return fmt.Errorf("ensure ai rca stream: %w", err)
	}

	// AckWait must outlast a single analysis (local AI round-trip) plus headroom so
	// JetStream doesn't redeliver while we're still calling the model.
	ackWait := c.cfg.LocalAI.Timeout + 30*time.Second
	consumer, err := c.queue.CreateConsumerWithOptions(ctx, stream, consumerName, queue.ConsumerOptions{
		AckWait:    ackWait,
		MaxDeliver: maxDeliver,
		BackOff:    []time.Duration{ackWait, 2 * ackWait},
	})
	if err != nil {
		return fmt.Errorf("create ai rca consumer: %w", err)
	}

	c.logger.WithFields(logrus.Fields{
		"stream":   stream,
		"subject":  subject,
		"consumer": consumerName,
	}).Info("AI root cause consumer started")

	return c.queue.Consume(ctx, consumer, func(msg *queue.Message) error {
		return c.handle(ctx, msg)
	})
}

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
	})

	// Bind queued IDs to one tenant/incident before gathering any evidence.
	// Serialize duplicate deliveries with an advisory lock on one pinned connection.
	conn, err := c.db.DB.Conn(ctx)
	if err != nil {
		return err
	}
	defer conn.Close()
	var claimed bool
	if err := conn.QueryRowContext(ctx, `SELECT pg_try_advisory_lock(hashtextextended($1,0))`, job.AnalysisID.String()).Scan(&claimed); err != nil {
		return err
	}
	if !claimed {
		return fmt.Errorf("analysis already being processed")
	}
	defer func() {
		unlockCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		var unlocked bool
		if err := conn.QueryRowContext(unlockCtx, `SELECT pg_advisory_unlock(hashtextextended($1,0))`, job.AnalysisID.String()).Scan(&unlocked); err != nil || !unlocked {
			entry.WithError(err).Warn("Failed to release analysis lock; discarding database session")
			_ = conn.Raw(func(any) error { return driver.ErrBadConn })
		}
	}()
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
		// Analyzer failures (service outage, queue saturation, invalid responses) are terminal
		// for this run; the operator retries from the UI.
		entry.WithError(err).Warn("Analysis failed")
		return c.finishFailed(ctx, entry, job, "Local AI service unavailable or returned invalid decisions. Retry after checking service readiness.")
	}

	if err := c.markReady(ctx, job, result); err != nil {
		entry.WithError(err).Error("Failed to persist analysis result")
		return err // retry the persist
	}
	entry.WithField("confidence", result.Confidence).Info("Analysis completed")
	return nil
}

// finishFailed records a terminal failure and acks (returns nil). If the write
// itself fails it returns the error so JetStream retries.
func (c *Consumer) finishFailed(ctx context.Context, entry *logrus.Entry, job ai.AnalysisJob, message string) error {
	if err := c.markFailed(ctx, job, message); err != nil {
		entry.WithError(err).Error("Failed to persist analysis failure")
		return err
	}
	return nil
}
