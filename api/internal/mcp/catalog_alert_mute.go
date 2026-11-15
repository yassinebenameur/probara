package mcp

import (
	"net/http"

	alerthandlers "github.com/yassinebenameur/probara/api/internal/handlers/alerts"
	"github.com/yassinebenameur/probara/api/internal/models"
)

// alertMuteTools are the ways out of and around the acknowledge mute: an
// acknowledged or snoozed alert sends no reminder and no further escalation.
func alertMuteTools() []spec {
	snoozeDocs := map[string]string{
		"duration_minutes": "Snooze length in minutes (1 minute to 7 days).",
		"until":            "Snooze end (RFC 3339, 1 minute to 7 days from now).",
	}
	return []spec{
		{
			Name:  "unacknowledge_alert",
			Title: "Unacknowledge alert",
			Description: "Return an acknowledged alert to active. Its reminders and escalation tiers that had not " +
				"fired yet resume, counted from now. Paging providers already told about the acknowledgement " +
				"(PagerDuty, Opsgenie) stay acknowledged there.",
			Method: http.MethodPost, Route: "/alerts/{id}/unacknowledge",
			PathDocs: map[string]string{"id": "Alert ID."},
		},
		{
			Name:  "snooze_alert",
			Title: "Snooze alert",
			Description: "Mute an open alert's reminders and not-yet-fired escalation tiers until a time, without " +
				"acknowledging it. The resolve notice is still sent. Replaces a running snooze; when it ends, " +
				"notifications resume counted from then. Send exactly one of duration_minutes or until.",
			Method: http.MethodPost, Route: "/alerts/{id}/snooze",
			PathDocs:  map[string]string{"id": "Alert ID."},
			Body:      typeOf[models.SnoozeAlertRequest](),
			FieldDocs: snoozeDocs,
		},
		{
			Name:        "unsnooze_alert",
			Title:       "Unsnooze alert",
			Description: "End an open alert's running snooze now. Notifications resume counted from now.",
			Method:      http.MethodPost, Route: "/alerts/{id}/unsnooze",
			PathDocs: map[string]string{"id": "Alert ID."},
		},
		{
			Name:  "bulk_unacknowledge_alerts",
			Title: "Unacknowledge alerts in bulk",
			Description: "Unacknowledge many alerts at once (up to 500 IDs), as unacknowledge_alert does for one. " +
				"Never all-or-nothing: items are the alerts changed, failed lists the others with a reason " +
				"(not_found, not_acknowledged, already_resolved).",
			Method: http.MethodPost, Route: "/alerts/bulk/unacknowledge",
			Body: typeOf[alerthandlers.BulkAlertsRequest](), Required: []string{"ids"},
			FieldDocs: map[string]string{"ids": "Alert IDs, 1-500."},
		},
		{
			Name:  "bulk_snooze_alerts",
			Title: "Snooze alerts in bulk",
			Description: "Snooze many open alerts at once (up to 500 IDs), as snooze_alert does for one. Never " +
				"all-or-nothing: failed lists the others with a reason (not_found, already_resolved).",
			Method: http.MethodPost, Route: "/alerts/bulk/snooze",
			Body: typeOf[alerthandlers.BulkSnoozeAlertsRequest](), Required: []string{"ids"},
			FieldDocs: map[string]string{
				"ids":              "Alert IDs, 1-500.",
				"duration_minutes": snoozeDocs["duration_minutes"],
				"until":            snoozeDocs["until"],
			},
		},
		{
			Name:  "bulk_unsnooze_alerts",
			Title: "Unsnooze alerts in bulk",
			Description: "End the running snooze of many alerts at once (up to 500 IDs). Never all-or-nothing: " +
				"failed lists the others with a reason (not_found, not_snoozed, already_resolved).",
			Method: http.MethodPost, Route: "/alerts/bulk/unsnooze",
			Body: typeOf[alerthandlers.BulkAlertsRequest](), Required: []string{"ids"},
			FieldDocs: map[string]string{"ids": "Alert IDs, 1-500."},
		},
	}
}
