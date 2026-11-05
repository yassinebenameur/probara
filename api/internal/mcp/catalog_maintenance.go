package mcp

import (
	"net/http"

	"github.com/yassinebenameur/probara/api/internal/models"
)

// maintenanceRecurrenceDoc documents the nested recurrence object shared by
// create, update and preview.
const maintenanceRecurrenceDoc = "Repeat rule; omit for a one-off. {frequency: none|daily|weekly|monthly|cron, " +
	"interval: every N days/weeks/months (default 1), weekdays: weekly days 0=Sunday..6 (default starts_at's), " +
	"month_day: monthly day 1-31, clamped to short months (default starts_at's), cron: 5-field expression, " +
	"timezone: IANA zone the wall-clock times are read in (default UTC), until: RFC 3339, no occurrence starts after it}. " +
	"starts_at/ends_at give the first occurrence: its time of day, first date and duration. Edits never change " +
	"occurrences that have ended."

// maintenanceRecurrenceTools covers recurring maintenance: previewing a
// rule's occurrences before saving it.
func maintenanceRecurrenceTools() []spec {
	return []spec{
		{
			Name:  "preview_maintenance_window",
			Title: "Preview maintenance window",
			Description: "The next occurrences of a maintenance schedule, computed without saving anything. " +
				"Use it to check a repeat rule (DST, short months, cron) before create_maintenance_window.",
			Method: http.MethodPost, Route: "/maintenance-windows/preview",
			Body:     typeOf[models.PreviewMaintenanceWindowRequest](),
			Required: []string{"starts_at", "ends_at"},
			FieldDocs: map[string]string{
				"starts_at":  "First occurrence start (RFC 3339).",
				"ends_at":    "First occurrence end (RFC 3339); ends_at - starts_at is every occurrence's duration.",
				"recurrence": maintenanceRecurrenceDoc,
				"count":      "How many occurrences to return (default 10, max 50).",
			},
		},
		{
			Name:  "end_maintenance_occurrence",
			Title: "End maintenance occurrence",
			Description: "End a maintenance window's occurrence in progress now; alerts for its monitors resume. " +
				"The window's rule is unchanged, so a repeating window runs again at its next occurrence. " +
				"Fails when no occurrence is in progress.",
			Method: http.MethodPost, Route: "/maintenance-windows/{id}/end",
			PathDocs: map[string]string{"id": "Maintenance window ID."},
		},
	}
}
