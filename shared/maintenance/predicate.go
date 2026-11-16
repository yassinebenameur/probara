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
// it is now. Skipped occurrences cover nothing.
//
// The shape is what keeps it cheap as history grows: it starts from the
// monitor's own links (four index lookups on monitor_id: direct and via its
// groups, current and removed) and only then reads each window's
// occurrences through idx_maintenance_window_occurrences_window_end, whose
// ends_at bound skips everything that ended before the instant or range
// asked about. Do not fold the branches into one "monitor_id = X OR IN
// (groups)" filter: the planner then starts from the occurrence table and
// reads a tenant's whole history once per monitor row. timeCond is applied
// to the raw occurrence (alias mwo, for the index) and to the clipped span
// (alias mo).
//
// A window that targets both a monitor and its group yields the occurrence
// twice, and a monitor removed and re-added yields one piece per link;
// every consumer unions or tests for existence, so the repeats are harmless.
func occurrencesSQL(tenantExpr, monitorExpr string, timeCond func(alias string) string) string {
	return `(
		SELECT mo.window_id, mo.occurrence_starts_at, mo.starts_at, mo.ends_at
		FROM (
			SELECT mwt.window_id, mwo.starts_at AS occurrence_starts_at,
				GREATEST(mwo.starts_at, mwt.valid_from) AS starts_at,
				LEAST(mwo.ends_at, mwt.valid_to) AS ends_at
			FROM (
				SELECT l.maintenance_window_id AS window_id, l.linked_at AS valid_from,
					'infinity'::timestamptz AS valid_to
				FROM maintenance_window_monitors l
				WHERE l.monitor_id = ` + monitorExpr + `
				UNION ALL
				SELECT l.maintenance_window_id, l.linked_at, 'infinity'::timestamptz
				FROM monitor_groups mg
				JOIN maintenance_window_monitors l ON l.monitor_id = mg.group_id
				WHERE mg.monitor_id = ` + monitorExpr + `
				UNION ALL
				SELECT p.maintenance_window_id, p.linked_at, p.unlinked_at
				FROM maintenance_window_past_targets p
				WHERE p.monitor_id = ` + monitorExpr + `
				UNION ALL
				SELECT p.maintenance_window_id, p.linked_at, p.unlinked_at
				FROM monitor_groups mg
				JOIN maintenance_window_past_targets p ON p.monitor_id = mg.group_id
				WHERE mg.monitor_id = ` + monitorExpr + `
			) mwt
			CROSS JOIN LATERAL (
				SELECT o.starts_at, o.ends_at
				FROM maintenance_window_occurrences o
				WHERE o.window_id = mwt.window_id
				  AND o.tenant_id = ` + tenantExpr + `
				  AND NOT o.skipped
				  AND ` + timeCond("o") + `
				  AND o.starts_at < mwt.valid_to AND o.ends_at > mwt.valid_from
			) mwo
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
