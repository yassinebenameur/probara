import type {
  GRPCMetricsEnvelope,
  HTTPMetricsEnvelope,
  PushMetrics,
  SIPMetricsEnvelope,
  SyntheticAPIMetricsEnvelope,
  SyntheticBrowserMetricsEnvelope,
  TCPMetricsEnvelope,
  WebSocketMetricsEnvelope,
} from './check-metrics';

// Check Result types
export interface CheckResult {
  id: string;
  status: 'success' | 'failure' | 'error' | 'degraded';
  result_source?: 'monitor' | 'platform' | 'derived';
  http_status?: number;
  latency_ms?: number;
  error_message?: string;
  metrics_data?:
    | PushMetrics
    | HTTPMetricsEnvelope
    | GRPCMetricsEnvelope
    | TCPMetricsEnvelope
    | WebSocketMetricsEnvelope
    | SIPMetricsEnvelope
    | SyntheticAPIMetricsEnvelope
    | SyntheticBrowserMetricsEnvelope;
  location_id?: string; // Absent = default fleet
  location_name?: string;
  created_at: string;
}

export interface MonitorResultsResponse {
  monitor_id: string;
  results: CheckResult[];
}

/** GET /monitors/results/batch: per monitor, what GET /monitors/{id}/results
 * returns for it; per group, its direct member ids. Ids that are not live
 * monitors of the tenant are absent from both maps. */
export interface MonitorResultsBatchResponse {
  limit: number;
  results: Record<string, CheckResult[]>;
  members: Record<string, string[]>;
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

// Per-check metrics_data snapshots over a range, downsampled server-side to
// the newest snapshot per bucket_seconds bucket (raw retention clamps long
// ranges: is_partial).
export interface MetricsSnapshot {
  created_at: string;
  metrics_data: unknown;
}

export interface MonitorMetricsSnapshotsResponse {
  monitor_id: string;
  range: MonitorAnalyticsRange;
  bucket_seconds: number;
  coverage_start: string;
  is_partial: boolean;
  snapshots: MetricsSnapshot[];
}

export interface RunMonitorNowResponse {
  job_id: string;
  monitor_id: string;
  queued_at: string;
  deadline: string;
}
