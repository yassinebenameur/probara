'use client';

import { ReactNode, useEffect, useState } from 'react';
import { Plus, X } from 'lucide-react';
import Button from '@/components/ui/Button';
import Callout, { type CalloutTone } from '@/components/ui/Callout';
import IconButton from '@/components/ui/IconButton';
import Tabs from '@/components/ui/Tabs';
import { Location } from '@/lib/types';
import { formatLatency } from '@/lib/format';
import { SyntheticEditorMode, SyntheticTestPhase, SyntheticVarKV } from '@/lib/monitor-form/synthetic';
import { FIELD_LABEL } from './form-kit';
import type { SyntheticLiveTest } from './useSyntheticLiveTest';

// Pieces the synthetic API and browser editors share: the Basic/Advanced
// switch, the template well, the variables table, the live-test panel, and
// the copy-on-write list helpers their step/row handlers use.

/** A copy of `list` with `list[idx]` replaced. */
export function withItem<T>(list: T[], idx: number, item: T): T[] {
  const next = [...list];
  next[idx] = item;
  return next;
}

/** A copy of `list` without `list[idx]`. */
export function withoutItem<T>(list: T[], idx: number): T[] {
  const next = [...list];
  next.splice(idx, 1);
  return next;
}

/** A copy of `list` with items `a` and `b` swapped. */
export function swapped<T>(list: T[], a: number, b: number): T[] {
  const next = [...list];
  [next[a], next[b]] = [next[b], next[a]];
  return next;
}

const EDITOR_MODE_TABS = [
  { id: 'basic', label: 'Basic' },
  { id: 'advanced', label: 'Advanced' },
] as const;

/**
 * A synthetic editor's Basic/Advanced mode. Starts Basic and switches to
 * Advanced when the stored (or cloned) config already uses advanced fields.
 * `isAdvanced` must be stable (module-level).
 */
export function useSyntheticEditorMode<C>(initialConfig: C | undefined, isAdvanced: (config: C) => boolean) {
  const [mode, setMode] = useState<SyntheticEditorMode>('basic');
  useEffect(() => {
    if (initialConfig && isAdvanced(initialConfig)) setMode('advanced');
  }, [initialConfig, isAdvanced]);
  return [mode, setMode] as const;
}

/** The Basic/Advanced tabs, with the journey's counts and what Advanced adds. */
export function SyntheticModeHeader({
  mode,
  onModeChange,
  facts,
  description,
}: {
  mode: SyntheticEditorMode;
  onModeChange: (mode: SyntheticEditorMode) => void;
  facts: ReactNode[];
  description: string;
}) {
  return (
    <div>
      <div className="flex flex-wrap items-center justify-between gap-3">
        <Tabs tabs={EDITOR_MODE_TABS} active={mode} onChange={onModeChange} />
        <p className="flex flex-wrap gap-x-3 text-xs tabular-nums text-slate-500">
          {facts.map((fact, i) => (
            <span key={i}>{fact}</span>
          ))}
        </p>
      </div>
      <p className="mt-2 text-xs text-slate-500">{description}</p>
    </div>
  );
}

/** "Quick start templates": one ghost button per starter journey. */
export function SyntheticTemplatesWell({
  description,
  templates,
}: {
  description: string;
  templates: { label: string; onApply: () => void }[];
}) {
  return (
    <div className="glass-well flex flex-wrap items-center justify-between gap-3 p-4">
      <div>
        <p className="text-sm font-medium text-white">Quick start templates</p>
        <p className="text-ui text-slate-400">{description}</p>
      </div>
      <div className="flex flex-wrap gap-2">
        {templates.map((t) => (
          <Button key={t.label} type="button" variant="ghost" size="xs" onClick={t.onApply}>
            {t.label}
          </Button>
        ))}
      </div>
    </div>
  );
}

/** Key/value variables. Removing the last row blanks it instead. */
export function SyntheticVariablesField({
  rows,
  onChange,
  error,
  hint,
}: {
  rows: SyntheticVarKV[];
  onChange: (rows: SyntheticVarKV[]) => void;
  error?: string;
  hint: ReactNode;
}) {
  const setRow = (idx: number, patch: Partial<SyntheticVarKV>) => {
    const next = [...rows];
    next[idx] = { ...next[idx], ...patch };
    onChange(next);
  };
  return (
    <div>
      <label className={`${FIELD_LABEL} mb-1.5`}>Variables (optional)</label>
      {error && <p className="mb-2 text-xs text-rose-300">{error}</p>}
      <div className="space-y-2">
        {rows.map((row, idx) => (
          <div key={`var-${idx}`} className="grid grid-cols-12 gap-2">
            <input
              className="input col-span-5"
              placeholder="Variable name"
              value={row.key}
              onChange={(e) => setRow(idx, { key: e.target.value })}
            />
            <input
              className="input col-span-6"
              placeholder="Variable value"
              value={row.value}
              onChange={(e) => setRow(idx, { value: e.target.value })}
            />
            <div className="col-span-1 flex items-center justify-center">
              <IconButton
                icon={<X strokeWidth={1.75} />}
                label="Remove variable"
                onClick={() => {
                  const next = [...rows];
                  if (next.length === 1) {
                    next[0] = { key: '', value: '' };
                  } else {
                    next.splice(idx, 1);
                  }
                  onChange(next);
                }}
              />
            </div>
          </div>
        ))}
      </div>
      <div className="mt-2">
        <Button
          type="button"
          variant="ghost"
          size="xs"
          icon={<Plus className="h-3.5 w-3.5" strokeWidth={1.75} />}
          onClick={() => onChange([...rows, { key: '', value: '' }])}
        >
          Add variable
        </Button>
      </div>
      <p className="mt-2 text-xs text-slate-500">{hint}</p>
    </div>
  );
}

const resultTone = (phase: SyntheticTestPhase): CalloutTone => {
  switch (phase) {
    case 'success':
      return 'success';
    case 'failure':
      return 'danger';
    case 'error':
    case 'timeout':
      return 'warning';
    default:
      return 'info';
  }
};

/** "Realtime test": runs the saved monitor now and shows the result (useSyntheticLiveTest). */
export function SyntheticLiveTestPanel({
  test,
  saved,
  locationIds,
  locations,
}: {
  test: SyntheticLiveTest;
  saved: boolean;
  locationIds: string[];
  locations: Location[];
}) {
  const { state } = test;
  return (
    <div className="glass-well p-4">
      <div className="flex flex-wrap items-start justify-between gap-3">
        <div>
          <p className="text-sm font-medium text-white">Realtime test</p>
          <p className="text-ui text-slate-400">Runs the saved monitor immediately and streams the latest result state here.</p>
        </div>
        <div className="flex flex-wrap items-center gap-2">
          {locationIds.length > 0 && (
            <select
              aria-label="Run test from location"
              value={test.locationId}
              onChange={(e) => test.setLocationId(e.target.value)}
              className="input input-sm !w-auto"
            >
              <option value="">Run from: all locations</option>
              {locations
                .filter((loc) => locationIds.includes(loc.id))
                .map((loc) => (
                  <option key={loc.id} value={loc.id}>Run from: {loc.name}</option>
                ))}
            </select>
          )}
          <Button type="button" variant="ghost" onClick={test.start} disabled={!saved || test.running}>
            {test.running ? 'Testing...' : 'Test now'}
          </Button>
        </div>
      </div>
      {!saved && <p className="mt-2 text-xs text-amber-300">Save this monitor first to enable realtime tests.</p>}
      {saved && <p className="mt-2 text-xs text-slate-500">Test runs use the currently saved monitor config.</p>}
      {state.phase !== 'idle' && (
        <Callout tone={resultTone(state.phase)} className="mt-3">
          <p>{state.message || 'Running...'}</p>
          {state.result && (
            <p className="mt-1 text-ui">
              Status: <span className="font-medium text-white">{state.result.status}</span>
              {typeof state.result.latency_ms === 'number' && (
                <span className="ml-3 tabular-nums">{formatLatency(state.result.latency_ms)}</span>
              )}
            </p>
          )}
        </Callout>
      )}
    </div>
  );
}
