'use client';

import { useState, useId } from 'react';
import { ChevronDown } from 'lucide-react';

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
    <div id={sectionId} className="glass overflow-hidden">
      <button
        type="button"
        onClick={handleToggle}
        aria-expanded={open}
        aria-controls={id}
        className="flex w-full items-center justify-between gap-3 px-6 py-4 text-left transition-colors hover:bg-well"
      >
        <div className="flex items-center gap-3 min-w-0">
          <span className="text-[15px] font-semibold text-white">{title}</span>
          {!open && summary && (
            <span className="truncate text-xs text-slate-500 hidden sm:block">{summary}</span>
          )}
        </div>
        <ChevronDown
          className={`h-4 w-4 shrink-0 text-slate-400 transition-transform duration-200 ${open ? 'rotate-180' : ''}`}
          strokeWidth={1.75}
          aria-hidden="true"
        />
      </button>

      <div
        id={id}
        className="grid transition-all duration-200 ease-in-out"
        style={{ gridTemplateRows: open ? '1fr' : '0fr' }}
      >
        <div className="overflow-hidden">
          <div className="space-y-4 px-6 pb-6 pt-1">
            {children}
          </div>
        </div>
      </div>
    </div>
  );
}
