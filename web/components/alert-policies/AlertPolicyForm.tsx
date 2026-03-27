'use client';

import { useState } from 'react';
import { AlertChannel, AlertPolicy, CreateAlertPolicyRequest, UpdateAlertPolicyRequest } from '@/lib/types';
import FormField from '@/components/ui/FormField';
import Input from '@/components/ui/Input';

interface AlertPolicyFormProps {
  policy?: AlertPolicy;
  channels?: AlertChannel[];
  onSubmit: (data: CreateAlertPolicyRequest | UpdateAlertPolicyRequest) => Promise<void>;
  onCancel?: () => void;
  loading?: boolean;
}

export default function AlertPolicyForm({
  policy,
  channels = [],
  onSubmit,
  onCancel,
  loading = false,
}: AlertPolicyFormProps) {
  const [formData, setFormData] = useState({
    name: policy?.name || '',
    description: policy?.description || '',
    failure_threshold: policy?.failure_threshold || 3,
    failure_window_seconds: policy?.failure_window_seconds || 300,
    create_incident_on_fire: policy?.create_incident_on_fire || false,
    channel_ids: policy?.channel_ids || [],
  });

  const [errors, setErrors] = useState<Record<string, string>>({});

  const handleSubmit = async (e: React.FormEvent) => {
    e.preventDefault();
    setErrors({});

    // Validation
    const newErrors: Record<string, string> = {};
    if (!formData.name.trim()) {
      newErrors.name = 'Name is required';
    }
    if (formData.failure_threshold < 1) {
      newErrors.failure_threshold = 'Failure threshold must be at least 1';
    }
    if (formData.failure_window_seconds < 1) {
      newErrors.failure_window_seconds = 'Failure window must be at least 1 second';
    }

    if (Object.keys(newErrors).length > 0) {
      setErrors(newErrors);
      return;
    }

    // Prepare request data
    const requestData: CreateAlertPolicyRequest | UpdateAlertPolicyRequest = {
      name: formData.name.trim(),
      failure_threshold: formData.failure_threshold,
      failure_window_seconds: formData.failure_window_seconds,
      create_incident_on_fire: formData.create_incident_on_fire,
      channel_ids: formData.channel_ids,
    };

    if (formData.description.trim()) {
      requestData.description = formData.description.trim();
    }

    await onSubmit(requestData);
  };

  return (
    <form onSubmit={handleSubmit} className="space-y-6">
      <FormField label="Name" required error={errors.name}>
        <Input
          value={formData.name}
          onChange={(e) => setFormData({ ...formData, name: e.target.value })}
          placeholder="Critical Alert Policy"
        />
      </FormField>

      <FormField label="Description" description="Optional description for this alert policy">
        <textarea
          className="input min-h-[96px] resize-y"
          rows={3}
          value={formData.description}
          onChange={(e) => setFormData({ ...formData, description: e.target.value })}
          placeholder="This policy triggers when..."
        />
      </FormField>

      <div className="grid grid-cols-2 gap-4">
        <FormField label="Failure Threshold" required error={errors.failure_threshold}>
          <Input
            type="number"
            min="1"
            value={formData.failure_threshold}
            onChange={(e) => setFormData({ ...formData, failure_threshold: parseInt(e.target.value) || 1 })}
          />
          <p className="mt-1 text-xs text-slate-500">
            Number of failures required to trigger alert
          </p>
        </FormField>

        <FormField label="Failure Window (seconds)" required error={errors.failure_window_seconds}>
          <Input
            type="number"
            min="1"
            value={formData.failure_window_seconds}
            onChange={(e) => setFormData({ ...formData, failure_window_seconds: parseInt(e.target.value) || 1 })}
          />
          <p className="mt-1 text-xs text-slate-500">
            Time window in which failures are counted
          </p>
        </FormField>
      </div>

      <FormField
        label="Incident Auto-Creation"
        description="If enabled, the platform will automatically create an incident when this policy fires. Incidents are never auto-published."
      >
        <label className="flex items-start gap-3 rounded-lg border border-white/[0.08] bg-slate-800/40 px-3 py-3 text-sm text-slate-200">
          <input
            type="checkbox"
            checked={formData.create_incident_on_fire}
            onChange={(e) => setFormData({ ...formData, create_incident_on_fire: e.target.checked })}
          />
          <div className="flex flex-col gap-1">
            <span className="font-medium">Create incident on fire</span>
            <span className="text-xs text-slate-500">
              Auto-created incidents start in <span className="font-mono">investigating</span> and require manual state changes and publication.
            </span>
          </div>
        </label>
      </FormField>

      <FormField label="Alert Channels" description="Select one or more channels to notify">
        {channels.length === 0 ? (
          <div className="text-sm text-slate-500">No alert channels configured yet.</div>
        ) : (
          <div className="space-y-2">
            {channels.map((channel) => {
              const checked = formData.channel_ids.includes(channel.id);
              return (
                <label
                  key={channel.id}
                  className="flex items-center gap-3 rounded-lg border border-white/[0.08] bg-slate-800/40 px-3 py-2 text-sm text-slate-200"
                >
                  <input
                    type="checkbox"
                    checked={checked}
                    onChange={(e) => {
                      const next = e.target.checked
                        ? [...formData.channel_ids, channel.id]
                        : formData.channel_ids.filter((id) => id !== channel.id);
                      setFormData({ ...formData, channel_ids: next });
                    }}
                  />
                  <div className="flex flex-col">
                    <span className="font-medium">{channel.name}</span>
                    <span className="text-xs text-slate-500">
                      {channel.type.toUpperCase()} - {channel.is_active ? 'Active' : 'Inactive'}
                    </span>
                  </div>
                </label>
              );
            })}
          </div>
        )}
      </FormField>

      <div className="rounded-lg border border-cyan-500/20 bg-cyan-500/10 px-4 py-3">
        <p className="text-sm text-cyan-200">
          <strong>Example:</strong> With a threshold of {formData.failure_threshold} and a window of {formData.failure_window_seconds} seconds,
          an alert will trigger when {formData.failure_threshold} failures occur within {formData.failure_window_seconds} seconds.
        </p>
      </div>

      <div className="flex justify-end gap-4">
        {onCancel && (
          <button
            type="button"
            onClick={onCancel}
            disabled={loading}
            className="btn btn-secondary btn-sm disabled:cursor-not-allowed disabled:opacity-50"
          >
            Cancel
          </button>
        )}
        <button
          type="submit"
          disabled={loading}
          className="btn btn-primary btn-sm disabled:cursor-not-allowed disabled:opacity-50"
        >
          {loading ? 'Saving...' : policy ? 'Update Policy' : 'Create Policy'}
        </button>
      </div>
    </form>
  );
}
