'use client';

import { useState, useEffect } from 'react';
import { Info } from 'lucide-react';
import { Monitor, CreateMonitorRequest, UpdateMonitorRequest, AlertPolicy, SIPMonitorConfig } from '@/lib/types';
import { getAlertPolicies } from '@/lib/api';
import FormField from '@/components/ui/FormField';
import FormSection from '@/components/ui/FormSection';
import FormActions from '@/components/ui/FormActions';

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
  const [alertPolicies, setAlertPolicies] = useState<AlertPolicy[]>([]);
  const isEditMode = Boolean(monitor);
  const initialSipConfig =
    !isEditMode && initialData?.type === 'sip' ? (initialData.config as SIPMonitorConfig) : undefined;

  const [formData, setFormData] = useState({
    name: monitor?.name || initialData?.name || '',
    host: monitor && monitor.type === 'sip'
      ? (monitor.config as SIPMonitorConfig)?.host || ''
      : initialSipConfig?.host || '',
    port: monitor && monitor.type === 'sip'
      ? (monitor.config as SIPMonitorConfig)?.port || 5060
      : initialSipConfig?.port || 5060,
    transport: monitor && monitor.type === 'sip'
      ? (monitor.config as SIPMonitorConfig)?.transport || 'udp'
      : initialSipConfig?.transport || 'udp' as 'udp' | 'tcp',
    expected_status: monitor && monitor.type === 'sip'
      ? (monitor.config as SIPMonitorConfig)?.expected_status?.toString() || ''
      : initialSipConfig?.expected_status?.toString() || '',
    interval_seconds: monitor?.interval_seconds || initialData?.interval_seconds || 60,
    timeout_seconds: monitor?.timeout_seconds || initialData?.timeout_seconds || 10,
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
    if (!formData.host.trim()) newErrors.host = 'Host is required';
    if (formData.port < 1 || formData.port > 65535) newErrors.port = 'Port must be between 1 and 65535';
    if (formData.timeout_seconds >= formData.interval_seconds) {
      newErrors.timeout_seconds = 'Timeout must be less than interval';
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

    requestData.alert_policy_ids = formData.alert_policy_ids;
    if (formData.tags.trim()) {
      requestData.tags = formData.tags.split(',').map(t => t.trim()).filter(t => t);
    }

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
              onChange={(e) => setFormData({ ...formData, transport: e.target.value as 'udp' | 'tcp' })}
              className="input"
            >
              <option value="udp">UDP</option>
              <option value="tcp">TCP</option>
            </select>
          </FormField>
        </div>

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

      <FormSection title="Alert policies">
        {alertPolicies.length === 0 ? (
          <div className="text-sm text-slate-500">No alert policies configured.</div>
        ) : (
          <div className="space-y-2">
            {alertPolicies.map((policy) => {
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
            })}
          </div>
        )}
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
            <p className="text-xs text-slate-500">Run SIP OPTIONS checks on schedule</p>
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
            <p className="mb-1 text-xs font-medium text-white">SIP OPTIONS request</p>
            <p className="text-xs text-slate-400">
              Sends a SIP OPTIONS request to test server availability without initiating a call.
            </p>
          </div>
        </div>
      </div>

      <FormActions
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
