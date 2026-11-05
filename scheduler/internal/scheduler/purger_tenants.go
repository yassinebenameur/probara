package scheduler

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/lib/pq"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/sirupsen/logrus"
)

// Tenant phase of the purger: removes soft-deleted workspaces (tenants with
// deleted_at set, see shared/tenancy) once their monitors are gone.
//
// Deleting a workspace in the API tombstones its monitors, so the monitor
// phase drains the per-monitor tables (check_results, rollups, metric store,
// …) first. This phase then drains, in bounded batches, the tenant-scoped
// tables that are not monitor children — alerts without a monitor (mesh
// edges), mesh probe history, incidents, push subscriptions, discovery
// inventory (whose tables carry tenant_id without a tenant FK) and SLA
// reports — and finally deletes the tenant row, whose ON DELETE CASCADE is
// then left with small configuration tables only (channels, status pages,
// locations, keys, settings). TestTenantPurgeCoversEveryTenantTable keeps
// that true as tables are added.
//
// audit_log rows are kept: the audit trail outlives the workspace and has
// its own retention (AUDIT_RETENTION_DAYS).
var tenantPurgeDeletes = []batchedDelete{
	// Mesh-edge and location alerts have no monitor; the cascade removes
	// their notification states, deliveries and triage rows per batch.
	{"alerts", `DELETE FROM alerts WHERE id IN (
		SELECT id FROM alerts WHERE tenant_id = $1 LIMIT $2)`},
	{"mesh_probe_results", `DELETE FROM mesh_probe_results WHERE id IN (
		SELECT id FROM mesh_probe_results WHERE tenant_id = $1 LIMIT $2)`},
	{"incident_timeline_entries", `DELETE FROM incident_timeline_entries WHERE id IN (
		SELECT e.id FROM incident_timeline_entries e
		JOIN incidents i ON i.id = e.incident_id
		WHERE i.tenant_id = $1 LIMIT $2)`},
	{"incidents", `DELETE FROM incidents WHERE id IN (
		SELECT id FROM incidents WHERE tenant_id = $1 LIMIT $2)`},
	{"status_page_push_subscriptions", `DELETE FROM status_page_push_subscriptions WHERE id IN (
		SELECT s.id FROM status_page_push_subscriptions s
		JOIN status_pages sp ON sp.id = s.status_page_id
		WHERE sp.tenant_id = $1 LIMIT $2)`},
	{"status_page_push_deliveries", `DELETE FROM status_page_push_deliveries WHERE id IN (
		SELECT d.id FROM status_page_push_deliveries d
		JOIN status_pages sp ON sp.id = d.status_page_id
		WHERE sp.tenant_id = $1 LIMIT $2)`},
	// Discovery inventory (migration 000098): discovered_services and
	// discovery_monitor_links carry tenant_id with no tenant FK, so they are
	// deleted explicitly, leaves first.
	{"discovered_resources", `DELETE FROM discovered_resources WHERE (integration_id, uid) IN (
		SELECT r.integration_id, r.uid FROM discovered_resources r
		JOIN discovery_integrations i ON i.id = r.integration_id
		WHERE i.tenant_id = $1 LIMIT $2)`},
	{"discovery_monitor_links", `DELETE FROM discovery_monitor_links WHERE (service_id, monitor_id) IN (
		SELECT service_id, monitor_id FROM discovery_monitor_links WHERE tenant_id = $1 LIMIT $2)`},
	{"discovered_services", `DELETE FROM discovered_services WHERE id IN (
		SELECT id FROM discovered_services WHERE tenant_id = $1 LIMIT $2)`},
	{"discovery_integrations", `DELETE FROM discovery_integrations WHERE id IN (
		SELECT id FROM discovery_integrations WHERE tenant_id = $1 LIMIT $2)`},
	{"sla_reports", `DELETE FROM sla_reports WHERE id IN (
		SELECT id FROM sla_reports WHERE tenant_id = $1 LIMIT $2)`},
	{"ai_labels", `DELETE FROM ai_labels WHERE id IN (
		SELECT id FROM ai_labels WHERE tenant_id = $1 LIMIT $2)`},
}

// deletedTenantCutoffs repeat the workspace delete's tombstoning and
// revocations for rows written after it: a request already past the auth
// check when the delete committed can still create a monitor, API key, MCP
// grant or discovery integration in the workspace. A late monitor would keep
// the workspace from ever finishing its purge; late credentials are already
// refused at read time (tenancy.LiveTenantSQL, the auth middleware), and this
// makes the stored state agree.
var deletedTenantCutoffs = []struct{ name, sql string }{
	{"monitors", `UPDATE monitors SET deleted_at = NOW(), updated_at = NOW()
		WHERE deleted_at IS NULL
		  AND tenant_id IN (SELECT id FROM tenants WHERE deleted_at IS NOT NULL)`},
	{"api keys", `UPDATE api_keys SET revoked_at = NOW()
		WHERE revoked_at IS NULL
		  AND tenant_id IN (SELECT id FROM tenants WHERE deleted_at IS NOT NULL)`},
	{"mcp grants", `UPDATE mcp_oauth_grants SET revoked_at = NOW()
		WHERE revoked_at IS NULL
		  AND tenant_id IN (SELECT id FROM tenants WHERE deleted_at IS NOT NULL)`},
	// Same marker as the API's delete: never equal to a hex token digest.
	{"discovery collector tokens", `UPDATE discovery_integrations SET token_hash = 'revoked'
		WHERE token_hash <> 'revoked'
		  AND tenant_id IN (SELECT id FROM tenants WHERE deleted_at IS NOT NULL)`},
}

// cutOffDeletedTenants applies deletedTenantCutoffs. ok is false before the
// soft-delete migration has run (no tenants.deleted_at): the tenant phase is
// then skipped.
func (p *purger) cutOffDeletedTenants(ctx context.Context, conn *sql.Conn) (ok bool, err error) {
	for _, c := range deletedTenantCutoffs {
		result, err := conn.ExecContext(ctx, c.sql)
		if err != nil {
			if isUndefinedColumn(err) {
				return false, nil
			}
			return false, fmt.Errorf("cut off deleted workspaces' %s: %w", c.name, err)
		}
		if n, _ := result.RowsAffected(); n > 0 {
			p.logger.WithFields(logrus.Fields{"rows": n, "kind": c.name}).
				Info("tenant purge: cut off rows written after a workspace delete")
		}
	}
	return true, nil
}

// purgeDeletedTenants purges deleted workspaces whose monitors are all gone,
// oldest first, within rowsBudget.
func (p *purger) purgeDeletedTenants(ctx context.Context, conn *sql.Conn, rowsBudget int) error {
	for rowsBudget > 0 {
		var tenantID uuid.UUID
		err := conn.QueryRowContext(ctx, `
			SELECT t.id FROM tenants t
			WHERE t.deleted_at IS NOT NULL
			  AND NOT EXISTS (SELECT 1 FROM monitors m WHERE m.tenant_id = t.id)
			ORDER BY t.deleted_at ASC
			LIMIT 1
		`).Scan(&tenantID)
		if err != nil {
			if isNoRows(err) {
				return nil
			}
			return fmt.Errorf("select next deleted workspace: %w", err)
		}

		fields := logrus.Fields{"tenant_id": tenantID}
		spent, drained, err := p.runBatchedDeletes(ctx, conn, tenantPurgeDeletes, tenantID, rowsBudget, fields)
		if err != nil {
			if p.metrics != nil {
				p.metrics.errors.With(prometheus.Labels{}).Inc()
			}
			p.logger.WithError(err).WithFields(fields).Error("purge deleted workspace failed; will retry on next run")
			return err
		}
		rowsBudget -= spent
		if !drained {
			p.logger.WithFields(fields).WithField("rows_purged", spent).
				Debug("tenant purge paused at row budget; resuming next tick")
			return nil
		}

		// Everything large is gone: the cascade removes the remaining
		// configuration rows. The guards make this a no-op if a monitor
		// appeared since the claim (the next run tombstones it).
		if _, err := conn.ExecContext(ctx, `
			DELETE FROM tenants
			WHERE id = $1 AND deleted_at IS NOT NULL
			  AND NOT EXISTS (SELECT 1 FROM monitors WHERE tenant_id = $1)
		`, tenantID); err != nil {
			if p.metrics != nil {
				p.metrics.errors.With(prometheus.Labels{}).Inc()
			}
			return fmt.Errorf("delete workspace row: %w", err)
		}
		p.logger.WithFields(fields).WithField("rows_purged", spent).Info("deleted workspace fully purged")
	}
	return nil
}

func isUndefinedColumn(err error) bool {
	var pqErr *pq.Error
	return errors.As(err, &pqErr) && pqErr.Code == "42703"
}
