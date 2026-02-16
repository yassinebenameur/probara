'use client';

import { useState, useEffect } from 'react';
import { Monitor, CreateMonitorRequest, UpdateMonitorRequest, AlertPolicy, SIPMonitorConfig } from '@/lib/types';
import { getAlertPolicies } from '@/lib/api';

interface SipFormProps {
  monitor?: Monitor;
  onSubmit: (data: CreateMonitorRequest | UpdateMonitorRequest) => Promise<void>;
  onCancel?: () => void;
  loading?: boolean;
}

export default function SipForm({
  monitor,
  onSubmit,
  onCancel,
  loading = false,
}: SipFormProps) {
  const [alertPolicies, setAlertPolicies] = useState<AlertPolicy[]>([]);

  const [formData, setFormData] = useState({
    name: monitor?.name || '',
    host: monitor && monitor.type === 'sip' 
      ? (monitor.config as SIPMonitorConfig)?.host || '' 
      : '',
    port: monitor && monitor.type === 'sip' 
      ? (monitor.config as SIPMonitorConfig)?.port || 5060 
      : 5060,
    transport: monitor && monitor.type === 'sip' 
      ? (monitor.config as SIPMonitorConfig)?.transport || 'udp' 
      : 'udp' as 'udp' | 'tcp',
    expected_status: monitor && monitor.type === 'sip' 
      ? (monitor.config as SIPMonitorConfig)?.expected_status?.toString() || '' 
      : '',
    interval_seconds: monitor?.interval_seconds || 60,
    timeout_seconds: monitor?.timeout_seconds || 10,
    alert_policy_ids: monitor?.alert_policy_ids || (monitor?.alert_policy_id ? [monitor.alert_policy_id] : []),
    enabled: monitor?.enabled ?? true,
    tags: monitor?.tags?.join(', ') || '',
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
      {/* Name */}
      <div>
        <label className="block text-xs font-medium text-slate-400 mb-1.5">Monitor Name</label>
        <input
          type="text"
          value={formData.name}
          onChange={(e) => setFormData({ ...formData, name: e.target.value })}
          placeholder="My SIP Server"
          className="input"
        />
        {errors.name && <p className="mt-1 text-xs text-rose-400">{errors.name}</p>}
        <p className="mt-1 text-xs text-slate-500">Descriptive name for this SIP monitor</p>
      </div>

      {/* Host */}
      <div>
        <label className="block text-xs font-medium text-slate-400 mb-1.5">Host</label>
        <input
          type="text"
          value={formData.host}
          onChange={(e) => setFormData({ ...formData, host: e.target.value })}
          placeholder="sip.example.com or 192.168.1.100"
          className="input"
        />
        {errors.host && <p className="mt-1 text-xs text-rose-400">{errors.host}</p>}
        <p className="mt-1 text-xs text-slate-500">SIP server hostname or IP address</p>
      </div>

      {/* Port and Transport */}
      <div className="grid grid-cols-2 gap-4">
        <div>
          <label className="block text-xs font-medium text-slate-400 mb-1.5">Port</label>
          <input
            type="number"
            value={formData.port}
            onChange={(e) => setFormData({ ...formData, port: parseInt(e.target.value) || 5060 })}
            min={1}
            max={65535}
            className="input"
          />
          {errors.port && <p className="mt-1 text-xs text-rose-400">{errors.port}</p>}
          <p className="mt-1 text-xs text-slate-500">Default: 5060</p>
        </div>

        <div>
          <label className="block text-xs font-medium text-slate-400 mb-1.5">Transport</label>
          <select
            value={formData.transport}
            onChange={(e) => setFormData({ ...formData, transport: e.target.value as 'udp' | 'tcp' })}
            className="input"
          >
            <option value="udp">UDP</option>
            <option value="tcp">TCP</option>
          </select>
          <p className="mt-1 text-xs text-slate-500">SIP transport protocol</p>
        </div>
      </div>

      {/* Expected Status */}
      <div>
        <label className="block text-xs font-medium text-slate-400 mb-1.5">Expected Status Code</label>
        <input
          type="number"
          value={formData.expected_status}
          onChange={(e) => setFormData({ ...formData, expected_status: e.target.value })}
          placeholder="200"
          min={100}
          max={699}
          className="input"
        />
        <p className="mt-1 text-xs text-slate-500">Expected SIP response code (default: 200 OK)</p>
      </div>

      {/* Schedule */}
      <div className="grid grid-cols-2 gap-4">
        <div>
          <label className="block text-xs font-medium text-slate-400 mb-1.5">Interval (seconds)</label>
          <input
            type="number"
            value={formData.interval_seconds}
            onChange={(e) => setFormData({ ...formData, interval_seconds: parseInt(e.target.value) || 60 })}
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
            onChange={(e) => setFormData({ ...formData, timeout_seconds: parseInt(e.target.value) || 10 })}
            min={1}
            className="input"
          />
          {errors.timeout_seconds && <p className="mt-1 text-xs text-rose-400">{errors.timeout_seconds}</p>}
          <p className="mt-1 text-xs text-slate-500">Request timeout</p>
        </div>
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
        <p className="mt-1 text-xs text-slate-500">Get notified when this monitor fails</p>
      </div>

      {/* Tags */}
      <div>
        <label className="block text-xs font-medium text-slate-400 mb-1.5">Tags</label>
        <input
          type="text"
          value={formData.tags}
          onChange={(e) => setFormData({ ...formData, tags: e.target.value })}
          placeholder="production, voip, critical (comma-separated)"
          className="input"
        />
        <p className="mt-1 text-xs text-slate-500">Comma-separated tags for filtering</p>
      </div>

      {/* Enabled Toggle */}
      <div className="flex items-center justify-between rounded-lg border border-white/[0.08] bg-slate-800/30 px-4 py-3">
        <div>
          <p className="text-sm font-medium text-white">Monitor Enabled</p>
          <p className="text-xs text-slate-500">Run SIP OPTIONS checks on schedule</p>
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

      {/* Info Box */}
      <div className="rounded-lg border border-cyan-500/20 bg-cyan-500/5 px-4 py-3">
        <div className="flex items-start gap-3">
          <svg className="h-4 w-4 text-cyan-400 mt-0.5 flex-shrink-0" fill="none" viewBox="0 0 24 24" stroke="currentColor">
            <path strokeLinecap="round" strokeLinejoin="round" strokeWidth={2} d="M13 16h-1v-4h-1m1-4h.01M21 12a9 9 0 11-18 0 9 9 0 0118 0z" />
          </svg>
          <div>
            <p className="text-xs font-medium text-white mb-1">SIP OPTIONS Request</p>
            <p className="text-xs text-slate-400">
              This monitor sends a SIP OPTIONS request to check if the SIP server is responding. 
              This is the standard way to test SIP server availability without initiating a call.
            </p>
          </div>
        </div>
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
          {loading ? 'Saving...' : monitor ? 'Save Changes' : 'Create SIP Monitor'}
        </button>
      </div>
    </form>
  );
}


