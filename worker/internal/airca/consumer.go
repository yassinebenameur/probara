package airca

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/sirupsen/logrus"

	"github.com/yassinebenameur/probara/shared/ai"
	"github.com/yassinebenameur/probara/shared/config"
	"github.com/yassinebenameur/probara/shared/db"
	"github.com/yassinebenameur/probara/shared/logger"
	"github.com/yassinebenameur/probara/shared/queue"
	"github.com/yassinebenameur/probara/shared/secrets"
)

// retryBackOff is the JetStream redelivery schedule for transient (infra)
// failures. Analyzer/data failures are terminal: they are recorded on the row
// as 'failed' and acked, so they don't retry here — the operator re-runs via
// the UI's Retry button.
var retryBackOff = []time.Duration{
	10 * time.Second,
	30 * time.Second,
}

// maxDeliver matches len(retryBackOff)+1.
const maxDeliver = 3

// Consumer runs AI root cause analyses pulled from the AI_RCA stream. The
// analyzer is resolved per job: a tenant's ai_settings row wins, otherwise the
// envAnalyzer (built from LLM_* env vars) is the fallback.
type Consumer struct {
	cfg         *config.WorkerConfig
	logger      *logger.Logger
	db          *db.Client
	queue       *queue.Client
	envAnalyzer ai.RootCauseAnalyzer // may be nil when no env LLM configured
	encryptor   secrets.Encryptor
}

// New constructs the consumer. envAnalyzer may be nil; in that case only tenants
// with their own ai_settings row can run analyses.
func New(cfg *config.WorkerConfig, log *logger.Logger, dbClient *db.Client, queueClient *queue.Client, envAnalyzer ai.RootCauseAnalyzer, encryptor secrets.Encryptor) *Consumer {
	if encryptor == nil {
		encryptor = secrets.NoOpEncryptor{}
	}
	return &Consumer{
		cfg:         cfg,
		logger:      log,
		db:          dbClient,
		queue:       queueClient,
		envAnalyzer: envAnalyzer,
		encryptor:   encryptor,
	}
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

	// AckWait must outlast a single analysis (LLM round-trip) plus headroom so
	// JetStream doesn't redeliver while we're still calling the model.
	ackWait := time.Duration(c.cfg.LLMTimeoutSeconds)*time.Second + 30*time.Second
	consumer, err := c.queue.CreateConsumerWithOptions(ctx, stream, consumerName, queue.ConsumerOptions{
		AckWait:    ackWait,
		MaxDeliver: maxDeliver,
		BackOff:    retryBackOff,
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

	// Idempotency: skip rows that are gone or already finished.
	status, err := c.loadStatus(ctx, job.AnalysisID)
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

	// Resolve which LLM to use for this tenant (per-tenant row or env fallback).
	analyzer, err := c.resolveAnalyzer(ctx, job.TenantID)
	if err != nil {
		entry.WithError(err).Error("Failed to resolve analyzer config")
		return err // transient DB/decrypt error — retry
	}
	if analyzer == nil {
		entry.Warn("No LLM configured for tenant; marking analysis failed")
		return c.finishFailed(ctx, entry, job.AnalysisID, "AI analysis is not configured for this workspace")
	}

	input, err := buildAnalysisInput(ctx, c.db, job.TenantID, job.IncidentID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			// Incident or evidence vanished — terminal, record and ack.
			entry.WithError(err).Warn("Incident not found while building analysis context")
			return c.finishFailed(ctx, entry, job.AnalysisID, "incident not found")
		}
		entry.WithError(err).Error("Failed to build analysis context")
		return err // likely transient DB error — retry
	}

	result, err := analyzer.Analyze(ctx, input)
	if err != nil {
		// Analyzer failures (provider down, refusal, bad config) are terminal
		// for this run; the operator retries from the UI.
		entry.WithError(err).Warn("Analysis failed")
		return c.finishFailed(ctx, entry, job.AnalysisID, err.Error())
	}

	if err := c.markReady(ctx, job.AnalysisID, result); err != nil {
		entry.WithError(err).Error("Failed to persist analysis result")
		return err // retry the persist
	}
	entry.WithField("confidence", result.Confidence).Info("Analysis completed")
	return nil
}

// finishFailed records a terminal failure and acks (returns nil). If the write
// itself fails it returns the error so JetStream retries.
func (c *Consumer) finishFailed(ctx context.Context, entry *logrus.Entry, id uuid.UUID, message string) error {
	if err := c.markFailed(ctx, id, message); err != nil {
		entry.WithError(err).Error("Failed to persist analysis failure")
		return err
	}
	return nil
}
