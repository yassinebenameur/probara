'use client';

import { useState } from 'react';
import {
  Monitor,
  CreateMonitorRequest,
  UpdateMonitorRequest,
  WebSocketMonitorConfig,
  DependencySuppression,
  NotificationMode,
  ChannelAssignment,
} from '@/lib/types';
import { Plus, X } from 'lucide-react';
import FormField from '@/components/ui/FormField';
import FormSection from '@/components/ui/FormSection';
import FormActions from '@/components/ui/FormActions';
import { AlertingSection } from './AlertingSection';
import { LocationsSection } from './LocationsSection';

interface WebsocketFormProps {
  monitor?: Monitor;
  initialData?: CreateMonitorRequest;
  onSubmit: (data: CreateMonitorRequest | UpdateMonitorRequest) => Promise<void>;
  onCancel?: () => void;
  loading?: boolean;
}

type HeaderRow = { key: string; value: string; hasStoredValue?: boolean; originalKey?: string };

function headersToRows(headers?: Record<string, string>): HeaderRow[] {
  return Object.entries(headers || {}).map(([key, value]) => ({
    key,
    value: value === '***' ? '' : value,
    hasStoredValue: value === '***',
    originalKey: value === '***' ? key : undefined,
  }));
}

export default function WebsocketForm({
  monitor,
  initialData,
  onSubmit,
  onCancel,
  loading = false,
}: WebsocketFormProps) {
  const isEditMode = Boolean(monitor);
  const initialWSConfig =
    !isEditMode && initialData?.type === 'websocket' ? (initialData.config as WebSocketMonitorConfig) : undefined;
  const existingConfig = monitor && monitor.type === 'websocket' ? (monitor.config as WebSocketMonitorConfig) : undefined;
  const sourceConfig = existingConfig ?? initialWSConfig;

  const [formData, setFormData] = useState({
    name: monitor?.name || initialData?.name || '',
    url: sourceConfig?.url ?? '',
    headers: headersToRows(sourceConfig?.headers),
    tls_skip_verify: sourceConfig?.tls_skip_verify ?? false,
    send_message: sourceConfig?.send_message ?? '',
    expected_substring: sourceConfig?.expected_substring ?? '',
    warn_latency_ms: sourceConfig?.warn_latency_ms ? String(sourceConfig.warn_latency_ms) : '',
    max_latency_ms: sourceConfig?.max_latency_ms ? String(sourceConfig.max_latency_ms) : '',
    interval_seconds: monitor?.interval_seconds || initialData?.interval_seconds || 60,
    timeout_seconds: monitor?.timeout_seconds || initialData?.timeout_seconds || 10,
    enabled: monitor?.enabled ?? initialData?.enabled ?? true,
    tags: monitor?.tags?.join(', ') || (initialData?.tags || []).join(', '),
    consecutive_failures_threshold: monitor?.consecutive_failures_threshold ?? 2,
    notification_mode: (monitor?.notification_mode ?? 'default') as NotificationMode,
    dependency_suppression: (monitor?.dependency_suppression ?? 'inherit') as DependencySuppression,
    notification_channels: monitor?.notification_channels ?? ([] as ChannelAssignment[]),
    location_ids: monitor?.location_ids ?? initialData?.location_ids ?? ([] as string[]),
    location_quorum: monitor?.location_quorum ?? initialData?.location_quorum ?? 1,
  });

  const [errors, setErrors] = useState<Record<string, string>>({});

  const isSecure = formData.url.trim().startsWith('wss://');

  const handleSubmit = async (e: React.FormEvent) => {
    e.preventDefault();
    setErrors({});

    const newErrors: Record<string, string> = {};
    if (!formData.name.trim()) newErrors.name = 'Name is required';
    const url = formData.url.trim();
    if (!url) {
      newErrors.url = 'URL is required';
    } else if (!url.startsWith('ws://') && !url.startsWith('wss://')) {
      newErrors.url = 'URL must start with ws:// or wss://';
    }
    for (const row of formData.headers) {
      if ((row.key.trim() === '') !== (row.value.trim() === '' && !row.hasStoredValue)) {
        newErrors.headers = 'Headers must have both a name and a value';
        break;
      }
    }
    const maxLatency = formData.max_latency_ms.trim() ? parseInt(formData.max_latency_ms, 10) : undefined;
    const warnLatency = formData.warn_latency_ms.trim() ? parseInt(formData.warn_latency_ms, 10) : undefined;
    if (formData.max_latency_ms.trim() && (!Number.isFinite(maxLatency) || (maxLatency as number) <= 0)) {
      newErrors.max_latency_ms = 'Must be a positive number of milliseconds';
    }
    if (formData.warn_latency_ms.trim() && (!Number.isFinite(warnLatency) || (warnLatency as number) <= 0)) {
      newErrors.warn_latency_ms = 'Must be a positive number of milliseconds';
    }
    if (maxLatency && warnLatency && warnLatency >= maxLatency) {
      newErrors.warn_latency_ms = 'Must be lower than max latency';
    }
    if (formData.timeout_seconds >= formData.interval_seconds) {
      newErrors.timeout_seconds = 'Timeout must be less than interval';
    }

    if (Object.keys(newErrors).length > 0) {
      setErrors(newErrors);
      return;
    }

    const config: WebSocketMonitorConfig = { url };
    const headers: Record<string, string> = {};
    for (const row of formData.headers) {
      if (row.key.trim()) headers[row.key.trim()] = row.value || (row.hasStoredValue ? '***' : '');
    }
    // On edit, always submit the object: an absent `headers` field means "keep
    // the stored headers" server-side, so removing every row has to send an
    // empty object to clear them.
    if (Object.keys(headers).length > 0 || isEditMode) config.headers = headers;
    if (isSecure && formData.tls_skip_verify) config.tls_skip_verify = true;
    if (formData.send_message) config.send_message = formData.send_message;
    if (formData.expected_substring) config.expected_substring = formData.expected_substring;
    if (maxLatency) config.max_latency_ms = maxLatency;
    if (warnLatency) config.warn_latency_ms = warnLatency;

    const requestData: CreateMonitorRequest | UpdateMonitorRequest = {
      name: formData.name.trim(),
      type: 'websocket',
      config,
      interval_seconds: formData.interval_seconds,
      timeout_seconds: formData.timeout_seconds,
      enabled: formData.enabled,
    };

    requestData.consecutive_failures_threshold = formData.consecutive_failures_threshold;
    requestData.notification_mode = formData.notification_mode;
    requestData.dependency_suppression = formData.dependency_suppression;
    requestData.notification_channels = formData.notification_mode === 'custom' ? formData.notification_channels : [];
    if (formData.tags.trim()) {
      requestData.tags = formData.tags.split(',').map((t) => t.trim()).filter((t) => t);
    }
    requestData.location_ids = formData.location_ids;
    requestData.location_quorum =
      formData.location_ids.length >= 2
        ? Math.min(formData.location_quorum, formData.location_ids.length)
        : 1;

    await onSubmit(requestData);
  };

  return (
    <form onSubmit={handleSubmit} className="space-y-5">
      <FormSection title="Target">
        <FormField label="Monitor name" required error={errors.name}>
          <input
            type="text"
            value={formData.name}
            onChange={(e) => setFormData({ ...formData, name: e.target.value })}
            placeholder="Live updates socket"
            className="input"
          />
        </FormField>

        <FormField label="URL" required error={errors.url} description="ws:// or wss:// endpoint to connect to">
          <input
            type="text"
            value={formData.url}
            onChange={(e) => setFormData({ ...formData, url: e.target.value })}
            placeholder="wss://api.example.com/socket"
            className="input font-mono text-xs"
          />
        </FormField>

        <FormField
          label="Handshake headers (optional)"
          error={errors.headers}
          description="Sent with the upgrade request — e.g. Authorization or Origin"
        >
          <div className="space-y-2">
            {formData.headers.map((row, i) => (
              <div key={i} className="flex items-center gap-2">
                <input
                  type="text"
                  value={row.key}
                  onChange={(e) => {
                    const headers = [...formData.headers];
                    const current = headers[i];
                    const keepsStoredValue = Boolean(
                      current.hasStoredValue &&
                      current.originalKey &&
                      current.originalKey.toLowerCase() === e.target.value.trim().toLowerCase()
                    );
                    headers[i] = { ...current, key: e.target.value, hasStoredValue: keepsStoredValue };
                    setFormData({ ...formData, headers });
                  }}
                  placeholder="Authorization"
                  aria-label={`Header ${i + 1} name`}
                  className="input flex-1"
                />
                <input
                  type="password"
                  value={row.value}
                  onChange={(e) => {
                    const headers = [...formData.headers];
                    headers[i] = { ...headers[i], value: e.target.value, hasStoredValue: false };
                    setFormData({ ...formData, headers });
                  }}
                  placeholder={row.hasStoredValue ? 'Stored value (leave blank to keep)' : 'Header value'}
                  aria-label={`Header ${i + 1} value`}
                  className="input flex-1"
                />
                <button
                  type="button"
                  onClick={() => setFormData({ ...formData, headers: formData.headers.filter((_, j) => j !== i) })}
                  className="rounded p-1.5 text-slate-500 transition-colors hover:text-rose-400"
                  aria-label={`Remove header ${i + 1}`}
                >
                  <X className="h-3.5 w-3.5" strokeWidth={1.75} />
                </button>
              </div>
            ))}
            <button
              type="button"
              onClick={() => setFormData({ ...formData, headers: [...formData.headers, { key: '', value: '', hasStoredValue: false }] })}
              className="inline-flex items-center gap-1.5 text-xs font-medium text-cyan-400 transition-colors hover:text-cyan-300"
            >
              <Plus className="h-3.5 w-3.5" strokeWidth={1.75} />
              Add header
            </button>
          </div>
        </FormField>

        {isSecure && (
          <FormField label="Skip certificate verification" description="Accept self-signed or mismatched certificates">
            <div className="flex h-10 items-center justify-between rounded-lg border border-white/[0.06] bg-slate-900/40 px-3">
              <span className={`text-sm ${formData.tls_skip_verify ? 'text-amber-300' : 'text-slate-400'}`}>
                {formData.tls_skip_verify ? 'Verification off' : 'Verification on'}
              </span>
              <button
                type="button"
                onClick={() => setFormData({ ...formData, tls_skip_verify: !formData.tls_skip_verify })}
                className={`relative h-5 w-9 rounded-full transition-colors ${
                  formData.tls_skip_verify ? 'bg-amber-500' : 'bg-slate-700'
                }`}
                aria-pressed={formData.tls_skip_verify}
                aria-label="Toggle certificate verification"
              >
                <span
                  className={`absolute left-0.5 top-0.5 h-4 w-4 rounded-full bg-white transition-transform ${
                    formData.tls_skip_verify ? 'translate-x-4' : ''
                  }`}
                />
              </button>
            </div>
          </FormField>
        )}
      </FormSection>

      <FormSection title="Checks">
        <div className="grid grid-cols-1 gap-4 sm:grid-cols-2">
          <FormField label="Send message (optional)" description="Text frame written after the handshake">
            <input
              type="text"
              value={formData.send_message}
              onChange={(e) => setFormData({ ...formData, send_message: e.target.value })}
              placeholder='{"type":"ping"}'
              className="input font-mono text-xs"
            />
          </FormField>
          <FormField
            label="Expected reply contains (optional)"
            description="Fail unless the first reply contains this text"
          >
            <input
              type="text"
              value={formData.expected_substring}
              onChange={(e) => setFormData({ ...formData, expected_substring: e.target.value })}
              placeholder="pong"
              className="input font-mono text-xs"
            />
          </FormField>
        </div>
        <div className="grid grid-cols-1 gap-4 sm:grid-cols-2">
          <FormField
            label="Warn latency (ms, optional)"
            error={errors.warn_latency_ms}
            description="Flag the check with a warning above this — without failing it"
          >
            <input
              type="number"
              value={formData.warn_latency_ms}
              onChange={(e) => setFormData({ ...formData, warn_latency_ms: e.target.value })}
              min={1}
              placeholder="e.g. 250"
              className="input"
            />
          </FormField>
          <FormField
            label="Max latency (ms, optional)"
            error={errors.max_latency_ms}
            description="Fail the check if the round trip takes longer than this"
          >
            <input
              type="number"
              value={formData.max_latency_ms}
              onChange={(e) => setFormData({ ...formData, max_latency_ms: e.target.value })}
              min={1}
              placeholder="e.g. 1000"
              className="input"
            />
          </FormField>
        </div>
      </FormSection>

      <FormSection title="Schedule">
        <div className="grid grid-cols-2 gap-4">
          <FormField label="Interval (seconds)" description="How often to check">
            <input
              type="number"
              value={formData.interval_seconds}
              onChange={(e) => setFormData({ ...formData, interval_seconds: parseInt(e.target.value, 10) || 60 })}
              min={10}
              step={5}
              className="input"
            />
          </FormField>
          <FormField label="Timeout (seconds)" error={errors.timeout_seconds} description="Must be less than interval">
            <input
              type="number"
              value={formData.timeout_seconds}
              onChange={(e) => setFormData({ ...formData, timeout_seconds: parseInt(e.target.value, 10) || 10 })}
              min={1}
              className="input"
            />
          </FormField>
        </div>
      </FormSection>

      <FormSection
        title="Locations"
        summary={formData.location_ids.length > 0 ? `${formData.location_ids.length} selected` : 'Default fleet'}
        collapsible
        defaultOpen={formData.location_ids.length > 0}
      >
        <LocationsSection
          selectedIds={formData.location_ids}
          onChange={(ids) => setFormData({ ...formData, location_ids: ids })}
          quorum={formData.location_quorum}
          onQuorumChange={(n) => setFormData({ ...formData, location_quorum: n })}
        />
      </FormSection>

      <FormSection title="Alerting">
        <AlertingSection
          isGroup={false}
          intervalSeconds={formData.interval_seconds}
          threshold={formData.consecutive_failures_threshold}
          onThresholdChange={(n) => setFormData({ ...formData, consecutive_failures_threshold: n })}
          mode={formData.notification_mode}
          onModeChange={(m) => setFormData({ ...formData, notification_mode: m })}
          dependencySuppression={formData.dependency_suppression}
          onDependencySuppressionChange={(d) => setFormData({ ...formData, dependency_suppression: d })}
          customChannels={formData.notification_channels}
          onCustomChannelsChange={(next) => setFormData({ ...formData, notification_channels: next })}
        />
      </FormSection>

      <FormSection title="Meta">
        <FormField label="Tags" description="Comma-separated, e.g. production, realtime, critical">
          <input
            type="text"
            value={formData.tags}
            onChange={(e) => setFormData({ ...formData, tags: e.target.value })}
            placeholder="production, realtime, critical"
            className="input"
          />
        </FormField>

        <div className="flex items-center justify-between rounded-lg border border-white/[0.06] bg-slate-900/40 px-4 py-3">
          <div>
            <p className="text-sm font-medium text-white">Monitor enabled</p>
            <p className="text-xs text-slate-500">Run WebSocket checks on schedule</p>
          </div>
          <button
            type="button"
            onClick={() => setFormData({ ...formData, enabled: !formData.enabled })}
            className={`relative h-5 w-9 rounded-full transition-colors ${
              formData.enabled ? 'bg-cyan-500' : 'bg-slate-700'
            }`}
            aria-pressed={formData.enabled}
            aria-label="Toggle monitor enabled"
          >
            <span
              className={`absolute left-0.5 top-0.5 h-4 w-4 rounded-full bg-white transition-transform ${
                formData.enabled ? 'translate-x-4' : ''
              }`}
            />
          </button>
        </div>
      </FormSection>

      <FormActions
        middle={
          formData.url.trim()
            ? `Every ${formData.interval_seconds}s · WebSocket handshake ${formData.url.trim()}${
                formData.expected_substring ? ' + reply assertion' : ''
              } · down after ${formData.consecutive_failures_threshold} failed check${
                formData.consecutive_failures_threshold === 1 ? '' : 's'
              }`
            : undefined
        }
        cancel={onCancel ? { label: 'Cancel', onClick: onCancel, disabled: loading } : undefined}
        submit={{
          label: loading ? 'Saving…' : isEditMode ? 'Update monitor' : 'Create monitor',
          loading,
          disabled: loading,
          type: 'submit',
        }}
      />
    </form>
  );
}
