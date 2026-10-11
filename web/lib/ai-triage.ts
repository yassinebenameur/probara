import type { Alert, AlertTriage, AlertTriageLabelRequest, AlertTriageSkipReason } from './types';
import { aiConfidenceTone } from './status-tones';
import type { StatusTone } from './status-tones';

// Pure helpers for automatic alert triage. Triage never changes paging: it
// never delays, routes, suppresses or re-prioritises a notification or changes
// severity, and the pill copy says so. The only message it can cause is the
// opt-in suspected-cause follow-up (triage_notifications_enabled).

export const TRIAGE_PAGING_NOTE = 'Never delays, routes or suppresses a page.';

/** The catalogue key the model uses when it abstains. */
export const TRIAGE_ABSTAIN = 'insufficient_evidence';

export interface TriagePresentation {
  /** False when nothing should render (no row, or a type triage does not model). */
  visible: boolean;
  label: string;
  tone: StatusTone;
  tooltip: string;
  /** Pending, failed and skipped states render subdued. */
  muted: boolean;
}

const SKIP_TOOLTIP: Record<Exclude<AlertTriageSkipReason, 'not_modeled'>, string> = {
  resolved: 'Not triaged: the alert resolved before triage ran.',
  cooldown: 'Not triaged: this monitor was triaged recently (cooldown).',
  disabled: 'Not triaged: automatic triage is turned off.',
};

const HIDDEN: TriagePresentation = { visible: false, label: '', tone: 'neutral', tooltip: '', muted: true };

function joinTooltip(...parts: (string | null | undefined)[]): string {
  return parts.filter((p): p is string => Boolean(p && p.trim())).join('\n\n');
}

export function triagePresentation(t: AlertTriage | null | undefined): TriagePresentation {
  if (!t) return HIDDEN;
  switch (t.status) {
    case 'ready': {
      if (!t.hypothesis || t.hypothesis === TRIAGE_ABSTAIN) {
        return {
          visible: true,
          label: 'No clear cause',
          tone: 'neutral',
          tooltip: joinTooltip(t.summary, TRIAGE_PAGING_NOTE),
          muted: false,
        };
      }
      const title = t.title || t.hypothesis;
      return {
        visible: true,
        label: `Suspected ${title}${t.confidence ? ` · ${t.confidence}` : ''}`,
        tone: aiConfidenceTone(t.confidence),
        tooltip: joinTooltip(t.summary, TRIAGE_PAGING_NOTE),
        muted: false,
      };
    }
    case 'pending':
      return { visible: true, label: 'Triaging…', tone: 'neutral', tooltip: `Automatic triage is running. ${TRIAGE_PAGING_NOTE}`, muted: true };
    case 'failed':
      return { visible: true, label: 'Triage failed', tone: 'neutral', tooltip: t.error_message || 'Automatic triage failed.', muted: true };
    case 'skipped': {
      if (t.skip_reason === 'not_modeled' || !t.skip_reason) return HIDDEN;
      return { visible: true, label: 'Not triaged', tone: 'neutral', tooltip: SKIP_TOOLTIP[t.skip_reason] ?? 'Not triaged.', muted: true };
    }
    default:
      return HIDDEN;
  }
}

/**
 * Applies an alert update to a listed row. Live stream events come from core
 * NATS and never carry triage, so an incoming alert without one keeps the
 * row's: a triage row is never deleted while its alert exists.
 */
export function mergeAlertUpdate(prev: Alert | undefined, incoming: Alert): Alert {
  if (incoming.triage === undefined && prev?.triage !== undefined) {
    return { ...incoming, triage: prev.triage };
  }
  return incoming;
}

/**
 * Marks one listed alert's triage as labelled. A patch over the current list
 * (used in a setState updater), never a whole row captured when the label
 * action started: a live update that landed meanwhile must survive.
 */
export function markTriageLabelled(alerts: Alert[], alertId: string): Alert[] {
  return alerts.map((a) => (a.id === alertId && a.triage ? { ...a, triage: { ...a.triage, labelled: true } } : a));
}

export interface TriageMenuCopy {
  confirm: string;
  correct: string;
  change: string;
}

/**
 * Row-menu wording for labelling a ready triage. Kept short enough for the
 * fixed RowMenu width. An abstention is still labelable (its decisions have
 * insufficient_evidence finals), so confirming it must not read as
 * confirming a cause.
 */
export function triageMenuCopy(t: AlertTriage | null | undefined): TriageMenuCopy {
  const abstained = !t?.hypothesis || t.hypothesis === TRIAGE_ABSTAIN;
  return abstained
    ? { confirm: 'Confirm no cause', correct: 'Name the cause…', change: 'Change label…' }
    : { confirm: 'Confirm cause', correct: 'Correct cause…', change: 'Change label…' };
}

export function buildTriageLabelRequest(
  verdict: 'confirmed' | 'corrected',
  label?: string,
  overrides?: Record<string, string>
): AlertTriageLabelRequest {
  const req: AlertTriageLabelRequest = { verdict };
  if (verdict === 'corrected' && label) req.label = label;
  // '' is the "Follow the actual cause" option: the server propagates instead.
  const answers = Object.fromEntries(Object.entries(overrides ?? {}).filter(([, key]) => key !== ''));
  if (Object.keys(answers).length > 0) req.answers = answers;
  return req;
}

// ── Settings ─────────────────────────────────────────────────────────────

export interface TriageSettingsValues {
  triage_delay_seconds: number;
  triage_cooldown_seconds: number;
  triage_max_per_hour: number;
}

/** Ranges pinned in migration 000094 and the ai-settings API. */
export const TRIAGE_SETTING_RANGES: Record<keyof TriageSettingsValues, { min: number; max: number }> = {
  triage_delay_seconds: { min: 0, max: 3600 },
  triage_cooldown_seconds: { min: 0, max: 86400 },
  triage_max_per_hour: { min: 1, max: 600 },
};

export type TriageSettingsErrors = Partial<Record<keyof TriageSettingsValues, string>>;

/**
 * Mirrors the API's ranges; empty when every value is valid. The texts are
 * operator copy shown under the labelled inputs; the API's own snake_case
 * messages only surface for a server 400, in the panel's Callout.
 */
export function validateTriageSettings(values: TriageSettingsValues): TriageSettingsErrors {
  const errors: TriageSettingsErrors = {};
  for (const key of Object.keys(TRIAGE_SETTING_RANGES) as (keyof TriageSettingsValues)[]) {
    const { min, max } = TRIAGE_SETTING_RANGES[key];
    const v = values[key];
    if (!Number.isInteger(v) || v < min || v > max) {
      errors[key] = `Must be a whole number from ${min} to ${max}`;
    }
  }
  return errors;
}

export interface TriageSettingsPayload extends TriageSettingsValues {
  auto_triage_enabled: boolean;
  triage_notifications_enabled: boolean;
}

/**
 * The triage part of an ai-settings PUT. While the triage controls are
 * locked (disabled because the AI draft toggle is off), their draft is not
 * editable, so it is neither validated nor sent: the last loaded server
 * values go instead, and turning AI off is never blocked by a field the
 * operator cannot reach.
 */
export function triageSettingsForSave(
  locked: boolean,
  draft: TriageSettingsPayload,
  server: TriageSettingsPayload
): { payload: TriageSettingsPayload; errors: TriageSettingsErrors } {
  const pick = (s: TriageSettingsPayload): TriageSettingsPayload => ({
    auto_triage_enabled: s.auto_triage_enabled,
    triage_notifications_enabled: s.triage_notifications_enabled,
    triage_delay_seconds: s.triage_delay_seconds,
    triage_cooldown_seconds: s.triage_cooldown_seconds,
    triage_max_per_hour: s.triage_max_per_hour,
  });
  if (locked) return { payload: pick(server), errors: {} };
  return { payload: pick(draft), errors: validateTriageSettings(draft) };
}

/** Parses a number input's text: '' and non-numbers become NaN (invalid). */
export function parseSettingInput(text: string): number {
  return text.trim() === '' ? Number.NaN : Number(text);
}
