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
