'use client';

import { FormEvent, useState } from 'react';
import { Activity, Megaphone, MessageSquare, type LucideIcon } from 'lucide-react';

import type { IncidentTimelineEntry } from '@/lib/types';
import { formatDateTime, pluralize } from '@/lib/format';
import Panel from '@/components/ui/Panel';
import Button from '@/components/ui/Button';

interface IncidentTimelineProps {
  entries: IncidentTimelineEntry[];
  onAddInternalNote: (message: string) => Promise<void>;
  onAddPublicUpdate: (message: string) => Promise<void>;
}

const ENTRY_STYLE: Record<IncidentTimelineEntry['entry_type'], { label: string; marker: string; Icon: LucideIcon }> = {
  public_update: { label: 'Public update', marker: 'bg-sky-500/[0.14] text-sky-400', Icon: Megaphone },
  internal_note: { label: 'Internal note', marker: 'bg-well-hover text-slate-400', Icon: MessageSquare },
  system: { label: 'System', marker: 'bg-well-hover text-slate-400', Icon: Activity },
};

function entryStyle(entryType: IncidentTimelineEntry['entry_type']) {
  const label = entryType.replace('_', ' ');
  return ENTRY_STYLE[entryType] ?? { ...ENTRY_STYLE.system, label: label.charAt(0).toUpperCase() + label.slice(1) };
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
      subtitle={`${pluralize(entries.length, 'event')} recorded`}
    >
      {entries.length === 0 ? (
        <div className="py-6 text-center text-sm text-slate-500">
          No timeline entries yet.
        </div>
      ) : (
        <ol className="space-y-5">
          {entries.map((entry, index) => {
            const { label, marker, Icon } = entryStyle(entry.entry_type);
            return (
              <li key={entry.id} className="relative flex gap-3">
                {index < entries.length - 1 && (
                  <span className="absolute -bottom-4 left-[13px] top-8 w-px bg-divider" aria-hidden="true" />
                )}
                <span
                  className={`flex h-7 w-7 shrink-0 items-center justify-center rounded-full ${marker}`}
                  aria-hidden="true"
                >
                  <Icon className="h-3.5 w-3.5" strokeWidth={1.75} />
                </span>
                <div className="min-w-0 flex-1 pt-1">
                  <div className="flex flex-wrap items-baseline gap-x-2">
                    <span className="text-ui font-medium text-slate-200">{label}</span>
                    <span className="text-xs text-slate-500">{formatDateTime(entry.created_at)}</span>
                  </div>
                  <p className="mt-1 whitespace-pre-line text-sm text-slate-300">{entry.message}</p>
                </div>
              </li>
            );
          })}
        </ol>
      )}

      <div className="mt-6 grid gap-4 md:grid-cols-2">
        <form onSubmit={submitInternal} className="glass-well flex flex-col p-4">
          <div className="text-ui font-medium text-slate-300">Add internal note</div>
          <textarea
            className="input mt-2 min-h-[88px] flex-1 resize-y"
            value={internalMessage}
            onChange={(event) => setInternalMessage(event.target.value)}
            placeholder="Capture responder-only context."
            disabled={savingInternal}
          />
          <div className="mt-3 flex justify-end">
            <Button type="submit" variant="ghost" size="sm" disabled={savingInternal} loading={savingInternal}>
              {savingInternal ? 'Saving…' : 'Add note'}
            </Button>
          </div>
        </form>

        <form onSubmit={submitPublic} className="glass-well flex flex-col p-4">
          <div className="text-ui font-medium text-slate-300">Add public update</div>
          <textarea
            className="input mt-2 min-h-[88px] flex-1 resize-y"
            value={publicMessage}
            onChange={(event) => setPublicMessage(event.target.value)}
            placeholder="Share customer-facing progress."
            disabled={savingPublic}
          />
          <div className="mt-3 flex justify-end">
            <Button type="submit" variant="ghost" size="sm" disabled={savingPublic} loading={savingPublic}>
              {savingPublic ? 'Saving…' : 'Add update'}
            </Button>
          </div>
        </form>
      </div>
    </Panel>
  );
}
