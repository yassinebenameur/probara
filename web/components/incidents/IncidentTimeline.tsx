'use client';

import { FormEvent, KeyboardEvent, useId, useMemo, useState } from 'react';
import { Activity, Megaphone, MessageSquare, type LucideIcon } from 'lucide-react';

import type { IncidentTimelineEntry } from '@/lib/types';
import { formatDateTimeFull, formatDuration, pluralize, timezoneLabel } from '@/lib/format';
import {
  countTimeline,
  groupTimeline,
  minutesSinceOpen,
  sortTimeline,
  timelineClock,
  type TimelineEntryType as EntryType,
  type TimelineFilter as Filter,
  type TimelineRow as Row,
} from '@/lib/incident-view';
import { isApplePlatform, shortcutLabel } from '@/lib/shortcuts';
import Panel from '@/components/ui/Panel';
import Button from '@/components/ui/Button';
import Callout from '@/components/ui/Callout';
import SegmentedControl from '@/components/ui/SegmentedControl';

type ComposeKind = 'internal_note' | 'public_update';

interface IncidentTimelineProps {
  entries: IncidentTimelineEntry[];
  /** The incident's open time: entries show their offset from it (+21m). */
  startedAt: string;
  /** Titles of the status pages the incident is published to, for the public-update hint. */
  publishedTo: string[];
  /** Resolved incidents keep the composer but lead with the history. */
  resolved?: boolean;
  /** Viewers read the timeline; only editors get the composer. */
  canWrite?: boolean;
  onAddInternalNote: (message: string) => Promise<void>;
  onAddPublicUpdate: (message: string) => Promise<void>;
}

const ENTRY_STYLE: Record<EntryType, { label: string; marker: string; Icon: LucideIcon }> = {
  public_update: { label: 'Public update', marker: 'bg-sky-500/[0.14] text-sky-400', Icon: Megaphone },
  internal_note: { label: 'Internal note', marker: 'bg-well-hover text-slate-300', Icon: MessageSquare },
  system: { label: 'System', marker: 'bg-well text-slate-500', Icon: Activity },
};

function entryStyle(entryType: EntryType) {
  const label = entryType.replace('_', ' ');
  return ENTRY_STYLE[entryType] ?? { ...ENTRY_STYLE.system, label: label.charAt(0).toUpperCase() + label.slice(1) };
}

const FILTERS: { value: Filter; label: string; title: string }[] = [
  { value: 'all', label: 'All', title: 'Every timeline entry' },
  { value: 'public_update', label: 'Public', title: 'Public updates only' },
  { value: 'internal_note', label: 'Notes', title: 'Internal notes only' },
  { value: 'system', label: 'System', title: 'State changes and links only' },
];

const COMPOSE_KINDS: { value: ComposeKind; label: string }[] = [
  { value: 'internal_note', label: 'Internal note' },
  { value: 'public_update', label: 'Public update' },
];

export default function IncidentTimeline({
  entries,
  startedAt,
  publishedTo,
  resolved = false,
  canWrite = true,
  onAddInternalNote,
  onAddPublicUpdate,
}: IncidentTimelineProps) {
  const [filter, setFilter] = useState<Filter>('all');
  const [kind, setKind] = useState<ComposeKind>('internal_note');
  const [message, setMessage] = useState('');
  const [saving, setSaving] = useState(false);
  const [error, setError] = useState('');
  // Read once: the app shell renders client-only, so no server markup to mismatch.
  const [apple] = useState(isApplePlatform);
  const textareaId = useId();
  const hintId = useId();

  const sorted = useMemo(() => sortTimeline(entries), [entries]);
  const counts = useMemo(() => countTimeline(sorted), [sorted]);
  // Folds on the full timeline, then filters (see groupTimeline).
  const days = useMemo(() => groupTimeline(sorted, filter), [sorted, filter]);

  const submit = async (event?: FormEvent<HTMLFormElement>) => {
    event?.preventDefault();
    const text = message.trim();
    if (!text || saving) return;
    setSaving(true);
    setError('');
    try {
      await (kind === 'public_update' ? onAddPublicUpdate(text) : onAddInternalNote(text));
      setMessage('');
      // Public updates cannot be deleted: after one is posted the composer
      // falls back to an internal note, so the next thought typed (and sent
      // with the shortcut) is never published by inertia.
      setKind('internal_note');
    } catch (err) {
      setError(err instanceof Error ? err.message : 'Failed to add the entry');
    } finally {
      setSaving(false);
    }
  };

  const onKeyDown = (event: KeyboardEvent<HTMLTextAreaElement>) => {
    if (event.key === 'Enter' && (event.metaKey || event.ctrlKey)) {
      event.preventDefault();
      void submit();
    }
  };

  const isPublic = kind === 'public_update';
  const hint = isPublic
    ? publishedTo.length > 0
      ? `Customers see this on ${publishedTo.join(', ')}.`
      : 'Not published to a status page yet; it shows once you publish the incident.'
    : 'Responders only; never shown publicly.';

  const composer = (
    <form
      onSubmit={submit}
      className={`glass-well p-3 ${isPublic ? 'ring-1 ring-inset ring-sky-500/35' : ''}`}
    >
      <div className="flex flex-wrap items-center justify-between gap-2">
        <SegmentedControl
          options={COMPOSE_KINDS}
          value={kind}
          onChange={setKind}
          label="Entry type"
          size="xs"
          disabled={saving}
        />
        <span id={hintId} className={`text-xs ${isPublic ? 'text-sky-300' : 'text-slate-500'}`}>{hint}</span>
      </div>
      <label htmlFor={textareaId} className="sr-only">
        {isPublic ? 'Public update' : 'Internal note'}
      </label>
      <textarea
        id={textareaId}
        aria-describedby={hintId}
        className="input mt-2.5 min-h-[76px] resize-y"
        rows={3}
        value={message}
        onChange={(event) => setMessage(event.target.value)}
        onKeyDown={onKeyDown}
        placeholder={isPublic ? 'Share customer-facing progress.' : 'Capture responder-only context.'}
        disabled={saving}
      />
      {error && <Callout tone="danger" className="mt-2.5">{error}</Callout>}
      <div className="mt-2.5 flex items-center justify-end gap-3">
        <span className="hidden text-2xs text-slate-500 sm:inline">{shortcutLabel('↵', apple)} to post</span>
        <Button type="submit" variant="ghost" size="sm" disabled={saving || !message.trim()} loading={saving}>
          {isPublic ? 'Post public update' : 'Add note'}
        </Button>
      </div>
    </form>
  );

  return (
    <Panel
      title="Timeline"
      subtitle={`${pluralize(entries.length, 'entry', 'entries')} · newest first · times ${timezoneLabel()}`}
      actions={
        entries.length > 0 ? (
          <SegmentedControl
            options={FILTERS.map((f) => ({ ...f, label: f.value === 'all' ? f.label : `${f.label} ${counts[f.value]}` }))}
            value={filter}
            onChange={setFilter}
            label="Show"
            size="xs"
          />
        ) : undefined
      }
    >
      {canWrite && !resolved && <div className="mb-6">{composer}</div>}

      {days.length === 0 ? (
        <div className="py-6 text-center text-sm text-slate-500">
          {entries.length === 0
            ? 'No timeline entries yet.'
            : `No ${FILTERS.find((f) => f.value === filter)?.title.replace(' only', '').toLowerCase() ?? 'entries'} yet.`}
        </div>
      ) : (
        <div className="space-y-5">
          {days.map((day) => (
            <section key={day.key} aria-label={day.label}>
              <h3 className="mb-2 flex items-center gap-3 text-2xs font-medium uppercase tracking-[0.12em] text-slate-500">
                {day.label}
                <span className="h-px flex-1 bg-divider" aria-hidden="true" />
              </h3>
              <ol>
                {day.rows.map((row, index) => (
                  <TimelineRow
                    key={row.entry.id}
                    row={row}
                    startedAt={startedAt}
                    last={index === day.rows.length - 1}
                  />
                ))}
              </ol>
            </section>
          ))}
        </div>
      )}

      {canWrite && resolved && <div className="mt-6">{composer}</div>}
    </Panel>
  );
}

function TimelineRow({ row, startedAt, last }: { row: Row; startedAt: string; last: boolean }) {
  const { entry, count, first } = row;
  const { label, marker, Icon } = entryStyle(entry.entry_type);
  const isSystem = entry.entry_type === 'system';
  // Notes and updates read "+21m" from the incident's open, to the minute;
  // system rows stay one line, and entries in the first minute carry none.
  const minutes = isSystem ? null : minutesSinceOpen(entry.created_at, startedAt);
  const offset = minutes === null ? null : `+${formatDuration(minutes * 60)}`;
  const when =
    count > 1
      ? `${formatDateTimeFull(first.created_at)} – ${formatDateTimeFull(entry.created_at)}`
      : formatDateTimeFull(entry.created_at);
  const clock = timelineClock(entry.created_at);
  // Below sm the time column would leave messages ~140px wide, so the time
  // moves inline (before a system message, next to a note's label).
  const inlineTime = (
    <time dateTime={entry.created_at} title={when} className="mr-2 text-xs tabular-nums text-slate-500 sm:hidden">
      {clock}
    </time>
  );

  return (
    <li className="relative grid grid-cols-[1.5rem_minmax(0,1fr)] gap-x-2.5 sm:grid-cols-[3.25rem_1.75rem_minmax(0,1fr)] sm:gap-x-3">
      <div className="hidden pt-1 text-right sm:block" title={when}>
        <time dateTime={entry.created_at} className="block text-xs tabular-nums text-slate-400">
          {clock}
        </time>
        {offset && <span className="block text-2xs tabular-nums text-slate-500">{offset}</span>}
      </div>
      <div className="relative flex justify-center">
        {!last && <span className="absolute bottom-0 top-7 w-px bg-divider" aria-hidden="true" />}
        <span
          className={`relative mt-0.5 flex h-6 w-6 shrink-0 items-center justify-center rounded-full ${marker}`}
          aria-hidden="true"
        >
          <Icon className="h-3 w-3" strokeWidth={1.75} />
        </span>
      </div>
      <div className={`min-w-0 ${last ? 'pb-0' : isSystem ? 'pb-3' : 'pb-5'}`}>
        {isSystem ? (
          <p className="pt-1 text-ui text-slate-400">
            {inlineTime}
            <span className="sr-only">{label}: </span>
            {entry.message}
            {count > 1 && (
              <span className="ml-2 rounded bg-well px-1.5 py-px text-2xs tabular-nums text-slate-400" title={when}>
                ×{count}
              </span>
            )}
          </p>
        ) : (
          <>
            <div className={`pt-1 text-xs font-medium ${entry.entry_type === 'public_update' ? 'text-sky-300' : 'text-slate-300'}`}>
              {inlineTime}
              {label}
              {offset && <span className="ml-2 font-normal tabular-nums text-slate-500 sm:hidden">{offset}</span>}
            </div>
            <p
              className={`mt-1.5 whitespace-pre-line text-sm leading-relaxed text-slate-200 ${
                entry.entry_type === 'public_update'
                  ? 'rounded-lg border border-sky-500/20 bg-sky-500/[0.06] px-3 py-2'
                  : ''
              }`}
            >
              {entry.message}
            </p>
          </>
        )}
      </div>
    </li>
  );
}
