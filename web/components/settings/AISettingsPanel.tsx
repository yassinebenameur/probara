'use client';

import { useCallback, useEffect, useState } from 'react';
import Panel from '@/components/ui/Panel';
import Button from '@/components/ui/Button';
import Callout from '@/components/ui/Callout';
import Checkbox from '@/components/ui/Checkbox';
import Input from '@/components/ui/Input';
import { useToast } from '@/components/ui/ToastProvider';
import { useIsTenantAdmin } from '@/components/providers/CurrentUserProvider';
import { getAISettings, updateAISettings, testAISettings, exportAILabels } from '@/lib/api';
import { labelErrorMessage, labelExportOutcome } from '@/lib/ai-labels';
import { TRIAGE_SETTING_RANGES, parseSettingInput, triageSettingsForSave } from '@/lib/ai-triage';
import type { TriageSettingsErrors, TriageSettingsValues } from '@/lib/ai-triage';
import type { AISettings, AITestResult } from '@/lib/types';
import { SETTINGS_PANEL_COLUMNS } from './layout';

// Number inputs hold text so a field can be cleared while typing; the values
// are parsed and range-checked on save (skipped while the controls are locked).
type TriageDraft = Record<keyof TriageSettingsValues, string>;

function triageDraft(s: AISettings): TriageDraft {
  return {
    triage_delay_seconds: String(s.triage_delay_seconds),
    triage_cooldown_seconds: String(s.triage_cooldown_seconds),
    triage_max_per_hour: String(s.triage_max_per_hour),
  };
}

const TRIAGE_FIELDS: { key: keyof TriageSettingsValues; label: string }[] = [
  { key: 'triage_delay_seconds', label: 'Wait before triage (s)' },
  { key: 'triage_cooldown_seconds', label: 'Cooldown per monitor (s)' },
  { key: 'triage_max_per_hour', label: 'Max triages per hour' },
];

// The three triage numbers sit in one row once the well reaches 28rem, else
// one per row, never 2+1: below 28rem the second max() term turns huge and
// the track minimum becomes 100%. items-end keeps the inputs level when a
// label wraps.
const TRIAGE_FIELDS_GRID =
  'grid items-end gap-3 grid-cols-[repeat(auto-fill,minmax(min(100%,max(calc((100%_-_1.5rem)/3),calc((28rem_-_100%)*999))),1fr))]';

// API errors are thrown as plain `{error, message}` objects, not Error instances.
function errorText(err: unknown, fallback: string): string {
  if (err && typeof err === 'object' && 'message' in err) {
    const m = (err as { message?: unknown }).message;
    if (typeof m === 'string' && m) return m;
  }
  return fallback;
}

export function AISettingsPanel({ readOnly = false }: { readOnly?: boolean }) {
  const { showToast } = useToast();
  const [settings, setSettings] = useState<AISettings | null>(null);
  const [enabled, setEnabled] = useState(true);
  const [autoTriage, setAutoTriage] = useState(false);
  const [notifyCauses, setNotifyCauses] = useState(false);
  const [draft, setDraft] = useState<TriageDraft>({ triage_delay_seconds: '60', triage_cooldown_seconds: '900', triage_max_per_hour: '60' });
  const [fieldErrors, setFieldErrors] = useState<TriageSettingsErrors>({});
  const [saving, setSaving] = useState(false);
  const [testing, setTesting] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [probe, setProbe] = useState<AITestResult | null>(null);
  const isTenantAdmin = useIsTenantAdmin();
  const [exporting, setExporting] = useState(false);
  const apply = useCallback((s: AISettings) => {
    setSettings(s);
    setEnabled(s.enabled);
    setAutoTriage(s.auto_triage_enabled);
    setNotifyCauses(s.triage_notifications_enabled);
    setDraft(triageDraft(s));
    setFieldErrors({});
  }, []);
  const load = useCallback(async () => {
    try { apply(await getAISettings()); }
    catch (err) { setError(errorText(err, 'Could not load AI settings')); }
  }, [apply]);
  useEffect(() => { void load(); }, [load]);
  // Triage needs AI on for the workspace; the toggle above is the draft value.
  const triageLocked = readOnly || !settings?.installation_enabled || !enabled;
  const save = async () => {
    if (!settings) return;
    const { payload, errors } = triageSettingsForSave(triageLocked, {
      auto_triage_enabled: autoTriage,
      triage_notifications_enabled: notifyCauses,
      triage_delay_seconds: parseSettingInput(draft.triage_delay_seconds),
      triage_cooldown_seconds: parseSettingInput(draft.triage_cooldown_seconds),
      triage_max_per_hour: parseSettingInput(draft.triage_max_per_hour),
    }, settings);
    setFieldErrors(errors);
    if (Object.keys(errors).length > 0) return;
    setSaving(true); setError(null);
    try {
      apply(await updateAISettings({ enabled, ...payload }));
      showToast('AI settings saved', 'success');
    }
    catch (err) { setError(errorText(err, 'Could not save AI settings')); }
    finally { setSaving(false); }
  };
  const test = async () => {
    setTesting(true);
    try { const p = await testAISettings(); setProbe(p); setSettings((s) => s ? { ...s, available: p.ok, model: p.model, message: p.message } : s); }
    catch (err) { setProbe({ ok: false, message: errorText(err, 'Local service unavailable') }); }
    finally { setTesting(false); }
  };
  // Same download path as the monitor export (app/monitors/page.tsx).
  const exportLabels = async () => {
    setExporting(true);
    try {
      const { blob, filename, rows, skipped } = await exportAILabels();
      const outcome = labelExportOutcome(rows, skipped);
      if (!outcome.download) {
        showToast(outcome.message, outcome.tone);
        return;
      }
      const url = window.URL.createObjectURL(blob);
      const link = document.createElement('a');
      link.href = url;
      link.download = filename;
      document.body.appendChild(link);
      link.click();
      link.remove();
      window.URL.revokeObjectURL(url);
      showToast(outcome.message, outcome.tone);
    } catch (err) {
      showToast(labelErrorMessage(err, 'Could not export training labels'), 'error');
    } finally {
      setExporting(false);
    }
  };
  return (
    <Panel title="Local AI analysis" subtitle="CPU analysis of monitoring evidence for dependency suggestions, suspected incident causes and alert triage.">
      <div className="space-y-4">
        {error && <Callout tone="danger">{error}</Callout>}
        {!settings && !error && <p role="status" className="text-sm text-slate-400">Loading…</p>}
        {settings && <>
          {/* Workspace switch and service status beside the triage controls
              once the panel is wide enough; one Save covers both. */}
          <div className={SETTINGS_PANEL_COLUMNS}>
            <div className="space-y-4">
              <Callout tone={settings.available ? 'success' : 'warning'}>
                {settings.installation_enabled ? (settings.available ? 'Local service available' : 'Local service unavailable') : 'Disabled for this installation'}
                {settings.model && ` · ${settings.model}`}
                {/* The disabled headline already says it; the message repeats it. */}
                {settings.installation_enabled && settings.message && <p className="mt-1">{settings.message}</p>}
              </Callout>
              <Checkbox
                checked={enabled}
                onChange={setEnabled}
                disabled={readOnly || !settings.installation_enabled}
                label="Enable AI analysis for this workspace"
              />
              <p className="max-w-prose text-ui text-slate-400">Explanations cite recorded checks and maintained troubleshooting guidance. The model selects a suspected cause and abstains when evidence is insufficient. Controlled-scenario validation does not establish real-world accuracy.</p>
            </div>
            <div className="glass-well space-y-3 p-4">
              <div>
                <h3 className="text-sm font-medium text-slate-200">Automatic alert triage</h3>
                <p className="mt-1 max-w-prose text-ui text-slate-400">Triage never delays, routes, suppresses or re-prioritises a page. Waits at least two check intervals.</p>
              </div>
              <Checkbox
                checked={autoTriage}
                onChange={setAutoTriage}
                disabled={triageLocked}
                label="Triage new alerts automatically"
              />
              <Checkbox
                checked={notifyCauses}
                onChange={setNotifyCauses}
                disabled={triageLocked || !autoTriage}
                label="Send suspected causes to alert channels"
                className="max-w-prose"
                description="When triage names a cause with medium or high confidence, each channel already paged for the alert gets one follow-up message. Pages are never delayed. PagerDuty and Opsgenie get nothing."
              />
              <div className={TRIAGE_FIELDS_GRID}>
                {TRIAGE_FIELDS.map(({ key, label }) => (
                  <Input
                    key={key}
                    label={label}
                    type="number"
                    inputMode="numeric"
                    step={1}
                    min={TRIAGE_SETTING_RANGES[key].min}
                    max={TRIAGE_SETTING_RANGES[key].max}
                    value={draft[key]}
                    onChange={(e) => {
                      const text = e.target.value;
                      setDraft((d) => ({ ...d, [key]: text }));
                      setFieldErrors((errs) => ({ ...errs, [key]: undefined }));
                    }}
                    disabled={triageLocked}
                    error={fieldErrors[key]}
                  />
                ))}
              </div>
            </div>
          </div>
          {probe && <Callout tone={probe.ok ? 'success' : 'danger'}>{probe.ok ? 'Local CPU service is ready.' : probe.message}</Callout>}
          <div className="flex justify-end gap-2">
            <Button variant="ghost" size="sm" loading={testing} disabled={testing || !settings.installation_enabled} onClick={test}>Test local service</Button>
            {!readOnly && <Button variant="accent" size="sm" loading={saving} disabled={saving || !settings.installation_enabled} onClick={save}>Save</Button>}
          </div>
        </>}
        {isTenantAdmin && (
          <div className="flex flex-wrap items-end justify-between gap-3 border-t border-divider pt-4">
            <div className="max-w-prose">
              <h3 className="text-sm font-medium text-slate-200">Training labels</h3>
              <p className="mt-1 text-ui text-slate-400">Download operator-confirmed answers as JSONL for retraining the local model. Rows contain evidence counters and catalogue labels only, with no monitor names or IDs.</p>
            </div>
            <Button variant="ghost" size="sm" loading={exporting} disabled={exporting} onClick={exportLabels}>Export labels</Button>
          </div>
        )}
      </div>
    </Panel>
  );
}
