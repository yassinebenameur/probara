'use client';

import { ReactNode, useState, useId } from 'react';
import { AlertTriangle, ChevronDown } from 'lucide-react';

export type CollapsibleTone = 'default' | 'warning' | 'danger';

interface CollapsibleSectionProps {
  title: ReactNode;
  /**
   * Shown beside the title while collapsed. A string renders muted; pass a
   * node for richer content. With a `tone` other than default it is coloured
   * and stays visible on small screens too.
   */
  summary?: ReactNode;
  /**
   * `warning` for a setting that weakens the check (skip TLS verification),
   * `danger` for a section holding validation errors. The tone also marks the
   * collapsed header so the state is visible without opening the section.
   */
  tone?: CollapsibleTone;
  /**
   * `panel` (default): its own glass card. `row`: a disclosure row inside an
   * existing panel, separated by a hairline, no glass of its own.
   */
  variant?: 'panel' | 'row';
  defaultOpen?: boolean;
  /** If provided, component runs in controlled mode */
  isOpen?: boolean;
  onToggle?: (open: boolean) => void;
  /** ID placed on the outer div for scroll-targeting */
  sectionId?: string;
  children: ReactNode;
}

const TONE_TEXT: Record<CollapsibleTone, string> = {
  default: 'text-slate-500',
  warning: 'text-amber-300',
  danger: 'text-rose-300',
};

export default function CollapsibleSection({
  title,
  summary,
  tone = 'default',
  variant = 'panel',
  defaultOpen = false,
  isOpen: controlledOpen,
  onToggle,
  sectionId,
  children,
}: CollapsibleSectionProps) {
  const [internalOpen, setInternalOpen] = useState(defaultOpen);
  const controlled = controlledOpen !== undefined;
  const open = controlled ? controlledOpen : internalOpen;

  const handleToggle = () => {
    const next = !open;
    if (controlled) {
      onToggle?.(next);
    } else {
      setInternalOpen(next);
      onToggle?.(next);
    }
  };

  const id = useId();
  const row = variant === 'row';
  const toned = tone !== 'default';

  return (
    <div
      id={sectionId}
      className={row ? 'border-t border-divider first:border-t-0' : 'glass overflow-hidden'}
    >
      <button
        type="button"
        onClick={handleToggle}
        aria-expanded={open}
        aria-controls={id}
        className={`flex w-full items-center justify-between gap-3 text-left transition-colors focus:outline-none focus-visible:ring-2 focus-visible:ring-inset focus-visible:ring-cyan-500/50 ${
          row ? 'rounded-lg py-3 hover:text-white' : 'px-6 py-4 hover:bg-well'
        }`}
      >
        <div className="flex min-w-0 items-center gap-3">
          <span className={row ? 'text-sm font-medium text-slate-200' : 'text-title font-semibold text-white'}>
            {title}
          </span>
          {!open && summary && (
            <span
              className={`min-w-0 items-center gap-1.5 truncate text-xs ${TONE_TEXT[tone]} ${
                toned ? 'inline-flex' : 'hidden sm:inline-flex'
              }`}
            >
              {toned && <AlertTriangle className="h-3.5 w-3.5 flex-shrink-0" strokeWidth={1.75} aria-hidden="true" />}
              <span className="truncate">{summary}</span>
            </span>
          )}
        </div>
        <ChevronDown
          className={`h-4 w-4 shrink-0 text-slate-400 transition-transform duration-200 motion-reduce:transition-none ${open ? 'rotate-180' : ''}`}
          strokeWidth={1.75}
          aria-hidden="true"
        />
      </button>

      <div
        id={id}
        className="grid transition-all duration-200 ease-in-out motion-reduce:transition-none"
        style={{ gridTemplateRows: open ? '1fr' : '0fr' }}
      >
        {/* Collapsed content stays mounted (it may hold form state) but is
            inert so its fields drop out of the tab order. */}
        <div className="overflow-hidden" inert={!open}>
          <div className={row ? 'space-y-4 pb-4 pt-1' : 'space-y-4 px-6 pb-6 pt-1'}>
            {children}
          </div>
        </div>
      </div>
    </div>
  );
}
