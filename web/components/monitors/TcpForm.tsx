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
import Switch from '@/components/ui/Switch';

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

        <div className="grid grid-cols-1 gap-4 sm:grid-cols-3">
          <div className="sm:col-span-2">
            <FormField label="Host" required error={errors.host}>
              <input
                type="text"
                value={formData.host}
                onChange={(e) => setFormData({ ...formData, host: e.target.value })}
                placeholder="db.internal.example.com"
                className="input"
              />
              <p className="mt-1 text-xs text-slate-500">Hostname or IP address to connect to</p>
            </FormField>
          </div>

          <FormField label="Port" required error={errors.port}>
            <input
              type="number"
              value={formData.port || ''}
              onChange={(e) => setFormData({ ...formData, port: parseInt(e.target.value, 10) || 0 })}
              min={1}
              max={65535}
              placeholder="5432"
              className="input"
            />
            <p className="mt-1 text-xs text-slate-500">TCP port, e.g. 5432</p>
          </FormField>
        </div>

        <div className="flex items-center justify-between gap-4">
          <div className="min-w-0">
            <p className="text-sm font-medium text-slate-200">TLS handshake</p>
            <p className="text-xs text-slate-500">Also complete a TLS handshake after connecting</p>
          </div>
          <Switch checked={formData.use_tls} onChange={() => setFormData({ ...formData, use_tls: !formData.use_tls })} label="Toggle TLS handshake" />
        </div>

        {formData.use_tls && (
          <div className="flex items-center justify-between gap-4">
            <div className="min-w-0">
              <p className="text-sm font-medium text-slate-200">Skip certificate verification</p>
              <p className="text-xs text-slate-500">Accept self-signed or mismatched certificates</p>
            </div>
            <Switch checked={formData.tls_skip_verify} onChange={() => setFormData({ ...formData, tls_skip_verify: !formData.tls_skip_verify })} label="Toggle certificate verification" tone="warning" />
          </div>
        )}
      </FormSection>

      <FormSection title="Schedule">
        <div className="grid grid-cols-1 gap-4 sm:grid-cols-2">
          <FormField label="Interval (seconds)">
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
          >
            <input
              type="number"
              value={formData.timeout_seconds}
              onChange={(e) => setFormData({ ...formData, timeout_seconds: parseInt(e.target.value, 10) || 10 })}
              min={1}
              className="input"
            />
            <p className="mt-1 text-xs text-slate-500">Must be less than interval</p>
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
          hasDependencies={(monitor?.depends_on_ids?.length ?? 0) > 0}
          onDependencySuppressionChange={(d) => setFormData({ ...formData, dependency_suppression: d })}
          customChannels={formData.notification_channels}
          onCustomChannelsChange={(next) => setFormData({ ...formData, notification_channels: next })}
        />
      </FormSection>

      <FormSection title="Meta">
        <FormField label="Tags">
          <input
            type="text"
            value={formData.tags}
            onChange={(e) => setFormData({ ...formData, tags: e.target.value })}
            placeholder="production, network, critical"
            className="input"
          />
          <p className="mt-1 text-xs text-slate-500">Comma-separated tags for filtering</p>
        </FormField>

        <div className="flex items-center justify-between gap-4">
          <div className="min-w-0">
            <p className="text-sm font-medium text-slate-200">Monitor enabled</p>
            <p className="text-xs text-slate-500">Run TCP connect checks on schedule</p>
          </div>
          <Switch checked={formData.enabled} onChange={() => setFormData({ ...formData, enabled: !formData.enabled })} label="Toggle monitor enabled" />
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
          label: loading ? 'Saving…' : isEditMode ? 'Save changes' : 'Create monitor',
          loading,
          disabled: loading,
          type: 'submit',
        }}
      />
    </form>
  );
}
