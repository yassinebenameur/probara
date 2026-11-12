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
