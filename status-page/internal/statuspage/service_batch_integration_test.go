package statuspage

import (
	"context"
	"database/sql"
	"fmt"
	"sync/atomic"
	"testing"
	"time"

	testcontainers "github.com/testcontainers/testcontainers-go"

	sharedanalytics "github.com/yassinebenameur/probara/shared/analytics"
	shareddb "github.com/yassinebenameur/probara/shared/db"
	"github.com/yassinebenameur/probara/shared/testutil"
)

// countingDB wraps a db.DB and counts the read queries issued through it, so tests can assert
// that status page rendering does not scale its query count with the number of monitors.
type countingDB struct {
	inner shareddb.DB
	count atomic.Int64
}

func (c *countingDB) QueryContext(ctx context.Context, query string, args ...interface{}) (*sql.Rows, error) {
	c.count.Add(1)
	return c.inner.QueryContext(ctx, query, args...)
}

func (c *countingDB) QueryRowContext(ctx context.Context, query string, args ...interface{}) *sql.Row {
	c.count.Add(1)
	return c.inner.QueryRowContext(ctx, query, args...)
}

func (c *countingDB) ExecContext(ctx context.Context, query string, args ...interface{}) (sql.Result, error) {
	return c.inner.ExecContext(ctx, query, args...)
}

func (c *countingDB) BeginTx(ctx context.Context, opts *sql.TxOptions) (*sql.Tx, error) {
	return c.inner.BeginTx(ctx, opts)
}

func (c *countingDB) HealthCheck(ctx context.Context) error { return c.inner.HealthCheck(ctx) }
func (c *countingDB) Close() error                          { return c.inner.Close() }

func (c *countingDB) queries() int { return int(c.count.Load()) }

// TestService_GetStatusPageBySlug_DoesNotScaleQueriesWithMonitorCount is the regression guard
// for the N+1 query storm. Rendering a status page with many monitors must issue only a
// bounded, monitor-count-independent number of queries. Under the previous per-monitor
// population this page would issue well over 300 queries.
func TestService_GetStatusPageBySlug_DoesNotScaleQueriesWithMonitorCount(t *testing.T) {
	testcontainers.SkipIfProviderIsNotHealthy(t)

	ctx := context.Background()
	dbClient, cleanup := testutil.SetupPostgresDB(ctx, t)
	defer cleanup()

	const monitorCount = 25
	const queryBudget = 50

	now := time.Now().UTC()
	tenantID := testutil.InsertTenant(ctx, t, dbClient, "status-batch")
	statusPageID := testutil.InsertStatusPage(ctx, t, dbClient, tenantID, "batch-status", "Batch Status")

	for i := 0; i < monitorCount; i++ {
		monitorID := testutil.InsertHTTPMonitor(ctx, t, dbClient, tenantID, fmt.Sprintf("monitor-%02d", i))
		testutil.AddMonitorToStatusPage(ctx, t, dbClient, statusPageID, monitorID, i)
		// A spread of recent check results so 24h/1h windows and the hourly strip have data.
		testutil.InsertCheckResult(ctx, t, dbClient, tenantID, monitorID, now.Add(-90*time.Minute), "success", "monitor", testutil.IntPtr(100))
		testutil.InsertCheckResult(ctx, t, dbClient, tenantID, monitorID, now.Add(-30*time.Minute), "success", "monitor", testutil.IntPtr(110))
		testutil.InsertCheckResult(ctx, t, dbClient, tenantID, monitorID, now.Add(-5*time.Minute), "failure", "monitor", nil)
		// Rollups so long-range analytics has data to aggregate.
		day := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC)
		testutil.InsertDailyRollup(ctx, t, dbClient, tenantID, monitorID, day.AddDate(0, 0, -1), 20, 19, 2000, 19, "success", day.Add(-2*time.Hour))
		testutil.InsertDailyRollup(ctx, t, dbClient, tenantID, monitorID, day, 20, 18, 2100, 18, "failure", now)
	}

	counter := &countingDB{inner: dbClient}
	svc := NewService(counter, sharedanalytics.NewRepository(counter))

	page, err := svc.GetStatusPageBySlug(ctx, "batch-status")
	if err != nil {
		t.Fatalf("GetStatusPageBySlug() error = %v", err)
	}
	if len(page.Monitors) != monitorCount {
		t.Fatalf("monitor count = %d, want %d", len(page.Monitors), monitorCount)
	}

	queries := counter.queries()
	if queries > queryBudget {
		t.Fatalf("GetStatusPageBySlug issued %d queries for %d monitors; want <= %d (N+1 regression)", queries, monitorCount, queryBudget)
	}
	t.Logf("GetStatusPageBySlug issued %d queries for %d monitors", queries, monitorCount)
}

// TestService_GetStatusPageBySlug_BatchedShortRangeMatchesPerMonitorHelpers verifies the
// batched population produces the same status, uptime, hourly strip and history as the
// existing single-monitor helpers it replaces.
func TestService_GetStatusPageBySlug_BatchedShortRangeMatchesPerMonitorHelpers(t *testing.T) {
	testcontainers.SkipIfProviderIsNotHealthy(t)

	ctx := context.Background()
	dbClient, cleanup := testutil.SetupPostgresDB(ctx, t)
	defer cleanup()

	svc := NewService(dbClient, sharedanalytics.NewRepository(dbClient))
	now := time.Now().UTC()

	tenantID := testutil.InsertTenant(ctx, t, dbClient, "status-batch-parity")
	statusPageID := testutil.InsertStatusPage(ctx, t, dbClient, tenantID, "batch-parity", "Batch Parity")
	monitorA := testutil.InsertHTTPMonitor(ctx, t, dbClient, tenantID, "alpha")
	monitorB := testutil.InsertHTTPMonitor(ctx, t, dbClient, tenantID, "beta")
	testutil.AddMonitorToStatusPage(ctx, t, dbClient, statusPageID, monitorA, 0)
	testutil.AddMonitorToStatusPage(ctx, t, dbClient, statusPageID, monitorB, 1)

	testutil.InsertCheckResult(ctx, t, dbClient, tenantID, monitorA, now.Add(-3*time.Hour), "success", "monitor", testutil.IntPtr(140))
	testutil.InsertCheckResult(ctx, t, dbClient, tenantID, monitorA, now.Add(-40*time.Minute), "failure", "monitor", nil)
	testutil.InsertCheckResult(ctx, t, dbClient, tenantID, monitorA, now.Add(-4*time.Minute), "success", "monitor", testutil.IntPtr(155))
	testutil.InsertCheckResult(ctx, t, dbClient, tenantID, monitorB, now.Add(-10*time.Minute), "success", "monitor", testutil.IntPtr(80))

	page, err := svc.GetStatusPageBySlug(ctx, "batch-parity")
	if err != nil {
		t.Fatalf("GetStatusPageBySlug() error = %v", err)
	}

	monitor := findMonitorStatus(t, page.Monitors, monitorA)

	// Status / latest check parity.
	wantStatus, err := svc.GetMonitorCurrentStatus(ctx, monitorA, tenantID)
	if err != nil {
		t.Fatalf("GetMonitorCurrentStatus() error = %v", err)
	}
	if monitor.Status != wantStatus.Status {
		t.Fatalf("status = %q, want %q", monitor.Status, wantStatus.Status)
	}
	if (monitor.LastLatency == nil) != (wantStatus.LastLatency == nil) {
		t.Fatalf("last latency nullability mismatch: got %v want %v", monitor.LastLatency, wantStatus.LastLatency)
	}
	if monitor.LastLatency != nil && wantStatus.LastLatency != nil && *monitor.LastLatency != *wantStatus.LastLatency {
		t.Fatalf("last latency = %d, want %d", *monitor.LastLatency, *wantStatus.LastLatency)
	}

	// 24h uptime parity.
	wantUptime, err := svc.CalculateUptime24h(ctx, monitorA, tenantID)
	if err != nil {
		t.Fatalf("CalculateUptime24h() error = %v", err)
	}
	assertFloatPtrEqual(t, "uptime24h", monitor.Uptime24h, wantUptime)

	// Hourly strip parity.
	wantHourly, err := svc.GetMonitorHourlyUptime(ctx, monitorA, tenantID)
	if err != nil {
		t.Fatalf("GetMonitorHourlyUptime() error = %v", err)
	}
	assertHourlySeriesEqual(t, monitor.UptimeHistory24h, wantHourly)

	// Recent history parity.
	wantHistory, err := svc.GetMonitorHistory(ctx, monitorA, tenantID, 50, nil)
	if err != nil {
		t.Fatalf("GetMonitorHistory() error = %v", err)
	}
	if len(monitor.History) != len(wantHistory) {
		t.Fatalf("history length = %d, want %d", len(monitor.History), len(wantHistory))
	}
	for i := range wantHistory {
		if monitor.History[i].Status != wantHistory[i].Status || !monitor.History[i].Timestamp.Equal(wantHistory[i].Timestamp) {
			t.Fatalf("history[%d] = %+v, want %+v", i, monitor.History[i], wantHistory[i])
		}
	}
}

func assertFloatPtrEqual(t *testing.T, label string, got, want *float64) {
	t.Helper()
	if (got == nil) != (want == nil) {
		t.Fatalf("%s nullability mismatch: got %v want %v", label, got, want)
	}
	if got != nil && want != nil && *got != *want {
		t.Fatalf("%s = %v, want %v", label, *got, *want)
	}
}
