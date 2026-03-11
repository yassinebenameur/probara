package monitors

import (
	"context"
	"database/sql"
	"testing"
	"time"

	"github.com/google/uuid"
	testcontainers "github.com/testcontainers/testcontainers-go"

	groupservice "github.com/yassinebenameur/probara/api/internal/services/groups"
	shareddb "github.com/yassinebenameur/probara/shared/db"
	"github.com/yassinebenameur/probara/shared/testutil"
)

type noopStatusNotifier struct{}

func (noopStatusNotifier) PublishStatusUpdate(ctx context.Context, monitorID, tenantID uuid.UUID) {}

func TestService_DeleteMonitorHistory_Integration(t *testing.T) {
	testcontainers.SkipIfProviderIsNotHealthy(t)

	ctx := context.Background()
	dbClient, cleanup := testutil.SetupPostgresDB(ctx, t)
	defer cleanup()

	tenantID := testutil.InsertTenant(ctx, t, dbClient, "tenant-a")
	otherTenantID := testutil.InsertTenant(ctx, t, dbClient, "tenant-b")
	monitorID := testutil.InsertHTTPMonitor(ctx, t, dbClient, tenantID, "history-target")
	otherMonitorID := testutil.InsertHTTPMonitor(ctx, t, dbClient, otherTenantID, "history-other")
	statusPageID := testutil.InsertStatusPage(ctx, t, dbClient, tenantID, "history-page", "History Page")
	testutil.AddMonitorToStatusPage(ctx, t, dbClient, statusPageID, monitorID, 1)

	seedMonitorHistory(ctx, t, dbClient, tenantID, monitorID)
	seedMonitorHistory(ctx, t, dbClient, otherTenantID, otherMonitorID)

	svc := NewService(NewPostgresRepository(dbClient))
	svc.ConfigureHistoryDependencies(groupservice.NewService(dbClient), noopStatusNotifier{})

	if err := svc.DeleteMonitorHistory(ctx, tenantID, monitorID); err != nil {
		t.Fatalf("DeleteMonitorHistory() error = %v", err)
	}

	assertTableCount(t, ctx, dbClient, `SELECT COUNT(*) FROM monitors WHERE id = $1`, 1, monitorID)
	assertTableCount(t, ctx, dbClient, `SELECT COUNT(*) FROM status_page_monitors WHERE monitor_id = $1`, 1, monitorID)
	assertTableCount(t, ctx, dbClient, `SELECT COUNT(*) FROM check_results WHERE tenant_id = $1 AND monitor_id = $2`, 0, tenantID, monitorID)
	assertTableCount(t, ctx, dbClient, `SELECT COUNT(*) FROM monitor_daily_rollups WHERE tenant_id = $1 AND monitor_id = $2`, 0, tenantID, monitorID)
	assertTableCount(t, ctx, dbClient, `SELECT COUNT(*) FROM monitor_downtime_periods WHERE tenant_id = $1 AND monitor_id = $2`, 0, tenantID, monitorID)
	assertTableCount(t, ctx, dbClient, `SELECT COUNT(*) FROM monitor_downtime_open WHERE tenant_id = $1 AND monitor_id = $2`, 0, tenantID, monitorID)
	assertTableCount(t, ctx, dbClient, `SELECT COUNT(*) FROM alerts WHERE tenant_id = $1 AND monitor_id = $2`, 0, tenantID, monitorID)
	assertTableCount(t, ctx, dbClient, `SELECT COUNT(*) FROM alert_notification_states`, 1)

	assertTableCount(t, ctx, dbClient, `SELECT COUNT(*) FROM check_results WHERE tenant_id = $1 AND monitor_id = $2`, 1, otherTenantID, otherMonitorID)
	assertTableCount(t, ctx, dbClient, `SELECT COUNT(*) FROM alerts WHERE tenant_id = $1 AND monitor_id = $2`, 1, otherTenantID, otherMonitorID)
}

func TestService_DeleteMonitorHistory_GroupMonitorClearsGroupAndLeafMembers(t *testing.T) {
	testcontainers.SkipIfProviderIsNotHealthy(t)

	ctx := context.Background()
	dbClient, cleanup := testutil.SetupPostgresDB(ctx, t)
	defer cleanup()

	tenantID := testutil.InsertTenant(ctx, t, dbClient, "tenant-group")
	groupID := testutil.InsertGroupMonitor(ctx, t, dbClient, tenantID, "group-target")
	memberA := testutil.InsertHTTPMonitor(ctx, t, dbClient, tenantID, "member-a")
	memberB := testutil.InsertHTTPMonitor(ctx, t, dbClient, tenantID, "member-b")
	unrelated := testutil.InsertHTTPMonitor(ctx, t, dbClient, tenantID, "member-c")

	testutil.AddMonitorToGroup(ctx, t, dbClient, memberA, groupID)
	testutil.AddMonitorToGroup(ctx, t, dbClient, memberB, groupID)

	seedMonitorHistory(ctx, t, dbClient, tenantID, groupID)
	seedMonitorHistory(ctx, t, dbClient, tenantID, memberA)
	seedMonitorHistory(ctx, t, dbClient, tenantID, memberB)
	seedMonitorHistory(ctx, t, dbClient, tenantID, unrelated)

	svc := NewService(NewPostgresRepository(dbClient))
	svc.ConfigureHistoryDependencies(groupservice.NewService(dbClient), noopStatusNotifier{})

	if err := svc.DeleteMonitorHistory(ctx, tenantID, groupID); err != nil {
		t.Fatalf("DeleteMonitorHistory(group) error = %v", err)
	}

	for _, id := range []uuid.UUID{groupID, memberA, memberB} {
		assertTableCount(t, ctx, dbClient, `SELECT COUNT(*) FROM check_results WHERE tenant_id = $1 AND monitor_id = $2`, 0, tenantID, id)
		assertTableCount(t, ctx, dbClient, `SELECT COUNT(*) FROM alerts WHERE tenant_id = $1 AND monitor_id = $2`, 0, tenantID, id)
	}
	assertTableCount(t, ctx, dbClient, `SELECT COUNT(*) FROM check_results WHERE tenant_id = $1 AND monitor_id = $2`, 1, tenantID, unrelated)
	assertTableCount(t, ctx, dbClient, `SELECT COUNT(*) FROM alerts WHERE tenant_id = $1 AND monitor_id = $2`, 1, tenantID, unrelated)
	assertTableCount(t, ctx, dbClient, `SELECT COUNT(*) FROM monitors WHERE id = $1`, 1, groupID)
}

func seedMonitorHistory(ctx context.Context, t *testing.T, db *shareddb.Client, tenantID, monitorID uuid.UUID) {
	t.Helper()

	now := time.Now().UTC().Truncate(time.Minute)
	testutil.InsertCheckResult(ctx, t, db, tenantID, monitorID, now.Add(-30*time.Minute), "success", "monitor", testutil.IntPtr(120))
	testutil.InsertDailyRollup(ctx, t, db, tenantID, monitorID, now.Add(-24*time.Hour), 10, 9, 1080, 9, "success", now.Add(-5*time.Minute))
	testutil.InsertDowntimePeriod(ctx, t, db, tenantID, monitorID, now.Add(-2*time.Hour), now.Add(-90*time.Minute))
	testutil.InsertOpenDowntime(ctx, t, db, tenantID, monitorID, now.Add(-15*time.Minute), now.Add(-5*time.Minute))

	policyID := uuid.New()
	alertID := uuid.New()
	channelID := uuid.New()
	if _, err := db.ExecContext(ctx, `
		INSERT INTO alert_policies (id, tenant_id, name, description, failure_threshold, failure_window_seconds, created_at, updated_at)
		VALUES ($1, $2, $3, NULL, 3, 300, NOW(), NOW())
	`, policyID, tenantID, "policy-"+monitorID.String()); err != nil {
		t.Fatalf("insert alert policy: %v", err)
	}
	if _, err := db.ExecContext(ctx, `
		INSERT INTO alert_channels (id, tenant_id, name, type, config, is_active, created_at, updated_at)
		VALUES ($1, $2, $3, 'email', '{"to":["ops@example.com"]}'::jsonb, TRUE, NOW(), NOW())
	`, channelID, tenantID, "channel-"+channelID.String()); err != nil {
		t.Fatalf("insert alert channel: %v", err)
	}
	if _, err := db.ExecContext(ctx, `
		INSERT INTO alerts (id, tenant_id, monitor_id, alert_policy_id, status, triggered_at, failure_count, created_at, updated_at)
		VALUES ($1, $2, $3, $4, 'active', NOW(), 3, NOW(), NOW())
	`, alertID, tenantID, monitorID, policyID); err != nil {
		t.Fatalf("insert alert: %v", err)
	}
	if _, err := db.ExecContext(ctx, `
		INSERT INTO alert_notification_states (alert_id, channel_id, last_sent_at, last_event_type, created_at, updated_at)
		VALUES ($1, $2, NOW(), 'created', NOW(), NOW())
	`, alertID, channelID); err != nil {
		t.Fatalf("insert alert notification state: %v", err)
	}
}

func assertTableCount(t *testing.T, ctx context.Context, db interface {
	QueryRowContext(ctx context.Context, query string, args ...interface{}) *sql.Row
}, query string, want int, args ...interface{}) {
	t.Helper()

	var got int
	if err := db.QueryRowContext(ctx, query, args...).Scan(&got); err != nil {
		t.Fatalf("count query failed: %v", err)
	}
	if got != want {
		t.Fatalf("count for %q = %d, want %d", query, got, want)
	}
}
