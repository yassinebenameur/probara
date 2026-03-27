'use client';

import { FormEvent, useState } from 'react';

import { createIncident } from '@/lib/api';
import type { IncidentDetail } from '@/lib/types';
import Input from '@/components/ui/Input';

interface IncidentCreateDialogProps {
  open: boolean;
  onClose: () => void;
  onCreated: (incident: IncidentDetail) => void;
}

export default function IncidentCreateDialog({
  open,
  onClose,
  onCreated,
}: IncidentCreateDialogProps) {
  const [title, setTitle] = useState('');
  const [summary, setSummary] = useState('');
  const [error, setError] = useState('');
  const [saving, setSaving] = useState(false);

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

    try {
      setSaving(true);
      setError('');
      const incident = await createIncident({
        title: nextTitle,
        summary: nextSummary,
      });
      setTitle('');
      setSummary('');
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
    <div className="fixed inset-0 z-50 flex items-center justify-center bg-slate-950/70 px-4 backdrop-blur-sm">
      <div className="w-full max-w-xl rounded-2xl border border-white/[0.08] bg-slate-900 p-6 shadow-2xl">
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

          {error && (
            <div className="rounded-lg border border-rose-500/25 bg-rose-500/10 px-3 py-2 text-sm text-rose-300">
              {error}
            </div>
          )}

          <div className="flex items-center justify-end gap-3 pt-2">
            <button
              type="button"
              onClick={onClose}
              className="btn btn-secondary btn-sm"
              disabled={saving}
            >
              Cancel
            </button>
            <button type="submit" className="btn btn-primary btn-sm" disabled={saving}>
              {saving ? 'Creating...' : 'Create Incident'}
            </button>
          </div>
        </form>
      </div>
    </div>
  );
}
