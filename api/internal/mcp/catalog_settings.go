package mcp

import (
	"net/http"

	"github.com/yassinebenameur/probara/api/internal/models"
	"github.com/yassinebenameur/probara/api/internal/services/aisettings"
	"github.com/yassinebenameur/probara/api/internal/services/notificationsettings"
	"github.com/yassinebenameur/probara/shared/notifications/plugin"
)

const channelConfigDoc = "Plugin-specific config object. Its fields are the plugin manifest's fields " +
	"(get_alert_channel_plugin): key -> value, secret fields write-only. On update the object replaces the stored " +
	"non-secret fields; a secret that is omitted, \"\" or \"***\" keeps its stored value and null clears it."

func notificationTools() []spec {
	channel := map[string]string{"id": "Alert channel ID."}
	return []spec{
		{
			Name:        "list_alert_channels",
			Title:       "List alert channels",
			Description: "Notification channels (Slack, email, PagerDuty, webhooks, ...), secrets masked.",
			Method:      http.MethodGet, Route: "/alert-channels",
			Query: pageParams,
		},
		{
			Name:        "get_alert_channel",
			Title:       "Get alert channel",
			Description: "One notification channel, secrets masked.",
			Method:      http.MethodGet, Route: "/alert-channels/{id}",
			PathDocs: channel,
		},
		{
			Name:  "create_alert_channel",
			Title: "Create alert channel",
			Description: "Create a notification channel. Get the type's config fields from get_alert_channel_plugin " +
				"first. Channels receive alerts once routed: workspace defaults (update_notification_settings) or a " +
				"monitor's custom routing.",
			Method: http.MethodPost, Route: "/alert-channels",
			Body: typeOf[models.CreateAlertChannelRequest](), Required: []string{"name", "type", "config"},
			Enums:     map[string][]string{"type": plugin.DefaultRegistry.Types()},
			FieldDocs: map[string]string{"config": channelConfigDoc, "is_active": "Default true."},
		},
		{
			Name:        "update_alert_channel",
			Title:       "Update alert channel",
			Description: "Rename, (de)activate or reconfigure a channel. Its type cannot change.",
			Method:      http.MethodPatch, Route: "/alert-channels/{id}",
			PathDocs:  channel,
			Body:      typeOf[models.UpdateAlertChannelRequest](),
			FieldDocs: map[string]string{"config": channelConfigDoc},
		},
		{
			Name:        "delete_alert_channel",
			Title:       "Delete alert channel",
			Description: "Delete a notification channel; alerts routed only to it reach nobody afterwards.",
			Method:      http.MethodDelete, Route: "/alert-channels/{id}",
			PathDocs: channel, Destructive: true,
		},
		{
			Name:        "test_alert_channel",
			Title:       "Send test notification",
			Description: "Send a REAL test notification through the channel and report delivery errors.",
			Method:      http.MethodPost, Route: "/alert-channels/{id}/test",
			PathDocs: channel,
		},
		{
			Name:        "list_alert_channel_plugins",
			Title:       "List channel plugins",
			Description: "Every notification channel type with its manifest: config fields (key, type, required, secret, options), capabilities.",
			Method:      http.MethodGet, Route: "/alert-channel-plugins",
		},
		{
			Name:        "get_alert_channel_plugin",
			Title:       "Get channel plugin",
			Description: "One channel type's manifest: the config fields create_alert_channel expects.",
			Method:      http.MethodGet, Route: "/alert-channel-plugins/{type}",
			PathDocs: map[string]string{"type": "Channel type, e.g. slack, email, pagerduty."},
		},
		{
			Name:  "get_notification_settings",
			Title: "Get notification settings",
			Description: "Workspace alerting defaults: default channels (who monitors in \"default\" mode page), " +
				"reminders, auto-incident creation, latency anomaly detection and dependency suppression.",
			Method: http.MethodGet, Route: "/notification-settings",
		},
		{
			Name:  "update_notification_settings",
			Title: "Update notification settings",
			Description: "Change workspace alerting defaults. Unsent fields stay. default_channels, when sent, " +
				"replaces the default routing in order ([] routes default-mode monitors to nobody).",
			Method: http.MethodPut, Route: "/notification-settings",
			Body: typeOf[notificationsettings.UpdateRequest](),
		},
		{
			Name:        "get_ai_settings",
			Title:       "Get local AI settings",
			Description: "Whether the local AI model is enabled and available, and the automatic alert triage settings.",
			Method:      http.MethodGet, Route: "/ai-settings",
		},
		{
			Name:        "update_ai_settings",
			Title:       "Update local AI settings",
			Description: "Enable/disable local AI for the workspace and tune automatic triage (delay, cooldown, hourly cap, follow-up notifications). Send at least one field.",
			Method:      http.MethodPut, Route: "/ai-settings",
			Body: typeOf[aisettings.UpdateRequest](),
		},
		{
			Name:        "test_ai_connection",
			Title:       "Test local AI",
			Description: "Check that the local AI service answers. Failures are reported in the result, not as errors.",
			Method:      http.MethodPost, Route: "/ai-settings/test",
			Body: typeOf[aisettings.TestRequest](),
		},
		{
			Name:  "export_ai_labels",
			Title: "Export AI training labels",
			Description: "Download operator verdicts on AI output as JSONL (fine-tuning data). Tenant admins only; " +
				"capped at 5,000 labels.",
			Method: http.MethodPost, Route: "/ai-labels/export",
			Body:  typeOf[models.AILabelExportFilter](),
			Enums: map[string][]string{"subject_types": {"incident_analysis", "alert_triage", "dependency_pair"}},
		},
		{
			Name:        "list_api_keys",
			Title:       "List API keys",
			Description: "The workspace's API keys (never the secret), including revoked ones.",
			Method:      http.MethodGet, Route: "/api-keys",
			Query: pageParams,
		},
		{
			Name:  "create_api_key",
			Title: "Create API key",
			Description: "Mint a workspace API key. The secret is returned ONLY in this response. Tenant admins only " +
				"(an API key can never mint keys).",
			Method: http.MethodPost, Route: "/api-keys",
			Body: typeOf[models.CreateApiKeyRequest](), Required: []string{"name"},
			Enums:     map[string][]string{"scope": {"read", "write"}},
			FieldDocs: map[string]string{"scope": "Default write.", "expires_at": "RFC 3339 expiry in the future; omit for no expiry."},
		},
		{
			Name:        "revoke_api_key",
			Title:       "Revoke API key",
			Description: "Revoke an API key immediately. Anything using it stops working. Tenant admins only.",
			Method:      http.MethodDelete, Route: "/api-keys/{id}",
			PathDocs: map[string]string{"id": "API key ID."}, Destructive: true,
		},
	}
}

var reportFormat = param{
	Name:        "format",
	Description: "Omit for JSON in the result; json/csv/pdf return a downloadable file.",
	Enum:        []string{"json", "csv", "pdf"},
}

func slaTools() []spec {
	sla := map[string]string{"id": "SLA ID."}
	return []spec{
		{
			Name:        "list_slas",
			Title:       "List SLAs",
			Description: "Service level agreements with the running period's attainment.",
			Method:      http.MethodGet, Route: "/slas",
			Query: []param{{Name: "monitor_id", Description: "Only SLAs that cover this monitor."}},
		},
		{
			Name:  "create_sla",
			Title: "Create SLA",
			Description: "Define an SLA: an availability target over monitors (explicit IDs and/or tags), " +
				"aggregated serially (all must be up) or as a mean, per week, month or quarter.",
			Method: http.MethodPost, Route: "/slas",
			Body: typeOf[models.CreateSLARequest](), Required: []string{"name", "target_pct"},
			Enums: map[string][]string{"aggregation": {"serial", "mean"}, "period": {"weekly", "monthly", "quarterly"}},
			FieldDocs: map[string]string{
				"target_pct": "Target availability percent, strictly between 0 and 100 (e.g. 99.9).",
				"timezone":   "IANA zone for period boundaries (default UTC).",
			},
		},
		{
			Name:        "get_sla",
			Title:       "Get SLA",
			Description: "One SLA with its current attainment.",
			Method:      http.MethodGet, Route: "/slas/{id}",
			PathDocs: sla,
		},
		{
			Name:        "update_sla",
			Title:       "Update SLA",
			Description: "Change an SLA; unsent fields stay, monitor_ids/tags when sent replace the set.",
			Method:      http.MethodPatch, Route: "/slas/{id}",
			PathDocs: sla,
			Body:     typeOf[models.UpdateSLARequest](),
			Enums:    map[string][]string{"aggregation": {"serial", "mean"}, "period": {"weekly", "monthly", "quarterly"}},
		},
		{
			Name:        "delete_sla",
			Title:       "Delete SLA",
			Description: "Delete an SLA and every report issued for it.",
			Method:      http.MethodDelete, Route: "/slas/{id}",
			PathDocs: sla, Destructive: true,
		},
		{
			Name:  "get_sla_report",
			Title: "Get SLA report",
			Description: "Compute an SLA report live: the running period by default, a named period, or a custom " +
				"from/to range (max 400 days).",
			Method: http.MethodGet, Route: "/slas/{id}/report",
			PathDocs: sla,
			Query: []param{
				{Name: "period", Description: "Period key: 2026-09 (monthly), 2026-Q3 (quarterly), 2026-W39 (weekly)."},
				{Name: "from", Description: "Custom range start date YYYY-MM-DD (with to; not with period)."},
				{Name: "to", Description: "Custom range end date YYYY-MM-DD, inclusive."},
				reportFormat,
			},
		},
		{
			Name:        "list_sla_reports",
			Title:       "List issued SLA reports",
			Description: "Reports issued (frozen) for an SLA, newest period first.",
			Method:      http.MethodGet, Route: "/slas/{id}/reports",
			PathDocs: sla,
		},
		{
			Name:  "issue_sla_report",
			Title: "Issue SLA report",
			Description: "Freeze a closed period's report as an immutable record (e.g. for a customer). Defaults to " +
				"the last closed period; a period can be issued once.",
			Method: http.MethodPost, Route: "/slas/{id}/reports",
			PathDocs:  sla,
			Body:      typeOf[models.IssueSLAReportRequest](),
			FieldDocs: map[string]string{"period": "Closed period key (default: the last closed period)."},
		},
		{
			Name:        "get_issued_sla_report",
			Title:       "Get issued SLA report",
			Description: "A previously issued (frozen) SLA report.",
			Method:      http.MethodGet, Route: "/sla-reports/{id}",
			PathDocs: map[string]string{"id": "Issued report ID (from list_sla_reports)."},
			Query:    []param{reportFormat},
		},
	}
}

func statusPageTools() []spec {
	page := map[string]string{"id": "Status page ID."}
	template := map[string]string{"templateId": "Library template ID."}
	return []spec{
		{
			Name:        "list_status_pages",
			Title:       "List status pages",
			Description: "Public status pages with their sections, monitors and settings.",
			Method:      http.MethodGet, Route: "/status-pages",
			Query: pageParams,
		},
		{
			Name:        "get_status_page",
			Title:       "Get status page",
			Description: "One status page with its sections, monitors, settings and public slug.",
			Method:      http.MethodGet, Route: "/status-pages/{id}",
			PathDocs: page,
		},
		{
			Name:  "create_status_page",
			Title: "Create status page",
			Description: "Create a PUBLIC status page at its slug. Lay monitors out in sections (title + monitors " +
				"with optional display names); settings toggle what visitors see.",
			Method: http.MethodPost, Route: "/status-pages",
			Body: typeOf[models.CreateStatusPageRequest](), Required: []string{"slug", "title"},
			FieldDocs: map[string]string{
				"slug":                  "URL slug: lowercase letters, digits and dashes; unique.",
				"sections":              "Ordered sections: {title, monitors: [{monitor_id, display_name?}]}. A monitor may appear once.",
				"monitor_ids":           "Legacy flat list (one \"Services\" section); prefer sections.",
				"monitor_display_names": "Legacy: monitor ID -> public name, with monitor_ids.",
			},
		},
		{
			Name:        "update_status_page",
			Title:       "Update status page",
			Description: "Change a status page. sections, when sent, replaces all sections; settings merge field by field. Changes are public immediately.",
			Method:      http.MethodPatch, Route: "/status-pages/{id}",
			PathDocs: page,
			Body:     typeOf[models.UpdateStatusPageRequest](),
		},
		{
			Name:        "delete_status_page",
			Title:       "Delete status page",
			Description: "Delete a status page; its public URL stops working.",
			Method:      http.MethodDelete, Route: "/status-pages/{id}",
			PathDocs: page, Destructive: true,
		},
		{
			Name:        "get_status_page_template",
			Title:       "Get page template state",
			Description: "A status page's custom template state: published version, draft source, version history.",
			Method:      http.MethodGet, Route: "/status-pages/{id}/template",
			PathDocs: page,
		},
		{
			Name:        "reset_status_page_template",
			Title:       "Reset page template",
			Description: "Archive the published custom template so the page renders the built-in one again (history kept).",
			Method:      http.MethodDelete, Route: "/status-pages/{id}/template",
			PathDocs: page,
		},
		{
			Name:        "get_default_status_page_template",
			Title:       "Get default template source",
			Description: "The built-in status page template source (Go html/template); the starting point for a custom one.",
			Method:      http.MethodGet, Route: "/status-pages/{id}/template/default",
			PathDocs: page,
		},
		{
			Name:        "get_status_page_template_version",
			Title:       "Get template version source",
			Description: "The source of one published or archived template version.",
			Method:      http.MethodGet, Route: "/status-pages/{id}/template/versions/{version}/source",
			PathDocs: map[string]string{"id": "Status page ID.", "version": "Version number."},
		},
		{
			Name:        "save_status_page_template_draft",
			Title:       "Save template draft",
			Description: "Validate and save a custom template as the page's draft (replaces any draft). Not public until publish_status_page_template.",
			Method:      http.MethodPut, Route: "/status-pages/{id}/template/draft",
			PathDocs: page,
			Body:     typeOf[models.SaveStatusPageTemplateDraftRequest](), Required: []string{"source"},
		},
		{
			Name:        "discard_status_page_template_draft",
			Title:       "Discard template draft",
			Description: "Delete the page's template draft. Idempotent.",
			Method:      http.MethodDelete, Route: "/status-pages/{id}/template/draft",
			PathDocs: page,
		},
		{
			Name:        "publish_status_page_template",
			Title:       "Publish template draft",
			Description: "Publish the draft as the next version; the public page switches to it immediately.",
			Method:      http.MethodPost, Route: "/status-pages/{id}/template/publish",
			PathDocs: page,
		},
		{
			Name:        "revert_status_page_template",
			Title:       "Revert template",
			Description: "Republish an archived version as a new version (live immediately).",
			Method:      http.MethodPost, Route: "/status-pages/{id}/template/revert",
			PathDocs: page,
			Body:     typeOf[models.RevertStatusPageTemplateRequest](), Required: []string{"version"},
		},
		{
			Name:        "list_status_page_library_templates",
			Title:       "List template library",
			Description: "The workspace's reusable status page templates (without sources).",
			Method:      http.MethodGet, Route: "/status-page-templates",
		},
		{
			Name:  "create_status_page_library_template",
			Title: "Add library template",
			Description: "Save a reusable template to the library. To apply one, save its source as a page draft " +
				"(save_status_page_template_draft) and publish.",
			Method: http.MethodPost, Route: "/status-page-templates",
			Body: typeOf[models.CreateStatusPageLibraryTemplateRequest](), Required: []string{"name", "source"},
		},
		{
			Name:        "get_status_page_library_template",
			Title:       "Get library template",
			Description: "One library template including its source.",
			Method:      http.MethodGet, Route: "/status-page-templates/{templateId}",
			PathDocs: template,
		},
		{
			Name:        "get_status_page_library_template_source",
			Title:       "Get library template source",
			Description: "A library template's raw source.",
			Method:      http.MethodGet, Route: "/status-page-templates/{templateId}/source",
			PathDocs: template,
		},
		{
			Name:        "update_status_page_library_template",
			Title:       "Update library template",
			Description: "Rename, describe or replace the source of a library template.",
			Method:      http.MethodPatch, Route: "/status-page-templates/{templateId}",
			PathDocs: template,
			Body:     typeOf[models.UpdateStatusPageLibraryTemplateRequest](),
		},
		{
			Name:        "delete_status_page_library_template",
			Title:       "Delete library template",
			Description: "Delete a library template (pages using a copy are unaffected).",
			Method:      http.MethodDelete, Route: "/status-page-templates/{templateId}",
			PathDocs: template, Destructive: true,
		},
	}
}

func workspaceTools() []spec {
	return []spec{
		{
			Name:        "get_tenant_settings",
			Title:       "Get workspace settings",
			Description: "Data retention and dashboard group tags.",
			Method:      http.MethodGet, Route: "/tenant-settings",
		},
		{
			Name:  "update_tenant_settings",
			Title: "Update workspace settings",
			Description: "Change data retention (0 = unlimited, else 30-3650 days; lowering it prunes history) or " +
				"the dashboard group tags (replaced as a list). Tenant admins only.",
			Method: http.MethodPatch, Route: "/tenant-settings",
			Body: typeOf[models.UpdateTenantSettingsRequest](),
		},
		{
			Name:        "list_audit_log",
			Title:       "List audit log",
			Description: "Who did what: every mutation with actor, action, resource, outcome and IP. Tenant admins only.",
			Method:      http.MethodGet, Route: "/audit-log",
			Query: []param{
				{Name: "action", Description: "Exact action, e.g. monitor.update (see list_audit_actions)."},
				{Name: "outcome", Enum: []string{"success", "failure", "denied"}, Description: "Only this outcome."},
				{Name: "actor_id", Description: "Only this user or API key ID."},
				{Name: "from", Description: "RFC 3339 lower bound."},
				{Name: "to", Description: "RFC 3339 upper bound."},
				{Name: "sort", Enum: []string{"occurred_at", "action", "resource", "outcome"}, Description: "Order (default occurred_at); ties read newest first."},
				{Name: "dir", Enum: []string{"asc", "desc"}, Description: "Direction; default desc for occurred_at, asc otherwise."},
				{Name: "page", Type: "integer", Description: "1-based page number (default 1)."},
				{Name: "page_size", Type: "integer", Description: "Items per page, up to 200 (default 50)."},
			},
		},
		{
			Name:        "list_audit_actions",
			Title:       "List audit actions",
			Description: "The distinct action names in the audit log, for filtering.",
			Method:      http.MethodGet, Route: "/audit-log/actions",
		},
	}
}

func platformTools() []spec {
	user := map[string]string{"id": "User ID."}
	mapping := map[string]string{"id": "Mapping ID."}
	return []spec{
		{
			Name:        "list_tenants",
			Title:       "List workspaces",
			Description: "Workspaces (tenants) you can act in, with your role in each. Pass an ID as tenant_id to other tools. Sign-in connections only.",
			Method:      http.MethodGet, Route: "/tenants", Platform: true,
		},
		{
			Name:        "list_users",
			Title:       "List users",
			Description: "Platform users with roles and workspace memberships. Superadmins only.",
			Method:      http.MethodGet, Route: "/users", Platform: true,
			Query: pageParams,
		},
		{
			Name:        "get_user",
			Title:       "Get user",
			Description: "One platform user. Superadmins only.",
			Method:      http.MethodGet, Route: "/users/{id}", Platform: true,
			PathDocs: user,
		},
		{
			Name:  "create_user",
			Title: "Create user",
			Description: "Create a platform user with a password (12+ chars) or as SSO-only (email required), and " +
				"workspace memberships. Superadmins only.",
			Method: http.MethodPost, Route: "/users", Platform: true,
			Body: typeOf[models.CreateAdminUserRequest](), Required: []string{"username"},
			Enums: map[string][]string{"platform_role": {"superadmin", "member"}},
		},
		{
			Name:  "update_user",
			Title: "Update user",
			Description: "Change a user's name, email, password (revokes their sessions), platform role or " +
				"memberships (replaced as a set). Superadmins only.",
			Method: http.MethodPatch, Route: "/users/{id}", Platform: true,
			PathDocs: user,
			Body:     typeOf[models.UpdateAdminUserRequest](),
		},
		{
			Name:        "delete_user",
			Title:       "Delete user",
			Description: "Delete a platform user. You cannot delete yourself or the last superadmin. Superadmins only.",
			Method:      http.MethodDelete, Route: "/users/{id}", Platform: true,
			PathDocs: user, Destructive: true,
		},
		{
			Name:        "list_oidc_group_mappings",
			Title:       "List SSO group mappings",
			Description: "SSO (OIDC) group -> role mappings and recently seen groups. Superadmins only.",
			Method:      http.MethodGet, Route: "/oidc-group-mappings", Platform: true,
		},
		{
			Name:  "create_oidc_group_mapping",
			Title: "Create SSO group mapping",
			Description: "Map an IdP group to a workspace role (or, with no tenant, to superadmin). Once any mapping " +
				"exists, SSO users' roles are re-synced from their groups at every login. Superadmins only.",
			Method: http.MethodPost, Route: "/oidc-group-mappings", Platform: true,
			Body: typeOf[models.CreateOIDCGroupMappingRequest](), Required: []string{"group_name", "role"},
			Enums: map[string][]string{"role": {"admin", "editor", "viewer", "superadmin"}},
		},
		{
			Name:        "update_oidc_group_mapping",
			Title:       "Update SSO group mapping",
			Description: "Change a mapping's role or label. Superadmins only.",
			Method:      http.MethodPatch, Route: "/oidc-group-mappings/{id}", Platform: true,
			PathDocs: mapping,
			Body:     typeOf[models.UpdateOIDCGroupMappingRequest](),
		},
		{
			Name:        "delete_oidc_group_mapping",
			Title:       "Delete SSO group mapping",
			Description: "Delete a mapping; affected users' roles change at their next SSO login. Superadmins only.",
			Method:      http.MethodDelete, Route: "/oidc-group-mappings/{id}", Platform: true,
			PathDocs: mapping, Destructive: true,
		},
	}
}
