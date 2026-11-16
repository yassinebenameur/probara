//go:build !unit

package alerter

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	shareddb "github.com/yassinebenameur/probara/shared/db"
	"github.com/yassinebenameur/probara/shared/testutil"
)

func insertDomainMonitor(ctx context.Context, t *testing.T, dbClient *shareddb.Client, tenantID uuid.UUID, configJSON string) uuid.UUID {
	t.Helper()
	id := uuid.New()
	if _, err := dbClient.ExecContext(ctx, `
		INSERT INTO monitors (
			id, tenant_id, name, type, config, interval_seconds, timeout_seconds,
			enabled, tags, created_at, updated_at
		) VALUES ($1, $2, 'example.com registration', 'domain', $3::jsonb, 3600, 30,
			TRUE, ARRAY[]::text[], NOW(), NOW())
	`, id, tenantID, configJSON); err != nil {
		t.Fatalf("insert domain monitor: %v", err)
	}
	return id
}

// insertDomainResult records a domain check result whose metrics_data
// carries the snapshot domainJSON, at createdAt.
func insertDomainResult(ctx context.Context, t *testing.T, dbClient *shareddb.Client, tenantID, monitorID uuid.UUID, createdAt time.Time, status, domainJSON string) {
	t.Helper()
	if _, err := dbClient.ExecContext(ctx, `
		INSERT INTO check_results (
			id, monitor_id, tenant_id, job_id, status, latency_ms, created_at, started_at, completed_at,
			result_source, metrics_data
		) VALUES ($1, $2, $3, $4, $5, 40, $6, $6, $6, 'monitor', jsonb_build_object('domain', $7::jsonb))
	`, uuid.New(), monitorID, tenantID, uuid.New(), status, createdAt.UTC(), domainJSON); err != nil {
		t.Fatalf("insert domain result: %v", err)
	}
}

// knownExpiry is a snapshot whose registration expires daysLeft days (plus
// an hour, so the day count is stable) from now.
func knownExpiry(daysLeft int) string {
	at := time.Now().UTC().Add(time.Duration(daysLeft)*24*time.Hour + time.Hour)
	return fmt.Sprintf(`{"domain":"example.com","expires_at":%q,"days_until_expiry":%d,"registrar":"Example Registrar","registered":true,"source":"rdap"}`,
		at.Format(time.RFC3339), daysLeft)
}

const unknownExpiry = `{"domain":"example.de","registered":true,"source":"rdap","unknown_reason":"the registry publishes no expiration date"}`

type domainAlertRow struct {
	status    string
	value     sql.NullFloat64
	threshold sql.NullFloat64
	lastError string
}

func domainAlerts(ctx context.Context, t *testing.T, dbClient *shareddb.Client, monitorID uuid.UUID) []domainAlertRow {
	t.Helper()
	rows, err := dbClient.QueryContext(ctx, `
		SELECT status, metric_value, threshold_value, COALESCE(last_error, '') FROM alerts
		WHERE monitor_id = $1 AND kind = 'domain_expiry' ORDER BY created_at
	`, monitorID)
	if err != nil {
		t.Fatalf("query domain alerts: %v", err)
	}
	defer rows.Close()
	var out []domainAlertRow
	for rows.Next() {
		var r domainAlertRow
		if err := rows.Scan(&r.status, &r.value, &r.threshold, &r.lastError); err != nil {
			t.Fatal(err)
		}
		out = append(out, r)
	}
	return out
}

func evaluateDomain(ctx context.Context, t *testing.T, a *Alerter) {
	t.Helper()
	if err := a.evaluateDomainExpiry(ctx); err != nil {
		t.Fatalf("evaluateDomainExpiry: %v", err)
	}
}

func TestEvaluateDomainExpiryOpensRefreshesAndResolves(t *testing.T) {
	ctx := context.Background()
	dbClient, cleanup := testutil.SetupPostgresDB(ctx, t)
	defer cleanup()
	tenantID := testutil.InsertTenant(ctx, t, dbClient, "alerter")
	monitorID := insertDomainMonitor(ctx, t, dbClient, tenantID, `{"domain":"example.com","min_days_valid":30}`)
	alerter := newHostMetricAlerter(dbClient)
	now := time.Now().UTC()

	// Outside the window: nothing opens.
	insertDomainResult(ctx, t, dbClient, tenantID, monitorID, now.Add(-3*time.Hour), "success", knownExpiry(45))
	evaluateDomain(ctx, t, alerter)
	if got := domainAlerts(ctx, t, dbClient, monitorID); len(got) != 0 {
		t.Fatalf("alerts outside the window: %+v", got)
	}

	// Inside the window: one alert with the days, window, date and registrar.
	insertDomainResult(ctx, t, dbClient, tenantID, monitorID, now.Add(-2*time.Hour), "success", knownExpiry(12))
	evaluateDomain(ctx, t, alerter)
	evaluateDomain(ctx, t, alerter) // idempotent
	got := domainAlerts(ctx, t, dbClient, monitorID)
	if len(got) != 1 || got[0].status != "active" || got[0].value.Float64 != 12 || got[0].threshold.Float64 != 30 {
		t.Fatalf("alerts = %+v", got)
	}
	date := now.Add(12*24*time.Hour + time.Hour).Format("2006-01-02")
	if want := "domain: expires " + date + " (12d < 30d); registrar: Example Registrar"; got[0].lastError != want {
		t.Fatalf("last_error = %q, want %q", got[0].lastError, want)
	}

	// Past expiry (the check now fails too): the open alert follows, so
	// reminders carry the already-expired wording.
	insertDomainResult(ctx, t, dbClient, tenantID, monitorID, now.Add(-time.Hour), "failure", knownExpiry(-3))
	evaluateDomain(ctx, t, alerter)
	got = domainAlerts(ctx, t, dbClient, monitorID)
	if len(got) != 1 || got[0].value.Float64 != -3 || !strings.HasPrefix(got[0].lastError, "domain: expired ") ||
		!strings.Contains(got[0].lastError, "(3d ago)") {
		t.Fatalf("expired: %+v", got)
	}

	// Renewed: resolved.
	insertDomainResult(ctx, t, dbClient, tenantID, monitorID, now.Add(-time.Minute), "success", knownExpiry(365))
	evaluateDomain(ctx, t, alerter)
	got = domainAlerts(ctx, t, dbClient, monitorID)
	if len(got) != 1 || got[0].status != "resolved" {
		t.Fatalf("renewed: %+v", got)
	}
}

func TestEvaluateDomainExpiryDefaultWindow(t *testing.T) {
	ctx := context.Background()
	dbClient, cleanup := testutil.SetupPostgresDB(ctx, t)
	defer cleanup()
	tenantID := testutil.InsertTenant(ctx, t, dbClient, "alerter")
	monitorID := insertDomainMonitor(ctx, t, dbClient, tenantID, `{"domain":"example.com"}`)
	insertDomainResult(ctx, t, dbClient, tenantID, monitorID, time.Now().Add(-time.Minute), "success", knownExpiry(29))
	evaluateDomain(ctx, t, newHostMetricAlerter(dbClient))
	if got := domainAlerts(ctx, t, dbClient, monitorID); len(got) != 1 || got[0].threshold.Float64 != 30 {
		t.Fatalf("default window: %+v", got)
	}
}

func TestEvaluateDomainExpiryUnknown(t *testing.T) {
	ctx := context.Background()
	dbClient, cleanup := testutil.SetupPostgresDB(ctx, t)
	defer cleanup()
	tenantID := testutil.InsertTenant(ctx, t, dbClient, "alerter")
	monitorID := insertDomainMonitor(ctx, t, dbClient, tenantID, `{"domain":"example.de"}`)
	alerter := newHostMetricAlerter(dbClient)
	now := time.Now().UTC()

	// One undetermined lookup is not enough.
	insertDomainResult(ctx, t, dbClient, tenantID, monitorID, now.Add(-2*time.Hour), "success", unknownExpiry)
	evaluateDomain(ctx, t, alerter)
	if got := domainAlerts(ctx, t, dbClient, monitorID); len(got) != 0 {
		t.Fatalf("one unknown lookup opened %+v", got)
	}

	// The second opens the warning, without a value.
	insertDomainResult(ctx, t, dbClient, tenantID, monitorID, now.Add(-time.Hour), "success", unknownExpiry)
	evaluateDomain(ctx, t, alerter)
	got := domainAlerts(ctx, t, dbClient, monitorID)
	if len(got) != 1 || got[0].status != "active" || got[0].value.Valid ||
		got[0].lastError != "domain: expiry unknown: the registry publishes no expiration date" {
		t.Fatalf("unknown: %+v", got)
	}

	// The expiry becomes readable and is far away: resolved.
	insertDomainResult(ctx, t, dbClient, tenantID, monitorID, now.Add(-time.Minute), "success", knownExpiry(200))
	evaluateDomain(ctx, t, alerter)
	if got := domainAlerts(ctx, t, dbClient, monitorID); len(got) != 1 || got[0].status != "resolved" {
		t.Fatalf("known again: %+v", got)
	}
}

func TestEvaluateDomainExpiryKnownExpiryOutlivesFailedLookups(t *testing.T) {
	ctx := context.Background()
	dbClient, cleanup := testutil.SetupPostgresDB(ctx, t)
	defer cleanup()
	tenantID := testutil.InsertTenant(ctx, t, dbClient, "alerter")
	monitorID := insertDomainMonitor(ctx, t, dbClient, tenantID, `{"domain":"example.com","min_days_valid":14}`)
	alerter := newHostMetricAlerter(dbClient)
	now := time.Now().UTC()

	// A day-old expiry inside the window, then failed lookups: the known
	// date still decides, and no "unknown" alert replaces it.
	insertDomainResult(ctx, t, dbClient, tenantID, monitorID, now.Add(-26*time.Hour), "success", knownExpiry(10))
	for i := 3; i > 0; i-- {
		insertDomainResult(ctx, t, dbClient, tenantID, monitorID, now.Add(-time.Duration(i)*time.Hour), "success",
			`{"domain":"example.com","unknown_reason":"rdap: timed out; whois: connection failed"}`)
	}
	evaluateDomain(ctx, t, alerter)
	got := domainAlerts(ctx, t, dbClient, monitorID)
	if len(got) != 1 || !got[0].value.Valid || !strings.HasPrefix(got[0].lastError, "domain: expires ") {
		t.Fatalf("alerts = %+v", got)
	}

	// Beyond the lookback the old date no longer counts: the lookups that
	// failed since raise "expiry unknown" in place of the value.
	if _, err := dbClient.ExecContext(ctx, `UPDATE check_results SET created_at = NOW() - INTERVAL '8 days'
		WHERE monitor_id = $1 AND metrics_data #>> '{domain,expires_at}' IS NOT NULL`, monitorID); err != nil {
		t.Fatal(err)
	}
	evaluateDomain(ctx, t, alerter)
	got = domainAlerts(ctx, t, dbClient, monitorID)
	// The alert changed meaning, so the dated one resolved and an "expiry
	// unknown" one opened in its place.
	if len(got) != 2 || got[0].status != "resolved" || got[1].status != "active" || got[1].value.Valid ||
		got[1].lastError != "domain: expiry unknown: rdap: timed out; whois: connection failed" {
		t.Fatalf("after the lookback: %+v", got)
	}
}

func TestEvaluateDomainExpiryIgnoresNotRegistered(t *testing.T) {
	ctx := context.Background()
	dbClient, cleanup := testutil.SetupPostgresDB(ctx, t)
	defer cleanup()
	tenantID := testutil.InsertTenant(ctx, t, dbClient, "alerter")
	monitorID := insertDomainMonitor(ctx, t, dbClient, tenantID, `{"domain":"example.com"}`)
	for i := 1; i <= 3; i++ {
		insertDomainResult(ctx, t, dbClient, tenantID, monitorID, time.Now().Add(-time.Duration(i)*time.Hour), "failure",
			`{"domain":"example.com","registered":false,"source":"rdap"}`)
	}
	evaluateDomain(ctx, t, newHostMetricAlerter(dbClient))
	if got := domainAlerts(ctx, t, dbClient, monitorID); len(got) != 0 {
		t.Fatalf("a not-registered name is the availability alert's: %+v", got)
	}
}

func TestEvaluateDomainExpiryOrphanResolution(t *testing.T) {
	ctx := context.Background()
	dbClient, cleanup := testutil.SetupPostgresDB(ctx, t)
	defer cleanup()
	tenantID := testutil.InsertTenant(ctx, t, dbClient, "alerter")
	monitorID := insertDomainMonitor(ctx, t, dbClient, tenantID, `{"domain":"example.com"}`)
	alerter := newHostMetricAlerter(dbClient)
	insertDomainResult(ctx, t, dbClient, tenantID, monitorID, time.Now().Add(-time.Minute), "success", knownExpiry(5))
	evaluateDomain(ctx, t, alerter)
	if got := domainAlerts(ctx, t, dbClient, monitorID); len(got) != 1 || got[0].status != "active" {
		t.Fatalf("open: %+v", got)
	}
	if _, err := dbClient.ExecContext(ctx, `UPDATE monitors SET enabled = FALSE WHERE id = $1`, monitorID); err != nil {
		t.Fatal(err)
	}
	evaluateDomain(ctx, t, alerter)
	if got := domainAlerts(ctx, t, dbClient, monitorID); len(got) != 1 || got[0].status != "resolved" {
		t.Fatalf("disabled monitor's alert: %+v", got)
	}
}

// Two alerter replicas evaluating at the same time open one alert and
// resolve it once.
func TestEvaluateDomainExpiryTwoReplicas(t *testing.T) {
	ctx := context.Background()
	dbClient, cleanup := testutil.SetupPostgresDB(ctx, t)
	defer cleanup()
	tenantID := testutil.InsertTenant(ctx, t, dbClient, "alerter")
	monitorID := insertDomainMonitor(ctx, t, dbClient, tenantID, `{"domain":"example.com"}`)
	replicas := []*Alerter{newHostMetricAlerter(dbClient), newHostMetricAlerter(dbClient)}
	now := time.Now().UTC()

	race := func() {
		var wg sync.WaitGroup
		for _, r := range replicas {
			for i := 0; i < 3; i++ {
				wg.Add(1)
				go func(r *Alerter) {
					defer wg.Done()
					if err := r.evaluateDomainExpiry(ctx); err != nil {
						t.Errorf("evaluateDomainExpiry: %v", err)
					}
				}(r)
			}
		}
		wg.Wait()
	}

	insertDomainResult(ctx, t, dbClient, tenantID, monitorID, now.Add(-time.Hour), "success", knownExpiry(7))
	race()
	if got := domainAlerts(ctx, t, dbClient, monitorID); len(got) != 1 || got[0].status != "active" {
		t.Fatalf("after racing opens: %+v", got)
	}

	insertDomainResult(ctx, t, dbClient, tenantID, monitorID, now.Add(-time.Minute), "success", knownExpiry(300))
	race()
	got := domainAlerts(ctx, t, dbClient, monitorID)
	if len(got) != 1 || got[0].status != "resolved" {
		t.Fatalf("after racing resolves: %+v", got)
	}
	var recoveryEntries int
	if err := dbClient.QueryRowContext(ctx, `SELECT COUNT(*) FROM alerts WHERE monitor_id = $1 AND resolved_at IS NOT NULL`, monitorID).Scan(&recoveryEntries); err != nil {
		t.Fatal(err)
	}
	if recoveryEntries != 1 {
		t.Fatalf("resolved rows = %d", recoveryEntries)
	}
}

// Two rows of one round (two locations reporting together) are one
// undetermined lookup, not two.
func TestEvaluateDomainExpiryCountsRoundsNotRows(t *testing.T) {
	ctx := context.Background()
	dbClient, cleanup := testutil.SetupPostgresDB(ctx, t)
	defer cleanup()
	tenantID := testutil.InsertTenant(ctx, t, dbClient, "alerter")
	monitorID := insertDomainMonitor(ctx, t, dbClient, tenantID, `{"domain":"example.fr"}`)
	alerter := newHostMetricAlerter(dbClient)
	round := time.Now().UTC().Add(-time.Hour)
	insertDomainResult(ctx, t, dbClient, tenantID, monitorID, round, "success", unknownExpiry)
	insertDomainResult(ctx, t, dbClient, tenantID, monitorID, round.Add(20*time.Second), "success", unknownExpiry)
	evaluateDomain(ctx, t, alerter)
	if got := domainAlerts(ctx, t, dbClient, monitorID); len(got) != 0 {
		t.Fatalf("one round opened %+v", got)
	}
}

// A registry that never publishes a date: "expiry unknown" is its normal
// state, so no alert opens, and one opened before resolves.
func TestEvaluateDomainExpiryNoDateRegistry(t *testing.T) {
	ctx := context.Background()
	dbClient, cleanup := testutil.SetupPostgresDB(ctx, t)
	defer cleanup()
	tenantID := testutil.InsertTenant(ctx, t, dbClient, "alerter")
	monitorID := insertDomainMonitor(ctx, t, dbClient, tenantID, `{"domain":"example.de"}`)
	alerter := newHostMetricAlerter(dbClient)
	now := time.Now().UTC()
	const noDate = `{"domain":"example.de","registered":true,"source":"whois","no_expiry_published":true,"unknown_reason":"the .de registry does not publish an expiry date; this monitor can only detect that the domain stops being registered"}`
	for i := 4; i > 0; i-- {
		insertDomainResult(ctx, t, dbClient, tenantID, monitorID, now.Add(-time.Duration(i)*time.Hour), "success", noDate)
	}
	evaluateDomain(ctx, t, alerter)
	if got := domainAlerts(ctx, t, dbClient, monitorID); len(got) != 0 {
		t.Fatalf("no-date registry opened %+v", got)
	}

	// An "expiry unknown" alert opened before the TLD was known to publish
	// no date resolves at the next lookup that says so.
	mustExec(ctx, t, dbClient, `INSERT INTO alerts (id, tenant_id, monitor_id, kind, status, triggered_at, failure_count, last_error, created_at, updated_at)
		VALUES ($1, $2, $3, 'domain_expiry', 'active', NOW(), 0, 'domain: expiry unknown: x', NOW(), NOW())`, uuid.New(), tenantID, monitorID)
	evaluateDomain(ctx, t, alerter)
	if got := domainAlerts(ctx, t, dbClient, monitorID); len(got) != 1 || got[0].status != "resolved" {
		t.Fatalf("stale unknown alert: %+v", got)
	}
}

// A passed date in the registry's renew grace period is still registered:
// inside the window (0 days left), not expired.
func TestEvaluateDomainExpiryRenewalGrace(t *testing.T) {
	ctx := context.Background()
	dbClient, cleanup := testutil.SetupPostgresDB(ctx, t)
	defer cleanup()
	tenantID := testutil.InsertTenant(ctx, t, dbClient, "alerter")
	monitorID := insertDomainMonitor(ctx, t, dbClient, tenantID, `{"domain":"example.com"}`)
	at := time.Now().UTC().Add(-3 * 24 * time.Hour)
	snapshot := fmt.Sprintf(`{"domain":"example.com","expires_at":%q,"registered":true,"source":"rdap","renewal_grace":true,"statuses":["auto renew period"]}`, at.Format(time.RFC3339))
	insertDomainResult(ctx, t, dbClient, tenantID, monitorID, time.Now().Add(-time.Minute), "success", snapshot)
	evaluateDomain(ctx, t, newHostMetricAlerter(dbClient))
	got := domainAlerts(ctx, t, dbClient, monitorID)
	if len(got) != 1 || got[0].value.Float64 != 0 || !strings.Contains(got[0].lastError, "renew grace period") {
		t.Fatalf("grace: %+v", got)
	}
}
