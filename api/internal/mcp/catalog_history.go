package mcp

import "net/http"

// historyTools cover monitor config version history: every create and
// update records a masked snapshot; these read, compare and restore them.
func historyTools() []spec {
	monitorVersion := map[string]string{
		"id":      "Monitor ID.",
		"version": "Version number, from list_monitor_versions.",
	}
	return []spec{
		{
			Name:  "list_monitor_versions",
			Title: "List monitor versions",
			Description: "List a monitor's config versions, newest first: who changed it (actor, source: api, ui, mcp, " +
				"import, apply, restore, discovery), when, which fields changed and which secret fields were set or " +
				"cleared (never their values). Only the last 100 versions are kept, and history starts at the first " +
				"change recorded (history_starts_at).",
			Method: http.MethodGet, Route: "/monitors/{id}/versions",
			PathDocs: map[string]string{"id": "Monitor ID."},
		},
		{
			Name:        "get_monitor_version",
			Title:       "Get monitor version",
			Description: "One config version with its full snapshot (secrets masked as ***).",
			Method:      http.MethodGet, Route: "/monitors/{id}/versions/{version}",
			PathDocs: monitorVersion,
		},
		{
			Name:  "diff_monitor_version",
			Title: "Diff monitor version",
			Description: "The field changes that lead from another version to this one (before/after per path such as " +
				"config.url). against defaults to the previous version; against=current compares with the live " +
				"monitor, i.e. what a restore of this version would undo.",
			Method: http.MethodGet, Route: "/monitors/{id}/versions/{version}/diff",
			PathDocs: monitorVersion,
			Query: []param{
				{Name: "against", Description: "A version number, or \"current\" for the live monitor (default: the previous version)."},
			},
		},
		{
			Name:  "restore_monitor_version",
			Title: "Restore monitor version",
			Description: "Re-apply a version's configuration (name, config, schedule, alerting, channels, locations, " +
				"escalation policy, dependencies, group members) to the monitor, as a new version. Secrets are never " +
				"restored or removed: every secret keeps its current value, including secret map keys added since the " +
				"version. Channels, locations, monitors and an escalation policy that no longer exist are dropped and " +
				"listed in dropped_references (a dropped policy leaves the monitor on default routing). Pause state " +
				"is not part of a version and is left as is. Check the effect first with diff_monitor_version " +
				"against=current.",
			Method: http.MethodPost, Route: "/monitors/{id}/versions/{version}/restore",
			PathDocs: monitorVersion, Destructive: true, NeedsWrite: true,
		},
	}
}
