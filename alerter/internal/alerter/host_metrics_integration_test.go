package alerter

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/yassinebenameur/probara/shared/config"
	shareddb "github.com/yassinebenameur/probara/shared/db"
	"github.com/yassinebenameur/probara/shared/logger"
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

// insertAgentMetricsResult inserts a successful agent check result carrying the
// given metrics_data JSON.
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

func cpuMetrics(cpuPercent float64) string {
	// Always include a healthy memory/disk denominator so only CPU can breach.
	return fmt.Sprintf(`{"cpu_percent":%f,"memory_used":1,"memory_total":100,"disk_used":1,"disk_total":100,"swap_used":0,"swap_total":0}`, cpuPercent)
}

func TestEvaluateHostMetricThresholdsOpensAndResolves(t *testing.T) {
	ctx := context.Background()
	dbClient, cleanup := testutil.SetupPostgresDB(ctx, t)
	defer cleanup()

	tenantID := testutil.InsertTenant(ctx, t, dbClient, "alerter")
	monitorID := insertAgentMonitor(ctx, t, dbClient, tenantID, "web-1",
		`{"agent_id":"web-1","expected_interval_seconds":60,"metric_thresholds":{"cpu_percent":80}}`)

	now := time.Now().UTC()

	// Latest report breaches the CPU threshold (95% >= 80%).
	insertAgentMetricsResult(ctx, t, dbClient, tenantID, monitorID, now.Add(-10*time.Second), cpuMetrics(95))

	alerter := newHostMetricAlerter(dbClient)
	if err := alerter.evaluateHostMetricThresholds(ctx); err != nil {
		t.Fatalf("evaluateHostMetricThresholds(open) error = %v", err)
	}
	if got := countHostMetricAlerts(ctx, t, dbClient, monitorID, "cpu", "active"); got != 1 {
		t.Fatalf("active cpu alert count = %d, want 1", got)
	}

	// Recorded value/threshold should reflect the breach.
	var value, threshold float64
	if err := dbClient.QueryRowContext(ctx, `
		SELECT metric_value, threshold_value FROM alerts
		WHERE monitor_id = $1 AND kind = 'host_metric' AND metric_name = 'cpu' AND status = 'active'
	`, monitorID).Scan(&value, &threshold); err != nil {
		t.Fatalf("read host metric alert: %v", err)
	}
	if value != 95 {
		t.Errorf("metric_value = %v, want 95", value)
	}
	if threshold != 80 {
		t.Errorf("threshold_value = %v, want 80", threshold)
	}

	// Idempotent: a second pass while still breaching must not duplicate.
	if err := alerter.evaluateHostMetricThresholds(ctx); err != nil {
		t.Fatalf("evaluateHostMetricThresholds(idempotent) error = %v", err)
	}
	if got := countHostMetricAlerts(ctx, t, dbClient, monitorID, "cpu", "active"); got != 1 {
		t.Fatalf("active cpu alert count after second pass = %d, want 1", got)
	}

	// Recovery: a newer report below the threshold resolves the alert.
	insertAgentMetricsResult(ctx, t, dbClient, tenantID, monitorID, now.Add(-1*time.Second), cpuMetrics(10))
	if err := alerter.evaluateHostMetricThresholds(ctx); err != nil {
		t.Fatalf("evaluateHostMetricThresholds(recover) error = %v", err)
	}
	if got := countHostMetricAlerts(ctx, t, dbClient, monitorID, "cpu", "active"); got != 0 {
		t.Fatalf("active cpu alert count after recovery = %d, want 0", got)
	}
	if got := countHostMetricAlerts(ctx, t, dbClient, monitorID, "cpu", "resolved"); got != 1 {
		t.Fatalf("resolved cpu alert count after recovery = %d, want 1", got)
	}
}

func TestEvaluateHostMetricThresholdsPerMetricCoexist(t *testing.T) {
	ctx := context.Background()
	dbClient, cleanup := testutil.SetupPostgresDB(ctx, t)
	defer cleanup()

	tenantID := testutil.InsertTenant(ctx, t, dbClient, "alerter")
	monitorID := insertAgentMonitor(ctx, t, dbClient, tenantID, "db-1",
		`{"agent_id":"db-1","expected_interval_seconds":60,"metric_thresholds":{"cpu_percent":80,"disk_percent":90}}`)

	now := time.Now().UTC()
	// CPU 95% (>=80) and disk 95% (95/100 >= 90) both breach simultaneously.
	insertAgentMetricsResult(ctx, t, dbClient, tenantID, monitorID, now.Add(-10*time.Second),
		`{"cpu_percent":95,"memory_used":1,"memory_total":100,"disk_used":95,"disk_total":100,"swap_used":0,"swap_total":0}`)

	alerter := newHostMetricAlerter(dbClient)
	if err := alerter.evaluateHostMetricThresholds(ctx); err != nil {
		t.Fatalf("evaluateHostMetricThresholds error = %v", err)
	}

	if got := countHostMetricAlerts(ctx, t, dbClient, monitorID, "cpu", "active"); got != 1 {
		t.Fatalf("active cpu alert count = %d, want 1", got)
	}
	if got := countHostMetricAlerts(ctx, t, dbClient, monitorID, "disk", "active"); got != 1 {
		t.Fatalf("active disk alert count = %d, want 1", got)
	}
}
