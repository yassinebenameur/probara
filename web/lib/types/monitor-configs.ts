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
  | "websocket"
  | "prometheus";

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

/** A value pulled from the JSON response and shown on the monitors list. */
export interface HTTPDisplayField {
  label?: string;
  /** gjson path */
  path: string;
}

export const MAX_HTTP_DISPLAY_FIELDS = 5;

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
  display_fields?: HTTPDisplayField[];
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

export interface PrometheusMonitorConfig {
  url: string;
  query: string;
  operator: 'gt' | 'gte' | 'lt' | 'lte' | 'eq' | 'ne';
  threshold: number;
  no_data_status?: 'success' | 'failure' | 'error';
  auth_type?: 'none' | 'basic' | 'bearer';
  username?: string;
  password?: string;
  bearer_token?: string;
}

export interface PrometheusMetrics {
  values: number[];
  sample_count: number;
  failed_count: number;
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
  // 0: 5060 (5061 over TLS), or from SRV under resolution 'rfc3263'.
  port: number;
  // '' is UDP, or left to NAPTR/SRV under resolution 'rfc3263'.
  transport: "" | "udp" | "tcp" | "tls";
  // 'rfc3263' locates the server through NAPTR (transport left to DNS) and
  // SRV with failover; absent or 'a' dials the host's A/AAAA records.
  resolution?: "a" | "rfc3263";
  // DNS server for rfc3263 lookups (host or host:port); rfc3263 only.
  nameserver?: string;
  // "invite" places a real test call (call is then required).
  method?: "options" | "register" | "invite";
  username?: string;
  password?: string; // write-only; API reads return the masked placeholder
  domain?: string;
  expected_status?: number;
  tls_skip_verify?: boolean;
  tls_server_name?: string;
  // Open a certificate-expiry alert when fewer days remain (transport tls only).
  tls_min_days_valid?: number;
  // The test call; set if and only if method is "invite" (Go shared/models
  // sip_call.go SIPCallConfig).
  call?: SIPCallConfig;
}

// A SIP test call. Exactly one of number / target_uri. Every check places a
// real, possibly billed, call, so only tenant admins may create, change, run
// or test one, and the schedule is bounded (interval >= 60 s, timeout <= 20 s).
export interface SIPCallConfig {
  // Called user part: sip:<number>@<domain or host> (;user=phone for digits).
  number?: string;
  // Full sip:/sips: Request-URI; the worker still sends to the monitor's host.
  target_uri?: string;
  from_user?: string;
  from_display?: string;
  from_domain?: string;
  // "answer" (default): 200, hold, BYE. "ring": success on the first 18x, then CANCEL.
  mode?: "answer" | "ring";
  // Answer mode only, 0-10000.
  hold_ms?: number;
  warn_pdd_ms?: number;
  max_pdd_ms?: number;
  // Answer mode only.
  warn_answer_ms?: number;
  max_answer_ms?: number;
  // NAT override for the SDP offer's o=/c= address.
  sdp_address?: string;
  // Adds X-Probara-Probe: 1.
  probe_header?: boolean;
  // Measures the hold's audio: answer mode, hold_ms >= 3000 (6000 for
  // two_way), expected_status unset or 2xx (Go shared/models sip_media.go).
  media?: SIPMediaConfig;
}

// What a test call's audio must prove: none measures and reports only, audio
// needs the far end's audio to reach the probe, two_way that and the far
// end's word that the probe's arrived (an echo of it or an RTCP report about
// it), echo the probe's own audio back.
export type SIPMediaExpect = 'none' | 'audio' | 'two_way' | 'echo';

// call.media. Keys are strict: the API and the worker refuse any key that is
// not one of these. Thresholds apply to every measured basis and are refused
// with expect none; warn adds a warning, max and min fail the check.
export interface SIPMediaConfig {
  expect: SIPMediaExpect;
  // Effective loss in percent, above 0 and below 100.
  warn_loss_pct?: number;
  max_loss_pct?: number;
  // Mean RFC 3550 jitter, 1-1000.
  warn_jitter_ms?: number;
  max_jitter_ms?: number;
  // The MOS-CQE estimate, 1-4.3; lower is worse, so warn sits above min.
  warn_mos?: number;
  min_mos?: number;
  // The echo's median round trip, else RTCP's, 1-5000.
  warn_rtt_ms?: number;
  max_rtt_ms?: number;
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
  | WebSocketMonitorConfig
  | PrometheusMonitorConfig;
