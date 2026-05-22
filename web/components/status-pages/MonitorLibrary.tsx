'use client';

import { useEffect, useMemo, useRef, useState } from 'react';
import type { Monitor, MonitorType } from '@/lib/types';
import SelectCheckbox from '@/components/ui/SelectCheckbox';
import Button from '@/components/ui/Button';
import type {
  DerivedState,
  EditableSection,
  Filters,
  MonitorPlacement,
  StatusFilter,
} from './useStatusPageFormState';

interface MonitorLibraryProps {
  monitors: Monitor[];
  loading: boolean;
  total: number;
  filters: Filters;
  filteredMonitors: Monitor[];
  selection: string[];
  derived: DerivedState;
  sections: EditableSection[];
  onFilterChange: (key: keyof Filters, value: string | null) => void;
  onToggleSelect: (monitorId: string) => void;
  onClearSelection: () => void;
  onSelectAllFiltered: () => void;
  onAddToSection: (monitorIds: string[], targetSectionId: string) => void;
  onAddNewSection: (monitorIds: string[], title: string) => void;
  onRevealSection: (sectionId: string) => void;
  onDragStartMonitor: (event: React.DragEvent, monitorId: string) => void;
}

export default function MonitorLibrary({
  monitors,
  loading,
  total,
  filters,
  filteredMonitors,
  selection,
  derived,
  sections,
  onFilterChange,
  onToggleSelect,
  onClearSelection,
  onSelectAllFiltered,
  onAddToSection,
  onAddNewSection,
  onRevealSection,
  onDragStartMonitor,
}: MonitorLibraryProps) {
  const selectionSet = useMemo(() => new Set(selection), [selection]);
  const selectedInView = filteredMonitors.filter((m) => selectionSet.has(m.id)).length;
  const allFilteredSelected =
    filteredMonitors.length > 0 && selectedInView === filteredMonitors.length;
  const someFilteredSelected = selectedInView > 0 && !allFilteredSelected;

  return (
    <div className="flex h-full min-h-[420px] flex-col overflow-hidden rounded-xl border border-white/[0.06] bg-slate-950/40">
      <div className="space-y-3 border-b border-white/[0.06] p-4">
        <div className="flex items-center justify-between gap-3">
          <div className="flex items-center gap-2">
            <span aria-hidden="true" className="h-1 w-6 rounded-full bg-gradient-to-r from-cyan-500/60 to-cyan-500/0" />
            <h4 className="text-[11px] font-semibold uppercase tracking-[0.14em] text-slate-300">
              Monitor library
            </h4>
          </div>
          <span className="text-[11px] tabular-nums text-slate-500">
            {loading ? 'Loading…' : `${filteredMonitors.length} / ${total}`}
          </span>
        </div>

        <input
          type="search"
          value={filters.search}
          onChange={(event) => onFilterChange('search', event.target.value)}
          placeholder="Search monitors by name, URL, tag…"
          className="input input-sm"
        />

        <div className="flex flex-wrap items-center gap-2">
          <FilterSelect
            label="Tag"
            value={filters.tag ?? ''}
            onChange={(value) => onFilterChange('tag', value || null)}
            options={derived.availableTags.map((tag) => ({ value: tag, label: tag }))}
          />
          <FilterSelect
            label="Type"
            value={filters.type ?? ''}
            onChange={(value) => onFilterChange('type', (value as MonitorType) || null)}
            options={derived.availableTypes.map((type) => ({ value: type, label: type.toUpperCase() }))}
          />
          <FilterSelect
            label="Status"
            value={filters.status === 'all' ? '' : filters.status}
            onChange={(value) => onFilterChange('status', (value as StatusFilter) || 'all')}
            options={[
              { value: 'active', label: 'Active' },
              { value: 'paused', label: 'Paused' },
            ]}
          />
          {(filters.search || filters.tag || filters.type || filters.status !== 'all') && (
            <button
              type="button"
              onClick={() => {
                onFilterChange('search', '');
                onFilterChange('tag', null);
                onFilterChange('type', null);
                onFilterChange('status', 'all');
              }}
              className="text-[10px] uppercase tracking-[0.14em] text-slate-500 transition-colors hover:text-slate-300"
            >
              Reset
            </button>
          )}
        </div>
      </div>

      <div className="flex items-center justify-between gap-3 border-b border-white/[0.06] bg-slate-950/60 px-4 py-2">
        <div className="flex items-center gap-2 text-xs">
          <SelectCheckbox
            size="sm"
            checked={allFilteredSelected}
            indeterminate={someFilteredSelected}
            onChange={() => (allFilteredSelected ? onClearSelection() : onSelectAllFiltered())}
            disabled={filteredMonitors.length === 0}
            label={allFilteredSelected ? 'Clear selection' : 'Select all in view'}
          />
          <button
            type="button"
            disabled={filteredMonitors.length === 0}
            onClick={() => (allFilteredSelected ? onClearSelection() : onSelectAllFiltered())}
            className="text-slate-400 transition-colors hover:text-slate-200 disabled:cursor-not-allowed disabled:opacity-40"
          >
            {selection.length > 0 ? `${selection.length} selected` : 'Select all in view'}
          </button>
        </div>
        <div className="flex items-center gap-2">
          <AddToMenu
            label="Add to ▾"
            disabled={selection.length === 0}
            sections={sections}
            onPickSection={(sectionId) => {
              onAddToSection(selection, sectionId);
            }}
            onCreateSection={(title) => {
              onAddNewSection(selection, title);
            }}
          />
          {selection.length > 0 && (
            <button
              type="button"
              onClick={onClearSelection}
              className="text-[10px] uppercase tracking-[0.14em] text-slate-500 transition-colors hover:text-slate-300"
            >
              Clear
            </button>
          )}
        </div>
      </div>

      <div className="flex-1 overflow-y-auto p-2">
        {monitors.length === 0 && !loading ? (
          <p className="p-6 text-center text-sm text-slate-500">No monitors available.</p>
        ) : filteredMonitors.length === 0 ? (
          <p className="p-6 text-center text-sm text-slate-500">No monitors match these filters.</p>
        ) : (
          <ul className="space-y-1">
            {filteredMonitors.map((monitor) => (
              <MonitorRow
                key={monitor.id}
                monitor={monitor}
                checked={selectionSet.has(monitor.id)}
                placement={derived.monitorIdToSection.get(monitor.id)}
                sections={sections}
                onToggleSelect={() => onToggleSelect(monitor.id)}
                onAddToSection={(sectionId) => onAddToSection([monitor.id], sectionId)}
                onAddNewSection={(title) => onAddNewSection([monitor.id], title)}
                onRevealSection={onRevealSection}
                onDragStart={(event) => onDragStartMonitor(event, monitor.id)}
              />
            ))}
          </ul>
        )}
      </div>
    </div>
  );
}

interface MonitorRowProps {
  monitor: Monitor;
  checked: boolean;
  placement?: MonitorPlacement;
  sections: EditableSection[];
  onToggleSelect: () => void;
  onAddToSection: (sectionId: string) => void;
  onAddNewSection: (title: string) => void;
  onRevealSection: (sectionId: string) => void;
  onDragStart: (event: React.DragEvent) => void;
}

function MonitorRow({
  monitor,
  checked,
  placement,
  sections,
  onToggleSelect,
  onAddToSection,
  onAddNewSection,
  onRevealSection,
  onDragStart,
}: MonitorRowProps) {
  return (
    <li
      draggable
      onDragStart={onDragStart}
      className={`group flex items-center gap-3 rounded-lg border px-3 py-2 transition-colors ${
        checked
          ? 'border-cyan-500/30 bg-cyan-500/10'
          : 'border-transparent hover:border-white/[0.06] hover:bg-white/[0.03]'
      }`}
    >
      <SelectCheckbox
        checked={checked}
        onChange={onToggleSelect}
        label={`Select ${monitor.name}`}
      />
      <span
        className={`h-2 w-2 flex-shrink-0 rounded-full ${
          monitor.enabled ? 'bg-emerald-400' : 'bg-slate-600'
        }`}
        aria-hidden="true"
      />
      <div className="min-w-0 flex-1">
        <p className="truncate text-sm font-medium text-white">{monitor.name}</p>
        <p className="truncate text-[11px] text-slate-500">
          {monitor.type.toUpperCase()}
          {monitor.url ? ` · ${monitor.url}` : ''}
        </p>
      </div>
      {placement ? (
        <button
          type="button"
          onClick={() => onRevealSection(placement.sectionId)}
          className="hidden max-w-[160px] items-center gap-1 truncate rounded-full border border-cyan-500/20 bg-cyan-500/[0.08] px-2 py-0.5 text-[10px] uppercase tracking-[0.12em] text-cyan-200/90 transition-colors hover:border-cyan-400/60 hover:bg-cyan-500/15 sm:inline-flex"
          title={`On page · ${placement.sectionTitle}`}
        >
          <span aria-hidden="true" className="h-1.5 w-1.5 flex-shrink-0 rounded-full bg-cyan-400" />
          <span className="truncate">{placement.sectionTitle}</span>
        </button>
      ) : null}
      <AddToMenu
        label={placement ? 'Move ▾' : 'Add ▾'}
        sections={sections}
        currentSectionId={placement?.sectionId}
        onPickSection={(sectionId) => onAddToSection(sectionId)}
        onCreateSection={(title) => onAddNewSection(title)}
        compact
      />
    </li>
  );
}

interface FilterSelectProps {
  label: string;
  value: string;
  options: { value: string; label: string }[];
  onChange: (value: string) => void;
}

function FilterSelect({ label, value, options, onChange }: FilterSelectProps) {
  const active = value !== '';
  return (
    <label className="flex items-center gap-1.5 text-[10px] uppercase tracking-[0.14em] text-slate-500">
      <span>{label}</span>
      <div className="relative">
        <select
          value={value}
          onChange={(event) => onChange(event.target.value)}
          className={`input input-xs h-7 min-w-[112px] cursor-pointer appearance-none pr-7 text-xs normal-case tracking-normal ${
            active ? 'border-cyan-500/40 text-cyan-100' : ''
          }`}
        >
          <option value="">Any</option>
          {options.map((option) => (
            <option key={option.value} value={option.value}>
              {option.label}
            </option>
          ))}
        </select>
        <svg
          aria-hidden="true"
          viewBox="0 0 20 20"
          fill="currentColor"
          className={`pointer-events-none absolute right-2 top-1/2 h-3 w-3 -translate-y-1/2 transition-colors ${
            active ? 'text-cyan-300' : 'text-slate-500'
          }`}
        >
          <path d="M5.23 7.21a.75.75 0 0 1 1.06.02L10 11.06l3.71-3.83a.75.75 0 1 1 1.08 1.04l-4.25 4.4a.75.75 0 0 1-1.08 0L5.21 8.27a.75.75 0 0 1 .02-1.06Z" />
        </svg>
      </div>
    </label>
  );
}

interface AddToMenuProps {
  label: string;
  disabled?: boolean;
  compact?: boolean;
  sections: EditableSection[];
  currentSectionId?: string;
  onPickSection: (sectionId: string) => void;
  onCreateSection: (title: string) => void;
}

export function AddToMenu({
  label,
  disabled = false,
  compact = false,
  sections,
  currentSectionId,
  onPickSection,
  onCreateSection,
}: AddToMenuProps) {
  const [open, setOpen] = useState(false);
  const [creating, setCreating] = useState(false);
  const [draft, setDraft] = useState('');
  const containerRef = useRef<HTMLDivElement>(null);
  const inputRef = useRef<HTMLInputElement>(null);

  useEffect(() => {
    if (!open) return;
    const handleClick = (event: MouseEvent) => {
      if (containerRef.current && !containerRef.current.contains(event.target as Node)) {
        setOpen(false);
        setCreating(false);
        setDraft('');
      }
    };
    document.addEventListener('mousedown', handleClick);
    return () => document.removeEventListener('mousedown', handleClick);
  }, [open]);

  useEffect(() => {
    if (creating) inputRef.current?.focus();
  }, [creating]);

  const commitCreate = () => {
    const title = draft.trim();
    if (!title) return;
    onCreateSection(title);
    setDraft('');
    setCreating(false);
    setOpen(false);
  };

  return (
    <div ref={containerRef} className="relative">
      <Button
        type="button"
        variant="ghost"
        size={compact ? 'xs' : 'sm'}
        disabled={disabled}
        onClick={() => setOpen((value) => !value)}
      >
        {label}
      </Button>
      {open && (
        <div className="absolute right-0 z-30 mt-1 w-56 rounded-lg border border-white/[0.08] bg-slate-900/95 p-1 shadow-2xl backdrop-blur">
          <div className="max-h-48 overflow-y-auto">
            {sections.length === 0 ? (
              <p className="px-3 py-2 text-xs text-slate-500">No sections yet.</p>
            ) : (
              sections.map((section) => (
                <button
                  key={section.id}
                  type="button"
                  disabled={section.id === currentSectionId}
                  onClick={() => {
                    onPickSection(section.id);
                    setOpen(false);
                  }}
                  className={`flex w-full items-center justify-between rounded-md px-3 py-1.5 text-left text-xs transition-colors ${
                    section.id === currentSectionId
                      ? 'cursor-default text-slate-500'
                      : 'text-slate-200 hover:bg-cyan-500/10 hover:text-cyan-200'
                  }`}
                >
                  <span className="truncate">{section.title || 'Untitled section'}</span>
                  {section.id === currentSectionId && (
                    <span className="text-[10px] uppercase tracking-wider text-slate-500">current</span>
                  )}
                </button>
              ))
            )}
          </div>
          <div className="mt-1 border-t border-white/[0.06] pt-1">
            {creating ? (
              <div className="flex items-center gap-1 px-1.5 py-1">
                <input
                  ref={inputRef}
                  type="text"
                  value={draft}
                  onChange={(event) => setDraft(event.target.value)}
                  onKeyDown={(event) => {
                    if (event.key === 'Enter') {
                      event.preventDefault();
                      commitCreate();
                    } else if (event.key === 'Escape') {
                      setCreating(false);
                      setDraft('');
                    }
                  }}
                  placeholder="Section name"
                  className="input input-xs flex-1"
                />
                <Button
                  type="button"
                  variant="accent"
                  size="xs"
                  onClick={commitCreate}
                  disabled={!draft.trim()}
                >
                  Add
                </Button>
              </div>
            ) : (
              <button
                type="button"
                onClick={() => setCreating(true)}
                className="flex w-full items-center gap-2 rounded-md px-3 py-1.5 text-left text-xs text-cyan-300 hover:bg-cyan-500/10"
              >
                + New section…
              </button>
            )}
          </div>
        </div>
      )}
    </div>
  );
}
