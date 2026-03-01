'use client';

import { useState, useId } from 'react';

interface CollapsibleSectionProps {
  title: string;
  summary?: string;
  defaultOpen?: boolean;
  /** If provided, component runs in controlled mode */
  isOpen?: boolean;
  onToggle?: (open: boolean) => void;
  /** ID placed on the outer div for scroll-targeting */
  sectionId?: string;
  children: React.ReactNode;
}

export default function CollapsibleSection({
  title,
  summary,
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

  return (
    <div id={sectionId} className="rounded-xl border border-white/[0.07] bg-slate-900/40 overflow-hidden transition-colors hover:border-white/[0.1]">
      <button
        type="button"
        onClick={handleToggle}
        aria-expanded={open}
        aria-controls={id}
        className="flex w-full items-center justify-between px-4 py-3.5 text-left"
      >
        <div className="flex items-center gap-3 min-w-0">
          <span className="text-sm font-medium text-white">{title}</span>
          {!open && summary && (
            <span className="truncate text-xs text-slate-500 hidden sm:block">{summary}</span>
          )}
        </div>
        <svg
          className={`h-4 w-4 shrink-0 text-slate-400 transition-transform duration-200 ${open ? 'rotate-180' : ''}`}
          fill="none"
          viewBox="0 0 24 24"
          stroke="currentColor"
          strokeWidth={2}
        >
          <path strokeLinecap="round" strokeLinejoin="round" d="M19 9l-7 7-7-7" />
        </svg>
      </button>

      <div
        id={id}
        className="grid transition-all duration-200 ease-in-out"
        style={{ gridTemplateRows: open ? '1fr' : '0fr' }}
      >
        <div className="overflow-hidden">
          <div className="px-4 pb-4 pt-1 space-y-4">
            {children}
          </div>
        </div>
      </div>
    </div>
  );
}
