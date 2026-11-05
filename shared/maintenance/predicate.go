// Package maintenance owns maintenance windows (docs/state-semantics.md S-M):
// the recurrence rule and its expansion (rule.go), the materialized
// occurrences (store.go), and the SQL that every consumer uses to ask "is
// this monitor in maintenance". A window is a rule; consumers read its
// occurrences (maintenance_window_occurrences) through the fragments below,
// never the rule's own starts_at/ends_at (S-M5). The API, alerter, status
// page, scheduler and the interval engine all go through this package, so
// the definition of "in maintenance" lives in exactly one place.
package maintenance

// occurrencesSQL is the one coverage query: a parenthesized relation of the
// occurrences (window_id, starts_at, ends_at) of tenantExpr's windows that
// reach monitorExpr, directly or via a targeted group it belongs to, and
// satisfy timeCond (over alias mo). A window that targets both a monitor and
// its group yields the occurrence twice; every consumer unions or tests for
// existence, so the repeat is harmless.
func occurrencesSQL(tenantExpr, monitorExpr, timeCond string) string {
	return `(
		SELECT mo.window_id, mo.starts_at, mo.ends_at
		FROM maintenance_window_monitors mwm
		JOIN maintenance_window_occurrences mo ON mo.window_id = mwm.maintenance_window_id
		WHERE mo.tenant_id = ` + tenantExpr + `
		  AND ` + timeCond + `
		  AND (mwm.monitor_id = ` + monitorExpr + `
		       OR mwm.monitor_id IN (
		           SELECT mg.group_id FROM monitor_groups mg
		           WHERE mg.monitor_id = ` + monitorExpr + `)))`
}

// OccurrenceRangesSQL returns a parenthesized relation (columns window_id,
// starts_at, ends_at; give it an alias) of the maintenance occurrences that
// reach the monitor monitorExpr of tenant tenantExpr and overlap the
// half-open span [fromExpr, toExpr). All arguments are trusted SQL
// expressions (columns, placeholders, NOW()); the fragment binds nothing, so
// it can be dropped into any query — typically as
// CROSS JOIN LATERAL <fragment> mo — without renumbering placeholders. The
// interval engine (shared/analytics/timeline.go) reads maintenance only
// through it.
func OccurrenceRangesSQL(tenantExpr, monitorExpr, fromExpr, toExpr string) string {
	return occurrencesSQL(tenantExpr, monitorExpr,
		`mo.starts_at < `+toExpr+` AND mo.ends_at > `+fromExpr)
}

// InMaintenancePredicate returns a SQL boolean expression that is TRUE when
// the monitor aliased by monAlias is covered by a maintenance occurrence in
// progress, either directly or via a targeted group it belongs to. The
// expression embeds NOW() and uses no bind parameters, so it can be appended
// to any WHERE clause or SELECT list without renumbering placeholders.
func InMaintenancePredicate(monAlias string) string {
	return InMaintenanceAtPredicate(monAlias, "NOW()")
}

// InMaintenanceAtPredicate uses the same direct/group coverage at a supplied
// trusted SQL time expression, allowing historical incident analysis.
func InMaintenanceAtPredicate(monAlias, timeExpr string) string {
	return `EXISTS (SELECT 1 FROM ` + coveringAt(monAlias, timeExpr) + ` cov)`
}

// MaintenanceUntilExpr returns a SQL expression that evaluates to the latest
// ends_at among the occurrences in progress that cover the monitor aliased
// by monAlias (directly or via group), or NULL when none are.
func MaintenanceUntilExpr(monAlias string) string {
	return `(SELECT MAX(cov.ends_at) FROM ` + coveringAt(monAlias, "NOW()") + ` cov)`
}

func coveringAt(monAlias, timeExpr string) string {
	return occurrencesSQL(monAlias+".tenant_id", monAlias+".id",
		`mo.starts_at <= `+timeExpr+` AND mo.ends_at > `+timeExpr)
}
