import type { Alert } from './alerts';
import type { MonitorState } from './monitors';

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
  /** Open alerts whose notifications are suppressed because an upstream dependency explains them (subset of active + acknowledged). */
  suppressed_alerts: number;
  /** Active monitors whose alerts would notify nobody. */
  unrouted_monitors: number;
}

export interface DashboardProblemMonitor {
  monitor_id: string;
  monitor_name: string;
  /** Latest check outcome as the API maps it from the state; degraded reads "success". */
  current_status: string | null;
  /** The persisted state machine value (monitors.current_state): the monitor's real state. */
  current_state?: MonitorState;
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
  /** Members down or degraded NOW (current state), not failures earlier in the range. */
  attention_count: number;
  /** First of members: down, then degraded, then lowest uptime; null when nothing needs attention. */
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
