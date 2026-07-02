// Package ingest hosts the platform-side consumer that persists check results
// published by workers over NATS. Workers have no Postgres access (remote
// location workers only ever reach NATS), so this is the single writer of
// check_results for active checks and the place monitor state advances.
package ingest

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/nats-io/nats.go/jetstream"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/sirupsen/logrus"

	"github.com/yassinebenameur/probara/shared/config"
	"github.com/yassinebenameur/probara/shared/db"
	"github.com/yassinebenameur/probara/shared/logger"
	"github.com/yassinebenameur/probara/shared/metrics"
	"github.com/yassinebenameur/probara/shared/models"
	"github.com/yassinebenameur/probara/shared/queue"
	"github.com/yassinebenameur/probara/shared/statusupdates"
)

const (
	checkResultStreamMaxAge = 24 * time.Hour
	consumerRestartBackoff  = 2 * time.Second
)

// statusPublisher is the minimal publishing surface the ingest needs. It is
// satisfied by *statusupdates.Publisher and by test spies.
type statusPublisher interface {
	Publish(event statusupdates.Event) error
}

// Ingest consumes CheckResultMessages from the results stream and persists
// them: insert the check_results row and advance the monitor state machine in
// one transaction (idempotent on job_id+result_source, so at-least-once
// delivery can never double-apply).
type Ingest struct {
	config *config.SchedulerConfig
	logger *logger.Logger
	db     *db.Client
	queue  *queue.Client
	status statusPublisher
	ctx    context.Context
	cancel context.CancelFunc
	wg     sync.WaitGroup

	resultsIngested *prometheus.CounterVec
	ingestErrors    *prometheus.CounterVec
	duplicates      prometheus.Counter
}

// New creates the ingest component. statusPub may be nil (status page updates
// are then skipped).
func New(cfg *config.SchedulerConfig, log *logger.Logger, metricsRegistry *metrics.Registry, dbClient *db.Client, queueClient *queue.Client, statusPub *statusupdates.Publisher) *Ingest {
	ctx, cancel := context.WithCancel(context.Background())

	i := &Ingest{
		config: cfg,
		logger: log,
		db:     dbClient,
		queue:  queueClient,
		ctx:    ctx,
		cancel: cancel,
	}
	if statusPub != nil {
		i.status = statusPub
	}

	i.resultsIngested = metricsRegistry.NewCounter(
		"results_ingested_total",
		"Total number of check results persisted by the ingest consumer",
		[]string{"status"},
	)
	i.ingestErrors = metricsRegistry.NewCounter(
		"result_ingest_errors_total",
		"Total number of check result ingest failures",
		[]string{},
	)
	duplicatesCounter := metricsRegistry.NewCounter(
		"result_ingest_duplicates_total",
		"Total number of redelivered check results skipped as duplicates",
		[]string{},
	)
	i.duplicates = duplicatesCounter.With(prometheus.Labels{})

	return i
}

// Start ensures the results stream, creates the durable consumer and spawns
// the ingest goroutines. It blocks until the context is cancelled.
func (i *Ingest) Start() error {
	i.logger.WithFields(logrus.Fields{
		"stream":      i.config.CheckResultStream,
		"subject":     i.config.CheckResultSubject,
		"consumer":    i.config.ResultIngestConsumerName,
		"concurrency": i.config.ResultIngestConcurrency,
	}).Info("Starting results ingest consumer")

	ctx, cancel := context.WithTimeout(i.ctx, 10*time.Second)
	defer cancel()

	_, err := i.queue.EnsureWorkQueueStream(ctx, i.config.CheckResultStream, []string{i.config.CheckResultSubject}, checkResultStreamMaxAge)
	if err != nil {
		return fmt.Errorf("failed to ensure results stream: %w", err)
	}

	consumer, err := i.queue.CreateConsumer(ctx, i.config.CheckResultStream, i.config.ResultIngestConsumerName)
	if err != nil {
		return fmt.Errorf("failed to create ingest consumer: %w", err)
	}

	// Location worker heartbeats → locations.last_seen_at (connected badge).
	heartbeatSub, err := i.startHeartbeatSubscriber()
	if err != nil {
		i.logger.WithError(err).Warn("Failed to subscribe to location heartbeats; location connection status will be stale")
	} else {
		defer func() { _ = heartbeatSub.Unsubscribe() }()
	}

	for n := 0; n < i.config.ResultIngestConcurrency; n++ {
		i.wg.Add(1)
		go func(id int) {
			defer i.wg.Done()
			i.consumeLoop(i.ctx, consumer, id)
		}(n)
	}

	<-i.ctx.Done()
	i.logger.Info("Ingest context cancelled, waiting for goroutines to finish")
	return nil
}

func (i *Ingest) consumeLoop(ctx context.Context, consumer jetstream.Consumer, id int) {
	handler := func(msg *queue.Message) error {
		return i.handleMessage(ctx, msg)
	}

	for {
		err := i.queue.Consume(ctx, consumer, handler)
		if err == nil || err == context.Canceled {
			break
		}

		i.logger.WithError(err).WithField("ingest_goroutine", id).Error("Ingest goroutine error")

		timer := time.NewTimer(consumerRestartBackoff)
		select {
		case <-ctx.Done():
			timer.Stop()
			return
		case <-timer.C:
		}
	}
}

// handleMessage persists one result message. Returning an error Naks the
// message for redelivery; returning nil Acks it. Poison messages (bad JSON,
// bad UUIDs, unknown monitor) are logged and acked — retrying cannot fix them.
func (i *Ingest) handleMessage(ctx context.Context, msg *queue.Message) error {
	var m models.CheckResultMessage
	if err := json.Unmarshal(msg.Data, &m); err != nil {
		i.logger.WithError(err).Error("Failed to unmarshal check result message")
		return nil
	}

	logEntry := i.logger.WithFields(logrus.Fields{
		"job_id":     m.JobID,
		"monitor_id": m.MonitorID,
		"tenant_id":  m.TenantID,
	})

	if m.Version != "v1" {
		logEntry.WithField("version", m.Version).Error("Unsupported check result version")
		return nil
	}

	result, err := i.buildResult(m)
	if err != nil {
		logEntry.WithError(err).Error("Invalid check result message")
		return nil
	}

	outcome, err := i.persist(ctx, m, result)
	if err != nil {
		if isMonitorGone(err) {
			// The monitor was purged while its last jobs were in flight.
			logEntry.Debug("Dropping result for missing monitor")
			return nil
		}
		logEntry.WithError(err).Error("Failed to persist check result")
		i.ingestErrors.With(prometheus.Labels{}).Inc()
		return err
	}

	if outcome.duplicate {
		i.duplicates.Inc()
		return nil
	}

	i.resultsIngested.With(prometheus.Labels{"status": m.Status}).Inc()

	// Only state transitions are published; per-result events flooded the
	// status-page service (and connected browsers) with no visible change.
	if outcome.stateChanged {
		i.publishStatusUpdate(m.MonitorID, m.TenantID, "state_change")
	}
	return nil
}

func (i *Ingest) buildResult(m models.CheckResultMessage) (monitorResult, error) {
	monitorID, err := uuid.Parse(m.MonitorID)
	if err != nil {
		return monitorResult{}, fmt.Errorf("invalid monitor_id: %w", err)
	}
	tenantID, err := uuid.Parse(m.TenantID)
	if err != nil {
		return monitorResult{}, fmt.Errorf("invalid tenant_id: %w", err)
	}
	jobID, err := uuid.Parse(m.JobID)
	if err != nil {
		return monitorResult{}, fmt.Errorf("invalid job_id: %w", err)
	}
	var locationID *uuid.UUID
	if m.LocationID != "" {
		id, err := uuid.Parse(m.LocationID)
		if err != nil {
			return monitorResult{}, fmt.Errorf("invalid location_id: %w", err)
		}
		locationID = &id
	}
	return monitorResult{
		MonitorID:  monitorID,
		TenantID:   tenantID,
		JobID:      jobID,
		LocationID: locationID,
	}, nil
}

// monitorResult carries the parsed identifiers of one result message.
type monitorResult struct {
	MonitorID  uuid.UUID
	TenantID   uuid.UUID
	JobID      uuid.UUID
	LocationID *uuid.UUID
}

func (i *Ingest) publishStatusUpdate(monitorID, tenantID, eventType string) {
	if i.status == nil {
		return
	}
	event := statusupdates.Event{
		Type:      eventType,
		MonitorID: monitorID,
		TenantID:  tenantID,
		Timestamp: time.Now().UTC(),
	}
	if err := i.status.Publish(event); err != nil {
		i.logger.WithError(err).Warn("Failed to publish status page update")
	}
}

// Shutdown gracefully stops the ingest goroutines.
func (i *Ingest) Shutdown(ctx context.Context) error {
	i.cancel()

	done := make(chan struct{})
	go func() {
		i.wg.Wait()
		close(done)
	}()

	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}
