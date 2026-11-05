//go:build !unit

package alerter

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/yassinebenameur/probara/shared/testutil"
)

// tcpChainMetrics is a TLS-wrapped TCP check's envelope (metrics_data.tcp.tls)
// whose leaf has leafDays left and whose chain's earliest certificate (an
// intermediate) has chainDays left.
func tcpChainMetrics(leafDays, chainDays int) string {
	at := func(days int) string {
		return time.Now().UTC().Add(time.Duration(days)*24*time.Hour + time.Hour).Format(time.RFC3339)
	}
	return fmt.Sprintf(`{"tcp":{"host":"mail.example.com","port":465,"use_tls":true,"tls":{"not_after":%q,"days_until_expiry":%d,"chain_not_after":%q,"chain_min_days":%d}}}`,
		at(leafDays), leafDays, at(chainDays), chainDays)
}

// A TCP+TLS monitor's certificate drives tls_expiry, and tls_check_chain
// switches the measure from the leaf to the chain's earliest expiry: the same
// row alerts only for the monitor that opted in.
func TestEvaluateTLSExpiryTCPChain(t *testing.T) {
	ctx := context.Background()
	dbClient, cleanup := testutil.SetupPostgresDB(ctx, t)
	defer cleanup()

	tenantID := testutil.InsertTenant(ctx, t, dbClient, "alerter")
	leafOnly := insertTLSMonitor(ctx, t, dbClient, tenantID, "tcp", "smtps leaf",
		`{"host":"mail.example.com","port":465,"use_tls":true,"tls_min_days_valid":14}`)
	chain := insertTLSMonitor(ctx, t, dbClient, tenantID, "tcp", "smtps chain",
		`{"host":"mail.example.com","port":465,"use_tls":true,"tls_min_days_valid":14,"tls_check_chain":true}`)

	now := time.Now().UTC()
	insertMetricsDataResult(ctx, t, dbClient, tenantID, leafOnly, now.Add(-10*time.Second), tcpChainMetrics(200, 9))
	insertMetricsDataResult(ctx, t, dbClient, tenantID, chain, now.Add(-10*time.Second), tcpChainMetrics(200, 9))

	alerter := newHostMetricAlerter(dbClient)
	if err := alerter.evaluateTLSExpiry(ctx); err != nil {
		t.Fatalf("evaluateTLSExpiry error = %v", err)
	}
	if got := countTLSExpiryAlerts(ctx, t, dbClient, leafOnly, "active"); got != 0 {
		t.Fatalf("leaf-only monitor: active tls_expiry = %d, want 0 (leaf has 200 days)", got)
	}
	if got := countTLSExpiryAlerts(ctx, t, dbClient, chain, "active"); got != 1 {
		t.Fatalf("chain monitor: active tls_expiry = %d, want 1 (intermediate has 9 days)", got)
	}
	var lastError string
	if err := dbClient.QueryRowContext(ctx, `
		SELECT last_error FROM alerts WHERE monitor_id = $1 AND kind = 'tls_expiry' AND status = 'active'
	`, chain).Scan(&lastError); err != nil {
		t.Fatalf("read tls expiry alert: %v", err)
	}
	if lastError != "tls: expires in 9d (< 14d)" {
		t.Errorf("last_error = %q", lastError)
	}

	// The leaf expiring inside the window alerts the leaf-only monitor too.
	insertMetricsDataResult(ctx, t, dbClient, tenantID, leafOnly, now.Add(-5*time.Second), tcpChainMetrics(5, 5))
	// Turning the opt-in off measures the leaf again and resolves.
	if _, err := dbClient.ExecContext(ctx, `UPDATE monitors SET config = config - 'tls_check_chain' WHERE id = $1`, chain); err != nil {
		t.Fatal(err)
	}
	if err := alerter.evaluateTLSExpiry(ctx); err != nil {
		t.Fatalf("evaluateTLSExpiry error = %v", err)
	}
	if got := countTLSExpiryAlerts(ctx, t, dbClient, leafOnly, "active"); got != 1 {
		t.Fatalf("leaf-only monitor after leaf nears expiry: active = %d, want 1", got)
	}
	if got := countTLSExpiryAlerts(ctx, t, dbClient, chain, "resolved"); got != 1 {
		t.Fatalf("chain monitor after opting out: resolved = %d, want 1", got)
	}
}
