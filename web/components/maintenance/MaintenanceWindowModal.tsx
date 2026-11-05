'use client';

import { useEffect, useMemo, useState } from 'react';
import { createMaintenanceWindow, getGroupMembers, previewMaintenanceWindow, updateMaintenanceWindow } from '@/lib/api';
import type {
  MaintenanceFrequency,
  MaintenanceOccurrence,
  MaintenanceRecurrence,
  MaintenanceWindow,
  Monitor,
} from '@/lib/types';
import { getAllMonitors, sortMonitorsByName } from '@/lib/monitor-list';
import { formatDateTimeInZone, formatDuration } from '@/lib/format';
import {
  browserTimeZone,
  FREQUENCY_LABELS,
  isoToZonedWall,
  isRepeating,
  timeZoneOptions,
  WEEKDAY_ORDER,
  WEEKDAY_SHORT,
  zonedWallToISO,
} from '@/lib/maintenance-schedule';
import Button from '@/components/ui/Button';
import Callout from '@/components/ui/Callout';
import ChipGroup from '@/components/ui/ChipGroup';
import Dialog from '@/components/ui/Dialog';
import FilterChip from '@/components/ui/FilterChip';
import SegmentedControl from '@/components/ui/SegmentedControl';
import { MonitorMultiSelect } from '@/components/monitors/MonitorMultiSelect';

interface MaintenanceWindowModalProps {
  window?: MaintenanceWindow; // present = edit mode
  onDone: () => void;
  onCancel: () => void;
}

const labelClass = 'mb-1.5 block text-ui font-medium text-slate-300';

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

const FREQUENCIES: MaintenanceFrequency[] = ['none', 'daily', 'weekly', 'monthly', 'cron'];
const FREQUENCY_OPTIONS = FREQUENCIES.map((value) => ({ value, label: FREQUENCY_LABELS[value] }));
const UNIT: Partial<Record<MaintenanceFrequency, [string, string]>> = {
  daily: ['day', 'days'],
  weekly: ['week', 'weeks'],
  monthly: ['month', 'months'],
};
const PREVIEW_COUNT = 5;
const PREVIEW_DEBOUNCE_MS = 400;

/** Day of the week (0 = Sunday) and day of the month of a `YYYY-MM-DDTHH:mm` wall time. */
function wallDate(wall: string): { weekday: number; day: number } | null {
  const m = /^(\d{4})-(\d{2})-(\d{2})/.exec(wall);
  if (!m) return null;
  const date = new Date(Date.UTC(Number(m[1]), Number(m[2]) - 1, Number(m[3])));
  return { weekday: date.getUTCDay(), day: date.getUTCDate() };
}

export function MaintenanceWindowModal({ window, onDone, onCancel }: MaintenanceWindowModalProps) {
  const rule = window?.recurrence;
  const repeatingAtOpen = isRepeating(rule);
  const [title, setTitle] = useState(window?.title ?? '');
  const [description, setDescription] = useState(window?.description ?? '');
  // One-off windows use the browser's zone (as before); a repeating rule's
  // times are wall-clock in its own zone, so its inputs are read there.
  const [frequency, setFrequency] = useState<MaintenanceFrequency>(rule?.frequency ?? 'none');
  const [timeZone, setTimeZone] = useState(repeatingAtOpen && rule?.timezone ? rule.timezone : browserTimeZone());
  const [startsAt, setStartsAt] = useState(
    window ? (repeatingAtOpen ? isoToZonedWall(window.starts_at, timeZone) : isoToLocalInput(window.starts_at)) : defaultStart()
  );
  const [endsAt, setEndsAt] = useState(window ? isoToLocalInput(window.ends_at) : defaultEnd());
  const [durationMinutes, setDurationMinutes] = useState(
    window ? Math.max(1, Math.round((Date.parse(window.ends_at) - Date.parse(window.starts_at)) / 60000)) : 60
  );
  const [interval, setIntervalN] = useState(rule?.interval ?? 1);
  const [weekdays, setWeekdays] = useState<number[]>(rule?.weekdays ?? []);
  const [monthDay, setMonthDay] = useState(rule?.month_day ?? 0);
  const [cron, setCron] = useState(rule?.cron ?? '');
  const [untilDate, setUntilDate] = useState(rule?.until ? isoToZonedWall(rule.until, timeZone).slice(0, 10) : '');
  const [preview, setPreview] = useState<MaintenanceOccurrence[] | null>(null);
  const [previewError, setPreviewError] = useState<string | null>(null);
  const [selectedIds, setSelectedIds] = useState<string[]>(window?.monitor_ids ?? []);
  const [monitors, setMonitors] = useState<Monitor[]>([]);
  const [groupMembers, setGroupMembers] = useState<Record<string, string[]>>({});
  const [monitorsLoading, setMonitorsLoading] = useState(true);
  const [saving, setSaving] = useState(false);
  const [error, setError] = useState<string | null>(null);

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

  const repeating = frequency !== 'none';
  const timeZones = useMemo(() => timeZoneOptions(timeZone), [timeZone]);

  // Switching to weekly/monthly starts from the first occurrence's own day.
  const chooseFrequency = (next: MaintenanceFrequency) => {
    if (next !== 'none' && frequency === 'none') {
      // Carry the one-off's times over as the first occurrence.
      const start = new Date(startsAt);
      const end = new Date(endsAt);
      if (!Number.isNaN(start.getTime()) && end > start) {
        setStartsAt(isoToZonedWall(start.toISOString(), timeZone));
        setDurationMinutes(Math.max(1, Math.round((end.getTime() - start.getTime()) / 60000)));
      }
    }
    if (next === 'none' && frequency !== 'none') {
      const startISO = zonedWallToISO(startsAt, timeZone);
      if (startISO) {
        setStartsAt(isoToLocalInput(startISO));
        setEndsAt(isoToLocalInput(new Date(Date.parse(startISO) + durationMinutes * 60000).toISOString()));
      }
    }
    const day = wallDate(startsAt);
    if (next === 'weekly' && weekdays.length === 0 && day) setWeekdays([day.weekday]);
    if (next === 'monthly' && monthDay === 0 && day) setMonthDay(day.day);
    setFrequency(next);
  };

  const toggleWeekday = (day: number) =>
    setWeekdays((current) => (current.includes(day) ? current.filter((d) => d !== day) : [...current, day]));

  // The payload's schedule half, or null while the inputs are incomplete.
  const schedule = useMemo((): { starts_at: string; ends_at: string; recurrence: MaintenanceRecurrence } | null => {
    if (!repeating) {
      if (startsAt === '' || endsAt === '' || new Date(endsAt) <= new Date(startsAt)) return null;
      return { starts_at: localInputToISO(startsAt), ends_at: localInputToISO(endsAt), recurrence: { frequency: 'none' } };
    }
    const start = zonedWallToISO(startsAt, timeZone);
    if (!start || !(durationMinutes > 0)) return null;
    const recurrence: MaintenanceRecurrence = { frequency, timezone: timeZone };
    if (frequency !== 'cron') recurrence.interval = Math.max(1, interval || 1);
    if (frequency === 'weekly') recurrence.weekdays = [...weekdays].sort((a, b) => a - b);
    if (frequency === 'monthly' && monthDay > 0) recurrence.month_day = monthDay;
    if (frequency === 'cron') recurrence.cron = cron.trim();
    if (untilDate) recurrence.until = zonedWallToISO(`${untilDate}T23:59`, timeZone);
    return {
      starts_at: start,
      ends_at: new Date(Date.parse(start) + durationMinutes * 60000).toISOString(),
      recurrence,
    };
  }, [repeating, startsAt, endsAt, timeZone, durationMinutes, frequency, interval, weekdays, monthDay, cron, untilDate]);

  // Next occurrences, computed by the API (the one expansion of a rule).
  const previewKey = repeating && schedule ? JSON.stringify(schedule) : '';
  useEffect(() => {
    if (!previewKey) {
      setPreview(null);
      setPreviewError(null);
      return;
    }
    let cancelled = false;
    const timer = setTimeout(async () => {
      try {
        const res = await previewMaintenanceWindow({ ...JSON.parse(previewKey), count: PREVIEW_COUNT });
        if (!cancelled) {
          setPreview(res.occurrences);
          setPreviewError(null);
        }
      } catch (err: unknown) {
        if (!cancelled) {
          setPreview(null);
          setPreviewError(err instanceof Error ? err.message : 'Preview failed');
        }
      }
    }, PREVIEW_DEBOUNCE_MS);
    return () => {
      cancelled = true;
      clearTimeout(timer);
    };
  }, [previewKey]);

  const endBeforeStart = !repeating && startsAt !== '' && endsAt !== '' && new Date(endsAt) <= new Date(startsAt);
  const canSave =
    !saving &&
    title.trim() !== '' &&
    schedule !== null &&
    (frequency !== 'weekly' || weekdays.length > 0) &&
    (frequency !== 'cron' || cron.trim() !== '') &&
    selectedIds.length > 0;

  const save = async () => {
    if (!schedule) return;
    setSaving(true);
    setError(null);
    try {
      const payload = {
        title: title.trim(),
        description,
        ...schedule,
        monitor_ids: selectedIds,
      };
      if (window) {
        await updateMaintenanceWindow(window.id, payload);
      } else {
        await createMaintenanceWindow(payload);
      }
      onDone();
    } catch (err: unknown) {
      const message = err instanceof Error ? err.message : 'Operation failed';
      setError(message);
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
          Description <span className="font-normal text-slate-500">(shown on status pages)</span>
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
        <span className={labelClass} id="mw-frequency-label">
          Repeats
        </span>
        <SegmentedControl
          options={FREQUENCY_OPTIONS}
          value={frequency}
          onChange={chooseFrequency}
          labelledBy="mw-frequency-label"
          wrap
        />
      </div>

      {repeating ? (
        <RecurrenceFields
          frequency={frequency}
          timeZone={timeZone}
          timeZones={timeZones}
          onTimeZone={setTimeZone}
          startsAt={startsAt}
          onStartsAt={setStartsAt}
          durationMinutes={durationMinutes}
          onDurationMinutes={setDurationMinutes}
          interval={interval}
          onInterval={setIntervalN}
          weekdays={weekdays}
          onToggleWeekday={toggleWeekday}
          monthDay={monthDay}
          onMonthDay={setMonthDay}
          cron={cron}
          onCron={setCron}
          untilDate={untilDate}
          onUntilDate={setUntilDate}
          preview={preview}
          previewError={previewError}
          editing={!!window}
        />
      ) : (
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
      )}

      <div>
        <span className={labelClass}>
          Monitors <span className="font-normal text-slate-500">— pick a group to cover its members</span>
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

interface RecurrenceFieldsProps {
  frequency: MaintenanceFrequency;
  timeZone: string;
  timeZones: string[];
  onTimeZone: (zone: string) => void;
  startsAt: string;
  onStartsAt: (wall: string) => void;
  durationMinutes: number;
  onDurationMinutes: (minutes: number) => void;
  interval: number;
  onInterval: (n: number) => void;
  weekdays: number[];
  onToggleWeekday: (day: number) => void;
  monthDay: number;
  onMonthDay: (day: number) => void;
  cron: string;
  onCron: (expr: string) => void;
  untilDate: string;
  onUntilDate: (date: string) => void;
  preview: MaintenanceOccurrence[] | null;
  previewError: string | null;
  editing: boolean;
}

/** The repeating half of the dialog: when it runs, in which zone, and what that means. */
function RecurrenceFields(props: RecurrenceFieldsProps) {
  const { frequency, timeZone } = props;
  const unit = UNIT[frequency];
  return (
    <>
      <div className="grid grid-cols-1 gap-3 sm:grid-cols-2">
        <div>
          <label className={labelClass} htmlFor="mw-first-start">
            {frequency === 'cron' ? 'Active from' : 'First occurrence'}
          </label>
          <input
            id="mw-first-start"
            type="datetime-local"
            value={props.startsAt}
            onChange={(e) => props.onStartsAt(e.target.value)}
            className="input h-10"
          />
        </div>
        <div>
          <label className={labelClass} htmlFor="mw-duration">
            Duration <span className="font-normal text-slate-500">(minutes)</span>
          </label>
          <input
            id="mw-duration"
            type="number"
            min={1}
            value={props.durationMinutes}
            onChange={(e) => props.onDurationMinutes(Number(e.target.value))}
            className="input h-10"
          />
        </div>
      </div>

      <div className="grid grid-cols-1 gap-3 sm:grid-cols-2">
        <div>
          <label className={labelClass} htmlFor="mw-timezone">
            Timezone
          </label>
          <select id="mw-timezone" value={timeZone} onChange={(e) => props.onTimeZone(e.target.value)} className="input h-10">
            {props.timeZones.map((zone) => (
              <option key={zone} value={zone}>
                {zone}
              </option>
            ))}
          </select>
        </div>
        {unit ? (
          <div>
            <label className={labelClass} htmlFor="mw-interval">
              Every
            </label>
            <div className="flex items-center gap-2">
              <input
                id="mw-interval"
                type="number"
                min={1}
                max={366}
                value={props.interval}
                onChange={(e) => props.onInterval(Number(e.target.value))}
                className="input h-10 w-24"
              />
              <span className="text-ui text-slate-400">{props.interval === 1 ? unit[0] : unit[1]}</span>
            </div>
          </div>
        ) : (
          <div>
            <label className={labelClass} htmlFor="mw-cron">
              Cron expression
            </label>
            <input
              id="mw-cron"
              type="text"
              value={props.cron}
              onChange={(e) => props.onCron(e.target.value)}
              placeholder="0 2 * * 0"
              className="input h-10 font-mono"
              aria-describedby="mw-cron-help"
            />
            <p id="mw-cron-help" className="mt-1 text-xs text-slate-500">
              minute hour day-of-month month day-of-week, in the timezone
            </p>
          </div>
        )}
      </div>

      {frequency === 'weekly' && (
        <ChipGroup label="On" className="min-h-8">
          {WEEKDAY_ORDER.map((day) => (
            <FilterChip key={day} selected={props.weekdays.includes(day)} onClick={() => props.onToggleWeekday(day)}>
              {WEEKDAY_SHORT[day]}
            </FilterChip>
          ))}
        </ChipGroup>
      )}

      {frequency === 'monthly' && (
        <div>
          <label className={labelClass} htmlFor="mw-month-day">
            Day of the month
          </label>
          <input
            id="mw-month-day"
            type="number"
            min={1}
            max={31}
            value={props.monthDay || ''}
            onChange={(e) => props.onMonthDay(Number(e.target.value))}
            className="input h-10 w-24"
            aria-describedby="mw-month-day-help"
          />
          <p id="mw-month-day-help" className="mt-1 text-xs text-slate-500">
            Days past the end of a shorter month run on its last day.
          </p>
        </div>
      )}

      <div>
        <label className={labelClass} htmlFor="mw-until">
          Ends on <span className="font-normal text-slate-500">(optional, last day an occurrence may start)</span>
        </label>
        <input
          id="mw-until"
          type="date"
          value={props.untilDate}
          onChange={(e) => props.onUntilDate(e.target.value)}
          className="input h-10 sm:w-1/2"
        />
      </div>

      <div className="glass-well px-3 py-2.5" aria-live="polite">
        <p className="text-xs font-medium text-slate-400">
          Next occurrences <span className="font-normal text-slate-500">({timeZone})</span>
        </p>
        {props.previewError ? (
          <p className="mt-1.5 text-xs text-rose-300">{props.previewError}</p>
        ) : props.preview === null ? (
          <p className="mt-1.5 text-xs text-slate-500">Complete the schedule to see when it runs.</p>
        ) : props.preview.length === 0 ? (
          <p className="mt-1.5 text-xs text-slate-500">No occurrence in the next 400 days.</p>
        ) : (
          <ul className="mt-1.5 space-y-0.5 text-ui tabular-nums text-slate-300">
            {props.preview.map((o) => {
              const start = isoToZonedWall(o.starts_at, timeZone);
              const end = isoToZonedWall(o.ends_at, timeZone);
              // Same day: just the end's clock time.
              const endText = start.slice(0, 10) === end.slice(0, 10) ? end.slice(11, 16) : formatDateTimeInZone(o.ends_at, timeZone);
              return (
                <li key={o.starts_at}>
                  {formatDateTimeInZone(o.starts_at, timeZone)}
                  <span className="text-slate-500">
                    {' '}
                    → {endText} · {formatDuration((Date.parse(o.ends_at) - Date.parse(o.starts_at)) / 1000)}
                  </span>
                </li>
              );
            })}
          </ul>
        )}
        {props.editing && (
          <p className="mt-2 text-xs text-slate-500">
            Saving changes only future occurrences; one in progress keeps its start, and ended ones stay as history.
          </p>
        )}
      </div>
    </>
  );
}
