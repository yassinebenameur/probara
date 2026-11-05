package mcp

import (
	"net/http"

	escalationservice "github.com/yassinebenameur/probara/api/internal/services/escalation"
)

// escalationTools are the escalation policy tools (people layer).
func escalationTools() []spec {
	policy := map[string]string{"id": "Escalation policy ID, from list_escalation_policies."}
	docs := map[string]string{
		"name":        "Policy name, unique in the workspace (1-100 characters).",
		"description": "Optional description (up to 500 characters).",
		"repeat_count": "How many more times to walk the person steps after the last step while the alert stays " +
			"active and unacknowledged (0-10). Channel targets follow the workspace reminders instead.",
		"repeat_after_seconds": "Pause after the last step before a repeat walk (60-86400, default 1800).",
		"steps": "Steps in order (1-10). Each has delay_seconds (0-604800, counted from when the alert started paging; " +
			"never shorter than the previous step's) and 1-20 targets: {kind: user, user_id} (a workspace member, " +
			"from list_oncall_people), {kind: schedule, schedule_id} (whoever list_oncall_schedules has on call when " +
			"the step fires), {kind: all_admins} (every workspace admin), or {kind: channel, channel_id, " +
			"notify_after_ack} (an alert channel, paged like a routing tier with reminders; a channel may be in " +
			"one step only). People are paged at their verified contacts (email; SMS through the workspace's " +
			"personal SMS channel).",
	}
	return []spec{
		{
			Name:  "list_escalation_policies",
			Title: "List escalation policies",
			Description: "List the workspace's escalation policies with their steps and targets, how many monitors use " +
				"each (monitor_count), which is the workspace default (tenant_default), and per target whether it " +
				"can reach anybody now (reachable).",
			Method: http.MethodGet, Route: "/escalation-policies",
		},
		{
			Name:        "get_escalation_policy",
			Title:       "Get escalation policy",
			Description: "Get one escalation policy with its steps and targets.",
			Method:      http.MethodGet, Route: "/escalation-policies/{id}",
			PathDocs: policy,
		},
		{
			Name:  "create_escalation_policy",
			Title: "Create escalation policy",
			Description: "Create an escalation policy. Assign it to a monitor with notification_mode \"policy\" and " +
				"escalation_policy_id (update_monitor), or make it the workspace default " +
				"(update_notification_settings default_escalation_policy_id). Steps page their people while the " +
				"alert is active: acknowledging or snoozing it stops the walk.",
			Method: http.MethodPost, Route: "/escalation-policies",
			Body:      typeOf[escalationservice.WriteRequest](),
			Required:  []string{"name", "steps"},
			FieldDocs: docs,
		},
		{
			Name:        "update_escalation_policy",
			Title:       "Update escalation policy",
			Description: "Replace an escalation policy's name, description, repeat settings and steps (send them all).",
			Method:      http.MethodPut, Route: "/escalation-policies/{id}",
			PathDocs:  policy,
			Body:      typeOf[escalationservice.WriteRequest](),
			Required:  []string{"name", "steps"},
			FieldDocs: docs,
		},
		{
			Name:  "delete_escalation_policy",
			Title: "Delete escalation policy",
			Description: "Delete an escalation policy. Refused (409) while a monitor uses it or it is the workspace " +
				"default.",
			Method: http.MethodDelete, Route: "/escalation-policies/{id}",
			PathDocs: policy, Destructive: true,
		},
		{
			Name:  "convert_routes_to_escalation_policy",
			Title: "Convert routes to an escalation policy",
			Description: "Build an escalation policy equivalent to existing channel routes: a monitor's own routes " +
				"(monitor_id, notification_mode \"custom\") or, without monitor_id, the workspace default routes. " +
				"Routes sharing a delay become one step. With assign, the monitor switches to the new policy in the " +
				"same change (a workspace policy is made the default through update_notification_settings).",
			Method: http.MethodPost, Route: "/escalation-policies/convert",
			Body: typeOf[escalationservice.ConvertRequest](),
			FieldDocs: map[string]string{
				"monitor_id": "Monitor whose custom routes to convert; omit for the workspace default routes.",
				"name":       "Optional policy name (default \"<monitor> routes\" or \"Workspace default routes\").",
				"assign":     "Also switch the monitor to notification_mode \"policy\" with the new policy.",
			},
		},
	}
}
