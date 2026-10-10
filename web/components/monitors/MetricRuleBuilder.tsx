'use client';

import { useEffect, useId, useMemo, useState } from 'react';
import { Plus, X } from 'lucide-react';
import { MetricRule, MetricSeriesInfo } from '@/lib/types';
import { getMonitorMetricSeries } from '@/lib/api';
import {
  METRIC_META,
  isUtilizationMetric,
  metricUnitKind,
  percentToRatio,
  ratioToPercent,
} from '@/lib/metrics';
import Button from '@/components/ui/Button';

// Client-side mirror of the server's metric_rules validation.
const MAX_RULES = 50;
const MAX_METRIC_NAME_LENGTH = 255;
const MAX_ATTRIBUTE_FILTERS = 8;
const MAX_ATTRIBUTE_TOKEN_LENGTH = 128;
const MAX_FOR_DURATION_SECONDS = 86400;
const METRIC_NAME_PATTERN = /^[a-zA-Z_][a-zA-Z0-9_./-]*$/;
const FORBIDDEN_ATTRIBUTE_CHARS = /[,}=]/;

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

export interface MetricRuleDraft {
  metric_name: string;
  filters: Array<{ key: string; value: string }>;
  operator: '>=' | '<=';
  // Display units: percent for *.utilization metrics, native otherwise.
  threshold: string;
  for_duration_seconds: number;
}

function thresholdToInput(rule: MetricRule): string {
  if (isUtilizationMetric(rule.metric_name)) {
    return String(ratioToPercent(rule.threshold));
  }
  return String(rule.threshold);
}

export function draftsFromRules(rules?: MetricRule[]): MetricRuleDraft[] {
  return (rules || []).map((rule) => ({
    metric_name: rule.metric_name,
    filters: Object.entries(rule.attribute_filters || {}).map(([key, value]) => ({ key, value })),
    operator: rule.operator,
    threshold: thresholdToInput(rule),
    for_duration_seconds: rule.for_duration_seconds ?? 0,
  }));
}

function canonicalRuleKey(metricName: string, filters: Record<string, string>): string {
  const parts = Object.keys(filters)
    .sort()
    .map((key) => `${key}=${filters[key]}`);
  return `${metricName}{${parts.join(',')}}`;
}

// Parses drafts into MetricRule[] mirroring the server's validation.
// Returns per-row error messages keyed by row index.
export function rulesFromDrafts(drafts: MetricRuleDraft[]): {
  rules: MetricRule[];
  errors: Record<number, string>;
} {
  const rules: MetricRule[] = [];
  const errors: Record<number, string> = {};
  const seen = new Set<string>();

  if (drafts.length > MAX_RULES) {
    errors[MAX_RULES] = `At most ${MAX_RULES} rules are allowed`;
  }

  drafts.forEach((draft, index) => {
    const name = draft.metric_name.trim();
    if (!name) {
      errors[index] = 'Metric name is required';
      return;
    }
    if (name.length > MAX_METRIC_NAME_LENGTH || !METRIC_NAME_PATTERN.test(name)) {
      errors[index] = 'Metric name must match ^[a-zA-Z_][a-zA-Z0-9_./-]*$';
      return;
    }

    if (draft.filters.length > MAX_ATTRIBUTE_FILTERS) {
      errors[index] = `At most ${MAX_ATTRIBUTE_FILTERS} attribute filters per rule`;
      return;
    }
    const filters: Record<string, string> = {};
    for (const filter of draft.filters) {
      const key = filter.key.trim();
      const value = filter.value.trim();
      if (!key || !value) {
        errors[index] = 'Attribute filters need both a key and a value';
        return;
      }
      if (key.length > MAX_ATTRIBUTE_TOKEN_LENGTH || value.length > MAX_ATTRIBUTE_TOKEN_LENGTH) {
        errors[index] = `Attribute keys/values are limited to ${MAX_ATTRIBUTE_TOKEN_LENGTH} characters`;
        return;
      }
      if (FORBIDDEN_ATTRIBUTE_CHARS.test(key) || FORBIDDEN_ATTRIBUTE_CHARS.test(value)) {
        errors[index] = 'Attribute keys/values must not contain "," "}" or "="';
        return;
      }
      if (key in filters) {
        errors[index] = `Duplicate attribute key "${key}"`;
        return;
      }
      filters[key] = value;
    }

    const rawThreshold = draft.threshold.trim();
    const parsed = Number(rawThreshold);
    if (rawThreshold === '' || !Number.isFinite(parsed)) {
      errors[index] = 'Threshold is required';
      return;
    }
    let threshold = parsed;
    if (isUtilizationMetric(name)) {
      if (parsed <= 0 || parsed > 100) {
        errors[index] = 'Utilization thresholds are percentages between 0 and 100';
        return;
      }
      threshold = percentToRatio(parsed);
    }

    if (
      draft.for_duration_seconds < 0 ||
      draft.for_duration_seconds > MAX_FOR_DURATION_SECONDS
    ) {
      errors[index] = `Sustained duration must be between 0 and ${MAX_FOR_DURATION_SECONDS} seconds`;
      return;
    }

    const key = canonicalRuleKey(name, filters);
    if (seen.has(key)) {
      errors[index] = 'Duplicate rule: same metric and attribute filters as another rule';
      return;
    }
    seen.add(key);

    const rule: MetricRule = { metric_name: name, operator: draft.operator, threshold };
    if (Object.keys(filters).length > 0) rule.attribute_filters = filters;
    if (draft.for_duration_seconds > 0) rule.for_duration_seconds = draft.for_duration_seconds;
    rules.push(rule);
  });

  return { rules, errors };
}

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
        <p className="rounded-lg border border-white/[0.06] bg-slate-900/40 px-4 py-3 text-xs text-slate-500">
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
            className={`space-y-3 rounded-lg border px-4 py-3 ${
              error ? 'border-rose-500/40 bg-rose-500/5' : 'border-white/[0.06] bg-slate-900/40'
            }`}
          >
            <div className="flex flex-wrap items-end gap-2">
              <div className="min-w-[220px] flex-1">
                <label className="mb-1 block text-[11px] text-slate-500">
                  Metric{curatedLabel ? ` — ${curatedLabel}` : ''}
                </label>
                <input
                  type="text"
                  list={datalistId}
                  value={draft.metric_name}
                  onChange={(e) => updateDraft(index, { metric_name: e.target.value })}
                  placeholder="system.cpu.utilization"
                  className="input input-xs w-full font-mono"
                />
              </div>
              <div>
                <label className="mb-1 block text-[11px] text-slate-500">Operator</label>
                <select
                  value={draft.operator}
                  onChange={(e) => updateDraft(index, { operator: e.target.value as MetricRuleDraft['operator'] })}
                  className="input input-xs"
                >
                  <option value=">=">≥</option>
                  <option value="<=">≤</option>
                </select>
              </div>
              <div className="w-28">
                <label className="mb-1 block text-[11px] text-slate-500">
                  Threshold{unitSuffix ? ` (${unitSuffix})` : ''}
                </label>
                <input
                  type="number"
                  value={draft.threshold}
                  onChange={(e) => updateDraft(index, { threshold: e.target.value })}
                  placeholder={isUtilization ? 'e.g. 90' : 'value'}
                  step="any"
                  className="input input-xs w-full"
                />
              </div>
              <div>
                <label className="mb-1 block text-[11px] text-slate-500">Duration</label>
                <select
                  value={draft.for_duration_seconds}
                  onChange={(e) => updateDraft(index, { for_duration_seconds: Number(e.target.value) })}
                  className="input input-xs"
                >
                  {DURATION_OPTIONS.map((option) => (
                    <option key={option.value} value={option.value}>{option.label}</option>
                  ))}
                </select>
              </div>
              <Button
                variant="ghost"
                size="xs"
                type="button"
                aria-label="Remove rule"
                onClick={() => removeDraft(index)}
              >
                <X className="h-3.5 w-3.5" strokeWidth={1.75} />
              </Button>
            </div>

            <div className="flex flex-wrap items-center gap-1.5">
              {draft.filters.map((filter, filterIndex) => (
                <span
                  key={`${filter.key}-${filterIndex}`}
                  className="inline-flex items-center gap-1 rounded-full border border-cyan-500/20 bg-cyan-500/10 px-2 py-0.5 font-mono text-[10px] text-cyan-300"
                >
                  {filter.key}={filter.value}
                  <button
                    type="button"
                    aria-label={`Remove filter ${filter.key}`}
                    className="text-cyan-400/70 hover:text-white"
                    onClick={() => removeFilter(index, filterIndex)}
                  >
                    <X className="h-3 w-3" strokeWidth={1.75} />
                  </button>
                </span>
              ))}
              <input
                type="text"
                value={pending.key}
                onChange={(e) => setPendingFilter(index, { key: e.target.value })}
                placeholder="key"
                className="input input-xs w-24 font-mono"
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
                className="input input-xs w-24 font-mono"
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
              <span className="text-[10px] text-slate-600">
                No filters = rule applies to every matching series
              </span>
            </div>

            {error && <p className="text-xs text-rose-400">{error}</p>}
          </div>
        );
      })}

      <div className="flex items-center gap-2">
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
        <span className="text-[10px] text-slate-600">
          CPU / Memory / Filesystem ≥ 90%, Swap ≥ 80%
        </span>
      </div>
    </div>
  );
}
