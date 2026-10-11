import type {
  AILabel,
  AILabelChoice,
  DependencyLabelCorrection,
  DependencyLabelRequest,
  DependencySuggestion,
  IncidentAILabelRequest,
} from './types';

// Pure helpers for the AI label controls (incident analysis Confirm/Correct,
// dependency suggestion Accept/Dismiss). The server copies the evidence from
// its own snapshot; the client only ever sends a verdict and catalogue keys.

export function buildIncidentLabelRequest(
  analysisId: string,
  verdict: 'confirmed' | 'corrected',
  incidentLabel?: string,
  overrides?: Record<string, string>
): IncidentAILabelRequest {
  const req: IncidentAILabelRequest = { analysis_id: analysisId, verdict };
  if (verdict === 'corrected' && incidentLabel) req.incident_label = incidentLabel;
  // '' is the "Follow the actual cause" option: the server propagates instead.
  const answers = Object.fromEntries(Object.entries(overrides ?? {}).filter(([, key]) => key !== ''));
  if (Object.keys(answers).length > 0) req.answers = answers;
  return req;
}

export function choiceTitle(choices: AILabelChoice[], key: string): string {
  return choices.find((c) => c.key === key)?.title ?? key;
}

function byWhom(labeledBy: string | null | undefined, currentUserId?: string | null): string {
  if (!labeledBy) return '';
  return currentUserId && labeledBy === currentUserId ? ' by you' : ' by a teammate';
}

export function labelSummary(label: AILabel, choices: AILabelChoice[], currentUserId?: string | null): string {
  const who = byWhom(label.labeled_by, currentUserId);
  if (label.verdict === 'confirmed') return `Confirmed${who}`;
  if (label.verdict === 'corrected' && label.incident_label) {
    return `Corrected to ${choiceTitle(choices, label.incident_label)}${who}`;
  }
  return 'Labelled';
}

export function dependencyLabelRequest(
  s: Pick<DependencySuggestion, 'monitor_id' | 'depends_on_id'>,
  verdict: 'confirmed' | 'rejected',
  corrected?: DependencyLabelCorrection
): DependencyLabelRequest {
  const req: DependencyLabelRequest = { monitor_id: s.monitor_id, depends_on_id: s.depends_on_id, verdict };
  if (verdict === 'rejected' && corrected) req.corrected = corrected;
  return req;
}

// API errors are thrown as plain `{error, message}` objects, not Error
// instances; the label endpoints' 409 messages are written for operators.
export function labelErrorMessage(err: unknown, fallback: string): string {
  if (err && typeof err === 'object' && 'message' in err) {
    const m = (err as { message?: unknown }).message;
    if (typeof m === 'string' && m) return m;
  }
  return fallback;
}

// Parses an X-Probara-Export-Rows/-Skipped header value; null when absent or
// not a non-negative integer (the counts are then unknown, not zero).
export function parseExportCount(value: string | null): number | null {
  if (value === null || !/^\d+$/.test(value.trim())) return null;
  return Number(value.trim());
}

export interface LabelExportOutcome {
  download: boolean;
  message: string;
  tone: 'success' | 'info' | 'warning';
}

function plural(n: number, one: string, many: string): string {
  return `${n} ${n === 1 ? one : many}`;
}

// What the export button does with a response: a known-empty export skips
// the 0-byte download, and skipped labels (evidence that failed validation)
// are always surfaced. Unknown counts fall back to a plain download.
export function labelExportOutcome(rows: number | null, skipped: number | null): LabelExportOutcome {
  const skippedNote = skipped && skipped > 0
    ? `${plural(skipped, 'label was', 'labels were')} skipped because their evidence failed validation`
    : '';
  if (rows === 0) {
    return skippedNote
      ? { download: false, message: `No training rows to export: ${skippedNote}.`, tone: 'warning' }
      : { download: false, message: 'No labels to export yet.', tone: 'info' };
  }
  const base = rows === null ? 'Training labels downloaded' : `Downloaded ${plural(rows, 'training row', 'training rows')}`;
  return skippedNote
    ? { download: true, message: `${base}; ${skippedNote}.`, tone: 'warning' }
    : { download: true, message: base, tone: 'success' };
}

// Whether AILabelControl has anything to render. While identity loads,
// useCanWrite() reads false and the viewer note waits for loading to clear,
// so an unlabelled analysis has no content yet — render nothing rather than
// an empty divider that the prompt later pops into.
export function labelControlHasContent(hasLabel: boolean, canWrite: boolean, userLoading: boolean): boolean {
  return hasLabel || canWrite || !userLoading;
}
