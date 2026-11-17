package scheduler

import (
	"context"
	"database/sql"
	"fmt"
	"sort"
	"strings"
	"sync/atomic"
	"time"

	"github.com/google/uuid"
	"github.com/lib/pq"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/sirupsen/logrus"

	"github.com/yassinebenameur/probara/shared/db"
	"github.com/yassinebenameur/probara/shared/db/pgerr"
	"github.com/yassinebenameur/probara/shared/logger"
	"github.com/yassinebenameur/probara/shared/secrets"
)

// Workspace purge: removes soft-deleted workspaces (tenants.deleted_at, see
// shared/tenancy) in bounded batches, table by table, and deletes the tenant
// row last.
//
// The API never deletes a tenant row: dozens of foreign keys cascade from it,
// so one DELETE FROM tenants would remove the workspace's whole check
// history, metric samples and delivery log in a single transaction. Instead
// every statement here deletes at most one batch of one table, leaves first,
// so each delete's own cascades are already empty, and the work spreads over
// as many ticks as it needs.
//
// It holds its own session advisory lock (one replica at a time), never the
// one the monitor purge and rollup maintenance share. It keeps no progress
// state: every step is idempotent and an emptied table costs one index probe,
// so a run after a crash or a lost lock simply walks the plan again. The row
// goes only once a whole pass over the plan found nothing left to delete,
// which also sweeps up rows a racing writer added to a table the purge had
// already emptied.
//
// TestWorkspacePurgePlanCoversEveryTenantTable keeps workspacePurgePlan
// complete as tables are added.
const (
	workspacePurgeAdvisoryLock = int64(901_337_420)
	workspacePurgeRunTimeout   = 30 * time.Minute
	// workspacePurgeBatchPause spaces the batches so a large purge leaves
	// I/O and locks to ingest and dispatch between its statements.
	workspacePurgeBatchPause = 25 * time.Millisecond
)

// purgeStep deletes at most $2 rows of one table that belong to the
// workspace $1 and reports how many it deleted. Batches pick rows by ctid in
// an ARRAY(...) init-plan, so the delete itself is a TID scan whatever the
// table's keys; the selection walks an index on the tenant (or on the parent
// the table hangs off).
type purgeStep struct {
	table string
	sql   string
	// partitioned steps are run per partition (ctid is unique only within a
	// partition); sql is then a format with the partition name as %s.
	partitioned bool
}

// byTenant deletes rows of a table that carries tenant_id, through its
// tenant index.
func byTenant(table string) purgeStep {
	return purgeStep{table: table, sql: `DELETE FROM ` + table + ` WHERE ctid = ANY(ARRAY(
		SELECT ctid FROM ` + table + ` WHERE tenant_id = $1 LIMIT $2))`}
}

// viaParent deletes rows of a child table selected through a join to the
// parent that carries the workspace (alias c is the child).
func viaParent(table, join string) purgeStep {
	return purgeStep{table: table, sql: `DELETE FROM ` + table + ` WHERE ctid = ANY(ARRAY(
		SELECT c.ctid FROM ` + table + ` c ` + join + ` LIMIT $2))`}
}

// workspacePurgePlan is the delete order. Credentials and public surfaces go
// first (API keys, MCP grants, collector tokens, status pages and their
// globally unique slugs), then the large history tables, then configuration.
// A child always precedes its parent, and every table a later delete would
// SET NULL (check_results.location_id, discovery_integrations.location_id)
// is emptied before that parent goes.
var workspacePurgePlan = []purgeStep{
	// Access and credentials.
	byTenant("api_keys"),
	viaParent("mcp_oauth_tokens", `JOIN mcp_oauth_grants g ON g.id = c.grant_id WHERE g.tenant_id = $1`),
	byTenant("mcp_oauth_codes"),
	byTenant("mcp_oauth_grants"),
	byTenant("tenant_memberships"),
	byTenant("oidc_group_mappings"),
	viaParent("discovered_resources", `JOIN discovery_integrations i ON i.id = c.integration_id WHERE i.tenant_id = $1`),
	viaParent("discovery_monitor_links", `JOIN discovered_services s ON s.id = c.service_id WHERE s.tenant_id = $1`),
	byTenant("discovered_services"),
	byTenant("discovery_integrations"),

	// Status pages (their slugs are unique across workspaces).
	viaParent("status_page_push_subscriptions", `JOIN status_pages p ON p.id = c.status_page_id WHERE p.tenant_id = $1`),
	viaParent("status_page_push_deliveries", `JOIN status_pages p ON p.id = c.status_page_id WHERE p.tenant_id = $1`),
	viaParent("incident_status_page_monitors", `JOIN incidents i ON i.id = c.incident_id WHERE i.tenant_id = $1`),
	byTenant("incident_status_page_publications"),
	viaParent("status_page_section_monitors", `JOIN status_page_sections s ON s.id = c.section_id
		JOIN status_pages p ON p.id = s.status_page_id WHERE p.tenant_id = $1`),
	viaParent("status_page_sections", `JOIN status_pages p ON p.id = c.status_page_id WHERE p.tenant_id = $1`),
	viaParent("status_page_monitors", `JOIN status_pages p ON p.id = c.status_page_id WHERE p.tenant_id = $1`),
	viaParent("status_page_templates", `JOIN status_pages p ON p.id = c.status_page_id WHERE p.tenant_id = $1`),
	byTenant("status_pages"),
	byTenant("status_page_template_library"),

	// Alerts and notification history.
	byTenant("alert_notification_deliveries"),
	viaParent("alert_notification_recipient_deliveries", `JOIN alerts a ON a.id = c.alert_id WHERE a.tenant_id = $1`),
	viaParent("alert_notification_states", `JOIN alerts a ON a.id = c.alert_id WHERE a.tenant_id = $1`),
	viaParent("alert_relapses", `JOIN alerts a ON a.id = c.alert_id WHERE a.tenant_id = $1`),
	byTenant("alert_triage"),
	viaParent("incident_alerts", `JOIN incidents i ON i.id = c.incident_id WHERE i.tenant_id = $1`),
	byTenant("alerts"),

	// Incidents.
	viaParent("incident_timeline_entries", `JOIN incidents i ON i.id = c.incident_id WHERE i.tenant_id = $1`),
	viaParent("incident_ai_analyses", `JOIN incidents i ON i.id = c.incident_id WHERE i.tenant_id = $1`),
	viaParent("incident_monitors", `JOIN incidents i ON i.id = c.incident_id WHERE i.tenant_id = $1`),
	byTenant("incidents"),

	// SLAs and their emailed reports.
	viaParent("sla_report_deliveries", `JOIN sla_report_schedules s ON s.id = c.schedule_id WHERE s.tenant_id = $1`),
	viaParent("sla_report_unsubscribes", `JOIN sla_report_schedules s ON s.id = c.schedule_id WHERE s.tenant_id = $1`),
	byTenant("sla_report_schedules"),
	byTenant("sla_reports"),
	viaParent("sla_monitors", `JOIN slas s ON s.id = c.sla_id WHERE s.tenant_id = $1`),
	byTenant("slas"),

	// Maintenance windows and their history.
	byTenant("maintenance_window_occurrences"),
	viaParent("maintenance_window_past_targets", `JOIN maintenance_windows w ON w.id = c.maintenance_window_id WHERE w.tenant_id = $1`),
	viaParent("maintenance_window_monitors", `JOIN maintenance_windows w ON w.id = c.maintenance_window_id WHERE w.tenant_id = $1`),
	byTenant("maintenance_windows"),

	// Monitor history and the metric store.
	byTenant("check_results"),
	byTenant("monitor_hourly_rollups"),
	byTenant("monitor_daily_rollups"),
	byTenant("monitor_downtime_periods"),
	byTenant("monitor_downtime_open"),
	byTenant("monitor_state_intervals"),
	viaParent("monitor_location_state", `JOIN monitors m ON m.id = c.monitor_id WHERE m.tenant_id = $1`),
	byTenant("monitor_config_versions"),
	viaParent("rollup_dirty", `JOIN monitors m ON m.id = c.monitor_id WHERE m.tenant_id = $1`),
	// No foreign key from samples to series (a cascade across the
	// partitioned table would be unbounded), so they go explicitly, per
	// partition: $1 is the workspace's series IDs here.
	{table: "metric_samples", partitioned: true, sql: `DELETE FROM %s WHERE ctid = ANY(ARRAY(
		SELECT ctid FROM %s WHERE series_id = ANY($1) LIMIT $2))`},
	viaParent("metric_rollups_hourly", `JOIN metric_series s ON s.id = c.series_id WHERE s.tenant_id = $1`),
	viaParent("metric_rollup_dirty", `JOIN monitors m ON m.id = c.monitor_id WHERE m.tenant_id = $1`),
	byTenant("metric_series"),
	byTenant("mesh_probe_results"),
	byTenant("location_mesh_state"),

	// Configuration.
	viaParent("monitor_groups", `JOIN monitors m ON m.id = c.monitor_id WHERE m.tenant_id = $1`),
	viaParent("monitor_dependencies", `JOIN monitors m ON m.id = c.monitor_id WHERE m.tenant_id = $1`),
	viaParent("monitor_channels", `JOIN monitors m ON m.id = c.monitor_id WHERE m.tenant_id = $1`),
	viaParent("monitor_locations", `JOIN monitors m ON m.id = c.monitor_id WHERE m.tenant_id = $1`),
	viaParent("monitor_alert_policies", `JOIN monitors m ON m.id = c.monitor_id WHERE m.tenant_id = $1`),
	byTenant("monitors"),
	byTenant("tenant_default_channels"),
	viaParent("alert_policy_channels", `JOIN alert_policies p ON p.id = c.alert_policy_id WHERE p.tenant_id = $1`),
	byTenant("alert_policies"),
	byTenant("alert_channels"),
	byTenant("locations"),
	byTenant("ai_labels"),
	byTenant("ai_label_salts"),
	byTenant("ai_settings"),
}

// workspaceScrubPlan blanks the secret-bearing columns of a deleted
// workspace before the purge deletes anything, so its credentials do not wait
// at rest behind the history tables (the rows themselves go late, in plan
// order): channel configs (provider tokens, webhook URLs, passwords), monitor
// config secrets (every field secrets.MonitorSecretFields and
// MonitorSecretMapFields know, of any type) and push tokens, private-location
// worker credentials, and the legacy AI provider key. Each step updates only
// rows still holding a secret, in bounded batches, so it is a no-op once done.
// Visitor push keys (status_page_push_subscriptions) and the token, key and
// code hashes are whole rows deleted in the plan's first steps.
func workspaceScrubPlan() []purgeStep {
	return []purgeStep{
		{table: "alert_channels (secrets)", sql: `UPDATE alert_channels SET config = '{}'::jsonb
			WHERE ctid = ANY(ARRAY(SELECT ctid FROM alert_channels
				WHERE tenant_id = $1 AND config <> '{}'::jsonb LIMIT $2))`},
		{table: "monitors (secrets)", sql: `UPDATE monitors SET config = config - ` + monitorSecretKeysSQL + `, push_token = NULL
			WHERE ctid = ANY(ARRAY(SELECT ctid FROM monitors
				WHERE tenant_id = $1 AND (config ?| ` + monitorSecretKeysSQL + ` OR push_token IS NOT NULL) LIMIT $2))`},
		{table: "locations (secrets)", sql: `UPDATE locations SET worker_credential = NULL
			WHERE ctid = ANY(ARRAY(SELECT ctid FROM locations
				WHERE tenant_id = $1 AND worker_credential IS NOT NULL LIMIT $2))`},
		{table: "ai_settings (secrets)", sql: `UPDATE ai_settings SET api_key_encrypted = ''
			WHERE ctid = ANY(ARRAY(SELECT ctid FROM ai_settings
				WHERE tenant_id = $1 AND api_key_encrypted <> '' LIMIT $2))`},
	}
}

// monitorSecretKeysSQL is every top-level monitor config key the secret
// registries name, of any type, as a SQL text[] literal.
var monitorSecretKeysSQL = func() string {
	seen := map[string]bool{}
	var quoted []string
	for _, registry := range []map[string][]string{secrets.MonitorSecretFields, secrets.MonitorSecretMapFields} {
		for _, fields := range registry {
			for _, f := range fields {
				if !seen[f] {
					seen[f] = true
					quoted = append(quoted, pq.QuoteLiteral(f))
				}
			}
		}
	}
	sort.Strings(quoted)
	return "ARRAY[" + strings.Join(quoted, ", ") + "]::text[]"
}()

// workspacePurgeExempt lists the tenant-scoped tables the purge deliberately
// keeps, with the reason. TestWorkspacePurgePlanCoversEveryTenantTable fails
// on any tenant-scoped table that is in neither list.
var workspacePurgeExempt = map[string]string{
	// The audit trail outlives the workspace (who deleted it, and what was
	// done in it before) and has its own retention, AUDIT_RETENTION_DAYS. It
	// has no foreign key to tenants.
	"audit_log": "audit trail with its own retention (AUDIT_RETENTION_DAYS)",
}

type workspacePurgeMetrics struct {
	rows        prometheus.Counter
	workspaces  prometheus.Counter
	errors      prometheus.Counter
	pending     prometheus.Gauge
	runDuration prometheus.Observer
}

type workspacePurger struct {
	db      *db.Client
	logger  *logger.Logger
	metrics *workspacePurgeMetrics
	opts    purgerOptions
	pause   time.Duration
	running atomic.Bool
}

func newWorkspacePurger(dbClient *db.Client, log *logger.Logger, metrics *workspacePurgeMetrics, opts purgerOptions) *workspacePurger {
	if opts.BatchSize <= 0 {
		opts.BatchSize = 5000
	}
	if opts.MaxRowsPerRun <= 0 {
		opts.MaxRowsPerRun = 200000
	}
	return &workspacePurger{db: dbClient, logger: log, metrics: metrics, opts: opts, pause: workspacePurgeBatchPause}
}

// trigger starts a run in the background unless one is still going here;
// another replica's run is excluded by the advisory lock.
func (p *workspacePurger) trigger(ctx context.Context) {
	if !p.running.CompareAndSwap(false, true) {
		return
	}
	go func() {
		defer p.running.Store(false)
		start := time.Now()
		purged, err := p.runOnce(ctx)
		if p.metrics != nil {
			p.metrics.runDuration.Observe(time.Since(start).Seconds())
		}
		if err != nil && ctx.Err() == nil {
			p.logger.WithError(err).Warn("workspace purge run failed; it resumes on the next tick")
			return
		}
		if purged > 0 {
			p.logger.WithField("workspaces_purged", purged).Info("workspace purge run completed")
		}
	}()
}

// runOnce purges deleted workspaces, oldest first, within MaxRowsPerRun rows
// and returns how many it removed completely. It does nothing (and no error)
// when another replica holds the lock or before migration 000116.
func (p *workspacePurger) runOnce(ctx context.Context) (int, error) {
	ctx, cancel := context.WithTimeout(ctx, workspacePurgeRunTimeout)
	defer cancel()

	lock, err := acquireMaintenanceLock(ctx, p.db.DB, workspacePurgeAdvisoryLock)
	if err != nil {
		return 0, err
	}
	if lock == nil {
		p.logger.Debug("workspace purge: advisory lock held by another replica; skipping run")
		return 0, nil
	}
	defer func() {
		if err := lock.release(); err != nil {
			p.logger.WithError(err).Warn("failed to release the workspace purge advisory lock")
		}
	}()
	conn := lock.conn

	pending, err := p.deletedWorkspaces(ctx, conn)
	if err != nil {
		return 0, err
	}
	if p.metrics != nil {
		p.metrics.pending.Set(float64(len(pending)))
	}

	budget := p.opts.MaxRowsPerRun
	purged := 0
	for _, tenantID := range pending {
		done, spent, err := p.purgeWorkspace(ctx, conn, tenantID, budget)
		budget -= spent
		if err != nil {
			if p.metrics != nil {
				p.metrics.errors.Inc()
			}
			return purged, fmt.Errorf("purge workspace %s: %w", tenantID, err)
		}
		if !done {
			return purged, nil
		}
		purged++
		if p.metrics != nil {
			p.metrics.workspaces.Inc()
			p.metrics.pending.Dec()
		}
		if budget <= 0 {
			return purged, nil
		}
	}
	return purged, nil
}

// deletedWorkspaces lists the soft-deleted workspaces, oldest delete first.
func (p *workspacePurger) deletedWorkspaces(ctx context.Context, conn *sql.Conn) ([]uuid.UUID, error) {
	rows, err := conn.QueryContext(ctx, `SELECT id FROM tenants WHERE deleted_at IS NOT NULL ORDER BY deleted_at, id`)
	if err != nil {
		if pgerr.IsMissingSchema(err) {
			return nil, nil // before migration 000116 nothing can be deleted
		}
		return nil, fmt.Errorf("list deleted workspaces: %w", err)
	}
	defer rows.Close()
	var ids []uuid.UUID
	for rows.Next() {
		var id uuid.UUID
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

// purgeWorkspace walks the plan for one workspace within budget rows. done
// is true once the tenant row is gone; false with a nil error means the
// budget ran out and the next run resumes.
func (p *workspacePurger) purgeWorkspace(ctx context.Context, conn *sql.Conn, tenantID uuid.UUID, budget int) (done bool, spent int, err error) {
	fields := logrus.Fields{"tenant_id": tenantID}
	perTable := map[string]int{}
	defer func() {
		if spent > 0 {
			p.logger.WithFields(fields).WithFields(logrus.Fields{
				"rows_purged": spent, "tables": perTable, "complete": done,
			}).Info("workspace purge progress")
		}
	}()

	for {
		passDeleted := 0
		// The scrub comes first in every pass, so secrets a racing writer
		// stored meanwhile go before anything else too.
		for _, step := range append(workspaceScrubPlan(), workspacePurgePlan...) {
			n, drained, err := p.runStep(ctx, conn, step, tenantID, budget-spent)
			spent += n
			passDeleted += n
			if n > 0 {
				perTable[step.table] += n
			}
			if err != nil {
				return false, spent, fmt.Errorf("purge %s: %w", step.table, err)
			}
			if !drained {
				return false, spent, nil
			}
		}
		if passDeleted == 0 {
			break
		}
		// Something was deleted: walk once more, so the row goes only after a
		// pass that found every table empty (a racing writer may have added
		// to a table this pass had already emptied).
	}

	// Everything is empty: the cascade has nothing left to do. The guard
	// keeps a workspace that is somehow live again untouched.
	if _, err := conn.ExecContext(ctx, `DELETE FROM tenants WHERE id = $1 AND deleted_at IS NOT NULL`, tenantID); err != nil {
		return false, spent, fmt.Errorf("delete workspace row: %w", err)
	}
	p.logger.WithFields(fields).WithField("rows_purged", spent).Info("deleted workspace fully purged")
	return true, spent, nil
}

// runStep drains one table for the workspace within budget. drained is false
// when the budget ran out before the table was known to be empty.
func (p *workspacePurger) runStep(ctx context.Context, conn *sql.Conn, step purgeStep, tenantID uuid.UUID, budget int) (deleted int, drained bool, err error) {
	if step.partitioned {
		return p.runPartitionedStep(ctx, conn, step, tenantID, budget)
	}
	return p.drain(ctx, conn, budget, func(limit int) (sql.Result, error) {
		return conn.ExecContext(ctx, step.sql, tenantID, limit)
	})
}

// runPartitionedStep runs a metric_samples step on each partition with the
// workspace's series IDs.
func (p *workspacePurger) runPartitionedStep(ctx context.Context, conn *sql.Conn, step purgeStep, tenantID uuid.UUID, budget int) (int, bool, error) {
	series, err := queryInt64s(ctx, conn, `SELECT id FROM metric_series WHERE tenant_id = $1`, tenantID)
	if err != nil || len(series) == 0 {
		return 0, err == nil, err
	}
	partitions, err := queryStrings(ctx, conn, `
		SELECT c.relname FROM pg_inherits i
		JOIN pg_class c ON c.oid = i.inhrelid
		WHERE i.inhparent = to_regclass($1)
		ORDER BY c.relname`, step.table)
	if err != nil {
		return 0, false, err
	}
	total := 0
	for _, partition := range partitions {
		name := pq.QuoteIdentifier(partition)
		statement := fmt.Sprintf(step.sql, name, name)
		n, drained, err := p.drain(ctx, conn, budget-total, func(limit int) (sql.Result, error) {
			return conn.ExecContext(ctx, statement, pq.Array(series), limit)
		})
		total += n
		if err != nil || !drained {
			return total, false, err
		}
	}
	return total, true, nil
}

// drain repeats one bounded delete until a batch comes back short (the table
// is empty for this workspace) or the budget is spent.
func (p *workspacePurger) drain(ctx context.Context, conn *sql.Conn, budget int, exec func(limit int) (sql.Result, error)) (int, bool, error) {
	total := 0
	for {
		limit := p.opts.BatchSize
		if limit > budget-total {
			limit = budget - total
		}
		if limit <= 0 {
			return total, false, nil
		}
		result, err := exec(limit)
		if err != nil {
			return total, false, err
		}
		n, err := result.RowsAffected()
		if err != nil {
			return total, false, err
		}
		total += int(n)
		if p.metrics != nil && n > 0 {
			p.metrics.rows.Add(float64(n))
		}
		if int(n) < limit {
			return total, true, nil
		}
		if err := sleepCtx(ctx, p.pause); err != nil {
			return total, false, err
		}
	}
}

func sleepCtx(ctx context.Context, d time.Duration) error {
	if d <= 0 {
		return ctx.Err()
	}
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}

func queryInt64s(ctx context.Context, conn *sql.Conn, query string, args ...any) ([]int64, error) {
	rows, err := conn.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []int64
	for rows.Next() {
		var v int64
		if err := rows.Scan(&v); err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, rows.Err()
}

func queryStrings(ctx context.Context, conn *sql.Conn, query string, args ...any) ([]string, error) {
	rows, err := conn.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var v string
		if err := rows.Scan(&v); err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, rows.Err()
}
