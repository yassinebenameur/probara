// Package maintenance owns maintenance windows (docs/state-semantics.md
// S-M): the weekly recurrence rule and its expansion (rule.go), the
// materialized occurrences and the writers that keep them (store.go), and
// the SQL every consumer uses to ask "is this monitor in maintenance". A
// window is a schedule; what covers monitors is its occurrences
// (maintenance_window_occurrences), read only through the fragments below,
// never the window's own starts_at/ends_at (S-M5). The API, alerter, status
// page, scheduler, worker and the interval engine all go through this
// package, so the definition of "in maintenance" lives in exactly one place.
package maintenance

// occurrencesSQL is the one coverage query: a parenthesized relation of the
// occurrences of tenantExpr's windows that reach monitorExpr, directly or
// via a targeted group it belongs to, and satisfy timeCond (applied to the
// alias it is given). Columns: window_id, occurrence_starts_at (the
// occurrence's own start), starts_at and ends_at (the span it covers this
// monitor).
//
// Each occurrence is clipped to the time the target link reaching the
// monitor was in force (S-M5): current links from linked_at on, removed
// ones (maintenance_window_past_targets) over [linked_at, unlinked_at). So a
// target edit, like a schedule edit, takes effect when it is saved and never
// rewrites what an earlier occurrence covered. Group membership is read as
// it is now. Skipped occurrences cover nothing. timeCond is applied to the
// raw occurrence (alias mwo, for the index) and to the clipped span (alias
// mo).
//
// A window that targets both a monitor and its group yields the occurrence
// twice, and a monitor removed and re-added yields one piece per link;
// every consumer unions or tests for existence, so the repeats are harmless.
func occurrencesSQL(tenantExpr, monitorExpr string, timeCond func(alias string) string) string {
	return `(
		SELECT mo.window_id, mo.occurrence_starts_at, mo.starts_at, mo.ends_at
		FROM (
			SELECT mwo.window_id, mwo.starts_at AS occurrence_starts_at,
				GREATEST(mwo.starts_at, mwt.valid_from) AS starts_at,
				LEAST(mwo.ends_at, mwt.valid_to) AS ends_at
			FROM (
				SELECT maintenance_window_id, monitor_id, linked_at AS valid_from,
					'infinity'::timestamptz AS valid_to
				FROM maintenance_window_monitors
				UNION ALL
				SELECT maintenance_window_id, monitor_id, linked_at, unlinked_at
				FROM maintenance_window_past_targets
			) mwt
			JOIN maintenance_window_occurrences mwo ON mwo.window_id = mwt.maintenance_window_id
			WHERE mwo.tenant_id = ` + tenantExpr + `
			  AND NOT mwo.skipped
			  AND ` + timeCond("mwo") + `
			  AND mwo.starts_at < mwt.valid_to AND mwo.ends_at > mwt.valid_from
			  AND (mwt.monitor_id = ` + monitorExpr + `
			       OR mwt.monitor_id IN (
			           SELECT mg.group_id FROM monitor_groups mg
			           WHERE mg.monitor_id = ` + monitorExpr + `))
		) mo
		WHERE ` + timeCond("mo") + `)`
}

// OccurrenceRangesSQL returns a parenthesized relation (columns window_id,
// occurrence_starts_at, starts_at, ends_at; give it an alias) of the
// maintenance occurrences that reach the monitor monitorExpr of tenant
// tenantExpr and overlap the half-open span [fromExpr, toExpr); starts_at
// and ends_at are the covered span. All arguments are trusted SQL
// expressions (columns, placeholders, NOW()); the fragment binds nothing, so
// it can be dropped into any query — typically as
// CROSS JOIN LATERAL <fragment> mo — without renumbering placeholders. The
// interval engine (shared/analytics/timeline.go) and the status page read
// maintenance ranges only through it.
func OccurrenceRangesSQL(tenantExpr, monitorExpr, fromExpr, toExpr string) string {
	return occurrencesSQL(tenantExpr, monitorExpr, func(a string) string {
		return a + `.starts_at < ` + toExpr + ` AND ` + a + `.ends_at > ` + fromExpr
	})
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
	return occurrencesSQL(monAlias+".tenant_id", monAlias+".id", func(a string) string {
		return a + `.starts_at <= ` + timeExpr + ` AND ` + a + `.ends_at > ` + timeExpr
	})
}
