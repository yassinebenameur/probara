//go:build !unit

package otlp

// Integration tests for OTLP ingest: series/sample persistence, the
// server-clock heartbeat (S-F1/S-O3, docs/state-semantics.md), retry
// idempotence via the deterministic job id, unsupported-type and future-
// timestamp rejection into partial success, and the cardinality guardrail.

import (
	"context"
	"crypto/sha256"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	testcontainers "github.com/testcontainers/testcontainers-go"
	"go.opentelemetry.io/collector/pdata/pcommon"
	"go.opentelemetry.io/collector/pdata/pmetric"

	shareddb "github.com/yassinebenameur/probara/shared/db"
	"github.com/yassinebenameur/probara/shared/metricstore"
	"github.com/yassinebenameur/probara/shared/testutil"
)

// buildHostMetrics assembles a small hostmetrics-shaped export: one gauge
// (cpu utilization), one cumulative monotonic sum (network io), and one
// histogram (unsupported → rejected).
func buildHostMetrics(ts time.Time, cpuUsed float64, netRx float64) pmetric.Metrics {
	md := pmetric.NewMetrics()
	rm := md.ResourceMetrics().AppendEmpty()
	sm := rm.ScopeMetrics().AppendEmpty()

	cpu := sm.Metrics().AppendEmpty()
	cpu.SetName("system.cpu.utilization")
	cpu.SetUnit("1")
	dp := cpu.SetEmptyGauge().DataPoints().AppendEmpty()
	dp.SetTimestamp(pcommon.NewTimestampFromTime(ts))
	dp.SetDoubleValue(cpuUsed)
	dp.Attributes().PutStr("state", "used")

	net := sm.Metrics().AppendEmpty()
	net.SetName("system.network.io")
	net.SetUnit("By")
	sum := net.SetEmptySum()
	sum.SetIsMonotonic(true)
	sum.SetAggregationTemporality(pmetric.AggregationTemporalityCumulative)
	ndp := sum.DataPoints().AppendEmpty()
	ndp.SetTimestamp(pcommon.NewTimestampFromTime(ts))
	ndp.SetDoubleValue(netRx)
	ndp.Attributes().PutStr("device", "eth0")
	ndp.Attributes().PutStr("direction", "receive")

	hist := sm.Metrics().AppendEmpty()
	hist.SetName("http.server.duration")
	hdp := hist.SetEmptyHistogram().DataPoints().AppendEmpty()
	hdp.SetTimestamp(pcommon.NewTimestampFromTime(ts))
	hdp.SetCount(1)

	return md
}

func digestOf(seed string) [32]byte {
	return sha256.Sum256([]byte(seed))
}

func countRows(ctx context.Context, t *testing.T, db *shareddb.Client, query string, args ...interface{}) int {
	t.Helper()
	var n int
	if err := db.QueryRowContext(ctx, query, args...).Scan(&n); err != nil {
		t.Fatalf("count query %q: %v", query, err)
	}
	return n
}

func TestIngestPersistsSeriesSamplesAndHeartbeat(t *testing.T) {
	testcontainers.SkipIfProviderIsNotHealthy(t)

	ctx := context.Background()
	dbClient, cleanup := testutil.SetupPostgresDB(ctx, t)
	defer cleanup()

	tenantID := testutil.InsertTenant(ctx, t, dbClient, "tenant-otlp")
	agentID := uuid.New().String()
	monitorID := testutil.InsertAgentMonitor(ctx, t, dbClient, tenantID, "otlp-host", agentID, 60)

	svc := NewService(dbClient.DB, nil, 2000, 60)
	sampleTS := time.Now().Add(-30 * time.Second).UTC().Truncate(time.Millisecond)
	md := buildHostMetrics(sampleTS, 0.42, 123456)

	before := time.Now()
	res, err := svc.Ingest(ctx, tenantID, agentID, digestOf("req-1"), md)
	if err != nil {
		t.Fatalf("Ingest: %v", err)
	}
	if res.AcceptedPoints != 2 {
		t.Fatalf("accepted = %d, want 2", res.AcceptedPoints)
	}
	if res.RejectedPoints != 1 || res.RejectMessage == "" {
		t.Fatalf("rejected = %d (%q), want 1 with message", res.RejectedPoints, res.RejectMessage)
	}

	// Series registry: gauge + monotonic cumulative sum, canonical attrs.
	var metricType, temporality string
	var isMonotonic bool
	if err := dbClient.QueryRowContext(ctx, `
		SELECT metric_type, temporality, is_monotonic FROM metric_series
		WHERE monitor_id = $1 AND metric_name = 'system.network.io'
	`, monitorID).Scan(&metricType, &temporality, &isMonotonic); err != nil {
		t.Fatalf("query network series: %v", err)
	}
	if metricType != "sum" || temporality != "cumulative" || !isMonotonic {
		t.Fatalf("network series = %s/%s/monotonic=%v", metricType, temporality, isMonotonic)
	}
	if n := countRows(ctx, t, dbClient, `SELECT COUNT(*) FROM metric_series WHERE monitor_id = $1`, monitorID); n != 2 {
		t.Fatalf("series count = %d, want 2 (histogram must not register)", n)
	}
	if n := countRows(ctx, t, dbClient, `
		SELECT COUNT(*) FROM metric_samples ms JOIN metric_series s ON s.id = ms.series_id
		WHERE s.monitor_id = $1`, monitorID); n != 2 {
		t.Fatalf("sample count = %d, want 2", n)
	}
	if n := countRows(ctx, t, dbClient, `SELECT COUNT(*) FROM metric_rollup_dirty WHERE monitor_id = $1`, monitorID); n == 0 {
		t.Fatal("expected a dirty rollup mark")
	}

	// Heartbeat: one success check_result whose started_at is the SERVER
	// receipt clock (S-O3), not the 30s-old sample clock.
	var status string
	var startedAt time.Time
	if err := dbClient.QueryRowContext(ctx, `
		SELECT status, started_at FROM check_results WHERE monitor_id = $1
	`, monitorID).Scan(&status, &startedAt); err != nil {
		t.Fatalf("query heartbeat: %v", err)
	}
	if status != "success" {
		t.Fatalf("heartbeat status = %s", status)
	}
	if startedAt.Before(before.Add(-2*time.Second)) || time.Since(startedAt) > time.Minute {
		t.Fatalf("heartbeat started_at %v is not the server receipt time", startedAt)
	}
	var state string
	if err := dbClient.QueryRowContext(ctx, `SELECT current_state FROM monitors WHERE id = $1`, monitorID).Scan(&state); err != nil {
		t.Fatalf("query state: %v", err)
	}
	if state != "up" {
		t.Fatalf("monitor state = %s, want up", state)
	}

	// The heartbeat carries no metrics_data: the samples live only in the
	// metric store (the legacy AgentMetrics snapshot is retired).
	if n := countRows(ctx, t, dbClient, `
		SELECT COUNT(*) FROM check_results WHERE monitor_id = $1 AND metrics_data IS NOT NULL
	`, monitorID); n != 0 {
		t.Fatalf("heartbeat rows with metrics_data = %d, want 0", n)
	}

	// Same digest again = otlphttp retry after a dropped response: samples
	// dedupe on (series_id, ts), the heartbeat dedupes on the deterministic
	// job id — no second check_results row, no state double-advance.
	if _, err := svc.Ingest(ctx, tenantID, agentID, digestOf("req-1"), buildHostMetrics(sampleTS, 0.42, 123456)); err != nil {
		t.Fatalf("retry Ingest: %v", err)
	}
	if n := countRows(ctx, t, dbClient, `SELECT COUNT(*) FROM check_results WHERE monitor_id = $1`, monitorID); n != 1 {
		t.Fatalf("heartbeat rows after retry = %d, want 1", n)
	}
	if n := countRows(ctx, t, dbClient, `
		SELECT COUNT(*) FROM metric_samples ms JOIN metric_series s ON s.id = ms.series_id
		WHERE s.monitor_id = $1`, monitorID); n != 2 {
		t.Fatalf("sample rows after retry = %d, want 2", n)
	}

	// A later report (new digest) heartbeats again.
	md2 := buildHostMetrics(sampleTS.Add(time.Minute), 0.55, 234567)
	if _, err := svc.Ingest(ctx, tenantID, agentID, digestOf("req-2"), md2); err != nil {
		t.Fatalf("second Ingest: %v", err)
	}
	if n := countRows(ctx, t, dbClient, `SELECT COUNT(*) FROM check_results WHERE monitor_id = $1`, monitorID); n != 2 {
		t.Fatalf("heartbeat rows after second report = %d, want 2", n)
	}
}

// A transaction that created series and then rolled back must not leave
// their ids in the series cache: metric_samples has no FK to metric_series,
// so a cached ghost id turns every later export into orphaned samples that
// still return 200. The missing-partition retry is the in-tree path where
// this happens on every first export into a new day's partition.
func TestIngestRolledBackSeriesAreNotCached(t *testing.T) {
	testcontainers.SkipIfProviderIsNotHealthy(t)

	ctx := context.Background()
	dbClient, cleanup := testutil.SetupPostgresDB(ctx, t)
	defer cleanup()

	tenantID := testutil.InsertTenant(ctx, t, dbClient, "tenant-otlp-rollback")
	agentID := uuid.New().String()
	monitorID := testutil.InsertAgentMonitor(ctx, t, dbClient, tenantID, "rollback-host", agentID, 60)

	// Ten days back is outside the partitions the migration seeds; drop it
	// anyway so the test does not depend on that window.
	sampleTS := time.Now().Add(-10 * 24 * time.Hour).UTC().Truncate(time.Millisecond)
	day := sampleTS.Truncate(24 * time.Hour)
	if _, err := dbClient.ExecContext(ctx, `DROP TABLE IF EXISTS metric_samples_`+day.Format("20060102")); err != nil {
		t.Fatalf("drop partition: %v", err)
	}

	countOrphans := func() int {
		return countRows(ctx, t, dbClient, `
			SELECT COUNT(*) FROM metric_samples ms
			WHERE NOT EXISTS (SELECT 1 FROM metric_series s WHERE s.id = ms.series_id)`)
	}

	// One attempt, no retry: the series upsert succeeds inside the
	// transaction, the sample insert fails, everything rolls back.
	svc := NewService(dbClient.DB, nil, 2000, 60)
	_, _, _, _, err := svc.writeBatchOnce(ctx, tenantID, monitorID, cpuPoints(sampleTS))
	if !metricstore.IsMissingPartitionErr(err) {
		t.Fatalf("first attempt err = %v, want missing partition", err)
	}
	if n := countRows(ctx, t, dbClient, `SELECT COUNT(*) FROM metric_series WHERE monitor_id = $1`, monitorID); n != 0 {
		t.Fatalf("series rows after rollback = %d, want 0", n)
	}
	svc.cache.mu.Lock()
	cached := len(svc.cache.items)
	svc.cache.mu.Unlock()
	if cached != 0 {
		t.Fatalf("cache entries after rollback = %d, want 0", cached)
	}

	// The full export path on the same service: writeBatch's first attempt
	// fails the same way, creates the partition, and retries. Before the
	// fix the retry read the rolled-back ids from the cache and wrote
	// orphaned samples without ever registering the series.
	res, err := svc.Ingest(ctx, tenantID, agentID, digestOf("rollback-1"), buildHostMetrics(sampleTS, 0.42, 123456))
	if err != nil {
		t.Fatalf("Ingest: %v", err)
	}
	if res.AcceptedPoints != 2 {
		t.Fatalf("accepted = %d, want 2", res.AcceptedPoints)
	}
	if n := countRows(ctx, t, dbClient, `
		SELECT COUNT(*) FROM metric_samples ms JOIN metric_series s ON s.id = ms.series_id
		WHERE s.monitor_id = $1`, monitorID); n != 2 {
		t.Fatalf("joinable samples = %d, want 2", n)
	}
	if n := countOrphans(); n != 0 {
		t.Fatalf("orphaned samples = %d, want 0", n)
	}

	// The cache now holds exactly the committed rows, so a later export
	// (cache hits only) keeps landing on real series.
	rows, err := dbClient.QueryContext(ctx, `SELECT id, attr_hash FROM metric_series WHERE monitor_id = $1`, monitorID)
	if err != nil {
		t.Fatalf("query series: %v", err)
	}
	defer rows.Close()
	for rows.Next() {
		var id int64
		var hash []byte
		if err := rows.Scan(&id, &hash); err != nil {
			t.Fatalf("scan series: %v", err)
		}
		if got, ok := svc.cache.get(monitorID, hash); !ok || got != id {
			t.Fatalf("cache for series %d = (%d, %v)", id, got, ok)
		}
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("read series: %v", err)
	}
	if _, err := svc.Ingest(ctx, tenantID, agentID, digestOf("rollback-2"), buildHostMetrics(sampleTS.Add(time.Minute), 0.5, 234567)); err != nil {
		t.Fatalf("second Ingest: %v", err)
	}
	if n := countRows(ctx, t, dbClient, `
		SELECT COUNT(*) FROM metric_samples ms JOIN metric_series s ON s.id = ms.series_id
		WHERE s.monitor_id = $1`, monitorID); n != 4 {
		t.Fatalf("joinable samples after second export = %d, want 4", n)
	}
	if n := countOrphans(); n != 0 {
		t.Fatalf("orphaned samples after second export = %d, want 0", n)
	}
}

func TestIngestRejectsUnknownAndDisabledAgents(t *testing.T) {
	testcontainers.SkipIfProviderIsNotHealthy(t)

	ctx := context.Background()
	dbClient, cleanup := testutil.SetupPostgresDB(ctx, t)
	defer cleanup()

	tenantID := testutil.InsertTenant(ctx, t, dbClient, "tenant-otlp-err")
	svc := NewService(dbClient.DB, nil, 2000, 60)
	md := buildHostMetrics(time.Now(), 0.5, 1)

	_, err := svc.Ingest(ctx, tenantID, "no-such-agent", digestOf("x"), md)
	if !errors.Is(err, ErrUnknownAgent) {
		t.Fatalf("unknown agent err = %v, want ErrUnknownAgent", err)
	}

	agentID := uuid.New().String()
	monitorID := testutil.InsertAgentMonitor(ctx, t, dbClient, tenantID, "disabled-host", agentID, 60)
	if _, err := dbClient.ExecContext(ctx, `UPDATE monitors SET enabled = FALSE WHERE id = $1`, monitorID); err != nil {
		t.Fatalf("disable monitor: %v", err)
	}
	_, err = svc.Ingest(ctx, tenantID, agentID, digestOf("y"), buildHostMetrics(time.Now(), 0.5, 1))
	if !errors.Is(err, ErrMonitorDisabled) {
		t.Fatalf("disabled agent err = %v, want ErrMonitorDisabled", err)
	}

	// Tenant scoping: the same agent id under another tenant is unknown.
	otherTenant := testutil.InsertTenant(ctx, t, dbClient, "tenant-otlp-other")
	_, err = svc.Ingest(ctx, otherTenant, agentID, digestOf("z"), buildHostMetrics(time.Now(), 0.5, 1))
	if !errors.Is(err, ErrUnknownAgent) {
		t.Fatalf("cross-tenant err = %v, want ErrUnknownAgent", err)
	}
}

func TestIngestGuardrails(t *testing.T) {
	testcontainers.SkipIfProviderIsNotHealthy(t)

	ctx := context.Background()
	dbClient, cleanup := testutil.SetupPostgresDB(ctx, t)
	defer cleanup()

	tenantID := testutil.InsertTenant(ctx, t, dbClient, "tenant-otlp-guard")
	agentID := uuid.New().String()
	monitorID := testutil.InsertAgentMonitor(ctx, t, dbClient, tenantID, "guard-host", agentID, 60)

	// Cardinality cap of 1: the first series lands, the second is rejected
	// into partial success — and the request still heartbeats.
	svc := NewService(dbClient.DB, nil, 1, 60)
	res, err := svc.Ingest(ctx, tenantID, agentID, digestOf("cap"), buildHostMetrics(time.Now(), 0.5, 1))
	if err != nil {
		t.Fatalf("Ingest: %v", err)
	}
	if res.AcceptedPoints != 1 || res.RejectedPoints < 2 {
		t.Fatalf("cap result = %+v, want 1 accepted, >=2 rejected", res)
	}
	if n := countRows(ctx, t, dbClient, `SELECT COUNT(*) FROM metric_series WHERE monitor_id = $1`, monitorID); n != 1 {
		t.Fatalf("series after cap = %d, want 1", n)
	}
	if n := countRows(ctx, t, dbClient, `SELECT COUNT(*) FROM check_results WHERE monitor_id = $1`, monitorID); n != 1 {
		t.Fatal("capped request must still heartbeat")
	}

	// Future timestamps beyond the slack are rejected.
	svc2 := NewService(dbClient.DB, nil, 2000, 60)
	future := buildHostMetrics(time.Now().Add(time.Hour), 0.5, 1)
	res, err = svc2.Ingest(ctx, tenantID, agentID, digestOf("future"), future)
	if err != nil {
		t.Fatalf("future Ingest: %v", err)
	}
	if res.AcceptedPoints != 0 || res.RejectedPoints != 3 {
		t.Fatalf("future result = %+v, want 0 accepted / 3 rejected", res)
	}

	// Rate limiting: exhaust the burst, then expect ErrRateLimited.
	svc3 := NewService(dbClient.DB, nil, 2000, 1) // 1 request/min, burst 1
	if _, err := svc3.Ingest(ctx, tenantID, agentID, digestOf("rl-1"), buildHostMetrics(time.Now(), 0.5, 1)); err != nil {
		t.Fatalf("first rate-limited Ingest: %v", err)
	}
	_, err = svc3.Ingest(ctx, tenantID, agentID, digestOf("rl-2"), buildHostMetrics(time.Now(), 0.5, 2))
	if !errors.Is(err, ErrRateLimited) {
		t.Fatalf("rate limit err = %v, want ErrRateLimited", err)
	}
}
