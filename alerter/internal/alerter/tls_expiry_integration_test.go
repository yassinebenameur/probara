package alerter

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/google/uuid"

	shareddb "github.com/yassinebenameur/probara/shared/db"
	"github.com/yassinebenameur/probara/shared/testutil"
)

// insertHTTPMonitor inserts an enabled http monitor with the given config JSON.
func insertHTTPMonitor(ctx context.Context, t *testing.T, dbClient *shareddb.Client, tenantID uuid.UUID, name, configJSON string) uuid.UUID {
	t.Helper()
	id := uuid.New()
	if _, err := dbClient.ExecContext(ctx, `
		INSERT INTO monitors (
			id, tenant_id, name, type, config, interval_seconds, timeout_seconds,
			alert_policy_id, enabled, tags, agent_id, push_token, next_run_at, created_at, updated_at
		) VALUES (
			$1, $2, $3, 'http', $4::jsonb, 60, 30,
			NULL, TRUE, ARRAY[]::text[], NULL, NULL, NULL, NOW(), NOW()
		)
	`, id, tenantID, name, configJSON); err != nil {
		t.Fatalf("insert http monitor: %v", err)
	}
	return id
}

// tlsMetrics renders a metrics_data envelope whose certificate expires
// daysLeft days from now.
func tlsMetrics(daysLeft int) string {
	notAfter := time.Now().UTC().Add(time.Duration(daysLeft)*24*time.Hour + time.Hour)
	return fmt.Sprintf(`{"http":{"final_url":"https://example.com","tls":{"not_after":%q,"days_until_expiry":%d}}}`,
		notAfter.Format(time.RFC3339), daysLeft)
}

func countTLSExpiryAlerts(ctx context.Context, t *testing.T, dbClient *shareddb.Client, monitorID uuid.UUID, status string) int {
	t.Helper()
	var count int
	if err := dbClient.QueryRowContext(ctx, `
		SELECT COUNT(*) FROM alerts
		WHERE monitor_id = $1 AND kind = 'tls_expiry' AND status = $2
	`, monitorID, status).Scan(&count); err != nil {
		t.Fatalf("count tls expiry alerts: %v", err)
	}
	return count
}

func TestEvaluateTLSExpiryOpensAndResolves(t *testing.T) {
	ctx := context.Background()
	dbClient, cleanup := testutil.SetupPostgresDB(ctx, t)
	defer cleanup()

	tenantID := testutil.InsertTenant(ctx, t, dbClient, "alerter")
	monitorID := insertHTTPMonitor(ctx, t, dbClient, tenantID, "site-1",
		`{"url":"https://example.com","method":"GET","tls_min_days_valid":14}`)

	now := time.Now().UTC()

	// Latest result carries a cert inside the window (11d < 14d).
	insertAgentMetricsResult(ctx, t, dbClient, tenantID, monitorID, now.Add(-10*time.Second), tlsMetrics(11))

	alerter := newHostMetricAlerter(dbClient)
	if err := alerter.evaluateTLSExpiry(ctx); err != nil {
		t.Fatalf("evaluateTLSExpiry(open) error = %v", err)
	}
	if got := countTLSExpiryAlerts(ctx, t, dbClient, monitorID, "active"); got != 1 {
		t.Fatalf("active tls_expiry alert count = %d, want 1", got)
	}

	// Recorded value/threshold/reason should reflect the breach.
	var value, threshold float64
	var lastError string
	if err := dbClient.QueryRowContext(ctx, `
		SELECT metric_value, threshold_value, last_error FROM alerts
		WHERE monitor_id = $1 AND kind = 'tls_expiry' AND status = 'active'
	`, monitorID).Scan(&value, &threshold, &lastError); err != nil {
		t.Fatalf("read tls expiry alert: %v", err)
	}
	if value != 11 {
		t.Errorf("metric_value = %v, want 11", value)
	}
	if threshold != 14 {
		t.Errorf("threshold_value = %v, want 14", threshold)
	}
	if lastError != "tls: expires in 11d (< 14d)" {
		t.Errorf("last_error = %q, want %q", lastError, "tls: expires in 11d (< 14d)")
	}

	// Idempotent: a second pass while still inside the window must not duplicate.
	if err := alerter.evaluateTLSExpiry(ctx); err != nil {
		t.Fatalf("evaluateTLSExpiry(idempotent) error = %v", err)
	}
	if got := countTLSExpiryAlerts(ctx, t, dbClient, monitorID, "active"); got != 1 {
		t.Fatalf("active tls_expiry alert count after second pass = %d, want 1", got)
	}

	// Renewal: a newer result with a fresh cert resolves the alert.
	insertAgentMetricsResult(ctx, t, dbClient, tenantID, monitorID, now.Add(-1*time.Second), tlsMetrics(90))
	if err := alerter.evaluateTLSExpiry(ctx); err != nil {
		t.Fatalf("evaluateTLSExpiry(renewed) error = %v", err)
	}
	if got := countTLSExpiryAlerts(ctx, t, dbClient, monitorID, "active"); got != 0 {
		t.Fatalf("active tls_expiry alert count after renewal = %d, want 0", got)
	}
	if got := countTLSExpiryAlerts(ctx, t, dbClient, monitorID, "resolved"); got != 1 {
		t.Fatalf("resolved tls_expiry alert count after renewal = %d, want 1", got)
	}
}

func TestEvaluateTLSExpiryOrphanResolution(t *testing.T) {
	ctx := context.Background()
	dbClient, cleanup := testutil.SetupPostgresDB(ctx, t)
	defer cleanup()

	tenantID := testutil.InsertTenant(ctx, t, dbClient, "alerter")
	monitorID := insertHTTPMonitor(ctx, t, dbClient, tenantID, "site-2",
		`{"url":"https://example.com","method":"GET","tls_min_days_valid":14}`)

	now := time.Now().UTC()
	insertAgentMetricsResult(ctx, t, dbClient, tenantID, monitorID, now.Add(-10*time.Second), tlsMetrics(3))

	alerter := newHostMetricAlerter(dbClient)
	if err := alerter.evaluateTLSExpiry(ctx); err != nil {
		t.Fatalf("evaluateTLSExpiry(open) error = %v", err)
	}
	if got := countTLSExpiryAlerts(ctx, t, dbClient, monitorID, "active"); got != 1 {
		t.Fatalf("active tls_expiry alert count = %d, want 1", got)
	}

	// Removing the threshold from config drops the monitor out of the
	// evaluated set — the open alert must resolve as an orphan.
	if _, err := dbClient.ExecContext(ctx, `
		UPDATE monitors SET config = config - 'tls_min_days_valid' WHERE id = $1
	`, monitorID); err != nil {
		t.Fatalf("remove threshold: %v", err)
	}
	if err := alerter.evaluateTLSExpiry(ctx); err != nil {
		t.Fatalf("evaluateTLSExpiry(orphan) error = %v", err)
	}
	if got := countTLSExpiryAlerts(ctx, t, dbClient, monitorID, "active"); got != 0 {
		t.Fatalf("active tls_expiry alert count after threshold removal = %d, want 0", got)
	}
	if got := countTLSExpiryAlerts(ctx, t, dbClient, monitorID, "resolved"); got != 1 {
		t.Fatalf("resolved tls_expiry alert count after threshold removal = %d, want 1", got)
	}
}

// A failing check result (e.g. HTTP 500) still carries certificate info; the
// evaluator must use it — TLS expiry is orthogonal to availability.
func TestEvaluateTLSExpiryUsesFailingResults(t *testing.T) {
	ctx := context.Background()
	dbClient, cleanup := testutil.SetupPostgresDB(ctx, t)
	defer cleanup()

	tenantID := testutil.InsertTenant(ctx, t, dbClient, "alerter")
	monitorID := insertHTTPMonitor(ctx, t, dbClient, tenantID, "site-3",
		`{"url":"https://example.com","method":"GET","tls_min_days_valid":14}`)

	now := time.Now().UTC()
	if _, err := dbClient.ExecContext(ctx, `
		INSERT INTO check_results (
			id, monitor_id, tenant_id, job_id, status, latency_ms, created_at, started_at, completed_at,
			result_source, error_message, metrics_data
		) VALUES ($1, $2, $3, $4, 'failure', 5, $5, $5, $5, 'monitor', 'status_code: got 500', $6::jsonb)
	`, uuid.New(), monitorID, tenantID, uuid.New(), now.Add(-10*time.Second), tlsMetrics(5)); err != nil {
		t.Fatalf("insert failing result: %v", err)
	}

	alerter := newHostMetricAlerter(dbClient)
	if err := alerter.evaluateTLSExpiry(ctx); err != nil {
		t.Fatalf("evaluateTLSExpiry error = %v", err)
	}
	if got := countTLSExpiryAlerts(ctx, t, dbClient, monitorID, "active"); got != 1 {
		t.Fatalf("active tls_expiry alert count = %d, want 1", got)
	}
}
