'use client';

import { useCallback, useEffect, useState } from 'react';

import Panel from '@/components/ui/Panel';
import Button from '@/components/ui/Button';
import Callout from '@/components/ui/Callout';
import Input from '@/components/ui/Input';
import Select from '@/components/ui/Select';
import { useToast } from '@/components/ui/ToastProvider';
import { getAISettings, updateAISettings, testAISettings } from '@/lib/api';
import type { AISettings, AITestResult } from '@/lib/types';

// Provider options — extend as shared/ai gains vendors. The value must match a
// provider the backend's newProvider() understands.
const PROVIDERS = [{ value: 'openai_compat', label: 'OpenAI-compatible (OpenAI, Azure, Ollama, vLLM, SGLang, …)' }];

const JSON_MODES = [
  { value: 'off', label: 'Off — prompt + defensive parsing (most compatible)' },
  { value: 'json_object', label: 'json_object' },
  { value: 'json_schema', label: 'json_schema (strict)' },
];

const API_KEY_PLACEHOLDER = '••••••••  (saved — leave blank to keep)';

// Read-only users see the effective configuration without a Save button.
export function AISettingsPanel({ readOnly = false }: { readOnly?: boolean }) {
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
    >
      {loading ? (
        <p className="text-sm text-slate-500">Loading…</p>
      ) : (
        <div className="space-y-4">
          {error && <Callout tone="danger">{error}</Callout>}

          <div className="flex items-start gap-3">
            <input
              type="checkbox"
              id="ai-enabled"
              checked={enabled}
              onChange={(e) => setEnabled(e.target.checked)}
              disabled={readOnly}
              className="mt-0.5 h-4 w-4 flex-shrink-0 cursor-pointer accent-cyan-500 disabled:cursor-not-allowed"
            />
            <label htmlFor="ai-enabled" className={`text-sm text-slate-200 ${readOnly ? 'cursor-not-allowed' : 'cursor-pointer'}`}>
              Enable AI analysis for this workspace
            </label>
          </div>

          <div className="grid gap-4 sm:grid-cols-2">
            <Select label="Provider" className="h-10" value={provider} disabled={readOnly} onChange={(e) => setProvider(e.target.value)}>
              {PROVIDERS.map((p) => (
                <option key={p.value} value={p.value}>{p.label}</option>
              ))}
            </Select>
            <Input
              label="Base URL"
              placeholder="http://localhost:30000/v1"
              value={baseURL}
              disabled={readOnly}
              onChange={(e) => setBaseURL(e.target.value)}
            />
            <Input
              label="Model"
              placeholder="gpt-4o-mini"
              value={model}
              disabled={readOnly}
              onChange={(e) => setModel(e.target.value)}
            />
            <div>
              <label className="mb-1.5 block text-ui font-medium text-slate-300">
                API key {hasAPIKey && <span className="font-normal text-slate-500">(set)</span>}
              </label>
              <input
                className="input"
                type="password"
                placeholder={hasAPIKey ? API_KEY_PLACEHOLDER : 'Optional — leave blank for keyless local servers'}
                value={apiKey}
                disabled={readOnly}
                onChange={(e) => setApiKey(e.target.value)}
              />
            </div>
          </div>

          <details>
            <summary className="cursor-pointer text-ui font-medium text-slate-400 transition-colors hover:text-white">
              Advanced
            </summary>
            <div className="mt-3 grid gap-4 sm:grid-cols-2 lg:grid-cols-4">
              <div className="sm:col-span-2">
                <Select label="Structured output" className="h-10" value={jsonMode} disabled={readOnly} onChange={(e) => setJSONMode(e.target.value)}>
                  {JSON_MODES.map((m) => (
                    <option key={m.value} value={m.value}>{m.label}</option>
                  ))}
                </Select>
              </div>
              <Input
                label="Max tokens"
                type="number"
                min={1}
                value={maxTokens}
                disabled={readOnly}
                onChange={(e) => setMaxTokens(Number(e.target.value))}
              />
              <Input
                label="Timeout (seconds)"
                type="number"
                min={1}
                value={timeoutSeconds}
                disabled={readOnly}
                onChange={(e) => setTimeoutSeconds(Number(e.target.value))}
              />
            </div>
          </details>

          {testResult && (
            <Callout tone={testResult.ok ? 'success' : 'danger'}>
              {testResult.ok ? (
                <>Connected{testResult.model ? ` — model: ${testResult.model}` : ''}.</>
              ) : (
                <>Test failed: {testResult.message}</>
              )}
            </Callout>
          )}

          <div className="flex items-center justify-end gap-2">
            <Button variant="ghost" size="sm" loading={testing} disabled={testing || !baseURL || !model} onClick={handleTest}>
              Test connection
            </Button>
            {!readOnly && (
              <Button variant="accent" size="sm" loading={saving} disabled={saving} onClick={handleSave}>
                Save
              </Button>
            )}
          </div>
        </div>
      )}
    </Panel>
  );
}
