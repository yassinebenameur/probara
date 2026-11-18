package mcp

import "net/http"

// historyTools cover monitor configuration history: every save records a
// masked snapshot; these list, read, compare and restore them.
func historyTools() []spec {
	monitorVersion := map[string]string{
		"id":      "Monitor ID.",
		"version": "Version number, from list_monitor_versions.",
	}
	return []spec{
		{
			Name:  "list_monitor_versions",
			Title: "List monitor versions",
			Description: "List a monitor's configuration versions, newest first: who saved it (actor, source: ui, api, " +
				"mcp, import, discovery, restore; baseline for the configuration before the first recorded save, " +
				"system for a change made by deleting a location, channel or member monitor), when, which fields changed and which secret fields were set, " +
				"replaced or cleared (never their values). Each monitor keeps its last 100 versions; history starts " +
				"at its first save recorded (history_starts_at).",
			Method: http.MethodGet, Route: "/monitors/{id}/versions",
			PathDocs: map[string]string{"id": "Monitor ID."},
			Query:    withPaging(),
		},
		{
			Name:        "get_monitor_version",
			Title:       "Get monitor version",
			Description: "One configuration version with its full snapshot (secrets masked as ***).",
			Method:      http.MethodGet, Route: "/monitors/{id}/versions/{version}",
			PathDocs: monitorVersion,
		},
		{
			Name:  "diff_monitor_version",
			Title: "Diff monitor version",
			Description: "The field changes that lead from another version to this one (before/after per path such as " +
				"config.url; secret fields are flagged and show only *** or nothing). against defaults to the previous " +
				"version; against=current compares with the live monitor, which is what restoring this version would change.",
			Method: http.MethodGet, Route: "/monitors/{id}/versions/{version}/diff",
			PathDocs: monitorVersion,
			Query: []param{
				{Name: "against", Description: "A version number, or \"current\" for the live monitor (default: the previous version)."},
			},
		},
		{
			Name:  "restore_monitor_version",
			Title: "Restore monitor version",
			Description: "Re-apply a version's configuration (name, config, schedule, alerting, channels, " +
				"locations, dependencies, group members) to the monitor through the normal update path, recorded as " +
				"a new version, all or nothing; a version of another monitor type is refused. Secrets are never restored or removed: every secret keeps its current " +
				"value, and secrets the version had that the monitor no longer holds are listed in secrets_to_reenter; " +
				"a version that cannot be valid without them, or that conflicts with a secret held now, is refused " +
				"with nothing changed. Channels, locations and monitors that no longer exist, and dependencies or " +
				"members that would now close a cycle, are dropped and listed in dropped_references. Pause state is " +
				"left as is. Check the effect first with diff_monitor_version against=current.",
			Method: http.MethodPost, Route: "/monitors/{id}/versions/{version}/restore",
			PathDocs: monitorVersion, Destructive: true,
		},
	}
}
