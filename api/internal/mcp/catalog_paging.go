package mcp

import (
	"net/http"

	alerthandlers "github.com/yassinebenameur/probara/api/internal/handlers/alerts"
)

// pagingTools covers the paging actions on alerts: unacknowledge and
// per-alert snooze.
func pagingTools() []spec {
	alert := map[string]string{"id": "Alert ID."}
	return []spec{
		{
			Name:  "unacknowledge_alert",
			Title: "Unacknowledge alert",
			Description: "Return an acknowledged alert to active. Paging resumes: unfired escalation tiers and " +
				"reminders count again from now (nothing fires at once). Providers already told about the ack " +
				"(PagerDuty, Opsgenie) are not un-acknowledged; a later acknowledge reaches them again.",
			Method: http.MethodPost, Route: "/alerts/{id}/unacknowledge",
			PathDocs: alert,
		},
		{
			Name:  "bulk_unacknowledge_alerts",
			Title: "Unacknowledge alerts in bulk",
			Description: "Unacknowledge many alerts at once (up to 500 IDs). Never all-or-nothing: items are the " +
				"alerts returned to active, failed lists the others with a reason (not_found, not_acknowledged, " +
				"already_resolved).",
			Method: http.MethodPost, Route: "/alerts/bulk/unacknowledge",
			Body: typeOf[alerthandlers.BulkAlertsRequest](), Required: []string{"ids"},
			FieldDocs: map[string]string{"ids": "Alert IDs, 1-500."},
		},
		{
			Name:  "snooze_alert",
			Title: "Snooze alert",
			Description: "Mute an open alert's escalation tiers, reminders and suspected-cause follow-ups until a " +
				"time (1 minute to 7 days ahead); replaces a running snooze. Unlike snoozing the monitor it opens " +
				"no maintenance window, so SLA accounting is untouched. Resolve and acknowledge still notify.",
			Method: http.MethodPost, Route: "/alerts/{id}/snooze",
			PathDocs: alert,
			Body:     typeOf[alerthandlers.SnoozeAlertRequest](),
			FieldDocs: map[string]string{
				"minutes": "Snooze length from now, in minutes. Give this or until.",
				"until":   "RFC 3339 end of the snooze. Give this or minutes.",
			},
		},
		{
			Name:  "unsnooze_alert",
			Title: "Unsnooze alert",
			Description: "End an alert's snooze now. Paging resumes with its clocks counted from now, as after an " +
				"unacknowledge.",
			Method: http.MethodPost, Route: "/alerts/{id}/unsnooze",
			PathDocs: alert,
		},
	}
}
