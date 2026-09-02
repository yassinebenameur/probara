'use client';

import { useState } from 'react';
import {
  Monitor,
  CreateMonitorRequest,
  UpdateMonitorRequest,
  TCPMonitorConfig,
  DependencySuppression,
  NotificationMode,
  ChannelAssignment,
} from '@/lib/types';
import FormField from '@/components/ui/FormField';
import FormSection from '@/components/ui/FormSection';
import FormActions from '@/components/ui/FormActions';
import { AlertingSection } from './AlertingSection';
import { LocationsSection } from './LocationsSection';

interface TcpFormProps {
  monitor?: Monitor;
  initialData?: CreateMonitorRequest;
  onSubmit: (data: CreateMonitorRequest | UpdateMonitorRequest) => Promise<void>;
  onCancel?: () => void;
  loading?: boolean;
}

export default function TcpForm({
  monitor,
  initialData,
  onSubmit,
  onCancel,
  loading = false,
}: TcpFormProps) {
  const isEditMode = Boolean(monitor);
  const initialTcpConfig =
    !isEditMode && initialData?.type === 'tcp' ? (initialData.config as TCPMonitorConfig) : undefined;
  const existingConfig = monitor && monitor.type === 'tcp' ? (monitor.config as TCPMonitorConfig) : undefined;

  const [formData, setFormData] = useState({
    name: monitor?.name || initialData?.name || '',
    host: existingConfig?.host ?? initialTcpConfig?.host ?? '',
    port: existingConfig?.port ?? initialTcpConfig?.port ?? 0,
    use_tls: existingConfig?.use_tls ?? initialTcpConfig?.use_tls ?? false,
    tls_skip_verify: existingConfig?.tls_skip_verify ?? initialTcpConfig?.tls_skip_verify ?? false,
    interval_seconds: monitor?.interval_seconds || initialData?.interval_seconds || 60,
    timeout_seconds: monitor?.timeout_seconds || initialData?.timeout_seconds || 10,
    enabled: monitor?.enabled ?? initialData?.enabled ?? true,
    tags: monitor?.tags?.join(', ') || (initialData?.tags || []).join(', '),
    consecutive_failures_threshold: monitor?.consecutive_failures_threshold ?? 2,
    notification_mode: (monitor?.notification_mode ?? 'default') as NotificationMode,
    dependency_suppression: (monitor?.dependency_suppression ?? 'inherit') as DependencySuppression,
    notification_channels: monitor?.notification_channels ?? [] as ChannelAssignment[],
    location_ids: monitor?.location_ids ?? initialData?.location_ids ?? ([] as string[]),
    location_quorum: monitor?.location_quorum ?? initialData?.location_quorum ?? 1,
  });

  const [errors, setErrors] = useState<Record<string, string>>({});

  const handleSubmit = async (e: React.FormEvent) => {
    e.preventDefault();
    setErrors({});

    const newErrors: Record<string, string> = {};
    if (!formData.name.trim()) newErrors.name = 'Name is required';
    if (!formData.host.trim()) newErrors.host = 'Host is required';
    if (formData.port < 1 || formData.port > 65535) newErrors.port = 'Port must be between 1 and 65535';
    if (formData.timeout_seconds >= formData.interval_seconds) {
      newErrors.timeout_seconds = 'Timeout must be less than interval';
    }

    if (Object.keys(newErrors).length > 0) {
      setErrors(newErrors);
      return;
    }

    const config: TCPMonitorConfig = {
      host: formData.host.trim(),
      port: formData.port,
      use_tls: formData.use_tls,
    };

    if (formData.use_tls && formData.tls_skip_verify) {
      config.tls_skip_verify = true;
    }

    const requestData: CreateMonitorRequest | UpdateMonitorRequest = {
      name: formData.name.trim(),
      type: 'tcp',
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
      requestData.tags = formData.tags.split(',').map(t => t.trim()).filter(t => t);
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
            placeholder="Postgres port reachability"
            className="input"
          />
        </FormField>

        <div className="grid grid-cols-3 gap-4">
          <div className="col-span-2">
            <FormField label="Host" required error={errors.host} description="Hostname or IP address to connect to">
              <input
                type="text"
                value={formData.host}
                onChange={(e) => setFormData({ ...formData, host: e.target.value })}
                placeholder="db.internal.example.com"
                className="input"
              />
            </FormField>
          </div>

          <FormField label="Port" required error={errors.port} description="TCP port, e.g. 5432">
            <input
              type="number"
              value={formData.port || ''}
              onChange={(e) => setFormData({ ...formData, port: parseInt(e.target.value, 10) || 0 })}
              min={1}
              max={65535}
              placeholder="5432"
              className="input"
            />
          </FormField>
        </div>

        <FormField label="TLS handshake" description="Also complete a TLS handshake after connecting">
          <div className="flex h-10 items-center justify-between rounded-lg border border-white/[0.06] bg-slate-900/40 px-3">
            <span className={`text-sm ${formData.use_tls ? 'text-cyan-300' : 'text-slate-400'}`}>
              {formData.use_tls ? 'Enabled' : 'Disabled'}
            </span>
            <button
              type="button"
              onClick={() => setFormData({ ...formData, use_tls: !formData.use_tls })}
              className={`relative h-5 w-9 rounded-full transition-colors ${
                formData.use_tls ? 'bg-cyan-500' : 'bg-slate-700'
              }`}
              aria-pressed={formData.use_tls}
              aria-label="Toggle TLS handshake"
            >
              <span
                className={`absolute left-0.5 top-0.5 h-4 w-4 rounded-full bg-white transition-transform ${
                  formData.use_tls ? 'translate-x-4' : ''
                }`}
              />
            </button>
          </div>
        </FormField>

        {formData.use_tls && (
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

          <FormField
            label="Timeout (seconds)"
            error={errors.timeout_seconds}
            description="Must be less than interval"
          >
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
        <FormField label="Tags" description="Comma-separated, e.g. production, network, critical">
          <input
            type="text"
            value={formData.tags}
            onChange={(e) => setFormData({ ...formData, tags: e.target.value })}
            placeholder="production, network, critical"
            className="input"
          />
        </FormField>

        <div className="flex items-center justify-between rounded-lg border border-white/[0.06] bg-slate-900/40 px-4 py-3">
          <div>
            <p className="text-sm font-medium text-white">Monitor enabled</p>
            <p className="text-xs text-slate-500">Run TCP connect checks on schedule</p>
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
          formData.host.trim() && formData.port
            ? `Every ${formData.interval_seconds}s · TCP connect ${formData.host.trim()}:${formData.port}${formData.use_tls ? ' (TLS)' : ''} · down after ${formData.consecutive_failures_threshold} failed check${formData.consecutive_failures_threshold === 1 ? '' : 's'}`
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
