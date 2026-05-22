package scheduler

import (
	"context"
	"fmt"
	"path/filepath"
	"testing"
	"time"

	"github.com/google/uuid"
	testcontainers "github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/wait"

	"github.com/yassinebenameur/probara/shared/config"
	shareddb "github.com/yassinebenameur/probara/shared/db"
	"github.com/yassinebenameur/probara/shared/logger"
	"github.com/yassinebenameur/probara/shared/metrics"
	sharedmodels "github.com/yassinebenameur/probara/shared/models"
)

func TestRollupMaintenance_BackfillExcludesPlatformAndClosesDowntime(t *testing.T) {
	testcontainers.SkipIfProviderIsNotHealthy(t)

	ctx := context.Background()
	dbClient, cleanup := setupRollupTestDB(ctx, t)
	defer cleanup()

	s := newTestScheduler(dbClient)
	tenantID := insertTenant(ctx, t, dbClient)
	monitorID := insertMonitor(ctx, t, dbClient, tenantID, "rollup-backfill")

	base := time.Date(2026, time.January, 1, 0, 10, 0, 0, time.UTC)
	insertCheckResult(ctx, t, dbClient, tenantID, monitorID, base, string(sharedmodels.ResultStatusSuccess), string(sharedmodels.ResultSourceMonitor), 120)
	insertCheckResult(ctx, t, dbClient, tenantID, monitorID, base.Add(10*time.Minute), string(sharedmodels.ResultStatusFailure), string(sharedmodels.ResultSourcePlatform), 0)
	insertCheckResult(ctx, t, dbClient, tenantID, monitorID, base.Add(20*time.Minute), string(sharedmodels.ResultStatusFailure), string(sharedmodels.ResultSourceMonitor), 0)
	insertCheckResult(ctx, t, dbClient, tenantID, monitorID, base.Add(35*time.Minute), string(sharedmodels.ResultStatusSuccess), string(sharedmodels.ResultSourceMonitor), 80)

	processed, _, err := s.runRollupMaintenance()
	if err != nil {
		t.Fatalf("runRollupMaintenance() error = %v", err)
	}
	if processed != 3 {
		t.Fatalf("processed = %d, want 3", processed)
	}

	var totalChecks, successChecks, latencyCount int
	var latencySum float64
	err = dbClient.QueryRowContext(ctx, `
		SELECT total_checks, success_checks, latency_success_sum_ms, latency_success_count
		FROM monitor_daily_rollups
		WHERE tenant_id = $1 AND monitor_id = $2 AND bucket_day = $3::date
	`, tenantID, monitorID, base).Scan(&totalChecks, &successChecks, &latencySum, &latencyCount)
	if err != nil {
		t.Fatalf("query rollup row: %v", err)
	}
	if totalChecks != 3 {
		t.Fatalf("total_checks = %d, want 3", totalChecks)
	}
	if successChecks != 2 {
		t.Fatalf("success_checks = %d, want 2", successChecks)
	}
	if latencyCount != 2 {
		t.Fatalf("latency_success_count = %d, want 2", latencyCount)
	}
	if latencySum != 200 {
		t.Fatalf("latency_success_sum_ms = %v, want 200", latencySum)
	}

	var hourlyTotalChecks, hourlySuccessChecks, hourlyLatencyCount int
	var hourlyLatencySum float64
	hourBucket := base.Truncate(time.Hour)
	err = dbClient.QueryRowContext(ctx, `
		SELECT total_checks, success_checks, latency_success_sum_ms, latency_success_count
		FROM monitor_hourly_rollups
		WHERE tenant_id = $1 AND monitor_id = $2 AND bucket_hour = $3
	`, tenantID, monitorID, hourBucket).Scan(&hourlyTotalChecks, &hourlySuccessChecks, &hourlyLatencySum, &hourlyLatencyCount)
	if err != nil {
		t.Fatalf("query hourly rollup row: %v", err)
	}
	if hourlyTotalChecks != 3 {
		t.Fatalf("hourly total_checks = %d, want 3", hourlyTotalChecks)
	}
	if hourlySuccessChecks != 2 {
		t.Fatalf("hourly success_checks = %d, want 2", hourlySuccessChecks)
	}
	if hourlyLatencyCount != 2 {
		t.Fatalf("hourly latency_success_count = %d, want 2", hourlyLatencyCount)
	}
	if hourlyLatencySum != 200 {
		t.Fatalf("hourly latency_success_sum_ms = %v, want 200", hourlyLatencySum)
	}

	var downtimeCount int
	err = dbClient.QueryRowContext(ctx, `SELECT COUNT(*) FROM monitor_downtime_periods WHERE monitor_id = $1`, monitorID).Scan(&downtimeCount)
	if err != nil {
		t.Fatalf("count downtime periods: %v", err)
	}
	if downtimeCount != 1 {
		t.Fatalf("downtime period count = %d, want 1", downtimeCount)
	}

	err = dbClient.QueryRowContext(ctx, `SELECT COUNT(*) FROM monitor_downtime_open WHERE monitor_id = $1`, monitorID).Scan(&downtimeCount)
	if err != nil {
		t.Fatalf("count open downtime periods: %v", err)
	}
	if downtimeCount != 0 {
		t.Fatalf("open downtime period count = %d, want 0", downtimeCount)
	}
}

func TestRollupMaintenance_IncrementalCursorProcessing(t *testing.T) {
	testcontainers.SkipIfProviderIsNotHealthy(t)

	ctx := context.Background()
	dbClient, cleanup := setupRollupTestDB(ctx, t)
	defer cleanup()

	s := newTestScheduler(dbClient)
	tenantID := insertTenant(ctx, t, dbClient)
	monitorID := insertMonitor(ctx, t, dbClient, tenantID, "rollup-incremental")

	first := time.Date(2026, time.January, 2, 9, 0, 0, 0, time.UTC)
	insertCheckResult(ctx, t, dbClient, tenantID, monitorID, first, string(sharedmodels.ResultStatusSuccess), string(sharedmodels.ResultSourceMonitor), 50)
	if _, _, err := s.runRollupMaintenance(); err != nil {
		t.Fatalf("first runRollupMaintenance() error = %v", err)
	}

	second := first.Add(5 * time.Minute)
	insertCheckResult(ctx, t, dbClient, tenantID, monitorID, second, string(sharedmodels.ResultStatusFailure), string(sharedmodels.ResultSourceMonitor), 0)
	processed, cursorUnix, err := s.runRollupMaintenance()
	if err != nil {
		t.Fatalf("second runRollupMaintenance() error = %v", err)
	}
	if processed != 1 {
		t.Fatalf("processed on second run = %d, want 1", processed)
	}
	if cursorUnix != second.Unix() {
		t.Fatalf("cursorUnix = %d, want %d", cursorUnix, second.Unix())
	}

	var totalChecks int
	err = dbClient.QueryRowContext(ctx, `
		SELECT total_checks
		FROM monitor_daily_rollups
		WHERE tenant_id = $1 AND monitor_id = $2 AND bucket_day = $3::date
	`, tenantID, monitorID, first).Scan(&totalChecks)
	if err != nil {
		t.Fatalf("query rollup totals: %v", err)
	}
	if totalChecks != 2 {
		t.Fatalf("total_checks = %d, want 2", totalChecks)
	}
}

func TestRollupMaintenance_ProcessesMultipleBatchesPerRun(t *testing.T) {
	testcontainers.SkipIfProviderIsNotHealthy(t)

	ctx := context.Background()
	dbClient, cleanup := setupRollupTestDB(ctx, t)
	defer cleanup()

	s := newTestScheduler(dbClient)
	tenantID := insertTenant(ctx, t, dbClient)
	monitorID := insertMonitor(ctx, t, dbClient, tenantID, "rollup-multi-batch")

	base := time.Date(2026, time.January, 2, 10, 0, 0, 0, time.UTC)
	rowCount := rollupMaintenanceBatchSize + 3
	for i := 0; i < rowCount; i++ {
		insertCheckResult(
			ctx,
			t,
			dbClient,
			tenantID,
			monitorID,
			base.Add(time.Duration(i)*time.Second),
			string(sharedmodels.ResultStatusSuccess),
			string(sharedmodels.ResultSourceMonitor),
			50,
		)
	}

	processed, cursorUnix, err := s.runRollupMaintenance()
	if err != nil {
		t.Fatalf("runRollupMaintenance() error = %v", err)
	}
	if processed != rowCount {
		t.Fatalf("processed = %d, want %d", processed, rowCount)
	}
	if cursorUnix != base.Add(time.Duration(rowCount-1)*time.Second).Unix() {
		t.Fatalf("cursorUnix = %d, want %d", cursorUnix, base.Add(time.Duration(rowCount-1)*time.Second).Unix())
	}

	var totalChecks int
	err = dbClient.QueryRowContext(ctx, `
		SELECT total_checks
		FROM monitor_daily_rollups
		WHERE tenant_id = $1 AND monitor_id = $2 AND bucket_day = $3::date
	`, tenantID, monitorID, base).Scan(&totalChecks)
	if err != nil {
		t.Fatalf("query rollup totals: %v", err)
	}
	if totalChecks != rowCount {
		t.Fatalf("total_checks = %d, want %d", totalChecks, rowCount)
	}
}

func TestRollupMaintenance_DowntimeOpenAndClose(t *testing.T) {
	testcontainers.SkipIfProviderIsNotHealthy(t)

	ctx := context.Background()
	dbClient, cleanup := setupRollupTestDB(ctx, t)
	defer cleanup()

	s := newTestScheduler(dbClient)
	tenantID := insertTenant(ctx, t, dbClient)
	monitorID := insertMonitor(ctx, t, dbClient, tenantID, "rollup-downtime")

	failureAt := time.Date(2026, time.January, 3, 11, 0, 0, 0, time.UTC)
	insertCheckResult(ctx, t, dbClient, tenantID, monitorID, failureAt, string(sharedmodels.ResultStatusFailure), string(sharedmodels.ResultSourceMonitor), 0)
	if _, _, err := s.runRollupMaintenance(); err != nil {
		t.Fatalf("first runRollupMaintenance() error = %v", err)
	}

	var startedAt time.Time
	err := dbClient.QueryRowContext(ctx, `SELECT started_at FROM monitor_downtime_open WHERE monitor_id = $1`, monitorID).Scan(&startedAt)
	if err != nil {
		t.Fatalf("query open downtime row: %v", err)
	}
	if !startedAt.Equal(failureAt) {
		t.Fatalf("started_at = %v, want %v", startedAt, failureAt)
	}

	recoveryAt := failureAt.Add(15 * time.Minute)
	insertCheckResult(ctx, t, dbClient, tenantID, monitorID, recoveryAt, string(sharedmodels.ResultStatusSuccess), string(sharedmodels.ResultSourceMonitor), 40)
	if _, _, err := s.runRollupMaintenance(); err != nil {
		t.Fatalf("second runRollupMaintenance() error = %v", err)
	}

	var periodStart, periodEnd time.Time
	err = dbClient.QueryRowContext(ctx, `
		SELECT start_time, end_time
		FROM monitor_downtime_periods
		WHERE monitor_id = $1
	`, monitorID).Scan(&periodStart, &periodEnd)
	if err != nil {
		t.Fatalf("query closed downtime period: %v", err)
	}
	if !periodStart.Equal(failureAt) {
		t.Fatalf("period start = %v, want %v", periodStart, failureAt)
	}
	if !periodEnd.Equal(recoveryAt) {
		t.Fatalf("period end = %v, want %v", periodEnd, recoveryAt)
	}
}

func TestRollupMaintenance_RollupsSurviveRawPruning(t *testing.T) {
	testcontainers.SkipIfProviderIsNotHealthy(t)

	ctx := context.Background()
	dbClient, cleanup := setupRollupTestDB(ctx, t)
	defer cleanup()

	s := newTestScheduler(dbClient)
	tenantID := insertTenant(ctx, t, dbClient)
	monitorID := insertMonitor(ctx, t, dbClient, tenantID, "rollup-retention")

	createdAt := time.Now().UTC().AddDate(0, 0, -10)
	insertCheckResult(ctx, t, dbClient, tenantID, monitorID, createdAt, string(sharedmodels.ResultStatusSuccess), string(sharedmodels.ResultSourceMonitor), 90)
	if _, _, err := s.runRollupMaintenance(); err != nil {
		t.Fatalf("runRollupMaintenance() error = %v", err)
	}

	if _, err := dbClient.ExecContext(ctx, `DELETE FROM check_results WHERE tenant_id = $1`, tenantID); err != nil {
		t.Fatalf("delete raw check results: %v", err)
	}

	var rollupCount int
	err := dbClient.QueryRowContext(ctx, `SELECT COUNT(*) FROM monitor_daily_rollups WHERE tenant_id = $1 AND monitor_id = $2`, tenantID, monitorID).Scan(&rollupCount)
	if err != nil {
		t.Fatalf("count rollups after pruning raw results: %v", err)
	}
	if rollupCount != 1 {
		t.Fatalf("rollupCount = %d, want 1", rollupCount)
	}
}

func newTestScheduler(dbClient *shareddb.Client) *Scheduler {
	cfg := &config.SchedulerConfig{BaseConfig: config.BaseConfig{ServiceName: "scheduler_test"}}
	return NewScheduler(cfg, logger.New("scheduler_test", "error"), metrics.NewRegistry("scheduler_test"), dbClient, nil)
}

func setupRollupTestDB(ctx context.Context, t *testing.T) (*shareddb.Client, func()) {
	t.Helper()
	container, dsn := startSchedulerPostgresContainer(ctx, t)
	dbClient, err := newSchedulerTestDBClient(ctx, dsn, 20*time.Second)
	if err != nil {
		_ = container.Terminate(ctx)
		t.Fatalf("newSchedulerTestDBClient() error = %v", err)
	}
	migrationsPath := filepath.Join("..", "..", "..", "shared", "db", "migrations")
	if err := shareddb.Migrate(dbClient.DB, migrationsPath); err != nil {
		_ = container.Terminate(ctx)
		dbClient.Close()
		t.Fatalf("Migrate() error = %v", err)
	}
	return dbClient, func() {
		dbClient.Close()
		_ = container.Terminate(ctx)
	}
}

func insertTenant(ctx context.Context, t *testing.T, dbClient *shareddb.Client) uuid.UUID {
	t.Helper()
	id := uuid.New()
	if _, err := dbClient.ExecContext(ctx, `INSERT INTO tenants (id, name, created_at, updated_at) VALUES ($1, $2, NOW(), NOW())`, id, fmt.Sprintf("tenant-%s", id.String())); err != nil {
		t.Fatalf("insert tenant: %v", err)
	}
	return id
}

func insertMonitor(ctx context.Context, t *testing.T, dbClient *shareddb.Client, tenantID uuid.UUID, name string) uuid.UUID {
	t.Helper()
	id := uuid.New()
	if _, err := dbClient.ExecContext(ctx, `
		INSERT INTO monitors (
			id, tenant_id, name, type, config, interval_seconds, timeout_seconds,
			alert_policy_id, enabled, tags, agent_id, push_token, next_run_at, created_at, updated_at
		) VALUES ($1, $2, $3, 'http', '{"url":"https://example.com","method":"GET"}'::jsonb, 60, 30, NULL, TRUE, ARRAY[]::text[], NULL, NULL, NULL, NOW(), NOW())
	`, id, tenantID, name); err != nil {
		t.Fatalf("insert monitor: %v", err)
	}
	return id
}

func insertCheckResult(ctx context.Context, t *testing.T, dbClient *shareddb.Client, tenantID, monitorID uuid.UUID, createdAt time.Time, status, resultSource string, latencyMS int) {
	t.Helper()
	var latency interface{}
	if latencyMS > 0 {
		latency = latencyMS
	}
	if _, err := dbClient.ExecContext(ctx, `
		INSERT INTO check_results (
			id, monitor_id, tenant_id, job_id, status, http_status, latency_ms, error_message,
			matched_body_substring, created_at, started_at, completed_at, result_source
		) VALUES ($1, $2, $3, $4, $5, 200, $6, NULL, FALSE, $7, $7, $7, $8)
	`, uuid.New(), monitorID, tenantID, uuid.New(), status, latency, createdAt, resultSource); err != nil {
		t.Fatalf("insert check result: %v", err)
	}
}

func startSchedulerPostgresContainer(ctx context.Context, t *testing.T) (testcontainers.Container, string) {
	t.Helper()
	const (
		dbUser     = "uptime"
		dbPassword = "uptime"
		dbName     = "uptime"
	)
	req := testcontainers.ContainerRequest{
		Image:        "postgres:16-alpine",
		ExposedPorts: []string{"5432/tcp"},
		Env: map[string]string{
			"POSTGRES_USER":     dbUser,
			"POSTGRES_PASSWORD": dbPassword,
			"POSTGRES_DB":       dbName,
		},
		WaitingFor: wait.ForAll(
			wait.ForListeningPort("5432/tcp"),
			wait.ForLog("database system is ready to accept connections"),
		).WithDeadline(60 * time.Second),
	}
	container, err := testcontainers.GenericContainer(ctx, testcontainers.GenericContainerRequest{ContainerRequest: req, Started: true})
	if err != nil {
		t.Fatalf("failed to start postgres container: %v", err)
	}
	host, err := container.Host(ctx)
	if err != nil {
		_ = container.Terminate(ctx)
		t.Fatalf("failed to get postgres host: %v", err)
	}
	port, err := container.MappedPort(ctx, "5432/tcp")
	if err != nil {
		_ = container.Terminate(ctx)
		t.Fatalf("failed to get postgres port: %v", err)
	}
	return container, fmt.Sprintf("postgres://%s:%s@%s:%s/%s?sslmode=disable", dbUser, dbPassword, host, port.Port(), dbName)
}

func newSchedulerTestDBClient(ctx context.Context, dsn string, timeout time.Duration) (*shareddb.Client, error) {
	deadlineCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	var lastErr error
	ticker := time.NewTicker(500 * time.Millisecond)
	defer ticker.Stop()

	for {
		client, err := shareddb.NewClient(dsn)
		if err == nil {
			return client, nil
		}
		lastErr = err
		select {
		case <-deadlineCtx.Done():
			return nil, fmt.Errorf("timed out waiting for db readiness: %w", lastErr)
		case <-ticker.C:
		}
	}
}
