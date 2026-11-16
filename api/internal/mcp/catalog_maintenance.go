package mcp

import (
	"net/http"

	"github.com/yassinebenameur/probara/api/internal/models"
)

// maintenanceRecurrenceDoc documents the recurrence object shared by
// create, update and preview.
const maintenanceRecurrenceDoc = "Weekly repeat rule; omit for a one-off window. {frequency: \"weekly\" " +
	"(\"none\" on update makes a repeating window a one-off again, with starts_at and ends_at), " +
	"weekdays: 0=Sunday..6=Saturday (all seven is daily), start_time: local \"HH:MM\", " +
	"duration_minutes: 1-10080, timezone: IANA zone the weekdays and start time are read in (DST aware: " +
	"a start inside a spring-forward gap runs when the gap ends, a repeated hour runs once at its first instance), " +
	"until: optional last local date \"YYYY-MM-DD\"}. A repeating window takes no starts_at/ends_at. " +
	"Edits apply from the moment they are saved; occurrences that already started are never rewritten."

// maintenanceOccurrenceTools covers recurring maintenance: previewing a
// rule, listing a window's occurrences, ending or skipping one.
func maintenanceOccurrenceTools() []spec {
	window := map[string]string{"id": "Maintenance window ID."}
	return []spec{
		{
			Name:  "preview_maintenance_recurrence",
			Title: "Preview maintenance recurrence",
			Description: "The next occurrences of a weekly maintenance rule, computed without saving anything. " +
				"Use it to check a rule (weekdays, timezone, DST) before create_maintenance_window.",
			Method: http.MethodPost, Route: "/maintenance-windows/preview",
			Body:     typeOf[models.PreviewMaintenanceRecurrenceRequest](),
			Required: []string{"recurrence"},
			FieldDocs: map[string]string{
				"recurrence": maintenanceRecurrenceDoc,
				"count":      "How many occurrences to return (default 5, max 20).",
			},
		},
		{
			Name:  "list_maintenance_occurrences",
			Title: "List maintenance occurrences",
			Description: "A maintenance window's concrete occurrences, each active, upcoming, past or skipped. " +
				"upcoming (default) lists the one in progress and those ahead, soonest first; past lists ended ones, " +
				"most recent first. Occurrences are materialized 35 days ahead.",
			Method: http.MethodGet, Route: "/maintenance-windows/{id}/occurrences",
			PathDocs: window,
			Query: withPaging(
				param{Name: "status", Enum: []string{"upcoming", "past"}, Description: "Which occurrences (default upcoming)."},
			),
		},
		{
			Name:  "end_maintenance_occurrence",
			Title: "End maintenance occurrence",
			Description: "End a maintenance window's occurrence in progress now; alerts for its monitors resume. " +
				"A repeating window keeps its schedule and runs again at its next occurrence. Fails when nothing is in progress.",
			Method: http.MethodPost, Route: "/maintenance-windows/{id}/end-occurrence",
			PathDocs: window,
		},
		{
			Name:  "skip_next_maintenance_occurrence",
			Title: "Skip next maintenance occurrence",
			Description: "Skip a repeating maintenance window's next occurrence that has not started; the series " +
				"carries on after it. Fails for a one-off window or when nothing is ahead.",
			Method: http.MethodPost, Route: "/maintenance-windows/{id}/skip-next",
			PathDocs: window,
		},
	}
}
