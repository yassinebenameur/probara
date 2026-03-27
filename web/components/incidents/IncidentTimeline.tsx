'use client';

import { FormEvent, useState } from 'react';

import type { IncidentTimelineEntry } from '@/lib/types';
import Panel from '@/components/ui/Panel';

interface IncidentTimelineProps {
  entries: IncidentTimelineEntry[];
  onAddInternalNote: (message: string) => Promise<void>;
  onAddPublicUpdate: (message: string) => Promise<void>;
}

function formatTimestamp(value: string): string {
  const date = new Date(value);
  if (Number.isNaN(date.getTime())) {
    return value;
  }
  return date.toLocaleString();
}

function badgeClass(entryType: IncidentTimelineEntry['entry_type']): string {
  switch (entryType) {
    case 'public_update':
      return 'badge-warning';
    case 'internal_note':
      return 'badge-default';
    default:
      return 'badge-danger';
  }
}

export default function IncidentTimeline({
  entries,
  onAddInternalNote,
  onAddPublicUpdate,
}: IncidentTimelineProps) {
  const [internalMessage, setInternalMessage] = useState('');
  const [publicMessage, setPublicMessage] = useState('');
  const [savingInternal, setSavingInternal] = useState(false);
  const [savingPublic, setSavingPublic] = useState(false);

  const submitInternal = async (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault();
    const message = internalMessage.trim();
    if (!message) {
      return;
    }
    setSavingInternal(true);
    await onAddInternalNote(message);
    setInternalMessage('');
    setSavingInternal(false);
  };

  const submitPublic = async (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault();
    const message = publicMessage.trim();
    if (!message) {
      return;
    }
    setSavingPublic(true);
    await onAddPublicUpdate(message);
    setPublicMessage('');
    setSavingPublic(false);
  };

  return (
    <Panel
      title="Timeline"
      subtitle={`${entries.length} event${entries.length === 1 ? '' : 's'} recorded`}
      dotColor="var(--warning)"
    >
      <div className="grid gap-6 lg:grid-cols-[minmax(0,1.6fr)_minmax(320px,1fr)]">
        <div className="space-y-3">
          {entries.length === 0 ? (
            <div className="rounded-xl border border-dashed border-white/[0.08] px-4 py-8 text-center text-sm text-slate-500">
              No timeline entries yet.
            </div>
          ) : (
            entries.map((entry) => (
              <div key={entry.id} className="rounded-xl border border-white/[0.06] bg-slate-950/40 px-4 py-4">
                <div className="flex items-center justify-between gap-3">
                  <span className={`badge ${badgeClass(entry.entry_type)}`}>
                    {entry.entry_type.replace('_', ' ')}
                  </span>
                  <span className="text-xs text-slate-500">{formatTimestamp(entry.created_at)}</span>
                </div>
                <p className="mt-3 text-sm text-slate-200">{entry.message}</p>
              </div>
            ))
          )}
        </div>

        <div className="space-y-4">
          <form onSubmit={submitInternal} className="rounded-xl border border-white/[0.06] bg-slate-950/40 p-4">
            <div className="text-sm font-medium text-white">Add Internal Note</div>
            <textarea
              className="input mt-3 min-h-[120px] resize-y"
              value={internalMessage}
              onChange={(event) => setInternalMessage(event.target.value)}
              placeholder="Capture responder-only context."
              disabled={savingInternal}
            />
            <div className="mt-3 flex justify-end">
              <button type="submit" className="btn btn-secondary btn-sm" disabled={savingInternal}>
                {savingInternal ? 'Saving...' : 'Add Note'}
              </button>
            </div>
          </form>

          <form onSubmit={submitPublic} className="rounded-xl border border-white/[0.06] bg-slate-950/40 p-4">
            <div className="text-sm font-medium text-white">Add Public Update</div>
            <textarea
              className="input mt-3 min-h-[120px] resize-y"
              value={publicMessage}
              onChange={(event) => setPublicMessage(event.target.value)}
              placeholder="Share customer-facing progress."
              disabled={savingPublic}
            />
            <div className="mt-3 flex justify-end">
              <button type="submit" className="btn btn-primary btn-sm" disabled={savingPublic}>
                {savingPublic ? 'Saving...' : 'Add Update'}
              </button>
            </div>
          </form>
        </div>
      </div>
    </Panel>
  );
}
