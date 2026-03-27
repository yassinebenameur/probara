package alerts

import (
	"context"
	"database/sql"
	"errors"
	"testing"

	"github.com/google/uuid"

	"github.com/yassinebenameur/probara/api/internal/models"
	shareddb "github.com/yassinebenameur/probara/shared/db"
	"github.com/yassinebenameur/probara/shared/testutil"
)

func TestServiceCreateAlertInvokesIncidentAutomation(t *testing.T) {
	ctx := context.Background()
	dbClient, cleanup := testutil.SetupPostgresDB(ctx, t)
	defer cleanup()

	tenantID := testutil.InsertTenant(ctx, t, dbClient, "alerts")
	monitorID := testutil.InsertHTTPMonitor(ctx, t, dbClient, tenantID, "API")
	policyID := insertAlertServiceTestPolicy(ctx, t, dbClient, tenantID, true)
	automation := &fakeIncidentAutomation{}
	automation.ensureHook = func(_ context.Context, tx *sql.Tx, _ uuid.UUID, alert *models.AlertWithDetails) error {
		if tx == nil {
			t.Fatalf("expected non-nil tx for ensure hook")
		}
		var count int
		if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM alerts WHERE id = $1`, alert.ID).Scan(&count); err != nil {
			t.Fatalf("count alert in ensure tx: %v", err)
		}
		if count != 1 {
			t.Fatalf("alert visibility in ensure tx = %d, want 1", count)
		}
		return nil
	}
	svc := NewService(dbClient, automation)

	lastError := "timeout"
	alert, err := svc.CreateAlert(ctx, tenantID, monitorID, policyID, 3, &lastError)
	if err != nil {
		t.Fatalf("CreateAlert() error = %v", err)
	}

	if automation.ensureCalls != 1 {
		t.Fatalf("ensure calls = %d, want 1", automation.ensureCalls)
	}
	if automation.lastEnsureAlert == nil {
		t.Fatalf("expected ensured alert")
	}
	if automation.lastEnsureAlert.ID != alert.ID {
		t.Fatalf("ensured alert id = %s, want %s", automation.lastEnsureAlert.ID, alert.ID)
	}
	if !automation.sawEnsureTx {
		t.Fatalf("expected ensure hook to receive transaction")
	}
}

func TestServiceResolveAlertInvokesIncidentRecoveryAutomation(t *testing.T) {
	ctx := context.Background()
	dbClient, cleanup := testutil.SetupPostgresDB(ctx, t)
	defer cleanup()

	tenantID := testutil.InsertTenant(ctx, t, dbClient, "alerts")
	monitorID := testutil.InsertHTTPMonitor(ctx, t, dbClient, tenantID, "API")
	policyID := insertAlertServiceTestPolicy(ctx, t, dbClient, tenantID, true)
	alertID := uuid.New()
	if _, err := dbClient.ExecContext(ctx, `
		INSERT INTO alerts (id, tenant_id, monitor_id, alert_policy_id, status, triggered_at, failure_count, created_at, updated_at)
		VALUES ($1, $2, $3, $4, 'active', NOW(), 2, NOW(), NOW())
	`, alertID, tenantID, monitorID, policyID); err != nil {
		t.Fatalf("insert alert: %v", err)
	}

	automation := &fakeIncidentAutomation{}
	automation.recoveryHook = func(_ context.Context, tx *sql.Tx, _ uuid.UUID, alertID uuid.UUID) error {
		if tx == nil {
			t.Fatalf("expected non-nil tx for recovery hook")
		}
		var status string
		if err := tx.QueryRowContext(ctx, `SELECT status FROM alerts WHERE id = $1`, alertID).Scan(&status); err != nil {
			t.Fatalf("load alert status in recovery tx: %v", err)
		}
		if status != "resolved" {
			t.Fatalf("alert status in recovery tx = %q, want resolved", status)
		}
		return nil
	}
	svc := NewService(dbClient, automation)

	if _, err := svc.ResolveAlert(ctx, tenantID, alertID); err != nil {
		t.Fatalf("ResolveAlert() error = %v", err)
	}

	if automation.recoveryCalls != 1 {
		t.Fatalf("recovery calls = %d, want 1", automation.recoveryCalls)
	}
	if automation.lastRecoveryTenantID != tenantID {
		t.Fatalf("recovery tenant_id = %s, want %s", automation.lastRecoveryTenantID, tenantID)
	}
	if automation.lastRecoveryAlertID != alertID {
		t.Fatalf("recovery alert_id = %s, want %s", automation.lastRecoveryAlertID, alertID)
	}
	if !automation.sawRecoveryTx {
		t.Fatalf("expected recovery hook to receive transaction")
	}
}

func TestServiceCreateAlertRollsBackWhenIncidentAutomationFails(t *testing.T) {
	ctx := context.Background()
	dbClient, cleanup := testutil.SetupPostgresDB(ctx, t)
	defer cleanup()

	tenantID := testutil.InsertTenant(ctx, t, dbClient, "alerts")
	monitorID := testutil.InsertHTTPMonitor(ctx, t, dbClient, tenantID, "API")
	policyID := insertAlertServiceTestPolicy(ctx, t, dbClient, tenantID, true)
	automation := &fakeIncidentAutomation{ensureErr: errors.New("ensure failed")}
	svc := NewService(dbClient, automation)

	lastError := "timeout"
	if _, err := svc.CreateAlert(ctx, tenantID, monitorID, policyID, 3, &lastError); err == nil {
		t.Fatalf("expected CreateAlert() error")
	}

	if count := countAlertsForMonitor(ctx, t, dbClient, tenantID, monitorID); count != 0 {
		t.Fatalf("alert count after failed create = %d, want 0", count)
	}
}

func TestServiceResolveAlertRollsBackWhenIncidentAutomationFails(t *testing.T) {
	ctx := context.Background()
	dbClient, cleanup := testutil.SetupPostgresDB(ctx, t)
	defer cleanup()

	tenantID := testutil.InsertTenant(ctx, t, dbClient, "alerts")
	monitorID := testutil.InsertHTTPMonitor(ctx, t, dbClient, tenantID, "API")
	policyID := insertAlertServiceTestPolicy(ctx, t, dbClient, tenantID, true)
	alertID := uuid.New()
	if _, err := dbClient.ExecContext(ctx, `
		INSERT INTO alerts (id, tenant_id, monitor_id, alert_policy_id, status, triggered_at, failure_count, created_at, updated_at)
		VALUES ($1, $2, $3, $4, 'active', NOW(), 2, NOW(), NOW())
	`, alertID, tenantID, monitorID, policyID); err != nil {
		t.Fatalf("insert alert: %v", err)
	}

	automation := &fakeIncidentAutomation{recoveryErr: errors.New("recovery failed")}
	svc := NewService(dbClient, automation)

	if _, err := svc.ResolveAlert(ctx, tenantID, alertID); err == nil {
		t.Fatalf("expected ResolveAlert() error")
	}

	if status := loadAlertStatus(ctx, t, dbClient, alertID); status != "active" {
		t.Fatalf("alert status after failed resolve = %q, want active", status)
	}
}

func insertAlertServiceTestPolicy(ctx context.Context, t *testing.T, dbClient *shareddb.Client, tenantID uuid.UUID, createIncidentOnFire bool) uuid.UUID {
	t.Helper()

	policyID := uuid.New()
	if _, err := dbClient.ExecContext(ctx, `
		INSERT INTO alert_policies (id, tenant_id, name, failure_threshold, failure_window_seconds, create_incident_on_fire, created_at, updated_at)
		VALUES ($1, $2, 'policy', 1, 60, $3, NOW(), NOW())
	`, policyID, tenantID, createIncidentOnFire); err != nil {
		t.Fatalf("insert alert policy: %v", err)
	}
	return policyID
}

type fakeIncidentAutomation struct {
	ensureCalls          int
	recoveryCalls        int
	lastEnsureAlert      *models.AlertWithDetails
	lastRecoveryTenantID uuid.UUID
	lastRecoveryAlertID  uuid.UUID
	sawEnsureTx          bool
	sawRecoveryTx        bool
	ensureErr            error
	recoveryErr          error
	ensureHook           func(context.Context, *sql.Tx, uuid.UUID, *models.AlertWithDetails) error
	recoveryHook         func(context.Context, *sql.Tx, uuid.UUID, uuid.UUID) error
}

func (f *fakeIncidentAutomation) EnsureIncidentForAlertTx(ctx context.Context, tx *sql.Tx, tenantID uuid.UUID, alert *models.AlertWithDetails) error {
	f.ensureCalls++
	f.lastEnsureAlert = alert
	f.sawEnsureTx = tx != nil
	if f.ensureHook != nil {
		if err := f.ensureHook(ctx, tx, tenantID, alert); err != nil {
			return err
		}
	}
	if f.ensureErr != nil {
		return f.ensureErr
	}
	return nil
}

func (f *fakeIncidentAutomation) RecordAlertRecoveryIfNeededTx(ctx context.Context, tx *sql.Tx, tenantID, alertID uuid.UUID) error {
	f.recoveryCalls++
	f.lastRecoveryTenantID = tenantID
	f.lastRecoveryAlertID = alertID
	f.sawRecoveryTx = tx != nil
	if f.recoveryHook != nil {
		if err := f.recoveryHook(ctx, tx, tenantID, alertID); err != nil {
			return err
		}
	}
	if f.recoveryErr != nil {
		return f.recoveryErr
	}
	return nil
}

func countAlertsForMonitor(ctx context.Context, t *testing.T, dbClient *shareddb.Client, tenantID, monitorID uuid.UUID) int {
	t.Helper()

	var count int
	if err := dbClient.QueryRowContext(ctx, `
		SELECT COUNT(*)
		FROM alerts
		WHERE tenant_id = $1 AND monitor_id = $2
	`, tenantID, monitorID).Scan(&count); err != nil {
		t.Fatalf("count alerts: %v", err)
	}

	return count
}

func loadAlertStatus(ctx context.Context, t *testing.T, dbClient *shareddb.Client, alertID uuid.UUID) string {
	t.Helper()

	var status string
	if err := dbClient.QueryRowContext(ctx, `
		SELECT status
		FROM alerts
		WHERE id = $1
	`, alertID).Scan(&status); err != nil {
		t.Fatalf("load alert status: %v", err)
	}

	return status
}
