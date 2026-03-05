'use client';

import { useState, useEffect } from 'react';
import { Monitor, CreateMonitorRequest, UpdateMonitorRequest, AlertPolicy, GroupMonitorConfig } from '@/lib/types';
import { getAlertPolicies, getMonitors } from '@/lib/api';

interface GroupFormProps {
  monitor?: Monitor;
  initialData?: CreateMonitorRequest;
  onSubmit: (data: CreateMonitorRequest | UpdateMonitorRequest) => Promise<void>;
  onCancel?: () => void;
  loading?: boolean;
}

export default function GroupForm({
  monitor,
  initialData,
  onSubmit,
  onCancel,
  loading = false,
}: GroupFormProps) {
  const [alertPolicies, setAlertPolicies] = useState<AlertPolicy[]>([]);
  const [availableMonitors, setAvailableMonitors] = useState<Monitor[]>([]);
  const isEditMode = Boolean(monitor);
  const initialGroupConfig =
    !isEditMode && initialData?.type === 'group' ? (initialData.config as GroupMonitorConfig) : undefined;
  const existingMemberIds = monitor?.member_ids || initialGroupConfig?.monitor_ids || [];

  const [formData, setFormData] = useState({
    name: monitor?.name || initialData?.name || '',
    monitor_ids: existingMemberIds,
    alert_policy_ids:
      monitor?.alert_policy_ids ||
      (monitor?.alert_policy_id ? [monitor.alert_policy_id] : initialData?.alert_policy_ids || []),
    enabled: monitor?.enabled ?? initialData?.enabled ?? true,
    tags: monitor?.tags?.join(', ') || (initialData?.tags || []).join(', '),
  });

  const [errors, setErrors] = useState<Record<string, string>>({});
  const [initializedMonitorId, setInitializedMonitorId] = useState<string | null>(null);

  useEffect(() => {
    loadAlertPolicies();
    loadAvailableMonitors();
  }, []);

  useEffect(() => {
    if (monitor && monitor.id !== initializedMonitorId) {
      setFormData({
        name: monitor.name || '',
        monitor_ids: monitor.member_ids || [],
        alert_policy_ids: monitor.alert_policy_ids || (monitor.alert_policy_id ? [monitor.alert_policy_id] : []),
        enabled: monitor.enabled ?? true,
        tags: monitor.tags?.join(', ') || '',
      });
      setInitializedMonitorId(monitor.id);
    } else if (!monitor && initializedMonitorId !== null) {
      setFormData({
        name: '',
        monitor_ids: [],
        alert_policy_ids: [],
        enabled: true,
        tags: '',
      });
      setInitializedMonitorId(null);
    }
  }, [monitor?.id]);

  const loadAlertPolicies = async () => {
    try {
      const response = await getAlertPolicies({ page_size: 100 });
      setAlertPolicies(response?.items || []);
    } catch (error) {
      console.error('Failed to load alert policies:', error);
    }
  };

  const loadAvailableMonitors = async () => {
    try {
      const response = await getMonitors({ page_size: 100 });
      const selectableMonitors = (response?.items || []).filter((m) => m.id !== monitor?.id);
      setAvailableMonitors(selectableMonitors);
    } catch (error) {
      console.error('Failed to load monitors:', error);
    }
  };

  const handleSubmit = async (e: React.FormEvent) => {
    e.preventDefault();
    setErrors({});

    const newErrors: Record<string, string> = {};
    if (!formData.name.trim()) newErrors.name = 'Name is required';
    if (formData.monitor_ids.length === 0) newErrors.monitor_ids = 'Select at least one monitor';

    if (Object.keys(newErrors).length > 0) {
      setErrors(newErrors);
      return;
    }

    const config: GroupMonitorConfig = { monitor_ids: formData.monitor_ids };

    const requestData: CreateMonitorRequest | UpdateMonitorRequest = {
      name: formData.name.trim(),
      type: 'group',
      config,
      interval_seconds: 60,
      timeout_seconds: 30,
      enabled: formData.enabled,
    };

    requestData.alert_policy_ids = formData.alert_policy_ids;
    if (formData.tags.trim()) {
      requestData.tags = formData.tags.split(',').map(t => t.trim()).filter(t => t);
    }

    await onSubmit(requestData);
  };

  const toggleMonitor = (monitorId: string) => {
    const newSelectedIds = formData.monitor_ids.includes(monitorId)
      ? formData.monitor_ids.filter(id => id !== monitorId)
      : [...formData.monitor_ids, monitorId];
    setFormData({ ...formData, monitor_ids: newSelectedIds });
  };

  return (
    <form onSubmit={handleSubmit} className="space-y-5">
      {/* Group Name */}
      <div>
        <label className="block text-xs font-medium text-slate-400 mb-1.5">Group Name</label>
        <input
          type="text"
          value={formData.name}
          onChange={(e) => setFormData({ ...formData, name: e.target.value })}
          placeholder="Production Services"
          className="input"
        />
        {errors.name && <p className="mt-1 text-xs text-rose-400">{errors.name}</p>}
        <p className="mt-1 text-xs text-slate-500">A name for this group of monitors</p>
      </div>

      {/* Monitor Selection */}
      <div>
        <div className="flex items-center justify-between mb-1.5">
          <label className="text-xs font-medium text-slate-400">
            Select Monitors <span className="text-cyan-400">({formData.monitor_ids.length} selected)</span>
          </label>
          <div className="flex gap-2 text-xs">
            <button
              type="button"
              onClick={() => setFormData({ ...formData, monitor_ids: availableMonitors.map(m => m.id) })}
              className="btn btn-xs btn-outline"
            >
              Select all
            </button>
            <button
              type="button"
              onClick={() => setFormData({ ...formData, monitor_ids: [] })}
              className="btn btn-xs btn-outline"
            >
              Clear
            </button>
          </div>
        </div>

        <div className="max-h-56 overflow-y-auto rounded-lg border border-white/[0.08] bg-slate-800/30 p-2">
          {availableMonitors.length === 0 ? (
            <p className="py-6 text-center text-sm text-slate-500">No monitors available</p>
          ) : (
            <div className="space-y-1">
              {availableMonitors.map((mon) => (
                <label
                  key={mon.id}
                  className={`flex cursor-pointer items-center gap-3 rounded-lg px-3 py-2 transition-colors ${
                    formData.monitor_ids.includes(mon.id)
                      ? 'bg-cyan-500/10 border border-cyan-500/30'
                      : 'hover:bg-white/[0.03] border border-transparent'
                  }`}
                >
                  <input
                    type="checkbox"
                    checked={formData.monitor_ids.includes(mon.id)}
                    onChange={() => toggleMonitor(mon.id)}
                    className="sr-only"
                  />
                  <div className={`flex h-4 w-4 items-center justify-center rounded border transition-colors ${
                    formData.monitor_ids.includes(mon.id)
                      ? 'border-cyan-500 bg-cyan-500'
                      : 'border-slate-600'
                  }`}>
                    {formData.monitor_ids.includes(mon.id) && (
                      <svg className="h-3 w-3 text-white" fill="none" viewBox="0 0 24 24" stroke="currentColor">
                        <path strokeLinecap="round" strokeLinejoin="round" strokeWidth={3} d="M5 13l4 4L19 7" />
                      </svg>
                    )}
                  </div>
                  <div className="flex-1 min-w-0">
                    <p className="text-sm font-medium text-white truncate">{mon.name}</p>
                    <p className="text-xs text-slate-500">{mon.type.toUpperCase()}</p>
                  </div>
                  <span className={`text-xs ${mon.enabled ? 'text-emerald-400' : 'text-slate-500'}`}>
                    {mon.enabled ? 'Active' : 'Paused'}
                  </span>
                </label>
              ))}
            </div>
          )}
        </div>
        {errors.monitor_ids && <p className="mt-1 text-xs text-rose-400">{errors.monitor_ids}</p>}
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
          placeholder="production, critical (comma-separated)"
          className="input"
        />
      </div>

      {/* Enabled Toggle */}
      <div className="flex items-center justify-between rounded-lg border border-white/[0.08] bg-slate-800/30 px-4 py-3">
        <div>
          <p className="text-sm font-medium text-white">Group Enabled</p>
          <p className="text-xs text-slate-500">Track status of this group</p>
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
          {loading ? 'Saving...' : isEditMode ? 'Save Changes' : 'Create Group'}
        </button>
      </div>
    </form>
  );
}
