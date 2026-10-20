package mcp

import (
	"net/http"

	monitorhandlers "github.com/yassinebenameur/probara/api/internal/handlers/monitors"
	"github.com/yassinebenameur/probara/api/internal/models"
	"github.com/yassinebenameur/probara/api/internal/services/metricsquery"
)

var monitorConfigDoc = "Type-specific configuration object. Call describe_monitor_type with the monitor type " +
	"first: it returns this object's JSON schema and which fields are secrets. Secret fields read back as \"***\"; " +
	"on update, sending \"***\" or omitting a secret keeps the stored value and \"\" clears it."

var monitorFieldDocs = map[string]string{
	"config":                         monitorConfigDoc,
	"interval_seconds":               "Seconds between checks, 10-86400.",
	"timeout_seconds":                "Per-check timeout in seconds; must be below interval_seconds (active check types only).",
	"tags":                           "Free-form tags; dashboards, SLAs and status filters group by them.",
	"consecutive_failures_threshold": "Failed checks in a row before the monitor is down and alerts, 1-10.",
	"notification_mode":              "\"default\" routes alerts to the workspace default channels (notification settings); \"custom\" uses notification_channels.",
	"notification_channels":          "Channels for notification_mode \"custom\": channel_id (from list_alert_channels) and an optional delay_seconds before paging.",
	"member_alert_rollup":            "Groups only: \"per_monitor\" alerts per member, \"group\" raises one alert for the group and silences members.",
	"dependency_suppression":         "Whether alerts are suppressed while an upstream dependency is down: inherit (workspace setting), on, off.",
	"depends_on_ids":                 "Upstream monitor IDs this monitor depends on (for root-cause alert suppression).",
	"location_ids":                   "Private location IDs to run from (list_locations); empty means the default worker fleet.",
	"location_quorum":                "How many locations must fail before the monitor is down (multi-location monitors).",
	"alert_policy_id":                "Retired (alert policies were replaced by notification settings); leave unset.",
	"alert_policy_ids":               "Retired (alert policies were replaced by notification settings); leave unset.",
}

var monitorEnums = func() map[string][]string {
	return map[string][]string{
		"type":                   monitorTypes(),
		"notification_mode":      {"default", "custom"},
		"member_alert_rollup":    {"per_monitor", "group"},
		"dependency_suppression": {"inherit", "on", "off"},
	}
}

func monitorTools() []spec {
	createDocs := clone(monitorFieldDocs)
	createDocs["name"] = "Display name."
	createDocs["enabled"] = "Whether checks run (default true)."
	updateDocs := clone(monitorFieldDocs)
	updateDocs["config"] = "Replaces the whole config object (secrets follow the keep/clear rules). " + monitorConfigDoc
	updateDocs["depends_on_ids"] = "Replaces the upstream dependency list; [] clears it, omit to keep."
	updateDocs["location_ids"] = "Replaces the location list; [] moves the monitor to the default fleet, omit to keep."
	updateDocs["tags"] = "Replaces the tag list; omit to keep."
	updateDocs["enabled"] = "false pauses checks; true resumes them."

	return []spec{
		{
			Name:  "list_monitors",
			Title: "List monitors",
			Description: "List the workspace's monitors with their current status, type, config (secrets masked), tags, " +
				"alert routing and locations. Paginated; filter by tag or enabled state.",
			Method: http.MethodGet, Route: "/monitors",
			Query: withPaging(
				param{Name: "tag", Description: "Only monitors carrying this exact tag."},
				param{Name: "enabled", Type: "boolean", Description: "true: only running monitors; false: only paused ones."},
			),
		},
		{
			Name:        "get_monitor",
			Title:       "Get monitor",
			Description: "One monitor in full: status, config (secrets masked), group members, per-location state, dependencies and alert routing.",
			Method:      http.MethodGet, Route: "/monitors/{id}",
			PathDocs: map[string]string{"id": "Monitor ID."},
		},
		{
			Name:  "create_monitor",
			Title: "Create monitor",
			Description: "Create a monitor. Call describe_monitor_type first for the config schema; validate a config " +
				"without saving with test_monitor_config. Group monitors take config.monitor_ids (members).",
			Method: http.MethodPost, Route: "/monitors",
			Body: typeOf[models.CreateMonitorRequest](), Required: []string{"name", "type", "config"},
			FieldDocs: createDocs, Enums: monitorEnums(),
		},
		{
			Name:  "update_monitor",
			Title: "Update monitor",
			Description: "Change a monitor. Only the fields you send change. Set enabled=false to pause or true to " +
				"resume. A config you send replaces the stored config.",
			Method: http.MethodPatch, Route: "/monitors/{id}",
			PathDocs: map[string]string{"id": "Monitor ID."},
			Body:     typeOf[models.UpdateMonitorRequest](), FieldDocs: updateDocs, Enums: monitorEnums(),
		},
		{
			Name:        "delete_monitor",
			Title:       "Delete monitor",
			Description: "Delete a monitor (soft delete; its history is purged in the background). Cannot be undone from the API.",
			Method:      http.MethodDelete, Route: "/monitors/{id}",
			PathDocs: map[string]string{"id": "Monitor ID."}, Destructive: true,
		},
		{
			Name:  "test_monitor_config",
			Title: "Test monitor config",
			Description: "Run one check with a config without saving anything, and return its status, latency, error " +
				"and metrics. Pass monitor_id to reuse that monitor's stored secrets for \"***\" placeholders, " +
				"location_id to run from a private location. A SIP test-call config places a real phone call.",
			Method: http.MethodPost, Route: "/monitors/test",
			Body: typeOf[monitorhandlers.TestMonitorConfigRequest](), Required: []string{"type", "config"},
			FieldDocs: map[string]string{
				"config":          monitorConfigDoc,
				"timeout_seconds": "Check timeout, 1-60 (default 10).",
				"monitor_id":      "Existing monitor whose stored secrets replace \"***\" placeholders in config.",
				"location_id":     "Private location to run the test from.",
			},
			Enums: map[string][]string{"type": monitorTypes()},
		},
		{
			Name:  "run_monitor_now",
			Title: "Run monitor now",
			Description: "Queue an immediate check outside the schedule. The result is recorded like a scheduled " +
				"check and can open or resolve alerts. Not for group, push or agent monitors.",
			Method: http.MethodPost, Route: "/monitors/{id}/run",
			PathDocs: map[string]string{"id": "Monitor ID."},
			Body:     typeOf[monitorhandlers.RunMonitorNowRequest](),
			FieldDocs: map[string]string{
				"location_id": "Run only from this one of the monitor's locations (default: every selected location).",
			},
		},
		{
			Name:  "get_monitor_analytics",
			Title: "Get monitor analytics",
			Description: "Uptime, latency percentiles, uptime and latency series, and downtime periods for a time " +
				"window. Group monitors aggregate their members. summary.has_data=false means no data, not 0%.",
			Method: http.MethodGet, Route: "/monitors/{id}/analytics",
			PathDocs: map[string]string{"id": "Monitor ID."},
			Query:    []param{analyticsRange},
		},
		{
			Name:  "get_monitor_results",
			Title: "Get check results",
			Description: "Recent raw check results (status, latency, error, per-location, metrics_data), newest " +
				"first. Raw results are kept 30 days; use get_monitor_analytics for longer windows.",
			Method: http.MethodGet, Route: "/monitors/{id}/results",
			PathDocs: map[string]string{"id": "Monitor ID."},
			Query: []param{
				{Name: "limit", Type: "integer", Description: "Maximum results (default 50). Keep it small; results can be large."},
				{Name: "since", Description: "RFC 3339 timestamp; only results after it."},
			},
		},
		{
			Name:        "get_monitor_metrics_snapshots",
			Title:       "Get metrics snapshots",
			Description: "Downsampled metrics_data snapshots (the newest per bucket) over a window, e.g. DB server stats, or SIP call timings and call quality (loss, jitter, MOS estimate).",
			Method:      http.MethodGet, Route: "/monitors/{id}/metrics-snapshots",
			PathDocs: map[string]string{"id": "Monitor ID."},
			Query:    []param{analyticsRange},
		},
		{
			Name:  "delete_monitor_history",
			Title: "Delete monitor history",
			Description: "Irreversibly erase a monitor's check results, alert events and analytics (and every " +
				"member's, for a group). The monitor itself stays.",
			Method: http.MethodDelete, Route: "/monitors/{id}/history",
			PathDocs: map[string]string{"id": "Monitor ID."}, Destructive: true,
		},
		{
			Name:  "get_synthetic_screenshot",
			Title: "Get synthetic screenshot",
			Description: "The screenshot a synthetic_browser check captured, as an image. Take path from a result's " +
				"metrics_data.synthetic_browser.artifacts.screenshot_path (get_monitor_results).",
			Method: http.MethodGet, Route: "/monitors/{id}/artifacts/screenshot",
			PathDocs: map[string]string{"id": "synthetic_browser monitor ID."},
			Query:    []param{{Name: "path", Required: true, Description: "screenshot_path from the check result."}},
		},
		{
			Name:        "list_group_members",
			Title:       "List group members",
			Description: "The member monitors of a group monitor.",
			Method:      http.MethodGet, Route: "/monitors/{id}/members",
			PathDocs: map[string]string{"id": "Group monitor ID."},
		},
		{
			Name:        "add_group_members",
			Title:       "Add group members",
			Description: "Add monitors to a group monitor. Groups may nest; cycles are rejected.",
			Method:      http.MethodPost, Route: "/monitors/{id}/members",
			PathDocs: map[string]string{"id": "Group monitor ID."},
			Body:     typeOf[models.AddMonitorsToGroupRequest](), Required: []string{"monitor_ids"},
		},
		{
			Name:        "remove_group_members",
			Title:       "Remove group members",
			Description: "Remove monitors from a group monitor (the monitors themselves stay).",
			Method:      http.MethodDelete, Route: "/monitors/{id}/members",
			PathDocs: map[string]string{"id": "Group monitor ID."},
			Body:     typeOf[models.RemoveMonitorsFromGroupRequest](), Required: []string{"monitor_ids"},
		},
		{
			Name:        "list_monitor_dependencies",
			Title:       "List upstream dependencies",
			Description: "The monitors this monitor depends on (its upstreams). While an upstream is down, this monitor's alerts can be suppressed as a symptom.",
			Method:      http.MethodGet, Route: "/monitors/{id}/dependencies",
			PathDocs: map[string]string{"id": "Monitor ID."},
		},
		{
			Name:        "add_monitor_dependency",
			Title:       "Add dependency",
			Description: "Record that a monitor depends on another (an edge in the dependency graph). Cycles are rejected.",
			Method:      http.MethodPost, Route: "/monitors/{id}/dependencies",
			PathDocs: map[string]string{"id": "The dependent (downstream) monitor."},
			Body:     typeOf[monitorhandlers.AddDependencyRequest](), Required: []string{"depends_on_id"},
			FieldDocs: map[string]string{"depends_on_id": "The upstream monitor it depends on."},
		},
		{
			Name:        "remove_monitor_dependency",
			Title:       "Remove dependency",
			Description: "Remove one dependency edge. Idempotent.",
			Method:      http.MethodDelete, Route: "/monitors/{id}/dependencies/{dependsOnId}",
			PathDocs: map[string]string{"id": "The dependent (downstream) monitor.", "dependsOnId": "The upstream monitor."},
		},
		{
			Name:        "list_monitor_dependents",
			Title:       "List downstream dependents",
			Description: "The monitors that depend on this one (its downstreams): what breaks when it breaks.",
			Method:      http.MethodGet, Route: "/monitors/{id}/dependents",
			PathDocs: map[string]string{"id": "Monitor ID."},
		},
		{
			Name:        "get_dependency_graph",
			Title:       "Get dependency graph",
			Description: "The workspace's whole dependency graph: nodes (monitors with status) and edges from dependent to upstream.",
			Method:      http.MethodGet, Route: "/monitors/dependency-graph",
		},
		{
			Name:  "suggest_dependencies",
			Title: "Suggest dependencies (AI)",
			Description: "Ask the local AI model to propose dependency edges from alerts that fire together, with " +
				"reasons and confidence. Read-only; can take a while. 503 when local AI is off or busy (retry).",
			Method: http.MethodPost, Route: "/monitors/dependency-suggestions",
		},
		{
			Name:  "label_dependency_suggestion",
			Title: "Label dependency suggestion",
			Description: "Record an operator verdict on a suggested dependency (training data for the local model): " +
				"confirmed, or rejected with an optional correction.",
			Method: http.MethodPost, Route: "/monitors/dependency-suggestions/label",
			Body: typeOf[models.DependencyLabelRequest](), Required: []string{"monitor_id", "depends_on_id", "verdict"},
			Enums: map[string][]string{
				"verdict":   {"confirmed", "rejected"},
				"corrected": {"reversed", "shared_upstream", "no_dependency", "insufficient_evidence"},
			},
			FieldDocs: map[string]string{"corrected": "Only with verdict \"rejected\": what is actually true."},
		},
		{
			Name:        "bulk_update_alerting",
			Title:       "Bulk update alerting",
			Description: "Set the failure threshold and/or notification routing on many monitors at once. Unsent fields stay unchanged; all monitors must belong to the workspace.",
			Method:      http.MethodPost, Route: "/monitors/bulk/alerting",
			Body: typeOf[monitorhandlers.BulkAlertingRequest](), Required: []string{"monitor_ids"},
			Enums: map[string][]string{"notification_mode": {"default", "custom"}},
		},
		{
			Name:        "bulk_delete_monitors",
			Title:       "Bulk delete monitors",
			Description: "Delete many monitors at once (all or nothing).",
			Method:      http.MethodPost, Route: "/monitors/bulk/delete",
			Body: typeOf[models.BulkDeleteMonitorsRequest](), Required: []string{"monitor_ids"}, Destructive: true,
		},
		{
			Name:  "export_monitors",
			Title: "Export monitors",
			Description: "Every monitor in the workspace as portable YAML (secrets masked), suitable for " +
				"preview_monitor_import / import_monitors in another workspace. Can be large.",
			Method: http.MethodGet, Route: "/monitors/export",
		},
		{
			Name:  "preview_monitor_import",
			Title: "Preview monitor import",
			Description: "Parse an import file (Probara export YAML/JSON, Uptime Kuma backup JSON, or CSV) without " +
				"creating anything. Returns rows, a suggested field mapping and type mapping, warnings and skipped " +
				"records. Pass its rows and suggested_mapping to import_monitors.",
			Method: http.MethodPost, Route: "/monitors/import/preview",
			Multipart: &multipart{ContentArg: "content", FilenameArg: "filename"},
		},
		{
			Name:  "import_monitors",
			Title: "Import monitors",
			Description: "Create monitors from preview_monitor_import output: send its rows, mapping " +
				"(suggested_mapping, adjusted if needed) and optional type_mapping. Monitors with an existing name " +
				"and type are skipped.",
			Method: http.MethodPost, Route: "/monitors/import",
			Body: typeOf[models.ImportExecuteRequest](), Required: []string{"rows", "mapping"},
		},
		{
			Name:        "list_metric_series",
			Title:       "List metric series",
			Description: "The OpenTelemetry metric series an agent monitor's host reports (names, attributes, units). Use with query_metrics.",
			Method:      http.MethodGet, Route: "/monitors/{id}/metrics/series",
			PathDocs: map[string]string{"id": "Agent monitor ID."},
		},
		{
			Name:  "query_metrics",
			Title: "Query host metrics",
			Description: "Query an agent monitor's metric store: up to 12 queries over at most 90 days, each a " +
				"metric_name with optional attribute filters, aggregation and rate. Points are [epoch_ms, value].",
			Method: http.MethodPost, Route: "/monitors/{id}/metrics/query",
			PathDocs: map[string]string{"id": "Agent monitor ID."},
			Body:     typeOf[metricsquery.QueryRequest](), Required: []string{"start", "end", "step_seconds", "queries"},
			FieldDocs: map[string]string{
				"start":        "Range start (RFC 3339).",
				"end":          "Range end (RFC 3339), after start, at most 90 days later.",
				"step_seconds": "Point spacing, 10-86400 (may be raised for long ranges).",
				"queries":      "1-12 queries: ref (your label), metric_name (from list_metric_series), attribute_filters, agg (avg|min|max|sum|last), rate (counters only).",
			},
		},
		{
			Name:  "get_agent_install",
			Title: "Get agent install commands",
			Description: "Install and uninstall scripts (Linux/macOS and Windows) and collector config for an agent " +
				"monitor's host. When called with an API key, the key is embedded in the scripts; over a sign-in " +
				"connection the scripts carry no key and one must be added.",
			Method: http.MethodGet, Route: "/monitors/{id}/agent/install",
			PathDocs: map[string]string{"id": "Agent monitor ID."},
			Query:    []param{backendURLParam},
		},
		{
			Name:        "get_agent_collector_config",
			Title:       "Get collector config",
			Description: "The probara-collector YAML config for an agent monitor's host.",
			Method:      http.MethodGet, Route: "/monitors/{id}/agent/config.yaml",
			PathDocs: map[string]string{"id": "Agent monitor ID."},
			Query: []param{
				{Name: "platform", Description: "Host platform (default linux).", Enum: []string{"linux", "darwin", "windows"}},
				backendURLParam,
			},
		},
		{
			Name:        "get_agent_install_script_unix",
			Title:       "Get Unix install script",
			Description: "The Linux/macOS shell script that installs the collector for an agent monitor (see get_agent_install on embedded keys).",
			Method:      http.MethodGet, Route: "/monitors/{id}/agent/install/script.sh",
			PathDocs: map[string]string{"id": "Agent monitor ID."}, Query: []param{backendURLParam},
		},
		{
			Name:        "get_agent_install_script_windows",
			Title:       "Get Windows install script",
			Description: "The PowerShell script that installs the collector for an agent monitor (see get_agent_install on embedded keys).",
			Method:      http.MethodGet, Route: "/monitors/{id}/agent/install/script.ps1",
			PathDocs: map[string]string{"id": "Agent monitor ID."}, Query: []param{backendURLParam},
		},
		{
			Name:        "get_agent_uninstall_script_unix",
			Title:       "Get Unix uninstall script",
			Description: "The Linux/macOS shell script that removes the collector.",
			Method:      http.MethodGet, Route: "/monitors/{id}/agent/uninstall/script.sh",
			PathDocs: map[string]string{"id": "Agent monitor ID."}, Query: []param{backendURLParam},
		},
		{
			Name:        "get_agent_uninstall_script_windows",
			Title:       "Get Windows uninstall script",
			Description: "The PowerShell script that removes the collector.",
			Method:      http.MethodGet, Route: "/monitors/{id}/agent/uninstall/script.ps1",
			PathDocs: map[string]string{"id": "Agent monitor ID."}, Query: []param{backendURLParam},
		},
		{
			Name:  "get_push_info",
			Title: "Get push monitor info",
			Description: "A push monitor's secret token, webhook URL and an example curl. The job being monitored " +
				"calls the URL on every run. Requires write access because it reveals the token.",
			Method: http.MethodGet, Route: "/monitors/{id}/push/info",
			PathDocs: map[string]string{"id": "Push monitor ID."}, Query: []param{backendURLParam}, NeedsWrite: true,
		},
		{
			Name:  "snooze_monitor",
			Title: "Snooze monitor",
			Description: "Silence a monitor's alerts for a while by creating a maintenance window from now. Checks " +
				"keep running. Send exactly one of duration_minutes or until.",
			Method: http.MethodPost, Route: "/monitors/{id}/snooze",
			PathDocs: map[string]string{"id": "Monitor ID."},
			Body:     typeOf[models.SnoozeMonitorRequest](),
			FieldDocs: map[string]string{
				"duration_minutes": "Snooze length in minutes.",
				"until":            "Snooze end (RFC 3339, in the future).",
			},
		},
	}
}

func clone(m map[string]string) map[string]string {
	out := make(map[string]string, len(m))
	for k, v := range m {
		out[k] = v
	}
	return out
}
