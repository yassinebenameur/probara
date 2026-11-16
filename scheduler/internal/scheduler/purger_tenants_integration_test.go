//go:build !unit

package scheduler

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	shareddb "github.com/yassinebenameur/probara/shared/db"
	"github.com/yassinebenameur/probara/shared/testutil"
)

// Every table that holds a workspace's data, read from the schema: tables
// with a tenant_id column, and every table with a foreign key into one of
// those (transitively), so a new child table cannot be forgotten either.
// Each must be in workspacePurgePlan or in workspacePurgeExempt with a reason.
func TestWorkspacePurgePlanCoversEveryTenantTable(t *testing.T) {
	ctx := context.Background()
	dbClient, cleanup := testutil.SetupPostgresDB(ctx, t)
	t.Cleanup(cleanup)

	rows, err := dbClient.QueryContext(ctx, `
		WITH RECURSIVE scoped(name) AS (
			SELECT c.relname::text
			FROM pg_attribute a
			JOIN pg_class c ON c.oid = a.attrelid
			JOIN pg_namespace n ON n.oid = c.relnamespace
			WHERE n.nspname = 'public' AND c.relkind IN ('r', 'p') AND NOT c.relispartition
			  AND a.attname = 'tenant_id' AND NOT a.attisdropped
			UNION
			SELECT child.relname::text
			FROM pg_constraint fk
			JOIN pg_class child ON child.oid = fk.conrelid
			JOIN pg_class parent ON parent.oid = fk.confrelid
			JOIN scoped s ON s.name = parent.relname
			WHERE fk.contype = 'f' AND NOT child.relispartition
		)
		SELECT name FROM scoped ORDER BY name`)
	require.NoError(t, err)
	var scoped []string
	for rows.Next() {
		var name string
		require.NoError(t, rows.Scan(&name))
		scoped = append(scoped, name)
	}
	require.NoError(t, rows.Err())
	// Tables with no tenant_id of their own are reached through their keys.
	require.Contains(t, scoped, "alert_notification_states")
	require.Contains(t, scoped, "mcp_oauth_tokens")

	planned := map[string]bool{}
	for _, step := range workspacePurgePlan {
		require.Falsef(t, planned[step.table], "%s appears twice in workspacePurgePlan", step.table)
		planned[step.table] = true
	}
	var missing []string
	for _, table := range scoped {
		if !planned[table] && workspacePurgeExempt[table] == "" {
			missing = append(missing, table)
		}
	}
	require.Emptyf(t, missing, "tables holding workspace data that the purge neither deletes nor exempts: %s "+
		"(add a step to workspacePurgePlan in purger_tenants.go, or list the table in workspacePurgeExempt with the reason)",
		strings.Join(missing, ", "))

	known := map[string]bool{}
	for _, table := range scoped {
		known[table] = true
	}
	// Exemptions must name real tenant-scoped tables (no stale entries); the
	// plan may also hold metric_samples, which has neither a tenant_id nor a
	// foreign key and is reached through metric_series.
	for table := range workspacePurgeExempt {
		require.Truef(t, known[table], "workspacePurgeExempt lists %s, which holds no workspace data", table)
	}
	for table := range planned {
		var exists bool
		require.NoError(t, dbClient.QueryRowContext(ctx, `SELECT to_regclass($1) IS NOT NULL`, table).Scan(&exists))
		require.Truef(t, exists, "workspacePurgePlan names %s, which does not exist", table)
	}
}

// A workspace with rows in every planned table ends with none, the tenant row
// goes last, another workspace's rows are untouched, and the audit trail
// stays.
func TestWorkspacePurge_RemovesEveryRowAndSparesOtherWorkspaces(t *testing.T) {
	ctx := context.Background()
	dbClient, cleanup := testutil.SetupPostgresDB(ctx, t)
	t.Cleanup(cleanup)
	shared := seedPlatform(ctx, t, dbClient)

	bystander := testutil.InsertTenant(ctx, t, dbClient, "")
	seedWorkspaceEverywhere(ctx, t, dbClient, bystander, shared)
	baseline := purgeTableCounts(ctx, t, dbClient)

	victim := testutil.InsertTenant(ctx, t, dbClient, "")
	seedWorkspaceEverywhere(ctx, t, dbClient, victim, shared)
	seeded := purgeTableCounts(ctx, t, dbClient)
	for _, step := range workspacePurgePlan {
		require.Greaterf(t, seeded[step.table], baseline[step.table],
			"the fixture must put a row of the purged workspace in %s", step.table)
	}

	markDeleted(ctx, t, dbClient, victim)
	p := newWorkspacePurger(dbClient, testLogger(t), nil, purgerOptions{BatchSize: 3, MaxRowsPerRun: 1_000_000})
	p.pause = 0
	purged, err := p.runOnce(ctx)
	require.NoError(t, err)
	require.Equal(t, 1, purged)

	after := purgeTableCounts(ctx, t, dbClient)
	for table, want := range baseline {
		if table == "audit_log" {
			continue
		}
		require.Equalf(t, want, after[table], "%s: rows left after the purge, or another workspace's rows removed", table)
	}
	require.Equal(t, seeded["audit_log"], after["audit_log"], "the audit trail outlives the workspace")
	var victimRows int
	require.NoError(t, dbClient.QueryRowContext(ctx, `SELECT COUNT(*) FROM tenants WHERE id = $1`, victim).Scan(&victimRows))
	require.Zero(t, victimRows)
}

// Each run deletes at most MaxRowsPerRun rows and the next one resumes where
// it stopped (no progress state: an interrupted run is just a short run).
func TestWorkspacePurge_BoundedAndResumable(t *testing.T) {
	ctx := context.Background()
	dbClient, cleanup := testutil.SetupPostgresDB(ctx, t)
	t.Cleanup(cleanup)
	shared := seedPlatform(ctx, t, dbClient)
	testutil.InsertTenant(ctx, t, dbClient, "") // a live workspace remains

	victim := testutil.InsertTenant(ctx, t, dbClient, "")
	seedWorkspaceEverywhere(ctx, t, dbClient, victim, shared)
	markDeleted(ctx, t, dbClient, victim)

	const budget = 7
	p := newWorkspacePurger(dbClient, testLogger(t), nil, purgerOptions{BatchSize: 2, MaxRowsPerRun: budget})
	p.pause = 0
	total := func() int {
		n := 0
		for table, c := range purgeTableCounts(ctx, t, dbClient) {
			if table != "tenants" && table != "audit_log" {
				n += c
			}
		}
		return n
	}
	runs := 0
	for {
		before := total()
		purged, err := p.runOnce(ctx)
		require.NoError(t, err)
		runs++
		require.LessOrEqualf(t, before-total(), budget, "run %d deleted more than its budget", runs)
		if purged == 1 {
			break
		}
		require.Less(t, runs, 500, "the purge never finished")
	}
	require.Greater(t, runs, 5, "a small budget must spread the purge over several runs")
	var left int
	require.NoError(t, dbClient.QueryRowContext(ctx, `SELECT COUNT(*) FROM tenants WHERE id = $1`, victim).Scan(&left))
	require.Zero(t, left)
}

// A second replica skips while one holds the lock, and replicas racing run
// after run finish the purge without errors.
func TestWorkspacePurge_TwoReplicas(t *testing.T) {
	ctx := context.Background()
	dbClient, cleanup := testutil.SetupPostgresDB(ctx, t)
	t.Cleanup(cleanup)
	shared := seedPlatform(ctx, t, dbClient)
	testutil.InsertTenant(ctx, t, dbClient, "")
	victim := testutil.InsertTenant(ctx, t, dbClient, "")
	seedWorkspaceEverywhere(ctx, t, dbClient, victim, shared)
	markDeleted(ctx, t, dbClient, victim)

	holder, err := acquireMaintenanceLock(ctx, dbClient.DB, workspacePurgeAdvisoryLock)
	require.NoError(t, err)
	require.NotNil(t, holder)
	blocked := newWorkspacePurger(dbClient, testLogger(t), nil, purgerOptions{})
	blocked.pause = 0
	purged, err := blocked.runOnce(ctx)
	require.NoError(t, err)
	require.Zero(t, purged)
	var remaining int
	require.NoError(t, dbClient.QueryRowContext(ctx, `SELECT COUNT(*) FROM api_keys WHERE tenant_id = $1`, victim).Scan(&remaining))
	require.NotZero(t, remaining, "nothing may be deleted without the lock")
	require.NoError(t, holder.release())

	var done atomic.Int32
	var wg sync.WaitGroup
	for replica := 0; replica < 2; replica++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			p := newWorkspacePurger(dbClient, testLogger(t), nil, purgerOptions{BatchSize: 2, MaxRowsPerRun: 10})
			p.pause = 0
			for i := 0; i < 400 && done.Load() == 0; i++ {
				n, err := p.runOnce(ctx)
				if err != nil {
					t.Errorf("replica run: %v", err)
					return
				}
				if n > 0 {
					done.Add(int32(n))
				}
			}
		}()
	}
	wg.Wait()
	require.EqualValues(t, 1, done.Load(), "exactly one replica completes the workspace")
	require.NoError(t, dbClient.QueryRowContext(ctx, `SELECT COUNT(*) FROM tenants WHERE id = $1`, victim).Scan(&remaining))
	require.Zero(t, remaining)
}

// Monitors a user deleted before the workspace was deleted are left to the
// workspace purge, not the per-monitor purge (which shares a lock with
// rollups).
func TestMonitorPurgeLeavesDeletedWorkspaces(t *testing.T) {
	ctx := context.Background()
	dbClient, cleanup := testutil.SetupPostgresDB(ctx, t)
	t.Cleanup(cleanup)
	live := testutil.InsertTenant(ctx, t, dbClient, "")
	deleted := testutil.InsertTenant(ctx, t, dbClient, "")
	liveMonitor := testutil.InsertHTTPMonitor(ctx, t, dbClient, live, "")
	deletedMonitor := testutil.InsertHTTPMonitor(ctx, t, dbClient, deleted, "")
	_, err := dbClient.ExecContext(ctx, `UPDATE monitors SET deleted_at = NOW() WHERE id = ANY($1)`,
		fmt.Sprintf("{%s,%s}", liveMonitor, deletedMonitor))
	require.NoError(t, err)
	markDeleted(ctx, t, dbClient, deleted)

	p := newPurger(dbClient, testLogger(t), nil, purgerOptions{})
	purged, err := p.runOnce(ctx)
	require.NoError(t, err)
	require.Equal(t, 1, purged)
	var n int
	require.NoError(t, dbClient.QueryRowContext(ctx, `SELECT COUNT(*) FROM monitors WHERE id = $1`, deletedMonitor).Scan(&n))
	require.Equal(t, 1, n)
}

func markDeleted(ctx context.Context, t *testing.T, dbClient *shareddb.Client, tenant uuid.UUID) {
	t.Helper()
	_, err := dbClient.ExecContext(ctx, `UPDATE tenants SET deleted_at = NOW() WHERE id = $1`, tenant)
	require.NoError(t, err)
}

// purgeTableCounts counts every planned and exempt table, and tenants.
func purgeTableCounts(ctx context.Context, t *testing.T, dbClient *shareddb.Client) map[string]int {
	t.Helper()
	tables := []string{"tenants"}
	for _, step := range workspacePurgePlan {
		tables = append(tables, step.table)
	}
	for table := range workspacePurgeExempt {
		tables = append(tables, table)
	}
	sort.Strings(tables)
	counts := map[string]int{}
	for _, table := range tables {
		var n int
		require.NoError(t, dbClient.QueryRowContext(ctx, `SELECT COUNT(*) FROM `+table).Scan(&n))
		counts[table] = n
	}
	return counts
}

// platformRows are the platform-level rows the workspace fixture refers to.
type platformRows struct {
	admin, client uuid.UUID
}

func seedPlatform(ctx context.Context, t *testing.T, dbClient *shareddb.Client) platformRows {
	t.Helper()
	p := platformRows{admin: uuid.New(), client: uuid.New()}
	_, err := dbClient.ExecContext(ctx, `INSERT INTO admin_users (id, username, platform_role) VALUES ($1, $2, 'member')`,
		p.admin, "purge-"+p.admin.String()[:8])
	require.NoError(t, err)
	_, err = dbClient.ExecContext(ctx, `INSERT INTO mcp_oauth_clients (id, client_name, redirect_uris) VALUES ($1, 'agent', ARRAY['http://127.0.0.1/cb'])`, p.client)
	require.NoError(t, err)
	return p
}

// workspaceFixtureSQL puts at least one row of the workspace {T} in every
// table of workspacePurgePlan (and one audit_log row). Placeholders are
// replaced with generated IDs; the statements run as one simple-protocol
// batch.
const workspaceFixtureSQL = `
INSERT INTO api_keys (tenant_id, name, key_hash) VALUES ('{T}', 'key', 'hash');
INSERT INTO mcp_oauth_grants (id, client_id, admin_user_id, tenant_id, scope) VALUES ('{GRANT}', '{CLIENT}', '{ADMIN}', '{T}', 'read');
INSERT INTO mcp_oauth_tokens (token_hash, grant_id, kind, expires_at) VALUES ('tok-{X}', '{GRANT}', 'access', NOW() + INTERVAL '1 hour');
INSERT INTO mcp_oauth_codes (code_hash, client_id, admin_user_id, tenant_id, scope, redirect_uri, code_challenge, expires_at)
	VALUES ('code-{X}', '{CLIENT}', '{ADMIN}', '{T}', 'read', 'http://127.0.0.1/cb', 'challenge', NOW() + INTERVAL '1 minute');
INSERT INTO tenant_memberships (admin_user_id, tenant_id, role) VALUES ('{ADMIN}', '{T}', 'viewer');
INSERT INTO oidc_group_mappings (group_name, role, tenant_id) VALUES ('group-{X}', 'viewer', '{T}');

INSERT INTO locations (id, tenant_id, name, slug) VALUES ('{L1}', '{T}', 'loc-a-{X}', 'loc-a-{X}'), ('{L2}', '{T}', 'loc-b-{X}', 'loc-b-{X}');
INSERT INTO monitors (id, tenant_id, name, type, config, interval_seconds, timeout_seconds)
	VALUES ('{M1}', '{T}', 'm1-{X}', 'http', '{}'::jsonb, 60, 30),
	       ('{M2}', '{T}', 'm2-{X}', 'http', '{}'::jsonb, 60, 30),
	       ('{G}', '{T}', 'g-{X}', 'group', '{}'::jsonb, 60, 30);
INSERT INTO monitor_groups (monitor_id, group_id) VALUES ('{M1}', '{G}');
INSERT INTO monitor_dependencies (monitor_id, depends_on_id) VALUES ('{M1}', '{M2}');
INSERT INTO monitor_locations (monitor_id, location_id) VALUES ('{M1}', '{L1}');
INSERT INTO monitor_location_state (monitor_id, location_id, tenant_id) VALUES ('{M1}', '{L1}', '{T}');
INSERT INTO location_mesh_state (tenant_id, source_location_id, target_location_id) VALUES ('{T}', '{L1}', '{L2}');
INSERT INTO mesh_probe_results (id, tenant_id, source_location_id, target_location_id, job_id, status)
	VALUES (gen_random_uuid(), '{T}', '{L1}', '{L2}', gen_random_uuid(), 'success');

INSERT INTO alert_channels (id, tenant_id, name, type, config) VALUES ('{CH}', '{T}', 'chan-{X}', 'webhook', '{}'::jsonb);
INSERT INTO tenant_default_channels (tenant_id, channel_id) VALUES ('{T}', '{CH}');
INSERT INTO monitor_channels (monitor_id, channel_id) VALUES ('{M1}', '{CH}');
INSERT INTO alert_policies (id, tenant_id, name, failure_threshold, failure_window_seconds) VALUES ('{AP}', '{T}', 'policy', 1, 60);
INSERT INTO alert_policy_channels (alert_policy_id, channel_id) VALUES ('{AP}', '{CH}');
INSERT INTO monitor_alert_policies (monitor_id, alert_policy_id) VALUES ('{M1}', '{AP}');

INSERT INTO check_results (monitor_id, tenant_id, job_id, status, location_id)
	SELECT '{M1}', '{T}', gen_random_uuid(), 'success', '{L1}' FROM generate_series(1, 5);
INSERT INTO monitor_hourly_rollups (tenant_id, monitor_id, bucket_hour) VALUES ('{T}', '{M1}', date_trunc('hour', NOW()));
INSERT INTO monitor_daily_rollups (tenant_id, monitor_id, bucket_day) VALUES ('{T}', '{M1}', CURRENT_DATE);
INSERT INTO monitor_downtime_periods (tenant_id, monitor_id, start_time, end_time) VALUES ('{T}', '{M1}', NOW() - INTERVAL '2 hours', NOW() - INTERVAL '1 hour');
INSERT INTO monitor_downtime_open (tenant_id, monitor_id, started_at, last_seen_at) VALUES ('{T}', '{M1}', NOW(), NOW());
INSERT INTO monitor_state_intervals (tenant_id, monitor_id, state, reason, started_at) VALUES ('{T}', '{M1}', 'up', 'created', NOW());
INSERT INTO monitor_config_versions (tenant_id, monitor_id, version, snapshot, actor_type, source) VALUES ('{T}', '{M1}', 1, '{}'::jsonb, 'system', 'system');
INSERT INTO rollup_dirty (monitor_id, bucket_hour) VALUES ('{M1}', date_trunc('hour', NOW()));
INSERT INTO metric_series (tenant_id, monitor_id, metric_name, metric_type, attr_hash) VALUES ('{T}', '{M1}', 'cpu', 'gauge', '\x01'::bytea);
INSERT INTO metric_samples (series_id, ts, value)
	SELECT s.id, NOW() - make_interval(secs => g), 1 FROM metric_series s, generate_series(1, 4) g WHERE s.monitor_id = '{M1}';
INSERT INTO metric_rollups_hourly (series_id, bucket, sample_count, min_value, max_value, sum_value, first_value, last_value)
	SELECT s.id, date_trunc('hour', NOW()), 1, 1, 1, 1, 1, 1 FROM metric_series s WHERE s.monitor_id = '{M1}';
INSERT INTO metric_rollup_dirty (monitor_id, bucket_hour) VALUES ('{M1}', date_trunc('hour', NOW()));

INSERT INTO alerts (id, tenant_id, monitor_id, kind, failure_count) VALUES ('{A}', '{T}', '{M1}', 'availability', 1);
INSERT INTO alert_notification_states (alert_id, channel_id, last_sent_at, last_event_type) VALUES ('{A}', '{CH}', NOW(), 'created');
INSERT INTO alert_notification_recipient_deliveries (alert_id, channel_id, event_key, recipient_hash) VALUES ('{A}', '{CH}', 'created', 'r');
INSERT INTO alert_notification_deliveries (tenant_id, alert_id, channel_id, event_type, attempt, path, outcome)
	VALUES ('{T}', '{A}', '{CH}', 'created', 1, 'sync', 'delivered');
INSERT INTO alert_relapses (alert_id, relapse_number, recovered_at, reopened_at) VALUES ('{A}', 1, NOW() - INTERVAL '1 minute', NOW());
INSERT INTO alert_triage (id, tenant_id, alert_id, status) VALUES ('{TR}', '{T}', '{A}', 'pending');
INSERT INTO ai_labels (tenant_id, subject_type, subject_id, subject_key, verdict, answers, request, catalogue_version)
	VALUES ('{T}', 'alert_triage', '{TR}', '{TR}', 'confirmed', '{}'::jsonb, '{}'::jsonb, 'v1');
INSERT INTO ai_label_salts (tenant_id, salt) VALUES ('{T}', decode(repeat('ab', 32), 'hex'));
INSERT INTO ai_settings (tenant_id) VALUES ('{T}');

INSERT INTO incidents (id, tenant_id, title) VALUES ('{I}', '{T}', 'incident');
INSERT INTO incident_alerts (incident_id, alert_id) VALUES ('{I}', '{A}');
INSERT INTO incident_monitors (incident_id, monitor_id) VALUES ('{I}', '{M1}');
INSERT INTO incident_timeline_entries (tenant_id, incident_id, entry_type, message) VALUES ('{T}', '{I}', 'system', 'opened');
INSERT INTO incident_ai_analyses (tenant_id, incident_id, status) VALUES ('{T}', '{I}', 'pending');

INSERT INTO status_pages (id, tenant_id, slug, title) VALUES ('{P}', '{T}', 'page-{X}', 'Status');
INSERT INTO status_page_sections (id, status_page_id, title) VALUES ('{S}', '{P}', 'Section');
INSERT INTO status_page_section_monitors (section_id, monitor_id) VALUES ('{S}', '{M1}');
INSERT INTO status_page_monitors (status_page_id, monitor_id) VALUES ('{P}', '{M1}');
INSERT INTO status_page_templates (tenant_id, status_page_id, status, source) VALUES ('{T}', '{P}', 'draft', '<html></html>');
INSERT INTO status_page_template_library (tenant_id, name, source) VALUES ('{T}', 'lib-{X}', '<html></html>');
INSERT INTO status_page_push_subscriptions (status_page_id, endpoint, p256dh, auth) VALUES ('{P}', 'https://push.example.com/{X}', 'key', 'auth');
INSERT INTO status_page_push_deliveries (status_page_id, monitor_id, interval_id, kind, monitor_name) VALUES ('{P}', '{M1}', gen_random_uuid(), 'down', 'm1');
INSERT INTO incident_status_page_publications (incident_id, status_page_id, tenant_id) VALUES ('{I}', '{P}', '{T}');
INSERT INTO incident_status_page_monitors (incident_id, status_page_id, monitor_id) VALUES ('{I}', '{P}', '{M1}');

INSERT INTO slas (id, tenant_id, name, target_pct) VALUES ('{SLA}', '{T}', 'sla-{X}', 99.9);
INSERT INTO sla_monitors (sla_id, monitor_id) VALUES ('{SLA}', '{M1}');
INSERT INTO sla_reports (tenant_id, sla_id, period_key, period_start, period_end, target_pct, data)
	VALUES ('{T}', '{SLA}', '2026-10', '2026-10-01', '2026-11-01', 99.9, '{}'::jsonb);
INSERT INTO sla_report_schedules (id, tenant_id, sla_id, recipients, next_run_at) VALUES ('{SCH}', '{T}', '{SLA}', ARRAY['ops@example.com'], NOW());
INSERT INTO sla_report_deliveries (schedule_id, tenant_id, period_start, period_key, address_key, address)
	VALUES ('{SCH}', '{T}', '2026-10-01', '2026-10', 'k', 'ops@example.com');
INSERT INTO sla_report_unsubscribes (schedule_id, address_key) VALUES ('{SCH}', 'k');

INSERT INTO maintenance_windows (id, tenant_id, title, starts_at, ends_at) VALUES ('{W}', '{T}', 'window', NOW(), NOW() + INTERVAL '1 hour');
INSERT INTO maintenance_window_monitors (maintenance_window_id, monitor_id) VALUES ('{W}', '{M1}');
INSERT INTO maintenance_window_occurrences (window_id, tenant_id, starts_at, ends_at) VALUES ('{W}', '{T}', NOW(), NOW() + INTERVAL '1 hour');
INSERT INTO maintenance_window_past_targets (maintenance_window_id, monitor_id, linked_at, unlinked_at) VALUES ('{W}', '{M2}', NOW() - INTERVAL '1 day', NOW());

INSERT INTO discovery_integrations (id, tenant_id, name, namespaces, home_namespace, token_hash, location_id)
	VALUES ('{DI}', '{T}', 'cluster', ARRAY['default'], 'default', 'digest-{X}', '{L1}');
INSERT INTO discovered_services (id, tenant_id, integration_id, service_key, name, namespace) VALUES ('{DS}', '{T}', '{DI}', 'svc', 'svc', 'default');
INSERT INTO discovered_resources (integration_id, uid, service_id, kind, name, namespace) VALUES ('{DI}', 'uid-{X}', '{DS}', 'Service', 'svc', 'default');
INSERT INTO discovery_monitor_links (tenant_id, service_id, monitor_id) VALUES ('{T}', '{DS}', '{M1}');

INSERT INTO audit_log (tenant_id, actor_type, action, outcome) VALUES ('{T}', 'anonymous', 'monitor.create', 'success');
`

func seedWorkspaceEverywhere(ctx context.Context, t *testing.T, dbClient *shareddb.Client, tenant uuid.UUID, p platformRows) {
	t.Helper()
	pairs := []string{"{T}", tenant.String(), "{ADMIN}", p.admin.String(), "{CLIENT}", p.client.String(),
		"{X}", strings.ReplaceAll(uuid.NewString(), "-", "")[:12]}
	for _, key := range []string{"GRANT", "L1", "L2", "M1", "M2", "G", "CH", "AP", "A", "TR", "I", "P", "S", "SLA", "SCH", "W", "DI", "DS"} {
		pairs = append(pairs, "{"+key+"}", uuid.NewString())
	}
	_, err := dbClient.ExecContext(ctx, strings.NewReplacer(pairs...).Replace(workspaceFixtureSQL))
	require.NoError(t, err)
}
