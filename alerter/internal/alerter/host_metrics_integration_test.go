package alerter

// Integration tests for metric-rule alerting over the generic metric store:
// open/resolve/idempotence with canonical series-key identity, per-mount
// fan-out, the freshness bound (a stale series stops alerting and resolves),
// <= rules, for_duration sustained-breach semantics, and the 000085
// config/alert migration transform.

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/yassinebenameur/probara/shared/config"
	shareddb "github.com/yassinebenameur/probara/shared/db"
	"github.com/yassinebenameur/probara/shared/logger"
	"github.com/yassinebenameur/probara/shared/metricstore"
	"github.com/yassinebenameur/probara/shared/testutil"
)

func newHostMetricAlerter(dbClient *shareddb.Client) *Alerter {
	a := &Alerter{
		config: &config.AlerterConfig{},
		logger: logger.New("alerter-test", "error"),
		db:     dbClient,
	}
	a.sendFunc = func(context.Context, alertChannel, string, policyBinding, *alertRecord, *groupDetail, time.Time) error {
		return nil
	}
	return a
}

// insertAgentMonitor inserts an enabled agent monitor with the given config JSON.
func insertAgentMonitor(ctx context.Context, t *testing.T, dbClient *shareddb.Client, tenantID uuid.UUID, name, configJSON string) uuid.UUID {
	t.Helper()
	id := uuid.New()
	if _, err := dbClient.ExecContext(ctx, `
		INSERT INTO monitors (
			id, tenant_id, name, type, config, interval_seconds, timeout_seconds,
			alert_policy_id, enabled, tags, agent_id, push_token, next_run_at, created_at, updated_at
		) VALUES (
			$1, $2, $3, 'agent', $4::jsonb, 60, 60,
			NULL, TRUE, ARRAY[]::text[], $5, NULL, NULL, NOW(), NOW()
		)
	`, id, tenantID, name, configJSON, id.String()); err != nil {
		t.Fatalf("insert agent monitor: %v", err)
	}
	return id
}

// seedFreshSeries registers a series and writes one sample at `ts`, leaving
// last_seen_at = NOW() (fresh).
func seedFreshSeries(ctx context.Context, t *testing.T, dbClient *shareddb.Client, tenantID, monitorID uuid.UUID, name string, attrs map[string]string, ts time.Time, value float64) int64 {
	t.Helper()
	id, err := metricstore.ResolveSeries(ctx, dbClient.DB, metricstore.SeriesKey{
		TenantID: tenantID, MonitorID: monitorID, MetricName: name, Unit: "1",
		MetricType: "gauge", Temporality: "unspecified", Attributes: attrs,
	})
	if err != nil {
		t.Fatalf("resolve series %s: %v", name, err)
	}
	if err := metricstore.EnsurePartitions(ctx, dbClient.DB, ts.Add(-time.Hour), ts.Add(time.Hour)); err != nil {
		t.Fatalf("ensure partitions: %v", err)
	}
	if _, err := metricstore.InsertSamples(ctx, dbClient.DB, []metricstore.Sample{{SeriesID: id, TS: ts, Value: value}}); err != nil {
		t.Fatalf("insert sample: %v", err)
	}
	return id
}

// insertAgentMetricsResult inserts a successful check result carrying the
// given metrics_data JSON (still used by the TLS-expiry evaluator tests,
// which read metrics_data rather than the metric store).
func insertAgentMetricsResult(ctx context.Context, t *testing.T, dbClient *shareddb.Client, tenantID, monitorID uuid.UUID, createdAt time.Time, metricsJSON string) {
	t.Helper()
	if _, err := dbClient.ExecContext(ctx, `
		INSERT INTO check_results (
			id, monitor_id, tenant_id, job_id, status, latency_ms, created_at, started_at, completed_at,
			result_source, metrics_data
		) VALUES ($1, $2, $3, $4, 'success', 5, $5, $5, $5, 'monitor', $6::jsonb)
	`, uuid.New(), monitorID, tenantID, uuid.New(), createdAt.UTC(), metricsJSON); err != nil {
		t.Fatalf("insert agent metrics result: %v", err)
	}
}

func countHostMetricAlerts(ctx context.Context, t *testing.T, dbClient *shareddb.Client, monitorID uuid.UUID, metric, status string) int {
	t.Helper()
	var count int
	if err := dbClient.QueryRowContext(ctx, `
		SELECT COUNT(*) FROM alerts
		WHERE monitor_id = $1 AND kind = 'host_metric' AND metric_name = $2 AND status = $3
	`, monitorID, metric, status).Scan(&count); err != nil {
		t.Fatalf("count host metric alerts: %v", err)
	}
	return count
}

const cpuSeriesKey = "system.cpu.utilization{state=used}"

func TestEvaluateHostMetricRulesOpensAndResolves(t *testing.T) {
	ctx := context.Background()
	dbClient, cleanup := testutil.SetupPostgresDB(ctx, t)
	defer cleanup()

	tenantID := testutil.InsertTenant(ctx, t, dbClient, "alerter")
	monitorID := insertAgentMonitor(ctx, t, dbClient, tenantID, "web-1",
		`{"agent_id":"web-1","expected_interval_seconds":60,"metric_rules":[
			{"metric_name":"system.cpu.utilization","attribute_filters":{"state":"used"},"operator":">=","threshold":0.8}]}`)

	now := time.Now().UTC()
	seriesID := seedFreshSeries(ctx, t, dbClient, tenantID, monitorID,
		"system.cpu.utilization", map[string]string{"state": "used"}, now.Add(-10*time.Second), 0.95)

	alerter := newHostMetricAlerter(dbClient)
	if err := alerter.evaluateHostMetricThresholds(ctx); err != nil {
		t.Fatalf("evaluate(open) error = %v", err)
	}
	if got := countHostMetricAlerts(ctx, t, dbClient, monitorID, cpuSeriesKey, "active"); got != 1 {
		t.Fatalf("active cpu alert count = %d, want 1", got)
	}

	// Native (ratio) values on the alert row.
	var value, threshold float64
	if err := dbClient.QueryRowContext(ctx, `
		SELECT metric_value, threshold_value FROM alerts
		WHERE monitor_id = $1 AND kind = 'host_metric' AND metric_name = $2 AND status = 'active'
	`, monitorID, cpuSeriesKey).Scan(&value, &threshold); err != nil {
		t.Fatalf("read host metric alert: %v", err)
	}
	if value != 0.95 || threshold != 0.8 {
		t.Errorf("value/threshold = %v/%v, want 0.95/0.8 (native ratio)", value, threshold)
	}

	// Idempotent while still breaching.
	if err := alerter.evaluateHostMetricThresholds(ctx); err != nil {
		t.Fatalf("evaluate(idempotent) error = %v", err)
	}
	if got := countHostMetricAlerts(ctx, t, dbClient, monitorID, cpuSeriesKey, "active"); got != 1 {
		t.Fatalf("active cpu alert count after second pass = %d, want 1", got)
	}

	// Recovery: a newer sample below the threshold resolves.
	if _, err := metricstore.InsertSamples(ctx, dbClient.DB, []metricstore.Sample{{SeriesID: seriesID, TS: now.Add(-time.Second), Value: 0.10}}); err != nil {
		t.Fatalf("insert recovery sample: %v", err)
	}
	if err := alerter.evaluateHostMetricThresholds(ctx); err != nil {
		t.Fatalf("evaluate(recover) error = %v", err)
	}
	if got := countHostMetricAlerts(ctx, t, dbClient, monitorID, cpuSeriesKey, "active"); got != 0 {
		t.Fatalf("active cpu alert count after recovery = %d, want 0", got)
	}
	if got := countHostMetricAlerts(ctx, t, dbClient, monitorID, cpuSeriesKey, "resolved"); got != 1 {
		t.Fatalf("resolved cpu alert count after recovery = %d, want 1", got)
	}
}

func TestEvaluateHostMetricRulesPerMountFanOut(t *testing.T) {
	ctx := context.Background()
	dbClient, cleanup := testutil.SetupPostgresDB(ctx, t)
	defer cleanup()

	tenantID := testutil.InsertTenant(ctx, t, dbClient, "alerter")
	// One unfiltered filesystem rule fans out per mountpoint.
	monitorID := insertAgentMonitor(ctx, t, dbClient, tenantID, "db-1",
		`{"agent_id":"db-1","expected_interval_seconds":60,"metric_rules":[
			{"metric_name":"system.filesystem.utilization","operator":">=","threshold":0.9}]}`)

	now := time.Now().UTC()
	seedFreshSeries(ctx, t, dbClient, tenantID, monitorID,
		"system.filesystem.utilization", map[string]string{"mountpoint": "/", "state": "used"}, now.Add(-10*time.Second), 0.50)
	seedFreshSeries(ctx, t, dbClient, tenantID, monitorID,
		"system.filesystem.utilization", map[string]string{"mountpoint": "/data", "state": "used"}, now.Add(-10*time.Second), 0.95)

	alerter := newHostMetricAlerter(dbClient)
	if err := alerter.evaluateHostMetricThresholds(ctx); err != nil {
		t.Fatalf("evaluate error = %v", err)
	}

	dataKey := "system.filesystem.utilization{mountpoint=/data,state=used}"
	rootKey := "system.filesystem.utilization{mountpoint=/,state=used}"
	if got := countHostMetricAlerts(ctx, t, dbClient, monitorID, dataKey, "active"); got != 1 {
		t.Fatalf("active /data alert count = %d, want 1", got)
	}
	if got := countHostMetricAlerts(ctx, t, dbClient, monitorID, rootKey, "active"); got != 0 {
		t.Fatalf("active / alert count = %d, want 0", got)
	}

	// The second mount breaching later coexists under its own key.
	if _, err := dbClient.ExecContext(ctx, `
		UPDATE metric_samples SET value = 0.99
		FROM metric_series s
		WHERE metric_samples.series_id = s.id AND s.monitor_id = $1 AND s.attributes->>'mountpoint' = '/'
	`, monitorID); err != nil {
		t.Fatalf("raise root mount usage: %v", err)
	}
	if err := alerter.evaluateHostMetricThresholds(ctx); err != nil {
		t.Fatalf("evaluate(second mount) error = %v", err)
	}
	if got := countHostMetricAlerts(ctx, t, dbClient, monitorID, rootKey, "active"); got != 1 {
		t.Fatalf("active / alert count = %d, want 1", got)
	}
	if got := countHostMetricAlerts(ctx, t, dbClient, monitorID, dataKey, "active"); got != 1 {
		t.Fatalf("active /data alert count = %d, want 1 (coexists)", got)
	}
}

func TestEvaluateHostMetricRulesFreshnessBound(t *testing.T) {
	ctx := context.Background()
	dbClient, cleanup := testutil.SetupPostgresDB(ctx, t)
	defer cleanup()

	tenantID := testutil.InsertTenant(ctx, t, dbClient, "alerter")
	monitorID := insertAgentMonitor(ctx, t, dbClient, tenantID, "stale-1",
		`{"agent_id":"stale-1","expected_interval_seconds":60,"metric_rules":[
			{"metric_name":"system.cpu.utilization","attribute_filters":{"state":"used"},"operator":">=","threshold":0.8}]}`)

	now := time.Now().UTC()
	seedFreshSeries(ctx, t, dbClient, tenantID, monitorID,
		"system.cpu.utilization", map[string]string{"state": "used"}, now.Add(-10*time.Second), 0.95)

	alerter := newHostMetricAlerter(dbClient)
	if err := alerter.evaluateHostMetricThresholds(ctx); err != nil {
		t.Fatalf("evaluate(open) error = %v", err)
	}
	if got := countHostMetricAlerts(ctx, t, dbClient, monitorID, cpuSeriesKey, "active"); got != 1 {
		t.Fatalf("active alert count = %d, want 1", got)
	}

	// The agent goes silent: last_seen_at ages past the freshness horizon
	// (interval 60s ⇒ horizon 180s). The dead host's last breaching reading
	// must stop alerting — the availability watchdog owns the outage.
	if _, err := dbClient.ExecContext(ctx, `
		UPDATE metric_series SET last_seen_at = NOW() - INTERVAL '10 minutes' WHERE monitor_id = $1
	`, monitorID); err != nil {
		t.Fatalf("age series: %v", err)
	}
	if err := alerter.evaluateHostMetricThresholds(ctx); err != nil {
		t.Fatalf("evaluate(stale) error = %v", err)
	}
	if got := countHostMetricAlerts(ctx, t, dbClient, monitorID, cpuSeriesKey, "active"); got != 0 {
		t.Fatalf("active alert count with stale series = %d, want 0 (resolved)", got)
	}
	if got := countHostMetricAlerts(ctx, t, dbClient, monitorID, cpuSeriesKey, "resolved"); got != 1 {
		t.Fatalf("resolved alert count with stale series = %d, want 1", got)
	}
}

func TestEvaluateHostMetricRulesLessThanOperator(t *testing.T) {
	ctx := context.Background()
	dbClient, cleanup := testutil.SetupPostgresDB(ctx, t)
	defer cleanup()

	tenantID := testutil.InsertTenant(ctx, t, dbClient, "alerter")
	monitorID := insertAgentMonitor(ctx, t, dbClient, tenantID, "low-1",
		`{"agent_id":"low-1","expected_interval_seconds":60,"metric_rules":[
			{"metric_name":"myapp.workers.active","operator":"<=","threshold":2}]}`)

	now := time.Now().UTC()
	seedFreshSeries(ctx, t, dbClient, tenantID, monitorID, "myapp.workers.active", nil, now.Add(-5*time.Second), 1)

	alerter := newHostMetricAlerter(dbClient)
	if err := alerter.evaluateHostMetricThresholds(ctx); err != nil {
		t.Fatalf("evaluate error = %v", err)
	}
	if got := countHostMetricAlerts(ctx, t, dbClient, monitorID, "myapp.workers.active", "active"); got != 1 {
		t.Fatalf("active low-watermark alert count = %d, want 1", got)
	}
}

func TestEvaluateHostMetricRulesForDuration(t *testing.T) {
	ctx := context.Background()
	dbClient, cleanup := testutil.SetupPostgresDB(ctx, t)
	defer cleanup()

	tenantID := testutil.InsertTenant(ctx, t, dbClient, "alerter")
	now := time.Now().UTC()
	attrs := map[string]string{"state": "used"}
	alerter := newHostMetricAlerter(dbClient)

	// Case 1: breaching continuously since before the window ⇒ opens.
	sustained := insertAgentMonitor(ctx, t, dbClient, tenantID, "sustained", `{"agent_id":"sustained","expected_interval_seconds":60,"metric_rules":[
		{"metric_name":"system.cpu.utilization","attribute_filters":{"state":"used"},"operator":">=","threshold":0.8,"for_duration_seconds":300}]}`)
	sID := seedFreshSeries(ctx, t, dbClient, tenantID, sustained, "system.cpu.utilization", attrs, now.Add(-7*time.Minute), 0.9)
	for _, off := range []time.Duration{-6 * time.Minute, -4 * time.Minute, -2 * time.Minute, -30 * time.Second} {
		if _, err := metricstore.InsertSamples(ctx, dbClient.DB, []metricstore.Sample{{SeriesID: sID, TS: now.Add(off), Value: 0.9}}); err != nil {
			t.Fatalf("insert sustained sample: %v", err)
		}
	}

	// Case 2: breach with an in-window recovery sample ⇒ does not open.
	flapping := insertAgentMonitor(ctx, t, dbClient, tenantID, "flapping", `{"agent_id":"flapping","expected_interval_seconds":60,"metric_rules":[
		{"metric_name":"system.cpu.utilization","attribute_filters":{"state":"used"},"operator":">=","threshold":0.8,"for_duration_seconds":300}]}`)
	fID := seedFreshSeries(ctx, t, dbClient, tenantID, flapping, "system.cpu.utilization", attrs, now.Add(-7*time.Minute), 0.9)
	for _, s := range []struct {
		off time.Duration
		v   float64
	}{{-6 * time.Minute, 0.9}, {-3 * time.Minute, 0.5}, {-time.Minute, 0.9}, {-10 * time.Second, 0.9}} {
		if _, err := metricstore.InsertSamples(ctx, dbClient.DB, []metricstore.Sample{{SeriesID: fID, TS: now.Add(s.off), Value: s.v}}); err != nil {
			t.Fatalf("insert flapping sample: %v", err)
		}
	}

	// Case 3: breach younger than the window (no breaching anchor) ⇒ does not open.
	young := insertAgentMonitor(ctx, t, dbClient, tenantID, "young", `{"agent_id":"young","expected_interval_seconds":60,"metric_rules":[
		{"metric_name":"system.cpu.utilization","attribute_filters":{"state":"used"},"operator":">=","threshold":0.8,"for_duration_seconds":300}]}`)
	yID := seedFreshSeries(ctx, t, dbClient, tenantID, young, "system.cpu.utilization", attrs, now.Add(-7*time.Minute), 0.2)
	for _, off := range []time.Duration{-2 * time.Minute, -time.Minute, -10 * time.Second} {
		if _, err := metricstore.InsertSamples(ctx, dbClient.DB, []metricstore.Sample{{SeriesID: yID, TS: now.Add(off), Value: 0.9}}); err != nil {
			t.Fatalf("insert young sample: %v", err)
		}
	}

	if err := alerter.evaluateHostMetricThresholds(ctx); err != nil {
		t.Fatalf("evaluate error = %v", err)
	}
	if got := countHostMetricAlerts(ctx, t, dbClient, sustained, cpuSeriesKey, "active"); got != 1 {
		t.Fatalf("sustained breach alert count = %d, want 1", got)
	}
	if got := countHostMetricAlerts(ctx, t, dbClient, flapping, cpuSeriesKey, "active"); got != 0 {
		t.Fatalf("flapping breach alert count = %d, want 0", got)
	}
	if got := countHostMetricAlerts(ctx, t, dbClient, young, cpuSeriesKey, "active"); got != 0 {
		t.Fatalf("young breach alert count = %d, want 0", got)
	}
}

// TestAgentMetricRulesMigrationTransform replays the 000085 up migration
// against legacy-shaped data (the harness has already run it on an empty
// database) and asserts the config and open-alert rewrites.
func TestAgentMetricRulesMigrationTransform(t *testing.T) {
	ctx := context.Background()
	dbClient, cleanup := testutil.SetupPostgresDB(ctx, t)
	defer cleanup()

	tenantID := testutil.InsertTenant(ctx, t, dbClient, "alerter")
	monitorID := insertAgentMonitor(ctx, t, dbClient, tenantID, "legacy-1",
		`{"agent_id":"legacy-1","expected_interval_seconds":60,"metric_thresholds":{"cpu_percent":80,"memory_percent":0,"disk_percent":90}}`)

	// An open legacy cpu alert (percent values).
	if _, err := dbClient.ExecContext(ctx, `
		INSERT INTO alerts (id, tenant_id, monitor_id, kind, status, triggered_at, failure_count,
			metric_name, metric_value, threshold_value, created_at, updated_at)
		VALUES ($1, $2, $3, 'host_metric', 'active', NOW(), 0, 'cpu', 95, 80, NOW(), NOW())
	`, uuid.New(), tenantID, monitorID); err != nil {
		t.Fatalf("insert legacy alert: %v", err)
	}

	migrationSQL, err := os.ReadFile(filepath.Join("..", "..", "..", "shared", "db", "migrations", "000085_agent_metric_rules.up.sql"))
	if err != nil {
		t.Fatalf("read migration: %v", err)
	}
	if _, err := dbClient.ExecContext(ctx, string(migrationSQL)); err != nil {
		t.Fatalf("replay migration: %v", err)
	}

	var configJSON string
	if err := dbClient.QueryRowContext(ctx, `SELECT config::text FROM monitors WHERE id = $1`, monitorID).Scan(&configJSON); err != nil {
		t.Fatalf("read config: %v", err)
	}
	var rulesCount int
	if err := dbClient.QueryRowContext(ctx, `
		SELECT jsonb_array_length(config->'metric_rules') FROM monitors WHERE id = $1
	`, monitorID).Scan(&rulesCount); err != nil {
		t.Fatalf("count rules: %v", err)
	}
	// cpu 80% and disk 90% convert; memory 0 is dropped.
	if rulesCount != 2 {
		t.Fatalf("metric_rules count = %d, want 2 (config: %s)", rulesCount, configJSON)
	}
	var hasLegacy bool
	if err := dbClient.QueryRowContext(ctx, `
		SELECT config ? 'metric_thresholds' FROM monitors WHERE id = $1
	`, monitorID).Scan(&hasLegacy); err != nil {
		t.Fatalf("check legacy block: %v", err)
	}
	if hasLegacy {
		t.Fatal("metric_thresholds must be removed")
	}
	var cpuThreshold float64
	if err := dbClient.QueryRowContext(ctx, `
		SELECT (r->>'threshold')::float8 FROM monitors,
			jsonb_array_elements(config->'metric_rules') r
		WHERE id = $1 AND r->>'metric_name' = 'system.cpu.utilization'
	`, monitorID).Scan(&cpuThreshold); err != nil {
		t.Fatalf("read cpu rule: %v", err)
	}
	if cpuThreshold != 0.8 {
		t.Fatalf("cpu threshold = %v, want 0.8 (ratio)", cpuThreshold)
	}
	// The disk rule fans out per WRITABLE mountpoint: read-only mounts
	// (squashfs, snapshot volumes) sit at 100% forever and must not page.
	var diskMode string
	if err := dbClient.QueryRowContext(ctx, `
		SELECT r#>>'{attribute_filters,mode}' FROM monitors,
			jsonb_array_elements(config->'metric_rules') r
		WHERE id = $1 AND r->>'metric_name' = 'system.filesystem.utilization'
	`, monitorID).Scan(&diskMode); err != nil {
		t.Fatalf("read disk rule: %v", err)
	}
	if diskMode != "rw" {
		t.Fatalf("disk rule mode filter = %q, want rw", diskMode)
	}

	// Open alert renamed to the canonical key with ratio values.
	var name string
	var value, threshold float64
	if err := dbClient.QueryRowContext(ctx, `
		SELECT metric_name, metric_value, threshold_value FROM alerts WHERE monitor_id = $1 AND status = 'active'
	`, monitorID).Scan(&name, &value, &threshold); err != nil {
		t.Fatalf("read migrated alert: %v", err)
	}
	if name != cpuSeriesKey || value != 0.95 || threshold != 0.8 {
		t.Fatalf("migrated alert = %s %v/%v, want %s 0.95/0.8", name, value, threshold, cpuSeriesKey)
	}
}
