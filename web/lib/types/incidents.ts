import type { AIAnalysisMetadata, AILabelContext } from './ai';
import type { AlertStatus, AlertTriage } from './alerts';
import type { MonitorType } from './monitor-configs';

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
  /** Compact automatic triage (never carries label_context here). */
  triage?: AlertTriage;
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
  severity: IncidentSeverity;
  owner_user_id?: string;
  owner_username?: string;
  /** Not in the list response yet; the list shows Opened once the API sends it. */
  created_at?: string;
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
  metadata?: AIAnalysisMetadata;
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
  /** Present only for ready analyses that recorded their model exchange. */
  label_context?: AILabelContext | null;
  /** Current names of the monitors the metadata cites, by ID (purged monitors absent). */
  monitor_names?: Record<string, string>;
}

export interface IncidentListResponse {
  items: IncidentListItem[];
  page: number;
  page_size: number;
  total: number;
  /** Unresolved incidents for the tenant, independent of pagination. */
  open_total: number;
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

export interface CreateIncidentTimelineEntryRequest {
  entry_type: 'internal_note' | 'public_update';
  message: string;
  metadata?: Record<string, unknown>;
}

export interface PublishIncidentToStatusPageRequest {
  monitor_ids: string[];
}
