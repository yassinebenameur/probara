'use client';

import { useEffect, useId, useMemo, useState } from 'react';
import { Plus, X } from 'lucide-react';
import { FOCUS_RING } from '@/components/ui/focus';
import { MetricRule, MetricSeriesInfo } from '@/lib/types';
import { getMonitorMetricSeries } from '@/lib/api';
import {
  METRIC_META,
  isUtilizationMetric,
  metricUnitKind,
} from '@/lib/metrics';
import Button from '@/components/ui/Button';
import IconButton from '@/components/ui/IconButton';
import { MAX_RULES, MetricRuleDraft, canonicalRuleKey, draftsFromRules } from '@/lib/monitor-form/metric-rules';

// Drafts and their parsing live in lib so they can be tested; re-exported
// for the forms that import them from here.
export { draftsFromRules, rulesFromDrafts } from '@/lib/monitor-form/metric-rules';
export type { MetricRuleDraft } from '@/lib/monitor-form/metric-rules';

const DURATION_OPTIONS: Array<{ value: number; label: string }> = [
  { value: 0, label: 'Instant' },
  { value: 60, label: 'Sustained 1m' },
  { value: 300, label: 'Sustained 5m' },
  { value: 900, label: 'Sustained 15m' },
  { value: 1800, label: 'Sustained 30m' },
  { value: 3600, label: 'Sustained 1h' },
];

// Filesystem series carry {device, mode, mountpoint, type} — no state attr.
// {mode: 'rw'} keeps permanently-full read-only mounts (squashfs/snap images,
// macOS system volumes) from paging instantly; matches the server-side
// migration of legacy disk thresholds.
const COMMON_RULES: MetricRule[] = [
  { metric_name: 'system.cpu.utilization', attribute_filters: { state: 'used' }, operator: '>=', threshold: 0.9 },
  { metric_name: 'system.memory.utilization', attribute_filters: { state: 'used' }, operator: '>=', threshold: 0.9 },
  { metric_name: 'system.filesystem.utilization', attribute_filters: { mode: 'rw' }, operator: '>=', threshold: 0.9 },
  { metric_name: 'system.paging.utilization', attribute_filters: { state: 'used' }, operator: '>=', threshold: 0.8 },
];

interface MetricRuleBuilderProps {
  drafts: MetricRuleDraft[];
  onChange: (drafts: MetricRuleDraft[]) => void;
  errors?: Record<number, string>;
  // When editing an existing monitor with data, discovered series feed the
  // metric-name suggestions. Free text is always allowed.
  monitorId?: string;
}

interface MetricNameOption {
  name: string;
  label?: string;
}

export default function MetricRuleBuilder({
  drafts,
  onChange,
  errors,
  monitorId,
}: MetricRuleBuilderProps) {
  const datalistId = useId();
  const [discoveredSeries, setDiscoveredSeries] = useState<MetricSeriesInfo[]>([]);
  const [pendingFilters, setPendingFilters] = useState<Record<number, { key: string; value: string }>>({});

  useEffect(() => {
    if (!monitorId) return;
    let cancelled = false;
    getMonitorMetricSeries(monitorId)
      .then((response) => {
        if (!cancelled) setDiscoveredSeries(response.items || []);
      })
      .catch((err) => {
        console.warn('Failed to load metric series for suggestions:', err);
      });
    return () => {
      cancelled = true;
    };
  }, [monitorId]);

  const metricOptions = useMemo<MetricNameOption[]>(() => {
    const names = new Map<string, MetricNameOption>();
    for (const series of discoveredSeries) {
      if (!names.has(series.metric_name)) {
        names.set(series.metric_name, {
          name: series.metric_name,
          label: METRIC_META[series.metric_name]?.label,
        });
      }
    }
    if (names.size === 0) {
      // No discovered data yet (new monitor): suggest the curated set.
      for (const [name, meta] of Object.entries(METRIC_META)) {
        if (meta.legacyPercent) continue;
        names.set(name, { name, label: meta.label });
      }
    }
    return Array.from(names.values()).sort((a, b) => a.name.localeCompare(b.name));
  }, [discoveredSeries]);

  const updateDraft = (index: number, patch: Partial<MetricRuleDraft>) => {
    onChange(drafts.map((draft, i) => (i === index ? { ...draft, ...patch } : draft)));
  };

  const removeDraft = (index: number) => {
    onChange(drafts.filter((_, i) => i !== index));
    setPendingFilters((prev) => {
      const next = { ...prev };
      delete next[index];
      return next;
    });
  };

  const addDraft = () => {
    onChange([
      ...drafts,
      { metric_name: '', filters: [], operator: '>=', threshold: '', for_duration_seconds: 0 },
    ]);
  };

  const addCommonRules = () => {
    const existing = new Set(
      drafts.map((draft) =>
        canonicalRuleKey(
          draft.metric_name.trim(),
          Object.fromEntries(draft.filters.map((f) => [f.key.trim(), f.value.trim()]))
        )
      )
    );
    const additions = COMMON_RULES.filter(
      (rule) => !existing.has(canonicalRuleKey(rule.metric_name, rule.attribute_filters || {}))
    );
    if (additions.length > 0) {
      onChange([...drafts, ...draftsFromRules(additions)]);
    }
  };

  const setPendingFilter = (index: number, patch: Partial<{ key: string; value: string }>) => {
    setPendingFilters((prev) => {
      const current = prev[index] || { key: '', value: '' };
      return { ...prev, [index]: { ...current, ...patch } };
    });
  };

  const addFilter = (index: number) => {
    const pending = pendingFilters[index];
    if (!pending?.key.trim() || !pending?.value.trim()) return;
    updateDraft(index, {
      filters: [...drafts[index].filters, { key: pending.key.trim(), value: pending.value.trim() }],
    });
    setPendingFilters((prev) => ({ ...prev, [index]: { key: '', value: '' } }));
  };

  const removeFilter = (index: number, filterIndex: number) => {
    updateDraft(index, {
      filters: drafts[index].filters.filter((_, i) => i !== filterIndex),
    });
  };

  return (
    <div className="space-y-3">
      <datalist id={datalistId}>
        {metricOptions.map((option) => (
          <option key={option.name} value={option.name}>
            {option.label}
          </option>
        ))}
      </datalist>

      {drafts.length === 0 && (
        <p className="glass-well px-4 py-3 text-ui text-slate-400">
          No metric rules. Alerts open only for missed heartbeats until you add one.
        </p>
      )}

      {drafts.map((draft, index) => {
        const metricName = draft.metric_name.trim();
        const isUtilization = isUtilizationMetric(metricName);
        const unitKind = metricName ? metricUnitKind(metricName) : 'count';
        const unitSuffix = isUtilization
          ? '%'
          : unitKind === 'bytes'
            ? 'bytes'
            : unitKind === 'seconds'
              ? 's'
              : '';
        const curatedLabel = METRIC_META[metricName]?.label;
        const pending = pendingFilters[index] || { key: '', value: '' };
        const error = errors?.[index];

        return (
          <div
            key={index}
            className={`glass-well space-y-3 p-4 ${error ? 'ring-1 ring-inset ring-rose-500/40' : ''}`}
          >
            <div className="flex flex-wrap items-end gap-2">
              <div className="min-w-[220px] flex-1">
                <label htmlFor={`${datalistId}-${index}-metric`} className="mb-1.5 block text-xs text-slate-400">
                  Metric{curatedLabel ? ` — ${curatedLabel}` : ''}
                </label>
                <input
                  id={`${datalistId}-${index}-metric`}
                  type="text"
                  list={datalistId}
                  value={draft.metric_name}
                  onChange={(e) => updateDraft(index, { metric_name: e.target.value })}
                  placeholder="system.cpu.utilization"
                  className="input input-sm h-9 font-mono"
                />
              </div>
              <div>
                <label htmlFor={`${datalistId}-${index}-op`} className="mb-1.5 block text-xs text-slate-400">Operator</label>
                <select
                  id={`${datalistId}-${index}-op`}
                  value={draft.operator}
                  onChange={(e) => updateDraft(index, { operator: e.target.value as MetricRuleDraft['operator'] })}
                  className="input input-sm h-9"
                >
                  <option value=">=">≥</option>
                  <option value="<=">≤</option>
                </select>
              </div>
              <div className="w-28">
                <label htmlFor={`${datalistId}-${index}-threshold`} className="mb-1.5 block text-xs text-slate-400">
                  Threshold{unitSuffix ? ` (${unitSuffix})` : ''}
                </label>
                <input
                  id={`${datalistId}-${index}-threshold`}
                  type="number"
                  value={draft.threshold}
                  onChange={(e) => updateDraft(index, { threshold: e.target.value })}
                  placeholder={isUtilization ? 'e.g. 90' : 'value'}
                  step="any"
                  className="input input-sm h-9"
                />
              </div>
              <div>
                <label htmlFor={`${datalistId}-${index}-duration`} className="mb-1.5 block text-xs text-slate-400">Duration</label>
                <select
                  id={`${datalistId}-${index}-duration`}
                  value={draft.for_duration_seconds}
                  onChange={(e) => updateDraft(index, { for_duration_seconds: Number(e.target.value) })}
                  className="input input-sm h-9"
                >
                  {DURATION_OPTIONS.map((option) => (
                    <option key={option.value} value={option.value}>{option.label}</option>
                  ))}
                </select>
              </div>
              <IconButton
                icon={<X strokeWidth={1.75} />}
                label="Remove rule"
                danger
                onClick={() => removeDraft(index)}
              />
            </div>

            <div className="flex flex-wrap items-center gap-1.5">
              {draft.filters.map((filter, filterIndex) => (
                <span
                  key={`${filter.key}-${filterIndex}`}
                  className="inline-flex items-center gap-1 rounded-full bg-well-hover py-0.5 pl-2 pr-1 font-mono text-2xs text-slate-200"
                >
                  {filter.key}={filter.value}
                  <button
                    type="button"
                    aria-label={`Remove filter ${filter.key}`}
                    className={`rounded-full p-0.5 text-slate-500 transition-colors hover:text-white ${FOCUS_RING}`}
                    onClick={() => removeFilter(index, filterIndex)}
                  >
                    <X className="h-3 w-3" strokeWidth={1.75} aria-hidden="true" />
                  </button>
                </span>
              ))}
              <input
                type="text"
                value={pending.key}
                onChange={(e) => setPendingFilter(index, { key: e.target.value })}
                aria-label="Filter key"
                placeholder="key"
                className="input input-sm h-7 !w-28 font-mono"
              />
              <input
                type="text"
                value={pending.value}
                onChange={(e) => setPendingFilter(index, { value: e.target.value })}
                onKeyDown={(e) => {
                  if (e.key === 'Enter') {
                    e.preventDefault();
                    addFilter(index);
                  }
                }}
                placeholder="value"
                aria-label="Filter value"
                className="input input-sm h-7 !w-28 font-mono"
              />
              <Button
                variant="ghost"
                size="xs"
                type="button"
                disabled={!pending.key.trim() || !pending.value.trim()}
                onClick={() => addFilter(index)}
              >
                Add filter
              </Button>
              <span className="text-xs text-slate-500">
                No filters = rule applies to every matching series
              </span>
            </div>

            {error && <p data-form-error className="text-xs text-rose-300">{error}</p>}
          </div>
        );
      })}

      <div className="flex flex-wrap items-center gap-2">
        <Button
          variant="ghost"
          size="xs"
          type="button"
          icon={<Plus strokeWidth={1.75} />}
          onClick={addDraft}
          disabled={drafts.length >= MAX_RULES}
        >
          Add rule
        </Button>
        <Button variant="ghost" size="xs" type="button" onClick={addCommonRules}>
          Add common rules
        </Button>
        <span className="text-xs text-slate-500">
          CPU / Memory / Filesystem ≥ 90%, Swap ≥ 80%
        </span>
      </div>
    </div>
  );
}
