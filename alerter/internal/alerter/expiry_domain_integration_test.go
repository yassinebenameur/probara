//go:build !unit

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

// domainMetrics renders a domain check's metrics_data whose registration
// expires daysLeft days from now (shared/domainexpiry).
func domainMetrics(daysLeft int) string {
	expires := time.Now().UTC().Add(time.Duration(daysLeft)*24*time.Hour + time.Hour)
	return fmt.Sprintf(`{"domain":{"domain":"example.com","expires_at":%q,"days_until_expiry":%d,"registered":true,"source":"rdap"}}`,
		expires.Format(time.RFC3339), daysLeft)
}

func countExpiryAlerts(ctx context.Context, t *testing.T, dbClient *shareddb.Client, monitorID uuid.UUID, kind, status string) int {
	t.Helper()
	var count int
	if err := dbClient.QueryRowContext(ctx, `
		SELECT COUNT(*) FROM alerts WHERE monitor_id = $1 AND kind = $2 AND status = $3
	`, monitorID, kind, status).Scan(&count); err != nil {
		t.Fatalf("count %s alerts: %v", kind, err)
	}
	return count
}

// A domain monitor's registration expiry opens, keeps and resolves a
// domain_expiry alert; the TLS spec never touches it and vice versa.
func TestEvaluateDomainExpiryOpensAndResolves(t *testing.T) {
	ctx := context.Background()
	dbClient, cleanup := testutil.SetupPostgresDB(ctx, t)
	defer cleanup()

	tenantID := testutil.InsertTenant(ctx, t, dbClient, "alerter")
	domainID := insertTLSMonitor(ctx, t, dbClient, tenantID, "domain", "example.com",
		`{"domain":"example.com","min_days_valid":30}`)
	// An HTTP monitor with the same key name must not be read by the
	// domain spec (it is restricted to type domain).
	httpID := insertHTTPMonitor(ctx, t, dbClient, tenantID, "site",
		`{"url":"https://example.com","method":"GET","min_days_valid":30}`)

	now := time.Now().UTC()
	insertMetricsDataResult(ctx, t, dbClient, tenantID, domainID, now.Add(-10*time.Second), domainMetrics(12))
	insertMetricsDataResult(ctx, t, dbClient, tenantID, httpID, now.Add(-10*time.Second), domainMetrics(12))

	alerter := newHostMetricAlerter(dbClient)
	if err := alerter.evaluateTLSExpiry(ctx); err != nil {
		t.Fatalf("evaluateTLSExpiry error = %v", err)
	}
	if got := countExpiryAlerts(ctx, t, dbClient, domainID, "tls_expiry", "active"); got != 0 {
		t.Fatalf("tls spec opened %d alerts for a domain monitor", got)
	}
	if err := alerter.evaluateDomainExpiry(ctx); err != nil {
		t.Fatalf("evaluateDomainExpiry(open) error = %v", err)
	}
	if got := countExpiryAlerts(ctx, t, dbClient, domainID, "domain_expiry", "active"); got != 1 {
		t.Fatalf("active domain_expiry alert count = %d, want 1", got)
	}
	if got := countExpiryAlerts(ctx, t, dbClient, httpID, "domain_expiry", "active"); got != 0 {
		t.Fatalf("an http monitor got %d domain_expiry alerts", got)
	}
	var value, threshold float64
	var lastError string
	if err := dbClient.QueryRowContext(ctx, `
		SELECT metric_value, threshold_value, last_error FROM alerts
		WHERE monitor_id = $1 AND kind = 'domain_expiry' AND status = 'active'
	`, domainID).Scan(&value, &threshold, &lastError); err != nil {
		t.Fatalf("read domain expiry alert: %v", err)
	}
	if value != 12 || threshold != 30 || lastError != "domain: expires in 12d (< 30d)" {
		t.Fatalf("alert = %v %v %q", value, threshold, lastError)
	}

	// Idempotent; and the TLS orphan sweep leaves it alone.
	if err := alerter.evaluateDomainExpiry(ctx); err != nil {
		t.Fatalf("evaluateDomainExpiry(idempotent) error = %v", err)
	}
	if err := alerter.evaluateTLSExpiry(ctx); err != nil {
		t.Fatalf("evaluateTLSExpiry error = %v", err)
	}
	if got := countExpiryAlerts(ctx, t, dbClient, domainID, "domain_expiry", "active"); got != 1 {
		t.Fatalf("active domain_expiry alert count after second pass = %d, want 1", got)
	}

	// Renewal resolves it.
	insertMetricsDataResult(ctx, t, dbClient, tenantID, domainID, now.Add(-1*time.Second), domainMetrics(365))
	if err := alerter.evaluateDomainExpiry(ctx); err != nil {
		t.Fatalf("evaluateDomainExpiry(renewed) error = %v", err)
	}
	if got := countExpiryAlerts(ctx, t, dbClient, domainID, "domain_expiry", "resolved"); got != 1 {
		t.Fatalf("resolved domain_expiry alert count = %d, want 1", got)
	}
}

func TestEvaluateDomainExpiryExpiredAndOrphan(t *testing.T) {
	ctx := context.Background()
	dbClient, cleanup := testutil.SetupPostgresDB(ctx, t)
	defer cleanup()

	tenantID := testutil.InsertTenant(ctx, t, dbClient, "alerter")
	domainID := insertTLSMonitor(ctx, t, dbClient, tenantID, "domain", "lapsed.com",
		`{"domain":"lapsed.com","min_days_valid":0}`)
	now := time.Now().UTC()
	insertMetricsDataResult(ctx, t, dbClient, tenantID, domainID, now.Add(-10*time.Second), domainMetrics(-4))

	alerter := newHostMetricAlerter(dbClient)
	alerter.evaluateExpiryAlerts(ctx)
	var lastError string
	if err := dbClient.QueryRowContext(ctx, `
		SELECT last_error FROM alerts WHERE monitor_id = $1 AND kind = 'domain_expiry' AND status = 'active'
	`, domainID).Scan(&lastError); err != nil {
		t.Fatalf("read domain expiry alert: %v", err)
	}
	if lastError != "domain: expired 4d ago (< 0d)" {
		t.Fatalf("last_error = %q", lastError)
	}

	// Removing the threshold resolves it as an orphan.
	if _, err := dbClient.ExecContext(ctx, `UPDATE monitors SET config = config - 'min_days_valid' WHERE id = $1`, domainID); err != nil {
		t.Fatal(err)
	}
	alerter.evaluateExpiryAlerts(ctx)
	if got := countExpiryAlerts(ctx, t, dbClient, domainID, "domain_expiry", "active"); got != 0 {
		t.Fatalf("active domain_expiry alerts after threshold removal = %d, want 0", got)
	}
	if got := countExpiryAlerts(ctx, t, dbClient, domainID, "domain_expiry", "resolved"); got != 1 {
		t.Fatalf("resolved domain_expiry alerts = %d, want 1", got)
	}
}
