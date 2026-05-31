package testutil

import (
	"context"
	"fmt"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/google/uuid"
	testcontainers "github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/wait"

	shareddb "github.com/yassinebenameur/probara/shared/db"
)

func SetupPostgresDB(ctx context.Context, t testing.TB) (*shareddb.Client, func()) {
	t.Helper()

	container, dsn := startPostgresContainer(ctx, t)
	dbClient, err := newTestDBClient(ctx, dsn, 20*time.Second)
	if err != nil {
		_ = container.Terminate(ctx)
		t.Fatalf("newTestDBClient() error = %v", err)
	}

	_, filename, _, ok := runtime.Caller(0)
	if !ok {
		_ = container.Terminate(ctx)
		dbClient.Close()
		t.Fatalf("runtime.Caller() failed")
	}
	migrationsPath := filepath.Join(filepath.Dir(filename), "..", "db", "migrations")
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

func InsertTenant(ctx context.Context, t testing.TB, dbClient *shareddb.Client, name string) uuid.UUID {
	t.Helper()

	id := uuid.New()
	if name == "" {
		name = fmt.Sprintf("tenant-%s", id.String())
	}
	if _, err := dbClient.ExecContext(ctx, `
		INSERT INTO tenants (id, name, created_at, updated_at)
		VALUES ($1, $2, NOW(), NOW())
	`, id, name); err != nil {
		t.Fatalf("insert tenant: %v", err)
	}
	return id
}

func InsertHTTPMonitor(ctx context.Context, t testing.TB, dbClient *shareddb.Client, tenantID uuid.UUID, name string) uuid.UUID {
	t.Helper()

	id := uuid.New()
	if name == "" {
		name = fmt.Sprintf("http-%s", id.String())
	}
	if _, err := dbClient.ExecContext(ctx, `
		INSERT INTO monitors (
			id, tenant_id, name, type, config, interval_seconds, timeout_seconds,
			alert_policy_id, enabled, tags, agent_id, push_token, next_run_at, created_at, updated_at
		) VALUES (
			$1, $2, $3, 'http', '{"url":"https://example.com","method":"GET"}'::jsonb, 60, 30,
			NULL, TRUE, ARRAY[]::text[], NULL, NULL, NULL, NOW(), NOW()
		)
	`, id, tenantID, name); err != nil {
		t.Fatalf("insert http monitor: %v", err)
	}
	return id
}

func InsertGroupMonitor(ctx context.Context, t testing.TB, dbClient *shareddb.Client, tenantID uuid.UUID, name string) uuid.UUID {
	t.Helper()

	id := uuid.New()
	if name == "" {
		name = fmt.Sprintf("group-%s", id.String())
	}
	if _, err := dbClient.ExecContext(ctx, `
		INSERT INTO monitors (
			id, tenant_id, name, type, config, interval_seconds, timeout_seconds,
			alert_policy_id, enabled, tags, agent_id, push_token, next_run_at, created_at, updated_at
		) VALUES (
			$1, $2, $3, 'group', '{"monitor_ids":[]}'::jsonb, 60, 30,
			NULL, TRUE, ARRAY[]::text[], NULL, NULL, NULL, NOW(), NOW()
		)
	`, id, tenantID, name); err != nil {
		t.Fatalf("insert group monitor: %v", err)
	}
	return id
}

func AddMonitorToGroup(ctx context.Context, t testing.TB, dbClient *shareddb.Client, monitorID, groupID uuid.UUID) {
	t.Helper()

	if _, err := dbClient.ExecContext(ctx, `
		INSERT INTO monitor_groups (monitor_id, group_id, created_at)
		VALUES ($1, $2, NOW())
	`, monitorID, groupID); err != nil {
		t.Fatalf("insert monitor group membership: %v", err)
	}
}

func InsertStatusPage(ctx context.Context, t testing.TB, dbClient *shareddb.Client, tenantID uuid.UUID, slug, title string) uuid.UUID {
	t.Helper()

	id := uuid.New()
	if slug == "" {
		slug = fmt.Sprintf("status-%s", id.String())
	}
	if title == "" {
		title = slug
	}
	if _, err := dbClient.ExecContext(ctx, `
		INSERT INTO status_pages (
			id, tenant_id, slug, title, description, logo_url, primary_color, secondary_color,
			created_at, updated_at, settings
		) VALUES (
			$1, $2, $3, $4, NULL, NULL, NULL, NULL, NOW(), NOW(), '{}'::jsonb
		)
	`, id, tenantID, slug, title); err != nil {
		t.Fatalf("insert status page: %v", err)
	}
	return id
}

func AddMonitorToStatusPage(ctx context.Context, t testing.TB, dbClient *shareddb.Client, statusPageID, monitorID uuid.UUID, position int) {
	t.Helper()

	if _, err := dbClient.ExecContext(ctx, `
		INSERT INTO status_page_monitors (status_page_id, monitor_id, position, display_name)
		VALUES ($1, $2, $3, NULL)
	`, statusPageID, monitorID, position); err != nil {
		t.Fatalf("insert status page monitor: %v", err)
	}
}

func InsertCheckResult(ctx context.Context, t testing.TB, dbClient *shareddb.Client, tenantID, monitorID uuid.UUID, createdAt time.Time, status, resultSource string, latencyMS *int) uuid.UUID {
	t.Helper()

	id := uuid.New()
	var latency interface{}
	if latencyMS != nil {
		latency = *latencyMS
	}
	if _, err := dbClient.ExecContext(ctx, `
		INSERT INTO check_results (
			id, monitor_id, tenant_id, job_id, status, http_status, latency_ms, error_message,
			matched_body_substring, created_at, started_at, completed_at, result_source
		) VALUES ($1, $2, $3, $4, $5, 200, $6, NULL, FALSE, $7, $7, $7, $8)
	`, id, monitorID, tenantID, uuid.New(), status, latency, createdAt.UTC(), resultSource); err != nil {
		t.Fatalf("insert check result: %v", err)
	}
	return id
}

func InsertDailyRollup(ctx context.Context, t testing.TB, dbClient *shareddb.Client, tenantID, monitorID uuid.UUID, bucketDay time.Time, totalChecks, successChecks int, latencySumMS float64, latencyCount int, latestStatus string, latestCheckAt time.Time) {
	t.Helper()

	day := time.Date(bucketDay.UTC().Year(), bucketDay.UTC().Month(), bucketDay.UTC().Day(), 0, 0, 0, 0, time.UTC)
	if _, err := dbClient.ExecContext(ctx, `
		INSERT INTO monitor_daily_rollups (
			tenant_id, monitor_id, bucket_day, total_checks, success_checks,
			latency_success_sum_ms, latency_success_count, latest_status, latest_check_at,
			created_at, updated_at
		) VALUES ($1, $2, $3, $4, $5, $6, $7, NULLIF($8, ''), $9, NOW(), NOW())
	`, tenantID, monitorID, day, totalChecks, successChecks, latencySumMS, latencyCount, latestStatus, latestCheckAt.UTC()); err != nil {
		t.Fatalf("insert daily rollup: %v", err)
	}
}

func InsertHourlyRollup(ctx context.Context, t testing.TB, dbClient *shareddb.Client, tenantID, monitorID uuid.UUID, bucketHour time.Time, totalChecks, successChecks int, latencySumMS float64, latencyCount int, latestStatus string, latestCheckAt time.Time) {
	t.Helper()

	hour := bucketHour.UTC().Truncate(time.Hour)
	if _, err := dbClient.ExecContext(ctx, `
		INSERT INTO monitor_hourly_rollups (
			tenant_id, monitor_id, bucket_hour, total_checks, success_checks,
			latency_success_sum_ms, latency_success_count, latest_status, latest_check_at,
			created_at, updated_at
		) VALUES ($1, $2, $3, $4, $5, $6, $7, NULLIF($8, ''), $9, NOW(), NOW())
	`, tenantID, monitorID, hour, totalChecks, successChecks, latencySumMS, latencyCount, latestStatus, latestCheckAt.UTC()); err != nil {
		t.Fatalf("insert hourly rollup: %v", err)
	}
}

func InsertRollupJobState(ctx context.Context, t testing.TB, dbClient *shareddb.Client, jobName string, lastCreatedAt time.Time, lastCheckResultID uuid.UUID) {
	t.Helper()

	if _, err := dbClient.ExecContext(ctx, `
		INSERT INTO rollup_job_state (job_name, last_created_at, last_check_result_id, last_run_at, updated_at)
		VALUES ($1, $2, $3, NOW(), NOW())
		ON CONFLICT (job_name) DO UPDATE SET
			last_created_at = EXCLUDED.last_created_at,
			last_check_result_id = EXCLUDED.last_check_result_id,
			last_run_at = NOW(),
			updated_at = NOW()
	`, jobName, lastCreatedAt.UTC(), lastCheckResultID); err != nil {
		t.Fatalf("insert rollup job state: %v", err)
	}
}

func InsertDowntimePeriod(ctx context.Context, t testing.TB, dbClient *shareddb.Client, tenantID, monitorID uuid.UUID, start, end time.Time) {
	t.Helper()

	if _, err := dbClient.ExecContext(ctx, `
		INSERT INTO monitor_downtime_periods (tenant_id, monitor_id, start_time, end_time, created_at, updated_at)
		VALUES ($1, $2, $3, $4, NOW(), NOW())
	`, tenantID, monitorID, start.UTC(), end.UTC()); err != nil {
		t.Fatalf("insert downtime period: %v", err)
	}
}

func InsertOpenDowntime(ctx context.Context, t testing.TB, dbClient *shareddb.Client, tenantID, monitorID uuid.UUID, startedAt, lastSeenAt time.Time) {
	t.Helper()

	if _, err := dbClient.ExecContext(ctx, `
		INSERT INTO monitor_downtime_open (tenant_id, monitor_id, started_at, last_seen_at, created_at, updated_at)
		VALUES ($1, $2, $3, $4, NOW(), NOW())
	`, tenantID, monitorID, startedAt.UTC(), lastSeenAt.UTC()); err != nil {
		t.Fatalf("insert open downtime: %v", err)
	}
}

func IntPtr(v int) *int {
	return &v
}

func startPostgresContainer(ctx context.Context, t testing.TB) (testcontainers.Container, string) {
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

func newTestDBClient(ctx context.Context, dsn string, timeout time.Duration) (*shareddb.Client, error) {
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
