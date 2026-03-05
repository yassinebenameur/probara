'use client';

import { useState, useEffect } from 'react';
import { Monitor, CreateMonitorRequest, UpdateMonitorRequest, AlertPolicy, PushMonitorConfig, PushInfo } from '@/lib/types';
import { getAlertPolicies, getPushInfo } from '@/lib/api';
import { getApiKey } from '@/lib/auth';

interface PushFormProps {
  monitor?: Monitor;
  initialData?: CreateMonitorRequest;
  onSubmit: (data: CreateMonitorRequest | UpdateMonitorRequest) => Promise<void>;
  onCancel?: () => void;
  loading?: boolean;
}

export default function PushForm({
  monitor,
  initialData,
  onSubmit,
  onCancel,
  loading = false,
}: PushFormProps) {
  const [alertPolicies, setAlertPolicies] = useState<AlertPolicy[]>([]);
  const [showWebhookInfo, setShowWebhookInfo] = useState(false);
  const [pushInfo, setPushInfo] = useState<PushInfo | null>(null);
  const [loadingPushInfo, setLoadingPushInfo] = useState(false);
  const [copiedField, setCopiedField] = useState<string | null>(null);
  const isEditMode = Boolean(monitor);
  const initialPushConfig =
    !isEditMode && initialData?.type === 'push' ? (initialData.config as PushMonitorConfig) : undefined;

  const [formData, setFormData] = useState({
    name: monitor?.name || initialData?.name || '',
    expected_interval_seconds: monitor && monitor.type === 'push' 
      ? (monitor.config as PushMonitorConfig)?.expected_interval_seconds || 60 
      : initialPushConfig?.expected_interval_seconds || 60,
    grace_period_seconds: monitor && monitor.type === 'push' 
      ? (monitor.config as PushMonitorConfig)?.grace_period_seconds || 120 
      : initialPushConfig?.grace_period_seconds || 120,
    alert_policy_ids:
      monitor?.alert_policy_ids ||
      (monitor?.alert_policy_id ? [monitor.alert_policy_id] : initialData?.alert_policy_ids || []),
    enabled: monitor?.enabled ?? initialData?.enabled ?? true,
    tags: monitor?.tags?.join(', ') || (initialData?.tags || []).join(', '),
  });

  const [errors, setErrors] = useState<Record<string, string>>({});

  useEffect(() => {
    loadAlertPolicies();
  }, []);

  // Auto-calculate grace period when interval changes (default to 2x interval)
  useEffect(() => {
    if (!isEditMode && !initialPushConfig) {
      setFormData(prev => ({
        ...prev,
        grace_period_seconds: prev.expected_interval_seconds * 2,
      }));
    }
  }, [formData.expected_interval_seconds, isEditMode, initialPushConfig]);

  const loadAlertPolicies = async () => {
    try {
      const response = await getAlertPolicies({ page_size: 100 });
      setAlertPolicies(response?.items || []);
    } catch (error) {
      console.error('Failed to load alert policies:', error);
    }
  };

  const handleSubmit = async (e: React.FormEvent) => {
    e.preventDefault();
    setErrors({});

    const newErrors: Record<string, string> = {};
    if (!formData.name.trim()) newErrors.name = 'Name is required';
    if (formData.expected_interval_seconds < 5) newErrors.expected_interval_seconds = 'Minimum 5 seconds';
    if (formData.grace_period_seconds < 0) newErrors.grace_period_seconds = 'Cannot be negative';

    if (Object.keys(newErrors).length > 0) {
      setErrors(newErrors);
      return;
    }

    const config: PushMonitorConfig = {
      push_token: isEditMode ? monitor?.push_token || '' : '',
      expected_interval_seconds: formData.expected_interval_seconds,
      grace_period_seconds: formData.grace_period_seconds,
    };

    const requestData: CreateMonitorRequest | UpdateMonitorRequest = {
      name: formData.name.trim(),
      type: 'push',
      config,
      interval_seconds: formData.expected_interval_seconds,
      timeout_seconds: formData.expected_interval_seconds, // For push monitors, timeout equals interval
      enabled: formData.enabled,
    };

    requestData.alert_policy_ids = formData.alert_policy_ids;
    if (formData.tags.trim()) {
      requestData.tags = formData.tags.split(',').map(t => t.trim()).filter(t => t);
    }

    try {
      await onSubmit(requestData);
      if (!isEditMode) setShowWebhookInfo(true);
    } catch (error) {
      console.error('Failed to save monitor:', error);
    }
  };

  const loadPushInfo = async () => {
    if (!monitor?.id) return;
    setLoadingPushInfo(true);
    try {
      const info = await getPushInfo(monitor.id);
      setPushInfo(info);
    } catch (error) {
      console.error('Failed to load push info:', error);
    } finally {
      setLoadingPushInfo(false);
    }
  };

  const copyToClipboard = (text: string, field: string) => {
    navigator.clipboard.writeText(text);
    setCopiedField(field);
    setTimeout(() => setCopiedField(null), 2000);
  };

  // Webhook info view (shown after creation or when viewing existing)
  if (isEditMode && monitor && showWebhookInfo) {
    if (!pushInfo && !loadingPushInfo) loadPushInfo();

    return (
      <div className="space-y-5">
        <div className="rounded-xl border border-white/[0.06] bg-slate-800/30 p-5">
          <h3 className="text-sm font-medium text-white mb-4">Webhook Information</h3>

          {loadingPushInfo ? (
            <div className="text-sm text-slate-500">Loading webhook details...</div>
          ) : pushInfo ? (
            <div className="space-y-4">
              {/* Webhook URL */}
              <div>
                <label className="block text-xs text-slate-500 mb-1">Webhook URL</label>
                <div className="flex gap-2">
                  <code className="flex-1 rounded-lg border border-white/[0.08] bg-slate-900/50 px-3 py-2 text-xs text-cyan-400 font-mono overflow-x-auto">
                    {pushInfo.webhook_url}
                  </code>
                  <button
                    onClick={() => copyToClipboard(pushInfo.webhook_url, 'webhook_url')}
                    className="btn btn-xs btn-outline"
                  >
                    {copiedField === 'webhook_url' ? 'OK' : 'Copy'}
                  </button>
                </div>
              </div>

              {/* Token */}
              <div>
                <label className="block text-xs text-slate-500 mb-1">Push Token</label>
                <div className="flex gap-2">
                  <code className="flex-1 rounded-lg border border-white/[0.08] bg-slate-900/50 px-3 py-2 text-xs text-cyan-400 font-mono overflow-x-auto">
                    {pushInfo.push_token}
                  </code>
                  <button
                    onClick={() => copyToClipboard(pushInfo.push_token, 'push_token')}
                    className="btn btn-xs btn-outline"
                  >
                    {copiedField === 'push_token' ? 'OK' : 'Copy'}
                  </button>
                </div>
              </div>

              {/* Example Commands */}
              <div>
                <label className="block text-xs text-slate-500 mb-1">Example Usage</label>
                <pre className="rounded-lg border border-white/[0.08] bg-slate-900/50 px-3 py-2 text-xs text-slate-300 font-mono overflow-x-auto whitespace-pre-wrap">
                  {pushInfo.example_curl}
                </pre>
                <button
                  onClick={() => copyToClipboard(pushInfo.example_curl, 'example')}
                  className="btn btn-xs btn-outline mt-2"
                >
                  {copiedField === 'example' ? 'Copied' : 'Copy Examples'}
                </button>
              </div>

              {/* Info Box */}
              <div className="rounded-lg border border-emerald-500/20 bg-emerald-500/5 px-4 py-3">
                <div className="flex items-start gap-3">
                  <span className="text-emerald-400">OK</span>
                  <div>
                    <p className="text-xs font-medium text-white mb-1">Auto-Detected Metrics</p>
                    <p className="text-xs text-slate-400">
                      Any additional parameters you send (besides &quot;status&quot; and &quot;error&quot;) will be 
                      automatically captured and displayed as metrics. Send latency, CPU, memory, 
                      or any custom metrics you need!
                    </p>
                  </div>
                </div>
              </div>

              {/* Timing Info */}
              <div className="rounded-lg border border-amber-500/20 bg-amber-500/5 px-4 py-3">
                <div className="flex items-start gap-3">
                  <span className="text-amber-400">Timer</span>
                  <div>
                    <p className="text-xs font-medium text-white mb-1">Expected Timing</p>
                    <p className="text-xs text-slate-400">
                      Push every <strong>{pushInfo.interval_seconds}s</strong>. 
                      Grace period: <strong>{pushInfo.grace_period_seconds}s</strong>. 
                      Monitor will be marked as down if no push is received within {pushInfo.interval_seconds + pushInfo.grace_period_seconds}s.
                    </p>
                  </div>
                </div>
              </div>
            </div>
          ) : (
            <p className="text-sm text-rose-400">Failed to load webhook details</p>
          )}
        </div>

        {/* Actions */}
        <div className="flex items-center justify-end gap-3">
          <button
            type="button"
            onClick={() => setShowWebhookInfo(false)}
            className="btn btn-secondary"
          >
            Back to Settings
          </button>
          {onCancel && (
            <button
              type="button"
              onClick={onCancel}
              className="btn btn-primary"
            >
              Done
            </button>
          )}
        </div>
      </div>
    );
  }

  return (
    <form onSubmit={handleSubmit} className="space-y-5">
      {/* Name */}
      <div>
        <label className="block text-xs font-medium text-slate-400 mb-1.5">Monitor Name</label>
        <input
          type="text"
          value={formData.name}
          onChange={(e) => setFormData({ ...formData, name: e.target.value })}
          placeholder="My Service Health"
          className="input"
        />
        {errors.name && <p className="mt-1 text-xs text-rose-400">{errors.name}</p>}
        <p className="mt-1 text-xs text-slate-500">Descriptive name for this push monitor</p>
      </div>

      {/* Expected Interval */}
      <div>
        <label className="block text-xs font-medium text-slate-400 mb-1.5">Expected Interval (seconds)</label>
        <div className="flex gap-2 mb-2">
          {[15, 30, 60, 300].map((preset) => (
            <button
              key={preset}
              type="button"
              onClick={() => setFormData({ ...formData, expected_interval_seconds: preset })}
              className={`btn btn-filter ${formData.expected_interval_seconds === preset ? 'is-active' : ''}`}
            >
              {preset >= 60 ? `${preset / 60}m` : `${preset}s`}
            </button>
          ))}
        </div>
        <input
          type="number"
          value={formData.expected_interval_seconds}
          onChange={(e) => setFormData({ ...formData, expected_interval_seconds: parseInt(e.target.value) || 60 })}
          min={5}
          step={5}
          className="input"
        />
        {errors.expected_interval_seconds && <p className="mt-1 text-xs text-rose-400">{errors.expected_interval_seconds}</p>}
        <p className="mt-1 text-xs text-slate-500">How often your service will send a push</p>
      </div>

      {/* Grace Period */}
      <div>
        <label className="block text-xs font-medium text-slate-400 mb-1.5">Grace Period (seconds)</label>
        <input
          type="number"
          value={formData.grace_period_seconds}
          onChange={(e) => setFormData({ ...formData, grace_period_seconds: parseInt(e.target.value) || 0 })}
          min={0}
          step={5}
          className="input"
        />
        {errors.grace_period_seconds && <p className="mt-1 text-xs text-rose-400">{errors.grace_period_seconds}</p>}
        <p className="mt-1 text-xs text-slate-500">
          Extra buffer before marking as down (debounce). Total window: {formData.expected_interval_seconds + formData.grace_period_seconds}s
        </p>
      </div>

      {/* Alert Policies */}
      <div>
        <label className="block text-xs font-medium text-slate-400 mb-1.5">Alert Policies</label>
        <div className="space-y-2">
          {alertPolicies.length === 0 ? (
            <div className="text-sm text-slate-500">No alert policies configured.</div>
          ) : (
            alertPolicies.map((policy) => {
              const checked = formData.alert_policy_ids.includes(policy.id);
              return (
                <label key={policy.id} className="flex items-center gap-2 text-sm text-slate-300">
                  <input
                    type="checkbox"
                    checked={checked}
                    onChange={(e) => {
                      const next = e.target.checked
                        ? [...formData.alert_policy_ids, policy.id]
                        : formData.alert_policy_ids.filter((id) => id !== policy.id);
                      setFormData({ ...formData, alert_policy_ids: next });
                    }}
                  />
                  {policy.name}
                </label>
              );
            })
          )}
        </div>
      </div>

      {/* Tags */}
      <div>
        <label className="block text-xs font-medium text-slate-400 mb-1.5">Tags</label>
        <input
          type="text"
          value={formData.tags}
          onChange={(e) => setFormData({ ...formData, tags: e.target.value })}
          placeholder="production, backend, critical (comma-separated)"
          className="input"
        />
      </div>

      {/* Enabled Toggle */}
      <div className="flex items-center justify-between rounded-lg border border-white/[0.08] bg-slate-800/30 px-4 py-3">
        <div>
          <p className="text-sm font-medium text-white">Monitor Enabled</p>
          <p className="text-xs text-slate-500">Accept pushes and track uptime</p>
        </div>
        <button
          type="button"
          onClick={() => setFormData({ ...formData, enabled: !formData.enabled })}
          className={`relative h-5 w-9 rounded-full transition-colors ${
            formData.enabled ? 'bg-cyan-500' : 'bg-slate-700'
          }`}
        >
          <span className={`absolute top-0.5 left-0.5 h-4 w-4 rounded-full bg-white transition-transform ${
            formData.enabled ? 'translate-x-4' : ''
          }`} />
        </button>
      </div>

      {/* Actions */}
      <div className="flex items-center justify-end gap-3 pt-4 border-t border-white/[0.06]">
        {onCancel && (
          <button
            type="button"
            onClick={onCancel}
            disabled={loading}
            className="btn btn-secondary disabled:opacity-50 disabled:cursor-not-allowed"
          >
            Cancel
          </button>
        )}
        <button
          type="submit"
          disabled={loading}
          className="btn btn-primary disabled:opacity-50 disabled:cursor-not-allowed"
        >
          {loading ? 'Saving...' : isEditMode ? 'Save Changes' : 'Create Push Monitor'}
        </button>
      </div>

      {/* Webhook Link for existing monitors */}
      {isEditMode && monitor && (
        <div className="pt-4 border-t border-white/[0.06]">
          <button
            type="button"
            onClick={() => setShowWebhookInfo(true)}
            className="btn btn-secondary w-full"
          >
            <svg className="h-4 w-4" fill="none" viewBox="0 0 24 24" stroke="currentColor">
              <path strokeLinecap="round" strokeLinejoin="round" strokeWidth={2} d="M13.828 10.172a4 4 0 00-5.656 0l-4 4a4 4 0 105.656 5.656l1.102-1.101m-.758-4.899a4 4 0 005.656 0l4-4a4 4 0 00-5.656-5.656l-1.1 1.1" />
            </svg>
            View Webhook URL &amp; Instructions
          </button>
        </div>
      )}
    </form>
  );
}
