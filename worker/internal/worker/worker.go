package worker

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"time"

	"github.com/nats-io/nats.go/jetstream"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/sirupsen/logrus"

	"github.com/yassinebenameur/probara/shared/config"
	"github.com/yassinebenameur/probara/shared/logger"
	"github.com/yassinebenameur/probara/shared/metrics"
	"github.com/yassinebenameur/probara/shared/models"
	"github.com/yassinebenameur/probara/shared/queue"
	"github.com/yassinebenameur/probara/shared/secrets"
)

const checkJobStreamMaxAge = 24 * time.Hour
const consumerRestartBackoff = 2 * time.Second

// resultPublisher is the minimal publishing surface the worker needs to hand
// results back to the platform. Satisfied by *queue.Client and test spies.
type resultPublisher interface {
	PublishJSON(ctx context.Context, subject string, v interface{}, headers map[string][]string) error
}

// Worker represents the worker service
type Worker struct {
	config  *config.WorkerConfig
	logger  *logger.Logger
	metrics *metrics.Registry
	queue   *queue.Client
	results resultPublisher
	ctx     context.Context
	cancel  context.CancelFunc
	wg      sync.WaitGroup

	// Checker registry for extensible monitor types
	checkerRegistry *CheckerRegistry

	// Decrypts secret config fields (DB passwords, …) just before a check
	// runs; configs travel encrypted through the DB and NATS.
	secretsEncryptor secrets.Encryptor

	// Passive monitor types that don't need active checking
	passiveTypes map[string]bool

	// Metrics
	jobsTotal           *prometheus.CounterVec
	jobDuration         *prometheus.HistogramVec
	httpRequestDuration *prometheus.HistogramVec
	httpErrors          *prometheus.CounterVec
	natsAckTotal        prometheus.Counter
	natsNakTotal        prometheus.Counter
	resultPublishErrors prometheus.Counter
}

// NewWorker creates a new worker instance
func NewWorker(cfg *config.WorkerConfig, log *logger.Logger, metricsRegistry *metrics.Registry, queueClient *queue.Client) *Worker {
	ctx, cancel := context.WithCancel(context.Background())

	w := &Worker{
		config:           cfg,
		logger:           log,
		metrics:          metricsRegistry,
		queue:            queueClient,
		results:          queueClient,
		ctx:              ctx,
		cancel:           cancel,
		secretsEncryptor: secrets.NoOpEncryptor{},
		checkerRegistry:  NewDefaultRegistry(cfg.MaxBodySizeBytes, cfg.HTTPBlockPrivateIPs, cfg.HTTPAllowedCIDRs, cfg.SyntheticArtifactsDir),
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

	resultPublishErrorsCounter := metricsRegistry.NewCounter(
		"result_publish_errors_total",
		"Total number of check result publish errors",
		[]string{},
	)
	w.resultPublishErrors = resultPublishErrorsCounter.With(prometheus.Labels{})

	return w
}

// ConfigureEncryption wires the encryptor used to decrypt secret monitor
// config fields. Without it the worker falls back to a NoOpEncryptor, which
// passes plaintext through but refuses ciphertext envelopes.
func (w *Worker) ConfigureEncryption(encryptor secrets.Encryptor) {
	if encryptor != nil {
		w.secretsEncryptor = encryptor
	}
}

// RegisterChecker registers a custom checker for a monitor type
func (w *Worker) RegisterChecker(monitorType string, checker Checker) {
	w.checkerRegistry.Register(monitorType, checker)
}

// RegisterPassiveType marks a monitor type as passive (no active checking)
func (w *Worker) RegisterPassiveType(monitorType string) {
	w.passiveTypes[monitorType] = true
}

// jobFilterSubject returns the per-location subject this worker consumes.
func (w *Worker) jobFilterSubject() string {
	if w.config.LocationID == "" {
		return models.CheckJobSubjectDefault(w.config.CheckJobSubject)
	}
	return models.CheckJobSubjectForLocation(w.config.CheckJobSubject, w.config.LocationID)
}

// consumerName returns this worker fleet's durable consumer name. Each
// location gets its own durable (with a matching filter subject); the default
// fleet's name is suffixed too, so it can never collide with the legacy
// filterless consumer the scheduler deletes on upgrade.
func (w *Worker) consumerName() string {
	if w.config.LocationID == "" {
		return w.config.NATSConsumerName + "-default"
	}
	return w.config.NATSConsumerName + "-loc-" + w.config.LocationID
}

// testCheckSubject returns the request-reply subject for ephemeral test
// checks served by this worker fleet.
func (w *Worker) testCheckSubject() (subject, queueGroup string) {
	if w.config.LocationID == "" {
		return models.TestCheckSubject, "workers"
	}
	return models.TestCheckSubjectForLocation(w.config.LocationID), "workers-loc-" + w.config.LocationID
}

// Start starts the worker loop
func (w *Worker) Start() error {
	filterSubject := w.jobFilterSubject()
	consumerName := w.consumerName()

	w.logger.WithFields(logrus.Fields{
		"worker_concurrency":  w.config.WorkerConcurrency,
		"consumer_name":       consumerName,
		"stream":              w.config.CheckJobStream,
		"subject":             filterSubject,
		"location_id":         w.config.LocationID,
		"registered_checkers": w.checkerRegistry.Types(),
	}).Info("Starting worker service")

	// Ensure JetStream stream exists. The subject set must match the
	// scheduler's exactly (CreateOrUpdateStream applies whatever it is given):
	// the bare base subject plus the per-location hierarchy.
	ctx, cancel := context.WithTimeout(w.ctx, 10*time.Second)
	defer cancel()

	jobSubjects := []string{w.config.CheckJobSubject, w.config.CheckJobSubject + ".>"}
	_, err := w.queue.EnsureWorkQueueStream(ctx, w.config.CheckJobStream, jobSubjects, checkJobStreamMaxAge)
	if err != nil {
		return fmt.Errorf("failed to ensure JetStream stream: %w", err)
	}

	// Results stream: the scheduler-side ingest consumer ensures it too, so
	// ordering doesn't matter; ensuring here lets a worker start first.
	_, err = w.queue.EnsureWorkQueueStream(ctx, w.config.CheckResultStream, []string{w.config.CheckResultSubject}, checkJobStreamMaxAge)
	if err != nil {
		return fmt.Errorf("failed to ensure results stream: %w", err)
	}

	w.logger.WithField("stream", w.config.CheckJobStream).Info("JetStream streams ensured")

	// Create or get this fleet's durable consumer, filtered to its subject.
	consumer, err := w.queue.CreateConsumerWithOptions(ctx, w.config.CheckJobStream, consumerName, queue.ConsumerOptions{
		FilterSubject: filterSubject,
	})
	if err != nil {
		return fmt.Errorf("failed to create consumer: %w", err)
	}

	w.logger.WithField("consumer", consumerName).Info("NATS consumer created")

	// Spawn worker goroutines
	for i := 0; i < w.config.WorkerConcurrency; i++ {
		w.wg.Add(1)
		go func(workerID int) {
			defer w.wg.Done()
			w.processMessages(w.ctx, consumer, workerID)
		}(i)
	}

	// Test-connection requests (core NATS request-reply, no persistence):
	// the API forwards "test this config before saving" requests here.
	// Location workers serve only their own subject — a test targeted at a
	// location must run from that vantage point.
	testSubject, testQueueGroup := w.testCheckSubject()
	testSub, err := w.queue.SubscribeRequestReply(testSubject, testQueueGroup, w.handleTestCheck)
	if err != nil {
		w.logger.WithError(err).Warn("Failed to subscribe to test-check requests; test-connection will be unavailable")
	} else {
		defer func() { _ = testSub.Unsubscribe() }()
		w.logger.WithField("subject", testSubject).Info("Test-check subscription ready")
	}

	// Location workers heartbeat their liveness so the UI can show the
	// location as connected.
	if w.config.LocationID != "" {
		w.wg.Add(1)
		go func() {
			defer w.wg.Done()
			w.heartbeatLoop(w.ctx)
		}()
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

	for {
		err := w.queue.Consume(ctx, consumer, handler)
		if err == nil || err == context.Canceled {
			break
		}

		w.logger.WithError(err).WithField("worker_id", workerID).Error("Worker goroutine error")

		timer := time.NewTimer(consumerRestartBackoff)
		select {
		case <-ctx.Done():
			timer.Stop()
			return
		case <-timer.C:
		}
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
		if err := w.publishExpiredJob(ctx, &job); err != nil {
			logEntry.WithError(err).Error("Failed to publish expired job result")
			w.resultPublishErrors.Inc()
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

	var checkResult CheckResult
	if checkConfig, err := secrets.DecryptMonitorConfig(w.secretsEncryptor, payload.Type, payload.Config); err != nil {
		// A check that can't decrypt its secrets is an operator problem
		// (missing/rotated PROBARA_SECRETS_KEY), not a target outage — but it
		// still must surface as an errored check rather than vanish.
		logEntry.WithError(err).Error("Failed to decrypt monitor config")
		errMsg := fmt.Sprintf("config_decrypt: %v", err)
		checkResult = CheckResult{Status: "error", ErrorMessage: &errMsg}
	} else {
		checkResult = checker.Check(checkCtx, checkConfig, payload.TimeoutSeconds)
	}

	// Publish result for the platform-side ingest consumer to persist
	if err := w.publishResult(ctx, &job, &payload, &checkResult, startTime); err != nil {
		logEntry.WithError(err).Error("Failed to publish result")
		w.resultPublishErrors.Inc()
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

// handleTestCheck runs an ephemeral check for a test-connection request and
// returns the JSON-encoded TestCheckResponse. Nothing is persisted and no
// monitor state advances — this exists so users can validate a config before
// saving it.
func (w *Worker) handleTestCheck(data []byte) []byte {
	respond := func(resp models.TestCheckResponse) []byte {
		b, err := json.Marshal(resp)
		if err != nil {
			return []byte(`{"status":"error","error_message":"worker: encode response"}`)
		}
		return b
	}
	errorResponse := func(msg string) []byte {
		return respond(models.TestCheckResponse{Status: "error", ErrorMessage: &msg})
	}

	var payload models.CheckJobPayload
	if err := json.Unmarshal(data, &payload); err != nil {
		return errorResponse(fmt.Sprintf("invalid test payload: %v", err))
	}
	if payload.TimeoutSeconds <= 0 || payload.TimeoutSeconds > 120 {
		return errorResponse("timeout_seconds must be between 1 and 120")
	}
	if w.passiveTypes[payload.Type] {
		return errorResponse(fmt.Sprintf("monitor type %q is passive and cannot be tested", payload.Type))
	}
	checker, err := w.checkerRegistry.Get(payload.Type)
	if err != nil {
		return errorResponse(fmt.Sprintf("unknown monitor type: %s", payload.Type))
	}

	config, err := secrets.DecryptMonitorConfig(w.secretsEncryptor, payload.Type, payload.Config)
	if err != nil {
		w.logger.WithError(err).Error("Test check: failed to decrypt config")
		return errorResponse(fmt.Sprintf("config_decrypt: %v", err))
	}

	ctx, cancel := context.WithTimeout(w.ctx, time.Duration(payload.TimeoutSeconds+5)*time.Second)
	defer cancel()
	result := checker.Check(ctx, config, payload.TimeoutSeconds)

	return respond(models.TestCheckResponse{
		Status:       result.Status,
		LatencyMs:    result.LatencyMs,
		ErrorMessage: result.ErrorMessage,
		MetricsData:  result.MetricsData,
	})
}

// publishResult hands the executed check back to the platform over NATS. The
// scheduler-side ingest consumer persists it and advances the monitor's state
// machine — the worker never touches Postgres, so remote location workers
// only need NATS reachability.
func (w *Worker) publishResult(ctx context.Context, job *models.Job, payload *models.CheckJobPayload, checkResult *CheckResult, startedAt time.Time) error {
	msg := models.CheckResultMessage{
		Version:              "v1",
		JobID:                job.ID,
		MonitorID:            payload.MonitorID,
		TenantID:             job.TenantID,
		LocationID:           payload.LocationID,
		Status:               checkResult.Status,
		ResultSource:         string(models.ResultSourceMonitor),
		HTTPStatus:           checkResult.HTTPStatus,
		LatencyMs:            checkResult.LatencyMs,
		ErrorMessage:         checkResult.ErrorMessage,
		MatchedBodySubstring: checkResult.MatchedBodySubstring,
		MetricsData:          checkResult.MetricsData,
		StartedAt:            startedAt,
		CompletedAt:          time.Now(),
	}
	return w.publishResultMessage(ctx, msg)
}

// publishExpiredJob publishes an expired job as a platform-sourced error
// result. The ingest consumer inserts it without running the state machine —
// an expired job never observed the target.
func (w *Worker) publishExpiredJob(ctx context.Context, job *models.Job) error {
	var payload models.CheckJobPayload
	if err := json.Unmarshal(job.Payload, &payload); err != nil {
		return nil
	}
	if payload.MonitorID == "" {
		return nil
	}

	errorMsg := "Job expired before processing"
	now := time.Now()
	msg := models.CheckResultMessage{
		Version:      "v1",
		JobID:        job.ID,
		MonitorID:    payload.MonitorID,
		TenantID:     job.TenantID,
		LocationID:   payload.LocationID,
		Status:       string(models.ResultStatusError),
		ResultSource: string(models.ResultSourcePlatform),
		ErrorMessage: &errorMsg,
		StartedAt:    now,
		CompletedAt:  now,
	}
	return w.publishResultMessage(ctx, msg)
}

func (w *Worker) publishResultMessage(ctx context.Context, msg models.CheckResultMessage) error {
	// Nats-Msg-Id enables JetStream's publish-side dedupe window; the durable
	// dedupe is the unique index on check_results(job_id, result_source).
	headers := map[string][]string{"Nats-Msg-Id": {msg.DedupeID()}}
	if err := w.results.PublishJSON(ctx, w.config.CheckResultSubject, msg, headers); err != nil {
		return fmt.Errorf("publish check result: %w", err)
	}
	return nil
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
