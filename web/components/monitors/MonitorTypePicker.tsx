'use client';

import { useEffect, useId, useRef, useState } from 'react';
import { Lock, Search, type LucideIcon } from 'lucide-react';
import Button from '@/components/ui/Button';
import { MonitorType } from '@/lib/types';
import { TILE_SELECTED } from './form-kit';

/** One entry of MonitorForm's MONITOR_TYPE_META registry. */
export type MonitorTypeMeta = {
  type: MonitorType;
  label: string;
  /** One line at the picker's column width (~30 characters). */
  description: string;
  icon: LucideIcon;
  category: string;
  namePlaceholder: string;
};

/**
 * Step one of a fresh create: choose the type. Categories sit side by side as
 * columns, so the grid has no orphan tiles and the whole catalogue fits in one
 * short block. `onCancel` (when a type was already chosen) closes it again.
 * The catalogue itself is MonitorForm's MONITOR_TYPE_META, passed in.
 */
export function MonitorTypePicker({
  types,
  categories,
  value,
  onSelect,
  onCancel,
}: {
  types: readonly MonitorTypeMeta[];
  categories: readonly string[];
  value: MonitorType | null;
  onSelect: (type: MonitorType) => void;
  onCancel?: () => void;
}) {
  const [query, setQuery] = useState('');
  const headingId = useId();
  const searchRef = useRef<HTMLInputElement>(null);
  // Reopened from "Change type" (onCancel set): the button that had focus is
  // gone, so focus moves to the search. A fresh create leaves focus alone.
  const reopened = Boolean(onCancel);
  useEffect(() => {
    if (reopened) searchRef.current?.focus();
  }, [reopened]);
  const q = query.trim().toLowerCase();
  const matches = (m: MonitorTypeMeta) =>
    !q ||
    m.label.toLowerCase().includes(q) ||
    m.type.includes(q) ||
    m.description.toLowerCase().includes(q) ||
    m.category.toLowerCase().includes(q);
  const visible = types.filter(matches);

  return (
    <section
      aria-labelledby={headingId}
      className="glass p-5 sm:p-6"
      onKeyDown={(e) => {
        if (e.key === 'Escape' && onCancel) {
          e.preventDefault();
          onCancel();
        }
      }}
    >
      <div className="mb-5 flex flex-wrap items-start justify-between gap-3">
        <div>
          <h2 id={headingId} className="text-title font-semibold text-white">Monitor type</h2>
          <p className="mt-0.5 text-ui text-slate-400">Choose what to watch.</p>
        </div>
        <div className="flex w-full items-center gap-2 sm:w-auto">
          <div className="relative min-w-0 flex-1 sm:flex-none">
            <Search className="pointer-events-none absolute left-2.5 top-1/2 h-3.5 w-3.5 -translate-y-1/2 text-slate-500" strokeWidth={1.75} aria-hidden="true" />
            <input
              ref={searchRef}
              type="search"
              value={query}
              onChange={(e) => setQuery(e.target.value)}
              onKeyDown={(e) => {
                // Enter picks the only (or first) match: "redis⏎".
                if (e.key === 'Enter' && visible.length > 0) {
                  e.preventDefault();
                  onSelect(visible[0].type);
                }
              }}
              placeholder="Search types…"
              aria-label="Search monitor types"
              className="input input-sm !pl-8 sm:!w-52"
            />
          </div>
          {onCancel && (
            <Button type="button" variant="ghost" size="sm" onClick={onCancel}>
              Cancel
            </Button>
          )}
        </div>
      </div>
      <div className="grid gap-x-5 gap-y-5 sm:grid-cols-2 xl:grid-cols-4">
        {categories.map((category) => {
          const items = visible.filter((m) => m.category === category);
          if (items.length === 0) return null;
          return (
            <div key={category} role="group" aria-label={category}>
              <p className="mb-2 text-xs font-medium text-slate-500">{category}</p>
              <ul className="grid grid-cols-2 gap-1.5 sm:grid-cols-1">
                {items.map((m) => {
                  const Icon = m.icon;
                  const isSelected = m.type === value;
                  return (
                    <li key={m.type}>
                      <button
                        type="button"
                        onClick={() => onSelect(m.type)}
                        aria-current={isSelected ? 'true' : undefined}
                        title={m.label}
                        className={`glass-well flex w-full items-center gap-3 px-3 py-2.5 text-left transition-colors focus:outline-none focus-visible:ring-2 focus-visible:ring-cyan-500/50 ${
                          isSelected ? TILE_SELECTED : 'hover:bg-well-hover'
                        }`}
                      >
                        <Icon className="h-4 w-4 shrink-0 text-slate-400" strokeWidth={1.75} aria-hidden="true" />
                        <span className="min-w-0">
                          {/* Wraps in the two-column phone grid, where truncation would hide the name. */}
                          <span className="block break-words text-ui font-medium leading-tight text-slate-100 sm:truncate sm:leading-normal">{m.label}</span>
                          <span className="hidden truncate text-xs text-slate-500 sm:block">{m.description}</span>
                        </span>
                      </button>
                    </li>
                  );
                })}
              </ul>
            </div>
          );
        })}
      </div>
      {visible.length === 0 && (
        <p className="py-2 text-center text-ui text-slate-500">No monitor types match &ldquo;{query}&rdquo;.</p>
      )}
    </section>
  );
}

/** The chosen type, collapsed to one line above the form. */
export function MonitorTypeBar({
  meta,
  locked,
  onChange,
}: {
  meta: MonitorTypeMeta;
  locked: boolean;
  onChange: () => void;
}) {
  const Icon = meta.icon;
  return (
    <div className="glass flex flex-wrap items-center justify-between gap-3 px-6 py-4">
      <div className="flex min-w-0 items-center gap-3">
        <Icon className="h-4 w-4 shrink-0 text-slate-400" strokeWidth={1.75} aria-hidden="true" />
        <div className="min-w-0">
          <p className="text-sm font-medium text-white">{meta.label} monitor</p>
          <p className="truncate text-xs text-slate-500">{meta.description}</p>
        </div>
      </div>
      {locked ? (
        <span className="flex shrink-0 items-center gap-1.5 text-xs text-slate-500">
          <Lock className="h-3.5 w-3.5" strokeWidth={1.75} aria-hidden="true" />
          Type is fixed after creation
        </span>
      ) : (
        <Button type="button" variant="ghost" size="sm" onClick={onChange}>
          Change type
        </Button>
      )}
    </div>
  );
}
