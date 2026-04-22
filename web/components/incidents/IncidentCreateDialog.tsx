'use client';

import { useEffect, useState, type FormEvent } from 'react';

import { createIncident } from '@/lib/api';
import type { AdminUser, IncidentDetail, IncidentSeverity } from '@/lib/types';
import Input from '@/components/ui/Input';

export interface IncidentCreateDialogInitialContext {
  alert_id?: string;
  monitor_id?: string;
  title?: string;
  summary?: string;
}

interface IncidentCreateDialogProps {
  open: boolean;
  onClose: () => void;
  onCreated: (incident: IncidentDetail) => void;
  users: AdminUser[];
  initialContext?: IncidentCreateDialogInitialContext;
}

const severityOptions: Array<{ value: IncidentSeverity; label: string }> = [
  { value: 'critical', label: 'Critical' },
  { value: 'high', label: 'High' },
  { value: 'medium', label: 'Medium' },
  { value: 'low', label: 'Low' },
];

function getUserLabel(user: AdminUser): string {
  return user.disabled_at ? `${user.username} (disabled)` : user.username;
}

export default function IncidentCreateDialog({
  open,
  onClose,
  onCreated,
  users,
  initialContext,
}: IncidentCreateDialogProps) {
  const [title, setTitle] = useState('');
  const [summary, setSummary] = useState('');
  const [severity, setSeverity] = useState<IncidentSeverity>('medium');
  const [ownerUserId, setOwnerUserId] = useState('');
  const [error, setError] = useState('');
  const [saving, setSaving] = useState(false);

  useEffect(() => {
    if (!open) {
      return;
    }

    setTitle(initialContext?.title ?? '');
    setSummary(initialContext?.summary ?? '');
    setSeverity('medium');
    setOwnerUserId(users.length === 1 ? users[0].id : '');
    setError('');
    setSaving(false);
  }, [initialContext, open, users]);

  useEffect(() => {
    if (open && !ownerUserId && users.length === 1) {
      setOwnerUserId(users[0].id);
    }
  }, [open, ownerUserId, users]);

  if (!open) {
    return null;
  }

  const handleSubmit = async (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault();

    const nextTitle = title.trim();
    const nextSummary = summary.trim();
    if (!nextTitle) {
      setError('Title is required');
      return;
    }
    if (!nextSummary) {
      setError('Summary is required');
      return;
    }
    if (!ownerUserId) {
      setError('Owner is required');
      return;
    }

    try {
      setSaving(true);
      setError('');
      const incident = await createIncident({
        title: nextTitle,
        summary: nextSummary,
        severity,
        owner_user_id: ownerUserId,
        ...(initialContext?.alert_id ? { alert_id: initialContext.alert_id } : {}),
        ...(initialContext?.monitor_id ? { monitor_id: initialContext.monitor_id } : {}),
      });
      setTitle('');
      setSummary('');
      setSeverity('medium');
      setOwnerUserId('');
      onCreated(incident);
      onClose();
    } catch (err) {
      setError(err instanceof Error ? err.message : 'Failed to create incident');
      setSaving(false);
      return;
    }
    setSaving(false);
  };

  const selectBaseClass = 'input';
  const selectErrorClass = 'border-rose-500/50 bg-rose-500/5 focus:border-rose-500/70 focus:ring-1 focus:ring-rose-500/20';

  return (
    <div className="fixed inset-0 z-50 flex items-center justify-center bg-slate-950/70 px-4 backdrop-blur-sm">
      <div className="w-full max-w-2xl rounded-2xl border border-white/[0.08] bg-slate-900 p-6 shadow-2xl">
        <div className="mb-5 flex items-start justify-between gap-4">
          <div>
            <h2 className="text-lg font-semibold text-white">Create Incident</h2>
            <p className="mt-1 text-sm text-slate-500">
              Start a human-managed incident and keep publication manual.
            </p>
          </div>
          <button
            type="button"
            onClick={onClose}
            className="rounded-md px-2 py-1 text-sm text-slate-400 transition hover:bg-white/[0.04] hover:text-slate-200"
          >
            Close
          </button>
        </div>

        {initialContext?.alert_id || initialContext?.monitor_id ? (
          <div className="mb-4 rounded-lg border border-white/[0.06] bg-white/[0.03] px-3 py-2 text-xs text-slate-400">
            Creating with linked
            {initialContext.alert_id ? ` alert ${initialContext.alert_id}` : ''}
            {initialContext.alert_id && initialContext.monitor_id ? ' and' : ''}
            {initialContext.monitor_id ? ` monitor ${initialContext.monitor_id}` : ''}
          </div>
        ) : null}

        <form className="space-y-4" onSubmit={handleSubmit}>
          <Input
            label="Title"
            value={title}
            onChange={(event) => setTitle(event.target.value)}
            placeholder="API outage"
            maxLength={140}
            disabled={saving}
          />

          <div>
            <label className="mb-1.5 block text-xs font-medium text-slate-400">
              Summary
            </label>
            <textarea
              value={summary}
              onChange={(event) => setSummary(event.target.value)}
              placeholder="Requests are failing across regions."
              className="input min-h-[120px] resize-y"
              disabled={saving}
              maxLength={2000}
            />
          </div>

          <div className="grid gap-4 md:grid-cols-2">
            <div>
              <label className="mb-1.5 block text-xs font-medium text-slate-400">
                Severity
              </label>
              <select
                value={severity}
                onChange={(event) => setSeverity(event.target.value as IncidentSeverity)}
                className={`${selectBaseClass} ${error ? selectErrorClass : ''}`}
                disabled={saving}
              >
                {severityOptions.map((option) => (
                  <option key={option.value} value={option.value}>
                    {option.label}
                  </option>
                ))}
              </select>
            </div>

            <div>
              <label className="mb-1.5 block text-xs font-medium text-slate-400">
                Owner
              </label>
              <select
                value={ownerUserId}
                onChange={(event) => setOwnerUserId(event.target.value)}
                className={`${selectBaseClass} ${error ? selectErrorClass : ''}`}
                disabled={saving || users.length === 0}
              >
                <option value="" disabled>
                  {users.length === 0 ? 'No users available' : 'Select an owner'}
                </option>
                {users.map((user) => (
                  <option key={user.id} value={user.id}>
                    {getUserLabel(user)}
                  </option>
                ))}
              </select>
              {users.length === 0 ? (
                <p className="mt-1 text-xs text-slate-500">
                  Load users before creating an incident.
                </p>
              ) : null}
            </div>
          </div>

          {error ? (
            <div className="rounded-lg border border-rose-500/25 bg-rose-500/10 px-3 py-2 text-sm text-rose-300">
              {error}
            </div>
          ) : null}

          <div className="flex items-center justify-end gap-3 pt-2">
            <button
              type="button"
              onClick={onClose}
              className="btn btn-secondary btn-sm"
              disabled={saving}
            >
              Cancel
            </button>
            <button
              type="submit"
              className="btn btn-primary btn-sm"
              disabled={saving || users.length === 0}
            >
              {saving ? 'Creating...' : 'Create Incident'}
            </button>
          </div>
        </form>
      </div>
    </div>
  );
}
