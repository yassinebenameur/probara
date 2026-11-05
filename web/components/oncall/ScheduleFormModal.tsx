'use client';

import { useEffect, useMemo, useState } from 'react';
import { ArrowDown, ArrowUp, Plus, Trash2, X } from 'lucide-react';
import { createOnCallSchedule, getOnCallPeople, updateOnCallSchedule } from '@/lib/api';
import {
  WEEKDAYS, browserTimezone, isoToZonedLocal, scheduleInputError, timezoneOptions, zonedLocalToISO,
  type OnCallPerson, type OnCallSchedule, type RotationKind, type ScheduleInput,
} from '@/lib/oncall';
import Button from '@/components/ui/Button';
import Callout from '@/components/ui/Callout';
import Dialog from '@/components/ui/Dialog';
import IconButton from '@/components/ui/IconButton';
import SegmentedControl from '@/components/ui/SegmentedControl';

interface Props {
  schedule?: OnCallSchedule; // present = edit mode
  onDone: (s: OnCallSchedule) => void;
  onCancel: () => void;
}

// A rotation as the form edits it: start and end as wall-clock times in the
// schedule's zone (converted on save).
interface RotationDraft {
  key: number;
  name: string;
  kind: RotationKind;
  handoffTime: string;
  weekday: number;
  start: string;
  end: string;
  members: string[];
}

const KIND_OPTIONS = [
  { value: 'weekly' as const, label: 'Weekly' },
  { value: 'daily' as const, label: 'Daily' },
];

let nextKey = 1;

function nextMonday(): string {
  const d = new Date();
  d.setDate(d.getDate() + ((8 - d.getDay()) % 7 || 7));
  const pad = (n: number) => String(n).padStart(2, '0');
  return `${d.getFullYear()}-${pad(d.getMonth() + 1)}-${pad(d.getDate())}T09:00`;
}

function blankRotation(): RotationDraft {
  return { key: nextKey++, name: '', kind: 'weekly', handoffTime: '09:00', weekday: 1, start: nextMonday(), end: '', members: [] };
}

const label = 'mb-1.5 block text-ui font-medium text-slate-300';
const labelHint = 'font-normal text-slate-500';

export function ScheduleFormModal({ schedule, onDone, onCancel }: Props) {
  const [name, setName] = useState(schedule?.name ?? '');
  const [description, setDescription] = useState(schedule?.description ?? '');
  const [timezone, setTimezone] = useState(schedule?.timezone ?? browserTimezone());
  const [rotations, setRotations] = useState<RotationDraft[]>(() =>
    schedule
      ? schedule.rotations.map((r) => ({
          key: nextKey++,
          name: r.name,
          kind: r.kind,
          handoffTime: r.handoff_time,
          weekday: r.handoff_weekday ?? 1,
          start: isoToZonedLocal(r.start_at, schedule.timezone),
          end: r.end_at ? isoToZonedLocal(r.end_at, schedule.timezone) : '',
          members: r.members.map((m) => m.id),
        }))
      : [blankRotation()]
  );
  const [people, setPeople] = useState<OnCallPerson[]>([]);
  const [saving, setSaving] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const timezones = useMemo(() => timezoneOptions(), []);

  useEffect(() => {
    getOnCallPeople().then(setPeople).catch((err: any) => setError(err.message || 'Failed to load people'));
  }, []);

  // Names for members, including ones no longer listed as people.
  const names = useMemo(() => {
    const map = new Map(people.map((p) => [p.id, p.username]));
    for (const r of schedule?.rotations ?? []) for (const m of r.members) if (!map.has(m.id)) map.set(m.id, m.username);
    return map;
  }, [people, schedule]);

  const input: ScheduleInput = {
    name: name.trim(),
    description: description.trim(),
    timezone: timezone.trim() || 'UTC',
    rotations: rotations.map((r) => ({
      name: r.name.trim() || undefined,
      kind: r.kind,
      handoff_time: r.handoffTime,
      handoff_weekday: r.kind === 'weekly' ? r.weekday : undefined,
      start_at: zonedLocalToISO(r.start, timezone.trim() || 'UTC') ?? '',
      end_at: r.end ? zonedLocalToISO(r.end, timezone.trim() || 'UTC') ?? undefined : undefined,
      member_ids: r.members,
    })),
  };
  const validation = scheduleInputError(input);

  const update = (key: number, patch: Partial<RotationDraft>) =>
    setRotations((prev) => prev.map((r) => (r.key === key ? { ...r, ...patch } : r)));

  const move = (key: number, idx: number, delta: number) =>
    setRotations((prev) =>
      prev.map((r) => {
        if (r.key !== key) return r;
        const members = [...r.members];
        const [m] = members.splice(idx, 1);
        members.splice(idx + delta, 0, m);
        return { ...r, members };
      })
    );

  const save = async () => {
    if (validation) {
      setError(validation);
      return;
    }
    setSaving(true);
    setError(null);
    try {
      onDone(schedule ? await updateOnCallSchedule(schedule.id, input) : await createOnCallSchedule(input));
    } catch (err: any) {
      setError(err.message || 'Save failed');
      setSaving(false);
    }
  };

  return (
    <Dialog
      onClose={onCancel}
      busy={saving}
      size="lg"
      title={schedule ? 'Edit schedule' : 'New on-call schedule'}
      description="Members take turns at a local handoff time. Overrides on the schedule page win over every rotation."
      bodyClassName="dashboard-scroll space-y-4"
      footer={
        <>
          <Button variant="ghost" size="sm" onClick={onCancel} disabled={saving}>Cancel</Button>
          <Button variant="accent" size="sm" onClick={save} loading={saving}>
            {schedule ? 'Save changes' : 'Create schedule'}
          </Button>
        </>
      }
    >
      <form noValidate className="space-y-4" onSubmit={(e) => { e.preventDefault(); save(); }}>
        <div className="grid grid-cols-1 gap-3 sm:grid-cols-2">
          <div>
            <label className={label} htmlFor="oncall-name">Name</label>
            <input id="oncall-name" className="input" value={name} maxLength={100} autoFocus
              placeholder="Platform primary" onChange={(e) => setName(e.target.value)} />
          </div>
          <div>
            <label className={label} htmlFor="oncall-timezone">Time zone <span className={labelHint}>(handoff times)</span></label>
            <input id="oncall-timezone" list="oncall-timezones" className="input" value={timezone}
              onChange={(e) => setTimezone(e.target.value)} />
            <datalist id="oncall-timezones">
              {timezones.map((tz) => <option key={tz} value={tz} />)}
            </datalist>
          </div>
        </div>
        <div>
          <label className={label} htmlFor="oncall-description">Description <span className={labelHint}>(optional)</span></label>
          <textarea id="oncall-description" rows={2} maxLength={500} className="input resize-none" value={description}
            onChange={(e) => setDescription(e.target.value)} />
        </div>

        {rotations.map((r, i) => {
          const available = people.filter((p) => !r.members.includes(p.id));
          return (
            <div key={r.key} role="group" aria-label={rotations.length > 1 ? `Layer ${i + 1}` : 'Rotation'} className="glass-well space-y-3 p-4">
              <div className="flex items-center justify-between gap-3">
                <h3 className="text-sm font-medium text-white">
                  {rotations.length > 1 ? `Layer ${i + 1}` : 'Rotation'}
                  {rotations.length > 1 && i === rotations.length - 1 && (
                    <span className={`ml-2 text-xs ${labelHint}`}>wins while active</span>
                  )}
                </h3>
                {rotations.length > 1 && (
                  <IconButton icon={<Trash2 strokeWidth={1.75} />} label="Remove rotation" danger
                    onClick={() => setRotations((prev) => prev.filter((x) => x.key !== r.key))} />
                )}
              </div>
              <div className="grid grid-cols-1 gap-3 sm:grid-cols-2">
                <div>
                  <span className={label}>Handoff</span>
                  <SegmentedControl options={KIND_OPTIONS} value={r.kind} label="Rotation cadence"
                    onChange={(kind) => update(r.key, { kind })} />
                </div>
                <div className="flex gap-2">
                  {r.kind === 'weekly' && (
                    <div className="flex-1">
                      <label className={label} htmlFor={`oncall-day-${r.key}`}>Day</label>
                      <select id={`oncall-day-${r.key}`} className="input" value={r.weekday}
                        onChange={(e) => update(r.key, { weekday: Number(e.target.value) })}>
                        {WEEKDAYS.map((d, n) => <option key={d} value={n}>{d}</option>)}
                      </select>
                    </div>
                  )}
                  <div className="flex-1">
                    <label className={label} htmlFor={`oncall-time-${r.key}`}>Time</label>
                    <input id={`oncall-time-${r.key}`} type="time" className="input" value={r.handoffTime}
                      onChange={(e) => update(r.key, { handoffTime: e.target.value })} />
                  </div>
                </div>
                <div>
                  <label className={label} htmlFor={`oncall-start-${r.key}`}>Starts <span className={labelHint}>({timezone || 'UTC'})</span></label>
                  <input id={`oncall-start-${r.key}`} type="datetime-local" className="input" value={r.start}
                    onChange={(e) => update(r.key, { start: e.target.value })} />
                </div>
                <div>
                  <label className={label} htmlFor={`oncall-end-${r.key}`}>Ends <span className={labelHint}>(optional)</span></label>
                  <input id={`oncall-end-${r.key}`} type="datetime-local" className="input" value={r.end}
                    onChange={(e) => update(r.key, { end: e.target.value })} />
                </div>
              </div>
              <div>
                <span className={label}>Members <span className={labelHint}>— in turn order; the first is on call from the start</span></span>
                {r.members.length > 0 && (
                  <ol className="mb-2 space-y-1">
                    {r.members.map((id, idx) => (
                      <li key={`${id}-${idx}`} className="flex items-center justify-between gap-2 rounded-lg bg-control px-3 py-1.5 text-sm">
                        <span className="min-w-0 truncate text-slate-200">
                          <span className="mr-2 tabular-nums text-slate-500">{idx + 1}.</span>
                          {names.get(id) ?? id}
                        </span>
                        <span className="flex flex-shrink-0 items-center gap-0.5">
                          <IconButton icon={<ArrowUp strokeWidth={1.75} />} label="Move up" disabled={idx === 0}
                            onClick={() => move(r.key, idx, -1)} />
                          <IconButton icon={<ArrowDown strokeWidth={1.75} />} label="Move down"
                            disabled={idx === r.members.length - 1} onClick={() => move(r.key, idx, 1)} />
                          <IconButton icon={<X strokeWidth={1.75} />} label="Remove member" danger
                            onClick={() => update(r.key, { members: r.members.filter((_, j) => j !== idx) })} />
                        </span>
                      </li>
                    ))}
                  </ol>
                )}
                <select
                  aria-label="Add member"
                  className="input"
                  value=""
                  onChange={(e) => e.target.value && update(r.key, { members: [...r.members, e.target.value] })}
                >
                  <option value="">Add member…</option>
                  {available.map((p) => (
                    <option key={p.id} value={p.id}>
                      {p.username}{p.verified_contacts === 0 ? ' (no verified contact)' : ''}
                    </option>
                  ))}
                </select>
              </div>
            </div>
          );
        })}

        <Button size="xs" icon={<Plus strokeWidth={1.75} />} onClick={() => setRotations((prev) => [...prev, blankRotation()])}
          disabled={rotations.length >= 10}>
          Add layer
        </Button>

        {error && <Callout tone="danger">{error}</Callout>}
      </form>
    </Dialog>
  );
}
