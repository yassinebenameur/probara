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
}

export interface UpdateTenantSettingsRequest {
  data_retention_days?: number;
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
  | "synthetic_browser";

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
  | GroupMonitorConfig
  | AgentMonitorConfig
  | PushMonitorConfig
  | SIPMonitorConfig
  | SyntheticAPIMonitorConfig
  | SyntheticBrowserMonitorConfig;

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
  created_at: string;
  updated_at: string;
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
}

export interface MonitorListResponse {
  items: Monitor[];
  page: number;
  page_size: number;
  total: number;
}

// Alert types
export type AlertStatus = 'active' | 'acknowledged' | 'resolved';

export interface Alert {
  id: string;
  tenant_id: string;
  monitor_id: string;
  alert_policy_id: string;
  status: AlertStatus;
  triggered_at: string;
  acknowledged_at?: string;
  resolved_at?: string;
  failure_count: number;
  last_error?: string;
  created_at: string;
  updated_at: string;
  monitor_name?: string;
  policy_name?: string;
}

export interface AlertListResponse {
  items: Alert[];
  page: number;
  page_size: number;
  total: number;
}

export type DashboardRange = '24h' | '7d' | '30d' | '90d' | '365d';
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
  latest_status: string | null;
  latest_check_at: string | null;
}

export interface DashboardOpsSummary {
  up_monitors: number;
  down_monitors: number;
  paused_monitors: number;
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
  stats: DashboardStats;
  trend: DashboardTrendPoint[];
  activity_24h: DashboardActivityPoint[];
  ops_summary: DashboardOpsSummary;
  monitor_health: DashboardMonitorHealth[];
  problem_monitors: DashboardProblemMonitor[];
  recent_failures: DashboardFailureEvent[];
  recent_alerts: Alert[];
}

// Alert Policy types
export interface AlertPolicy {
  id: string;
  tenant_id: string;
  name: string;
  description?: string;
  failure_threshold: number;
  failure_window_seconds: number;
  channel_ids?: string[];
  email_subject_template?: string;
  email_body_template?: string;
  created_at: string;
  updated_at: string;
}

export interface CreateAlertPolicyRequest {
  name: string;
  description?: string;
  failure_threshold: number;
  failure_window_seconds: number;
  channel_ids?: string[];
  email_subject_template?: string;
  email_body_template?: string;
}

export interface UpdateAlertPolicyRequest {
  name?: string;
  description?: string;
  failure_threshold?: number;
  failure_window_seconds?: number;
  channel_ids?: string[];
  email_subject_template?: string;
  email_body_template?: string;
}

export interface AlertPolicyListResponse {
  items: AlertPolicy[];
  page: number;
  page_size: number;
  total: number;
}

// Alert Channel types
export type AlertChannelType = 'teams' | 'email';

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

// Status Page types
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
}

export interface UpdateStatusPageRequest {
  slug?: string;
  title?: string;
  description?: string;
  logo_url?: string;
  primary_color?: string;
  secondary_color?: string;
  monitor_ids?: string[];
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
