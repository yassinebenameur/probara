// Package maintenance provides the shared SQL predicate that determines
// whether a monitor is currently covered by an active maintenance window.
// It is used by the API, alerter, and status-page services so the definition
// of "in maintenance" lives in exactly one place.
package maintenance

// InMaintenancePredicate returns a SQL boolean expression that is TRUE when
// the monitor aliased by monAlias is covered by an active maintenance window,
// either directly or via a targeted group it belongs to. The expression embeds
// NOW() and uses no bind parameters, so it can be appended to any WHERE clause
// or SELECT list without renumbering placeholders.
func InMaintenancePredicate(monAlias string) string {
	return `EXISTS (
		SELECT 1
		FROM maintenance_window_monitors mwm
		JOIN maintenance_windows mw ON mw.id = mwm.maintenance_window_id
		WHERE mw.tenant_id = ` + monAlias + `.tenant_id
		  AND NOW() >= mw.starts_at
		  AND NOW() < mw.ends_at
		  AND (mwm.monitor_id = ` + monAlias + `.id
		       OR mwm.monitor_id IN (
		           SELECT mg.group_id FROM monitor_groups mg
		           WHERE mg.monitor_id = ` + monAlias + `.id)))`
}

// MaintenanceUntilExpr returns a SQL expression that evaluates to the latest
// ends_at among active maintenance windows covering the monitor aliased by
// monAlias (directly or via group), or NULL when none are active.
func MaintenanceUntilExpr(monAlias string) string {
	return `(
		SELECT MAX(mw.ends_at)
		FROM maintenance_window_monitors mwm
		JOIN maintenance_windows mw ON mw.id = mwm.maintenance_window_id
		WHERE mw.tenant_id = ` + monAlias + `.tenant_id
		  AND NOW() >= mw.starts_at
		  AND NOW() < mw.ends_at
		  AND (mwm.monitor_id = ` + monAlias + `.id
		       OR mwm.monitor_id IN (
		           SELECT mg.group_id FROM monitor_groups mg
		           WHERE mg.monitor_id = ` + monAlias + `.id)))`
}
