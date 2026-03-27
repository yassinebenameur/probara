package alerter

import (
	"context"
	"database/sql"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/yassinebenameur/probara/shared/config"
	shareddb "github.com/yassinebenameur/probara/shared/db"
	"github.com/yassinebenameur/probara/shared/testutil"
)

func TestAlerterCreateAlertCreatesAndReusesAutoIncident(t *testing.T) {
	ctx := context.Background()
	dbClient, cleanup := testutil.SetupPostgresDB(ctx, t)
	defer cleanup()

	tenantID := testutil.InsertTenant(ctx, t, dbClient, "alerter")
	monitorID := testutil.InsertHTTPMonitor(ctx, t, dbClient, tenantID, "API")
	policyID := insertAlerterTestPolicy(ctx, t, dbClient, tenantID, "policy", true)
	now := time.Now().UTC()
	alerter := newIntegrationAlerter(dbClient)

	binding := policyBinding{
		MonitorID:            monitorID,
		TenantID:             tenantID,
		MonitorName:          "API",
		PolicyID:             policyID,
		PolicyName:           "policy",
		FailureThreshold:     1,
		FailureWindowSeconds: 60,
		CreateIncidentOnFire: true,
	}

	firstAlert, err := alerter.createAlert(ctx, binding, 2, nil, now)
	if err != nil {
		t.Fatalf("createAlert(first) error = %v", err)
	}
	incidentID := loadAlerterAutoIncidentID(ctx, t, dbClient, tenantID, monitorID, policyID)
	if count := countAlerterIncidentAlertLinks(ctx, t, dbClient, incidentID); count != 1 {
		t.Fatalf("incident alert count after first create = %d, want 1", count)
	}

	secondAlert, err := alerter.createAlert(ctx, binding, 3, nil, now.Add(time.Minute))
	if err != nil {
		t.Fatalf("createAlert(second) error = %v", err)
	}
	reusedIncidentID := loadAlerterAutoIncidentID(ctx, t, dbClient, tenantID, monitorID, policyID)
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
	ctx := context.Background()
	dbClient, cleanup := testutil.SetupPostgresDB(ctx, t)
	defer cleanup()

	tenantID := testutil.InsertTenant(ctx, t, dbClient, "alerter")
	monitorID := testutil.InsertHTTPMonitor(ctx, t, dbClient, tenantID, "API")
	policyID := insertAlerterTestPolicy(ctx, t, dbClient, tenantID, "policy", true)
	now := time.Now().UTC()
	alerter := newIntegrationAlerter(dbClient)

	binding := policyBinding{
		MonitorID:            monitorID,
		TenantID:             tenantID,
		MonitorName:          "API",
		PolicyID:             policyID,
		PolicyName:           "policy",
		FailureThreshold:     1,
		FailureWindowSeconds: 60,
		CreateIncidentOnFire: true,
	}

	firstAlert, err := alerter.createAlert(ctx, binding, 2, nil, now)
	if err != nil {
		t.Fatalf("createAlert(first) error = %v", err)
	}
	secondAlert, err := alerter.createAlert(ctx, binding, 3, nil, now.Add(time.Minute))
	if err != nil {
		t.Fatalf("createAlert(second) error = %v", err)
	}

	incidentID := loadAlerterAutoIncidentID(ctx, t, dbClient, tenantID, monitorID, policyID)
	if _, err := alerter.resolveAlert(ctx, firstAlert.ID, now.Add(2*time.Minute)); err != nil {
		t.Fatalf("resolveAlert(first) error = %v", err)
	}
	if got := countAlerterTimelineMessages(ctx, t, dbClient, incidentID, "All linked alerts recovered"); got != 0 {
		t.Fatalf("recovery message count after first resolve = %d, want 0", got)
	}

	if _, err := alerter.resolveAlert(ctx, secondAlert.ID, now.Add(3*time.Minute)); err != nil {
		t.Fatalf("resolveAlert(second) error = %v", err)
	}
	if got := countAlerterTimelineMessages(ctx, t, dbClient, incidentID, "All linked alerts recovered"); got != 1 {
		t.Fatalf("recovery message count after second resolve = %d, want 1", got)
	}
}

func TestAlerterResolveAlertSerializesConcurrentFinalResolutions(t *testing.T) {
	ctx := context.Background()
	dbClient, cleanup := testutil.SetupPostgresDB(ctx, t)
	defer cleanup()

	tenantID := testutil.InsertTenant(ctx, t, dbClient, "alerter")
	monitorID := testutil.InsertHTTPMonitor(ctx, t, dbClient, tenantID, "API")
	policyID := insertAlerterTestPolicy(ctx, t, dbClient, tenantID, "policy", true)
	now := time.Now().UTC()
	alerter := newIntegrationAlerter(dbClient)

	binding := policyBinding{
		MonitorID:            monitorID,
		TenantID:             tenantID,
		MonitorName:          "API",
		PolicyID:             policyID,
		PolicyName:           "policy",
		FailureThreshold:     1,
		FailureWindowSeconds: 60,
		CreateIncidentOnFire: true,
	}

	firstAlert, err := alerter.createAlert(ctx, binding, 2, nil, now)
	if err != nil {
		t.Fatalf("createAlert(first) error = %v", err)
	}
	secondAlert, err := alerter.createAlert(ctx, binding, 3, nil, now.Add(time.Minute))
	if err != nil {
		t.Fatalf("createAlert(second) error = %v", err)
	}

	incidentID := loadAlerterAutoIncidentID(ctx, t, dbClient, tenantID, monitorID, policyID)

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

	if err := alerter.recordAlertRecoveryIfNeededTx(ctx, tx1, firstAlert.ID); err != nil {
		t.Fatalf("recordAlertRecoveryIfNeededTx(tx1) error = %v", err)
	}
	assertAlerterIncidentRecoveryLockHeld(ctx, t, dbClient, incidentID)

	errCh := make(chan error, 1)
	go func() {
		if err := alerter.recordAlertRecoveryIfNeededTx(ctx, tx2, secondAlert.ID); err != nil {
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
	policyID := insertAlerterTestPolicy(ctx, t, dbClient, tenantID, "policy", true)
	now := time.Now().UTC()
	alerter := newIntegrationAlerter(dbClient)

	binding := policyBinding{
		MonitorID:            monitorID,
		TenantID:             tenantID,
		MonitorName:          "API",
		PolicyID:             policyID,
		PolicyName:           "policy",
		FailureThreshold:     1,
		FailureWindowSeconds: 60,
		CreateIncidentOnFire: true,
	}

	alert, err := alerter.createAlert(ctx, binding, 2, nil, now)
	if err != nil {
		t.Fatalf("createAlert() error = %v", err)
	}
	incidentID := loadAlerterAutoIncidentID(ctx, t, dbClient, tenantID, monitorID, policyID)
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
	if err := alerter.recordAlertRecoveryIfNeededTx(ctx, tx, alert.ID); err != nil {
		_ = tx.Rollback()
		t.Fatalf("recordAlertRecoveryIfNeededTx() error = %v", err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatalf("Commit() error = %v", err)
	}

	if got := countAlerterTimelineMessages(ctx, t, dbClient, incidentID, "All linked alerts recovered"); got != 1 {
		t.Fatalf("recovery message count after duplicate check = %d, want 1", got)
	}
}

func newIntegrationAlerter(dbClient *shareddb.Client) *Alerter {
	return &Alerter{
		config: &config.AlerterConfig{},
		db:     dbClient,
	}
}

func insertAlerterTestPolicy(ctx context.Context, t *testing.T, dbClient *shareddb.Client, tenantID uuid.UUID, name string, createIncidentOnFire bool) uuid.UUID {
	t.Helper()

	policyID := uuid.New()
	if _, err := dbClient.ExecContext(ctx, `
		INSERT INTO alert_policies (id, tenant_id, name, failure_threshold, failure_window_seconds, create_incident_on_fire, created_at, updated_at)
		VALUES ($1, $2, $3, 1, 60, $4, NOW(), NOW())
	`, policyID, tenantID, name, createIncidentOnFire); err != nil {
		t.Fatalf("insert alert policy: %v", err)
	}

	return policyID
}

func loadAlerterAutoIncidentID(ctx context.Context, t *testing.T, dbClient *shareddb.Client, tenantID, monitorID, policyID uuid.UUID) uuid.UUID {
	t.Helper()

	var incidentID uuid.UUID
	if err := dbClient.QueryRowContext(ctx, `
		SELECT id
		FROM incidents
		WHERE tenant_id = $1 AND is_auto_created = TRUE AND auto_monitor_id = $2 AND auto_alert_policy_id = $3
	`, tenantID, monitorID, policyID).Scan(&incidentID); err != nil {
		t.Fatalf("load auto incident: %v", err)
	}
	return incidentID
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
