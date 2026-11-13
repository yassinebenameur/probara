//go:build !unit

package alerts

import (
	"context"
	"database/sql"
	"errors"
	"testing"

	"github.com/google/uuid"

	shareddb "github.com/yassinebenameur/probara/shared/db"
	"github.com/yassinebenameur/probara/shared/testutil"
)

func TestServiceResolveAlertInvokesIncidentRecoveryAutomation(t *testing.T) {
	ctx := context.Background()
	dbClient, cleanup := testutil.SetupPostgresDB(ctx, t)
	defer cleanup()

	tenantID := testutil.InsertTenant(ctx, t, dbClient, "alerts")
	monitorID := testutil.InsertHTTPMonitor(ctx, t, dbClient, tenantID, "API")
	alertID := uuid.New()
	if _, err := dbClient.ExecContext(ctx, `
		INSERT INTO alerts (id, tenant_id, monitor_id, status, triggered_at, failure_count, created_at, updated_at)
		VALUES ($1, $2, $3, 'active', NOW(), 2, NOW(), NOW())
	`, alertID, tenantID, monitorID); err != nil {
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

func TestServiceResolveAlertRollsBackWhenIncidentAutomationFails(t *testing.T) {
	ctx := context.Background()
	dbClient, cleanup := testutil.SetupPostgresDB(ctx, t)
	defer cleanup()

	tenantID := testutil.InsertTenant(ctx, t, dbClient, "alerts")
	monitorID := testutil.InsertHTTPMonitor(ctx, t, dbClient, tenantID, "API")
	alertID := uuid.New()
	if _, err := dbClient.ExecContext(ctx, `
		INSERT INTO alerts (id, tenant_id, monitor_id, status, triggered_at, failure_count, created_at, updated_at)
		VALUES ($1, $2, $3, 'active', NOW(), 2, NOW(), NOW())
	`, alertID, tenantID, monitorID); err != nil {
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

type fakeIncidentAutomation struct {
	recoveryCalls        int
	lastRecoveryTenantID uuid.UUID
	lastRecoveryAlertID  uuid.UUID
	sawRecoveryTx        bool
	recoveryErr          error
	recoveryHook         func(context.Context, *sql.Tx, uuid.UUID, uuid.UUID) error
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
