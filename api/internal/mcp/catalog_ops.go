package mcp

import (
	"net/http"

	"github.com/yassinebenameur/probara/api/internal/models"
	discoveryservice "github.com/yassinebenameur/probara/api/internal/services/discovery"
)

var tagFilter = param{Name: "tag", Type: "array", Description: "Only monitors carrying any of these tags (repeatable)."}

func observabilityTools() []spec {
	return []spec{
		{
			Name:  "whoami",
			Title: "Who am I",
			Description: "The identity behind this connection: actor type (user or API key), tenant, roles, scope and " +
				"whether it can write. Call it first when unsure what you may do.",
			Method: http.MethodGet, Route: "/auth-context",
		},
		{
			Name:  "get_dashboard_overview",
			Title: "Get dashboard overview",
			Description: "The fleet at a glance: monitor counts by state, uptime trend, 24h activity, ops summary, " +
				"problem monitors, recent failures and recent alerts. The best first call for \"how is everything?\".",
			Method: http.MethodGet, Route: "/dashboard/overview",
			Query: []param{
				dashboardRange, tagFilter,
				{Name: "failures_limit", Type: "integer", Description: "Recent failures to include, max 50 (default 10)."},
				{Name: "alerts_limit", Type: "integer", Description: "Recent alerts to include, max 50 (default 10)."},
			},
		},
		{
			Name:        "get_dashboard_summary",
			Title:       "Get dashboard summary",
			Description: "Fleet summary with per-group (dashboard group tag) health and uptime.",
			Method:      http.MethodGet, Route: "/dashboard/summary",
			Query: []param{dashboardRange, tagFilter},
		},
		{
			Name:        "list_problem_monitors",
			Title:       "List problem monitors",
			Description: "Monitors currently down or degraded, worst first.",
			Method:      http.MethodGet, Route: "/dashboard/problem-monitors",
			Query: []param{dashboardRange, tagFilter, {Name: "limit", Type: "integer", Description: "Maximum, up to 50 (default 5)."}},
		},
		{
			Name:        "list_recent_failures",
			Title:       "List recent failures",
			Description: "The most recent failed checks across the fleet, with error messages.",
			Method:      http.MethodGet, Route: "/dashboard/recent-failures",
			Query: []param{dashboardRange, tagFilter, {Name: "limit", Type: "integer", Description: "Maximum, up to 50 (default 10)."}},
		},
		{
			Name:        "get_dashboard_recent_alerts",
			Title:       "Get dashboard recent alerts",
			Description: "The most recent alerts, as the dashboard shows them.",
			Method:      http.MethodGet, Route: "/dashboard/recent-alerts",
			Query: []param{dashboardRange, tagFilter, {Name: "limit", Type: "integer", Description: "Maximum, up to 50 (default 10)."}},
		},
		{
			Name:        "get_group_sparkline",
			Title:       "Get group sparkline",
			Description: "12 uptime buckets for one dashboard group (null bucket = no data).",
			Method:      http.MethodGet, Route: "/dashboard/group-sparkline",
			Query: []param{
				dashboardRange,
				{Name: "group", Description: "Dashboard group tag; omit for the ungrouped monitors."},
			},
		},
		{
			Name:  "get_mesh",
			Title: "Get location mesh",
			Description: "The inter-location connectivity matrix: every private location and each directed edge's " +
				"latest status and latency.",
			Method: http.MethodGet, Route: "/mesh",
		},
		{
			Name:        "get_mesh_edge_history",
			Title:       "Get mesh edge history",
			Description: "Status and latency history of one directed mesh edge (source location to target location), up to 500 points.",
			Method:      http.MethodGet, Route: "/mesh/history",
			Query: []param{
				{Name: "source", Required: true, Description: "Source location ID."},
				{Name: "target", Required: true, Description: "Target location ID."},
				{Name: "hours", Type: "integer", Description: "Look-back in hours, 1-720 (default 24)."},
			},
		},
		{
			Name:        "probe_mesh_edge",
			Title:       "Probe mesh edge",
			Description: "Probe one mesh edge now from the source location's worker and return the result. Nothing is recorded.",
			Method:      http.MethodPost, Route: "/mesh/probe",
			Body: typeOf[models.MeshProbeRequest](), Required: []string{"source_location_id", "target_location_id"},
		},
	}
}

func locationTools() []spec {
	return []spec{
		{
			Name:        "list_locations",
			Title:       "List private locations",
			Description: "Private locations (remote worker deployments that run checks from inside other networks), with worker connection state.",
			Method:      http.MethodGet, Route: "/locations",
			Query: []param{
				{Name: "page", Type: "integer", Description: "1-based page number (default 1)."},
				{Name: "page_size", Type: "integer", Description: "Items per page, up to 200 (default 50)."},
			},
		},
		{
			Name:        "get_location",
			Title:       "Get private location",
			Description: "One private location.",
			Method:      http.MethodGet, Route: "/locations/{id}",
			PathDocs: map[string]string{"id": "Location ID."},
		},
		{
			Name:  "create_location",
			Title: "Create private location",
			Description: "Create a private location and its worker credential. Then call get_location_deploy_info " +
				"for the docker command that starts its worker. Set mesh_endpoint (host:port) to join the mesh.",
			Method: http.MethodPost, Route: "/locations",
			Body: typeOf[models.CreateLocationRequest](), Required: []string{"name"},
		},
		{
			Name:        "update_location",
			Title:       "Update private location",
			Description: "Rename, describe, enable/disable a location or change its mesh endpoint (\"\" leaves the mesh).",
			Method:      http.MethodPatch, Route: "/locations/{id}",
			PathDocs: map[string]string{"id": "Location ID."},
			Body:     typeOf[models.UpdateLocationRequest](),
		},
		{
			Name:        "delete_location",
			Title:       "Delete private location",
			Description: "Delete a location. Monitors using it are detached from it and their quorum clamped.",
			Method:      http.MethodDelete, Route: "/locations/{id}",
			PathDocs: map[string]string{"id": "Location ID."}, Destructive: true,
		},
		{
			Name:  "get_location_deploy_info",
			Title: "Get location deploy info",
			Description: "docker run / compose snippets and env to start a location's worker. Contains the worker's " +
				"secret credential; requires write access.",
			Method: http.MethodGet, Route: "/locations/{id}/deploy",
			PathDocs: map[string]string{"id": "Location ID."}, NeedsWrite: true,
		},
		{
			Name:        "list_maintenance_windows",
			Title:       "List maintenance windows",
			Description: "Maintenance windows (scheduled alert silences; checks keep running).",
			Method:      http.MethodGet, Route: "/maintenance-windows",
			Query: withPaging(
				param{Name: "status", Enum: []string{"active", "upcoming", "past"}, Description: "Only windows in this phase."},
				param{Name: "monitor_id", Description: "Only windows covering this monitor."},
			),
		},
		{
			Name:        "get_maintenance_window",
			Title:       "Get maintenance window",
			Description: "One maintenance window.",
			Method:      http.MethodGet, Route: "/maintenance-windows/{id}",
			PathDocs: map[string]string{"id": "Maintenance window ID."},
		},
		{
			Name:  "create_maintenance_window",
			Title: "Create maintenance window",
			Description: "Schedule a maintenance window: alerts for the listed monitors (a group covers its members) " +
				"are suppressed between starts_at and ends_at. Checks keep running and uptime is accounted as maintenance.",
			Method: http.MethodPost, Route: "/maintenance-windows",
			Body:     typeOf[models.CreateMaintenanceWindowRequest](),
			Required: []string{"title", "starts_at", "ends_at", "monitor_ids"},
			FieldDocs: map[string]string{
				"starts_at": "Start (RFC 3339).",
				"ends_at":   "End (RFC 3339), after starts_at and in the future.",
			},
		},
		{
			Name:        "update_maintenance_window",
			Title:       "Update maintenance window",
			Description: "Change a maintenance window. monitor_ids, when sent, replaces the covered set.",
			Method:      http.MethodPatch, Route: "/maintenance-windows/{id}",
			PathDocs: map[string]string{"id": "Maintenance window ID."},
			Body:     typeOf[models.UpdateMaintenanceWindowRequest](),
		},
		{
			Name:        "delete_maintenance_window",
			Title:       "Delete maintenance window",
			Description: "Delete a maintenance window (ends it immediately if active).",
			Method:      http.MethodDelete, Route: "/maintenance-windows/{id}",
			PathDocs: map[string]string{"id": "Maintenance window ID."}, Destructive: true,
		},
	}
}

func discoveryTools() []spec {
	integration := map[string]string{"id": "Discovery integration ID (list_discovery_integrations)."}
	service := map[string]string{"id": "Discovered service ID (get_discovery_coverage)."}
	return []spec{
		{
			Name:  "list_discovery_integrations",
			Title: "List Kubernetes discovery integrations",
			Description: "Connected Kubernetes clusters: each integration's namespace allowlist, the private location " +
				"its internal checks run from, when its collector last uploaded a snapshot, and the inventory revision.",
			Method: http.MethodGet, Route: "/discovery/integrations",
		},
		{
			Name:  "create_discovery_integration",
			Title: "Create Kubernetes discovery integration",
			Description: "Register a cluster for discovery. Returns the integration with its collector token, shown only " +
				"this once; the in-cluster collector needs it (the Discovery page's Connect cluster flow builds the " +
				"manifest). Nothing is monitored until a suggestion is approved.",
			Method: http.MethodPost, Route: "/discovery/integrations",
			Body: typeOf[discoveryservice.CreateRequest](), Required: []string{"name", "namespaces"},
			FieldDocs: map[string]string{
				"namespaces":  "1-20 existing Kubernetes namespaces the collector may list Services and Ingresses in.",
				"location_id": "Enabled private location (list_locations) for checks of cluster-internal services; null for none.",
			},
		},
		{
			Name:  "delete_discovery_integration",
			Title: "Delete Kubernetes discovery integration",
			Description: "Delete an integration with its discovered inventory and links; its collector token stops " +
				"working. Monitors created from it are kept.",
			Method: http.MethodDelete, Route: "/discovery/integrations/{id}",
			PathDocs: integration, Destructive: true,
		},
		{
			Name:  "rotate_discovery_collector_token",
			Title: "Rotate discovery collector token",
			Description: "Issue a new collector token and revoke the old one at once: the running collector is " +
				"refused until its secret is updated. Returns the new token, shown only this once.",
			Method: http.MethodPost, Route: "/discovery/integrations/{id}/rotate-token",
			PathDocs: integration, Destructive: true,
		},
		{
			Name:  "get_discovery_coverage",
			Title: "Get Kubernetes discovery coverage",
			Description: "Monitoring coverage of one integration's discovered services, 50 per page: each service's " +
				"Services/Ingresses with suggested HTTP/TCP checks (key, target, internal), linked monitors (enabled, " +
				"state, fresh evidence, alert routing), existing monitors matching a suggestion (candidates) and gaps: " +
				"no_enabled_monitor, stale_checks, unrouted, freshness_unavailable, resource_missing(_grace), " +
				"inventory_stale. The summary counts every service before filtering and ignores inventory_stale " +
				"(reported once as inventory_stale); missing counts resource_missing, missing_grace " +
				"resource_missing_grace. Coverage is not health.",
			Method: http.MethodGet, Route: "/discovery/integrations/{id}/coverage",
			PathDocs: integration,
			Query: []param{
				{Name: "page", Type: "integer", Description: "1-based page number (default 1)."},
				{Name: "q", Description: "Search service names and namespaces (max 200 characters)."},
				{Name: "gaps", Type: "boolean", Description: "true: only services with at least one gap."},
				{Name: "sort", Enum: []string{"name", "gaps"}, Description: "Order by name (default) or most gaps first."},
			},
		},
		{
			Name:  "approve_discovery_suggestion",
			Title: "Approve discovery suggestion",
			Description: "Create a monitor (tagged \"discovered\") from one suggestion of a discovered service, together " +
				"with its location, channels and inventory link. Returns monitor_id and created. Idempotent per " +
				"suggestion: approving again returns the monitor already linked with created false. Refused while the inventory is stale, for an internal target without a private " +
				"location, and when an existing monitor checks the target from that location (link it instead).",
			Method: http.MethodPost, Route: "/discovery/services/{id}/approve",
			PathDocs: service,
			Body:     typeOf[discoveryservice.ApproveRequest](),
			Required: []string{"key", "name", "interval_seconds"},
			FieldDocs: map[string]string{
				"key":               "Suggestion key from get_discovery_coverage.",
				"interval_seconds":  "Seconds between checks, 30-86400.",
				"expected_status":   "HTTP suggestions only: expected status code, 100-599.",
				"location_id":       "Enabled private location to check from; required for internal targets.",
				"notification_mode": "\"default\" (workspace default channels) or \"custom\" (channel_ids).",
				"channel_ids":       "1-20 active channel IDs for notification_mode \"custom\"; empty for \"default\".",
			},
			Enums: map[string][]string{"notification_mode": {"default", "custom"}},
		},
		{
			Name:  "link_discovery_monitor",
			Title: "Link monitor to discovered service",
			Description: "Count an existing monitor as coverage for a discovered service. key optionally binds it to one " +
				"current suggestion; a suggestion links to at most one monitor.",
			Method: http.MethodPost, Route: "/discovery/services/{id}/links",
			PathDocs: service,
			Body:     typeOf[discoveryservice.LinkRequest](), Required: []string{"monitor_id"},
			FieldDocs: map[string]string{"key": "Suggestion key from get_discovery_coverage; omit for a service-level link."},
		},
		{
			Name:        "unlink_discovery_monitor",
			Title:       "Unlink monitor from discovered service",
			Description: "Remove a monitor's link to a discovered service. The monitor itself is unchanged.",
			Method:      http.MethodDelete, Route: "/discovery/services/{id}/links/{monitorID}",
			PathDocs: map[string]string{"id": "Discovered service ID.", "monitorID": "Linked monitor ID."},
		},
	}
}
