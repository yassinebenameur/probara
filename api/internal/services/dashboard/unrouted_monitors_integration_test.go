package dashboard

import (
	"context"
	"testing"

	"github.com/google/uuid"
	testcontainers "github.com/testcontainers/testcontainers-go"

	"github.com/yassinebenameur/probara/api/internal/models"
	sharedanalytics "github.com/yassinebenameur/probara/shared/analytics"
	"github.com/yassinebenameur/probara/shared/testutil"
)

// TestOpsSummaryCountsUnroutedMonitors pins the dashboard warning: it counts
// active monitors whose alerts would notify nobody, and nothing else. Paused
// monitors never alert, so they are not counted however they are routed.
func TestOpsSummaryCountsUnroutedMonitors(t *testing.T) {
	testcontainers.SkipIfProviderIsNotHealthy(t)

	ctx := context.Background()
	dbClient, cleanup := testutil.SetupPostgresDB(ctx, t)
	defer cleanup()

	svc := NewService(dbClient, nil, sharedanalytics.NewRepository(dbClient), &fakeTenantSettingsReader{}, nil)
	tenantID := testutil.InsertTenant(ctx, t, dbClient, "unrouted")

	channelID := uuid.New()
	if _, err := dbClient.ExecContext(ctx, `
		INSERT INTO alert_channels (id, tenant_id, name, type, config, is_active, created_at, updated_at)
		VALUES ($1, $2, 'ops-email', 'email', '{"to":["ops@example.com"]}'::jsonb, TRUE, NOW(), NOW())
	`, channelID, tenantID); err != nil {
		t.Fatalf("insert alert channel: %v", err)
	}

	setCustom := func(monitorID uuid.UUID, channels ...uuid.UUID) {
		t.Helper()
		if _, err := dbClient.ExecContext(ctx,
			`UPDATE monitors SET notification_mode = 'custom' WHERE id = $1`, monitorID); err != nil {
			t.Fatalf("set custom mode: %v", err)
		}
		for _, ch := range channels {
			if _, err := dbClient.ExecContext(ctx, `
				INSERT INTO monitor_channels (monitor_id, channel_id, delay_seconds) VALUES ($1, $2, 0)
			`, monitorID, ch); err != nil {
				t.Fatalf("assign channel: %v", err)
			}
		}
	}

	// Two unrouted: custom mode with nothing assigned, and default mode while
	// the workspace has no default routes yet.
	setCustom(testutil.InsertHTTPMonitor(ctx, t, dbClient, tenantID, "custom-empty"))
	testutil.InsertHTTPMonitor(ctx, t, dbClient, tenantID, "default-no-routes")
	// Routed, so not counted.
	setCustom(testutil.InsertHTTPMonitor(ctx, t, dbClient, tenantID, "custom-routed"), channelID)
	// Paused and unrouted: never alerts at all, so not counted.
	paused := testutil.InsertHTTPMonitor(ctx, t, dbClient, tenantID, "paused-unrouted")
	setCustom(paused)
	if _, err := dbClient.ExecContext(ctx, `UPDATE monitors SET enabled = FALSE WHERE id = $1`, paused); err != nil {
		t.Fatalf("pause monitor: %v", err)
	}

	if got := unroutedCount(ctx, t, svc, tenantID); got != 2 {
		t.Fatalf("UnroutedMonitors = %d, want 2 (custom-empty + default-no-routes)", got)
	}

	// A workspace default route fixes every 'default' mode monitor at once.
	if _, err := dbClient.ExecContext(ctx, `
		INSERT INTO tenant_default_channels (tenant_id, channel_id, delay_seconds, position)
		VALUES ($1, $2, 0, 0)
	`, tenantID, channelID); err != nil {
		t.Fatalf("insert tenant default channel: %v", err)
	}
	if got := unroutedCount(ctx, t, svc, tenantID); got != 1 {
		t.Fatalf("UnroutedMonitors = %d, want 1 (custom-empty only)", got)
	}

	// Deactivating the only channel silences everything routed through it —
	// the assignment still exists, so only reachability catches this.
	if _, err := dbClient.ExecContext(ctx,
		`UPDATE alert_channels SET is_active = FALSE WHERE id = $1`, channelID); err != nil {
		t.Fatalf("deactivate channel: %v", err)
	}
	if got := unroutedCount(ctx, t, svc, tenantID); got != 3 {
		t.Fatalf("UnroutedMonitors = %d, want 3 (every active monitor, channel disabled)", got)
	}
}

func unroutedCount(ctx context.Context, t *testing.T, svc *Service, tenantID uuid.UUID) int {
	t.Helper()
	summary, err := svc.GetSummary(ctx, tenantID, &models.DashboardOverviewQuery{Range: models.DashboardRange24h})
	if err != nil {
		t.Fatalf("GetSummary error = %v", err)
	}
	return summary.OpsSummary.UnroutedMonitors
}
