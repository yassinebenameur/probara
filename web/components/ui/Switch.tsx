'use client';

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
// in light mode (see tailwind.config.ts).
export default function Switch({ checked, onChange, label, tone = 'accent', disabled = false }: SwitchProps) {
  const on = tone === 'warning' ? 'bg-amber-500' : 'bg-cyan-500';
  return (
    <button
      type="button"
      aria-pressed={checked}
      aria-label={label}
      disabled={disabled}
      onClick={() => onChange(!checked)}
      className={`relative h-5 w-9 shrink-0 rounded-full transition-colors focus:outline-none focus-visible:ring-2 focus-visible:ring-cyan-500/50 disabled:cursor-not-allowed disabled:opacity-40 ${
        checked ? on : 'bg-slate-600'
      }`}
    >
      <span
        className={`absolute left-0.5 top-0.5 h-4 w-4 rounded-full bg-[#fff] shadow-[0_1px_2px_rgb(0_0_0/0.25)] transition-transform ${
          checked ? 'translate-x-4' : ''
        }`}
      />
    </button>
  );
}
