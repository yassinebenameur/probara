//go:build !unit

package alerter

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/yassinebenameur/probara/shared/testutil"
)

// tcpChainMetrics renders a TLS-wrapped TCP check's metrics_data whose leaf
// expires in leafDays and whose intermediate expires in interDays.
func tcpChainMetrics(leafDays, interDays int) string {
	at := func(days int) string {
		return time.Now().UTC().Add(time.Duration(days)*24*time.Hour + time.Hour).Format(time.RFC3339)
	}
	earliest, subject := leafDays, "CN=imap.example.com"
	if interDays < leafDays {
		earliest, subject = interDays, "CN=Example Issuing CA 2,O=Example"
	}
	return fmt.Sprintf(`{"tcp":{"host":"imap.example.com","port":993,"use_tls":true,"tls":{"subject":"CN=imap.example.com","not_after":%q,"days_until_expiry":%d,"chain_not_after":%q,"chain_min_days":%d,"chain_expiring_subject":%q}}}`,
		at(leafDays), leafDays, at(earliest), earliest, subject)
}

// A TLS-wrapped TCP monitor feeds tls_expiry like HTTPS, and the evaluation
// acts on the earliest expiry of the presented chain: an intermediate inside
// the window opens the alert although the leaf is fine, the alert names it,
// and a chain whose intermediate was replaced resolves it.
func TestEvaluateTLSExpiryTCPChain(t *testing.T) {
	ctx := context.Background()
	dbClient, cleanup := testutil.SetupPostgresDB(ctx, t)
	defer cleanup()

	tenantID := testutil.InsertTenant(ctx, t, dbClient, "alerter")
	monitorID := insertTLSMonitor(ctx, t, dbClient, tenantID, "tcp", "imap",
		`{"host":"imap.example.com","port":993,"use_tls":true,"tls_min_days_valid":14}`)

	now := time.Now().UTC()
	insertMetricsDataResult(ctx, t, dbClient, tenantID, monitorID, now.Add(-10*time.Second), tcpChainMetrics(200, 9))

	alerter := newHostMetricAlerter(dbClient)
	if err := alerter.evaluateTLSExpiry(ctx); err != nil {
		t.Fatalf("evaluateTLSExpiry(open) error = %v", err)
	}
	if got := countTLSExpiryAlerts(ctx, t, dbClient, monitorID, "active"); got != 1 {
		t.Fatalf("active tls_expiry alert count = %d, want 1 (intermediate in 9d < 14d)", got)
	}
	var value float64
	var lastError string
	if err := dbClient.QueryRowContext(ctx, `
		SELECT metric_value, last_error FROM alerts WHERE monitor_id = $1 AND kind = 'tls_expiry' AND status = 'active'
	`, monitorID).Scan(&value, &lastError); err != nil {
		t.Fatalf("read tls expiry alert: %v", err)
	}
	wantDate := now.Add(9*24*time.Hour + time.Hour).Format("2006-01-02")
	want := "tls: intermediate certificate CN=Example Issuing CA 2,O=Example expires on " + wantDate + " (in 9d, < 14d)"
	if value != 9 || lastError != want {
		t.Fatalf("alert = %v / %q, want 9 / %q", value, lastError, want)
	}

	insertMetricsDataResult(ctx, t, dbClient, tenantID, monitorID, now.Add(-1*time.Second), tcpChainMetrics(200, 400))
	if err := alerter.evaluateTLSExpiry(ctx); err != nil {
		t.Fatalf("evaluateTLSExpiry(renewed) error = %v", err)
	}
	if got := countTLSExpiryAlerts(ctx, t, dbClient, monitorID, "resolved"); got != 1 {
		t.Fatalf("resolved tls_expiry alert count = %d, want 1 after the chain renewed", got)
	}
}
