package scheduler

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	testcontainers "github.com/testcontainers/testcontainers-go"

	shareddb "github.com/yassinebenameur/probara/shared/db"
	sharedmodels "github.com/yassinebenameur/probara/shared/models"
)

// loadBackfillScript reads scripts/backfill_hourly_rollups.sql and substitutes
// the psql :'start' / :'end' variables with SQL literals so the script body
// can be executed directly through database/sql (multi-statement simple
// query). Everything else (advisory lock, BEGIN/COMMIT, SET LOCAL) runs
// exactly as it would under psql.
func loadBackfillScript(t *testing.T, start, end time.Time) string {
	t.Helper()
	path := filepath.Join("..", "..", "..", "scripts", "backfill_hourly_rollups.sql")
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read backfill script: %v", err)
	}
	script := string(raw)
	if !strings.Contains(script, ":'start'") || !strings.Contains(script, ":'end'") {
		t.Fatalf("backfill script no longer uses :'start'/:'end' psql variables; update this test")
	}
	const layout = "2006-01-02 15:04:05.999999-07"
	script = strings.ReplaceAll(script, ":'start'", "'"+start.UTC().Format(layout)+"'")
	script = strings.ReplaceAll(script, ":'end'", "'"+end.UTC().Format(layout)+"'")
	return script
}

type rollupBucket struct {
	TotalChecks         int
	SuccessChecks       int
	ErrorChecks         int
	LatencySuccessSumMS float64
	LatencySuccessCount int
	LatestStatus        string
	LatestCheckAt       time.Time
}

func queryHourlyBucket(ctx context.Context, t *testing.T, dbClient *shareddb.Client, monitorID uuid.UUID, bucketHour time.Time) (rollupBucket, bool) {
	t.Helper()
	var b rollupBucket
	err := dbClient.QueryRowContext(ctx, `
		SELECT total_checks, success_checks, error_checks, latency_success_sum_ms,
		       latency_success_count, latest_status, latest_check_at
		FROM monitor_hourly_rollups
		WHERE monitor_id = $1 AND bucket_hour = $2
	`, monitorID, bucketHour).Scan(&b.TotalChecks, &b.SuccessChecks, &b.ErrorChecks, &b.LatencySuccessSumMS, &b.LatencySuccessCount, &b.LatestStatus, &b.LatestCheckAt)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return rollupBucket{}, false
		}
		t.Fatalf("query hourly bucket %v: %v", bucketHour, err)
	}
	return b, true
}

func queryDailyBucket(ctx context.Context, t *testing.T, dbClient *shareddb.Client, monitorID uuid.UUID, bucketDay time.Time) (rollupBucket, bool) {
	t.Helper()
	var b rollupBucket
	err := dbClient.QueryRowContext(ctx, `
		SELECT total_checks, success_checks, error_checks, latency_success_sum_ms,
		       latency_success_count, latest_status, latest_check_at
		FROM monitor_daily_rollups
		WHERE monitor_id = $1 AND bucket_day = $2::date
	`, monitorID, bucketDay).Scan(&b.TotalChecks, &b.SuccessChecks, &b.ErrorChecks, &b.LatencySuccessSumMS, &b.LatencySuccessCount, &b.LatestStatus, &b.LatestCheckAt)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return rollupBucket{}, false
		}
		t.Fatalf("query daily bucket %v: %v", bucketDay, err)
	}
	return b, true
}

func assertBucket(t *testing.T, label string, got rollupBucket, want rollupBucket) {
	t.Helper()
	if got.TotalChecks != want.TotalChecks ||
		got.SuccessChecks != want.SuccessChecks ||
		got.ErrorChecks != want.ErrorChecks ||
		got.LatencySuccessSumMS != want.LatencySuccessSumMS ||
		got.LatencySuccessCount != want.LatencySuccessCount ||
		got.LatestStatus != want.LatestStatus ||
		!got.LatestCheckAt.UTC().Equal(want.LatestCheckAt) {
		t.Fatalf("%s = %+v, want %+v", label, got, want)
	}
}

func setRollupCursor(ctx context.Context, t *testing.T, dbClient *shareddb.Client, cursor time.Time) {
	t.Helper()
	if _, err := dbClient.ExecContext(ctx, `
		INSERT INTO rollup_job_state (job_name, last_created_at, last_check_result_id, last_run_at, updated_at)
		VALUES ($1, $2, $3, NOW(), NOW())
		ON CONFLICT (job_name) DO UPDATE SET
			last_created_at = EXCLUDED.last_created_at,
			last_check_result_id = EXCLUDED.last_check_result_id,
			last_run_at = NOW(),
			updated_at = NOW()
	`, rollupJobName, cursor, uuid.New()); err != nil {
		t.Fatalf("set rollup cursor: %v", err)
	}
}

func TestBackfillScript_RebuildsHourlyAndDailyRollups(t *testing.T) {
	testcontainers.SkipIfProviderIsNotHealthy(t)

	ctx := context.Background()
	dbClient, cleanup := setupRollupTestDB(ctx, t)
	defer cleanup()

	tenantID := insertTenant(ctx, t, dbClient)
	monitorID := insertMonitor(ctx, t, dbClient, tenantID, "rollup-backfill-script")

	// Three hours of raw data on Jan 10 (21:00, 22:00, 23:00 UTC), with the
	// incremental cursor sitting on Jan 11 at 00:30. Hour cap = Jan 11 00:00,
	// day cap = Jan 11 00:00, so hours 21-23 and day Jan 10 are backfillable.
	hour0 := time.Date(2026, time.January, 10, 21, 0, 0, 0, time.UTC)
	hour1 := hour0.Add(time.Hour)
	hour2 := hour0.Add(2 * time.Hour)
	cursorHour := time.Date(2026, time.January, 11, 0, 0, 0, 0, time.UTC)
	cursor := cursorHour.Add(30 * time.Minute)

	src := string(sharedmodels.ResultSourceMonitor)
	// hour0: success(120) + failure + error, plus a platform row that must be
	// excluded from the aggregates.
	insertCheckResult(ctx, t, dbClient, tenantID, monitorID, hour0.Add(5*time.Minute), string(sharedmodels.ResultStatusSuccess), src, 120)
	insertCheckResult(ctx, t, dbClient, tenantID, monitorID, hour0.Add(15*time.Minute), string(sharedmodels.ResultStatusFailure), src, 0)
	insertCheckResult(ctx, t, dbClient, tenantID, monitorID, hour0.Add(25*time.Minute), string(sharedmodels.ResultStatusError), src, 0)
	insertCheckResult(ctx, t, dbClient, tenantID, monitorID, hour0.Add(35*time.Minute), string(sharedmodels.ResultStatusFailure), string(sharedmodels.ResultSourcePlatform), 0)
	// hour1: single success(80).
	insertCheckResult(ctx, t, dbClient, tenantID, monitorID, hour1.Add(10*time.Minute), string(sharedmodels.ResultStatusSuccess), src, 80)
	// hour2: success(60) then error (latest_status must be 'error').
	insertCheckResult(ctx, t, dbClient, tenantID, monitorID, hour2.Add(5*time.Minute), string(sharedmodels.ResultStatusSuccess), src, 60)
	insertCheckResult(ctx, t, dbClient, tenantID, monitorID, hour2.Add(20*time.Minute), string(sharedmodels.ResultStatusError), src, 0)
	// A raw row inside the cursor hour: owned by the incremental writer, must
	// NOT be aggregated by the backfill even though it precedes the cursor.
	insertCheckResult(ctx, t, dbClient, tenantID, monitorID, cursorHour.Add(10*time.Minute), string(sharedmodels.ResultStatusSuccess), src, 40)

	setRollupCursor(ctx, t, dbClient, cursor)

	// Stale pre-existing rollup row for hour0: REPLACE semantics must
	// overwrite it wholesale, not add to it.
	if _, err := dbClient.ExecContext(ctx, `
		INSERT INTO monitor_hourly_rollups (
			tenant_id, monitor_id, bucket_hour, total_checks, success_checks, error_checks,
			latency_success_sum_ms, latency_success_count, latest_status, latest_check_at, created_at, updated_at
		) VALUES ($1, $2, $3, 999, 999, 999, 999, 999, 'success', $3, NOW(), NOW())
	`, tenantID, monitorID, hour0); err != nil {
		t.Fatalf("seed stale hourly rollup: %v", err)
	}
	// Rows the incremental writer owns (cursor hour / cursor day): the
	// backfill must leave them untouched.
	if _, err := dbClient.ExecContext(ctx, `
		INSERT INTO monitor_hourly_rollups (
			tenant_id, monitor_id, bucket_hour, total_checks, success_checks, error_checks,
			latency_success_sum_ms, latency_success_count, latest_status, latest_check_at, created_at, updated_at
		) VALUES ($1, $2, $3, 7, 7, 0, 280, 7, 'success', $4, NOW(), NOW())
	`, tenantID, monitorID, cursorHour, cursorHour.Add(10*time.Minute)); err != nil {
		t.Fatalf("seed cursor-hour rollup: %v", err)
	}
	if _, err := dbClient.ExecContext(ctx, `
		INSERT INTO monitor_daily_rollups (
			tenant_id, monitor_id, bucket_day, total_checks, success_checks, error_checks,
			latency_success_sum_ms, latency_success_count, latest_status, latest_check_at, created_at, updated_at
		) VALUES ($1, $2, $3::date, 7, 7, 0, 280, 7, 'success', $4, NOW(), NOW())
	`, tenantID, monitorID, cursorHour, cursorHour.Add(10*time.Minute)); err != nil {
		t.Fatalf("seed cursor-day rollup: %v", err)
	}

	// Range deliberately extends past the cursor: the cap must clamp it.
	script := loadBackfillScript(t, hour0, cursorHour.Add(24*time.Hour))
	if _, err := dbClient.ExecContext(ctx, script); err != nil {
		t.Fatalf("execute backfill script: %v", err)
	}

	wantHour0 := rollupBucket{TotalChecks: 3, SuccessChecks: 1, ErrorChecks: 1, LatencySuccessSumMS: 120, LatencySuccessCount: 1, LatestStatus: string(sharedmodels.ResultStatusError), LatestCheckAt: hour0.Add(25 * time.Minute)}
	wantHour1 := rollupBucket{TotalChecks: 1, SuccessChecks: 1, ErrorChecks: 0, LatencySuccessSumMS: 80, LatencySuccessCount: 1, LatestStatus: string(sharedmodels.ResultStatusSuccess), LatestCheckAt: hour1.Add(10 * time.Minute)}
	wantHour2 := rollupBucket{TotalChecks: 2, SuccessChecks: 1, ErrorChecks: 1, LatencySuccessSumMS: 60, LatencySuccessCount: 1, LatestStatus: string(sharedmodels.ResultStatusError), LatestCheckAt: hour2.Add(20 * time.Minute)}
	wantDay := rollupBucket{TotalChecks: 6, SuccessChecks: 3, ErrorChecks: 2, LatencySuccessSumMS: 260, LatencySuccessCount: 3, LatestStatus: string(sharedmodels.ResultStatusError), LatestCheckAt: hour2.Add(20 * time.Minute)}
	wantCursorBucket := rollupBucket{TotalChecks: 7, SuccessChecks: 7, ErrorChecks: 0, LatencySuccessSumMS: 280, LatencySuccessCount: 7, LatestStatus: string(sharedmodels.ResultStatusSuccess), LatestCheckAt: cursorHour.Add(10 * time.Minute)}

	checkAll := func(pass string) {
		t.Helper()
		got, ok := queryHourlyBucket(ctx, t, dbClient, monitorID, hour0)
		if !ok {
			t.Fatalf("%s: hour0 bucket missing", pass)
		}
		assertBucket(t, pass+" hour0", got, wantHour0)
		got, ok = queryHourlyBucket(ctx, t, dbClient, monitorID, hour1)
		if !ok {
			t.Fatalf("%s: hour1 bucket missing", pass)
		}
		assertBucket(t, pass+" hour1", got, wantHour1)
		got, ok = queryHourlyBucket(ctx, t, dbClient, monitorID, hour2)
		if !ok {
			t.Fatalf("%s: hour2 bucket missing", pass)
		}
		assertBucket(t, pass+" hour2", got, wantHour2)

		got, ok = queryDailyBucket(ctx, t, dbClient, monitorID, hour0)
		if !ok {
			t.Fatalf("%s: daily bucket missing", pass)
		}
		assertBucket(t, pass+" day Jan10", got, wantDay)

		// Buckets at/after the cursor hour (and the cursor day) are untouched.
		got, ok = queryHourlyBucket(ctx, t, dbClient, monitorID, cursorHour)
		if !ok {
			t.Fatalf("%s: cursor-hour bucket missing", pass)
		}
		assertBucket(t, pass+" cursor hour (untouched)", got, wantCursorBucket)
		got, ok = queryDailyBucket(ctx, t, dbClient, monitorID, cursorHour)
		if !ok {
			t.Fatalf("%s: cursor-day bucket missing", pass)
		}
		assertBucket(t, pass+" cursor day (untouched)", got, wantCursorBucket)

		var hourlyCount, dailyCount int
		if err := dbClient.QueryRowContext(ctx, `SELECT COUNT(*) FROM monitor_hourly_rollups WHERE monitor_id = $1`, monitorID).Scan(&hourlyCount); err != nil {
			t.Fatalf("%s: count hourly rollups: %v", pass, err)
		}
		if hourlyCount != 4 {
			t.Fatalf("%s: hourly rollup count = %d, want 4 (hours 21-23 + cursor hour)", pass, hourlyCount)
		}
		if err := dbClient.QueryRowContext(ctx, `SELECT COUNT(*) FROM monitor_daily_rollups WHERE monitor_id = $1`, monitorID).Scan(&dailyCount); err != nil {
			t.Fatalf("%s: count daily rollups: %v", pass, err)
		}
		if dailyCount != 2 {
			t.Fatalf("%s: daily rollup count = %d, want 2 (Jan 10 + cursor day)", pass, dailyCount)
		}
	}

	checkAll("first run")

	// Idempotency: a second run over the same range must leave every bucket
	// (including the protected cursor buckets) byte-identical.
	if _, err := dbClient.ExecContext(ctx, script); err != nil {
		t.Fatalf("re-execute backfill script: %v", err)
	}
	checkAll("second run")
}

func TestBackfillScript_MatchesIncrementalJobOutput(t *testing.T) {
	testcontainers.SkipIfProviderIsNotHealthy(t)

	ctx := context.Background()
	dbClient, cleanup := setupRollupTestDB(ctx, t)
	defer cleanup()

	s := newTestScheduler(dbClient)
	tenantID := insertTenant(ctx, t, dbClient)
	monitorID := insertMonitor(ctx, t, dbClient, tenantID, "rollup-backfill-equivalence")

	base := time.Date(2026, time.February, 3, 6, 0, 0, 0, time.UTC)
	src := string(sharedmodels.ResultSourceMonitor)
	statuses := []struct {
		offset  time.Duration
		status  string
		latency int
	}{
		{5 * time.Minute, string(sharedmodels.ResultStatusSuccess), 50},
		{20 * time.Minute, string(sharedmodels.ResultStatusFailure), 0},
		{65 * time.Minute, string(sharedmodels.ResultStatusError), 0},
		{80 * time.Minute, string(sharedmodels.ResultStatusSuccess), 90},
		{130 * time.Minute, string(sharedmodels.ResultStatusSuccess), 30},
	}
	for _, c := range statuses {
		insertCheckResult(ctx, t, dbClient, tenantID, monitorID, base.Add(c.offset), c.status, src, c.latency)
	}
	// Sentinel row on the next day so the incremental cursor lands past every
	// full hour/day above (its own buckets are excluded from the comparison).
	sentinelAt := base.Add(20 * time.Hour)
	insertCheckResult(ctx, t, dbClient, tenantID, monitorID, sentinelAt, string(sharedmodels.ResultStatusSuccess), src, 10)

	// Reference: let the incremental job process everything.
	if _, _, err := s.runRollupMaintenance(); err != nil {
		t.Fatalf("runRollupMaintenance() error = %v", err)
	}

	snapshot := func(table, bucketCol string) map[string]rollupBucket {
		rows, err := dbClient.QueryContext(ctx, `
			SELECT `+bucketCol+`::text, total_checks, success_checks, error_checks,
			       latency_success_sum_ms, latency_success_count, latest_status, latest_check_at
			FROM `+table+` WHERE monitor_id = $1 ORDER BY `+bucketCol, monitorID)
		if err != nil {
			t.Fatalf("snapshot %s: %v", table, err)
		}
		defer rows.Close()
		out := map[string]rollupBucket{}
		for rows.Next() {
			var key string
			var b rollupBucket
			if err := rows.Scan(&key, &b.TotalChecks, &b.SuccessChecks, &b.ErrorChecks, &b.LatencySuccessSumMS, &b.LatencySuccessCount, &b.LatestStatus, &b.LatestCheckAt); err != nil {
				t.Fatalf("scan %s snapshot: %v", table, err)
			}
			b.LatestCheckAt = b.LatestCheckAt.UTC()
			out[key] = b
		}
		if err := rows.Err(); err != nil {
			t.Fatalf("iterate %s snapshot: %v", table, err)
		}
		return out
	}

	wantHourly := snapshot("monitor_hourly_rollups", "bucket_hour")
	wantDaily := snapshot("monitor_daily_rollups", "bucket_day")

	// Wipe the rollups produced before the cursor and rebuild them with the
	// script; the cursor itself stays where the incremental job left it.
	if _, err := dbClient.ExecContext(ctx, `
		DELETE FROM monitor_hourly_rollups WHERE monitor_id = $1 AND bucket_hour < date_trunc('hour', $2::timestamptz)
	`, monitorID, sentinelAt); err != nil {
		t.Fatalf("delete hourly rollups: %v", err)
	}
	if _, err := dbClient.ExecContext(ctx, `
		DELETE FROM monitor_daily_rollups WHERE monitor_id = $1 AND bucket_day < date_trunc('day', $2::timestamptz)::date
	`, monitorID, sentinelAt); err != nil {
		t.Fatalf("delete daily rollups: %v", err)
	}

	script := loadBackfillScript(t, base.Add(-time.Hour), sentinelAt.Add(24*time.Hour))
	if _, err := dbClient.ExecContext(ctx, script); err != nil {
		t.Fatalf("execute backfill script: %v", err)
	}

	gotHourly := snapshot("monitor_hourly_rollups", "bucket_hour")
	gotDaily := snapshot("monitor_daily_rollups", "bucket_day")

	if len(gotHourly) != len(wantHourly) {
		t.Fatalf("hourly bucket count = %d, want %d", len(gotHourly), len(wantHourly))
	}
	for key, want := range wantHourly {
		got, ok := gotHourly[key]
		if !ok {
			t.Fatalf("hourly bucket %s missing after backfill", key)
		}
		assertBucket(t, "hourly "+key, got, rollupBucket{
			TotalChecks: want.TotalChecks, SuccessChecks: want.SuccessChecks, ErrorChecks: want.ErrorChecks,
			LatencySuccessSumMS: want.LatencySuccessSumMS, LatencySuccessCount: want.LatencySuccessCount,
			LatestStatus: want.LatestStatus, LatestCheckAt: want.LatestCheckAt,
		})
	}
	if len(gotDaily) != len(wantDaily) {
		t.Fatalf("daily bucket count = %d, want %d", len(gotDaily), len(wantDaily))
	}
	for key, want := range wantDaily {
		got, ok := gotDaily[key]
		if !ok {
			t.Fatalf("daily bucket %s missing after backfill", key)
		}
		assertBucket(t, "daily "+key, got, rollupBucket{
			TotalChecks: want.TotalChecks, SuccessChecks: want.SuccessChecks, ErrorChecks: want.ErrorChecks,
			LatencySuccessSumMS: want.LatencySuccessSumMS, LatencySuccessCount: want.LatencySuccessCount,
			LatestStatus: want.LatestStatus, LatestCheckAt: want.LatestCheckAt,
		})
	}
}

func TestBackfillScript_NoCursorIsNoOp(t *testing.T) {
	testcontainers.SkipIfProviderIsNotHealthy(t)

	ctx := context.Background()
	dbClient, cleanup := setupRollupTestDB(ctx, t)
	defer cleanup()

	tenantID := insertTenant(ctx, t, dbClient)
	monitorID := insertMonitor(ctx, t, dbClient, tenantID, "rollup-backfill-nocursor")

	at := time.Date(2026, time.March, 1, 12, 5, 0, 0, time.UTC)
	insertCheckResult(ctx, t, dbClient, tenantID, monitorID, at, string(sharedmodels.ResultStatusSuccess), string(sharedmodels.ResultSourceMonitor), 25)

	// rollup_job_state is empty: without a cursor the script cannot know
	// which buckets the incremental writer owns and must do nothing.
	script := loadBackfillScript(t, at.Add(-24*time.Hour), at.Add(24*time.Hour))
	if _, err := dbClient.ExecContext(ctx, script); err != nil {
		t.Fatalf("execute backfill script: %v", err)
	}

	var hourly, daily int
	if err := dbClient.QueryRowContext(ctx, `SELECT COUNT(*) FROM monitor_hourly_rollups WHERE monitor_id = $1`, monitorID).Scan(&hourly); err != nil {
		t.Fatalf("count hourly rollups: %v", err)
	}
	if err := dbClient.QueryRowContext(ctx, `SELECT COUNT(*) FROM monitor_daily_rollups WHERE monitor_id = $1`, monitorID).Scan(&daily); err != nil {
		t.Fatalf("count daily rollups: %v", err)
	}
	if hourly != 0 || daily != 0 {
		t.Fatalf("rollup rows = %d hourly / %d daily, want 0/0 (no cursor => no-op)", hourly, daily)
	}
}
