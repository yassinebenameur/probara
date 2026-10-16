package slas

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/yassinebenameur/probara/api/internal/models"
	sharedanalytics "github.com/yassinebenameur/probara/shared/analytics"
	"github.com/yassinebenameur/probara/shared/testutil"
)

// The "Triage pending" bucket follows the relay's eligibility
// (ai.TriageClaimableAlertSQL + ai.TriageSettingsEnabledSQL): an alert with
// no triage row is pending only while open, inside the lookback, on a live
// monitor, in a workspace with AI and auto-triage on, and with neither
// installation kill switch set.
func TestLoadCauseAlertsClaimable(t *testing.T) {
	ctx := context.Background()
	dbClient, cleanup := testutil.SetupPostgresDB(ctx, t)
	t.Cleanup(cleanup)
	now := time.Now().UTC()

	on := testutil.InsertTenant(ctx, t, dbClient, "triage-on")
	off := testutil.InsertTenant(ctx, t, dbClient, "triage-off")
	_, err := dbClient.ExecContext(ctx, `INSERT INTO ai_settings (tenant_id, enabled, auto_triage_enabled) VALUES ($1, TRUE, TRUE), ($2, TRUE, FALSE)`, on, off)
	require.NoError(t, err)

	alert := func(tenant, monitor uuid.UUID, status string, triggered time.Time, resolved *time.Time) {
		t.Helper()
		_, err := dbClient.ExecContext(ctx, `
			INSERT INTO alerts (id, tenant_id, monitor_id, kind, status, triggered_at, resolved_at, failure_count, created_at, updated_at)
			VALUES ($1, $2, $3, 'availability', $4, $5, $6, 1, NOW(), NOW())`,
			uuid.New(), tenant, monitor, status, triggered, resolved)
		require.NoError(t, err)
	}
	want := map[uuid.UUID]string{}
	monitor := func(tenant uuid.UUID, name, kind string) uuid.UUID {
		id := testutil.InsertHTTPMonitor(ctx, t, dbClient, tenant, name)
		want[id] = kind
		return id
	}
	alert(on, monitor(on, "active", models.SLACausePending), "active", now.Add(-time.Hour), nil)
	alert(on, monitor(on, "acked", models.SLACausePending), "acknowledged", now.Add(-time.Hour), nil)
	alert(on, monitor(on, "resolved", models.SLACauseUntriaged), "resolved", now.Add(-time.Hour), at(now.Add(-30*time.Minute)))
	alert(on, monitor(on, "old", models.SLACauseUntriaged), "active", now.Add(-7*time.Hour), nil)
	deleted := monitor(on, "deleted", models.SLACauseUntriaged)
	alert(on, deleted, "active", now.Add(-time.Hour), nil)
	_, err = dbClient.ExecContext(ctx, `UPDATE monitors SET deleted_at = NOW() WHERE id = $1`, deleted)
	require.NoError(t, err)
	settingsOff := testutil.InsertHTTPMonitor(ctx, t, dbClient, off, "settings-off")
	alert(off, settingsOff, "active", now.Add(-time.Hour), nil)

	window := sharedanalytics.TimeRange{Start: now.Add(-24 * time.Hour), End: now}
	causes := func(svc *Service, tenant uuid.UUID, ids []uuid.UUID) map[uuid.UUID]string {
		t.Helper()
		alerts, err := svc.loadCauseAlerts(ctx, tenant, ids, window, now)
		require.NoError(t, err)
		require.Len(t, alerts, len(ids))
		got := map[uuid.UUID]string{}
		for _, a := range alerts {
			got[a.MonitorID] = a.Cause.Kind
		}
		return got
	}
	repo := sharedanalytics.NewRepository(dbClient)
	ids := make([]uuid.UUID, 0, len(want))
	for id := range want {
		ids = append(ids, id)
	}

	require.Equal(t, want, causes(NewService(dbClient, repo, false), on, ids))
	require.Equal(t, map[uuid.UUID]string{settingsOff: models.SLACauseUntriaged},
		causes(NewService(dbClient, repo, false), off, []uuid.UUID{settingsOff}), "auto-triage off in the workspace")

	// Under the installation kill switch nothing is claimable.
	for id, got := range causes(NewService(dbClient, repo, true), on, ids) {
		require.Equal(t, models.SLACauseUntriaged, got, "monitor %s", id)
	}
}
