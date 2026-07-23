'use client';

import { useCallback, useEffect, useState } from 'react';

import Panel from '@/components/ui/Panel';
import Button from '@/components/ui/Button';
import Pill from '@/components/ui/Pill';
import { useToast } from '@/components/ui/ToastProvider';
import { getAISettings, updateAISettings, testAISettings } from '@/lib/api';
import type { AISettings, AITestResult } from '@/lib/types';

const SELECT_CLASS =
  'rounded border border-white/[0.08] bg-slate-800 px-3 py-2 text-xs text-slate-300 focus:border-cyan-500/50 focus:outline-none cursor-pointer';

// Provider options — extend as shared/ai gains vendors. The value must match a
// provider the backend's newProvider() understands.
const PROVIDERS = [{ value: 'openai_compat', label: 'OpenAI-compatible (OpenAI, Azure, Ollama, vLLM, SGLang, …)' }];

const JSON_MODES = [
  { value: 'off', label: 'Off — prompt + defensive parsing (most compatible)' },
  { value: 'json_object', label: 'json_object' },
  { value: 'json_schema', label: 'json_schema (strict)' },
];

const API_KEY_PLACEHOLDER = '••••••••  (saved — leave blank to keep)';

export function AISettingsPanel() {
  const { showToast } = useToast();
  const [loading, setLoading] = useState(true);
  const [saving, setSaving] = useState(false);
  const [testing, setTesting] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [testResult, setTestResult] = useState<AITestResult | null>(null);

  const [enabled, setEnabled] = useState(false);
  const [provider, setProvider] = useState('openai_compat');
  const [baseURL, setBaseURL] = useState('');
  const [model, setModel] = useState('');
  const [jsonMode, setJSONMode] = useState('off');
  const [maxTokens, setMaxTokens] = useState(1024);
  const [timeoutSeconds, setTimeoutSeconds] = useState(60);
  const [hasAPIKey, setHasAPIKey] = useState(false);
  const [apiKey, setApiKey] = useState(''); // empty = unchanged on save

  const applySettings = (s: AISettings) => {
    setEnabled(s.enabled);
    setProvider(s.provider || 'openai_compat');
    setBaseURL(s.base_url || '');
    setModel(s.model || '');
    setJSONMode(s.json_mode || 'off');
    setMaxTokens(s.max_tokens || 1024);
    setTimeoutSeconds(s.timeout_seconds || 60);
    setHasAPIKey(s.has_api_key);
    setApiKey('');
  };

  const loadSettings = useCallback(async () => {
    setLoading(true);
    setError(null);
    try {
      applySettings(await getAISettings());
    } catch (err) {
      setError(err instanceof Error ? err.message : 'Failed to load AI settings');
    } finally {
      setLoading(false);
    }
  }, []);

  useEffect(() => {
    loadSettings();
  }, [loadSettings]);

  const handleSave = async () => {
    setSaving(true);
    setError(null);
    try {
      const updated = await updateAISettings({
        enabled,
        provider,
        base_url: baseURL,
        model,
        json_mode: jsonMode,
        max_tokens: maxTokens,
        timeout_seconds: timeoutSeconds,
        // Only send api_key when the user typed one (else leave unchanged).
        ...(apiKey ? { api_key: apiKey } : {}),
      });
      applySettings(updated);
      showToast('AI settings saved', 'success');
    } catch (err) {
      const msg = err && typeof err === 'object' && 'message' in err
        ? String((err as { message?: unknown }).message)
        : 'Failed to save AI settings';
      setError(msg);
    } finally {
      setSaving(false);
    }
  };

  const handleTest = async () => {
    setTesting(true);
    setTestResult(null);
    try {
      const result = await testAISettings({
        provider,
        base_url: baseURL,
        model,
        json_mode: jsonMode,
        ...(apiKey ? { api_key: apiKey } : {}),
      });
      setTestResult(result);
    } catch (err) {
      setTestResult({ ok: false, message: err instanceof Error ? err.message : 'Test failed' });
    } finally {
      setTesting(false);
    }
  };

  return (
    <Panel
      title="AI root cause analysis"
      subtitle="Configure the LLM used to analyze incidents. Works with any OpenAI-compatible endpoint."
      dotColor="#ff5a24"
    >
      {loading ? (
        <div className="text-sm text-slate-500">Loading…</div>
      ) : (
        <div className="space-y-4">
          {error && (
            <div className="rounded-lg border border-rose-500/30 bg-rose-500/10 px-3 py-2 text-xs text-rose-200">
              {error}
            </div>
          )}

          <div className="flex items-start gap-3">
            <input
              type="checkbox"
              id="ai-enabled"
              checked={enabled}
              onChange={(e) => setEnabled(e.target.checked)}
              className="mt-0.5 h-3.5 w-3.5 rounded border-slate-600 bg-slate-800 text-cyan-500 accent-cyan-500 cursor-pointer flex-shrink-0"
            />
            <label htmlFor="ai-enabled" className="text-sm text-slate-300 cursor-pointer">
              Enable AI analysis for this workspace
            </label>
          </div>

          <div>
            <label className="mb-1.5 block text-xs font-medium text-slate-400">Provider</label>
            <select className={SELECT_CLASS} value={provider} onChange={(e) => setProvider(e.target.value)}>
              {PROVIDERS.map((p) => (
                <option key={p.value} value={p.value}>{p.label}</option>
              ))}
            </select>
          </div>

          <div>
            <label className="mb-1.5 block text-xs font-medium text-slate-400">Base URL</label>
            <input
              className="input"
              placeholder="http://localhost:30000/v1"
              value={baseURL}
              onChange={(e) => setBaseURL(e.target.value)}
            />
          </div>

          <div>
            <label className="mb-1.5 block text-xs font-medium text-slate-400">Model</label>
            <input
              className="input"
              placeholder="gpt-4o-mini"
              value={model}
              onChange={(e) => setModel(e.target.value)}
            />
          </div>

          <div>
            <label className="mb-1.5 block text-xs font-medium text-slate-400">
              API key {hasAPIKey && <span className="text-slate-500">(set)</span>}
            </label>
            <input
              className="input"
              type="password"
              placeholder={hasAPIKey ? API_KEY_PLACEHOLDER : 'Optional — leave blank for keyless local servers'}
              value={apiKey}
              onChange={(e) => setApiKey(e.target.value)}
            />
          </div>

          <details className="text-sm">
            <summary className="cursor-pointer text-xs font-medium text-slate-400">Advanced</summary>
            <div className="mt-3 grid grid-cols-2 gap-4">
              <div>
                <label className="mb-1.5 block text-xs font-medium text-slate-400">Structured output</label>
                <select className={SELECT_CLASS} value={jsonMode} onChange={(e) => setJSONMode(e.target.value)}>
                  {JSON_MODES.map((m) => (
                    <option key={m.value} value={m.value}>{m.label}</option>
                  ))}
                </select>
              </div>
              <div />
              <div>
                <label className="mb-1.5 block text-xs font-medium text-slate-400">Max tokens</label>
                <input
                  className="input"
                  type="number"
                  min={1}
                  value={maxTokens}
                  onChange={(e) => setMaxTokens(Number(e.target.value))}
                />
              </div>
              <div>
                <label className="mb-1.5 block text-xs font-medium text-slate-400">Timeout (seconds)</label>
                <input
                  className="input"
                  type="number"
                  min={1}
                  value={timeoutSeconds}
                  onChange={(e) => setTimeoutSeconds(Number(e.target.value))}
                />
              </div>
            </div>
          </details>

          {testResult && (
            <div
              className={`rounded-lg border px-3 py-2 text-xs ${
                testResult.ok
                  ? 'border-emerald-500/30 bg-emerald-500/10 text-emerald-200'
                  : 'border-rose-500/30 bg-rose-500/10 text-rose-200'
              }`}
            >
              {testResult.ok ? (
                <>Connected{testResult.model ? ` — model: ${testResult.model}` : ''}.</>
              ) : (
                <>Test failed: {testResult.message}</>
              )}
            </div>
          )}

          <div className="flex items-center justify-end gap-2">
            <Pill tone={enabled ? 'success' : 'neutral'} size="xs">
              {enabled ? 'Enabled' : 'Disabled'}
            </Pill>
            <Button variant="ghost" size="sm" loading={testing} disabled={testing || !baseURL || !model} onClick={handleTest}>
              Test connection
            </Button>
            <Button variant="accent" size="sm" loading={saving} disabled={saving} onClick={handleSave}>
              Save
            </Button>
          </div>
        </div>
      )}
    </Panel>
  );
}
