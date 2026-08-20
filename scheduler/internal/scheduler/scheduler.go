package scheduler

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/lib/pq"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/sirupsen/logrus"

	"github.com/yassinebenameur/probara/shared/config"
	"github.com/yassinebenameur/probara/shared/db"
	"github.com/yassinebenameur/probara/shared/locationauth"
	"github.com/yassinebenameur/probara/shared/logger"
	"github.com/yassinebenameur/probara/shared/metrics"
	"github.com/yassinebenameur/probara/shared/models"
	"github.com/yassinebenameur/probara/shared/queue"
	"github.com/yassinebenameur/probara/shared/secrets"
)

const (
	retentionCleanupTickerInterval = time.Minute
	retentionCleanupRunTimeout     = 30 * time.Minute
	retentionCleanupAdvisoryLock   = int64(901_337_401)
	rollupMaintenanceTicker        = time.Minute
	checkJobStreamMaxAge           = 24 * time.Hour
	meshBatchTickerInterval        = 10 * time.Second

	// suspectRecheckInterval is the fast cadence used while a monitor is in the
	// suspect state, confirming or clearing a potential outage (spec §5).
	suspectRecheckInterval = 20 * time.Second
)

// nextCheckDelay returns how long after now the monitor should run again.
// Only 'suspect' (temporal, mid-confirmation) gets the fast recheck;
// 'degraded' (spatial: some locations down, below quorum) can persist
// indefinitely and runs at the normal interval.
func nextCheckDelay(currentState string, intervalSeconds int) time.Duration {
	interval := time.Duration(intervalSeconds) * time.Second
	if currentState == "suspect" && suspectRecheckInterval < interval {
		return suspectRecheckInterval
	}
	return interval
}

// Monitor represents a monitor for scheduling purposes
type Monitor struct {
	ID              uuid.UUID
	TenantID        uuid.UUID
	Type            string
	Config          []byte
	IntervalSeconds int
	TimeoutSeconds  int
	CurrentState    string
	// LocationIDs the monitor fans out to; empty = default platform fleet.
	LocationIDs []uuid.UUID
	// LocationCredentials contains plaintext per-location credentials only for
	// the lifetime of this scheduling batch.
	LocationCredentials map[uuid.UUID]string
}

// Scheduler represents the scheduler service
type Scheduler struct {
	config                  *config.SchedulerConfig
	logger                  *logger.Logger
	metrics                 *metrics.Registry
	db                      *db.Client
	queue                   *queue.Client
	secretsEncryptor        secrets.Encryptor
	ctx                     context.Context
	cancel                  context.CancelFunc
	stop                    chan struct{}
	stopOnce                sync.Once
	retentionMu             sync.Mutex
	retentionRunning        bool
	lastRetentionRunUTCDate string
	rollupMu                sync.Mutex
	rollupRunning           bool
	watchdogMu              sync.Mutex
	watchdogRunning         bool
	status                  statusPublisher

	// Metrics
	loopsTotal        *prometheus.CounterVec
	monitorsScheduled *prometheus.CounterVec
	jobsPublishErrors *prometheus.CounterVec
	dbErrors          *prometheus.CounterVec
	loopDuration      *prometheus.HistogramVec
	monitorsInBatch   *prometheus.HistogramVec
	retentionRuns     *prometheus.CounterVec
	retentionRows     *prometheus.CounterVec
	rollupRuns        *prometheus.CounterVec
	rollupRows        *prometheus.CounterVec
	rollupErrors      *prometheus.CounterVec
	rollupRowsSkipped *prometheus.CounterVec
	rollupDuration    *prometheus.HistogramVec
	rollupCursor      *prometheus.GaugeVec

	meshEdgesScheduled *prometheus.CounterVec
	meshPublishErrors  *prometheus.CounterVec

	// applyRow applies one check result to the rollup tables inside the given
	// transaction. It defaults to applyRollupRow and exists as a seam so tests
	// can inject per-row failures.
	applyRow func(ctx context.Context, tx *sql.Tx, row rollupCheckResult) error

	// publish sends one job to the queue. It defaults to the NATS-backed
	// implementation and exists as a seam (same pattern as applyRow) so tests
	// can capture published jobs without a broker.
	publish func(ctx context.Context, subject string, job *models.Job) error

	purger *purger
}

// NewScheduler creates a new scheduler instance
func NewScheduler(cfg *config.SchedulerConfig, log *logger.Logger, metricsRegistry *metrics.Registry, dbClient *db.Client, queueClient *queue.Client) *Scheduler {
	ctx, cancel := context.WithCancel(context.Background())

	s := &Scheduler{
		config:           cfg,
		logger:           log,
		metrics:          metricsRegistry,
		db:               dbClient,
		queue:            queueClient,
		ctx:              ctx,
		cancel:           cancel,
		stop:             make(chan struct{}),
		secretsEncryptor: secrets.NoOpEncryptor{},
	}

	// Initialize metrics (using empty labels, so we'll use With(prometheus.Labels{}))
	s.loopsTotal = metricsRegistry.NewCounter(
		"loops_total",
		"Total number of scheduler loop iterations",
		[]string{},
	)
	s.monitorsScheduled = metricsRegistry.NewCounter(
		"monitors_scheduled_total",
		"Total number of monitors scheduled",
		[]string{},
	)
	s.jobsPublishErrors = metricsRegistry.NewCounter(
		"jobs_publish_errors_total",
		"Total number of job publish errors",
		[]string{},
	)
	s.dbErrors = metricsRegistry.NewCounter(
		"db_errors_total",
		"Total number of database errors",
		[]string{},
	)
	s.meshEdgesScheduled = metricsRegistry.NewCounter(
		"mesh_edges_scheduled_total",
		"Total number of mesh probe jobs published",
		[]string{},
	)
	s.meshPublishErrors = metricsRegistry.NewCounter(
		"mesh_publish_errors_total",
		"Total number of mesh probe publish errors",
		[]string{},
	)
	s.loopDuration = metricsRegistry.NewHistogram(
		"loop_duration_seconds",
		"Duration of scheduler loop iterations",
		[]string{},
		nil,
	)
	s.monitorsInBatch = metricsRegistry.NewHistogram(
		"monitors_in_batch",
		"Number of monitors processed per batch",
		[]string{},
		nil,
	)
	s.retentionRuns = metricsRegistry.NewCounter(
		"retention_cleanup_runs_total",
		"Total number of retention cleanup runs",
		[]string{},
	)
	s.retentionRows = metricsRegistry.NewCounter(
		"retention_cleanup_rows_total",
		"Total number of check result rows deleted by retention cleanup",
		[]string{},
	)
	s.rollupRuns = metricsRegistry.NewCounter(
		"rollup_runs_total",
		"Total number of monitor rollup maintenance runs",
		[]string{},
	)
	s.rollupRows = metricsRegistry.NewCounter(
		"rollup_rows_total",
		"Total number of check result rows processed by rollup maintenance",
		[]string{},
	)
	s.rollupErrors = metricsRegistry.NewCounter(
		"rollup_errors_total",
		"Total number of rollup maintenance failures",
		[]string{},
	)
	s.rollupRowsSkipped = metricsRegistry.NewCounter(
		"rollup_rows_skipped_total",
		"Total number of poisoned check result rows skipped by rollup maintenance",
		[]string{},
	)
	s.rollupDuration = metricsRegistry.NewHistogram(
		"rollup_duration_seconds",
		"Duration of rollup maintenance runs",
		[]string{},
		nil,
	)
	s.rollupCursor = metricsRegistry.NewGauge(
		"rollup_cursor_unix",
		"Unix timestamp of the latest processed check result cursor",
		[]string{},
	)
	s.applyRow = applyRollupRow
	s.publish = func(ctx context.Context, subject string, job *models.Job) error {
		return s.queue.PublishJSON(ctx, subject, job, nil)
	}

	purgerMetrics := &purgerMetrics{
		runs: metricsRegistry.NewCounter(
			"monitor_purge_runs_total", "Total monitor purge runs", []string{}),
		rows: metricsRegistry.NewCounter(
			"monitor_purge_rows_total", "Total child rows deleted by purger", []string{}),
		monitors: metricsRegistry.NewCounter(
			"monitor_purge_monitors_total", "Total monitor rows fully purged", []string{}),
		errors: metricsRegistry.NewCounter(
			"monitor_purge_errors_total", "Total monitor purge errors", []string{}),
		runDuration: metricsRegistry.NewHistogram(
			"monitor_purge_run_duration_seconds", "Duration of monitor purge runs", []string{}, nil),
	}
	s.purger = newPurger(dbClient, log, purgerMetrics, purgerOptions{
		BatchSize:     cfg.MonitorPurgeBatchSize,
		MaxRowsPerRun: cfg.MonitorPurgeMaxRowsPerRun,
	})

	return s
}

// ConfigureEncryption wires the platform key used to decrypt monitor configs
// and per-location credentials immediately before location-scoped publishing.
func (s *Scheduler) ConfigureEncryption(encryptor secrets.Encryptor) {
	if encryptor != nil {
		s.secretsEncryptor = encryptor
	}
}

// Start starts the scheduler loop
func (s *Scheduler) Start() error {
	s.logger.WithFields(logrus.Fields{
		"schedule_interval_seconds": s.config.ScheduleIntervalSeconds,
		"batch_size":                s.config.SchedulerBatchSize,
		"check_job_subject":         s.config.CheckJobSubject,
		"check_job_stream":          s.config.CheckJobStream,
	}).Info("Starting scheduler loop")

	// Ensure JetStream stream exists
	ctx, cancel := context.WithTimeout(s.ctx, 10*time.Second)
	defer cancel()

	// The stream carries the bare base subject (legacy, still valid for jobs
	// in flight during rollout) plus the per-location hierarchy
	// (<base>.default and <base>.loc.<id>).
	subjects := []string{s.config.CheckJobSubject, s.config.CheckJobSubject + ".>"}
	_, err := s.queue.EnsureWorkQueueStream(ctx, s.config.CheckJobStream, subjects, checkJobStreamMaxAge)
	if err != nil {
		return fmt.Errorf("failed to ensure JetStream stream: %w", err)
	}

	// Work-queue streams forbid a filterless consumer coexisting with the
	// filtered per-location consumers workers now create, so drop legacy
	// filterless consumers (pre-locations worker fleets) if still present.
	for _, name := range s.config.LegacyCheckConsumers {
		if err := s.queue.DeleteConsumer(ctx, s.config.CheckJobStream, name); err == nil {
			s.logger.WithField("consumer", name).Info("Deleted legacy filterless check-jobs consumer")
		}
	}

	s.logger.WithField("stream", s.config.CheckJobStream).Info("JetStream stream ensured")

	// Create ticker with interval from config
	ticker := time.NewTicker(time.Duration(s.config.ScheduleIntervalSeconds) * time.Second)
	defer ticker.Stop()
	retentionTicker := time.NewTicker(retentionCleanupTickerInterval)
	defer retentionTicker.Stop()
	rollupTicker := time.NewTicker(rollupMaintenanceTicker)
	defer rollupTicker.Stop()
	purgeTicker := time.NewTicker(time.Duration(s.config.MonitorPurgeIntervalSeconds) * time.Second)
	defer purgeTicker.Stop()
	// The mesh tick just claims due edges; the per-edge cadence lives in
	// location_mesh_state.next_run_at.
	meshTicker := time.NewTicker(meshBatchTickerInterval)
	defer meshTicker.Stop()
	watchdogTicker := time.NewTicker(watchdogTickerInterval)
	defer watchdogTicker.Stop()

	// Initial run
	s.scheduleBatch(s.ctx)
	s.triggerRetentionCleanup()
	s.triggerRollupMaintenance()
	s.triggerWatchdog()
	if s.config.MeshEnabled {
		s.runMeshBatch(s.ctx)
	}

	for {
		select {
		case <-s.stop:
			s.logger.Info("Scheduler loop stopped")
			return nil
		case <-s.ctx.Done():
			s.logger.Info("Scheduler context cancelled")
			return s.ctx.Err()
		case <-ticker.C:
			s.scheduleBatch(s.ctx)
		case <-retentionTicker.C:
			s.triggerRetentionCleanup()
		case <-rollupTicker.C:
			s.triggerRollupMaintenance()
		case <-purgeTicker.C:
			s.triggerMonitorPurge()
		case <-meshTicker.C:
			if s.config.MeshEnabled {
				s.runMeshBatch(s.ctx)
			}
		case <-watchdogTicker.C:
			s.triggerWatchdog()
		}
	}
}

func (s *Scheduler) triggerRetentionCleanup() {
	if !s.config.RetentionCleanupEnabled {
		return
	}

	now := time.Now().UTC()
	if now.Hour() < s.config.RetentionCleanupHourUTC {
		return
	}
	runDate := now.Format("2006-01-02")

	s.retentionMu.Lock()
	if s.retentionRunning || s.lastRetentionRunUTCDate == runDate {
		s.retentionMu.Unlock()
		return
	}
	s.retentionRunning = true
	s.retentionMu.Unlock()

	go func(runDate string) {
		defer func() {
			s.retentionMu.Lock()
			s.retentionRunning = false
			s.retentionMu.Unlock()
		}()

		executed, deletedRows, err := s.runRetentionCleanup()
		if err != nil {
			s.logger.WithError(err).Error("Retention cleanup run failed")
			return
		}
		if !executed {
			return
		}

		s.retentionMu.Lock()
		s.lastRetentionRunUTCDate = runDate
		s.retentionMu.Unlock()

		s.retentionRuns.With(prometheus.Labels{}).Inc()
		if deletedRows > 0 {
			s.retentionRows.With(prometheus.Labels{}).Add(float64(deletedRows))
		}

		s.logger.WithFields(logrus.Fields{
			"run_date_utc":    runDate,
			"deleted_rows":    deletedRows,
			"target_hour_utc": s.config.RetentionCleanupHourUTC,
		}).Info("Retention cleanup run completed")
	}(runDate)
}

func (s *Scheduler) runRetentionCleanup() (bool, int64, error) {
	ctx, cancel := context.WithTimeout(s.ctx, retentionCleanupRunTimeout)
	defer cancel()

	var locked bool
	if err := s.db.QueryRowContext(ctx, "SELECT pg_try_advisory_lock($1)", retentionCleanupAdvisoryLock).Scan(&locked); err != nil {
		return false, 0, fmt.Errorf("failed to acquire retention cleanup advisory lock: %w", err)
	}
	if !locked {
		return false, 0, nil
	}
	defer func() {
		if _, err := s.db.ExecContext(context.Background(), "SELECT pg_advisory_unlock($1)", retentionCleanupAdvisoryLock); err != nil {
			s.logger.WithError(err).Warn("Failed to release retention cleanup advisory lock")
		}
	}()

	query := `
		SELECT id, data_retention_days
		FROM tenants
		WHERE data_retention_days > 0
		ORDER BY id
	`
	rows, err := s.db.QueryContext(ctx, query)
	if err != nil {
		return true, 0, fmt.Errorf("failed to list tenant retention settings: %w", err)
	}
	defer rows.Close()

	var totalDeleted int64
	for rows.Next() {
		var tenantID uuid.UUID
		var retentionDays int
		if err := rows.Scan(&tenantID, &retentionDays); err != nil {
			return true, totalDeleted, fmt.Errorf("failed to scan tenant retention row: %w", err)
		}

		deleted, err := s.pruneTenantCheckResults(ctx, tenantID, retentionDays)
		if err != nil {
			return true, totalDeleted, fmt.Errorf("failed to prune tenant %s: %w", tenantID, err)
		}
		totalDeleted += deleted

		meshDeleted, err := s.pruneTenantMeshResults(ctx, tenantID, retentionDays)
		if err != nil {
			return true, totalDeleted, fmt.Errorf("failed to prune tenant %s mesh results: %w", tenantID, err)
		}
		totalDeleted += meshDeleted

		if deleted > 0 {
			s.logger.WithFields(logrus.Fields{
				"tenant_id":        tenantID,
				"retention_days":   retentionDays,
				"deleted_rows":     deleted,
				"batch_size":       s.config.RetentionCleanupBatchSize,
				"max_rows_per_run": s.config.RetentionCleanupMaxRowsPerRun,
			}).Info("Pruned stale check results for tenant")
		}
	}
	if err := rows.Err(); err != nil {
		return true, totalDeleted, fmt.Errorf("error iterating tenant retention rows: %w", err)
	}

	return true, totalDeleted, nil
}

func (s *Scheduler) pruneTenantCheckResults(ctx context.Context, tenantID uuid.UUID, retentionDays int) (int64, error) {
	if retentionDays <= 0 {
		return 0, nil
	}

	cutoff := time.Now().UTC().AddDate(0, 0, -retentionDays)
	deleteQuery := `
		DELETE FROM check_results
		WHERE id IN (
			SELECT id
			FROM check_results
			WHERE tenant_id = $1
			  AND created_at < $2
			ORDER BY created_at ASC
			LIMIT $3
		)
	`

	var totalDeleted int64
	maxRows := s.config.RetentionCleanupMaxRowsPerRun
	batchSize := s.config.RetentionCleanupBatchSize
	if batchSize <= 0 {
		batchSize = 5000
	}
	if maxRows <= 0 {
		maxRows = 200000
	}

	for int(totalDeleted) < maxRows {
		remaining := maxRows - int(totalDeleted)
		limit := batchSize
		if remaining < limit {
			limit = remaining
		}

		result, err := s.db.ExecContext(ctx, deleteQuery, tenantID, cutoff, limit)
		if err != nil {
			return totalDeleted, err
		}
		rowsDeleted, err := result.RowsAffected()
		if err != nil {
			return totalDeleted, fmt.Errorf("failed to read rows affected: %w", err)
		}
		if rowsDeleted == 0 {
			break
		}

		totalDeleted += rowsDeleted
		if rowsDeleted < int64(limit) {
			break
		}
	}

	return totalDeleted, nil
}

// fetchDueMonitors fetches monitors that are due to run within a transaction
func (s *Scheduler) fetchDueMonitors(ctx context.Context, tx *sql.Tx, batchSize int) ([]Monitor, error) {
	query := `
		SELECT id, tenant_id, type, config, interval_seconds, timeout_seconds, current_state
		FROM monitors
		WHERE enabled = true
		  AND deleted_at IS NULL
		  AND type != 'group'
		  AND (next_run_at IS NULL OR next_run_at <= NOW())
		ORDER BY next_run_at NULLS FIRST, id
		LIMIT $1
		FOR UPDATE SKIP LOCKED
	`

	rows, err := tx.QueryContext(ctx, query, batchSize)
	if err != nil {
		return nil, fmt.Errorf("failed to query due monitors: %w", err)
	}
	defer rows.Close()

	var monitors []Monitor
	for rows.Next() {
		var m Monitor

		err := rows.Scan(
			&m.ID, &m.TenantID, &m.Type, &m.Config,
			&m.IntervalSeconds, &m.TimeoutSeconds, &m.CurrentState,
		)
		if err != nil {
			return nil, fmt.Errorf("failed to scan monitor: %w", err)
		}

		monitors = append(monitors, m)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("error iterating monitors: %w", err)
	}

	if err := s.attachMonitorLocations(ctx, tx, monitors); err != nil {
		return nil, err
	}

	return monitors, nil
}

// attachMonitorLocations loads the selected locations for a batch of monitors
// in one query (enabled, non-deleted locations only — a disabled location's
// workers may be gone, so no jobs are addressed to it).
func (s *Scheduler) attachMonitorLocations(ctx context.Context, tx *sql.Tx, monitors []Monitor) error {
	if len(monitors) == 0 {
		return nil
	}

	ids := make([]string, len(monitors))
	index := make(map[uuid.UUID]int, len(monitors))
	for i, m := range monitors {
		ids[i] = m.ID.String()
		index[m.ID] = i
	}

	rows, err := tx.QueryContext(ctx, `
		SELECT ml.monitor_id, ml.location_id, l.worker_credential
		FROM monitor_locations ml
		JOIN locations l ON l.id = ml.location_id
			AND l.deleted_at IS NULL
			AND l.enabled = TRUE
		WHERE ml.monitor_id = ANY($1::uuid[])
	`, pq.Array(ids))
	if err != nil {
		return fmt.Errorf("failed to query monitor locations: %w", err)
	}
	defer rows.Close()

	for rows.Next() {
		var monitorID, locationID uuid.UUID
		var encryptedCredential sql.NullString
		if err := rows.Scan(&monitorID, &locationID, &encryptedCredential); err != nil {
			return fmt.Errorf("failed to scan monitor location: %w", err)
		}
		i, ok := index[monitorID]
		if !ok {
			continue
		}
		monitors[i].LocationIDs = append(monitors[i].LocationIDs, locationID)
		// Existing locations are issued credentials by their first deploy-info
		// request. Until then, do not publish unauthenticated private jobs.
		if !encryptedCredential.Valid || encryptedCredential.String == "" {
			continue
		}
		credential, err := s.secretsEncryptor.Decrypt(encryptedCredential.String)
		if err != nil {
			return fmt.Errorf("decrypt worker credential for location %s: %w", locationID, err)
		}
		if monitors[i].LocationCredentials == nil {
			monitors[i].LocationCredentials = make(map[uuid.UUID]string)
		}
		monitors[i].LocationCredentials[locationID] = credential
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("error iterating monitor locations: %w", err)
	}
	return nil
}

// createCheckJob creates a check job for a monitor. locationID is empty for
// the default platform fleet.
func (s *Scheduler) createCheckJob(monitor Monitor, locationID string) (*models.Job, error) {
	jobID := uuid.New().String()

	configJSON := json.RawMessage(monitor.Config)
	if locationID != "" {
		id, err := uuid.Parse(locationID)
		if err != nil {
			return nil, fmt.Errorf("invalid location id: %w", err)
		}
		credential := monitor.LocationCredentials[id]
		if credential == "" {
			return nil, fmt.Errorf("missing worker credential for location %s", locationID)
		}
		if secrets.HasMonitorSecrets(monitor.Type) {
			plaintext, err := secrets.DecryptMonitorConfig(s.secretsEncryptor, monitor.Type, configJSON)
			if err != nil {
				return nil, fmt.Errorf("decrypt monitor config for location %s: %w", locationID, err)
			}
			locationEncryptor, err := locationauth.ConfigEncryptor(credential)
			if err != nil {
				return nil, fmt.Errorf("derive config key for location %s: %w", locationID, err)
			}
			configJSON, err = secrets.EncryptMonitorConfig(locationEncryptor, monitor.Type, plaintext)
			if err != nil {
				return nil, fmt.Errorf("encrypt monitor config for location %s: %w", locationID, err)
			}
		}
	}

	payload := models.CheckJobPayload{
		MonitorID:      monitor.ID.String(),
		Type:           monitor.Type,
		Config:         configJSON,
		TimeoutSeconds: monitor.TimeoutSeconds,
		LocationID:     locationID,
	}

	payloadJSON, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal job payload: %w", err)
	}

	now := time.Now()
	deadline := now.Add(time.Duration(2*monitor.TimeoutSeconds) * time.Second)

	job := models.NewJob(jobID, monitor.TenantID.String(), models.JobTypeCheck, "v1", payloadJSON)
	job = job.WithDeadline(deadline)

	return job, nil
}

// jobSubject returns the per-location subject a job is published to.
func (s *Scheduler) jobSubject(locationID string) string {
	if locationID == "" {
		return models.CheckJobSubjectDefault(s.config.CheckJobSubject)
	}
	return models.CheckJobSubjectForLocation(s.config.CheckJobSubject, locationID)
}

func (s *Scheduler) ensureLocationConsumer(ctx context.Context, locationID string) error {
	if locationID == "" || s.queue == nil {
		return nil
	}
	_, err := s.queue.CreateConsumerWithOptions(ctx, s.config.CheckJobStream,
		models.CheckJobConsumerForLocation(locationID),
		queue.ConsumerOptions{FilterSubject: s.jobSubject(locationID)})
	if err != nil {
		return fmt.Errorf("ensure location consumer: %w", err)
	}
	return nil
}

// publishJob publishes a job to NATS (via the publish seam).
func (s *Scheduler) publishJob(ctx context.Context, subject string, job *models.Job) error {
	if err := s.publish(ctx, subject, job); err != nil {
		return fmt.Errorf("failed to publish job: %w", err)
	}
	return nil
}

// publishMonitorJobs fans a due monitor out to its locations (or the default
// fleet when none are selected). It returns how many jobs were published; the
// caller reschedules the monitor iff at least one publish succeeded, so a
// NATS blip retries next tick without skipping an interval.
func (s *Scheduler) publishMonitorJobs(ctx context.Context, monitor Monitor) (published, failed int) {
	locationIDs := []string{""}
	if len(monitor.LocationIDs) > 0 {
		locationIDs = locationIDs[:0]
		for _, id := range monitor.LocationIDs {
			locationIDs = append(locationIDs, id.String())
		}
	}

	for _, locationID := range locationIDs {
		if err := s.ensureLocationConsumer(ctx, locationID); err != nil {
			s.logger.WithError(err).WithField("location_id", locationID).Error("Failed to ensure location consumer")
			failed++
			continue
		}
		job, err := s.createCheckJob(monitor, locationID)
		if err != nil {
			s.logger.WithError(err).
				WithField("monitor_id", monitor.ID).
				WithField("tenant_id", monitor.TenantID).
				Error("Failed to create check job")
			failed++
			continue
		}

		if err := s.publishJob(ctx, s.jobSubject(locationID), job); err != nil {
			s.jobsPublishErrors.With(prometheus.Labels{}).Inc()
			s.logger.WithError(err).
				WithField("monitor_id", monitor.ID).
				WithField("tenant_id", monitor.TenantID).
				WithField("location_id", locationID).
				WithField("job_id", job.ID).
				Error("Failed to publish job")
			failed++
			continue
		}
		published++
	}
	return published, failed
}

// updateMonitorNextRunAt updates the next_run_at for a monitor within a transaction
func (s *Scheduler) updateMonitorNextRunAt(ctx context.Context, tx *sql.Tx, monitorID uuid.UUID, nextRunAt time.Time) error {
	query := `
		UPDATE monitors
		SET next_run_at = $1, updated_at = NOW()
		WHERE id = $2 AND deleted_at IS NULL
	`

	_, err := tx.ExecContext(ctx, query, nextRunAt, monitorID)
	if err != nil {
		return fmt.Errorf("failed to update monitor next_run_at: %w", err)
	}

	return nil
}

// scheduleBatch processes a batch of due monitors
// Uses a transaction to ensure FOR UPDATE SKIP LOCKED works correctly with concurrent schedulers
func (s *Scheduler) scheduleBatch(ctx context.Context) {
	startTime := time.Now()
	s.loopsTotal.With(prometheus.Labels{}).Inc()

	// Start a transaction for atomicity and proper locking
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		s.dbErrors.With(prometheus.Labels{}).Inc()
		s.logger.WithError(err).Error("Failed to begin transaction")
		return
	}
	defer tx.Rollback()

	// Fetch due monitors within the transaction (with FOR UPDATE SKIP LOCKED)
	monitors, err := s.fetchDueMonitors(ctx, tx, s.config.SchedulerBatchSize)
	if err != nil {
		s.dbErrors.With(prometheus.Labels{}).Inc()
		s.logger.WithError(err).Error("Failed to fetch due monitors")
		return
	}

	batchSize := len(monitors)
	s.monitorsInBatch.With(prometheus.Labels{}).Observe(float64(batchSize))

	if batchSize == 0 {
		// No monitors to schedule, just record the loop duration
		// Still need to commit (or rollback) the transaction
		if err := tx.Commit(); err != nil {
			s.dbErrors.With(prometheus.Labels{}).Inc()
			s.logger.WithError(err).Error("Failed to commit empty transaction")
		}
		duration := time.Since(startTime).Seconds()
		s.loopDuration.With(prometheus.Labels{}).Observe(duration)
		return
	}

	// Process each monitor
	scheduledCount := 0
	publishErrors := 0

	for _, monitor := range monitors {
		// Fan out to the monitor's locations (or the default fleet) — jobs go
		// to NATS outside the transaction; the DB reschedule below only
		// happens when at least one job made it out.
		published, failed := s.publishMonitorJobs(ctx, monitor)
		publishErrors += failed
		if published == 0 {
			// Nothing published: leave next_run_at untouched so the monitor
			// is retried on the next tick.
			continue
		}

		// Calculate next run time
		nextRunAt := time.Now().Add(nextCheckDelay(monitor.CurrentState, monitor.IntervalSeconds))

		// Update monitor's next_run_at within the transaction
		if err := s.updateMonitorNextRunAt(ctx, tx, monitor.ID, nextRunAt); err != nil {
			s.dbErrors.With(prometheus.Labels{}).Inc()
			s.logger.WithError(err).
				WithField("monitor_id", monitor.ID).
				WithField("tenant_id", monitor.TenantID).
				Error("Failed to update monitor next_run_at")
			// If DB update fails, we've already published the job(s)
			// This is acceptable - the monitor will be scheduled again, but the jobs are already queued
			// We continue to process other monitors
			continue
		}

		scheduledCount++
		s.logger.WithFields(logrus.Fields{
			"monitor_id":     monitor.ID,
			"tenant_id":      monitor.TenantID,
			"jobs_published": published,
		}).Debug("Scheduled monitor check")
	}

	// Commit the transaction
	// This releases the row locks and makes all updates visible
	if err := tx.Commit(); err != nil {
		s.dbErrors.With(prometheus.Labels{}).Inc()
		s.logger.WithError(err).Error("Failed to commit transaction")
		return
	}

	// Update metrics
	s.monitorsScheduled.With(prometheus.Labels{}).Add(float64(scheduledCount))
	duration := time.Since(startTime).Seconds()
	s.loopDuration.With(prometheus.Labels{}).Observe(duration)

	// Log batch summary
	if scheduledCount > 0 || publishErrors > 0 {
		s.logger.WithFields(logrus.Fields{
			"batch_size":       batchSize,
			"scheduled_count":  scheduledCount,
			"publish_errors":   publishErrors,
			"duration_seconds": duration,
		}).Info("Scheduler batch completed")
	}
}

// Shutdown gracefully shuts down the scheduler
func (s *Scheduler) Shutdown(ctx context.Context) error {
	s.logger.Info("Shutting down scheduler")

	// Cancel context to stop scheduling loop
	s.cancel()

	// Close stop channel only once to signal the loop to stop
	s.stopOnce.Do(func() {
		close(s.stop)
	})

	// Start() will return when it sees the context is cancelled or stop channel is closed
	// The timeout in the caller (main.go) will handle cases where Start() doesn't stop in time
	return nil
}

func (s *Scheduler) triggerMonitorPurge() {
	if !s.config.MonitorPurgeEnabled {
		return
	}
	go func() {
		start := time.Now()
		purged, err := s.purger.runOnce(s.ctx)
		duration := time.Since(start).Seconds()
		s.purger.metrics.runs.With(prometheus.Labels{}).Inc()
		s.purger.metrics.runDuration.With(prometheus.Labels{}).Observe(duration)
		if err != nil {
			s.logger.WithError(err).Warn("monitor purge run failed")
			return
		}
		fields := logrus.Fields{
			"monitors_purged":  purged,
			"duration_seconds": duration,
		}
		if purged > 0 {
			s.logger.WithFields(fields).Info("monitor purge run completed")
		} else {
			s.logger.WithFields(fields).Debug("monitor purge tick completed (no-op)")
		}
	}()
}
