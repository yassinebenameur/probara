'use client';

import { useEffect, useId, useState, type FormEvent } from 'react';

import { createIncident } from '@/lib/api';
import type { AdminUser, IncidentDetail, IncidentSeverity } from '@/lib/types';
import Input from '@/components/ui/Input';
import Button from '@/components/ui/Button';
import Callout from '@/components/ui/Callout';
import FormField from '@/components/ui/FormField';
import Dialog from '@/components/ui/Dialog';

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
  const formId = useId();

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

  return (
    <Dialog
      onClose={onClose}
      busy={saving}
      size="lg"
      title="Create incident"
      description="Start a human-managed incident and keep publication manual."
      footer={
        <>
          <Button type="button" variant="ghost" size="sm" onClick={onClose} disabled={saving}>
            Cancel
          </Button>
          <Button
            type="submit"
            form={formId}
            variant="accent"
            size="sm"
            disabled={saving || users.length === 0}
            loading={saving}
          >
            {saving ? 'Creating…' : 'Create incident'}
          </Button>
        </>
      }
    >
      <form id={formId} onSubmit={handleSubmit} noValidate className="space-y-4">
        {initialContext?.alert_id || initialContext?.monitor_id ? (
          <div className="glass-well px-3 py-2 text-xs text-slate-400">
            Creating with linked
            {initialContext.alert_id ? (
              <> alert <span className="font-mono">{initialContext.alert_id}</span></>
            ) : ''}
            {initialContext.alert_id && initialContext.monitor_id ? ' and' : ''}
            {initialContext.monitor_id ? (
              <> monitor <span className="font-mono">{initialContext.monitor_id}</span></>
            ) : ''}
          </div>
        ) : null}

        <Input
          label="Title"
          value={title}
          onChange={(event) => setTitle(event.target.value)}
          placeholder="e.g. API outage"
          maxLength={140}
          disabled={saving}
          autoFocus
        />

        <FormField label="Summary">
          <textarea
            value={summary}
            onChange={(event) => setSummary(event.target.value)}
            placeholder="e.g. Requests are failing across regions."
            className="input min-h-[120px] resize-y"
            disabled={saving}
            maxLength={2000}
          />
        </FormField>

        <div className="grid gap-4 md:grid-cols-2">
          <FormField label="Severity">
            <select
              value={severity}
              onChange={(event) => setSeverity(event.target.value as IncidentSeverity)}
              className="input"
              disabled={saving}
            >
              {severityOptions.map((option) => (
                <option key={option.value} value={option.value}>
                  {option.label}
                </option>
              ))}
            </select>
          </FormField>

          <FormField label="Owner">
            <select
              value={ownerUserId}
              onChange={(event) => setOwnerUserId(event.target.value)}
              className="input"
              disabled={saving || users.length === 0}
            >
              <option value="" disabled data-prompt>
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
          </FormField>
        </div>

        {error ? <Callout tone="danger">{error}</Callout> : null}
      </form>
    </Dialog>
  );
}
