package worker

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

const checkJobStreamMaxAge = 24 * time.Hour

// Worker represents the worker service
type Worker struct {
	config  *config.WorkerConfig
	logger  *logger.Logger
	metrics *metrics.Registry
	db      *db.Client
	queue   *queue.Client
	status  *statusupdates.Publisher
	ctx     context.Context
	cancel  context.CancelFunc
	wg      sync.WaitGroup

	// Checker registry for extensible monitor types
	checkerRegistry *CheckerRegistry

	// Passive monitor types that don't need active checking
	passiveTypes map[string]bool

	// Metrics
	jobsTotal           *prometheus.CounterVec
	jobDuration         *prometheus.HistogramVec
	httpRequestDuration *prometheus.HistogramVec
	httpErrors          *prometheus.CounterVec
	natsAckTotal        prometheus.Counter
	natsNakTotal        prometheus.Counter
	dbWriteErrors       prometheus.Counter
}

// NewWorker creates a new worker instance
func NewWorker(cfg *config.WorkerConfig, log *logger.Logger, metricsRegistry *metrics.Registry, dbClient *db.Client, queueClient *queue.Client, statusPublisher *statusupdates.Publisher) *Worker {
	ctx, cancel := context.WithCancel(context.Background())

	w := &Worker{
		config:          cfg,
		logger:          log,
		metrics:         metricsRegistry,
		db:              dbClient,
		queue:           queueClient,
		status:          statusPublisher,
		ctx:             ctx,
		cancel:          cancel,
		checkerRegistry: NewDefaultRegistry(cfg.MaxBodySizeBytes, cfg.HTTPBlockPrivateIPs, cfg.HTTPAllowedCIDRs, cfg.SyntheticArtifactsDir),
		passiveTypes: map[string]bool{
			"agent": true, // Agent monitors receive pushed metrics
			"group": true, // Group monitors aggregate member results
		},
	}

	// Initialize metrics
	w.jobsTotal = metricsRegistry.NewCounter(
		"jobs_total",
		"Total number of executed jobs per status",
		[]string{"status"},
	)
	w.jobDuration = metricsRegistry.NewHistogram(
		"job_duration_seconds",
		"Duration of job processing including HTTP and DB",
		[]string{"status"},
		nil,
	)
	w.httpRequestDuration = metricsRegistry.NewHistogram(
		"http_request_duration_seconds",
		"HTTP request latency only",
		[]string{},
		nil,
	)
	w.httpErrors = metricsRegistry.NewCounter(
		"http_errors_total",
		"HTTP error count breakdown",
		[]string{"reason"},
	)
	natsAckCounter := metricsRegistry.NewCounter(
		"nats_ack_total",
		"Total number of NATS messages acknowledged",
		[]string{},
	)
	w.natsAckTotal = natsAckCounter.With(prometheus.Labels{})

	natsNakCounter := metricsRegistry.NewCounter(
		"nats_nak_total",
		"Total number of NATS messages negatively acknowledged",
		[]string{},
	)
	w.natsNakTotal = natsNakCounter.With(prometheus.Labels{})

	dbWriteErrorsCounter := metricsRegistry.NewCounter(
		"db_write_errors_total",
		"Total number of database write errors",
		[]string{},
	)
	w.dbWriteErrors = dbWriteErrorsCounter.With(prometheus.Labels{})

	return w
}

// RegisterChecker registers a custom checker for a monitor type
func (w *Worker) RegisterChecker(monitorType string, checker Checker) {
	w.checkerRegistry.Register(monitorType, checker)
}

// RegisterPassiveType marks a monitor type as passive (no active checking)
func (w *Worker) RegisterPassiveType(monitorType string) {
	w.passiveTypes[monitorType] = true
}

// Start starts the worker loop
func (w *Worker) Start() error {
	w.logger.WithFields(logrus.Fields{
		"worker_concurrency":  w.config.WorkerConcurrency,
		"consumer_name":       w.config.NATSConsumerName,
		"stream":              w.config.CheckJobStream,
		"subject":             w.config.CheckJobSubject,
		"registered_checkers": w.checkerRegistry.Types(),
	}).Info("Starting worker service")

	// Ensure JetStream stream exists
	ctx, cancel := context.WithTimeout(w.ctx, 10*time.Second)
	defer cancel()

	_, err := w.queue.EnsureWorkQueueStream(ctx, w.config.CheckJobStream, []string{w.config.CheckJobSubject}, checkJobStreamMaxAge)
	if err != nil {
		return fmt.Errorf("failed to ensure JetStream stream: %w", err)
	}

	w.logger.WithField("stream", w.config.CheckJobStream).Info("JetStream stream ensured")

	// Create or get consumer
	consumer, err := w.queue.CreateConsumer(ctx, w.config.CheckJobStream, w.config.NATSConsumerName)
	if err != nil {
		return fmt.Errorf("failed to create consumer: %w", err)
	}

	w.logger.WithField("consumer", w.config.NATSConsumerName).Info("NATS consumer created")

	// Spawn worker goroutines
	for i := 0; i < w.config.WorkerConcurrency; i++ {
		w.wg.Add(1)
		go func(workerID int) {
			defer w.wg.Done()
			w.processMessages(w.ctx, consumer, workerID)
		}(i)
	}

	// Wait for context cancellation
	<-w.ctx.Done()
	w.logger.Info("Worker context cancelled, waiting for goroutines to finish")

	return nil
}

// processMessages processes messages from the consumer
func (w *Worker) processMessages(ctx context.Context, consumer jetstream.Consumer, workerID int) {
	w.logger.WithField("worker_id", workerID).Debug("Worker goroutine started")

	handler := func(msg *queue.Message) error {
		return w.processJob(ctx, msg)
	}

	err := w.queue.Consume(ctx, consumer, handler)
	if err != nil && err != context.Canceled {
		w.logger.WithError(err).WithField("worker_id", workerID).Error("Worker goroutine error")
	}

	w.logger.WithField("worker_id", workerID).Debug("Worker goroutine stopped")
}

// processJob processes a single job message
func (w *Worker) processJob(ctx context.Context, msg *queue.Message) error {
	startTime := time.Now()

	// Parse job envelope
	var job models.Job
	if err := json.Unmarshal(msg.Data, &job); err != nil {
		w.logger.WithError(err).Error("Failed to unmarshal job envelope")
		// ACK poison messages to avoid infinite retries
		return nil
	}

	logEntry := w.logger.WithFields(logrus.Fields{
		"job_id":      job.ID,
		"tenant_id":   job.TenantID,
		"job_type":    job.Type,
		"job_version": job.Version,
	})

	// Validate job type and version
	if job.Type != models.JobTypeCheck {
		logEntry.Error("Invalid job type")
		return nil
	}

	if job.Version != "v1" {
		logEntry.Error("Unsupported job version")
		return nil
	}

	// Check deadline
	if job.IsExpired() {
		logEntry.Warn("Job expired, marking as error")
		if err := w.persistExpiredJob(ctx, &job); err != nil {
			logEntry.WithError(err).Error("Failed to persist expired job")
			w.natsNakTotal.Inc()
			return err
		}
		w.jobsTotal.With(prometheus.Labels{"status": "error"}).Inc()
		w.jobDuration.With(prometheus.Labels{"status": "error"}).Observe(time.Since(startTime).Seconds())
		w.natsAckTotal.Inc()
		return nil
	}

	// Parse CheckJobPayload
	var payload models.CheckJobPayload
	if err := json.Unmarshal(job.Payload, &payload); err != nil {
		logEntry.WithError(err).Error("Failed to unmarshal job payload")
		return nil
	}

	logEntry = logEntry.WithField("monitor_id", payload.MonitorID)

	// Validate payload
	if payload.Type == "" {
		logEntry.Error("Missing type in payload")
		return nil
	}

	if len(payload.Config) == 0 {
		logEntry.Error("Missing config in payload")
		return nil
	}

	if payload.TimeoutSeconds <= 0 {
		logEntry.Error("Invalid timeout_seconds in payload")
		return nil
	}

	// Check if this is a passive monitor type
	if w.passiveTypes[payload.Type] {
		logEntry.WithField("type", payload.Type).Debug("Skipping passive monitor check")
		return nil
	}

	// Get checker from registry
	checker, err := w.checkerRegistry.Get(payload.Type)
	if err != nil {
		logEntry.WithField("type", payload.Type).Error("Unknown monitor type")
		return nil
	}

	// Execute check using the registered checker
	checkCtx := ctx
	if payload.Type == "synthetic_browser" {
		checkCtx = withSyntheticBrowserMonitorID(checkCtx, payload.MonitorID)
	}
	checkResult := checker.Check(checkCtx, payload.Config, payload.TimeoutSeconds)

	// Persist result
	if err := w.persistResult(ctx, &job, &payload, &checkResult, startTime); err != nil {
		logEntry.WithError(err).Error("Failed to persist result")
		w.dbWriteErrors.Inc()
		w.natsNakTotal.Inc()
		return err
	}

	// Record metrics
	duration := time.Since(startTime).Seconds()
	w.jobsTotal.With(prometheus.Labels{"status": checkResult.Status}).Inc()
	w.jobDuration.With(prometheus.Labels{"status": checkResult.Status}).Observe(duration)

	logEntry.WithFields(logrus.Fields{
		"status":   checkResult.Status,
		"duration": duration,
	}).Debug("Job processed successfully")

	w.natsAckTotal.Inc()
	return nil
}

// persistResult persists the check result to the database
func (w *Worker) persistResult(ctx context.Context, job *models.Job, payload *models.CheckJobPayload, checkResult *CheckResult, startedAt time.Time) error {
	monitorID, err := uuid.Parse(payload.MonitorID)
	if err != nil {
		return fmt.Errorf("invalid monitor_id: %w", err)
	}

	tenantID, err := uuid.Parse(job.TenantID)
	if err != nil {
		return fmt.Errorf("invalid tenant_id: %w", err)
	}

	jobID, err := uuid.Parse(job.ID)
	if err != nil {
		return fmt.Errorf("invalid job_id: %w", err)
	}

	completedAt := time.Now()
	metricsData := checkResult.MetricsData
	if len(metricsData) == 0 {
		// lib/pq rejects []byte(nil) for json/jsonb parameters ("invalid input syntax for type json").
		// Use explicit JSON null when a checker doesn't emit metrics.
		metricsData = json.RawMessage("null")
	}

	query := `
		INSERT INTO check_results (
			id, monitor_id, tenant_id, job_id, status, result_source, http_status,
			latency_ms, error_message, matched_body_substring, metrics_data,
			created_at, started_at, completed_at
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14)
	`

	resultID := uuid.New()
	_, err = w.db.ExecContext(ctx, query,
		resultID,
		monitorID,
		tenantID,
		jobID,
		checkResult.Status,
		string(models.ResultSourceMonitor),
		checkResult.HTTPStatus,
		checkResult.LatencyMs,
		checkResult.ErrorMessage,
		checkResult.MatchedBodySubstring,
		metricsData,
		startedAt,
		startedAt,
		completedAt,
	)

	if err != nil {
		return fmt.Errorf("failed to insert check result: %w", err)
	}

	w.publishStatusUpdate(monitorID, tenantID)
	return nil
}

// persistExpiredJob persists an expired job as an error result
func (w *Worker) persistExpiredJob(ctx context.Context, job *models.Job) error {
	var payload models.CheckJobPayload
	if err := json.Unmarshal(job.Payload, &payload); err != nil {
		return nil
	}

	monitorID, err := uuid.Parse(payload.MonitorID)
	if err != nil {
		return nil
	}

	tenantID, err := uuid.Parse(job.TenantID)
	if err != nil {
		return nil
	}

	jobID, err := uuid.Parse(job.ID)
	if err != nil {
		return nil
	}

	errorMsg := "Job expired before processing"
	now := time.Now()
	metricsData := json.RawMessage("null")

	query := `
		INSERT INTO check_results (
			id, monitor_id, tenant_id, job_id, status, result_source, http_status,
			latency_ms, error_message, matched_body_substring, metrics_data,
			created_at, started_at, completed_at
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14)
	`

	resultID := uuid.New()
	_, err = w.db.ExecContext(ctx, query,
		resultID,
		monitorID,
		tenantID,
		jobID,
		string(models.ResultStatusError),
		string(models.ResultSourcePlatform),
		nil,
		nil,
		&errorMsg,
		false,
		metricsData,
		now,
		now,
		now,
	)

	if err != nil {
		return fmt.Errorf("failed to insert expired job result: %w", err)
	}

	w.publishStatusUpdate(monitorID, tenantID)
	return nil
}

func (w *Worker) publishStatusUpdate(monitorID, tenantID uuid.UUID) {
	if w.status == nil {
		return
	}
	event := statusupdates.Event{
		Type:      "check_result",
		MonitorID: monitorID.String(),
		TenantID:  tenantID.String(),
		Timestamp: time.Now().UTC(),
	}
	if err := w.status.Publish(event); err != nil {
		w.logger.WithError(err).Warn("Failed to publish status page update")
	}
}

// Shutdown gracefully shuts down the worker
func (w *Worker) Shutdown(ctx context.Context) error {
	w.logger.Info("Shutting down worker")

	w.cancel()

	done := make(chan struct{})
	go func() {
		w.wg.Wait()
		close(done)
	}()

	select {
	case <-done:
		w.logger.Info("All worker goroutines finished")
		return nil
	case <-ctx.Done():
		w.logger.Warn("Shutdown timeout exceeded, some jobs may still be processing")
		return ctx.Err()
	}
}
