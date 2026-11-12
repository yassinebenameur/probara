//go:build !unit

package alerter

import (
	"context"
	"database/sql"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/yassinebenameur/probara/shared/config"
	shareddb "github.com/yassinebenameur/probara/shared/db"
	"github.com/yassinebenameur/probara/shared/incidentrecovery"
	"github.com/yassinebenameur/probara/shared/logger"
	"github.com/yassinebenameur/probara/shared/testutil"
)

func TestAlerterFiringAlertCreatesAndReusesAutoIncident(t *testing.T) {
	// Verifies that after the first alert resolves, a new alert for the same
	// monitor reuses the still-open auto-incident rather than creating a
	// duplicate. Under the one-open-alert-per-monitor invariant, the monitor
	// must have no open alert before the second one can be inserted.
	ctx := context.Background()
	dbClient, cleanup := testutil.SetupPostgresDB(ctx, t)
	defer cleanup()

	tenantID := testutil.InsertTenant(ctx, t, dbClient, "alerter")
	monitorID := testutil.InsertHTTPMonitor(ctx, t, dbClient, tenantID, "API")
	now := time.Now().UTC()
	alerter := newIntegrationAlerter(dbClient)

	binding := policyBinding{
		MonitorID:            monitorID,
		TenantID:             tenantID,
		MonitorName:          "API",
		CreateIncidentOnFire: true,
	}

	firstAlert, err := fireTestAlert(ctx, alerter, binding, 2, now)
	if err != nil {
		t.Fatalf("fireTestAlert(first) error = %v", err)
	}
	incidentID := loadAlerterAutoIncidentID(ctx, t, dbClient, tenantID, monitorID)
	if count := countAlerterIncidentAlertLinks(ctx, t, dbClient, incidentID); count != 1 {
		t.Fatalf("incident alert count after first create = %d, want 1", count)
	}

	// Resolve the first alert so the monitor has no open alert. The incident
	// remains open (state = investigating) until an operator closes it.
	if _, err := alerter.resolveAlert(ctx, firstAlert.ID, now.Add(30*time.Second)); err != nil {
		t.Fatalf("resolveAlert(first) error = %v", err)
	}

	// The monitor re-fires; the new alert must link to the existing incident.
	secondAlert, err := fireTestAlert(ctx, alerter, binding, 3, now.Add(time.Minute))
	if err != nil {
		t.Fatalf("fireTestAlert(second) error = %v", err)
	}
	reusedIncidentID := loadAlerterAutoIncidentID(ctx, t, dbClient, tenantID, monitorID)
	if reusedIncidentID != incidentID {
		t.Fatalf("reused incident id = %s, want %s", reusedIncidentID, incidentID)
	}
	if count := countAlerterIncidentAlertLinks(ctx, t, dbClient, incidentID); count != 2 {
		t.Fatalf("incident alert count after second create = %d, want 2", count)
	}
	if firstAlert.ID == secondAlert.ID {
		t.Fatalf("expected distinct alerts")
	}
}

func TestAlerterResolveAlertRecordsRecoveryTimeline(t *testing.T) {
	// Two monitors fire alerts that both link to the same auto-incident
	// (same tenant; the second monitor re-fires after the
	// first resolves so it finds the still-open incident). Recovery is recorded
	// only when the last linked alert resolves.
	ctx := context.Background()
	dbClient, cleanup := testutil.SetupPostgresDB(ctx, t)
	defer cleanup()

	tenantID := testutil.InsertTenant(ctx, t, dbClient, "alerter")
	monitorID := testutil.InsertHTTPMonitor(ctx, t, dbClient, tenantID, "API")
	now := time.Now().UTC()
	alerter := newIntegrationAlerter(dbClient)

	binding := policyBinding{
		MonitorID:            monitorID,
		TenantID:             tenantID,
		MonitorName:          "API",
		CreateIncidentOnFire: true,
	}

	// First fire: alert + incident created.
	firstAlert, err := fireTestAlert(ctx, alerter, binding, 2, now)
	if err != nil {
		t.Fatalf("fireTestAlert(first) error = %v", err)
	}
	incidentID := loadAlerterAutoIncidentID(ctx, t, dbClient, tenantID, monitorID)

	// Resolve first alert; incident stays open (state = investigating).
	if _, err := alerter.resolveAlert(ctx, firstAlert.ID, now.Add(time.Minute)); err != nil {
		t.Fatalf("resolveAlert(first) error = %v", err)
	}
	// After first (and only) alert resolves, recovery IS recorded.
	if got := countAlerterTimelineMessages(ctx, t, dbClient, incidentID, "All linked alerts recovered"); got != 1 {
		t.Fatalf("recovery message count after first resolve = %d, want 1", got)
	}

	// Monitor re-fires; new alert links to the same still-open incident.
	secondAlert, err := fireTestAlert(ctx, alerter, binding, 3, now.Add(2*time.Minute))
	if err != nil {
		t.Fatalf("fireTestAlert(second) error = %v", err)
	}
	// Incident now has 2 alerts (1 resolved, 1 active) — not all resolved yet.
	if got := countAlerterTimelineMessages(ctx, t, dbClient, incidentID, "All linked alerts recovered"); got != 1 {
		t.Fatalf("recovery message count after second create = %d, want still 1", got)
	}

	// Resolve second alert; all alerts now resolved → second recovery message.
	if _, err := alerter.resolveAlert(ctx, secondAlert.ID, now.Add(3*time.Minute)); err != nil {
		t.Fatalf("resolveAlert(second) error = %v", err)
	}
	if got := countAlerterTimelineMessages(ctx, t, dbClient, incidentID, "All linked alerts recovered"); got != 2 {
		t.Fatalf("recovery message count after second resolve = %d, want 2", got)
	}
}

func TestAlerterResolveAlertSerializesConcurrentFinalResolutions(t *testing.T) {
	// Two alerts are linked to the same auto-incident. The first is created via
	// the normal path; the second is inserted directly (bypassing the unique
	// index on the first monitor) and manually linked to the same incident.
	// This exercises the advisory-lock serialization in
	// incidentrecovery.RecordForAlertTx when both are resolved concurrently.
	ctx := context.Background()
	dbClient, cleanup := testutil.SetupPostgresDB(ctx, t)
	defer cleanup()

	tenantID := testutil.InsertTenant(ctx, t, dbClient, "alerter")
	monitorID := testutil.InsertHTTPMonitor(ctx, t, dbClient, tenantID, "API")
	monitor2ID := testutil.InsertHTTPMonitor(ctx, t, dbClient, tenantID, "API2")
	now := time.Now().UTC()
	alerter := newIntegrationAlerter(dbClient)

	binding := policyBinding{
		MonitorID:            monitorID,
		TenantID:             tenantID,
		MonitorName:          "API",
		CreateIncidentOnFire: true,
	}

	firstAlert, err := fireTestAlert(ctx, alerter, binding, 2, now)
	if err != nil {
		t.Fatalf("fireTestAlert(first) error = %v", err)
	}
	incidentID := loadAlerterAutoIncidentID(ctx, t, dbClient, tenantID, monitorID)

	// Insert a second alert on a different monitor and link it directly to the
	// same incident, simulating a multi-monitor incident under the new schema.
	secondAlert := insertAlerterSecondAlertAndLink(ctx, t, dbClient, tenantID, monitor2ID, incidentID, now.Add(time.Minute))

	tx1, err := dbClient.BeginTx(ctx, nil)
	if err != nil {
		t.Fatalf("BeginTx(tx1) error = %v", err)
	}
	defer func() {
		_ = tx1.Rollback()
	}()

	tx2, err := dbClient.BeginTx(ctx, nil)
	if err != nil {
		t.Fatalf("BeginTx(tx2) error = %v", err)
	}

	resolveTime := now.Add(2 * time.Minute)
	if err := setAlerterAlertResolvedTx(ctx, tx1, firstAlert.ID, resolveTime); err != nil {
		t.Fatalf("setAlerterAlertResolvedTx(first) error = %v", err)
	}
	if err := setAlerterAlertResolvedTx(ctx, tx2, secondAlert.ID, resolveTime.Add(time.Minute)); err != nil {
		t.Fatalf("setAlerterAlertResolvedTx(second) error = %v", err)
	}

	if err := incidentrecovery.RecordForAlertTx(ctx, tx1, firstAlert.ID); err != nil {
		t.Fatalf("incidentrecovery.RecordForAlertTx(tx1) error = %v", err)
	}
	assertAlerterIncidentRecoveryLockHeld(ctx, t, dbClient, incidentID)

	errCh := make(chan error, 1)
	go func() {
		if err := incidentrecovery.RecordForAlertTx(ctx, tx2, secondAlert.ID); err != nil {
			_ = tx2.Rollback()
			errCh <- err
			return
		}
		errCh <- tx2.Commit()
	}()

	if err := tx1.Commit(); err != nil {
		t.Fatalf("Commit(tx1) error = %v", err)
	}
	if err := <-errCh; err != nil {
		t.Fatalf("tx2 recovery/commit error = %v", err)
	}

	if got := countAlerterTimelineMessages(ctx, t, dbClient, incidentID, "All linked alerts recovered"); got != 1 {
		t.Fatalf("recovery message count after concurrent resolve = %d, want 1", got)
	}
}

func TestAlerterResolveAlertSkipsDuplicateAfterLaterNonRecoveryEntry(t *testing.T) {
	ctx := context.Background()
	dbClient, cleanup := testutil.SetupPostgresDB(ctx, t)
	defer cleanup()

	tenantID := testutil.InsertTenant(ctx, t, dbClient, "alerter")
	monitorID := testutil.InsertHTTPMonitor(ctx, t, dbClient, tenantID, "API")
	now := time.Now().UTC()
	alerter := newIntegrationAlerter(dbClient)

	binding := policyBinding{
		MonitorID:            monitorID,
		TenantID:             tenantID,
		MonitorName:          "API",
		CreateIncidentOnFire: true,
	}

	alert, err := fireTestAlert(ctx, alerter, binding, 2, now)
	if err != nil {
		t.Fatalf("fireTestAlert() error = %v", err)
	}
	incidentID := loadAlerterAutoIncidentID(ctx, t, dbClient, tenantID, monitorID)
	if _, err := alerter.resolveAlert(ctx, alert.ID, now.Add(time.Minute)); err != nil {
		t.Fatalf("resolveAlert() error = %v", err)
	}
	if _, err := dbClient.ExecContext(ctx, `
		INSERT INTO incident_timeline_entries (id, tenant_id, incident_id, entry_type, message, metadata, created_at)
		VALUES ($1, $2, $3, 'internal_note', 'Operator note after recovery', '{}'::jsonb, clock_timestamp())
	`, uuid.New(), tenantID, incidentID); err != nil {
		t.Fatalf("insert non-recovery timeline entry: %v", err)
	}

	tx, err := dbClient.BeginTx(ctx, nil)
	if err != nil {
		t.Fatalf("BeginTx() error = %v", err)
	}
	if err := incidentrecovery.RecordForAlertTx(ctx, tx, alert.ID); err != nil {
		_ = tx.Rollback()
		t.Fatalf("incidentrecovery.RecordForAlertTx() error = %v", err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatalf("Commit() error = %v", err)
	}

	if got := countAlerterTimelineMessages(ctx, t, dbClient, incidentID, "All linked alerts recovered"); got != 1 {
		t.Fatalf("recovery message count after duplicate check = %d, want 1", got)
	}
}

func newIntegrationAlerter(dbClient *shareddb.Client) *Alerter {
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

func countAlerterAlertsByStatus(ctx context.Context, t *testing.T, dbClient *shareddb.Client, monitorID uuid.UUID, status string) int {
	t.Helper()

	var count int
	if err := dbClient.QueryRowContext(ctx, `
		SELECT COUNT(*) FROM alerts WHERE monitor_id = $1 AND status = $2
	`, monitorID, status).Scan(&count); err != nil {
		t.Fatalf("count alerts by status: %v", err)
	}
	return count
}

func countAlerterIncidentAlertLinks(ctx context.Context, t *testing.T, dbClient *shareddb.Client, incidentID uuid.UUID) int {
	t.Helper()

	var count int
	if err := dbClient.QueryRowContext(ctx, `
		SELECT COUNT(*)
		FROM incident_alerts
		WHERE incident_id = $1
	`, incidentID).Scan(&count); err != nil {
		t.Fatalf("count incident alerts: %v", err)
	}
	return count
}

func countAlerterTimelineMessages(ctx context.Context, t *testing.T, dbClient *shareddb.Client, incidentID uuid.UUID, message string) int {
	t.Helper()

	var count int
	if err := dbClient.QueryRowContext(ctx, `
		SELECT COUNT(*)
		FROM incident_timeline_entries
		WHERE incident_id = $1 AND message = $2
	`, incidentID, message).Scan(&count); err != nil {
		t.Fatalf("count incident timeline messages: %v", err)
	}
	return count
}

func setAlerterAlertResolvedTx(ctx context.Context, tx *sql.Tx, alertID uuid.UUID, resolvedAt time.Time) error {
	_, err := tx.ExecContext(ctx, `
		UPDATE alerts
		SET status = 'resolved', resolved_at = $2, updated_at = NOW()
		WHERE id = $1
	`, alertID, resolvedAt)
	return err
}

func assertAlerterIncidentRecoveryLockHeld(ctx context.Context, t *testing.T, dbClient *shareddb.Client, incidentID uuid.UUID) {
	t.Helper()

	lockKey := "incident-recovery:" + incidentID.String()
	var acquired bool
	if err := dbClient.QueryRowContext(ctx, `
		SELECT pg_try_advisory_xact_lock(hashtextextended($1, 0))
	`, lockKey).Scan(&acquired); err != nil {
		t.Fatalf("probe incident recovery lock: %v", err)
	}
	if acquired {
		t.Fatalf("expected incident recovery lock to be held for %s", incidentID)
	}
}

// insertAlerterSecondAlertAndLink inserts an active alert for monitorID and
// links it to an existing incidentID. This bypasses the auto-incident logic so that two concurrent active alerts can exist for
// different monitors but share the same incident — needed for tests that verify
// the advisory-lock serialization in incidentrecovery.RecordForAlertTx.
func insertAlerterSecondAlertAndLink(ctx context.Context, t *testing.T, dbClient *shareddb.Client, tenantID, monitorID, incidentID uuid.UUID, triggeredAt time.Time) *alertRecord {
	t.Helper()

	alertID := uuid.New()
	var record alertRecord
	var lastErr sql.NullString
	if err := dbClient.QueryRowContext(ctx, `
		INSERT INTO alerts (id, tenant_id, monitor_id, status, triggered_at, failure_count, created_at, updated_at)
		VALUES ($1, $2, $3, 'active', $4, 1, $4, $4)
		RETURNING id, tenant_id, monitor_id, status, triggered_at, failure_count, last_error
	`, alertID, tenantID, monitorID, triggeredAt).Scan(
		&record.ID, &record.TenantID, &record.MonitorID,
		&record.Status, &record.TriggeredAt, &record.FailureCount, &lastErr,
	); err != nil {
		t.Fatalf("insert second alert: %v", err)
	}
	if lastErr.Valid {
		record.LastError = &lastErr.String
	}
	if _, err := dbClient.ExecContext(ctx, `
		INSERT INTO incident_alerts (incident_id, alert_id, created_at)
		VALUES ($1, $2, NOW())
		ON CONFLICT DO NOTHING
	`, incidentID, alertID); err != nil {
		t.Fatalf("link second alert to incident: %v", err)
	}
	return &record
}

// fireTestAlert opens an alert the way the lifecycle does: insert the alert
// and, in the same transaction, create or reuse the monitor's auto-incident.
func fireTestAlert(ctx context.Context, a *Alerter, binding policyBinding, failureCount int, now time.Time) (*alertRecord, error) {
	tx, err := a.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()

	record := alertRecord{TenantID: binding.TenantID, MonitorID: binding.MonitorID, FailureCount: failureCount}
	if err := tx.QueryRowContext(ctx, `
		INSERT INTO alerts (id, tenant_id, monitor_id, status, triggered_at, failure_count, created_at, updated_at)
		VALUES ($1, $2, $3, 'active', $4, $5, $4, $4)
		RETURNING id, status, triggered_at
	`, uuid.New(), binding.TenantID, binding.MonitorID, now, failureCount).Scan(&record.ID, &record.Status, &record.TriggeredAt); err != nil {
		return nil, err
	}
	if err := a.ensureAutoIncidentForAlertTx(ctx, tx, binding, &record); err != nil {
		return nil, err
	}
	return &record, tx.Commit()
}

func loadAlerterAutoIncidentID(ctx context.Context, t *testing.T, dbClient *shareddb.Client, tenantID, monitorID uuid.UUID) uuid.UUID {
	t.Helper()

	var incidentID uuid.UUID
	if err := dbClient.QueryRowContext(ctx, `
		SELECT id
		FROM incidents
		WHERE tenant_id = $1 AND is_auto_created = TRUE AND auto_monitor_id = $2
	`, tenantID, monitorID).Scan(&incidentID); err != nil {
		t.Fatalf("load auto incident: %v", err)
	}
	return incidentID
}
