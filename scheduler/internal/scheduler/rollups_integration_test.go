package scheduler

// Integration tests for the dirty-ledger rollup consumer (rollups_dirty.go).
//
// Result ingestion (shared/monitorstate/record.go) marks each inserted row's
// (monitor, hour) in rollup_dirty within the same transaction. These tests
// seed check_results with raw SQL, which BYPASSES ingestion — so every seed
// must also write the dirty mark itself (insertMarkedCheckResult /
// markRollupDirty), exactly as ingestion would have.

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

// TestRollupMaintenance_ConsumesLedgerAndAdvancesWatermark is the core
// consumer test: marked buckets are rebuilt wholesale from raw rows, the
// affected daily buckets are re-derived from the hourly table, the ledger is
// drained, and the completeness watermark (rollup_job_state) lands on the
// newest monitor-source row.
func TestRollupMaintenance_ConsumesLedgerAndAdvancesWatermark(t *testing.T) {
	testcontainers.SkipIfProviderIsNotHealthy(t)

	ctx := context.Background()
	dbClient, cleanup := setupRollupTestDB(ctx, t)
	defer cleanup()

	s := newTestScheduler(dbClient)
	tenantID := insertTenant(ctx, t, dbClient)
	monitorID := insertMonitor(ctx, t, dbClient, tenantID, "rollup-consumer")

	// Two dirty hours on the same day.
	hourA := time.Date(2026, time.January, 2, 9, 0, 0, 0, time.UTC)
	hourB := hourA.Add(time.Hour)
	insertMarkedCheckResult(ctx, t, dbClient, tenantID, monitorID, hourA.Add(5*time.Minute), string(sharedmodels.ResultStatusSuccess), 50)
	insertMarkedCheckResult(ctx, t, dbClient, tenantID, monitorID, hourA.Add(10*time.Minute), string(sharedmodels.ResultStatusFailure), 0)
	insertMarkedCheckResult(ctx, t, dbClient, tenantID, monitorID, hourB.Add(15*time.Minute), string(sharedmodels.ResultStatusSuccess), 90)
	newest := hourB.Add(20 * time.Minute)
	insertMarkedCheckResult(ctx, t, dbClient, tenantID, monitorID, newest, string(sharedmodels.ResultStatusError), 0)

	processed, cursorUnix, err := s.runRollupMaintenance()
	if err != nil {
		t.Fatalf("runRollupMaintenance() error = %v", err)
	}
	if processed != 2 {
		t.Fatalf("processed = %d, want 2 dirty buckets", processed)
	}
	if cursorUnix != newest.Unix() {
		t.Fatalf("cursorUnix = %d, want %d", cursorUnix, newest.Unix())
	}

	gotA, ok := queryHourlyBucket(ctx, t, dbClient, monitorID, hourA)
	if !ok {
		t.Fatalf("hourly bucket %v missing", hourA)
	}
	assertBucket(t, "hourA", gotA, rollupBucket{
		TotalChecks: 2, SuccessChecks: 1, ErrorChecks: 0,
		LatencySuccessSumMS: 50, LatencySuccessCount: 1,
		LatestStatus: string(sharedmodels.ResultStatusFailure), LatestCheckAt: hourA.Add(10 * time.Minute),
	})
	gotB, ok := queryHourlyBucket(ctx, t, dbClient, monitorID, hourB)
	if !ok {
		t.Fatalf("hourly bucket %v missing", hourB)
	}
	assertBucket(t, "hourB", gotB, rollupBucket{
		TotalChecks: 2, SuccessChecks: 1, ErrorChecks: 1,
		LatencySuccessSumMS: 90, LatencySuccessCount: 1,
		LatestStatus: string(sharedmodels.ResultStatusError), LatestCheckAt: newest,
	})
	gotDay, ok := queryDailyBucket(ctx, t, dbClient, monitorID, hourA)
	if !ok {
		t.Fatalf("daily bucket missing")
	}
	assertBucket(t, "day", gotDay, rollupBucket{
		TotalChecks: 4, SuccessChecks: 2, ErrorChecks: 1,
		LatencySuccessSumMS: 140, LatencySuccessCount: 2,
		LatestStatus: string(sharedmodels.ResultStatusError), LatestCheckAt: newest,
	})

	if got := countDirtyMarks(ctx, t, dbClient); got != 0 {
		t.Fatalf("rollup_dirty rows = %d, want 0 (ledger drained)", got)
	}

	// The watermark row equals the newest monitor-source row's created_at.
	var lastCreatedAt time.Time
	if err := dbClient.QueryRowContext(ctx, `
		SELECT last_created_at FROM rollup_job_state WHERE job_name = $1
	`, rollupJobName).Scan(&lastCreatedAt); err != nil {
		t.Fatalf("load rollup watermark: %v", err)
	}
	if !lastCreatedAt.Equal(newest) {
		t.Fatalf("rollup_job_state.last_created_at = %v, want %v", lastCreatedAt, newest)
	}
}

// TestRollupMaintenance_ProcessesMultipleBatchesPerRun seeds more distinct
// dirty buckets than one consumer batch holds and asserts a single run drains
// them all.
func TestRollupMaintenance_ProcessesMultipleBatchesPerRun(t *testing.T) {
	testcontainers.SkipIfProviderIsNotHealthy(t)

	ctx := context.Background()
	dbClient, cleanup := setupRollupTestDB(ctx, t)
	defer cleanup()

	s := newTestScheduler(dbClient)
	tenantID := insertTenant(ctx, t, dbClient)
	monitorID := insertMonitor(ctx, t, dbClient, tenantID, "rollup-multi-batch")

	// One row per hour across bucketCount distinct hours: more dirty buckets
	// than rollupMaintenanceBatchSize, all recent enough to survive pruning.
	bucketCount := rollupMaintenanceBatchSize + 3
	base := time.Now().UTC().Add(-time.Duration(bucketCount+24) * time.Hour).Truncate(time.Hour)
	if _, err := dbClient.ExecContext(ctx, `
		INSERT INTO check_results (
			id, monitor_id, tenant_id, job_id, status, http_status, latency_ms, error_message,
			matched_body_substring, created_at, started_at, completed_at, result_source
		)
		SELECT gen_random_uuid(), $1, $2, gen_random_uuid(), 'success', 200, 50, NULL, FALSE, ts, ts, ts, 'monitor'
		FROM generate_series($3::timestamptz, $3::timestamptz + ($4 - 1) * INTERVAL '1 hour', INTERVAL '1 hour') AS ts
	`, monitorID, tenantID, base, bucketCount); err != nil {
		t.Fatalf("seed check results: %v", err)
	}
	if _, err := dbClient.ExecContext(ctx, `
		INSERT INTO rollup_dirty (monitor_id, bucket_hour)
		SELECT $1, ts
		FROM generate_series($2::timestamptz, $2::timestamptz + ($3 - 1) * INTERVAL '1 hour', INTERVAL '1 hour') AS ts
		ON CONFLICT DO NOTHING
	`, monitorID, base, bucketCount); err != nil {
		t.Fatalf("seed dirty marks: %v", err)
	}

	processed, cursorUnix, err := s.runRollupMaintenance()
	if err != nil {
		t.Fatalf("runRollupMaintenance() error = %v", err)
	}
	if processed != bucketCount {
		t.Fatalf("processed = %d, want %d (all batches in one run)", processed, bucketCount)
	}
	newest := base.Add(time.Duration(bucketCount-1) * time.Hour)
	if cursorUnix != newest.Unix() {
		t.Fatalf("cursorUnix = %d, want %d", cursorUnix, newest.Unix())
	}
	if got := countDirtyMarks(ctx, t, dbClient); got != 0 {
		t.Fatalf("rollup_dirty rows = %d, want 0 (ledger drained in one run)", got)
	}

	var hourlyCount, hourlyTotal int
	if err := dbClient.QueryRowContext(ctx, `
		SELECT COUNT(*), COALESCE(SUM(total_checks), 0) FROM monitor_hourly_rollups WHERE monitor_id = $1
	`, monitorID).Scan(&hourlyCount, &hourlyTotal); err != nil {
		t.Fatalf("count hourly rollups: %v", err)
	}
	if hourlyCount != bucketCount || hourlyTotal != bucketCount {
		t.Fatalf("hourly buckets/total = %d/%d, want %d/%d", hourlyCount, hourlyTotal, bucketCount, bucketCount)
	}
	var dailyTotal int
	if err := dbClient.QueryRowContext(ctx, `
		SELECT COALESCE(SUM(total_checks), 0) FROM monitor_daily_rollups WHERE monitor_id = $1
	`, monitorID).Scan(&dailyTotal); err != nil {
		t.Fatalf("sum daily rollups: %v", err)
	}
	if dailyTotal != bucketCount {
		t.Fatalf("daily total_checks sum = %d, want %d", dailyTotal, bucketCount)
	}
}

// TestRollupMaintenance_ExcludesPlatformRows: the rebuild aggregates only
// result_source='monitor' rows; a platform row inside a marked bucket must
// not count (ingestion never marks platform rows, but the filter has to hold
// even when the bucket is dirty for other reasons).
func TestRollupMaintenance_ExcludesPlatformRows(t *testing.T) {
	testcontainers.SkipIfProviderIsNotHealthy(t)

	ctx := context.Background()
	dbClient, cleanup := setupRollupTestDB(ctx, t)
	defer cleanup()

	s := newTestScheduler(dbClient)
	tenantID := insertTenant(ctx, t, dbClient)
	monitorID := insertMonitor(ctx, t, dbClient, tenantID, "rollup-platform-excluded")

	base := time.Date(2026, time.January, 1, 0, 10, 0, 0, time.UTC)
	insertMarkedCheckResult(ctx, t, dbClient, tenantID, monitorID, base, string(sharedmodels.ResultStatusSuccess), 120)
	insertCheckResult(ctx, t, dbClient, tenantID, monitorID, base.Add(10*time.Minute), string(sharedmodels.ResultStatusFailure), string(sharedmodels.ResultSourcePlatform), 0)
	insertMarkedCheckResult(ctx, t, dbClient, tenantID, monitorID, base.Add(20*time.Minute), string(sharedmodels.ResultStatusFailure), 0)
	insertMarkedCheckResult(ctx, t, dbClient, tenantID, monitorID, base.Add(35*time.Minute), string(sharedmodels.ResultStatusSuccess), 80)

	processed, _, err := s.runRollupMaintenance()
	if err != nil {
		t.Fatalf("runRollupMaintenance() error = %v", err)
	}
	if processed != 1 {
		t.Fatalf("processed = %d, want 1 dirty bucket", processed)
	}

	want := rollupBucket{
		TotalChecks: 3, SuccessChecks: 2, ErrorChecks: 0,
		LatencySuccessSumMS: 200, LatencySuccessCount: 2,
		LatestStatus: string(sharedmodels.ResultStatusSuccess), LatestCheckAt: base.Add(35 * time.Minute),
	}
	gotHour, ok := queryHourlyBucket(ctx, t, dbClient, monitorID, base.Truncate(time.Hour))
	if !ok {
		t.Fatalf("hourly bucket missing")
	}
	assertBucket(t, "hourly (platform row excluded)", gotHour, want)
	gotDay, ok := queryDailyBucket(ctx, t, dbClient, monitorID, base)
	if !ok {
		t.Fatalf("daily bucket missing")
	}
	assertBucket(t, "daily (platform row excluded)", gotDay, want)
}

// TestRollupMaintenance_DowntimeOpenAndClose: the downtime tables are a
// projection of monitor_state_intervals — closed 'down' intervals upsert into
// monitor_downtime_periods, and monitor_downtime_open mirrors the currently
// open 'down' interval (deleted once the monitor recovers).
func TestRollupMaintenance_DowntimeOpenAndClose(t *testing.T) {
	testcontainers.SkipIfProviderIsNotHealthy(t)

	ctx := context.Background()
	dbClient, cleanup := setupRollupTestDB(ctx, t)
	defer cleanup()

	s := newTestScheduler(dbClient)
	tenantID := insertTenant(ctx, t, dbClient)
	closedMon := insertMonitor(ctx, t, dbClient, tenantID, "downtime-closed")
	openMon := insertMonitor(ctx, t, dbClient, tenantID, "downtime-open")

	now := time.Now().UTC().Truncate(time.Second)
	closedStart := now.Add(-2 * time.Hour)
	closedEnd := closedStart.Add(10 * time.Minute)
	insertStateInterval(ctx, t, dbClient, tenantID, closedMon, "down", closedStart, &closedEnd)
	insertStateInterval(ctx, t, dbClient, tenantID, closedMon, "up", closedEnd, nil)

	openStart := now.Add(-30 * time.Minute)
	insertStateInterval(ctx, t, dbClient, tenantID, openMon, "down", openStart, nil)

	if _, _, err := s.runRollupMaintenance(); err != nil {
		t.Fatalf("first runRollupMaintenance() error = %v", err)
	}

	var periodStart, periodEnd time.Time
	if err := dbClient.QueryRowContext(ctx, `
		SELECT start_time, end_time FROM monitor_downtime_periods WHERE monitor_id = $1
	`, closedMon).Scan(&periodStart, &periodEnd); err != nil {
		t.Fatalf("query closed downtime period: %v", err)
	}
	if !periodStart.Equal(closedStart) || !periodEnd.Equal(closedEnd) {
		t.Fatalf("closed period = [%v, %v], want [%v, %v]", periodStart, periodEnd, closedStart, closedEnd)
	}
	if got := countRows(ctx, t, dbClient, `SELECT COUNT(*) FROM monitor_downtime_open WHERE monitor_id = $1`, closedMon); got != 0 {
		t.Fatalf("open rows for recovered monitor = %d, want 0", got)
	}

	var openStartGot time.Time
	if err := dbClient.QueryRowContext(ctx, `
		SELECT started_at FROM monitor_downtime_open WHERE monitor_id = $1
	`, openMon).Scan(&openStartGot); err != nil {
		t.Fatalf("query open downtime row: %v", err)
	}
	if !openStartGot.Equal(openStart) {
		t.Fatalf("open started_at = %v, want %v", openStartGot, openStart)
	}
	if got := countRows(ctx, t, dbClient, `SELECT COUNT(*) FROM monitor_downtime_periods WHERE monitor_id = $1`, openMon); got != 0 {
		t.Fatalf("periods for still-down monitor = %d, want 0", got)
	}

	// Recovery: close the open down interval and open a following up interval.
	recoverAt := openStart.Add(15 * time.Minute)
	if _, err := dbClient.ExecContext(ctx, `
		UPDATE monitor_state_intervals SET ended_at = $1 WHERE monitor_id = $2 AND ended_at IS NULL
	`, recoverAt, openMon); err != nil {
		t.Fatalf("close open interval: %v", err)
	}
	insertStateInterval(ctx, t, dbClient, tenantID, openMon, "up", recoverAt, nil)

	if _, _, err := s.runRollupMaintenance(); err != nil {
		t.Fatalf("second runRollupMaintenance() error = %v", err)
	}

	if got := countRows(ctx, t, dbClient, `SELECT COUNT(*) FROM monitor_downtime_open WHERE monitor_id = $1`, openMon); got != 0 {
		t.Fatalf("open rows after recovery = %d, want 0", got)
	}
	if err := dbClient.QueryRowContext(ctx, `
		SELECT start_time, end_time FROM monitor_downtime_periods WHERE monitor_id = $1
	`, openMon).Scan(&periodStart, &periodEnd); err != nil {
		t.Fatalf("query recovered downtime period: %v", err)
	}
	if !periodStart.Equal(openStart) || !periodEnd.Equal(recoverAt) {
		t.Fatalf("recovered period = [%v, %v], want [%v, %v]", periodStart, periodEnd, openStart, recoverAt)
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
	insertMarkedCheckResult(ctx, t, dbClient, tenantID, monitorID, createdAt, string(sharedmodels.ResultStatusSuccess), 90)
	if _, _, err := s.runRollupMaintenance(); err != nil {
		t.Fatalf("runRollupMaintenance() error = %v", err)
	}

	// Simulate raw retention: the rows disappear WITHOUT new dirty marks, so
	// the next run must leave the already-built rollups untouched (a bucket is
	// only rebuilt — and possibly deleted — when it is marked again).
	if _, err := dbClient.ExecContext(ctx, `DELETE FROM check_results WHERE tenant_id = $1`, tenantID); err != nil {
		t.Fatalf("delete raw check results: %v", err)
	}
	if _, _, err := s.runRollupMaintenance(); err != nil {
		t.Fatalf("post-prune runRollupMaintenance() error = %v", err)
	}

	if got := countRows(ctx, t, dbClient, `SELECT COUNT(*) FROM monitor_daily_rollups WHERE monitor_id = $1`, monitorID); got != 1 {
		t.Fatalf("daily rollup rows = %d, want 1 (survives raw pruning)", got)
	}
	if got := countRows(ctx, t, dbClient, `SELECT COUNT(*) FROM monitor_hourly_rollups WHERE monitor_id = $1`, monitorID); got != 1 {
		t.Fatalf("hourly rollup rows = %d, want 1 (survives raw pruning)", got)
	}
}

func TestRollupMaintenance_TracksErrorChecks(t *testing.T) {
	testcontainers.SkipIfProviderIsNotHealthy(t)

	ctx := context.Background()
	dbClient, cleanup := setupRollupTestDB(ctx, t)
	defer cleanup()

	s := newTestScheduler(dbClient)
	tenantID := insertTenant(ctx, t, dbClient)
	monitorID := insertMonitor(ctx, t, dbClient, tenantID, "rollup-error-checks")

	base := time.Date(2026, time.January, 5, 14, 5, 0, 0, time.UTC)
	insertMarkedCheckResult(ctx, t, dbClient, tenantID, monitorID, base, string(sharedmodels.ResultStatusSuccess), 70)
	insertMarkedCheckResult(ctx, t, dbClient, tenantID, monitorID, base.Add(time.Minute), string(sharedmodels.ResultStatusFailure), 0)
	insertMarkedCheckResult(ctx, t, dbClient, tenantID, monitorID, base.Add(2*time.Minute), string(sharedmodels.ResultStatusError), 0)
	insertMarkedCheckResult(ctx, t, dbClient, tenantID, monitorID, base.Add(3*time.Minute), string(sharedmodels.ResultStatusError), 0)

	if _, _, err := s.runRollupMaintenance(); err != nil {
		t.Fatalf("runRollupMaintenance() error = %v", err)
	}

	var hourlyTotal, hourlySuccess, hourlyErrors int
	if err := dbClient.QueryRowContext(ctx, `
		SELECT total_checks, success_checks, error_checks
		FROM monitor_hourly_rollups
		WHERE tenant_id = $1 AND monitor_id = $2 AND bucket_hour = $3
	`, tenantID, monitorID, base.Truncate(time.Hour)).Scan(&hourlyTotal, &hourlySuccess, &hourlyErrors); err != nil {
		t.Fatalf("query hourly rollup row: %v", err)
	}
	if hourlyTotal != 4 || hourlySuccess != 1 || hourlyErrors != 2 {
		t.Fatalf("hourly total/success/error = %d/%d/%d, want 4/1/2", hourlyTotal, hourlySuccess, hourlyErrors)
	}

	var dailyTotal, dailySuccess, dailyErrors int
	if err := dbClient.QueryRowContext(ctx, `
		SELECT total_checks, success_checks, error_checks
		FROM monitor_daily_rollups
		WHERE tenant_id = $1 AND monitor_id = $2 AND bucket_day = $3::date
	`, tenantID, monitorID, base).Scan(&dailyTotal, &dailySuccess, &dailyErrors); err != nil {
		t.Fatalf("query daily rollup row: %v", err)
	}
	if dailyTotal != 4 || dailySuccess != 1 || dailyErrors != 2 {
		t.Fatalf("daily total/success/error = %d/%d/%d, want 4/1/2", dailyTotal, dailySuccess, dailyErrors)
	}
}

// TestRollupRebuildIncludesLateRow covers the exact scenario the retired
// cursor's permanent-skip bug lost: a row committing with a created_at BEHIND
// the already-advanced cursor (late commit / lagging worker clock). The
// retired cursor scanned (created_at, id) strictly forward, so such a row was
// never folded into its bucket — the ~0.1-0.6% rollup undercount. With the
// dirty ledger the late insert re-marks its (already-processed, past) bucket
// and the next run rebuilds it wholesale.
func TestRollupRebuildIncludesLateRow(t *testing.T) {
	testcontainers.SkipIfProviderIsNotHealthy(t)

	ctx := context.Background()
	dbClient, cleanup := setupRollupTestDB(ctx, t)
	defer cleanup()

	s := newTestScheduler(dbClient)
	tenantID := insertTenant(ctx, t, dbClient)
	monitorID := insertMonitor(ctx, t, dbClient, tenantID, "rollup-late-row")

	hour := time.Date(2026, time.July, 10, 12, 0, 0, 0, time.UTC)
	insertMarkedCheckResult(ctx, t, dbClient, tenantID, monitorID, hour.Add(5*time.Minute), string(sharedmodels.ResultStatusSuccess), 40)
	insertMarkedCheckResult(ctx, t, dbClient, tenantID, monitorID, hour.Add(10*time.Minute), string(sharedmodels.ResultStatusSuccess), 60)
	watermarkAt := hour.Add(40 * time.Minute)
	insertMarkedCheckResult(ctx, t, dbClient, tenantID, monitorID, watermarkAt, string(sharedmodels.ResultStatusSuccess), 80)

	if _, cursorUnix, err := s.runRollupMaintenance(); err != nil {
		t.Fatalf("first runRollupMaintenance() error = %v", err)
	} else if cursorUnix != watermarkAt.Unix() {
		t.Fatalf("cursorUnix = %d, want %d", cursorUnix, watermarkAt.Unix())
	}
	gotHour, ok := queryHourlyBucket(ctx, t, dbClient, monitorID, hour)
	if !ok {
		t.Fatalf("hourly bucket missing after first run")
	}
	if gotHour.TotalChecks != 3 {
		t.Fatalf("hourly total_checks = %d, want 3", gotHour.TotalChecks)
	}

	// The late row: created_at inside the already-processed hour, strictly
	// behind the advanced watermark — the row class the retired cursor
	// permanently skipped.
	lateAt := hour.Add(20 * time.Minute)
	insertMarkedCheckResult(ctx, t, dbClient, tenantID, monitorID, lateAt, string(sharedmodels.ResultStatusFailure), 0)

	processed, cursorUnix, err := s.runRollupMaintenance()
	if err != nil {
		t.Fatalf("second runRollupMaintenance() error = %v", err)
	}
	if processed != 1 {
		t.Fatalf("processed = %d, want 1 (the re-marked bucket)", processed)
	}
	// The watermark stays on the newest row; the late row never moves it back.
	if cursorUnix != watermarkAt.Unix() {
		t.Fatalf("cursorUnix after late row = %d, want %d", cursorUnix, watermarkAt.Unix())
	}

	gotHour, ok = queryHourlyBucket(ctx, t, dbClient, monitorID, hour)
	if !ok {
		t.Fatalf("hourly bucket missing after rebuild")
	}
	assertBucket(t, "hourly (late row folded)", gotHour, rollupBucket{
		TotalChecks: 4, SuccessChecks: 3, ErrorChecks: 0,
		LatencySuccessSumMS: 180, LatencySuccessCount: 3,
		LatestStatus: string(sharedmodels.ResultStatusSuccess), LatestCheckAt: watermarkAt,
	})
	gotDay, ok := queryDailyBucket(ctx, t, dbClient, monitorID, hour)
	if !ok {
		t.Fatalf("daily bucket missing after rebuild")
	}
	assertBucket(t, "daily (late row folded)", gotDay, rollupBucket{
		TotalChecks: 4, SuccessChecks: 3, ErrorChecks: 0,
		LatencySuccessSumMS: 180, LatencySuccessCount: 3,
		LatestStatus: string(sharedmodels.ResultStatusSuccess), LatestCheckAt: watermarkAt,
	})
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

// markRollupDirty writes the dirty mark ingestion would have written in the
// same transaction as the raw insert (shared/monitorstate/record.go). Every
// test that seeds check_results with raw SQL must mark the touched buckets or
// the consumer has nothing to rebuild.
func markRollupDirty(ctx context.Context, t *testing.T, dbClient *shareddb.Client, monitorID uuid.UUID, createdAt time.Time) {
	t.Helper()
	if _, err := dbClient.ExecContext(ctx, `
		INSERT INTO rollup_dirty (monitor_id, bucket_hour)
		VALUES ($1, date_trunc('hour', $2::timestamptz))
		ON CONFLICT DO NOTHING
	`, monitorID, createdAt); err != nil {
		t.Fatalf("mark rollup dirty: %v", err)
	}
}

// insertMarkedCheckResult inserts a monitor-source raw row plus its dirty mark.
func insertMarkedCheckResult(ctx context.Context, t *testing.T, dbClient *shareddb.Client, tenantID, monitorID uuid.UUID, createdAt time.Time, status string, latencyMS int) {
	t.Helper()
	insertCheckResult(ctx, t, dbClient, tenantID, monitorID, createdAt, status, string(sharedmodels.ResultSourceMonitor), latencyMS)
	markRollupDirty(ctx, t, dbClient, monitorID, createdAt)
}

func insertStateInterval(ctx context.Context, t *testing.T, dbClient *shareddb.Client, tenantID, monitorID uuid.UUID, state string, startedAt time.Time, endedAt *time.Time) {
	t.Helper()
	if _, err := dbClient.ExecContext(ctx, `
		INSERT INTO monitor_state_intervals (tenant_id, monitor_id, state, reason, started_at, ended_at)
		VALUES ($1, $2, $3, 'result', $4, $5)
	`, tenantID, monitorID, state, startedAt, endedAt); err != nil {
		t.Fatalf("insert state interval: %v", err)
	}
}

func countDirtyMarks(ctx context.Context, t *testing.T, dbClient *shareddb.Client) int {
	t.Helper()
	return countRows(ctx, t, dbClient, `SELECT COUNT(*) FROM rollup_dirty`)
}

func countRows(ctx context.Context, t *testing.T, dbClient *shareddb.Client, query string, args ...interface{}) int {
	t.Helper()
	var n int
	if err := dbClient.QueryRowContext(ctx, query, args...).Scan(&n); err != nil {
		t.Fatalf("count query %q: %v", query, err)
	}
	return n
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
