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
  // Caller's membership role in this tenant ("admin" for superadmins);
  // present on list responses.
  role?: TenantRole;
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

// RBAC types
export type TenantRole = "admin" | "editor" | "viewer";
export type PlatformRole = "superadmin" | "member";
export type ApiKeyScope = "read" | "write";

export interface TenantMembership {
  tenant_id: string;
  tenant_name?: string;
  role: TenantRole;
}

// Effective identity of the current credential (cookie or API key),
// from GET /v1/auth-context.
export interface AuthContext {
  actor_type: "admin_user" | "api_key" | "";
  admin_id?: string;
  api_key_id?: string;
  tenant_id?: string;
  platform_role?: PlatformRole;
  role?: TenantRole;
  scope?: ApiKeyScope;
  can_write: boolean;
}

export interface OidcStatus {
  enabled: boolean;
  label?: string;
}

// OIDC group→role mapping. No tenant_id = platform mapping (superadmin).
// Any existing mappings make the IdP the source of truth: SSO users' roles
// are re-derived from their groups on every login.
export interface OidcGroupMapping {
  id: string;
  group_name: string;
  // Operator-facing display name; matching always uses group_name.
  label?: string;
  tenant_id?: string;
  tenant_name?: string;
  role: PlatformRole | TenantRole;
  created_at: string;
  updated_at: string;
}

// IdP group observed in a verified ID token at a past SSO login — the
// mapping editor's suggestion source (OIDC has no group-enumeration API).
export interface OidcSeenGroup {
  group_name: string;
  first_seen_at: string;
  last_seen_at: string;
}

export interface OidcGroupMappingListResponse {
  mappings: OidcGroupMapping[];
  seen_groups: OidcSeenGroup[];
  groups_claim: string;
  groups_scope_requested: boolean;
}

export interface CreateOidcGroupMappingRequest {
  group_name: string;
  label?: string;
  tenant_id?: string;
  role: string;
}

// Admin user types
export interface AdminUser {
  id: string;
  username: string;
  email?: string;
  platform_role?: PlatformRole;
  auth_method?: "password" | "oidc";
  memberships?: TenantMembership[];
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

export interface MembershipInput {
  tenant_id: string;
  role: TenantRole;
}

export interface CreateAdminUserRequest {
  username: string;
  email?: string;
  // Omitted = SSO-only user (requires email for IdP linking).
  password?: string;
  platform_role?: PlatformRole;
  memberships?: MembershipInput[];
}

export interface UpdateAdminUserRequest {
  username?: string;
  email?: string;
  password?: string;
  platform_role?: PlatformRole;
  memberships?: MembershipInput[];
}

// API Key types
export interface ApiKey {
  id: string;
  name: string;
  key_prefix: string;
  key?: string;
  scope: ApiKeyScope;
  expires_at?: string;
  last_used_at?: string;
  created_by?: string;
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
  scope?: ApiKeyScope;
  expires_at?: string;
}

// Audit log types
export type AuditOutcome = "success" | "failure" | "denied";

export interface AuditEvent {
  id: number;
  occurred_at: string;
  tenant_id?: string;
  actor_type: "admin_user" | "api_key" | "anonymous";
  actor_id?: string;
  actor_label: string;
  action: string;
  resource_type: string;
  resource_id: string;
  outcome: AuditOutcome;
  status_code?: number;
  ip: string;
  user_agent: string;
  details?: Record<string, unknown>;
}

export interface AuditListResponse {
  items: AuditEvent[];
  page: number;
  page_size: number;
  total: number;
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
  | "tcp"
  | "mysql"
  | "websocket";

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
  // Query a specific DNS server (host or host:port, default port 53) instead
  // of the worker's system resolver — for private zones and VPC resolvers.
  nameserver?: string;
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

// Generic metric alert rule evaluated by the alerter against the metric
// store. Thresholds are in the metric's NATIVE unit: *.utilization metrics
// are ratios 0-1 (the UI shows percent and converts on submit/display).
export interface MetricRule {
  metric_name: string;
  attribute_filters?: Record<string, string>;
  operator: '>=' | '<=';
  threshold: number;
  for_duration_seconds?: number; // 0/absent = instant
}

export interface AgentMonitorConfig {
  agent_id: string;
  expected_interval_seconds: number;
  metric_rules?: MetricRule[];
}

export interface PushMonitorConfig {
  push_token: string;
  expected_interval_seconds: number;
  grace_period_seconds: number;
}

export interface SIPMonitorConfig {
  host: string;
  port: number;
  transport: "udp" | "tcp" | "tls";
  method?: "options" | "register";
  username?: string;
  password?: string; // write-only; API reads return the masked placeholder
  domain?: string;
  expected_status?: number;
  tls_skip_verify?: boolean;
  tls_server_name?: string;
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
  // Cluster checks (read-only admin commands; need MongoDB's built-in
  // clusterMonitor role). Missing privileges never fail the check unless a
  // hard threshold below depends on the data.
  collect_replication?: boolean; // replSetGetStatus
  collect_connections?: boolean; // serverStatus.connections
  collect_cache?: boolean; // serverStatus.wiredTiger.cache
  collect_memory?: boolean; // serverStatus.mem
  collect_network?: boolean; // serverStatus.network + opcounters
  collect_cpu?: boolean; // serverStatus.extra_info process CPU time (Linux)
  // Max fails the check (and fails closed when lag can't be evaluated);
  // warn only annotates. Both require collect_replication.
  max_replication_lag_seconds?: number;
  warn_replication_lag_seconds?: number;
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

export interface MySQLMonitorConfig extends DBTLSMaterial {
  connection_string?: string; // secret, write-only
  host?: string;
  port?: number;
  database?: string;
  username?: string;
  password?: string; // secret, write-only
  tls_enabled?: boolean;
  tls_skip_verify?: boolean;
  query?: string;
  query_value_op?: DBQueryValueOp;
  query_value?: string;
  max_latency_ms?: number;
  warn_latency_ms?: number;
}

export interface WebSocketMonitorConfig {
  url: string;
  headers?: Record<string, string>;
  tls_skip_verify?: boolean;
  send_message?: string;
  expected_substring?: string;
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

export interface MongoDBReplicationMember {
  name: string;
  state: string; // PRIMARY/SECONDARY/ARBITER/...
  health: boolean;
  lag_seconds?: number;
}

export interface MongoDBReplicationMetrics {
  set?: string;
  primary?: string; // "" / absent = the set has no primary
  members_total: number;
  members_healthy: number;
  max_lag_seconds?: number; // absent = no primary or no secondaries
  members?: MongoDBReplicationMember[];
}

export interface MongoDBUnavailableCheck {
  check: "server_status" | "repl_set_status";
  reason: "unauthorized" | "not_replica_set" | "error";
  message?: string;
}

// MongoDB cluster-check extras (clusterMonitor role), flat siblings of the
// shared DBMetrics fields under metrics_data.mongodb.
export interface MongoDBMetrics extends DBMetrics {
  uptime_seconds?: number;
  connections_available?: number;
  mem_virtual_bytes?: number;
  cache_used_bytes?: number;
  cache_max_bytes?: number;
  cache_dirty_bytes?: number;
  network_bytes_in?: number; // cumulative since restart
  network_bytes_out?: number; // cumulative since restart
  network_requests?: number;
  opcounters?: Record<string, number>;
  cpu_user_us?: number; // mongod process CPU time, cumulative µs (Linux)
  cpu_system_us?: number;
  replication?: MongoDBReplicationMetrics;
  replication_lag_warn_seconds?: number;
  unavailable?: MongoDBUnavailableCheck[];
}

export type DBMetricsEnvelope = Partial<Record<"redis" | "postgres" | "rabbitmq" | "mysql", DBMetrics>> & {
  mongodb?: MongoDBMetrics;
};

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
  | RabbitMQMonitorConfig
  | MySQLMonitorConfig
  | WebSocketMonitorConfig;

export type NotificationMode = 'default' | 'custom';

// How a group handles alerts when its members go down:
//   'per_monitor' — each member alerts individually; the group emits no alert.
//   'group'       — members are suppressed; one group-level alert speaks for them.
export type MemberAlertRollup = 'per_monitor' | 'group';

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

export type AlertKind = 'availability' | 'latency_anomaly' | 'host_metric' | 'mesh_edge' | 'tls_expiry';

export type MonitorState = 'unknown' | 'up' | 'suspect' | 'down' | 'degraded';

// Private location types
export interface Location {
  id: string;
  tenant_id: string;
  name: string;
  slug: string;
  description?: string;
  enabled: boolean;
  connected: boolean;
  last_seen_at?: string;
  // host:port other locations probe; set = participates in the mesh.
  mesh_endpoint?: string;
  monitor_count: number;
  created_at: string;
  updated_at: string;
}

export interface LocationListResponse {
  items: Location[];
  page: number;
  page_size: number;
  total: number;
}

export interface CreateLocationRequest {
  name: string;
  description?: string;
  mesh_endpoint?: string;
}

export interface UpdateLocationRequest {
  name?: string;
  description?: string;
  enabled?: boolean;
  // Empty string clears the endpoint (opts the location out of the mesh).
  mesh_endpoint?: string;
}

// Inter-location connectivity mesh

export interface MeshLocation {
  id: string;
  name: string;
  connected: boolean;
  mesh_endpoint: string;
}

export type MeshEdgeState = 'unknown' | 'up' | 'suspect' | 'down';

export interface MeshEdge {
  source_location_id: string;
  target_location_id: string;
  state: MeshEdgeState;
  last_latency_ms?: number;
  last_check_at?: string;
  last_error?: string;
  stale: boolean;
  alert_open: boolean;
}

export interface MeshResponse {
  locations: MeshLocation[];
  edges: MeshEdge[];
  probe_interval_seconds: number;
}

export interface MeshEdgeHistoryPoint {
  status: string;
  latency_ms?: number;
  created_at: string;
}

export interface MeshProbeResponse {
  status: string;
  latency_ms?: number;
  error_message?: string;
}

export interface LocationDeployInfo {
  location_id: string;
  nats_url: string;
  docker_run_command: string;
  docker_compose_yaml: string;
  env: Record<string, string>;
}

export interface MonitorLocationStatus {
  id: string;
  name: string;
  connected: boolean;
  current_state: 'unknown' | 'up' | 'suspect' | 'down';
  last_latency_ms?: number;
  last_check_at?: string;
}

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
  location_ids?: string[]; // Private locations; empty/absent = default fleet
  location_quorum?: number; // Locations that must fail before the monitor is down
  locations?: MonitorLocationStatus[]; // Embedded only on GET /v1/monitors/{id}
  created_at: string;
  updated_at: string;
  consecutive_failures_threshold: number;
  notification_mode: NotificationMode;
  member_alert_rollup?: MemberAlertRollup;
  notification_channels?: ChannelAssignment[];
  current_state?: MonitorState;
  alert_routing?: AlertRouting; // Read-only; who this monitor's alerts reach
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

/**
 * Where a monitor's alerts actually go. Computed per request from
 * shared/alertrouting — the same rules the alerter dispatches by — so
 * `reachable: false` means an alert here would notify nobody.
 */
export interface AlertRouting {
  reachable: boolean;
  /**
   * `custom` / `tenant_default`: the monitor's own routing applies.
   * `group_rollup`: a group rolls its alerts up, so the group's routing decides.
   * `members`: it is a group that never alerts itself; members alert individually.
   */
  source: 'custom' | 'tenant_default' | 'group_rollup' | 'members';
  active_channels: number;
  /** Includes disabled channels, which never deliver. */
  assigned_channels: number;
  reason?:
    | 'no_custom_channels'
    | 'custom_channels_disabled'
    | 'no_tenant_default_channels'
    | 'tenant_default_channels_disabled'
    | 'group_rollup_unrouted'
    | 'group_rollup_paused';
  rollup_group_id?: string;
  rollup_group_name?: string;
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
  member_alert_rollup?: MemberAlertRollup;
  notification_channels?: ChannelAssignment[];
  depends_on_ids?: string[];
  location_ids?: string[];
  location_quorum?: number;
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
  member_alert_rollup?: MemberAlertRollup;
  notification_channels?: ChannelAssignment[];
  depends_on_ids?: string[];
  location_ids?: string[];
  location_quorum?: number;
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
  // Absent for mesh_edge alerts (their subject is a location pair).
  monitor_id?: string;
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
  metric_name?: string;
  metric_value?: number;
  threshold_value?: number;
  root_cause_monitor_id?: string;
  root_cause_down_since?: string;
  // Mesh-edge subject (kind === 'mesh_edge').
  source_location_id?: string;
  target_location_id?: string;
  created_at: string;
  updated_at: string;
  monitor_name?: string;
  policy_name?: string;
  root_cause_monitor_name?: string;
  source_location_name?: string;
  target_location_name?: string;
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
  /** null = no checks in the window; render as no-data, never 0% (S-D1). */
  overall_uptime: number | null;
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
  /** Active monitors whose alerts would notify nobody. */
  unrouted_monitors: number;
}

export interface DashboardProblemMonitor {
  monitor_id: string;
  monitor_name: string;
  current_status: string | null;
  failure_count: number;
  error_count: number;
  uptime: number;
  latest_failure_at: string | null;
  /** error_message of the most recent failing check in range; absent when the
   *  check had no message or raw results were already rolled up. */
  latest_error_message?: string | null;
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
  /** null = no checks in the window (paused, new); render "—", never 100%. */
  uptime: number | null;
  current_status: string | null;
}

export interface DashboardGroup {
  tag: string | null;
  monitor_count: number;
  /** Mean over members with data; null when no member has any. */
  uptime: number | null;
  attention_count: number;
  worst_member: DashboardGroupMember | null;
  members: DashboardGroupMember[];
}

export interface DashboardGroupSparklineResponse {
  tag: string | null;
  range: DashboardRange;
  /** null buckets = no checks landed in them; render gaps. */
  buckets: (number | null)[];
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

export interface StatusPageSettings {
  show_monitor_tags?: boolean;
  show_monitor_url?: boolean;
  show_monitor_uptime?: boolean;
  show_monitor_tls?: boolean;
  show_latency_charts?: boolean;
  show_agent_metrics?: boolean;
  show_global_uptime?: boolean;
  show_footer?: boolean;
  footer_text?: string;
  default_theme?: string;
  allow_theme_toggle?: boolean;
  /**
   * Offer visitors a browser-notification opt-in on the public page. Off by
   * default, and inert unless the deployment configures a VAPID keypair
   * (STATUS_PAGE_VAPID_PUBLIC_KEY / _PRIVATE_KEY).
   */
  enable_push_notifications?: boolean;
  custom_css?: string;
  custom_head_html?: string;
  custom_footer_html?: string;
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
  settings?: StatusPageSettings;
  created_at: string;
  updated_at: string;
}

export interface StatusPageTemplateVersion {
  version?: number;
  status: 'draft' | 'published' | 'archived';
  size_bytes: number;
  created_at: string;
  updated_at: string;
  published_at?: string;
}

export interface StatusPageTemplateState {
  has_custom: boolean;
  published_version?: number;
  draft_source?: string;
  draft_updated_at?: string;
  versions: StatusPageTemplateVersion[];
  preview_token?: string;
  max_size_bytes: number;
}

export interface StatusPageLibraryTemplate {
  id: string;
  name: string;
  description?: string;
  source?: string; // omitted in list responses
  size_bytes: number;
  created_at: string;
  updated_at: string;
}

export interface StatusPageLibraryTemplateListResponse {
  items: StatusPageLibraryTemplate[];
  total: number;
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
  settings?: StatusPageSettings;
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
  settings?: StatusPageSettings;
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
    | WebSocketMetricsEnvelope
    | SyntheticAPIMetricsEnvelope
    | SyntheticBrowserMetricsEnvelope;
  location_id?: string; // Absent = default fleet
  location_name?: string;
  created_at: string;
}

// Legacy agent metrics blob (pre-OTel host agent). New agent monitors emit
// heartbeat check results without metrics_data; historical results may still
// carry this shape, so the list-page mini view keeps rendering it.
export interface AgentDiskMount {
  path: string;
  used: number;
  total: number;
  fstype: string;
}

export interface AgentMetrics {
  cpu_percent: number;
  cpu_cores?: number;
  memory_used: number;
  memory_total: number;
  swap_used?: number;
  swap_total?: number;
  disk_used: number;
  disk_total: number;
  disk_mounts?: AgentDiskMount[];
  disk_read_bytes?: number;
  disk_write_bytes?: number;
  network_bytes_in: number;
  network_bytes_out: number;
  load_avg_1: number;
  load_avg_5: number;
  load_avg_15: number;
  process_count: number;
  uptime_seconds?: number;
  timestamp: string;
}

// Agent Install Command types (OpenTelemetry Collector distribution)
export interface AgentInstallCommand {
  agent_id: string;
  backend_url: string;
  install_script: string;
  windows_install_script: string;
  uninstall_script: string;
  windows_uninstall_script: string;
  // Generated collector YAML (linux variant); per-platform variants come from
  // GET /v1/monitors/{id}/agent/config.yaml?platform=linux|darwin|windows.
  collector_config: string;
  collector_version: string;
  download_url: string;
  interval_seconds: number;
}

// Generic metric store types (agent monitors push OTLP metrics)

export type MetricType = 'gauge' | 'counter';

export interface MetricSeriesInfo {
  series_key: string;
  metric_name: string;
  attributes: Record<string, string>;
  unit: string;
  metric_type: MetricType;
  last_seen_at: string;
}

export interface MetricSeriesListResponse {
  items: MetricSeriesInfo[];
}

export type MetricAgg = 'avg' | 'min' | 'max' | 'sum' | 'last';

export interface MetricQuerySpec {
  ref: string;
  metric_name: string;
  // Subset match; omitted = fan-out to every matching series.
  attribute_filters?: Record<string, string>;
  agg: MetricAgg;
  // Per-second rate; only valid for counter series (server 422s otherwise).
  rate?: boolean;
}

export interface MetricQueryRequest {
  start: string;
  end: string;
  step_seconds: number; // 10..86400
  queries: MetricQuerySpec[]; // max 12
}

export interface MetricSeriesData {
  series_key: string;
  metric_name: string;
  attributes: Record<string, string>;
  unit: string;
  metric_type: MetricType;
  // [epoch_ms, value]; buckets with no samples are omitted.
  points: [number, number][];
}

export interface MetricQueryRefResult {
  ref: string;
  step_seconds: number;
  source: 'raw' | 'rollup';
  truncated: boolean;
  series: MetricSeriesData[];
}

export interface MetricQueryResponse {
  results: MetricQueryRefResult[];
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

export interface WebSocketMetricsEnvelope {
  websocket?: WebSocketMetrics;
}

export interface WebSocketMetrics {
  subprotocol?: string;
  tls_version?: string;
  latency_warn_ms?: number;
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
  /** false = no checks in the window; the percentages are meaningless zeros. */
  has_data: boolean;
  /** "interval" = time-based availability from the state timeline; "sampled" = legacy count-based. */
  method: 'interval' | 'sampled';
  availability_pct: number;
  /** Observed share of the window; only present for method "interval". */
  coverage_pct?: number;
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
  /** Per-monitor translation caveats, set by format adapters (e.g. Uptime Kuma). */
  warnings?: string[];
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
  alert_policy_names?: string;
  consecutive_failures_threshold?: string;
}

/** A source record a format adapter refused to translate. Informational only. */
export interface ImportSkippedRow {
  name: string;
  source_type: string;
  reason: string;
}

/** Recognized source schemas. Anything else goes through field mapping. */
export const IMPORT_SCHEMA_PORTABLE = 'portable_monitor_export';
export const IMPORT_SCHEMA_UPTIME_KUMA = 'uptime_kuma_export';

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
  skipped_rows?: ImportSkippedRow[];
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
