'use client';

import { useRef, useState } from 'react';
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
import Button from '@/components/ui/Button';
import IconButton from '@/components/ui/IconButton';
import { AlertingSection } from './AlertingSection';
import ScheduleSection from './ScheduleSection';
import DependenciesField from './DependenciesField';
import { FieldHint, FormPanel, LabelsSection, PlanSummary, PlanTarget, ToggleRow, downAfterLabel, everyLabel, revealFirstError, reportConstraintViolation, useClearErrorsOnEdit, useFollowEnabled } from './form-kit';

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
    depends_on_ids: monitor?.depends_on_ids ?? initialData?.depends_on_ids ?? ([] as string[]),
  });

  const [errors, setErrors] = useState<Record<string, string>>({});
  useClearErrorsOnEdit(formData, setErrors);
  useFollowEnabled(monitor?.enabled, setFormData);
  const formRef = useRef<HTMLFormElement>(null);

  const isSecure = formData.url.trim().startsWith('wss://');

  const handleSubmit = async (e: React.FormEvent) => {
    e.preventDefault();
    if (reportConstraintViolation(formRef.current)) return;
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
      revealFirstError(formRef.current);
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
    requestData.depends_on_ids = formData.depends_on_ids;
    requestData.location_ids = formData.location_ids;
    requestData.location_quorum =
      formData.location_ids.length >= 2
        ? Math.min(formData.location_quorum, formData.location_ids.length)
        : 1;

    await onSubmit(requestData);
  };

  return (
    <form ref={formRef} onSubmit={handleSubmit} noValidate className="space-y-5">
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

        <FormField label="URL" required error={errors.url}>
          {(control) => (
            <>
              <input
                {...control}
                type="text"
                value={formData.url}
                onChange={(e) => setFormData({ ...formData, url: e.target.value })}
                placeholder="wss://api.example.com/socket"
                className="input font-mono"
              />
              <FieldHint>ws:// or wss:// endpoint to connect to</FieldHint>
            </>
          )}
        </FormField>

        <FormField
          label="Handshake headers"
          error={errors.headers}
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
                <IconButton
                  icon={<X strokeWidth={1.75} />}
                  label={`Remove header ${i + 1}`}
                  onClick={() => setFormData({ ...formData, headers: formData.headers.filter((_, j) => j !== i) })}
                  danger
                />
              </div>
            ))}
            <Button
              type="button"
              variant="ghost"
              size="xs"
              icon={<Plus strokeWidth={1.75} />}
              onClick={() => setFormData({ ...formData, headers: [...formData.headers, { key: '', value: '', hasStoredValue: false }] })}
            >
              Add header
            </Button>
          </div>
          <p className="mt-2 text-xs text-slate-500">Optional. Sent with the upgrade request, e.g. Authorization or Origin.</p>
        </FormField>

        {isSecure && (
          <ToggleRow
            title="Skip certificate verification"
            description="Accept self-signed or mismatched certificates"
            checked={formData.tls_skip_verify}
            onChange={(v) => setFormData({ ...formData, tls_skip_verify: v })}
            tone="warning"
          />
        )}
      </FormSection>

      <FormSection title="Check">
        <div className="grid grid-cols-1 gap-4 sm:grid-cols-2">
          <FormField label="Send message">
            {(control) => (
              <>
                <input
                  {...control}
                  type="text"
                  value={formData.send_message}
                  onChange={(e) => setFormData({ ...formData, send_message: e.target.value })}
                  placeholder='{"type":"ping"}'
                  className="input font-mono"
                />
                <FieldHint>Optional. Text frame written after the handshake</FieldHint>
              </>
            )}
          </FormField>
          <FormField label="Expected reply contains">
            {(control) => (
              <>
                <input
                  {...control}
                  type="text"
                  value={formData.expected_substring}
                  onChange={(e) => setFormData({ ...formData, expected_substring: e.target.value })}
                  placeholder="pong"
                  className="input font-mono"
                />
                <FieldHint>Optional. Fail unless the first reply contains this text</FieldHint>
              </>
            )}
          </FormField>
        </div>
        <div className="grid grid-cols-1 gap-4 sm:grid-cols-2">
          <FormField label="Warn latency (ms)" error={errors.warn_latency_ms}>
            {(control) => (
              <>
                <input
                  {...control}
                  type="number"
                  value={formData.warn_latency_ms}
                  onChange={(e) => setFormData({ ...formData, warn_latency_ms: e.target.value })}
                  min={1}
                  placeholder="e.g. 250"
                  className="input tabular-nums"
                />
                {!errors.warn_latency_ms && <FieldHint>Optional. Flag the check with a warning above this, without failing it</FieldHint>}
              </>
            )}
          </FormField>
          <FormField label="Max latency (ms)" error={errors.max_latency_ms}>
            {(control) => (
              <>
                <input
                  {...control}
                  type="number"
                  value={formData.max_latency_ms}
                  onChange={(e) => setFormData({ ...formData, max_latency_ms: e.target.value })}
                  min={1}
                  placeholder="e.g. 1000"
                  className="input tabular-nums"
                />
                {!errors.max_latency_ms && <FieldHint>Optional. Fail the check if the round trip takes longer than this</FieldHint>}
              </>
            )}
          </FormField>
        </div>
      </FormSection>

      <ScheduleSection
        intervalSeconds={formData.interval_seconds}
        onIntervalChange={(n) => setFormData((prev) => ({ ...prev, interval_seconds: n }))}
        timeoutSeconds={formData.timeout_seconds}
        onTimeoutChange={(n) => setFormData((prev) => ({ ...prev, timeout_seconds: n }))}
        timeoutError={errors.timeout_seconds}
        locations={{
          selectedIds: formData.location_ids,
          onChange: (ids) => setFormData((prev) => ({ ...prev, location_ids: ids })),
          quorum: formData.location_quorum,
          onQuorumChange: (n) => setFormData((prev) => ({ ...prev, location_quorum: n })),
        }}
        enabled={formData.enabled}
        onEnabledChange={(v) => setFormData((prev) => ({ ...prev, enabled: v }))}
        enabledDescription="Run WebSocket checks on this schedule"
      />

      <FormPanel
        title="Alerting"
        rows={
          <DependenciesField
            selectedIds={formData.depends_on_ids}
            onChange={(ids) => setFormData((prev) => ({ ...prev, depends_on_ids: ids }))}
            excludeId={monitor?.id}
            error={errors.depends_on_ids}
          />
        }
      >
        <AlertingSection
          isGroup={false}
          intervalSeconds={formData.interval_seconds}
          threshold={formData.consecutive_failures_threshold}
          onThresholdChange={(n) => setFormData({ ...formData, consecutive_failures_threshold: n })}
          mode={formData.notification_mode}
          onModeChange={(m) => setFormData({ ...formData, notification_mode: m })}
          dependencySuppression={formData.dependency_suppression}
          hasDependencies={formData.depends_on_ids.length > 0}
          onDependencySuppressionChange={(d) => setFormData({ ...formData, dependency_suppression: d })}
          customChannels={formData.notification_channels}
          onCustomChannelsChange={(next) => setFormData({ ...formData, notification_channels: next })}
        />
      </FormPanel>

      <LabelsSection
        tags={formData.tags}
        onTagsChange={(v) => setFormData({ ...formData, tags: v })}
        placeholder="production, realtime, critical"
      />

      <FormActions
        middle={
          <PlanSummary
            errors={errors}
            parts={
              formData.url.trim()
                ? [
                    everyLabel(formData.interval_seconds),
                    <PlanTarget key="t">{formData.url.trim()}</PlanTarget>,
                    formData.expected_substring ? 'handshake and reply assertion' : 'handshake',
                    downAfterLabel(formData.consecutive_failures_threshold),
                  ]
                : []
            }
          />
        }
        cancel={onCancel ? { label: 'Cancel', onClick: onCancel, disabled: loading } : undefined}
        submit={{
          label: loading ? 'Saving…' : isEditMode ? 'Save changes' : 'Create monitor',
          loading,
          disabled: loading,
          type: 'submit',
        }}
      />
    </form>
  );
}
