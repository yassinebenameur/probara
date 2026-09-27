'use client';

import type { InputHTMLAttributes, ReactNode, TextareaHTMLAttributes } from 'react';

// Compact controls shared by MonitorForm's inline editors (HTTP basics, ping,
// DNS, synthetic API/browser). Labels sit above the control; an error replaces
// the hint.

export const BTN_GHOST_SM = 'inline-flex items-center justify-center gap-2 rounded-[12px] border border-white/10 bg-white/[0.04] px-3 py-2 text-xs font-medium text-slate-200 transition-colors hover:bg-white/[0.08] hover:text-white focus:outline-none focus-visible:ring-2 focus-visible:ring-cyan-500/40 disabled:opacity-40 disabled:cursor-not-allowed';

function FieldMessages({ error, hint }: { error?: string; hint?: string }) {
  return (
    <>
      {error && <p className="mt-1 text-xs text-rose-400">{error}</p>}
      {hint && !error && <p className="mt-1 text-xs text-slate-500">{hint}</p>}
    </>
  );
}

type FormInputProps = Omit<InputHTMLAttributes<HTMLInputElement>, 'value' | 'onChange'> & {
  label: string;
  value: string | number;
  onChange: (value: string) => void;
  error?: string;
  hint?: string;
};

export function FormInput({ label, type = 'text', value, onChange, error, hint, ...props }: FormInputProps) {
  return (
    <div>
      <label className="block text-xs font-medium text-slate-400 mb-1.5">{label}</label>
      <input type={type} value={value} onChange={(e) => onChange(e.target.value)} className="input" {...props} />
      <FieldMessages error={error} hint={hint} />
    </div>
  );
}

type FormTextareaProps = Omit<TextareaHTMLAttributes<HTMLTextAreaElement>, 'value' | 'onChange'> & {
  label: string;
  value: string;
  onChange: (value: string) => void;
  error?: string;
  hint?: string;
};

export function FormTextarea({ label, value, onChange, error, hint, rows = 4, ...props }: FormTextareaProps) {
  return (
    <div>
      <label className="block text-xs font-medium text-slate-400 mb-1.5">{label}</label>
      <textarea
        rows={rows}
        value={value}
        onChange={(e) => onChange(e.target.value)}
        className="input min-h-[96px]"
        {...props}
      />
      <FieldMessages error={error} hint={hint} />
    </div>
  );
}

export function FormSelect({
  label,
  value,
  onChange,
  options,
  hint,
  disabled,
}: {
  label: string;
  value: string;
  onChange: (value: string) => void;
  options: { value: string; label: string }[];
  hint?: string;
  disabled?: boolean;
}) {
  return (
    <div>
      <label className="block text-xs font-medium text-slate-400 mb-1.5">{label}</label>
      <select value={value} onChange={(e) => onChange(e.target.value)} disabled={disabled} className="input">
        {options.map((opt) => (
          <option key={opt.value} value={opt.value}>{opt.label}</option>
        ))}
      </select>
      {hint && <p className="mt-1 text-xs text-slate-500">{hint}</p>}
    </div>
  );
}

export function FormToggle({
  label,
  description,
  checked,
  onChange,
}: {
  label: string;
  description?: string;
  checked: boolean;
  onChange: (checked: boolean) => void;
}) {
  return (
    <div className="flex items-center justify-between rounded-lg border border-white/[0.08] bg-slate-800/30 px-4 py-3">
      <div>
        <p className="text-sm font-medium text-white">{label}</p>
        {description && <p className="text-xs text-slate-500">{description}</p>}
      </div>
      <button
        type="button"
        onClick={() => onChange(!checked)}
        className={`relative h-5 w-9 rounded-full transition-colors ${checked ? 'bg-cyan-500' : 'bg-slate-700'}`}
      >
        <span
          className={`absolute top-0.5 left-0.5 h-4 w-4 rounded-full bg-white transition-transform ${checked ? 'translate-x-4' : ''}`}
        />
      </button>
    </div>
  );
}

export function SectionHeader({ title, description }: { title: string; description?: string }) {
  return (
    <div className="mb-4">
      <h3 className="text-sm font-medium text-white">{title}</h3>
      {description && <p className="text-xs text-slate-500 mt-0.5">{description}</p>}
    </div>
  );
}

/** Two-or-more-way switch (Basic/Advanced, Guided/Expert). */
export function SegmentedControl<T extends string>({
  value,
  options,
  onChange,
}: {
  value: T;
  options: { value: T; label: string }[];
  onChange: (value: T) => void;
}) {
  return (
    <div className="inline-flex rounded-lg border border-white/[0.1] bg-slate-900/50 p-1">
      {options.map((opt) => (
        <button
          key={opt.value}
          type="button"
          onClick={() => onChange(opt.value)}
          className={`rounded-md px-3 py-1 text-xs transition-colors ${
            value === opt.value ? 'bg-white/[0.12] text-white' : 'text-slate-400 hover:text-slate-200'
          }`}
        >
          {opt.label}
        </button>
      ))}
    </div>
  );
}

/** Small rounded count/summary chip used in editor headers. */
export function SummaryChip({ children, tone = 'default' }: { children: ReactNode; tone?: 'default' | 'strong' }) {
  return (
    <span
      className={`rounded-full border border-white/[0.1] ${tone === 'strong' ? 'bg-slate-900/60' : 'bg-slate-900/50'} px-2 py-1 text-slate-300`}
    >
      {children}
    </span>
  );
}

/** Move-up / move-down / remove buttons for a row in an ordered list. */
export function ReorderRemoveButtons({
  index,
  count,
  onMove,
  onRemove,
}: {
  index: number;
  count: number;
  onMove: (from: number, to: number) => void;
  onRemove: () => void;
}) {
  return (
    <div className="flex gap-2">
      <button type="button" className={BTN_GHOST_SM} disabled={index === 0} onClick={() => index > 0 && onMove(index, index - 1)}>
        ↑
      </button>
      <button
        type="button"
        className={BTN_GHOST_SM}
        disabled={index === count - 1}
        onClick={() => index < count - 1 && onMove(index, index + 1)}
      >
        ↓
      </button>
      <button type="button" className={BTN_GHOST_SM} onClick={onRemove}>
        Remove
      </button>
    </div>
  );
}

/** Returns a copy of `items` with the entries at `from` and `to` swapped. */
export function swapItems<T>(items: T[], from: number, to: number): T[] {
  const next = [...items];
  [next[from], next[to]] = [next[to], next[from]];
  return next;
}

/** Returns a copy of `items` with `index` replaced by `update(item)`. */
export function updateItem<T>(items: T[], index: number, update: (item: T) => T): T[] {
  return items.map((item, i) => (i === index ? update(item) : item));
}

/** Removes `index`; the last remaining row is reset to `blank()` instead, so the list never empties. */
export function removeKeepingOne<T>(items: T[], index: number, blank: () => T): T[] {
  if (items.length === 1) return [blank()];
  return items.filter((_, i) => i !== index);
}
