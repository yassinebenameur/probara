'use client';

import { useEffect, useMemo, useState } from 'react';
import {
  createMaintenanceWindow,
  getGroupMembers,
  getMaintenanceOccurrences,
  previewMaintenanceRecurrence,
  updateMaintenanceWindow,
} from '@/lib/api';
import type {
  CreateMaintenanceWindowRequest,
  MaintenanceOccurrence,
  MaintenanceRecurrence,
  MaintenanceWindow,
  Monitor,
} from '@/lib/types';
import { getAllMonitors, sortMonitorsByName } from '@/lib/monitor-list';
import { formatClock, formatDateTime, timezoneLabel } from '@/lib/format';
import {
  browserTimezone,
  describeRecurrence,
  MAX_RECURRING_DURATION_MINUTES,
  recurrenceInputError,
  sameRecurrence,
  splitMinutes,
  timezoneOptions,
  WEEKDAY_OPTIONS,
} from '@/lib/maintenance-schedule';
import { errorMessage } from '@/lib/errors';
import Button from '@/components/ui/Button';
import Callout from '@/components/ui/Callout';
import ChipGroup from '@/components/ui/ChipGroup';
import Dialog from '@/components/ui/Dialog';
import FilterChip from '@/components/ui/FilterChip';
import Pill from '@/components/ui/Pill';
import SegmentedControl from '@/components/ui/SegmentedControl';
import { MonitorMultiSelect } from '@/components/monitors/MonitorMultiSelect';

interface MaintenanceWindowModalProps {
  window?: MaintenanceWindow; // present = edit mode
  onDone: () => void;
  onCancel: () => void;
}

type Repeats = 'once' | 'weekly';

const REPEAT_OPTIONS = [
  { value: 'once', label: 'Once' },
  { value: 'weekly', label: 'Weekly', title: 'On chosen days of the week' },
] as const;

const PREVIEW_COUNT = 5;
const labelClass = 'mb-1.5 block text-ui font-medium text-slate-300';
const hintClass = 'font-normal text-slate-500';

// datetime-local inputs work in the browser's local timezone; the API speaks
// ISO/UTC. Convert at the edges.
function isoToLocalInput(iso: string): string {
  const d = new Date(iso);
  const pad = (n: number) => String(n).padStart(2, '0');
  return `${d.getFullYear()}-${pad(d.getMonth() + 1)}-${pad(d.getDate())}T${pad(d.getHours())}:${pad(d.getMinutes())}`;
}

function localInputToISO(value: string): string {
  return new Date(value).toISOString();
}

function defaultStart(): string {
  return isoToLocalInput(new Date().toISOString());
}

function defaultEnd(): string {
  return isoToLocalInput(new Date(Date.now() + 60 * 60 * 1000).toISOString());
}

/** One occurrence as a line: `17 Nov, 02:00 – 03:30`. */
function occurrenceSpan(o: MaintenanceOccurrence): string {
  const start = new Date(o.starts_at);
  const end = new Date(o.ends_at);
  const sameDay = start.toDateString() === end.toDateString();
  return `${formatDateTime(start)} – ${sameDay ? formatClock(end) : formatDateTime(end)}`;
}

export function MaintenanceWindowModal({ window, onDone, onCancel }: MaintenanceWindowModalProps) {
  const saved = window?.recurrence;
  const initialStart = window ? isoToLocalInput(window.starts_at) : defaultStart();
  const initialEnd = window ? isoToLocalInput(window.ends_at) : defaultEnd();
  const initialDuration = splitMinutes(saved?.duration_minutes ?? 60);

  const [title, setTitle] = useState(window?.title ?? '');
  const [description, setDescription] = useState(window?.description ?? '');
  const [repeats, setRepeats] = useState<Repeats>(saved ? 'weekly' : 'once');
  const [startsAt, setStartsAt] = useState(initialStart);
  const [endsAt, setEndsAt] = useState(initialEnd);
  const [weekdays, setWeekdays] = useState<number[]>(saved?.weekdays ?? [new Date().getDay()]);
  const [startTime, setStartTime] = useState(saved?.start_time ?? '02:00');
  const [durationHours, setDurationHours] = useState(String(initialDuration.hours));
  const [durationMinutes, setDurationMinutes] = useState(String(initialDuration.minutes));
  const [timezone, setTimezone] = useState(saved?.timezone ?? browserTimezone());
  const [until, setUntil] = useState(saved?.until ?? '');
  const [selectedIds, setSelectedIds] = useState<string[]>(window?.monitor_ids ?? []);
  const [monitors, setMonitors] = useState<Monitor[]>([]);
  const [groupMembers, setGroupMembers] = useState<Record<string, string[]>>({});
  const [monitorsLoading, setMonitorsLoading] = useState(true);
  const [saving, setSaving] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [preview, setPreview] = useState<MaintenanceOccurrence[] | null>(null);
  const [previewError, setPreviewError] = useState<string | null>(null);
  const timezones = useMemo(() => timezoneOptions(), []);

  useEffect(() => {
    let cancelled = false;
    (async () => {
      try {
        const all = sortMonitorsByName(await getAllMonitors());
        if (cancelled) return;
        setMonitors(all);

        // Membership isn't on the list payload, so pull it per group monitor —
        // it drives the by-group rows and the "covers N monitors" count.
        const groups = all.filter((m) => m.type === 'group');
        const memberEntries = await Promise.all(
          groups.map(async (group) => {
            try {
              const members = await getGroupMembers(group.id);
              return [group.id, members.map((m) => m.id)] as const;
            } catch (err) {
              console.error(`Failed to load members of group ${group.id}:`, err);
              return [group.id, []] as const;
            }
          })
        );
        if (!cancelled) setGroupMembers(Object.fromEntries(memberEntries));
      } catch (err) {
        console.error('Failed to load monitors:', err);
      } finally {
        if (!cancelled) setMonitorsLoading(false);
      }
    })();
    return () => {
      cancelled = true;
    };
  }, []);

  const rule: MaintenanceRecurrence = useMemo(
    () => ({
      frequency: 'weekly',
      weekdays,
      start_time: startTime,
      duration_minutes: (Number(durationHours) || 0) * 60 + (Number(durationMinutes) || 0),
      timezone: timezone.trim(),
      ...(until ? { until } : {}),
    }),
    [weekdays, startTime, durationHours, durationMinutes, timezone, until]
  );
  const ruleError = repeats === 'weekly' ? recurrenceInputError(rule) : null;
  // An unchanged rule on an existing window shows its real occurrences
  // (skips included); anything else is a preview of the rule as typed.
  const showSaved = !!window && !!saved && sameRecurrence(saved, rule);

  useEffect(() => {
    if (repeats !== 'weekly' || ruleError) {
      setPreview(null);
      setPreviewError(null);
      return;
    }
    let cancelled = false;
    const timer = setTimeout(async () => {
      try {
        const occurrences = showSaved && window
          ? (await getMaintenanceOccurrences(window.id, { status: 'upcoming', page_size: PREVIEW_COUNT })).items
          : (await previewMaintenanceRecurrence(rule, PREVIEW_COUNT)).occurrences;
        if (!cancelled) {
          setPreview(occurrences);
          setPreviewError(null);
        }
      } catch (err) {
        if (!cancelled) {
          setPreview(null);
          setPreviewError(errorMessage(err, 'Could not preview the schedule'));
        }
      }
    }, 300);
    return () => {
      cancelled = true;
      clearTimeout(timer);
    };
  }, [repeats, rule, ruleError, showSaved, window]);

  const toggleDay = (day: number) =>
    setWeekdays((days) => (days.includes(day) ? days.filter((d) => d !== day) : [...days, day]));

  const endBeforeStart = startsAt !== '' && endsAt !== '' && new Date(endsAt) <= new Date(startsAt);
  const timesValid = startsAt !== '' && endsAt !== '' && new Date(endsAt) > new Date(startsAt);
  const canSave =
    !saving &&
    title.trim() !== '' &&
    selectedIds.length > 0 &&
    (repeats === 'weekly' ? !ruleError : timesValid);

  const save = async () => {
    setSaving(true);
    setError(null);
    try {
      const payload: CreateMaintenanceWindowRequest = { title: title.trim(), description, monitor_ids: selectedIds };
      if (repeats === 'weekly') {
        payload.recurrence = rule;
      } else if (!window || saved) {
        // New, or a repeating window made a one-off: both times, and the
        // rule cleared.
        payload.starts_at = localInputToISO(startsAt);
        payload.ends_at = localInputToISO(endsAt);
        if (saved) payload.recurrence = { frequency: 'none' };
      } else {
        // Send only times that changed, so a title edit leaves the schedule
        // (and the running occurrence) exactly as it is.
        if (startsAt !== initialStart) payload.starts_at = localInputToISO(startsAt);
        if (endsAt !== initialEnd) payload.ends_at = localInputToISO(endsAt);
      }
      if (window) {
        await updateMaintenanceWindow(window.id, payload);
      } else {
        await createMaintenanceWindow(payload);
      }
      onDone();
    } catch (err: unknown) {
      setError(errorMessage(err, 'Operation failed'));
    } finally {
      setSaving(false);
    }
  };

  return (
    <Dialog
      onClose={onCancel}
      busy={saving}
      size="lg"
      title={window ? 'Edit maintenance window' : 'Schedule maintenance'}
      description="Alerts for the selected monitors are suppressed during the window. Checks keep running."
      footer={
        <>
          <Button variant="ghost" size="sm" onClick={onCancel} disabled={saving}>
            Cancel
          </Button>
          <Button variant="accent" size="sm" onClick={save} disabled={!canSave} loading={saving}>
            {window ? 'Save changes' : 'Schedule'}
          </Button>
        </>
      }
    >
      <div>
        <label className={labelClass} htmlFor="mw-title">
          Title
        </label>
        <input
          id="mw-title"
          type="text"
          value={title}
          onChange={(e) => setTitle(e.target.value)}
          placeholder="Database upgrade"
          className="input"
        />
      </div>

      <div>
        <label className={labelClass} htmlFor="mw-description">
          Description <span className={hintClass}>(shown on status pages)</span>
        </label>
        <textarea
          id="mw-description"
          value={description}
          onChange={(e) => setDescription(e.target.value)}
          rows={2}
          placeholder="Optional details for status page viewers"
          className="input resize-none"
        />
      </div>

      <div>
        <span id="mw-repeats" className={labelClass}>
          Repeats
        </span>
        <SegmentedControl
          options={REPEAT_OPTIONS}
          value={repeats}
          onChange={setRepeats}
          labelledBy="mw-repeats"
          equalWidth
        />
      </div>

      {repeats === 'once' ? (
        <>
          <div className="grid grid-cols-2 gap-3">
            <div>
              <label className={labelClass} htmlFor="mw-start">
                Starts
              </label>
              <input
                id="mw-start"
                type="datetime-local"
                value={startsAt}
                onChange={(e) => setStartsAt(e.target.value)}
                className="input h-10"
              />
            </div>
            <div>
              <label className={labelClass} htmlFor="mw-end">
                Ends
              </label>
              <input
                id="mw-end"
                type="datetime-local"
                value={endsAt}
                onChange={(e) => setEndsAt(e.target.value)}
                className="input h-10"
                aria-invalid={endBeforeStart || undefined}
                aria-describedby={endBeforeStart ? 'mw-end-error' : undefined}
              />
            </div>
          </div>
          {endBeforeStart && (
            <p id="mw-end-error" role="alert" className="text-xs text-rose-300">End time must be after start time.</p>
          )}
        </>
      ) : (
        <>
          <ChipGroup label="On" ariaLabel="Days of the week">
            {WEEKDAY_OPTIONS.map((day) => (
              <FilterChip
                key={day.value}
                selected={weekdays.includes(day.value)}
                onClick={() => toggleDay(day.value)}
                title={day.title}
              >
                {day.label}
              </FilterChip>
            ))}
          </ChipGroup>

          <div className="grid grid-cols-2 gap-3 sm:grid-cols-3">
            <div>
              <label className={labelClass} htmlFor="mw-start-time">
                Start time
              </label>
              <input
                id="mw-start-time"
                type="time"
                step={60}
                value={startTime}
                onChange={(e) => setStartTime(e.target.value)}
                className="input h-10"
              />
            </div>
            <div className="sm:col-span-2">
              <span id="mw-duration" className={labelClass}>
                Duration
              </span>
              <div className="flex items-center gap-2" role="group" aria-labelledby="mw-duration">
                <input
                  type="number"
                  min={0}
                  max={MAX_RECURRING_DURATION_MINUTES / 60}
                  value={durationHours}
                  onChange={(e) => setDurationHours(e.target.value)}
                  className="input h-10 w-20"
                  aria-label="Hours"
                />
                <span className="text-xs text-slate-500">h</span>
                <input
                  type="number"
                  min={0}
                  max={59}
                  value={durationMinutes}
                  onChange={(e) => setDurationMinutes(e.target.value)}
                  className="input h-10 w-20"
                  aria-label="Minutes"
                />
                <span className="text-xs text-slate-500">min</span>
              </div>
            </div>
          </div>

          <div className="grid grid-cols-2 gap-3">
            <div>
              <label className={labelClass} htmlFor="mw-timezone">
                Timezone <span className={hintClass}>(days and start time)</span>
              </label>
              <input
                id="mw-timezone"
                list="mw-timezones"
                value={timezone}
                onChange={(e) => setTimezone(e.target.value)}
                className="input h-10"
                spellCheck={false}
              />
              <datalist id="mw-timezones">
                {timezones.map((tz) => (
                  <option key={tz} value={tz} />
                ))}
              </datalist>
            </div>
            <div>
              <label className={labelClass} htmlFor="mw-until">
                Repeat until <span className={hintClass}>(optional)</span>
              </label>
              <input
                id="mw-until"
                type="date"
                value={until}
                onChange={(e) => setUntil(e.target.value)}
                className="input h-10"
              />
            </div>
          </div>

          <div className="glass-well px-3 py-2.5" aria-live="polite">
            <p className="text-xs text-slate-300">{ruleError ?? describeRecurrence(rule)}</p>
            {!ruleError && (
              <>
                <p className="mt-2 text-2xs font-medium uppercase tracking-wide text-slate-500">
                  {showSaved ? 'Upcoming occurrences' : 'Next occurrences'} · your time ({timezoneLabel('local')})
                </p>
                {previewError ? (
                  <p className="mt-1 text-xs text-rose-300">{previewError}</p>
                ) : preview === null ? (
                  <p className="mt-1 text-xs text-slate-500">Working out the schedule…</p>
                ) : preview.length === 0 ? (
                  <p className="mt-1 text-xs text-slate-500">No occurrence ahead.</p>
                ) : (
                  <ul className="mt-1 space-y-0.5">
                    {preview.map((o) => (
                      <li key={o.starts_at} className="flex items-center gap-2 text-xs tabular-nums text-slate-300">
                        <span className={o.status === 'skipped' ? 'text-slate-500 line-through' : ''}>{occurrenceSpan(o)}</span>
                        {o.status === 'skipped' && <Pill tone="neutral" size="xs">Skipped</Pill>}
                        {o.status === 'active' && <Pill tone="info" size="xs" dot>In progress</Pill>}
                      </li>
                    ))}
                  </ul>
                )}
              </>
            )}
          </div>
          {window && (
            <p className="text-xs text-slate-500">
              Schedule changes apply from now on: occurrences that already started keep their times.
            </p>
          )}
        </>
      )}

      <div>
        <span className={labelClass}>
          Monitors <span className={hintClass}>— pick a group to cover its members</span>
        </span>
        {monitorsLoading ? (
          <div className="glass-well px-2 py-6">
            <p className="text-center text-xs text-slate-500">Loading monitors…</p>
          </div>
        ) : (
          <MonitorMultiSelect
            monitors={monitors}
            selectedIds={selectedIds}
            onChange={setSelectedIds}
            groupMembers={groupMembers}
            maxHeightClass="max-h-72"
            emptyMessage="No monitors available"
          />
        )}
      </div>

      {error && <Callout tone="danger">{error}</Callout>}
    </Dialog>
  );
}
