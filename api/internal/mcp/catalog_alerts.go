package mcp

import (
	"net/http"

	alerthandlers "github.com/yassinebenameur/probara/api/internal/handlers/alerts"
	"github.com/yassinebenameur/probara/api/internal/models"
)

func alertTools() []spec {
	return []spec{
		{
			Name:  "list_alerts",
			Title: "List alerts",
			Description: "Alerts, newest first. \"active\" = firing and unacknowledged; \"open\" in the UI means " +
				"active + acknowledged. Each alert carries its monitor, routing, suppression reason and AI triage.",
			Method: http.MethodGet, Route: "/alerts",
			Query: withPaging(
				param{Name: "status", Enum: []string{"active", "acknowledged", "resolved"}, Description: "Only alerts in this state."},
				param{Name: "monitor_id", Description: "Only this monitor's alerts."},
				param{Name: "since", Description: "RFC 3339; alerts open at any point after it."},
				param{Name: "suppressed", Type: "boolean", Description: "true: only alerts whose notifications are suppressed (e.g. upstream dependency down); false: only unsuppressed."},
				param{Name: "sort", Enum: []string{"triggered_at", "status", "monitor", "duration"}, Description: "Order (default triggered_at): status ranks active, acknowledged, resolved; monitor is the subject name; duration counts open alerts to now."},
				param{Name: "dir", Enum: []string{"asc", "desc"}, Description: "Direction; default desc for triggered_at and duration, asc otherwise."},
			),
		},
		{
			Name:        "list_recent_alerts",
			Title:       "List recent alerts",
			Description: "The latest alerts, unpaginated.",
			Method:      http.MethodGet, Route: "/alerts/recent",
			Query: []param{{Name: "limit", Type: "integer", Description: "Maximum, 1-50 (default 10)."}},
		},
		{
			Name:        "get_alert_counts_by_policy",
			Title:       "Get alert counts by policy (legacy)",
			Description: "Legacy: active alert counts per retired alert policy. Prefer list_alerts.",
			Method:      http.MethodGet, Route: "/alerts/counts/by-policy",
		},
		{
			Name:        "get_monitor_counts_by_policy",
			Title:       "Get monitor counts by policy (legacy)",
			Description: "Legacy: monitor counts per retired alert policy.",
			Method:      http.MethodGet, Route: "/alerts/counts/monitors-by-policy",
		},
		{
			Name:  "get_alert",
			Title: "Get alert",
			Description: "One alert in full, including its AI triage (suspected cause, evidence) when the local " +
				"model has produced one, and the labelling context for label_alert_triage.",
			Method: http.MethodGet, Route: "/alerts/{id}",
			PathDocs: map[string]string{"id": "Alert ID."},
		},
		{
			Name:  "acknowledge_alert",
			Title: "Acknowledge alert",
			Description: "Acknowledge an active alert: stops reminders and tells paging channels (PagerDuty, " +
				"Opsgenie) to stop escalating. Only active alerts can be acknowledged.",
			Method: http.MethodPost, Route: "/alerts/{id}/acknowledge",
			PathDocs: map[string]string{"id": "Alert ID."},
		},
		{
			Name:  "resolve_alert",
			Title: "Resolve alert",
			Description: "Resolve an active or acknowledged alert by hand. Channels that were paged receive a real " +
				"\"resolved\" notification. If the monitor is still failing, a new alert can open.",
			Method: http.MethodPost, Route: "/alerts/{id}/resolve",
			PathDocs: map[string]string{"id": "Alert ID."},
		},
		{
			Name:  "bulk_acknowledge_alerts",
			Title: "Acknowledge alerts in bulk",
			Description: "Acknowledge many active alerts at once (up to 500 IDs). Never all-or-nothing: items are the " +
				"alerts acknowledged, failed lists the others with a reason (not_found, already_acknowledged, " +
				"already_resolved). Each alert is audited like acknowledge_alert.",
			Method: http.MethodPost, Route: "/alerts/bulk/acknowledge",
			Body: typeOf[alerthandlers.BulkAlertsRequest](), Required: []string{"ids"},
			FieldDocs: map[string]string{"ids": "Alert IDs, 1-500."},
		},
		{
			Name:  "bulk_resolve_alerts",
			Title: "Resolve alerts in bulk",
			Description: "Resolve many active or acknowledged alerts at once (up to 500 IDs), as resolve_alert does for " +
				"one. Never all-or-nothing: items are the alerts resolved, failed lists the others with a reason " +
				"(not_found, already_resolved).",
			Method: http.MethodPost, Route: "/alerts/bulk/resolve",
			Body: typeOf[alerthandlers.BulkAlertsRequest](), Required: []string{"ids"},
			FieldDocs: map[string]string{"ids": "Alert IDs, 1-500."},
		},
		{
			Name:  "label_alert_triage",
			Title: "Label alert triage",
			Description: "Record an operator verdict on an alert's AI triage (training data for the local model): " +
				"confirmed, or corrected with the right label. Take decision IDs and choices from the alert's " +
				"triage.label_context (get_alert). A correction stops pending suspected-cause follow-ups.",
			Method: http.MethodPost, Route: "/alerts/{id}/triage/label",
			PathDocs: map[string]string{"id": "Alert ID."},
			Body:     typeOf[models.AlertTriageLabelRequest](), Required: []string{"verdict"},
			Enums: map[string][]string{"verdict": {"confirmed", "corrected"}},
			FieldDocs: map[string]string{
				"label":   "Alert-level catalogue label; required when verdict is \"corrected\".",
				"answers": "Decision ID -> choice key, from triage.label_context.",
			},
		},
	}
}

func incidentTools() []spec {
	incident := map[string]string{"id": "Incident ID."}
	return []spec{
		{
			Name:        "list_incidents",
			Title:       "List incidents",
			Description: "Incidents, most recently updated first, with open_total.",
			Method:      http.MethodGet, Route: "/incidents",
			Query: pageParams,
		},
		{
			Name:  "create_incident",
			Title: "Create incident",
			Description: "Open an incident (state investigating), optionally linked to an alert and a monitor. Link " +
				"more with attach_alert_to_incident / attach_monitor_to_incident.",
			Method: http.MethodPost, Route: "/incidents",
			Body: typeOf[models.CreateIncidentRequest](), Required: []string{"title", "summary"},
			Enums: map[string][]string{"severity": {"critical", "high", "medium", "low"}},
			FieldDocs: map[string]string{
				"severity":      "Default high.",
				"owner_user_id": "User ID of the owner (optional).",
				"alert_id":      "Alert to link (optional).",
				"monitor_id":    "Monitor to link (optional).",
			},
		},
		{
			Name:        "get_incident",
			Title:       "Get incident",
			Description: "One incident with its linked alerts and monitors, status-page publications, timeline and latest AI analysis.",
			Method:      http.MethodGet, Route: "/incidents/{id}",
			PathDocs: incident,
		},
		{
			Name:        "update_incident",
			Title:       "Update incident",
			Description: "Change title, summary, severity or owner (owner_user_id \"\" clears it). Use transition_incident_state for the state.",
			Method:      http.MethodPatch, Route: "/incidents/{id}",
			PathDocs: incident,
			Body:     typeOf[models.UpdateIncidentRequest](),
			Enums:    map[string][]string{"severity": {"critical", "high", "medium", "low"}},
		},
		{
			Name:        "transition_incident_state",
			Title:       "Transition incident state",
			Description: "Move an incident through investigating -> identified -> monitoring -> resolved. Resolved incidents cannot be reopened.",
			Method:      http.MethodPost, Route: "/incidents/{id}/state",
			PathDocs: incident,
			Body:     typeOf[models.TransitionIncidentStateRequest](), Required: []string{"state"},
			Enums: map[string][]string{"state": {"investigating", "identified", "monitoring", "resolved"}},
		},
		{
			Name:  "add_incident_timeline_entry",
			Title: "Add incident timeline entry",
			Description: "Add a timeline entry. internal_note stays private; public_update is shown PUBLICLY on every " +
				"status page the incident is published to.",
			Method: http.MethodPost, Route: "/incidents/{id}/timeline",
			PathDocs: incident,
			Body:     typeOf[models.CreateIncidentTimelineEntryRequest](), Required: []string{"entry_type", "message"},
			Enums: map[string][]string{"entry_type": {"internal_note", "public_update"}},
		},
		{
			Name:        "attach_alert_to_incident",
			Title:       "Attach alert to incident",
			Description: "Link an alert to an incident.",
			Method:      http.MethodPost, Route: "/incidents/{id}/alerts",
			PathDocs: incident,
			Body:     typeOf[models.AttachIncidentAlertRequest](), Required: []string{"alert_id"},
		},
		{
			Name:        "detach_alert_from_incident",
			Title:       "Detach alert from incident",
			Description: "Unlink an alert from an incident.",
			Method:      http.MethodDelete, Route: "/incidents/{id}/alerts/{alertId}",
			PathDocs: map[string]string{"id": "Incident ID.", "alertId": "Alert ID."},
		},
		{
			Name:        "attach_monitor_to_incident",
			Title:       "Attach monitor to incident",
			Description: "Link an affected monitor to an incident.",
			Method:      http.MethodPost, Route: "/incidents/{id}/monitors",
			PathDocs: incident,
			Body:     typeOf[models.AttachIncidentMonitorRequest](), Required: []string{"monitor_id"},
		},
		{
			Name:        "detach_monitor_from_incident",
			Title:       "Detach monitor from incident",
			Description: "Unlink a monitor from an incident.",
			Method:      http.MethodDelete, Route: "/incidents/{id}/monitors/{monitorId}",
			PathDocs: map[string]string{"id": "Incident ID.", "monitorId": "Monitor ID."},
		},
		{
			Name:  "publish_incident_to_status_page",
			Title: "Publish incident to status page",
			Description: "Show the incident PUBLICLY on a status page, optionally naming affected monitors (each " +
				"must be linked to both the incident and the page). Republishing replaces the monitor list.",
			Method: http.MethodPut, Route: "/incidents/{id}/status-pages/{statusPageId}",
			PathDocs: map[string]string{"id": "Incident ID.", "statusPageId": "Status page ID."},
			Body:     typeOf[models.UpsertIncidentPublicationRequest](),
		},
		{
			Name:        "unpublish_incident_from_status_page",
			Title:       "Unpublish incident",
			Description: "Remove the incident from a status page. Idempotent.",
			Method:      http.MethodDelete, Route: "/incidents/{id}/status-pages/{statusPageId}",
			PathDocs: map[string]string{"id": "Incident ID.", "statusPageId": "Status page ID."},
		},
		{
			Name:  "request_incident_ai_analysis",
			Title: "Request incident AI analysis",
			Description: "Queue a root-cause analysis by the local AI model over the incident's probe evidence. " +
				"Returns a pending analysis; poll get_incident_ai_analysis until status is ready or failed. " +
				"503 when local AI is off.",
			Method: http.MethodPost, Route: "/incidents/{id}/ai-analysis",
			PathDocs: incident,
		},
		{
			Name:        "get_incident_ai_analysis",
			Title:       "Get incident AI analysis",
			Description: "The newest AI analysis of an incident (null if none was requested).",
			Method:      http.MethodGet, Route: "/incidents/{id}/ai-analysis",
			PathDocs: incident,
		},
		{
			Name:  "label_incident_ai_analysis",
			Title: "Label incident AI analysis",
			Description: "Record an operator verdict on an incident analysis (training data): confirmed, or " +
				"corrected with incident_label. Decision IDs and choices come from ai_analysis.label_context.",
			Method: http.MethodPost, Route: "/incidents/{id}/ai-analysis/label",
			PathDocs: incident,
			Body:     typeOf[models.IncidentAILabelRequest](), Required: []string{"verdict"},
			Enums: map[string][]string{"verdict": {"confirmed", "corrected"}},
			FieldDocs: map[string]string{
				"analysis_id":    "Analysis to label (default: the newest ready one).",
				"incident_label": "Required when verdict is \"corrected\".",
				"answers":        "Decision ID -> choice key, from ai_analysis.label_context.",
			},
		},
	}
}
