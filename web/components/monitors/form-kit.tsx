'use client';

import { Dispatch, ReactNode, SetStateAction, useEffect, useId, useRef, useState } from 'react';
import { AlertCircle } from 'lucide-react';
import FormField from '@/components/ui/FormField';
import Switch from '@/components/ui/Switch';
import { formatDuration } from '@/lib/format';

// Building blocks shared by every monitor type form, so the forms keep one
// section vocabulary (Target / Check / Schedule / Alerting / Labels & display)
// and one look for the pieces they all have.

/**
 * A form panel whose trailing disclosure rows (CollapsibleSection
 * variant="row") sit under a hairline, so related optional settings share one
 * card instead of stacking as separate one-line panels.
 */
export function FormPanel({
  title,
  children,
  rows,
}: {
  title: string;
  children?: ReactNode;
  rows?: ReactNode;
}) {
  const headingId = useId();
  return (
    <section aria-labelledby={headingId} className={`glass px-6 pt-6 ${rows ? 'pb-2' : 'pb-6'}`}>
      <h3 id={headingId} className="mb-4 text-title font-semibold text-white">
        {title}
      </h3>
      {children && <div className="space-y-4">{children}</div>}
      {rows && <div className={children ? 'mt-4 border-t border-divider' : ''}>{rows}</div>}
    </section>
  );
}

/** Helper text under a control. */
export function FieldHint({ children, tone = 'muted', id }: { children: ReactNode; tone?: 'muted' | 'warning'; id?: string }) {
  return (
    <p id={id} className={`mt-1 text-xs ${tone === 'warning' ? 'text-amber-300' : 'text-slate-500'}`}>
      {children}
    </p>
  );
}

/** A labelled switch row. The label doubles as the switch's accessible name. */
export function ToggleRow({
  title,
  description,
  checked,
  onChange,
  tone,
}: {
  title: string;
  description?: ReactNode;
  checked: boolean;
  onChange: (next: boolean) => void;
  tone?: 'accent' | 'warning';
}) {
  return (
    <div className="flex items-center justify-between gap-4">
      <div className="min-w-0">
        <p className="text-sm font-medium text-slate-200">{title}</p>
        {description && <p className="text-xs text-slate-500">{description}</p>}
      </div>
      <Switch checked={checked} onChange={onChange} label={title} tone={tone} />
    </div>
  );
}

type NumberInputProps = {
  value: number;
  onChange: (value: number) => void;
  min?: number;
  max?: number;
  step?: number;
  className?: string;
  id?: string;
  placeholder?: string;
  'aria-label'?: string;
  'aria-invalid'?: true;
  'aria-describedby'?: string;
};

/**
 * Integer input that keeps what the operator typed. A cleared field stays
 * empty instead of snapping back to a default (which turned "backspace, 5"
 * into "605"); every parseable keystroke commits, and blur restores the last
 * committed value, clamped to min/max.
 */
export function NumberInput({ value, onChange, min, max, step, className = 'input', ...rest }: NumberInputProps) {
  const [draft, setDraft] = useState(String(value));
  const [committed, setCommitted] = useState(value);
  // Follow outside changes (a preset click, a form reset) without clobbering a
  // draft that already parses to the same number.
  if (value !== committed) {
    setCommitted(value);
    if (Number(draft) !== value || draft.trim() === '') setDraft(String(value));
  }

  const clamp = (n: number) => Math.min(Math.max(n, min ?? -Infinity), max ?? Infinity);

  return (
    <input
      {...rest}
      type="number"
      inputMode="numeric"
      value={draft}
      min={min}
      max={max}
      step={step}
      className={className}
      onChange={(e) => {
        const next = e.target.value;
        setDraft(next);
        const n = parseInt(next, 10);
        if (next.trim() !== '' && Number.isFinite(n)) onChange(n);
      }}
      onBlur={() => {
        const n = parseInt(draft, 10);
        const settled = Number.isFinite(n) ? clamp(n) : value;
        setDraft(String(settled));
        if (settled !== value) onChange(settled);
      }}
    />
  );
}

/** `Every 1m` — the cadence segment of a plan line. */
export function everyLabel(seconds: number): string {
  return `Every ${formatDuration(seconds)}`;
}

/** `down after 2 failed checks` — the threshold segment of a plan line. */
export function downAfterLabel(threshold: number): string {
  return `down after ${threshold} failed check${threshold === 1 ? '' : 's'}`;
}

/**
 * The sticky action bar's left side: a plain-language plan of what the
 * monitor will do, as spaced facts, or — after a failed submit — how many
 * fields need fixing, so a click on Save never looks like it did nothing.
 */
export function PlanSummary({
  parts,
  errors,
}: {
  parts: (ReactNode | null | undefined | false)[];
  errors?: Record<string, string | undefined>;
}) {
  const errorCount = errors ? Object.values(errors).filter(Boolean).length : 0;
  if (errorCount > 0) {
    return (
      <span role="status" className="inline-flex items-center gap-1.5 text-rose-300">
        <AlertCircle className="h-3.5 w-3.5 shrink-0" strokeWidth={1.75} aria-hidden="true" />
        {errorCount === 1 ? '1 field needs attention' : `${errorCount} fields need attention`}
      </span>
    );
  }
  const shown = parts.filter((p) => p !== null && p !== undefined && p !== false && p !== '');
  if (shown.length === 0) return null;
  return (
    <span className="flex flex-wrap gap-x-3 gap-y-0.5">
      {shown.map((part, i) => (
        <span key={i} className="min-w-0">
          {part}
        </span>
      ))}
    </span>
  );
}

/** A machine value (host, URL) inside a plan line. */
export function PlanTarget({ children }: { children: ReactNode }) {
  return <span className="break-all font-mono text-slate-400">{children}</span>;
}

/**
 * The closing "Labels & display" panel: tags, plus whatever display options a
 * type has (HTTP's list display values).
 */
export function LabelsSection({
  tags,
  onTagsChange,
  placeholder = 'production, api, critical',
  children,
}: {
  tags: string;
  onTagsChange: (tags: string) => void;
  placeholder?: string;
  children?: ReactNode;
}) {
  return (
    <FormPanel title="Labels & display">
      <FormField label="Tags">
        {(p) => (
          <>
            <input
              {...p}
              type="text"
              value={tags}
              onChange={(e) => onTagsChange(e.target.value)}
              placeholder={placeholder}
              className="input"
            />
            <FieldHint>Comma-separated. Used to filter and group monitors.</FieldHint>
          </>
        )}
      </FormField>
      {children}
    </FormPanel>
  );
}

/** Cross-field rules every form has: the timeout is checked against the interval. */
const DEFAULT_RELATED_FIELDS: Record<string, string[]> = { timeout_seconds: ['interval_seconds'] };

/**
 * Clears a field's error as soon as the operator edits that field (or one it
 * is checked against, via `related`), so the inline message and PlanSummary's
 * "N fields need attention" follow the form rather than the last submit.
 * Error keys are formData keys in every monitor form. `related` must be a
 * stable (module-level) object.
 */
export function useClearErrorsOnEdit<T extends object>(
  formData: T,
  setErrors: Dispatch<SetStateAction<Record<string, string>>>,
  related: Record<string, string[]> = DEFAULT_RELATED_FIELDS
) {
  const previous = useRef(formData);
  useEffect(() => {
    const before = previous.current as Record<string, unknown>;
    const after = formData as Record<string, unknown>;
    previous.current = formData;
    if (before === after) return;
    const changed = new Set(Object.keys(after).filter((key) => after[key] !== before[key]));
    if (changed.size === 0) return;
    setErrors((prev) => {
      let next = prev;
      for (const key of Object.keys(prev)) {
        if (!prev[key]) continue;
        if ([key, ...(related[key] ?? [])].some((field) => changed.has(field))) {
          if (next === prev) next = { ...prev };
          next[key] = '';
        }
      }
      return next;
    });
  }, [formData, setErrors, related]);
}

/**
 * Keeps the form's `enabled` in step with the saved monitor. The detail
 * page's header Pause/Resume changes it while the Settings form stays
 * mounted; without this the next save would send the stale value and quietly
 * undo the pause.
 */
export function useFollowEnabled<T extends { enabled: boolean }>(
  monitorEnabled: boolean | undefined,
  setFormData: Dispatch<SetStateAction<T>>
) {
  useEffect(() => {
    if (monitorEnabled === undefined) return;
    setFormData((prev) => (prev.enabled === monitorEnabled ? prev : { ...prev, enabled: monitorEnabled }));
  }, [monitorEnabled, setFormData]);
}

/**
 * The monitor forms are noValidate so their own rules render inline, which
 * also switches off the browser's min/max/step/pattern checks that some
 * fields rely on (SIP status 100–699, Redis DB 0–15). This puts those back:
 * the first visible control breaking one gets the browser's message and
 * focus, and the caller stops the submit. Empty required fields are left to
 * each form's own inline rules.
 */
export function reportConstraintViolation(form: HTMLFormElement | null): boolean {
  if (!form) return false;
  const invalid = Array.from(
    form.querySelectorAll<HTMLInputElement | HTMLSelectElement | HTMLTextAreaElement>('input, select, textarea')
  ).find(
    (el) =>
      !el.disabled &&
      !el.validity.valid &&
      !el.validity.valueMissing &&
      !el.closest('[inert]') &&
      !el.closest('[hidden]')
  );
  if (!invalid) return false;
  invalid.reportValidity();
  return true;
}

const ERROR_SELECTOR = '[aria-invalid="true"], [data-form-error], [id$="-error"]';
const CONTROL_SELECTOR =
  'input:not([type="hidden"]):not([disabled]), select:not([disabled]), textarea:not([disabled]), button:not([disabled])';

/**
 * After a failed submit: scroll to the first field with an error and focus
 * its control. Callers open any collapsed section holding an error in the
 * same update; the two frames let React commit that (collapsed content is
 * inert, so it can't take focus until then).
 */
export function revealFirstError(root: HTMLElement | null) {
  if (!root || typeof window === 'undefined') return;
  requestAnimationFrame(() =>
    requestAnimationFrame(() => {
      const first = Array.from(root.querySelectorAll<HTMLElement>(ERROR_SELECTOR)).find(
        (el) => !el.closest('[inert]') && !el.closest('[hidden]')
      );
      if (!first) return;
      let control: HTMLElement | null = first.matches(CONTROL_SELECTOR) ? first : null;
      // An error message: look for the control in its field, then a level up.
      for (let node = first.parentElement, depth = 0; !control && node && depth < 3; node = node.parentElement, depth++) {
        control = node.querySelector<HTMLElement>(CONTROL_SELECTOR);
      }
      const reduceMotion = window.matchMedia('(prefers-reduced-motion: reduce)').matches;
      (control ?? first).scrollIntoView({ block: 'center', behavior: reduceMotion ? 'auto' : 'smooth' });
      control?.focus({ preventScroll: true });
    })
  );
}

/** The label style of the plain field helpers below. */
export const FIELD_LABEL = 'block text-ui font-medium text-slate-300';

/** A picked tile (type picker, guided journey). Selected is neutral raised glass, never orange. */
export const TILE_SELECTED = 'bg-glass-strong shadow-segment';

// Plain labelled controls for the fields MonitorForm's inline editors draw
// (name, host, DNS, synthetic steps). Each links its label to the control and
// marks it invalid, so a failed submit can find and focus the first broken
// field (see revealFirstError).
function useFieldIds(error?: string, hint?: string) {
  const id = useId();
  const errorId = `${id}-error`;
  const hintId = `${id}-hint`;
  return {
    id,
    errorId,
    hintId,
    control: {
      id,
      'aria-invalid': error ? (true as const) : undefined,
      'aria-describedby': error ? errorId : hint ? hintId : undefined,
    },
  };
}

export function FormInput({
  label,
  type = 'text',
  value,
  onChange,
  placeholder,
  error,
  hint,
  required,
  ...props
}: {
  label: string;
  type?: string;
  value: string | number;
  onChange: (value: string) => void;
  placeholder?: string;
  error?: string;
  hint?: string;
  required?: boolean;
  [key: string]: any;
}) {
  const f = useFieldIds(error, hint);
  return (
    <div>
      <label htmlFor={f.id} className={`${FIELD_LABEL} mb-1.5`}>
        {label}
        {required && <span className="ml-1 text-rose-300" aria-hidden="true">*</span>}
      </label>
      <input
        type={type}
        value={value}
        onChange={(e) => onChange(e.target.value)}
        placeholder={placeholder}
        className="input"
        // The asterisk is aria-hidden; this is the required cue for AT.
        aria-required={required || undefined}
        {...f.control}
        {...props}
      />
      {error && <p id={f.errorId} className="mt-1 text-xs text-rose-300">{error}</p>}
      {hint && !error && <p id={f.hintId} className="mt-1 text-xs text-slate-500">{hint}</p>}
    </div>
  );
}

export function FormTextarea({
  label,
  value,
  onChange,
  placeholder,
  error,
  hint,
  rows = 4,
  ...props
}: {
  label: string;
  value: string;
  onChange: (value: string) => void;
  placeholder?: string;
  error?: string;
  hint?: string;
  rows?: number;
  [key: string]: any;
}) {
  const f = useFieldIds(error, hint);
  return (
    <div>
      <label htmlFor={f.id} className={`${FIELD_LABEL} mb-1.5`}>{label}</label>
      <textarea
        rows={rows}
        value={value}
        onChange={(e) => onChange(e.target.value)}
        placeholder={placeholder}
        className="input min-h-[96px]"
        {...f.control}
        {...props}
      />
      {error && <p id={f.errorId} className="mt-1 text-xs text-rose-300">{error}</p>}
      {hint && !error && <p id={f.hintId} className="mt-1 text-xs text-slate-500">{hint}</p>}
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
  const f = useFieldIds(undefined, hint);
  return (
    <div>
      <label htmlFor={f.id} className={`${FIELD_LABEL} mb-1.5`}>{label}</label>
      <select
        value={value}
        onChange={(e) => onChange(e.target.value)}
        disabled={disabled}
        className="input"
        {...f.control}
      >
        {options.map((opt) => (
          <option key={opt.value} value={opt.value}>{opt.label}</option>
        ))}
      </select>
      {hint && <p id={f.hintId} className="mt-1 text-xs text-slate-500">{hint}</p>}
    </div>
  );
}
