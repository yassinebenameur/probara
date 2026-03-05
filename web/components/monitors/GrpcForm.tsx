'use client';

import { useState, useEffect } from 'react';
import {
  Monitor,
  CreateMonitorRequest,
  UpdateMonitorRequest,
  AlertPolicy,
  GRPCMonitorConfig,
} from '@/lib/types';
import { getAlertPolicies } from '@/lib/api';

interface GrpcFormProps {
  monitor?: Monitor;
  initialData?: CreateMonitorRequest;
  onSubmit: (data: CreateMonitorRequest | UpdateMonitorRequest) => Promise<void>;
  onCancel?: () => void;
  loading?: boolean;
}

export default function GrpcForm({
  monitor,
  initialData,
  onSubmit,
  onCancel,
  loading = false,
}: GrpcFormProps) {
  const [alertPolicies, setAlertPolicies] = useState<AlertPolicy[]>([]);
  const isEditMode = Boolean(monitor);
  const initialGrpcConfig =
    !isEditMode && initialData?.type === 'grpc' ? (initialData.config as GRPCMonitorConfig) : undefined;

  const [formData, setFormData] = useState({
    name: monitor?.name || initialData?.name || '',
    host: monitor && monitor.type === 'grpc'
      ? (monitor.config as GRPCMonitorConfig)?.host || ''
      : initialGrpcConfig?.host || '',
    port: monitor && monitor.type === 'grpc'
      ? (monitor.config as GRPCMonitorConfig)?.port || 443
      : initialGrpcConfig?.port || 443,
    service: monitor && monitor.type === 'grpc'
      ? (monitor.config as GRPCMonitorConfig)?.service || ''
      : initialGrpcConfig?.service || '',
    use_tls: monitor && monitor.type === 'grpc'
      ? (monitor.config as GRPCMonitorConfig)?.use_tls ?? true
      : initialGrpcConfig?.use_tls ?? true,
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

    const config: GRPCMonitorConfig = {
      host: formData.host.trim(),
      port: formData.port,
      use_tls: formData.use_tls,
    };

    if (formData.service.trim()) {
      config.service = formData.service.trim();
    }

    const requestData: CreateMonitorRequest | UpdateMonitorRequest = {
      name: formData.name.trim(),
      type: 'grpc',
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
      <div>
        <label className="block text-xs font-medium text-slate-400 mb-1.5">Monitor Name</label>
        <input
          type="text"
          value={formData.name}
          onChange={(e) => setFormData({ ...formData, name: e.target.value })}
          placeholder="My gRPC Service"
          className="input"
        />
        {errors.name && <p className="mt-1 text-xs text-rose-400">{errors.name}</p>}
        <p className="mt-1 text-xs text-slate-500">Descriptive name for this gRPC monitor</p>
      </div>

      <div>
        <label className="block text-xs font-medium text-slate-400 mb-1.5">Host</label>
        <input
          type="text"
          value={formData.host}
          onChange={(e) => setFormData({ ...formData, host: e.target.value })}
          placeholder="grpc.example.com"
          className="input"
        />
        {errors.host && <p className="mt-1 text-xs text-rose-400">{errors.host}</p>}
        <p className="mt-1 text-xs text-slate-500">gRPC server hostname or IP address</p>
      </div>

      <div className="grid grid-cols-2 gap-4">
        <div>
          <label className="block text-xs font-medium text-slate-400 mb-1.5">Port</label>
          <input
            type="number"
            value={formData.port}
            onChange={(e) => setFormData({ ...formData, port: parseInt(e.target.value, 10) || (formData.use_tls ? 443 : 80) })}
            min={1}
            max={65535}
            className="input"
          />
          {errors.port && <p className="mt-1 text-xs text-rose-400">{errors.port}</p>}
          <p className="mt-1 text-xs text-slate-500">Default: {formData.use_tls ? 443 : 80}</p>
        </div>

        <div>
          <label className="block text-xs font-medium text-slate-400 mb-1.5">TLS</label>
          <button
            type="button"
            onClick={() => {
              const nextUseTLS = !formData.use_tls;
              let nextPort = formData.port;
              if (formData.port === 443 && !nextUseTLS) nextPort = 80;
              if (formData.port === 80 && nextUseTLS) nextPort = 443;
              setFormData({ ...formData, use_tls: nextUseTLS, port: nextPort });
            }}
            className={`relative h-10 w-full rounded-lg border text-sm transition-colors ${
              formData.use_tls
                ? 'border-cyan-500/40 bg-cyan-500/10 text-cyan-300'
                : 'border-white/[0.08] bg-slate-800/30 text-slate-300'
            }`}
          >
            {formData.use_tls ? 'Enabled' : 'Disabled'}
          </button>
          <p className="mt-1 text-xs text-slate-500">Use TLS for gRPC connection</p>
        </div>
      </div>

      <div>
        <label className="block text-xs font-medium text-slate-400 mb-1.5">Service (optional)</label>
        <input
          type="text"
          value={formData.service}
          onChange={(e) => setFormData({ ...formData, service: e.target.value })}
          placeholder="my.package.Service"
          className="input"
        />
        <p className="mt-1 text-xs text-slate-500">Service name passed to grpc.health.v1.Health/Check</p>
      </div>

      <div className="grid grid-cols-2 gap-4">
        <div>
          <label className="block text-xs font-medium text-slate-400 mb-1.5">Interval (seconds)</label>
          <input
            type="number"
            value={formData.interval_seconds}
            onChange={(e) => setFormData({ ...formData, interval_seconds: parseInt(e.target.value, 10) || 60 })}
            min={10}
            step={5}
            className="input"
          />
          <p className="mt-1 text-xs text-slate-500">How often to check</p>
        </div>

        <div>
          <label className="block text-xs font-medium text-slate-400 mb-1.5">Timeout (seconds)</label>
          <input
            type="number"
            value={formData.timeout_seconds}
            onChange={(e) => setFormData({ ...formData, timeout_seconds: parseInt(e.target.value, 10) || 10 })}
            min={1}
            className="input"
          />
          {errors.timeout_seconds && <p className="mt-1 text-xs text-rose-400">{errors.timeout_seconds}</p>}
          <p className="mt-1 text-xs text-slate-500">Request timeout</p>
        </div>
      </div>

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
        <p className="mt-1 text-xs text-slate-500">Get notified when this monitor fails</p>
      </div>

      <div>
        <label className="block text-xs font-medium text-slate-400 mb-1.5">Tags</label>
        <input
          type="text"
          value={formData.tags}
          onChange={(e) => setFormData({ ...formData, tags: e.target.value })}
          placeholder="production, grpc, critical"
          className="input"
        />
        <p className="mt-1 text-xs text-slate-500">Comma-separated tags for filtering</p>
      </div>

      <div className="flex items-center justify-between rounded-lg border border-white/[0.08] bg-slate-800/30 px-4 py-3">
        <div>
          <p className="text-sm font-medium text-white">Monitor Enabled</p>
          <p className="text-xs text-slate-500">Run gRPC health checks on schedule</p>
        </div>
        <button
          type="button"
          onClick={() => setFormData({ ...formData, enabled: !formData.enabled })}
          className={`relative h-5 w-9 rounded-full transition-colors ${
            formData.enabled ? 'bg-cyan-500' : 'bg-slate-700'
          }`}
        >
          <span
            className={`absolute top-0.5 left-0.5 h-4 w-4 rounded-full bg-white transition-transform ${
              formData.enabled ? 'translate-x-4' : ''
            }`}
          />
        </button>
      </div>

      <div className="flex gap-3 pt-2">
        <button type="submit" disabled={loading} className="btn btn-primary flex-1">
          {loading ? 'Saving...' : isEditMode ? 'Update Monitor' : 'Create Monitor'}
        </button>
        {onCancel && (
          <button type="button" onClick={onCancel} className="btn btn-secondary">
            Cancel
          </button>
        )}
      </div>
    </form>
  );
}
