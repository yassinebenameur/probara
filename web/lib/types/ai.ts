// --- AI labels (operator-confirmed answers for retraining the local model) ---

export type AILabelVerdict = 'confirmed' | 'corrected' | 'rejected';

export type AILabelAnswerSource = 'operator' | 'confirmed' | 'propagated';

export type AILabelSubjectType = 'incident_analysis' | 'dependency_pair' | 'alert_triage';

export interface AILabel {
  subject_type: AILabelSubjectType;
  subject_id?: string;
  verdict: AILabelVerdict;
  /** Incident-level key; for alert_triage labels, the alert-level key. */
  incident_label?: string;
  answers: Record<string, { label: string; source: AILabelAnswerSource }>;
  catalogue_version: string;
  model_version?: string;
  labeled_by?: string | null;
  labeled_at: string;
}

export interface AILabelChoice { key: string; title: string }

export interface AILabelDecision {
  id: string;
  monitor_id?: string | null;
  monitor_name?: string | null;
  predicted: string;
}

export interface AILabelContext {
  catalogue_version: string;
  predicted: string;
  choices: AILabelChoice[];
  decisions: AILabelDecision[];
  label: AILabel | null;
}

export interface IncidentAILabelRequest {
  analysis_id: string;
  verdict: 'confirmed' | 'corrected';
  incident_label?: string;
  answers?: Record<string, string>;
}

export type DependencyLabelCorrection = 'reversed' | 'shared_upstream' | 'no_dependency' | 'insufficient_evidence';

export interface DependencyLabelRequest {
  monitor_id: string;
  depends_on_id: string;
  verdict: 'confirmed' | 'rejected';
  corrected?: DependencyLabelCorrection;
}

export interface AILabelExportFilter {
  since?: string;
  until?: string;
  subject_types?: AILabelSubjectType[];
}

// rows/skipped are null when the export headers are missing.
export interface AILabelExportResult {
  blob: Blob;
  filename: string;
  rows: number | null;
  skipped: number | null;
}

export interface DependencySuggestion {
  metadata?: AIAnalysisMetadata;
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
  abstained_pairs: number;
}

export interface AISettings {
  enabled: boolean;
  installation_enabled: boolean;
  available: boolean;
  model?: string;
  message?: string;
  auto_triage_enabled: boolean;
  /** 0..3600; triage still waits at least two check intervals. */
  triage_delay_seconds: number;
  /** 0..86400; 0 = no cooldown. */
  triage_cooldown_seconds: number;
  /** 1..600. */
  triage_max_per_hour: number;
  /**
   * Send one suspected-cause follow-up to each channel already paged for an
   * alert (medium/high named causes only; not PagerDuty or Opsgenie). Takes
   * effect only with `enabled` and `auto_triage_enabled`.
   */
  triage_notifications_enabled: boolean;
}

export type AISettingsUpdate = Partial<{
  enabled: boolean;
  auto_triage_enabled: boolean;
  triage_notifications_enabled: boolean;
  triage_delay_seconds: number;
  triage_cooldown_seconds: number;
  triage_max_per_hour: number;
}>;

export type AITestRequest = Record<string, never>;

export interface AIAnalysisMetadata {
  engine: string;
  model_version?: string;
  catalogue_version?: string;
  hypothesis?: string;
  calibrated_score?: number;
  outcome: 'supported' | 'insufficient_evidence' | 'failed' | 'legacy';
  evidence_references: { ref: string; monitor_ref?: string; fact: string }[];
  incomplete_evidence: string[];
}

export interface AITestResult {
  ok: boolean;
  model?: string;
  message?: string;
}
