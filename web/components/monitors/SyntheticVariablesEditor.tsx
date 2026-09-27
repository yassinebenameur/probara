'use client';

import type { ReactNode } from 'react';
import { type VariableKV, emptyVariableRow } from '@/lib/monitor-form/common';
import { BTN_GHOST_SM, removeKeepingOne, updateItem } from './MonitorFormControls';

/** Key/value rows for a synthetic journey's `{{variables}}`. Always keeps at least one row. */
export default function SyntheticVariablesEditor({
  rows,
  onChange,
  error,
  hint,
}: {
  rows: VariableKV[];
  onChange: (rows: VariableKV[]) => void;
  error?: string;
  hint: ReactNode;
}) {
  const setRow = (idx: number, patch: Partial<VariableKV>) => onChange(updateItem(rows, idx, (row) => ({ ...row, ...patch })));

  return (
    <div>
      <label className="block text-xs font-medium text-slate-400 mb-1.5">Variables (optional)</label>
      {error && <p className="mt-1 text-xs text-rose-400">{error}</p>}
      <div className="space-y-2">
        {rows.map((row, idx) => (
          <div key={idx} className="grid grid-cols-12 gap-2">
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
            <button
              type="button"
              className={`${BTN_GHOST_SM} col-span-1`}
              onClick={() => onChange(removeKeepingOne(rows, idx, emptyVariableRow))}
            >
              ×
            </button>
          </div>
        ))}
      </div>
      <div className="mt-2">
        <button type="button" className={BTN_GHOST_SM} onClick={() => onChange([...rows, emptyVariableRow()])}>
          + Add variable
        </button>
      </div>
      <p className="mt-2 text-xs text-slate-500">{hint}</p>
    </div>
  );
}
