// API Error types
export interface ApiError {
  error: string;
  message: string;
}

export interface ApiResponse<T> {
  data?: T;
  error?: ApiError;
}

// Tenant types
export interface Tenant {
  id: string;
  name: string;
  created_at: string;
  updated_at: string;
}

export interface TenantListResponse {
  items: Tenant[];
}

export interface TenantSettings {
  data_retention_days: number;
  dashboard_group_tags: string[];
}

export interface UpdateTenantSettingsRequest {
  data_retention_days?: number;
  dashboard_group_tags?: string[];
}

// Admin user types
export interface AdminUser {
  id: string;
  username: string;
  created_at: string;
  updated_at: string;
  last_login_at?: string;
  disabled_at?: string;
}

export interface AdminUserListResponse {
  items: AdminUser[];
  page: number;
  page_size: number;
  total: number;
}

export interface CreateAdminUserRequest {
  username: string;
  password: string;
}

export interface UpdateAdminUserRequest {
  username?: string;
  password?: string;
}

// API Key types
export interface ApiKey {
  id: string;
  name: string;
  key_prefix: string;
  key?: string;
  created_at: string;
  revoked_at?: string;
}

export interface ApiKeyListResponse {
  items: ApiKey[];
  page: number;
  page_size: number;
  total: number;
}

export interface CreateApiKeyRequest {
  name: string;
}

// Monitor types
export type MonitorType =
  | "http"
  | "ping"
  | "dns"
  | "grpc"
  | "group"
  | "agent"
  | "push"
  | "sip"
  | "synthetic_api"
  | "synthetic_browser"
  | "redis"
  | "postgres"
  | "mongodb"
  | "rabbitmq"
  | "tcp";

// Secret config fields (passwords, connection strings) are write-only: the
// API returns "***" in their place, and submitting "***" back keeps the
// stored value unchanged.
export const MASKED_SECRET = "***";

export interface HTTPStatusRange {
  min: number;
  max: number;
}

export type HTTPHeaderAssertionOp =
  | "exists"
  | "equals"
  | "contains"
  | "regex"
  | "not_equals"
  | "not_contains"
  | "not_regex";

export interface HTTPHeaderAssertion {
  name: string;
  op: HTTPHeaderAssertionOp;
  value?: string;
  case_insensitive?: boolean;
}

export type HTTPBodyAssertionOp = "contains" | "not_contains" | "regex" | "not_regex";

export interface HTTPBodyAssertion {
  op: HTTPBodyAssertionOp;
  value: string;
  case_insensitive?: boolean;
}

export type HTTPJSONAssertionOp =
  | "exists"
  | "equals"
  | "not_equals"
  | "contains"
  | "not_contains"
  | "regex"
  | "number_gt"
  | "number_gte"
  | "number_lt"
  | "number_lte"
  | "bool_is";

export interface HTTPJSONAssertion {
  path: string;
  op: HTTPJSONAssertionOp;
  value?: string;
  case_insensitive?: boolean;
}

export interface HTTPMonitorConfig {
  url: string;
  method: string;
  headers?: Record<string, string>;
  body?: string;
  expected_status?: number;
  expected_statuses?: number[];
  expected_status_ranges?: HTTPStatusRange[];
  expected_status_classes?: string[];
  expected_body_substring?: string;
  expected_body_regex?: string;
  body_assertions?: HTTPBodyAssertion[];
  response_header_assertions?: HTTPHeaderAssertion[];
  json_assertions?: HTTPJSONAssertion[];
  max_latency_ms?: number;
  follow_redirects?: boolean;
  max_redirects?: number;
  tls_skip_verify?: boolean;
  tls_min_days_valid?: number;
  tls_server_name?: string;
  tls_ca_pem?: string;
  collect_timing?: boolean;
}

export interface PingMonitorConfig {
  host: string;
}

export interface DNSMonitorConfig {
  host: string;
  record_type?: string;
  expected_answers?: string[];
}

export interface GRPCMonitorConfig {
  host: string;
  port?: number;
  service?: string;
  use_tls?: boolean;
}

export interface TCPMonitorConfig {
  host: string;
  port: number;
  use_tls?: boolean;
  tls_skip_verify?: boolean;
}

export interface GroupMonitorConfig {
  monitor_ids: string[];
}

export interface AgentMonitorConfig {
  agent_id: string;
  expected_interval_seconds: number;
}

export interface PushMonitorConfig {
  push_token: string;
  expected_interval_seconds: number;
  grace_period_seconds: number;
}

export interface SIPMonitorConfig {
  host: string;
  port: number;
  transport: "udp" | "tcp";
  expected_status?: number;
}

// Pasted TLS material shared by the database monitor types: CA PEM for
// private-CA verification, client cert+key for mutual TLS. The client key is
// a write-only secret.
export interface DBTLSMaterial {
  tls_ca_pem?: string;
  tls_client_cert_pem?: string;
  tls_client_key_pem?: string; // secret, write-only
}

export interface RedisMonitorConfig extends DBTLSMaterial {
  connection_string?: string; // secret, write-only
  host?: string;
  port?: number;
  username?: string;
  password?: string; // secret, write-only
  db?: number;
  tls_enabled?: boolean;
  tls_skip_verify?: boolean;
  expected_role?: "master" | "replica";
  max_latency_ms?: number;
  warn_latency_ms?: number;
}

export type DBQueryValueOp =
  | "equals"
  | "not_equals"
  | "contains"
  | "number_gt"
  | "number_gte"
  | "number_lt"
  | "number_lte";

export interface PostgresMonitorConfig extends DBTLSMaterial {
  connection_string?: string; // secret, write-only
  host?: string;
  port?: number;
  database?: string;
  username?: string;
  password?: string; // secret, write-only
  ssl_mode?: "disable" | "require" | "verify-full";
  query?: string;
  query_value_op?: DBQueryValueOp;
  query_value?: string;
  max_latency_ms?: number;
  warn_latency_ms?: number;
}

export interface MongoDBMonitorConfig extends DBTLSMaterial {
  connection_string?: string; // secret, write-only
  host?: string;
  port?: number;
  username?: string;
  password?: string; // secret, write-only
  auth_source?: string;
  tls_enabled?: boolean;
  tls_skip_verify?: boolean;
  replica_set?: string;
  max_latency_ms?: number;
  warn_latency_ms?: number;
}

export interface RabbitMQMonitorConfig extends DBTLSMaterial {
  connection_string?: string; // secret, write-only
  host?: string;
  port?: number;
  username?: string;
  password?: string; // secret, write-only
  vhost?: string;
  tls_enabled?: boolean;
  tls_skip_verify?: boolean;
  max_latency_ms?: number;
  warn_latency_ms?: number;
}

// Per-check metrics recorded by the database/broker checkers, keyed by
// monitor type in metrics_data (e.g. {"redis": {...}}).
export interface DBMetrics {
  server_version?: string;
  product?: string;
  role?: string;
  replica_set?: string;
  connected_clients?: number;
  used_memory_bytes?: number;
  latency_warn_ms?: number;
}

export type DBMetricsEnvelope = Partial<Record<"redis" | "postgres" | "mongodb" | "rabbitmq", DBMetrics>>;

export type SyntheticFailureMode = "fail_fast" | "continue";

export type SyntheticAPIAssertionTarget = "status" | "header" | "body" | "json";

export interface SyntheticAPIAssertionConfig {
  target: SyntheticAPIAssertionTarget;
  op: string;
  path?: string;
  value?: unknown;
}

export interface SyntheticAPIExtractConfig {
  name: string;
  from: "json" | "header";
  path: string;
  sensitive?: boolean;
}

export interface SyntheticAPIRequestConfig {
  method: string;
  url: string;
  headers?: Record<string, string>;
  body?: string;
  timeout_seconds?: number;
  follow_redirects?: boolean;
  max_redirects?: number;
}

export interface SyntheticAPIStepConfig {
  id: string;
  name?: string;
  request: SyntheticAPIRequestConfig;
  assert?: SyntheticAPIAssertionConfig[];
  extract?: SyntheticAPIExtractConfig[];
}

export interface SyntheticAPIMonitorConfig {
  base_url?: string;
  failure_mode?: SyntheticFailureMode;
  variables?: Record<string, string>;
  steps: SyntheticAPIStepConfig[];
}

export type SyntheticBrowserAction =
  | "goto"
  | "click"
  | "fill"
  | "wait_for"
  | "assert_visible"
  | "assert_text"
  | "assert_url";

export interface SyntheticBrowserArtifacts {
  screenshot_on_failure?: boolean;
  trace_on_failure?: boolean;
  har_on_failure?: boolean;
}

export interface SyntheticBrowserStepConfig {
  id: string;
  action: SyntheticBrowserAction;
  url?: string;
  selector?: string;
  value?: string;
  timeout_seconds?: number;
}

export interface SyntheticBrowserMonitorConfig {
  start_url: string;
  device?: string;
  failure_mode?: SyntheticFailureMode;
  variables?: Record<string, string>;
  artifacts?: SyntheticBrowserArtifacts;
  steps: SyntheticBrowserStepConfig[];
}

export type MonitorConfig =
  | HTTPMonitorConfig
  | PingMonitorConfig
  | DNSMonitorConfig
  | GRPCMonitorConfig
  | TCPMonitorConfig
  | GroupMonitorConfig
  | AgentMonitorConfig
  | PushMonitorConfig
  | SIPMonitorConfig
  | SyntheticAPIMonitorConfig
  | SyntheticBrowserMonitorConfig
  | RedisMonitorConfig
  | PostgresMonitorConfig
  | MongoDBMonitorConfig
  | RabbitMQMonitorConfig;

export type NotificationMode = 'default' | 'custom';

export interface ChannelAssignment {
  channel_id: string;
  channel_name?: string;
  channel_type?: string;
  delay_seconds: number;
}

export interface NotificationSettings {
  default_channels: ChannelAssignment[];
  alert_reminder_seconds: number;
  auto_create_incident: boolean;
  latency_anomaly_enabled: boolean;
  latency_baseline_window_hours: number;
  latency_anomaly_sensitivity: number;
  latency_anomaly_min_breach_seconds: number;
  latency_anomaly_min_delta_pct: number;
}

export type AlertKind = 'availability' | 'latency_anomaly';

export type MonitorState = 'unknown' | 'up' | 'suspect' | 'down';

// Maintenance window types
export type MaintenanceWindowStatus = 'active' | 'upcoming' | 'past';

export interface MaintenanceWindowMonitorRef {
  id: string;
  name: string;
  type: string;
}

export interface MaintenanceWindow {
  id: string;
  tenant_id: string;
  title: string;
  description: string;
  starts_at: string;
  ends_at: string;
  monitor_ids: string[];
  monitors?: MaintenanceWindowMonitorRef[];
  status: MaintenanceWindowStatus;
  created_at: string;
  updated_at: string;
}

export interface MaintenanceWindowListResponse {
  items: MaintenanceWindow[];
  page: number;
  page_size: number;
  total: number;
}

export interface CreateMaintenanceWindowRequest {
  title: string;
  description?: string;
  starts_at: string;
  ends_at: string;
  monitor_ids: string[];
}

export interface UpdateMaintenanceWindowRequest {
  title?: string;
  description?: string;
  starts_at?: string;
  ends_at?: string;
  monitor_ids?: string[];
}

export interface SnoozeMonitorRequest {
  until?: string;
  duration_minutes?: number;
}

export interface Monitor {
  id: string;
  tenant_id: string;
  name: string;
  type: MonitorType;
  config?: MonitorConfig; // Optional for backward compatibility
  interval_seconds: number;
  timeout_seconds: number;
  alert_policy_id?: string;
  alert_policy_ids?: string[];
  enabled: boolean;
  tags?: string[];
  next_run_at?: string;
  agent_id?: string; // Unique identifier for agent monitors
  push_token?: string; // Unique token for push monitors
  member_ids?: string[]; // Populated for group monitors
  depends_on_ids?: string[]; // Upstream monitors this one depends on
  created_at: string;
  updated_at: string;
  consecutive_failures_threshold: number;
  notification_mode: NotificationMode;
  notification_channels?: ChannelAssignment[];
  current_state?: MonitorState;
  in_maintenance?: boolean;
  maintenance_until?: string; // Latest ends_at among covering active windows
  // Old format fields (for backward compatibility during migration)
  url?: string;
  method?: string;
  headers?: Record<string, string>;
  body?: string;
  expected_status?: number;
  expected_body_substring?: string;
}

export interface CreateMonitorRequest {
  name: string;
  type: MonitorType;
  config: MonitorConfig;
  interval_seconds: number;
  timeout_seconds: number;
  alert_policy_id?: string;
  alert_policy_ids?: string[];
  enabled?: boolean;
  tags?: string[];
  consecutive_failures_threshold?: number;
  notification_mode?: NotificationMode;
  notification_channels?: ChannelAssignment[];
  depends_on_ids?: string[];
}

export interface UpdateMonitorRequest {
  name?: string;
  type?: MonitorType;
  config?: MonitorConfig;
  interval_seconds?: number;
  timeout_seconds?: number;
  alert_policy_id?: string;
  alert_policy_ids?: string[];
  enabled?: boolean;
  tags?: string[];
  consecutive_failures_threshold?: number;
  notification_mode?: NotificationMode;
  notification_channels?: ChannelAssignment[];
  depends_on_ids?: string[];
}

export interface MonitorListResponse {
  items: Monitor[];
  page: number;
  page_size: number;
  total: number;
}

// Dependency graph types
export interface DependencyMonitor {
  id: string;
  name: string;
  type: MonitorType;
  current_state: MonitorState;
  last_state_change_at?: string;
}

export interface DependencyGraphEdge {
  from: string; // downstream monitor (depends on `to`)
  to: string; // upstream monitor
}

export interface DependencyGraph {
  nodes: DependencyMonitor[];
  edges: DependencyGraphEdge[];
}

// Alert types
export type AlertStatus = 'active' | 'acknowledged' | 'resolved';

export interface Alert {
  id: string;
  tenant_id: string;
  monitor_id: string;
  alert_policy_id?: string;
  status: AlertStatus;
  triggered_at: string;
  acknowledged_at?: string;
  resolved_at?: string;
  failure_count: number;
  last_error?: string;
  kind: AlertKind;
  baseline_latency_ms?: number;
  observed_latency_ms?: number;
  anomaly_score?: number;
  root_cause_monitor_id?: string;
  root_cause_down_since?: string;
  created_at: string;
  updated_at: string;
  monitor_name?: string;
  policy_name?: string;
  root_cause_monitor_name?: string;
}

export interface AlertListResponse {
  items: Alert[];
  page: number;
  page_size: number;
  total: number;
}

export type AlertStreamEventType = 'created' | 'acknowledged' | 'resolved';

export interface AlertStreamEvent {
  type: AlertStreamEventType;
  alert: Alert;
  received_at: string;
}

export type DashboardRange = '1h' | '24h' | '7d' | '30d' | '90d' | '365d';
export type DashboardFailureState = 'firing' | 'resolved';

export interface DashboardStats {
  total_monitors: number;
  active_monitors: number;
  http_monitors: number;
  agent_monitors: number;
  overall_uptime: number;
  avg_response_ms: number;
}

export interface DashboardTrendPoint {
  bucket_start: string;
  label: string;
  uptime: number;
  response_time: number;
  total_checks: number;
}

export interface DashboardActivityPoint {
  bucket_start: string;
  label: string;
  checks: number;
  failures: number;
}

export interface DashboardMonitorHealth {
  monitor_id: string;
  monitor_name: string;
  enabled: boolean;
  in_maintenance: boolean;
  latest_status: string | null;
  latest_check_at: string | null;
}

export interface DashboardOpsSummary {
  up_monitors: number;
  down_monitors: number;
  paused_monitors: number;
  maintenance_monitors: number;
  active_alerts: number;
  acknowledged_alerts: number;
}

export interface DashboardProblemMonitor {
  monitor_id: string;
  monitor_name: string;
  current_status: string | null;
  failure_count: number;
  error_count: number;
  uptime: number;
  latest_failure_at: string | null;
}

export interface DashboardFailureEvent {
  check_result_id: string;
  monitor_id: string;
  monitor_name: string;
  status: 'failure' | 'error' | 'success' | 'degraded';
  result_source: 'monitor' | 'platform';
  error_message?: string;
  latency_ms?: number;
  occurred_at: string;
  state: DashboardFailureState;
  resolved_at?: string;
}

export interface DashboardOverviewResponse {
  range: DashboardRange;
  generated_at: string;
  available_tags: string[];
  stats: DashboardStats;
  trend: DashboardTrendPoint[];
  activity_24h: DashboardActivityPoint[];
  ops_summary: DashboardOpsSummary;
  monitor_health: DashboardMonitorHealth[];
  problem_monitors: DashboardProblemMonitor[];
  recent_failures: DashboardFailureEvent[];
  recent_alerts: Alert[];
}

export interface DashboardSummaryResponse {
  range: DashboardRange;
  generated_at: string;
  available_tags: string[];
  group_tags: string[];
  stats: DashboardStats;
  trend: DashboardTrendPoint[];
  activity_24h: DashboardActivityPoint[];
  ops_summary: DashboardOpsSummary;
  monitor_health: DashboardMonitorHealth[];
  groups: DashboardGroup[];
}

export interface DashboardGroupMember {
  monitor_id: string;
  monitor_name: string;
  uptime: number;
  current_status: string | null;
}

export interface DashboardGroup {
  tag: string | null;
  monitor_count: number;
  uptime: number;
  attention_count: number;
  worst_member: DashboardGroupMember | null;
  members: DashboardGroupMember[];
}

export interface DashboardGroupSparklineResponse {
  tag: string | null;
  range: DashboardRange;
  buckets: number[];
}

export interface DashboardProblemMonitorsResponse {
  range: DashboardRange;
  generated_at: string;
  problem_monitors: DashboardProblemMonitor[];
}

export interface DashboardRecentFailuresResponse {
  range: DashboardRange;
  generated_at: string;
  recent_failures: DashboardFailureEvent[];
}

export interface DashboardRecentAlertsResponse {
  range: DashboardRange;
  generated_at: string;
  recent_alerts: Alert[];
}

// Incident types
export type IncidentState = 'investigating' | 'identified' | 'monitoring' | 'resolved';
export type IncidentSource = 'manual' | 'auto';
export type IncidentSeverity = 'critical' | 'high' | 'medium' | 'low';

export type IncidentTimelineEntryType = 'system' | 'internal_note' | 'public_update';

export interface IncidentTimelineEntry {
  id: string;
  tenant_id: string;
  incident_id: string;
  entry_type: IncidentTimelineEntryType;
  message: string;
  metadata?: Record<string, unknown>;
  created_at: string;
}

export interface IncidentAlertSummary {
  id: string;
  monitor_id: string;
  alert_policy_id?: string;
  status: AlertStatus;
  triggered_at: string;
  acknowledged_at?: string;
  resolved_at?: string;
  failure_count: number;
  last_error?: string;
  created_at: string;
  updated_at: string;
  monitor_name: string;
  policy_name?: string;
}

export interface IncidentMonitorSummary {
  id: string;
  tenant_id: string;
  name: string;
  type: MonitorType;
  created_at: string;
  updated_at: string;
}

export interface IncidentStatusPagePublication {
  status_page_id: string;
  status_page_slug: string;
  status_page_title: string;
  published_at: string;
  unpublished_at?: string;
  monitor_ids: string[];
}

export interface IncidentListItem {
  id: string;
  title: string;
  state: IncidentState;
  source: IncidentSource;
  updated_at: string;
  resolved_at?: string;
  linked_alert_count: number;
  linked_monitor_count: number;
  publication_count: number;
}

export interface IncidentDetail {
  id: string;
  tenant_id: string;
  title: string;
  summary: string;
  severity: IncidentSeverity;
  owner_user_id?: string;
  owner_username?: string;
  state: IncidentState;
  resolved_at?: string;
  is_auto_created: boolean;
  auto_monitor_id?: string;
  auto_alert_policy_id?: string;
  created_at: string;
  updated_at: string;
  alerts: IncidentAlertSummary[];
  monitors: IncidentMonitorSummary[];
  publications: IncidentStatusPagePublication[];
  timeline: IncidentTimelineEntry[];
  ai_analysis?: IncidentAIAnalysis | null;
}

export type IncidentAIAnalysisStatus = 'pending' | 'ready' | 'failed';

export interface IncidentAIAnalysis {
  id: string;
  tenant_id: string;
  incident_id: string;
  status: IncidentAIAnalysisStatus;
  model?: string;
  summary?: string;
  probable_root_cause?: string;
  contributing_factors?: string[];
  recommended_actions?: string[];
  confidence?: string;
  evidence?: unknown;
  error_message?: string;
  requested_by?: string;
  created_at: string;
  completed_at?: string;
}

export interface DependencySuggestion {
  monitor_id: string;
  monitor_name: string;
  depends_on_id: string;
  depends_on_name: string;
  reason: string;
  confidence: string;
}

export interface DependencySuggestionResult {
  suggestions: DependencySuggestion[];
  model?: string;
  analyzed_pairs: number;
}

export interface AISettings {
  enabled: boolean;
  provider: string;
  base_url: string;
  model: string;
  json_mode: string;
  max_tokens: number;
  timeout_seconds: number;
  has_api_key: boolean;
}

export interface AISettingsUpdate {
  enabled?: boolean;
  provider?: string;
  base_url?: string;
  model?: string;
  json_mode?: string;
  max_tokens?: number;
  timeout_seconds?: number;
  // api_key: omit to leave unchanged, "" to clear, value to set.
  api_key?: string;
}

export interface AITestRequest {
  provider: string;
  base_url: string;
  model: string;
  json_mode: string;
  api_key?: string;
}

export interface AITestResult {
  ok: boolean;
  model?: string;
  message?: string;
}

export interface IncidentListResponse {
  items: IncidentListItem[];
  page: number;
  page_size: number;
  total: number;
}

export interface CreateIncidentRequest {
  title: string;
  summary?: string;
  severity: IncidentSeverity;
  owner_user_id: string;
  alert_id?: string;
  monitor_id?: string;
}

export interface UpdateIncidentRequest {
  title?: string;
  summary?: string;
  severity?: IncidentSeverity;
  owner_user_id?: string;
}

export interface UpdateIncidentStateRequest {
  state: IncidentState;
}

export interface AttachIncidentAlertsRequest {
  alert_ids: string[];
}

export interface AttachIncidentMonitorsRequest {
  monitor_ids: string[];
}

export interface CreateIncidentTimelineEntryRequest {
  entry_type: 'internal_note' | 'public_update';
  message: string;
  metadata?: Record<string, unknown>;
}

export interface PublishIncidentToStatusPageRequest {
  monitor_ids: string[];
}

// Alert Channel types
// Plugin types are dynamic now (registry-driven), so this is a free-form string.
export type AlertChannelType = string;

export interface AlertChannel {
  id: string;
  tenant_id: string;
  name: string;
  type: AlertChannelType;
  config: Record<string, any>;
  is_active: boolean;
  created_at: string;
  updated_at: string;
}

export interface CreateAlertChannelRequest {
  name: string;
  type: AlertChannelType;
  config: Record<string, any>;
  is_active?: boolean;
}

export interface UpdateAlertChannelRequest {
  name?: string;
  config?: Record<string, any>;
  is_active?: boolean;
}

export interface AlertChannelListResponse {
  items: AlertChannel[];
  page: number;
  page_size: number;
  total: number;
}

// Plugin manifest (mirrors shared/notifications/plugin.Manifest).
export type PluginFieldType =
  | 'string'
  | 'url'
  | 'email_list'
  | 'textarea'
  | 'secret'
  | 'bool';

export type PluginCapability = 'rendered_alert' | 'raw_event' | 'testable';

export interface PluginField {
  key: string;
  label: string;
  placeholder?: string;
  help?: string;
  type: PluginFieldType;
  required?: boolean;
  secret?: boolean;
  default?: unknown;
}

export interface PluginManifest {
  type: string;
  display_name: string;
  description: string;
  icon_key: string;
  docs_url?: string;
  version: string;
  capabilities: PluginCapability[];
  fields: PluginField[];
}

// Status Page types
export interface StatusPageSectionMonitor {
  monitor_id: string;
  display_name?: string;
  position?: number;
}

export interface StatusPageSection {
  id?: string;
  title: string;
  position?: number;
  monitors?: StatusPageSectionMonitor[];
  created_at?: string;
  updated_at?: string;
}

export interface StatusPage {
  id: string;
  tenant_id: string;
  slug: string;
  public_url?: string;
  title: string;
  description?: string;
  logo_url?: string;
  primary_color?: string;
  secondary_color?: string;
  monitor_ids?: string[];
  sections?: StatusPageSection[];
  created_at: string;
  updated_at: string;
}

export interface CreateStatusPageRequest {
  slug: string;
  title: string;
  description?: string;
  logo_url?: string;
  primary_color?: string;
  secondary_color?: string;
  monitor_ids?: string[];
  monitor_display_names?: Record<string, string>;
  sections?: StatusPageSection[];
}

export interface UpdateStatusPageRequest {
  slug?: string;
  title?: string;
  description?: string;
  logo_url?: string;
  primary_color?: string;
  secondary_color?: string;
  monitor_ids?: string[];
  monitor_display_names?: Record<string, string>;
  sections?: StatusPageSection[];
}

export interface StatusPageListResponse {
  items: StatusPage[];
  page: number;
  page_size: number;
  total: number;
}

// Check Result types
export interface CheckResult {
  id: string;
  status: 'success' | 'failure' | 'error' | 'degraded';
  result_source?: 'monitor' | 'platform' | 'derived';
  http_status?: number;
  latency_ms?: number;
  error_message?: string;
  metrics_data?:
    | AgentMetrics
    | PushMetrics
    | HTTPMetricsEnvelope
    | GRPCMetricsEnvelope
    | TCPMetricsEnvelope
    | SyntheticAPIMetricsEnvelope
    | SyntheticBrowserMetricsEnvelope;
  created_at: string;
}

// Agent Metrics types
export interface AgentMetrics {
  cpu_percent: number;
  memory_used: number;
  memory_total: number;
  disk_used: number;
  disk_total: number;
  network_bytes_in: number;
  network_bytes_out: number;
  load_avg_1: number;
  load_avg_5: number;
  load_avg_15: number;
  process_count: number;
  timestamp: string;
}

// Agent Install Command types
export interface AgentInstallCommand {
  agent_id: string;
  backend_url: string;
  install_script: string;
  windows_install_script: string;
  uninstall_script: string;
  windows_uninstall_script: string;
  config_template: string;
  download_url: string;
  interval_seconds: number;
}

// Push Info types
export interface PushInfo {
  push_token: string;
  webhook_url: string;
  interval_seconds: number;
  grace_period_seconds: number;
  example_curl: string;
}

// Push Metrics - auto-detected from incoming push data
export type PushMetrics = Record<string, string | number | boolean>;

// HTTP Metrics (worker-provided)
export interface HTTPMetricsEnvelope {
  http?: HTTPMetrics;
}

export interface HTTPMetrics {
  final_url?: string;
  redirects?: number;
  timing?: HTTPTimingInfo;
  tls?: HTTPTLSInfo;
  assertions_failed?: string[];
}

export interface GRPCMetricsEnvelope {
  grpc?: GRPCMetrics;
}

export interface GRPCMetrics {
  target?: string;
  host?: string;
  port?: number;
  service?: string;
  use_tls?: boolean;
  serving_status?: string;
}

export interface TCPMetricsEnvelope {
  tcp?: TCPMetrics;
}

export interface TCPMetrics {
  host?: string;
  port?: number;
  use_tls?: boolean;
  tls_version?: string;
}

export interface HTTPTimingInfo {
  dns_ms?: number;
  connect_ms?: number;
  tls_handshake_ms?: number;
  ttfb_ms?: number;
  total_ms?: number;
}

export interface HTTPTLSInfo {
  version?: string;
  cipher_suite?: string;
  server_name?: string;
  not_before?: string;
  not_after?: string;
  days_until_expiry?: number;
  subject?: string;
  issuer?: string;
  serial_number?: string;
  dns_names?: string[];
  ip_addresses?: string[];
  verified_chains?: number;
  peer_certificates?: number;
}

export interface SyntheticAPIMetricsEnvelope {
  synthetic_api?: SyntheticAPIMetrics;
}

export interface SyntheticAPIMetrics {
  failure_mode?: string;
  completed_steps?: number;
  total_latency_ms?: number;
  failed_step_id?: string;
}

export interface SyntheticBrowserMetricsEnvelope {
  synthetic_browser?: SyntheticBrowserMetrics;
}

export interface SyntheticBrowserMetrics {
  failure_mode?: string;
  start_url?: string;
  device?: string;
  completed_steps?: number;
  total_latency_ms?: number;
  final_url?: string;
  failed_step_id?: string;
  artifacts?: SyntheticBrowserArtifactMetrics;
}

export interface SyntheticBrowserArtifactMetrics {
  screenshot_path?: string;
  trace_path?: string;
  har_path?: string;
  warnings?: string[];
}

export interface MonitorResultsResponse {
  monitor_id: string;
  results: CheckResult[];
}

export type MonitorAnalyticsRange = '1h' | '6h' | '24h' | '7d' | '30d' | '90d' | '365d';
export type AnalyticsSource = 'raw' | 'rollup';

export interface MonitorAnalyticsSummary {
  uptime_pct: number;
  sla_pct: number;
  downtime_pct: number;
  avg_latency_ms?: number;
  median_latency_ms?: number;
  p95_latency_ms?: number;
  latest_status?: string;
  latest_check_at?: string;
}

export interface MonitorAnalyticsSeriesPoint {
  bucket_start: string;
  uptime_pct: number;
  avg_latency_ms?: number;
  total_checks: number;
  has_data: boolean;
}

export interface MonitorAnalyticsDowntimePeriod {
  start_time: string;
  end_time: string;
  is_open: boolean;
}

export interface MonitorAnalyticsResponse {
  monitor_id: string;
  range: MonitorAnalyticsRange;
  generated_at: string;
  source: AnalyticsSource;
  coverage_start?: string;
  is_partial: boolean;
  summary: MonitorAnalyticsSummary;
  uptime_series: MonitorAnalyticsSeriesPoint[];
  latency_series: MonitorAnalyticsSeriesPoint[];
  downtime_periods: MonitorAnalyticsDowntimePeriod[];
}

export interface RunMonitorNowResponse {
  job_id: string;
  monitor_id: string;
  queued_at: string;
  deadline: string;
}

// Group management types
export interface AddMonitorsToGroupRequest {
  monitor_ids: string[];
}

export interface RemoveMonitorsFromGroupRequest {
  monitor_ids: string[];
}

// Import types
export type ImportFormat = 'json' | 'yaml' | 'csv';

export interface ImportRow {
  index: number;
  fields: Record<string, unknown>;
}

export interface FieldMapping {
  name?: string;
  type?: string;
  config?: string;
  url?: string;
  method?: string;
  expected_status?: string;
  expected_body?: string;
  host?: string;
  port?: string;
  service?: string;
  use_tls?: string;
  interval_seconds?: string;
  timeout_seconds?: string;
  tags?: string;
  enabled?: string;
  group_members?: string;
}

export interface ImportPreviewResponse {
  format: ImportFormat;
  schema?: string;
  rows: ImportRow[];
  detected_fields: string[];
  suggested_mapping: FieldMapping;
  warnings: string[];
  total_rows: number;
  detected_types: string[];
  suggested_type_mapping: Record<string, string>;
}

export interface ImportExecuteRequest {
  rows: ImportRow[];
  mapping: FieldMapping;
  type_mapping?: Record<string, string>;
}

export interface ImportRowResult {
  index: number;
  status: 'success' | 'failed' | 'skipped';
  monitor_id?: string;
  name: string;
  type: string;
  error?: string;
  skip_reason?: string;
}

export interface ImportExecuteResponse {
  results: ImportRowResult[];
  total_rows: number;
  success_count: number;
  failed_count: number;
  skipped_count: number;
}
