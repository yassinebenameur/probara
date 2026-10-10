import { ReactNode } from 'react';

interface ChipGroupProps {
  /** Short visible label ("Status", "Range"); also the group's accessible name. */
  label?: string;
  /** Accessible name when no visible label is wanted. */
  ariaLabel?: string;
  /** Draw a hairline before the group to separate it from the previous one. */
  divider?: boolean;
  className?: string;
  children: ReactNode;
}

/**
 * A run of related FilterChips. Two chip sets side by side (status and
 * routing, say) read as one list unless each is grouped and labelled.
 */
export default function ChipGroup({ label, ariaLabel, divider = false, className = '', children }: ChipGroupProps) {
  return (
    <div
      role="group"
      aria-label={ariaLabel ?? label}
      className={`flex flex-wrap items-center gap-1.5 ${divider ? 'border-l border-divider pl-3' : ''} ${className}`.trim()}
    >
      {label && (
        <span className="mr-0.5 text-2xs font-medium text-slate-500" aria-hidden="true">
          {label}
        </span>
      )}
      {children}
    </div>
  );
}
