'use client';

import { useEffect, useMemo, useState } from 'react';
import { X } from 'lucide-react';
import { createSla, getGroupMembers, updateSla } from '@/lib/api';
import type { Monitor } from '@/lib/types';
import { getAllMonitors, sortMonitorsByName } from '@/lib/monitor-list';
import {
  SLA_AGGREGATION_LABELS, SLA_PERIOD_LABELS, SLA_TARGET_PRESETS, formatTarget, slaInputError,
  type Sla, type SlaAggregation, type SlaInput, type SlaPeriod,
} from '@/lib/sla';
import Button from '@/components/ui/Button';
import Callout from '@/components/ui/Callout';
import FilterChip from '@/components/ui/FilterChip';
import IconButton from '@/components/ui/IconButton';
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

  const label = 'mb-1.5 block text-[13px] font-medium text-slate-300';
  const labelHint = 'font-normal text-slate-500';

  return (
    <ModalPortal>
      <div className="glass-scrim fixed inset-0 z-50 flex items-center justify-center p-4">
        <div className="glass-overlay flex max-h-[90vh] w-full max-w-2xl flex-col">
          <div className="flex items-start justify-between gap-4 px-6 pt-5">
            <div>
              <h2 className="text-base font-semibold text-white">{sla ? 'Edit SLA' : 'New SLA'}</h2>
              <p className="mt-1 text-[13px] text-slate-400">
                A target for a set of monitors over calendar periods. Maintenance windows and paused time are excluded.
              </p>
            </div>
            <IconButton icon={<X strokeWidth={1.75} />} label="Close" onClick={onCancel} />
          </div>

          <div className="dashboard-scroll min-h-0 flex-1 space-y-4 overflow-y-auto px-6 py-4">
            <div>
              <label className={label} htmlFor="sla-name">Name</label>
              <input id="sla-name" type="text" value={name} onChange={(e) => setName(e.target.value)}
                placeholder="Checkout platform" className="input" />
            </div>

            <div>
              <label className={label} htmlFor="sla-description">
                Description <span className={labelHint}>(printed on reports)</span>
              </label>
              <textarea id="sla-description" value={description} onChange={(e) => setDescription(e.target.value)}
                rows={2} className="input resize-none" />
            </div>

            <div>
              <label className={label} htmlFor="sla-target">Target availability</label>
              <div className="flex flex-wrap items-center gap-2">
                {SLA_TARGET_PRESETS.map((preset) => (
                  <FilterChip key={preset} selected={Number(target) === preset} onClick={() => setTarget(String(preset))}
                    className="font-mono tabular-nums">
                    {formatTarget(preset)}
                  </FilterChip>
                ))}
                <div className="flex items-center gap-1.5">
                  <input id="sla-target" type="number" step="0.001" min="0" max="99.9999" value={target}
                    onChange={(e) => setTarget(e.target.value)} className="input input-sm h-7 !w-28 font-mono tabular-nums" />
                  <span className="text-[13px] text-slate-400">%</span>
                </div>
              </div>
            </div>

            <div className="grid grid-cols-1 gap-3 sm:grid-cols-2">
              <div>
                <label className={label} htmlFor="sla-period">Reporting period</label>
                <select id="sla-period" value={period} onChange={(e) => setPeriod(e.target.value as SlaPeriod)}
                  className="input">
                  {(Object.keys(SLA_PERIOD_LABELS) as SlaPeriod[]).map((p) => (
                    <option key={p} value={p}>{SLA_PERIOD_LABELS[p]}</option>
                  ))}
                </select>
              </div>
              <div>
                <label className={label} htmlFor="sla-timezone">Timezone <span className={labelHint}>(period boundaries)</span></label>
                <input id="sla-timezone" list="sla-timezones" value={timezone} onChange={(e) => setTimezone(e.target.value)}
                  className="input" />
                <datalist id="sla-timezones">
                  {timezones.map((tz) => <option key={tz} value={tz} />)}
                </datalist>
              </div>
            </div>

            <div>
              <span className={label}>How monitors combine</span>
              <div className="grid grid-cols-1 gap-2 sm:grid-cols-2">
                {(Object.keys(SLA_AGGREGATION_LABELS) as SlaAggregation[]).map((mode) => (
                  <label key={mode} className={`cursor-pointer rounded-xl border p-3 transition-colors has-[:focus-visible]:ring-2 has-[:focus-visible]:ring-cyan-500/50 ${
                    aggregation === mode
                      ? 'border-cyan-500/40 bg-cyan-500/[0.08]'
                      : 'border-edge-control bg-control hover:bg-control-hover'
                  }`}>
                    <input type="radio" name="sla-aggregation" value={mode} checked={aggregation === mode}
                      onChange={() => setAggregation(mode)} className="sr-only" />
                    <div className="text-[13px] font-medium text-white">{SLA_AGGREGATION_LABELS[mode]}</div>
                    <div className="mt-1 text-xs leading-snug text-slate-400">{AGGREGATION_HELP[mode]}</div>
                  </label>
                ))}
              </div>
            </div>

            <label className="flex cursor-pointer items-start gap-2.5 text-sm text-slate-200">
              <input type="checkbox" checked={degradedAsDown} onChange={(e) => setDegradedAsDown(e.target.checked)}
                className="mt-0.5 h-4 w-4 flex-shrink-0 cursor-pointer accent-cyan-500" />
              <span>
                Count degraded time as downtime
                <span className="mt-0.5 block text-xs text-slate-500">
                  Off: a monitor failing in some but not enough locations still counts as available.
                </span>
              </span>
            </label>

            <div>
              <span className={label}>
                Monitors <span className={labelHint}>— a group covers its members</span>
              </span>
              {monitorsLoading ? (
                <div className="glass-well px-2 py-6">
                  <p className="text-center text-xs text-slate-500">Loading monitors…</p>
                </div>
              ) : (
                <MonitorMultiSelect monitors={monitors} selectedIds={selectedIds} onChange={setSelectedIds}
                  groupMembers={groupMembers} maxHeightClass="max-h-60" emptyMessage="No monitors available" />
              )}
            </div>

            <div>
              <span className={label}>
                Tags <span className={labelHint}>— monitors carrying any of them join automatically</span>
              </span>
              <TagFilter tags={tagOptions} selected={tags} onToggle={toggleTag} onClear={() => setTags(new Set())} />
            </div>

            {(error || (validation && (name || selectedIds.length > 0 || tags.size > 0))) && (
              error ? (
                <Callout tone="danger">{error}</Callout>
              ) : (
                <p className="text-xs text-rose-300">{error ?? validation}</p>
              )
            )}
          </div>

          <div className="flex justify-end gap-2 px-6 pb-5">
            <Button variant="ghost" size="sm" onClick={onCancel} disabled={saving}>Cancel</Button>
            <Button variant="accent" size="sm" onClick={save} disabled={saving || validation !== null} loading={saving}>
              {sla ? 'Save changes' : 'Create SLA'}
            </Button>
          </div>
        </div>
      </div>
    </ModalPortal>
  );
}
