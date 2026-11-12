import type { SIPMediaExpect } from './monitor-configs';

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
  tls?: TLSCertInfo;
  assertions_failed?: string[];
  display_values?: HTTPDisplayValue[];
}

export interface HTTPDisplayValue {
  label?: string;
  path: string;
  value: string;
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

// SIP metrics (worker sip_metrics.go). Written on success, failure and error
// once the check got as far as resolving its host; timing anchors are
// documented on the Go struct.
export interface SIPMetricsEnvelope {
  sip?: SIPMetrics;
}

export interface SIPMetrics {
  method: string;
  // Absent only when rfc3263 left the transport to DNS and DNS failed.
  transport?: string;
  request_uri?: string;
  call_id?: string;
  status_code?: number;
  reason?: string;
  server?: string;
  warning?: string;
  reason_header?: SIPReasonHeader;
  target?: SIPTargetMetrics;
  resolution?: SIPResolutionMetrics;
  timing: SIPTimingInfo;
  retransmissions: number;
  stray_messages: number;
  auth?: SIPAuthMetrics;
  tls?: TLSCertInfo;
  // INVITE test calls only.
  call?: SIPCallMetrics;
  // The warn threshold a successful call exceeded; warnings says which.
  latency_warn_ms?: number;
  // Worth an operator's attention without changing the status: a warn
  // threshold, an unconfirmed teardown, a ring-mode call that was answered.
  warnings?: string[];
  assertions_failed?: string[];
}

// metrics_data.sip.call (worker sip_metrics.go sipCallMetrics). pdd_ms and
// answer_ms run from the INVITE that drew them (the authenticated one when
// challenged); setup_pdd_ms and setup_ms from the first INVITE.
export interface SIPCallMetrics {
  mode: 'answer' | 'ring';
  trying_ms?: number;
  pdd_ms?: number;
  // The response that ended post-dial delay: "180", "183", or "200" when no 18x came.
  pdd_source?: string;
  setup_pdd_ms?: number;
  answer_ms?: number;
  setup_ms?: number;
  hold_ms: number;
  bye_ms?: number;
  provisional?: { code: number; at_ms: number }[];
  early_media: boolean;
  answered: boolean;
  forked_answers: number;
  route_hops: number;
  require_100rel: boolean;
  answered_by?: { source?: string; contact_host?: string; record_route?: string[] };
  sdp_answer?: { codec?: string; payload_type?: number; remote_media?: string; direction?: string };
  rtp_rx_packets: number;
  // With call.media: the hold's audio, measured.
  media?: SIPMediaMetrics;
  // bye_ok, bye_481, bye_<code>, bye_unconfirmed, cancelled,
  // cancel_unconfirmed, answered_after_cancel, remote_bye, no_provisional, none.
  teardown: string;
  teardown_status?: number;
}

// metrics_data.sip.call.media (worker sip_media.go sipMediaMetrics, with the
// statistics of worker/internal/rtp). Quality figures appear for a basis only
// when its window was long enough to judge; floats are rounded and finite.
export interface SIPMediaMetrics {
  expect: SIPMediaExpect;
  // truncated: under 2 s measured; interrupted: a worker shutdown mid-hold;
  // not_held: the call never reached its hold; refused: the answer's address
  // was refused and nothing latched.
  status: 'measured' | 'truncated' | 'interrupted' | 'not_held' | 'unusable_answer' | 'refused';
  // The reason code of an unusable or refused answer (inactive, recvonly,
  // private_behind_public, ...).
  detail?: string;
  // Which SDP the plan used: the 200's, or the dialog's last 18x.
  answer_source?: '2xx' | '18x';
  codec?: string;
  tx_payload_type?: number;
  ptime_ms?: number;
  direction?: string;
  destination?: string;
  rtcp_destination?: string;
  // port: the SDP address's IP on another port; peer: the signalling peer.
  latched?: '' | 'port' | 'peer';
  window_ms: number;
  warmup_ms: number;
  tx?: {
    packets: number;
    rtcp: number;
    skipped_slots: number;
    timer_p95_ms: number;
    sched_p99_ms?: number;
    // The worker's own timing was trustworthy; without it no quality
    // threshold applied.
    reliable: boolean;
    errors?: number;
    error_class?: string;
  };
  rx?: {
    packets: number;
    invalid: number;
    foreign: number;
    overflow: number;
    unknown_pt: number;
    sources?: string[];
    after_bye_packets: number;
    first_packet_ms?: number;
    rtcp?: number;
    rtcp_ignored?: number;
  };
  // The probe's receiver report on the far end's stream.
  inbound?: {
    ssrc: string;
    ssrc_changes: number;
    ssrc_overflow?: number;
    expected: number;
    lost: number;
    loss_pct: number;
    cumulative_lost: number;
    duplicates: number;
    reordered: number;
    late: number;
    // Network loss plus the reference jitter buffer's late discards: the
    // figure the loss thresholds judge.
    effective_loss_pct: number;
    burst_r: number;
    jitter_ms?: number;
    jitter_max_ms?: number;
    jitter_last_ms?: number;
    max_gap_ms: number;
    stall_ms: number;
    comfort_noise: number;
    telephone_events: number;
    pt_mismatch?: number;
    quality?: SIPMediaQuality;
  };
  // The probe's own audio echoed back (an echo test line).
  round_trip?: {
    matched_by: 'ssrc' | 'marker';
    expected: number;
    matched: number;
    loss_pct: number;
    duplicates: number;
    late: number;
    effective_loss_pct: number;
    burst_r: number;
    jitter_ms?: number;
    rtt_ms?: { p50: number; p95: number; max: number };
    quality?: SIPMediaQuality;
  };
  // The far end's RTCP reports on the probe's stream.
  outbound?: {
    reports: number;
    expected: number;
    // The probe's packets the last report should have reached but did not
    // (a far end that stopped hearing it); counted in expected and as lost.
    unreported?: number;
    loss_pct: number;
    cumulative_lost: number;
    jitter_ms: number;
    rtt_ms?: number;
    quality?: SIPMediaQuality & { burst_r: number; burst_r_source: string; discards: string };
  };
  model?: string;
  assumptions?: string[];
  two_way?: 'verified' | 'inbound_only' | 'outbound_only' | 'none';
  two_way_evidence?: ('rx' | 'echo' | 'rtcp')[];
}

// One evidence basis's E-model estimate. delay_source unmeasured: no network
// delay was counted, so the MOS is an upper bound.
export interface SIPMediaQuality {
  r_factor: number;
  mos_cqe: number;
  delay_ms: number;
  delay_source: 'echo' | 'rtcp' | 'unmeasured';
}

export interface SIPReasonHeader {
  protocol: string;
  cause?: number;
  text?: string;
}

export interface SIPTargetMetrics {
  host: string;
  address?: string;
  // naptr: an SRV target whose SRV name a NAPTR record gave.
  resolved_via: 'literal' | 'a' | 'srv' | 'naptr';
  // An earlier address was tried first and given up on.
  failover?: boolean;
}

export interface SIPResolutionMetrics {
  mode: 'a' | 'rfc3263';
  // rfc3263 only. unavailable: no nameserver answered, SRV went ahead
  // without it; skipped: an IP host, or a port or transport was set.
  naptr_status?: 'ok' | 'none' | 'unavailable' | 'skipped';
  // The usable NAPTR records, in order and preference.
  naptr?: { order: number; pref: number; service: string; replacement: string }[];
  srv?: { target: string; port: number; priority: number; weight: number }[];
  // Every address tried, in order: outcome is ok, 503 or the error class.
  attempts?: { address: string; transport: string; outcome: string; ms: number }[];
}

export interface SIPTimingInfo {
  dns_ms?: number;
  connect_ms?: number;
  tls_handshake_ms?: number;
  first_response_ms?: number;
  response_ms?: number;
  total_ms: number;
}

export interface SIPAuthMetrics {
  challenge: number;
  algorithm?: string;
  rounds: number;
  auth_ms?: number;
}

export interface HTTPTimingInfo {
  dns_ms?: number;
  connect_ms?: number;
  tls_handshake_ms?: number;
  ttfb_ms?: number;
  total_ms?: number;
}

// A check's certificate snapshot (Go shared/tlscert Info): http.tls for
// HTTPS checks, sip.tls for SIP over TLS.
export interface TLSCertInfo {
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
