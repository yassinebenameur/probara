'use client';

import { FOCUS_RING } from './focus';

interface SwitchProps {
  checked: boolean;
  onChange: (next: boolean) => void;
  /** Accessible name when no visible label is associated with the control. */
  label?: string;
  /** `warning` for settings that weaken a check (e.g. skip TLS verification). */
  tone?: 'accent' | 'warning';
  disabled?: boolean;
}

// The knob is a literal white: `bg-white` follows the theme and turns to ink
// in light mode (see tailwind.config.ts). The off track is --switch-off (≥3:1
// against every surface) so an off switch is still visibly a control; the
// hit area extends to 24px tall around the 20px track.
export default function Switch({ checked, onChange, label, tone = 'accent', disabled = false }: SwitchProps) {
  const on = tone === 'warning' ? 'bg-amber-500' : 'bg-cyan-500';
  return (
    <button
      type="button"
      role="switch"
      aria-checked={checked}
      aria-label={label}
      disabled={disabled}
      onClick={() => onChange(!checked)}
      className={`group/switch relative h-5 w-9 shrink-0 rounded-full transition-colors duration-base ease-standard before:absolute before:-inset-y-0.5 before:inset-x-0 before:content-[''] ${FOCUS_RING} disabled:cursor-not-allowed disabled:opacity-40 ${
        checked ? on : 'bg-[var(--switch-off,rgb(var(--tw-slate-500)))]'
      }`}
    >
      <span
        className={`absolute left-0.5 top-0.5 h-4 w-4 rounded-full bg-[#fff] shadow-[0_1px_2px_rgb(0_0_0/0.25)] transition-[transform,width] duration-base ease-emphasized group-active/switch:w-5 motion-reduce:group-active/switch:w-4 ${
          checked ? 'translate-x-4 group-active/switch:translate-x-3 motion-reduce:group-active/switch:translate-x-4' : ''
        }`}
      />
    </button>
  );
}
