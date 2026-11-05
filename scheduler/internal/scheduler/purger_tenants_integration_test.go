//go:build !unit

package scheduler

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	shareddb "github.com/yassinebenameur/probara/shared/db"
	"github.com/yassinebenameur/probara/shared/testutil"
)

// tenantFixture seeds one workspace with rows in every table the tenant phase
// drains, n rows each where the table is unbounded in practice. With
// tombstone, one of its two monitors is already tombstoned.
func tenantFixture(ctx context.Context, t *testing.T, dbClient *shareddb.Client, n int, tombstone bool) (tenantID, liveMonitor uuid.UUID) {
	t.Helper()
	exec := func(query string, args ...any) {
		t.Helper()
		_, err := dbClient.ExecContext(ctx, query, args...)
		require.NoError(t, err, query)
	}

	tenantID = testutil.InsertTenant(ctx, t, dbClient, "")
	tombstoned := testutil.InsertHTTPMonitor(ctx, t, dbClient, tenantID, "")
	liveMonitor = testutil.InsertHTTPMonitor(ctx, t, dbClient, tenantID, "")
	for _, m := range []uuid.UUID{tombstoned, liveMonitor} {
		for i := 0; i < n; i++ {
			exec(`INSERT INTO check_results (id, tenant_id, monitor_id, job_id, status, latency_ms, created_at)
			      VALUES (gen_random_uuid(), $1, $2, gen_random_uuid(), 'success', 12, NOW())`, tenantID, m)
		}
	}
	if tombstone {
		exec(`UPDATE monitors SET deleted_at = NOW() WHERE id = $1`, tombstoned)
	}

	src, dst := uuid.New(), uuid.New()
	for _, loc := range []uuid.UUID{src, dst} {
		exec(`INSERT INTO locations (id, tenant_id, name, slug) VALUES ($1, $2, $3, $3)`, loc, tenantID, "loc-"+loc.String()[:8])
	}
	for i := 0; i < n; i++ {
		exec(`INSERT INTO mesh_probe_results (id, tenant_id, source_location_id, target_location_id, job_id, status)
		      VALUES (gen_random_uuid(), $1, $2, $3, gen_random_uuid(), 'success')`, tenantID, src, dst)
		exec(`INSERT INTO alerts (id, tenant_id, kind, source_location_id, target_location_id, triggered_at, failure_count, status)
		      VALUES (gen_random_uuid(), $1, 'mesh_edge', $2, $3, NOW(), 1, 'resolved')`, tenantID, src, dst)
	}

	incident := uuid.New()
	exec(`INSERT INTO incidents (id, tenant_id, title) VALUES ($1, $2, 'outage')`, incident, tenantID)
	for i := 0; i < n; i++ {
		exec(`INSERT INTO incident_timeline_entries (tenant_id, incident_id, entry_type, message)
		      VALUES ($1, $2, 'internal_note', 'note')`, tenantID, incident)
	}

	page := testutil.InsertStatusPage(ctx, t, dbClient, tenantID, "page-"+tenantID.String()[:8], "Status")
	for i := 0; i < n; i++ {
		exec(`INSERT INTO status_page_push_subscriptions (status_page_id, endpoint, p256dh, auth)
		      VALUES ($1, $2, 'key', 'auth')`, page, fmt.Sprintf("https://push.example.com/%s/%d", tenantID, i))
	}

	integration, service := uuid.New(), uuid.New()
	exec(`INSERT INTO discovery_integrations (id, tenant_id, name, namespaces, home_namespace, token_hash)
	      VALUES ($1, $2, 'cluster', ARRAY['default'], 'default', 'digest')`, integration, tenantID)
	exec(`INSERT INTO discovered_services (id, tenant_id, integration_id, service_key, name, namespace)
	      VALUES ($1, $2, $3, 'svc', 'svc', 'default')`, service, tenantID, integration)
	for i := 0; i < n; i++ {
		exec(`INSERT INTO discovered_resources (integration_id, uid, service_id, kind, name, namespace)
		      VALUES ($1, $2, $3, 'Pod', $2, 'default')`, integration, fmt.Sprintf("pod-%d", i), service)
	}
	exec(`INSERT INTO discovery_monitor_links (tenant_id, service_id, monitor_id) VALUES ($1, $2, $3)`, tenantID, service, liveMonitor)

	// An escalation policy referenced by NO ACTION FKs from both a monitor
	// and the tenant row, a monitor's config history and an SLA report
	// schedule: each must cascade without blocking the purge.
	policy, sla := uuid.New(), uuid.New()
	exec(`INSERT INTO escalation_policies (id, tenant_id, name) VALUES ($1, $2, 'primary')`, policy, tenantID)
	exec(`UPDATE monitors SET notification_mode = 'policy', escalation_policy_id = $1 WHERE id = $2`, policy, liveMonitor)
	exec(`UPDATE tenants SET default_escalation_policy_id = $1 WHERE id = $2`, policy, tenantID)
	for i := 1; i <= n; i++ {
		exec(`INSERT INTO monitor_config_versions (tenant_id, monitor_id, version, snapshot, snapshot_hash, actor_type, source)
		      VALUES ($1, $2, $3, '{}', 'hash', 'system', 'api')`, tenantID, liveMonitor, i)
	}
	exec(`INSERT INTO slas (id, tenant_id, name, target_pct) VALUES ($1, $2, 'core', 99.9)`, sla, tenantID)
	exec(`INSERT INTO sla_report_schedules (tenant_id, sla_id, recipients, next_run_at, unsubscribe_key)
	      VALUES ($1, $2, ARRAY['ops@example.com'], NOW(), '\x00')`, tenantID, sla)
	return tenantID, liveMonitor
}

func tenantRowCounts(ctx context.Context, t *testing.T, dbClient *shareddb.Client, tenantID uuid.UUID) map[string]int {
	t.Helper()
	queries := map[string]string{
		"tenants":                        `SELECT COUNT(*) FROM tenants WHERE id = $1`,
		"monitors":                       `SELECT COUNT(*) FROM monitors WHERE tenant_id = $1`,
		"check_results":                  `SELECT COUNT(*) FROM check_results WHERE tenant_id = $1`,
		"alerts":                         `SELECT COUNT(*) FROM alerts WHERE tenant_id = $1`,
		"mesh_probe_results":             `SELECT COUNT(*) FROM mesh_probe_results WHERE tenant_id = $1`,
		"locations":                      `SELECT COUNT(*) FROM locations WHERE tenant_id = $1`,
		"incidents":                      `SELECT COUNT(*) FROM incidents WHERE tenant_id = $1`,
		"incident_timeline_entries":      `SELECT COUNT(*) FROM incident_timeline_entries WHERE tenant_id = $1`,
		"status_pages":                   `SELECT COUNT(*) FROM status_pages WHERE tenant_id = $1`,
		"status_page_push_subscriptions": `SELECT COUNT(*) FROM status_page_push_subscriptions s JOIN status_pages p ON p.id = s.status_page_id WHERE p.tenant_id = $1`,
		"discovery_integrations":         `SELECT COUNT(*) FROM discovery_integrations WHERE tenant_id = $1`,
		"discovered_services":            `SELECT COUNT(*) FROM discovered_services WHERE tenant_id = $1`,
		"discovered_resources":           `SELECT COUNT(*) FROM discovered_resources r JOIN discovered_services s ON s.id = r.service_id WHERE s.tenant_id = $1`,
		"discovery_monitor_links":        `SELECT COUNT(*) FROM discovery_monitor_links WHERE tenant_id = $1`,
		"escalation_policies":            `SELECT COUNT(*) FROM escalation_policies WHERE tenant_id = $1`,
		"monitor_config_versions":        `SELECT COUNT(*) FROM monitor_config_versions WHERE tenant_id = $1`,
		"sla_report_schedules":           `SELECT COUNT(*) FROM sla_report_schedules WHERE tenant_id = $1`,
	}
	counts := make(map[string]int, len(queries))
	for table, query := range queries {
		var n int
		require.NoError(t, dbClient.QueryRowContext(ctx, query, tenantID).Scan(&n), table)
		counts[table] = n
	}
	return counts
}

func TestPurger_DeletedTenantPurgedInBatches(t *testing.T) {
	ctx := context.Background()
	dbClient, cleanup := testutil.SetupPostgresDB(ctx, t)
	t.Cleanup(cleanup)

	const rowsPerTable = 5
	deleted, liveMonitor := tenantFixture(ctx, t, dbClient, rowsPerTable, true)
	kept, _ := tenantFixture(ctx, t, dbClient, rowsPerTable, false)
	keptBefore := tenantRowCounts(ctx, t, dbClient, kept)

	// The workspace delete stamps deleted_at; liveMonitor stands for a
	// monitor created after the delete's own tombstoning, which the purger
	// must catch itself.
	_, err := dbClient.ExecContext(ctx, `UPDATE tenants SET deleted_at = NOW() WHERE id = $1`, deleted)
	require.NoError(t, err)

	p := newPurger(dbClient, testLogger(t), nil, purgerOptions{BatchSize: 2, MaxRowsPerRun: 7})
	runs := 0
	for ; runs < 100; runs++ {
		var remaining int
		require.NoError(t, dbClient.QueryRowContext(ctx, `SELECT COUNT(*) FROM tenants WHERE id = $1`, deleted).Scan(&remaining))
		if remaining == 0 {
			break
		}
		_, err := p.runOnce(ctx)
		require.NoError(t, err)
	}
	// 10 check results, 5 each of alerts, probes, timeline entries,
	// subscriptions and resources: far more than one run's 7-row budget.
	require.Greater(t, runs, 5, "the purge must be spread over budget-bounded runs")
	require.Less(t, runs, 100, "the deleted workspace was never fully purged")

	var tombstoned bool
	require.NoError(t, dbClient.QueryRowContext(ctx,
		`SELECT NOT EXISTS (SELECT 1 FROM monitors WHERE id = $1)`, liveMonitor).Scan(&tombstoned))
	require.True(t, tombstoned, "a live monitor of a deleted workspace is purged with it")

	for table, n := range tenantRowCounts(ctx, t, dbClient, deleted) {
		require.Zerof(t, n, "%s rows of the deleted workspace remain", table)
	}
	require.Equal(t, keptBefore, tenantRowCounts(ctx, t, dbClient, kept), "another workspace's rows must be untouched")
}

func TestPurger_LiveTenantsAreNeverPurged(t *testing.T) {
	ctx := context.Background()
	dbClient, cleanup := testutil.SetupPostgresDB(ctx, t)
	t.Cleanup(cleanup)

	tenantID := testutil.InsertTenant(ctx, t, dbClient, "")
	p := newPurger(dbClient, testLogger(t), nil, purgerOptions{BatchSize: 2, MaxRowsPerRun: 100})
	_, err := p.runOnce(ctx)
	require.NoError(t, err)

	var n int
	require.NoError(t, dbClient.QueryRowContext(ctx, `SELECT COUNT(*) FROM tenants WHERE id = $1`, tenantID).Scan(&n))
	require.Equal(t, 1, n, "a workspace with no monitors but no deleted_at is not purged")
}

// TestTenantPurgeCoversEveryTenantTable fails when a table that carries
// tenant_id is added without deciding how a workspace purge removes it. The
// final DELETE FROM tenants cascades whatever is left in one statement, so an
// unbounded table must be drained in batches first (tenantPurgeDeletes, or the
// monitor phase when it is a monitor child), and a table with no FK to tenants
// would otherwise be orphaned.
func TestTenantPurgeCoversEveryTenantTable(t *testing.T) {
	ctx := context.Background()
	dbClient, cleanup := testutil.SetupPostgresDB(ctx, t)
	t.Cleanup(cleanup)

	covered := map[string]string{}
	for _, d := range tenantPurgeDeletes {
		covered[d.name] = "tenant phase"
	}
	// Monitor children: drained per monitor by purgeMonitor or cascaded with
	// the monitor row, before the tenant phase starts.
	for _, table := range []string{
		"check_results", "monitor_daily_rollups", "monitor_hourly_rollups", "monitor_downtime_periods",
		"monitor_downtime_open", "monitor_location_state", "monitor_state_intervals", "metric_series",
		"alert_triage",
	} {
		covered[table] = "monitor phase"
	}
	// Config history cascades with its monitor row and is capped at 100
	// versions per monitor (pruned on insert).
	covered["monitor_config_versions"] = "monitor phase"
	// Small configuration tables: the tenant row's cascade removes them.
	for _, table := range []string{
		"ai_label_salts", "ai_settings", "alert_channels", "alert_policies", "api_keys",
		"incident_ai_analyses", "incident_status_page_publications", "location_mesh_state", "locations",
		"maintenance_windows", "mcp_oauth_codes", "mcp_oauth_grants", "oidc_group_mappings", "slas",
		"status_page_template_library", "status_page_templates", "status_pages", "tenant_default_channels",
		"tenant_memberships",
	} {
		covered[table] = "tenant cascade"
	}
	// Escalation policies cascade their steps and targets; the NO ACTION
	// FKs to them from monitors and tenants are checked at statement end, by
	// when the monitor phase and the tenant row's cascade have removed both
	// sides.
	covered["escalation_policies"] = "tenant cascade"
	// Report schedules are small configuration rows; they cascade their
	// unsubscribes.
	covered["sla_report_schedules"] = "tenant cascade"
	// Maintenance occurrences cascade with their window and their tenant.
	covered["maintenance_window_occurrences"] = "tenant cascade"
	// People layer: schedules cascade their rotations, members and
	// overrides (bounded by schedule size and override windows).
	covered["oncall_schedules"] = "tenant cascade"
	// Kept on purpose: the audit trail outlives the workspace and has its
	// own retention.
	covered["audit_log"] = "retained"
	// Alert children: they cascade with their alert, which either phase
	// deletes in batches, and are pruned by age in between.
	covered["alert_notification_deliveries"] = "alert cascade"
	covered["monitors"] = "monitor phase"

	rows, err := dbClient.QueryContext(ctx, `
		SELECT c.relname
		FROM pg_class c
		JOIN pg_namespace n ON n.oid = c.relnamespace
		JOIN pg_attribute a ON a.attrelid = c.oid AND a.attname = 'tenant_id' AND NOT a.attisdropped
		WHERE n.nspname = 'public' AND c.relkind IN ('r', 'p') AND NOT c.relispartition
	`)
	require.NoError(t, err)
	defer rows.Close()
	var missing []string
	for rows.Next() {
		var table string
		require.NoError(t, rows.Scan(&table))
		if _, ok := covered[table]; !ok {
			missing = append(missing, table)
		}
	}
	require.NoError(t, rows.Err())
	sort.Strings(missing)
	require.Emptyf(t, missing, "tables with tenant_id that the workspace purge does not account for: %s "+
		"(add a batched delete to tenantPurgeDeletes in purger_tenants.go, or list them here with the reason)",
		strings.Join(missing, ", "))
}

// Rows a racing request writes after a workspace delete committed are
// tombstoned or revoked by the next purger run, whatever the purge budget.
func TestPurger_CutsOffRowsWrittenAfterWorkspaceDelete(t *testing.T) {
	ctx := context.Background()
	dbClient, cleanup := testutil.SetupPostgresDB(ctx, t)
	t.Cleanup(cleanup)

	deleted := testutil.InsertTenant(ctx, t, dbClient, "")
	live := testutil.InsertTenant(ctx, t, dbClient, "")
	_, err := dbClient.ExecContext(ctx, `UPDATE tenants SET deleted_at = NOW() WHERE id = $1`, deleted)
	require.NoError(t, err)

	for _, tenant := range []uuid.UUID{deleted, live} {
		testutil.InsertHTTPMonitor(ctx, t, dbClient, tenant, "")
		_, err := dbClient.ExecContext(ctx, `
			INSERT INTO api_keys (tenant_id, name, key_hash, key_prefix, scope)
			VALUES ($1, 'late', 'hash', $2, 'write')`, tenant, uuid.NewString())
		require.NoError(t, err)
		_, err = dbClient.ExecContext(ctx, `
			INSERT INTO discovery_integrations (id, tenant_id, name, namespaces, home_namespace, token_hash)
			VALUES (gen_random_uuid(), $1, 'cluster', ARRAY['default'], 'default', 'digest')`, tenant)
		require.NoError(t, err)
	}

	conn, err := dbClient.Conn(ctx)
	require.NoError(t, err)
	defer conn.Close()
	p := newPurger(dbClient, testLogger(t), nil, purgerOptions{})
	ok, err := p.cutOffDeletedTenants(ctx, conn)
	require.NoError(t, err)
	require.True(t, ok)

	cutOff := func(tenant uuid.UUID) (liveMonitors, liveKeys, liveTokens int) {
		require.NoError(t, dbClient.QueryRowContext(ctx, `
			SELECT
				(SELECT COUNT(*) FROM monitors WHERE tenant_id = $1 AND deleted_at IS NULL),
				(SELECT COUNT(*) FROM api_keys WHERE tenant_id = $1 AND revoked_at IS NULL),
				(SELECT COUNT(*) FROM discovery_integrations WHERE tenant_id = $1 AND token_hash <> 'revoked')
		`, tenant).Scan(&liveMonitors, &liveKeys, &liveTokens))
		return
	}
	m, k, tok := cutOff(deleted)
	require.Equal(t, [3]int{0, 0, 0}, [3]int{m, k, tok}, "deleted workspace: live monitors, keys, collector tokens")
	m, k, tok = cutOff(live)
	require.Equal(t, [3]int{1, 1, 1}, [3]int{m, k, tok}, "a live workspace is untouched")
}
