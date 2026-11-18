// Package statuspagesql holds the SQL fragments that say what a status page
// shows, for every service that needs to name or select a page's
// components the way the page itself does: the status-page renderer
// (incident and maintenance "affected" lists) and the API (subscriber mail,
// the notification outbox and automatic publication).
package statuspagesql

// PublicNameSQL is the name the page pageExpr shows for the monitor aliased
// by monAlias: its section display name, else its legacy flat-layout display
// name, else the monitor's own name, trimmed. Affected lists use it so they
// never print an internal name the component list hides. Both arguments are
// trusted SQL (an alias and a placeholder or column).
func PublicNameSQL(pageExpr, monAlias string) string {
	return `COALESCE((
		SELECT NULLIF(btrim(dn.display_name), '')
		FROM (
			SELECT spsm.display_name, 0 AS priority
			FROM status_page_section_monitors spsm
			JOIN status_page_sections sps ON sps.id = spsm.section_id
			WHERE sps.status_page_id = ` + pageExpr + ` AND spsm.monitor_id = ` + monAlias + `.id
			UNION ALL
			SELECT spm.display_name, 1 AS priority
			FROM status_page_monitors spm
			WHERE spm.status_page_id = ` + pageExpr + ` AND spm.monitor_id = ` + monAlias + `.id
		) dn
		ORDER BY dn.priority
		LIMIT 1
	), btrim(` + monAlias + `.name))`
}

// RenderedMonitorsSQL is a parenthesized relation (columns status_page_id,
// monitor_id; give it an alias) of the monitors each page renders: its
// section monitors and its legacy flat list. Deliberately not a walk up
// through monitor_groups: a page that lists a group does not show its
// members, so nothing about a member may be announced on it.
const RenderedMonitorsSQL = `(
	SELECT sps.status_page_id, spsm.monitor_id
	FROM status_page_sections sps
	JOIN status_page_section_monitors spsm ON spsm.section_id = sps.id
	UNION
	SELECT spm.status_page_id, spm.monitor_id
	FROM status_page_monitors spm
)`
