package mcp

import (
	"net/http"
	"reflect"

	apierrors "github.com/yassinebenameur/probara/api/internal/errors"
	"github.com/yassinebenameur/probara/api/internal/models"
	"github.com/yassinebenameur/probara/api/internal/services/aisettings"
	alertservice "github.com/yassinebenameur/probara/api/internal/services/alerts"
	auditservice "github.com/yassinebenameur/probara/api/internal/services/audit"
	"github.com/yassinebenameur/probara/api/internal/services/depsuggest"
	"github.com/yassinebenameur/probara/api/internal/services/discovery"
	"github.com/yassinebenameur/probara/api/internal/services/metricsquery"
	"github.com/yassinebenameur/probara/api/internal/services/notificationsettings"
	"github.com/yassinebenameur/probara/api/internal/services/results"
	sharedmodels "github.com/yassinebenameur/probara/shared/models"
	"github.com/yassinebenameur/probara/shared/notifications/deliverylog"
	"github.com/yassinebenameur/probara/shared/notifications/plugin"
)

// successResponse is an operation's success answer as its handler writes
// it. Body is the type of the value the handler writes, never a hand-made
// copy of it: its JSON schema is reflected, so field changes follow
// automatically. Media are non-JSON bodies, described without a schema.
type successResponse struct {
	Status int
	Body   reflect.Type
	// Nullable: the handler may write a bare JSON null instead of Body.
	Nullable bool
	Media    []string
}

func (r successResponse) description() string {
	switch {
	case r.Body == nil && len(r.Media) == 0:
		return "Success, no body."
	case r.Status == http.StatusCreated:
		return "Created."
	case r.Status == http.StatusAccepted:
		return "Accepted."
	default:
		return "Success."
	}
}

func jsonAnswer[T any](status int) successResponse {
	return successResponse{Status: status, Body: reflect.TypeFor[T]()}
}

func ok[T any]() successResponse { return jsonAnswer[T](http.StatusOK) }

// okOrNull is a 200 whose body is T or JSON null (a nil pointer the handler
// writes on purpose, e.g. "no analysis yet").
func okOrNull[T any]() successResponse {
	r := ok[T]()
	r.Nullable = true
	return r
}
func created[T any]() successResponse { return jsonAnswer[T](http.StatusCreated) }

var noContent = successResponse{Status: http.StatusNoContent}

func mediaAnswer(media ...string) successResponse {
	return successResponse{Status: http.StatusOK, Media: media}
}

// successResponses gives the success answer of each operation whose handler
// is known, keyed by operation ID. An operation missing here is documented
// as an undescribed 2XX, never guessed. Read the handler before adding one.
var successResponses = map[string]successResponse{
	// Monitors
	"list_monitors":                      ok[models.MonitorListResponse](),
	"get_monitor":                        ok[models.Monitor](),
	"create_monitor":                     created[models.Monitor](),
	"update_monitor":                     ok[models.Monitor](),
	"delete_monitor":                     noContent,
	"test_monitor_config":                ok[sharedmodels.TestCheckResponse](),
	"run_monitor_now":                    jsonAnswer[models.RunMonitorNowResponse](http.StatusAccepted),
	"get_monitor_analytics":              ok[models.MonitorAnalyticsResponse](),
	"get_monitor_results":                ok[models.MonitorResultsResponse](),
	"get_monitor_result":                 ok[models.CheckResult](),
	"get_monitor_results_batch":          ok[results.MonitorResultsBatchResponse](),
	"get_monitor_metrics_snapshots":      ok[models.MonitorMetricsSnapshotsResponse](),
	"delete_monitor_history":             noContent,
	"get_synthetic_screenshot":           mediaAnswer("image/png"),
	"list_group_members":                 ok[[]models.Monitor](),
	"add_group_members":                  noContent,
	"remove_group_members":               noContent,
	"list_monitor_dependencies":          ok[models.DependencyMonitorList](),
	"add_monitor_dependency":             noContent,
	"remove_monitor_dependency":          noContent,
	"list_monitor_dependents":            ok[models.DependencyMonitorList](),
	"get_dependency_graph":               ok[models.DependencyGraph](),
	"suggest_dependencies":               ok[depsuggest.Result](),
	"label_dependency_suggestion":        ok[models.AILabel](),
	"bulk_update_alerting":               ok[models.BulkAlertingResponse](),
	"bulk_delete_monitors":               ok[models.BulkDeleteMonitorsResponse](),
	"export_monitors":                    mediaAnswer("application/x-yaml"),
	"preview_monitor_import":             ok[models.ImportPreviewResponse](),
	"import_monitors":                    ok[models.ImportExecuteResponse](),
	"list_metric_series":                 ok[metricsquery.SeriesListResponse](),
	"query_metrics":                      ok[metricsquery.QueryResponse](),
	"get_agent_install":                  ok[sharedmodels.AgentInstallCommand](),
	"get_agent_collector_config":         mediaAnswer("application/yaml"),
	"get_agent_install_script_unix":      mediaAnswer("text/x-shellscript"),
	"get_agent_install_script_windows":   mediaAnswer("text/plain"),
	"get_agent_uninstall_script_unix":    mediaAnswer("text/x-shellscript"),
	"get_agent_uninstall_script_windows": mediaAnswer("text/plain"),
	"get_push_info":                      ok[models.PushInfo](),
	"snooze_monitor":                     created[models.MaintenanceWindow](),
	"list_monitor_versions":              ok[models.MonitorVersionListResponse](),
	"get_monitor_version":                ok[models.MonitorVersion](),
	"diff_monitor_version":               ok[models.MonitorVersionDiffResponse](),
	"restore_monitor_version":            ok[models.RestoreMonitorVersionResponse](),

	// Identity, dashboard, mesh, locations
	"whoami":                      ok[models.AuthContextResponse](),
	"get_dashboard_overview":      ok[models.DashboardOverviewResponse](),
	"get_dashboard_summary":       ok[models.DashboardSummaryResponse](),
	"list_problem_monitors":       ok[models.DashboardProblemMonitorsResponse](),
	"list_recent_failures":        ok[models.DashboardRecentFailuresResponse](),
	"get_dashboard_recent_alerts": ok[models.DashboardRecentAlertsResponse](),
	"get_group_sparkline":         ok[models.DashboardGroupSparklineResponse](),
	"get_mesh":                    ok[models.MeshResponse](),
	"get_mesh_edge_history":       ok[models.MeshEdgeHistoryResponse](),
	"probe_mesh_edge":             ok[sharedmodels.TestCheckResponse](),
	"list_locations":              ok[models.LocationListResponse](),
	"get_location":                ok[models.Location](),
	"create_location":             created[models.Location](),
	"update_location":             ok[models.Location](),
	"delete_location":             ok[models.DeleteLocationResponse](),
	"get_location_deploy_info":    ok[models.LocationDeployInfo](),

	// Maintenance
	"list_maintenance_windows":         ok[models.MaintenanceWindowListResponse](),
	"get_maintenance_window":           ok[models.MaintenanceWindow](),
	"create_maintenance_window":        created[models.MaintenanceWindow](),
	"update_maintenance_window":        ok[models.MaintenanceWindow](),
	"delete_maintenance_window":        noContent,
	"preview_maintenance_recurrence":   ok[models.PreviewMaintenanceRecurrenceResponse](),
	"list_maintenance_occurrences":     ok[models.MaintenanceOccurrenceListResponse](),
	"end_maintenance_occurrence":       ok[models.MaintenanceWindow](),
	"skip_next_maintenance_occurrence": ok[models.MaintenanceWindow](),

	// Discovery
	"list_discovery_integrations":      ok[[]discovery.Integration](),
	"create_discovery_integration":     created[discovery.Integration](),
	"update_discovery_integration":     ok[discovery.Integration](),
	"get_discovery_namespaces":         ok[discovery.NamespaceList](),
	"delete_discovery_integration":     noContent,
	"rotate_discovery_collector_token": ok[discovery.CollectorTokenResponse](),
	"get_discovery_coverage":           ok[discovery.Overview](),
	"approve_discovery_suggestion":     ok[discovery.ApproveResponse](),
	"link_discovery_monitor":           noContent,
	"unlink_discovery_monitor":         noContent,

	// Alerts
	"list_alerts":               ok[models.AlertListResponse](),
	"list_recent_alerts":        ok[[]models.AlertWithDetails](),
	"get_alert":                 ok[models.AlertWithDetails](),
	"acknowledge_alert":         ok[models.AlertWithDetails](),
	"resolve_alert":             ok[models.AlertWithDetails](),
	"unacknowledge_alert":       ok[models.AlertWithDetails](),
	"snooze_alert":              ok[models.AlertWithDetails](),
	"unsnooze_alert":            ok[models.AlertWithDetails](),
	"bulk_acknowledge_alerts":   ok[alertservice.BulkResult](),
	"bulk_resolve_alerts":       ok[alertservice.BulkResult](),
	"bulk_unacknowledge_alerts": ok[alertservice.BulkResult](),
	"bulk_snooze_alerts":        ok[alertservice.BulkResult](),
	"bulk_unsnooze_alerts":      ok[alertservice.BulkResult](),
	"label_alert_triage":        ok[models.AILabel](),
	"list_alert_deliveries":     ok[deliverylog.Page](),

	// Incidents
	"list_incidents":                      ok[models.IncidentListResponse](),
	"create_incident":                     created[models.IncidentDetail](),
	"get_incident":                        ok[models.IncidentDetail](),
	"update_incident":                     ok[models.IncidentDetail](),
	"transition_incident_state":           ok[models.IncidentDetail](),
	"add_incident_timeline_entry":         created[models.IncidentDetail](),
	"attach_alert_to_incident":            ok[models.IncidentDetail](),
	"detach_alert_from_incident":          ok[models.IncidentDetail](),
	"attach_monitor_to_incident":          ok[models.IncidentDetail](),
	"detach_monitor_from_incident":        ok[models.IncidentDetail](),
	"publish_incident_to_status_page":     ok[models.IncidentDetail](),
	"unpublish_incident_from_status_page": ok[models.IncidentDetail](),
	"request_incident_ai_analysis":        jsonAnswer[models.IncidentAIAnalysis](http.StatusAccepted),
	"get_incident_ai_analysis":            okOrNull[models.IncidentAIAnalysis](),
	"label_incident_ai_analysis":          ok[models.AILabel](),

	// Notifications and workspace settings
	"list_alert_channels":          ok[models.AlertChannelListResponse](),
	"get_alert_channel":            ok[models.AlertChannel](),
	"create_alert_channel":         created[models.AlertChannel](),
	"update_alert_channel":         ok[models.AlertChannel](),
	"delete_alert_channel":         noContent,
	"test_alert_channel":           noContent,
	"list_channel_deliveries":      ok[deliverylog.Page](),
	"list_alert_channel_plugins":   ok[[]plugin.Manifest](),
	"get_alert_channel_plugin":     ok[plugin.Manifest](),
	"get_notification_settings":    ok[notificationsettings.Settings](),
	"update_notification_settings": ok[notificationsettings.Settings](),
	"get_ai_settings":              ok[aisettings.Settings](),
	"update_ai_settings":           ok[aisettings.Settings](),
	"test_ai_connection":           ok[aisettings.TestResult](),
	"export_ai_labels":             mediaAnswer("application/x-ndjson"),
	"list_api_keys":                ok[models.ApiKeyListResponse](),
	"create_api_key":               created[models.ApiKey](),
	"revoke_api_key":               noContent,
	"get_tenant_settings":          ok[models.TenantSettings](),
	"update_tenant_settings":       ok[models.TenantSettings](),
	"list_audit_log":               ok[auditservice.ListResponse](),
	"list_audit_actions":           ok[models.AuditActionListResponse](),

	// SLAs
	"list_slas":  ok[models.SLAListResponse](),
	"create_sla": created[models.SLA](),
	"get_sla":    ok[models.SLA](),
	"update_sla": ok[models.SLA](),
	"delete_sla": noContent,
	"get_sla_report": {Status: http.StatusOK, Body: reflect.TypeFor[models.SLAReport](),
		Media: []string{"text/csv", "application/pdf"}},
	"list_sla_reports": ok[models.SLAReportRefListResponse](),
	"issue_sla_report": created[models.SLAReport](),
	"get_issued_sla_report": {Status: http.StatusOK, Body: reflect.TypeFor[models.SLAReport](),
		Media: []string{"text/csv", "application/pdf"}},
	"get_sla_report_schedule":    ok[models.SLAReportScheduleResponse](),
	"set_sla_report_schedule":    ok[models.SLAReportScheduleResponse](),
	"delete_sla_report_schedule": noContent,

	// Status pages
	"list_status_pages":                       ok[models.StatusPageListResponse](),
	"get_status_page":                         ok[models.StatusPage](),
	"create_status_page":                      created[models.StatusPage](),
	"update_status_page":                      ok[models.StatusPage](),
	"delete_status_page":                      noContent,
	"get_status_page_template":                ok[models.StatusPageTemplateState](),
	"reset_status_page_template":              ok[models.StatusPageTemplateState](),
	"get_default_status_page_template":        mediaAnswer("text/plain"),
	"get_status_page_template_version":        mediaAnswer("text/plain"),
	"save_status_page_template_draft":         ok[models.StatusPageTemplateState](),
	"discard_status_page_template_draft":      ok[models.StatusPageTemplateState](),
	"publish_status_page_template":            ok[models.StatusPageTemplateState](),
	"revert_status_page_template":             ok[models.StatusPageTemplateState](),
	"list_status_page_library_templates":      ok[models.StatusPageLibraryTemplateListResponse](),
	"create_status_page_library_template":     created[models.StatusPageLibraryTemplate](),
	"get_status_page_library_template":        ok[models.StatusPageLibraryTemplate](),
	"get_status_page_library_template_source": mediaAnswer("text/plain"),
	"update_status_page_library_template":     ok[models.StatusPageLibraryTemplate](),
	"delete_status_page_library_template":     noContent,

	// Platform
	"list_tenants":              ok[models.TenantListResponse](),
	"list_users":                ok[models.AdminUserListResponse](),
	"get_user":                  ok[models.AdminUser](),
	"create_user":               created[models.AdminUser](),
	"update_user":               ok[models.AdminUser](),
	"delete_user":               noContent,
	"list_oidc_group_mappings":  ok[models.OIDCGroupMappingListResponse](),
	"create_oidc_group_mapping": created[models.OIDCGroupMapping](),
	"update_oidc_group_mapping": ok[models.OIDCGroupMapping](),
	"delete_oidc_group_mapping": noContent,

	// Workspaces, sign-in security, status page access, config as code
	"list_workspace_overview":         ok[models.WorkspaceOverviewResponse](),
	"create_tenant":                   created[models.Tenant](),
	"rename_tenant":                   ok[models.Tenant](),
	"delete_tenant":                   ok[models.DeleteWorkspaceResponse](),
	"unlock_user":                     ok[models.UnlockUserResponse](),
	"list_user_sessions":              ok[models.AdminSessionListResponse](),
	"revoke_user_session":             noContent,
	"revoke_user_sessions":            ok[models.RevokeSessionsResponse](),
	"invite_user":                     ok[models.AccountLinkResponse](),
	"create_password_reset_link":      ok[models.AccountLinkResponse](),
	"reset_user_two_factor":           ok[models.ResetTwoFactorResponse](),
	"list_status_page_access_tokens":  ok[models.StatusPageAccessTokenListResponse](),
	"create_status_page_access_token": created[models.CreatedStatusPageAccessToken](),
	"revoke_status_page_access_token": noContent,
	"plan_monitor_apply":              ok[models.MonitorApplyPlan](),
	"apply_monitors":                  ok[models.MonitorApplyResult](),

	// Routes documented outside the catalog (openapi_routes.go)
	"push_heartbeat":        ok[models.PushAcceptedResponse](),
	"push_heartbeat_report": ok[models.PushAcceptedResponse](),
	"export_otlp_metrics":   mediaAnswer("application/x-protobuf", "application/json"),
	"stream_alerts":         mediaAnswer("text/event-stream"),
	"get_openapi_document":  mediaAnswer("application/json"),
}

// operationErrors are error answers of catalog operations whose handler
// writes more than the shared error envelope, keyed by operation ID. Like
// successResponses, add one only after reading the handler.
var operationErrors = map[string]map[int]routeError{
	// handlers/users/lifecycle.go. Their success is one shape with or without
	// email delivery: delivered=true and no link when mailed, else the link
	// (once) and the reason.
	"invite_user": {
		400: {Description: "Invalid method; or single sign-on is enabled and no method was given for an account not " +
			"linked to the identity provider yet (validation_error).", Bodies: []reflect.Type{reflect.TypeFor[apierrors.ErrorResponse]()}},
		409: {Description: "The account is disabled, signs in with single sign-on (password invite), already has a " +
			"password, or single sign-on is not configured (sso invite).", Bodies: []reflect.Type{reflect.TypeFor[apierrors.ErrorResponse]()}},
		502: {Description: "Email delivery is configured but the mail could not be sent (delivery_failed); the link is " +
			"not returned.", Bodies: []reflect.Type{reflect.TypeFor[apierrors.ErrorResponse]()}},
	},
	"create_password_reset_link": {
		409: {Description: "The account is disabled, signs in with single sign-on, or has no password yet (invite it " +
			"instead).", Bodies: []reflect.Type{reflect.TypeFor[apierrors.ErrorResponse]()}},
		502: {Description: "Email delivery is configured but the mail could not be sent (delivery_failed); the link is " +
			"not returned.", Bodies: []reflect.Type{reflect.TypeFor[apierrors.ErrorResponse]()}},
	},
	// import.writeApplyError: a refused apply answers with the plan it
	// computed plus error and message, and wrote nothing.
	"apply_monitors": {
		422: {Description: "The document is invalid (error invalid_document): the body is the plan with each " +
			"monitor's errors. Nothing was written.", Bodies: []reflect.Type{reflect.TypeFor[models.MonitorApplyResult]()}},
		409: {Description: "Refused, nothing written. With the plan and the reason: the monitors changed since the " +
			"fingerprinted plan (plan_changed), prune or a probable key rename needs force (prune_refused, " +
			"probable_rename), a monitor was saved meanwhile (apply_conflict; retry). In the error envelope: a key " +
			"already taken (key_taken).",
			Bodies: []reflect.Type{reflect.TypeFor[models.MonitorApplyResult](), reflect.TypeFor[apierrors.ErrorResponse]()}},
	},
}
