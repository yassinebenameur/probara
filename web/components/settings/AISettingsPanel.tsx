'use client';

import { useCallback, useEffect, useState } from 'react';
import Panel from '@/components/ui/Panel';
import Button from '@/components/ui/Button';
import Callout from '@/components/ui/Callout';
import { useToast } from '@/components/ui/ToastProvider';
import { getAISettings, updateAISettings, testAISettings } from '@/lib/api';
import type { AISettings, AITestResult } from '@/lib/types';

export function AISettingsPanel({ readOnly = false }: { readOnly?: boolean }) {
  const { showToast } = useToast();
  const [settings, setSettings] = useState<AISettings | null>(null);
  const [enabled, setEnabled] = useState(true);
  const [saving, setSaving] = useState(false);
  const [testing, setTesting] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [probe, setProbe] = useState<AITestResult | null>(null);
  const load = useCallback(async () => {
    try { const s = await getAISettings(); setSettings(s); setEnabled(s.enabled); }
    catch (err) { setError(err instanceof Error ? err.message : 'Could not load AI settings'); }
  }, []);
  useEffect(() => { void load(); }, [load]);
  const save = async () => {
    setSaving(true); setError(null);
    try { const s = await updateAISettings({ enabled }); setSettings(s); setEnabled(s.enabled); showToast('AI settings saved', 'success'); }
    catch (err) { setError(err instanceof Error ? err.message : 'Could not save AI settings'); }
    finally { setSaving(false); }
  };
  const test = async () => {
    setTesting(true);
    try { const p = await testAISettings(); setProbe(p); setSettings((s) => s ? { ...s, available: p.ok, model: p.model, message: p.message } : s); }
    catch (err) { setProbe({ ok: false, message: err instanceof Error ? err.message : 'Local service unavailable' }); }
    finally { setTesting(false); }
  };
  return (
    <Panel title="Local AI analysis" subtitle="CPU analysis of monitoring evidence for dependency suggestions and suspected incident causes.">
      <div className="space-y-4">
        {error && <Callout tone="danger">{error}</Callout>}
        {!settings && !error && <p className="text-sm text-slate-400">Loading…</p>}
        {settings && <>
          <Callout tone={settings.available ? 'success' : 'warning'}>
            {settings.installation_enabled ? (settings.available ? 'Local service available' : 'Local service unavailable') : 'Disabled for this installation'}
            {settings.model && ` · ${settings.model}`}
            {settings.message && <p className="mt-1">{settings.message}</p>}
          </Callout>
          <label className="flex items-center gap-3 text-sm text-slate-200">
            <input type="checkbox" checked={enabled} onChange={(e) => setEnabled(e.target.checked)} disabled={readOnly || !settings.installation_enabled} className="h-4 w-4 accent-cyan-500" />
            Enable AI analysis for this workspace
          </label>
          <p className="text-ui text-slate-400">Explanations cite recorded checks and maintained troubleshooting guidance. The model selects a suspected cause and abstains when evidence is insufficient. Controlled-scenario validation does not establish real-world accuracy.</p>
          {probe && <Callout tone={probe.ok ? 'success' : 'danger'}>{probe.ok ? 'Local CPU service is ready.' : probe.message}</Callout>}
          <div className="flex justify-end gap-2">
            <Button variant="ghost" size="sm" loading={testing} disabled={testing || !settings.installation_enabled} onClick={test}>Test local service</Button>
            {!readOnly && <Button variant="accent" size="sm" loading={saving} disabled={saving || !settings.installation_enabled} onClick={save}>Save</Button>}
          </div>
        </>}
      </div>
    </Panel>
  );
}
