'use client';

import { useEffect, useMemo, useState } from 'react';
import { createSla, getGroupMembers, updateSla } from '@/lib/api';
import type { Monitor } from '@/lib/types';
import { getAllMonitors, sortMonitorsByName } from '@/lib/monitor-list';
import {
  SLA_AGGREGATION_LABELS, SLA_PERIOD_LABELS, SLA_TARGET_PRESETS, formatTarget, slaInputError,
  type Sla, type SlaAggregation, type SlaInput, type SlaPeriod,
} from '@/lib/sla';
import Button from '@/components/ui/Button';
import ModalPortal from '@/components/ui/ModalPortal';
import { MonitorMultiSelect } from '@/components/monitors/MonitorMultiSelect';
import TagFilter, { type TagOption } from '@/components/monitors/TagFilter';

interface SlaFormModalProps {
  sla?: Sla; // present = edit mode
  onDone: (sla: Sla) => void;
  onCancel: () => void;
}

function browserTimezone(): string {
  try {
    return Intl.DateTimeFormat().resolvedOptions().timeZone || 'UTC';
  } catch {
    return 'UTC';
  }
}

function timezoneOptions(): string[] {
  const supported = (Intl as unknown as { supportedValuesOf?: (key: string) => string[] }).supportedValuesOf;
  try {
    return supported ? ['UTC', ...supported('timeZone').filter((tz) => tz !== 'UTC')] : ['UTC'];
  } catch {
    return ['UTC'];
  }
}

const AGGREGATION_HELP: Record<SlaAggregation, string> = {
  serial: 'The service counts as down whenever any selected monitor is down. Matches how most contracts are worded.',
  mean: 'The unweighted average of each monitor’s own availability.',
};

export function SlaFormModal({ sla, onDone, onCancel }: SlaFormModalProps) {
  const [name, setName] = useState(sla?.name ?? '');
  const [description, setDescription] = useState(sla?.description ?? '');
  const [target, setTarget] = useState(String(sla?.target_pct ?? 99.9));
  const [aggregation, setAggregation] = useState<SlaAggregation>(sla?.aggregation ?? 'serial');
  const [period, setPeriod] = useState<SlaPeriod>(sla?.period ?? 'monthly');
  const [timezone, setTimezone] = useState(sla?.timezone ?? browserTimezone());
  const [degradedAsDown, setDegradedAsDown] = useState(sla?.degraded_counts_as_down ?? false);
  const [tags, setTags] = useState<Set<string>>(new Set(sla?.tags ?? []));
  const [selectedIds, setSelectedIds] = useState<string[]>(sla?.monitor_ids ?? []);
  const [monitors, setMonitors] = useState<Monitor[]>([]);
  const [groupMembers, setGroupMembers] = useState<Record<string, string[]>>({});
  const [monitorsLoading, setMonitorsLoading] = useState(true);
  const [saving, setSaving] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const timezones = useMemo(() => timezoneOptions(), []);

  useEffect(() => {
    let cancelled = false;
    (async () => {
      try {
        const all = sortMonitorsByName(await getAllMonitors());
        if (cancelled) return;
        setMonitors(all);
        const groups = all.filter((m) => m.type === 'group');
        const memberEntries = await Promise.all(
          groups.map(async (group) => {
            try {
              const members = await getGroupMembers(group.id);
              return [group.id, members.map((m) => m.id)] as const;
            } catch {
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

  const tagOptions: TagOption[] = useMemo(() => {
    const counts = new Map<string, number>();
    for (const m of monitors) for (const t of m.tags ?? []) counts.set(t, (counts.get(t) ?? 0) + 1);
    // Keep stored tags selectable even when no monitor carries them any more.
    for (const t of tags) if (!counts.has(t)) counts.set(t, 0);
    return [...counts].map(([tag, count]) => ({ tag, count }));
  }, [monitors, tags]);

  const input: SlaInput = {
    name: name.trim(),
    description,
    target_pct: Number(target),
    aggregation,
    period,
    timezone: timezone.trim(),
    degraded_counts_as_down: degradedAsDown,
    tags: [...tags],
    monitor_ids: selectedIds,
  };
  const validation = slaInputError(input);

  const save = async () => {
    setSaving(true);
    setError(null);
    try {
      const saved = sla ? await updateSla(sla.id, input) : await createSla(input);
      onDone(saved);
    } catch (err: unknown) {
      setError(err instanceof Error ? err.message : (err as { message?: string })?.message || 'Save failed');
    } finally {
      setSaving(false);
    }
  };

  const toggleTag = (tag: string) =>
    setTags((prev) => {
      const next = new Set(prev);
      if (next.has(tag)) next.delete(tag);
      else next.add(tag);
      return next;
    });

  const label = 'mb-1 block text-xs font-medium text-slate-400';

  return (
    <ModalPortal>
      <div className="fixed inset-0 z-50 flex items-center justify-center bg-slate-950/70 p-4 backdrop-blur-sm">
        <div className="dashboard-scroll max-h-[90vh] w-full max-w-2xl overflow-y-auto rounded-xl border border-white/[0.08] bg-slate-900/95 p-6 shadow-2xl">
          <div className="mb-5 flex items-start justify-between">
            <div>
              <h2 className="text-base font-semibold text-white">{sla ? 'Edit SLA' : 'New SLA'}</h2>
              <p className="mt-1 text-xs text-slate-500">
                A target for a set of monitors over calendar periods. Maintenance windows and paused time are excluded.
              </p>
            </div>
            <button onClick={onCancel} className="text-slate-400 transition-colors hover:text-white" aria-label="Close">
              ×
            </button>
          </div>

          <div className="space-y-4">
            <div>
              <label className={label} htmlFor="sla-name">Name</label>
              <input id="sla-name" type="text" value={name} onChange={(e) => setName(e.target.value)}
                placeholder="Checkout platform" className="input input-sm w-full" />
            </div>

            <div>
              <label className={label} htmlFor="sla-description">
                Description <span className="text-slate-600">(printed on reports)</span>
              </label>
              <textarea id="sla-description" value={description} onChange={(e) => setDescription(e.target.value)}
                rows={2} className="input input-sm w-full resize-none" />
            </div>

            <div>
              <label className={label} htmlFor="sla-target">Target availability</label>
              <div className="flex flex-wrap items-center gap-2">
                {SLA_TARGET_PRESETS.map((preset) => (
                  <button key={preset} type="button" onClick={() => setTarget(String(preset))}
                    className={`rounded-md border px-2.5 py-1 font-mono text-xs transition-colors ${
                      Number(target) === preset
                        ? 'border-cyan-500/50 bg-cyan-500/10 text-cyan-200'
                        : 'border-white/[0.08] text-slate-400 hover:text-white'
                    }`}>
                    {formatTarget(preset)}
                  </button>
                ))}
                <div className="flex items-center gap-1">
                  <input id="sla-target" type="number" step="0.001" min="0" max="99.9999" value={target}
                    onChange={(e) => setTarget(e.target.value)} className="input input-sm !w-28 font-mono" />
                  <span className="text-xs text-slate-500">%</span>
                </div>
              </div>
            </div>

            <div className="grid grid-cols-1 gap-3 sm:grid-cols-2">
              <div>
                <label className={label} htmlFor="sla-period">Reporting period</label>
                <select id="sla-period" value={period} onChange={(e) => setPeriod(e.target.value as SlaPeriod)}
                  className="input input-sm w-full">
                  {(Object.keys(SLA_PERIOD_LABELS) as SlaPeriod[]).map((p) => (
                    <option key={p} value={p}>{SLA_PERIOD_LABELS[p]}</option>
                  ))}
                </select>
              </div>
              <div>
                <label className={label} htmlFor="sla-timezone">Timezone <span className="text-slate-600">(period boundaries)</span></label>
                <input id="sla-timezone" list="sla-timezones" value={timezone} onChange={(e) => setTimezone(e.target.value)}
                  className="input input-sm w-full" />
                <datalist id="sla-timezones">
                  {timezones.map((tz) => <option key={tz} value={tz} />)}
                </datalist>
              </div>
            </div>

            <div>
              <span className={label}>How monitors combine</span>
              <div className="grid grid-cols-1 gap-2 sm:grid-cols-2">
                {(Object.keys(SLA_AGGREGATION_LABELS) as SlaAggregation[]).map((mode) => (
                  <label key={mode} className={`cursor-pointer rounded-lg border p-3 transition-colors ${
                    aggregation === mode ? 'border-cyan-500/40 bg-cyan-500/[0.06]' : 'border-white/[0.06] hover:border-white/[0.12]'
                  }`}>
                    <input type="radio" name="sla-aggregation" value={mode} checked={aggregation === mode}
                      onChange={() => setAggregation(mode)} className="sr-only" />
                    <div className="text-xs font-medium text-white">{SLA_AGGREGATION_LABELS[mode]}</div>
                    <div className="mt-1 text-[11px] leading-snug text-slate-500">{AGGREGATION_HELP[mode]}</div>
                  </label>
                ))}
              </div>
            </div>

            <label className="flex items-start gap-2 text-xs text-slate-300">
              <input type="checkbox" checked={degradedAsDown} onChange={(e) => setDegradedAsDown(e.target.checked)}
                className="mt-0.5" />
              <span>
                Count degraded time as downtime
                <span className="block text-[11px] text-slate-500">
                  Off: a monitor failing in some but not enough locations still counts as available.
                </span>
              </span>
            </label>

            <div>
              <span className={label}>
                Monitors <span className="text-slate-600">— a group covers its members</span>
              </span>
              {monitorsLoading ? (
                <div className="rounded-lg border border-white/[0.06] bg-slate-950/40 px-2 py-6">
                  <p className="text-center text-xs text-slate-500">Loading monitors…</p>
                </div>
              ) : (
                <MonitorMultiSelect monitors={monitors} selectedIds={selectedIds} onChange={setSelectedIds}
                  groupMembers={groupMembers} maxHeightClass="max-h-60" emptyMessage="No monitors available" />
              )}
            </div>

            <div>
              <span className={label}>
                Tags <span className="text-slate-600">— monitors carrying any of them join automatically</span>
              </span>
              <TagFilter tags={tagOptions} selected={tags} onToggle={toggleTag} onClear={() => setTags(new Set())} />
            </div>

            {(error || (validation && (name || selectedIds.length > 0 || tags.size > 0))) && (
              <p className="text-xs text-rose-400">{error ?? validation}</p>
            )}

            <div className="flex justify-end gap-2 pt-1">
              <Button variant="ghost" size="sm" onClick={onCancel} disabled={saving}>Cancel</Button>
              <Button size="sm" onClick={save} disabled={saving || validation !== null} loading={saving}>
                {sla ? 'Save changes' : 'Create SLA'}
              </Button>
            </div>
          </div>
        </div>
      </div>
    </ModalPortal>
  );
}
