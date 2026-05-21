'use client';

interface SelectCheckboxProps {
  checked: boolean;
  indeterminate?: boolean;
  onChange: () => void;
  disabled?: boolean;
  label?: string;
  size?: 'sm' | 'md';
  className?: string;
}

export default function SelectCheckbox({
  checked,
  indeterminate = false,
  onChange,
  disabled = false,
  label,
  size = 'md',
  className = '',
}: SelectCheckboxProps) {
  const box = size === 'sm' ? 'h-3.5 w-3.5' : 'h-4 w-4';
  const icon = size === 'sm' ? 'h-2 w-2' : 'h-2.5 w-2.5';
  const active = checked || indeterminate;

  return (
    <button
      type="button"
      role="checkbox"
      aria-checked={indeterminate ? 'mixed' : checked}
      aria-label={label}
      disabled={disabled}
      onClick={(event) => {
        event.stopPropagation();
        if (!disabled) onChange();
      }}
      className={`flex ${box} flex-shrink-0 items-center justify-center rounded border transition-all focus:outline-none focus-visible:ring-2 focus-visible:ring-cyan-500/40 ${
        active
          ? 'border-cyan-500 bg-cyan-500'
          : 'border-white/20 bg-slate-900/60 hover:border-white/40'
      } ${disabled ? 'cursor-not-allowed opacity-40' : 'cursor-pointer'} ${className}`}
    >
      {indeterminate ? (
        <svg className={`${icon} text-white`} viewBox="0 0 24 24" fill="currentColor" aria-hidden="true">
          <rect x="5" y="11" width="14" height="2.5" rx="1" />
        </svg>
      ) : checked ? (
        <svg
          className={`${icon} text-white`}
          fill="none"
          viewBox="0 0 24 24"
          stroke="currentColor"
          strokeWidth={3}
          aria-hidden="true"
        >
          <path strokeLinecap="round" strokeLinejoin="round" d="M5 13l4 4L19 7" />
        </svg>
      ) : null}
    </button>
  );
}
