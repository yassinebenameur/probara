'use client';

import { useState } from 'react';
import { Info } from 'lucide-react';
import { Monitor, CreateMonitorRequest, UpdateMonitorRequest, SIPMonitorConfig, NotificationMode, ChannelAssignment } from '@/lib/types';
import FormField from '@/components/ui/FormField';
import FormSection from '@/components/ui/FormSection';
import FormActions from '@/components/ui/FormActions';
import { AlertingSection } from './AlertingSection';
import { LocationsSection } from './LocationsSection';

interface SipFormProps {
  monitor?: Monitor;
  initialData?: CreateMonitorRequest;
  onSubmit: (data: CreateMonitorRequest | UpdateMonitorRequest) => Promise<void>;
  onCancel?: () => void;
  loading?: boolean;
}

export default function SipForm({
  monitor,
  initialData,
  onSubmit,
  onCancel,
  loading = false,
}: SipFormProps) {
  const isEditMode = Boolean(monitor);
  const initialSipConfig =
    !isEditMode && initialData?.type === 'sip' ? (initialData.config as SIPMonitorConfig) : undefined;

  const editSipConfig =
    monitor && monitor.type === 'sip' ? (monitor.config as SIPMonitorConfig) : undefined;

  const [formData, setFormData] = useState({
    name: monitor?.name || initialData?.name || '',
    host: editSipConfig?.host || initialSipConfig?.host || '',
    port: editSipConfig?.port || initialSipConfig?.port || 5060,
    transport: (editSipConfig?.transport || initialSipConfig?.transport || 'udp') as 'udp' | 'tcp' | 'tls',
    method: (editSipConfig?.method || initialSipConfig?.method || 'options') as 'options' | 'register',
    username: editSipConfig?.username || initialSipConfig?.username || '',
    password: editSipConfig?.password || initialSipConfig?.password || '',
    domain: editSipConfig?.domain || initialSipConfig?.domain || '',
    tls_skip_verify: editSipConfig?.tls_skip_verify ?? initialSipConfig?.tls_skip_verify ?? false,
    expected_status: editSipConfig?.expected_status?.toString()
      || initialSipConfig?.expected_status?.toString() || '',
    interval_seconds: monitor?.interval_seconds || initialData?.interval_seconds || 60,
    timeout_seconds: monitor?.timeout_seconds || initialData?.timeout_seconds || 10,
    enabled: monitor?.enabled ?? initialData?.enabled ?? true,
    tags: monitor?.tags?.join(', ') || (initialData?.tags || []).join(', '),
    consecutive_failures_threshold: monitor?.consecutive_failures_threshold ?? 2,
    notification_mode: (monitor?.notification_mode ?? 'default') as NotificationMode,
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
    if ((formData.username.trim() === '') !== (formData.password === '')) {
      newErrors.username = 'Username and password must be provided together';
    }

    if (Object.keys(newErrors).length > 0) {
      setErrors(newErrors);
      return;
    }

    const config: SIPMonitorConfig = {
      host: formData.host.trim(),
      port: formData.port,
      transport: formData.transport,
    };

    if (formData.method !== 'options') config.method = formData.method;
    if (formData.username.trim()) {
      config.username = formData.username.trim();
      config.password = formData.password;
    } else if (editSipConfig?.password) {
      // Auth dropped: an omitted secret field means "keep the stored value"
      // server-side, so clearing it takes an explicit empty string.
      config.password = '';
    }
    if (formData.domain.trim()) config.domain = formData.domain.trim();
    if (formData.transport === 'tls' && formData.tls_skip_verify) config.tls_skip_verify = true;

    if (formData.expected_status) {
      const status = parseInt(formData.expected_status);
      if (!isNaN(status)) config.expected_status = status;
    }

    const requestData: CreateMonitorRequest | UpdateMonitorRequest = {
      name: formData.name.trim(),
      type: 'sip',
      config,
      interval_seconds: formData.interval_seconds,
      timeout_seconds: formData.timeout_seconds,
      enabled: formData.enabled,
    };

    requestData.consecutive_failures_threshold = formData.consecutive_failures_threshold;
    requestData.notification_mode = formData.notification_mode;
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
      <FormSection title="Endpoint">
        <FormField label="Monitor name" required error={errors.name}>
          <input
            type="text"
            value={formData.name}
            onChange={(e) => setFormData({ ...formData, name: e.target.value })}
            placeholder="My SIP server"
            className="input"
          />
        </FormField>

        <FormField label="Host" required error={errors.host} description="Hostname or IP">
          <input
            type="text"
            value={formData.host}
            onChange={(e) => setFormData({ ...formData, host: e.target.value })}
            placeholder="sip.example.com"
            className="input"
          />
        </FormField>

        <div className="grid grid-cols-2 gap-4">
          <FormField label="Port" required error={errors.port} description="Default: 5060">
            <input
              type="number"
              value={formData.port}
              onChange={(e) => setFormData({ ...formData, port: parseInt(e.target.value) || 5060 })}
              min={1}
              max={65535}
              className="input"
            />
          </FormField>
          <FormField label="Transport" description="SIP transport protocol">
            <select
              value={formData.transport}
              onChange={(e) => {
                const transport = e.target.value as 'udp' | 'tcp' | 'tls';
                setFormData({
                  ...formData,
                  transport,
                  // Track the conventional default port unless the user set a custom one.
                  port: transport === 'tls' && formData.port === 5060
                    ? 5061
                    : transport !== 'tls' && formData.port === 5061
                    ? 5060
                    : formData.port,
                });
              }}
              className="input"
            >
              <option value="udp">UDP</option>
              <option value="tcp">TCP</option>
              <option value="tls">TLS</option>
            </select>
          </FormField>
        </div>

        {formData.transport === 'tls' && (
          <div className="flex items-center justify-between rounded-lg border border-white/[0.06] bg-slate-900/40 px-4 py-3">
            <div>
              <p className="text-sm font-medium text-white">Skip certificate verification</p>
              <p className="text-xs text-slate-500">For lab servers with self-signed certificates</p>
            </div>
            <button
              type="button"
              onClick={() => setFormData({ ...formData, tls_skip_verify: !formData.tls_skip_verify })}
              className={`relative h-5 w-9 rounded-full transition-colors ${
                formData.tls_skip_verify ? 'bg-cyan-500' : 'bg-slate-700'
              }`}
              aria-pressed={formData.tls_skip_verify}
              aria-label="Toggle certificate verification skip"
            >
              <span className={`absolute left-0.5 top-0.5 h-4 w-4 rounded-full bg-white transition-transform ${
                formData.tls_skip_verify ? 'translate-x-4' : ''
              }`} />
            </button>
          </div>
        )}

        <FormField label="Expected status code" description="Default: 200 OK">
          <input
            type="number"
            value={formData.expected_status}
            onChange={(e) => setFormData({ ...formData, expected_status: e.target.value })}
            placeholder="200"
            min={100}
            max={699}
            className="input"
          />
        </FormField>
      </FormSection>

      <FormSection title="Check">
        <FormField label="Method" description="What the probe proves">
          <select
            value={formData.method}
            onChange={(e) => setFormData({ ...formData, method: e.target.value as 'options' | 'register' })}
            className="input"
          >
            <option value="options">OPTIONS — server availability ping</option>
            <option value="register">REGISTER — registrar + authentication probe</option>
          </select>
        </FormField>

        {formData.method === 'register' && (
          <FormField
            label="SIP domain"
            description="Domain of the address-of-record. Defaults to the host."
          >
            <input
              type="text"
              value={formData.domain}
              onChange={(e) => setFormData({ ...formData, domain: e.target.value })}
              placeholder="example.com"
              className="input"
            />
          </FormField>
        )}

        <div className="grid grid-cols-2 gap-4">
          <FormField
            label="Username"
            error={errors.username}
            description="Digest auth user (optional)"
          >
            <input
              type="text"
              value={formData.username}
              onChange={(e) => setFormData({ ...formData, username: e.target.value })}
              placeholder="agent42"
              autoComplete="off"
              className="input"
            />
          </FormField>
          <FormField
            label="Password"
            description={isEditMode ? 'Leave *** to keep the stored password' : 'Stored encrypted'}
          >
            <input
              type="password"
              value={formData.password}
              onChange={(e) => setFormData({ ...formData, password: e.target.value })}
              autoComplete="new-password"
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
              onChange={(e) => setFormData({ ...formData, interval_seconds: parseInt(e.target.value) || 60 })}
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
              onChange={(e) => setFormData({ ...formData, timeout_seconds: parseInt(e.target.value) || 10 })}
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
          customChannels={formData.notification_channels}
          onCustomChannelsChange={(next) => setFormData({ ...formData, notification_channels: next })}
        />
      </FormSection>

      <FormSection title="Meta">
        <FormField label="Tags" description="Comma-separated, e.g. production, voip, critical">
          <input
            type="text"
            value={formData.tags}
            onChange={(e) => setFormData({ ...formData, tags: e.target.value })}
            placeholder="production, voip, critical"
            className="input"
          />
        </FormField>

        <div className="flex items-center justify-between rounded-lg border border-white/[0.06] bg-slate-900/40 px-4 py-3">
          <div>
            <p className="text-sm font-medium text-white">Monitor enabled</p>
            <p className="text-xs text-slate-500">
              Run SIP {formData.method === 'register' ? 'REGISTER' : 'OPTIONS'} checks on schedule
            </p>
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
            <span className={`absolute left-0.5 top-0.5 h-4 w-4 rounded-full bg-white transition-transform ${
              formData.enabled ? 'translate-x-4' : ''
            }`} />
          </button>
        </div>
      </FormSection>

      <div className="rounded-lg border border-cyan-500/20 bg-cyan-500/5 px-4 py-3">
        <div className="flex items-start gap-3">
          <Info className="mt-0.5 h-4 w-4 flex-shrink-0 text-cyan-300" strokeWidth={1.75} />
          <div>
            <p className="mb-1 text-xs font-medium text-white">
              {formData.method === 'register' ? 'SIP REGISTER probe' : 'SIP OPTIONS request'}
            </p>
            <p className="text-xs text-slate-400">
              {formData.method === 'register'
                ? 'Sends a query-style REGISTER (no Contact) that exercises the registrar and its digest authentication without creating or removing any bindings.'
                : 'Sends a SIP OPTIONS request to test server availability without initiating a call. Answers digest challenges when credentials are configured.'}
            </p>
          </div>
        </div>
      </div>

      <FormActions
        middle={
          formData.host.trim()
            ? `Every ${formData.interval_seconds}s · SIP ${formData.method.toUpperCase()} ${formData.host.trim()}:${formData.port} (${formData.transport.toUpperCase()}) · down after ${formData.consecutive_failures_threshold} failed check${formData.consecutive_failures_threshold === 1 ? '' : 's'}`
            : undefined
        }
        cancel={onCancel ? { label: 'Cancel', onClick: onCancel, disabled: loading } : undefined}
        submit={{
          label: loading ? 'Saving…' : isEditMode ? 'Save changes' : 'Create SIP monitor',
          loading,
          disabled: loading,
          type: 'submit',
        }}
      />
    </form>
  );
}
