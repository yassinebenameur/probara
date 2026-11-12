import type { AILabelContext } from './ai';
import type { AlertKind } from './monitors';

// Alert types
export type AlertStatus = 'active' | 'acknowledged' | 'resolved';

export interface Alert {
  id: string;
  tenant_id: string;
  // Absent for mesh_edge alerts (their subject is a location pair).
  monitor_id?: string;
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
  root_cause_monitor_name?: string;
  source_location_name?: string;
  target_location_name?: string;
  // Set while the alerter deliberately sends no notification for this open
  // alert. 'dependency': an upstream dependency is down (or recovered less
  // than the grace period ago) and dependency suppression is on.
  suppression_reason?: 'dependency';
  // How many open downstream alerts this alert's monitor is the suppressed
  // root cause of (its notification lists them under "also affecting").
  impacted_count?: number;
  // Automatic triage (read-only; never changes paging). Absent from REST responses when the alert
  // has no triage row; always absent (= unknown) on live stream events, which
  // are built from core NATS, so merge those with mergeAlertUpdate.
  triage?: AlertTriage;
}

// --- Automatic alert triage (never delays, routes or suppresses a page) ---

export type AlertTriageStatus = 'pending' | 'ready' | 'failed' | 'skipped';

export type AlertTriageSkipReason = 'resolved' | 'cooldown' | 'not_modeled' | 'disabled';

export interface AlertTriage {
  id: string;
  status: AlertTriageStatus;
  /** Catalogue rca key; ready only. */
  hypothesis: string | null;
  /** Catalogue title of the hypothesis; ready only. */
  title: string | null;
  confidence: 'low' | 'medium' | 'high' | null;
  summary: string | null;
  skip_reason: AlertTriageSkipReason | null;
  /** Fixed operator strings; failed only. */
  error_message: string | null;
  created_at: string;
  completed_at: string | null;
  labelled: boolean;
  /** Ready and the model saw at least one decision. */
  labelable: boolean;
  /** GET /alerts/{id} only, when labelable. */
  label_context?: AILabelContext | null;
}

export interface AlertTriageLabelRequest {
  verdict: 'confirmed' | 'corrected';
  /** Alert-level catalogue key: required for corrected. */
  label?: string;
  /** Per-decision overrides (decision id -> catalogue key). */
  answers?: Record<string, string>;
}

export interface AlertListResponse {
  items: Alert[];
  page: number;
  page_size: number;
  total: number;
}

/** `GET /alerts?sort=` columns (with `dir=asc|desc`). */
export type AlertSortColumn = 'triggered_at' | 'status' | 'monitor' | 'duration';

/** Why a bulk acknowledge/resolve left an alert unchanged. */
export type AlertBulkFailureReason = 'not_found' | 'already_acknowledged' | 'already_resolved';

/** `POST /alerts/bulk/{acknowledge,resolve}`: per-alert results, never all-or-nothing. */
export interface AlertBulkResult {
  /** The alerts changed, in their new state (request order). */
  items: Alert[];
  /** The requested alerts left unchanged. */
  failed: { id: string; reason: AlertBulkFailureReason }[];
}

export type AlertStreamEventType = 'created' | 'acknowledged' | 'resolved';

export interface AlertStreamEvent {
  type: AlertStreamEventType;
  alert: Alert;
  received_at: string;
}
