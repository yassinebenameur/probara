package alerter

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/yassinebenameur/probara/shared/config"
	shareddb "github.com/yassinebenameur/probara/shared/db"
	"github.com/yassinebenameur/probara/shared/logger"
	"github.com/yassinebenameur/probara/shared/testutil"
)

// newAnomalyAlerter is like newIntegrationAlerter but with latency anomaly
// detection enabled in config.
func newAnomalyAlerter(dbClient *shareddb.Client) *Alerter {
	a := &Alerter{
		config: &config.AlerterConfig{LatencyAnomalyEnabled: true},
		logger: logger.New("alerter-test", "error"),
		db:     dbClient,
	}
	a.sendFunc = func(context.Context, alertChannel, string, policyBinding, *alertRecord, *groupDetail, time.Time) error {
		return nil
	}
	return a
}

func enableTenantAnomaly(ctx context.Context, t *testing.T, dbClient *shareddb.Client, tenantID uuid.UUID) {
	t.Helper()
	if _, err := dbClient.ExecContext(ctx,
		`UPDATE tenants SET latency_anomaly_enabled = TRUE WHERE id = $1`, tenantID); err != nil {
		t.Fatalf("enable tenant anomaly: %v", err)
	}
}

// seedFlatBaseline inserts `hours` hourly rollups of a steady avg latency.
func seedFlatBaseline(ctx context.Context, t *testing.T, dbClient *shareddb.Client, tenantID, monitorID uuid.UUID, now time.Time, hours int, avgMs float64) {
	t.Helper()
	const checksPerHour = 60
	for i := 1; i <= hours; i++ {
		bucket := now.Add(-time.Duration(i) * time.Hour)
		testutil.InsertHourlyRollup(ctx, t, dbClient, tenantID, monitorID, bucket,
			checksPerHour, checksPerHour, avgMs*checksPerHour, checksPerHour, "success", bucket)
	}
}

func countLatencyAlertsByStatus(ctx context.Context, t *testing.T, dbClient *shareddb.Client, monitorID uuid.UUID, status string) int {
	t.Helper()
	var count int
	if err := dbClient.QueryRowContext(ctx, `
		SELECT COUNT(*) FROM alerts
		WHERE monitor_id = $1 AND kind = 'latency_anomaly' AND status = $2
	`, monitorID, status).Scan(&count); err != nil {
		t.Fatalf("count latency alerts: %v", err)
	}
	return count
}

func TestEvaluateLatencyAnomaliesOpensAndResolves(t *testing.T) {
	ctx := context.Background()
	dbClient, cleanup := testutil.SetupPostgresDB(ctx, t)
	defer cleanup()

	tenantID := testutil.InsertTenant(ctx, t, dbClient, "alerter")
	monitorID := testutil.InsertHTTPMonitor(ctx, t, dbClient, tenantID, "API")
	enableTenantAnomaly(ctx, t, dbClient, tenantID)

	now := time.Now().UTC()
	// Steady ~100ms baseline over the past 12 hours.
	seedFlatBaseline(ctx, t, dbClient, tenantID, monitorID, now, 12, 100)

	// Recent window: four successful checks at ~500ms — a clear degradation.
	for _, age := range []time.Duration{10, 30, 60, 90} {
		testutil.InsertCheckResult(ctx, t, dbClient, tenantID, monitorID,
			now.Add(-age*time.Second), "success", "monitor", testutil.IntPtr(500))
	}

	alerter := newAnomalyAlerter(dbClient)
	if err := alerter.evaluateLatencyAnomalies(ctx); err != nil {
		t.Fatalf("evaluateLatencyAnomalies(open) error = %v", err)
	}

	if got := countLatencyAlertsByStatus(ctx, t, dbClient, monitorID, "active"); got != 1 {
		t.Fatalf("active latency alert count = %d, want 1", got)
	}

	// Verify the recorded baseline/observed are sensible.
	var baseline, observed float64
	if err := dbClient.QueryRowContext(ctx, `
		SELECT baseline_latency_ms, observed_latency_ms FROM alerts
		WHERE monitor_id = $1 AND kind = 'latency_anomaly' AND status = 'active'
	`, monitorID).Scan(&baseline, &observed); err != nil {
		t.Fatalf("read latency alert metrics: %v", err)
	}
	if baseline != 100 {
		t.Errorf("baseline_latency_ms = %v, want 100", baseline)
	}
	if observed != 500 {
		t.Errorf("observed_latency_ms = %v, want 500", observed)
	}

	// Idempotent: a second pass while still degraded must not open a duplicate.
	if err := alerter.evaluateLatencyAnomalies(ctx); err != nil {
		t.Fatalf("evaluateLatencyAnomalies(idempotent) error = %v", err)
	}
	if got := countLatencyAlertsByStatus(ctx, t, dbClient, monitorID, "active"); got != 1 {
		t.Fatalf("active latency alert count after second pass = %d, want 1", got)
	}

	// Recovery: age out the slow checks and land fresh ~100ms checks.
	if _, err := dbClient.ExecContext(ctx,
		`UPDATE check_results SET created_at = $2, started_at = $2, completed_at = $2 WHERE monitor_id = $1`,
		monitorID, now.Add(-10*time.Minute)); err != nil {
		t.Fatalf("age recent checks: %v", err)
	}
	for _, age := range []time.Duration{10, 30, 60} {
		testutil.InsertCheckResult(ctx, t, dbClient, tenantID, monitorID,
			now.Add(-age*time.Second), "success", "monitor", testutil.IntPtr(100))
	}

	if err := alerter.evaluateLatencyAnomalies(ctx); err != nil {
		t.Fatalf("evaluateLatencyAnomalies(recover) error = %v", err)
	}
	if got := countLatencyAlertsByStatus(ctx, t, dbClient, monitorID, "active"); got != 0 {
		t.Fatalf("active latency alert count after recovery = %d, want 0", got)
	}
	if got := countLatencyAlertsByStatus(ctx, t, dbClient, monitorID, "resolved"); got != 1 {
		t.Fatalf("resolved latency alert count after recovery = %d, want 1", got)
	}
}

func TestEvaluateLatencyAnomaliesCoexistsWithAvailabilityAlert(t *testing.T) {
	ctx := context.Background()
	dbClient, cleanup := testutil.SetupPostgresDB(ctx, t)
	defer cleanup()

	tenantID := testutil.InsertTenant(ctx, t, dbClient, "alerter")
	monitorID := testutil.InsertHTTPMonitor(ctx, t, dbClient, tenantID, "API")
	enableTenantAnomaly(ctx, t, dbClient, tenantID)

	// Pre-existing availability alert (open) for the same monitor.
	if _, err := dbClient.ExecContext(ctx, `
		INSERT INTO alerts (id, tenant_id, monitor_id, alert_policy_id, kind, status,
			triggered_at, failure_count, created_at, updated_at)
		VALUES ($1, $2, $3, NULL, 'availability', 'active', NOW(), 2, NOW(), NOW())
	`, uuid.New(), tenantID, monitorID); err != nil {
		t.Fatalf("seed availability alert: %v", err)
	}

	now := time.Now().UTC()
	seedFlatBaseline(ctx, t, dbClient, tenantID, monitorID, now, 12, 100)
	for _, age := range []time.Duration{10, 30, 60, 90} {
		testutil.InsertCheckResult(ctx, t, dbClient, tenantID, monitorID,
			now.Add(-age*time.Second), "success", "monitor", testutil.IntPtr(500))
	}

	alerter := newAnomalyAlerter(dbClient)
	if err := alerter.evaluateLatencyAnomalies(ctx); err != nil {
		t.Fatalf("evaluateLatencyAnomalies error = %v", err)
	}

	if got := countLatencyAlertsByStatus(ctx, t, dbClient, monitorID, "active"); got != 1 {
		t.Fatalf("active latency alert count = %d, want 1", got)
	}
	// The availability alert must be untouched: both kinds open simultaneously.
	if got := countAlerterAlertsByStatus(ctx, t, dbClient, monitorID, "active"); got != 2 {
		t.Fatalf("total active alert count = %d, want 2 (availability + latency)", got)
	}
}
