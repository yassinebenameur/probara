'use client';

import { useEffect, useMemo, useRef, useState } from 'react';
import type { Monitor } from '@/lib/types';
import { AddToMenu } from './MonitorLibrary';
import type { DerivedState, EditableSection } from './useStatusPageFormState';

interface SectionCardProps {
  section: EditableSection;
  index: number;
  total: number;
  monitors: Monitor[];
  derived: DerivedState;
  allSections: EditableSection[];
  error?: string;
  highlighted: boolean;
  dropActive: boolean;
  onRename: (title: string) => void;
  onRemove: () => void;
  onReorder: (direction: 'up' | 'down') => void;
  onReorderMonitor: (monitorId: string, direction: 'up' | 'down') => void;
  onRemoveMonitor: (monitorId: string) => void;
  onMoveMonitor: (monitorId: string, targetSectionId: string) => void;
  onAddNewSection: (title: string, monitorIds: string[]) => void;
  onSetDisplayName: (monitorId: string, value: string) => void;
  onAddMonitors: (monitorIds: string[]) => void;
  onDragStartMonitor: (event: React.DragEvent, monitorId: string) => void;
  onDragOverSection: (event: React.DragEvent) => void;
  onDropOnSection: (event: React.DragEvent) => void;
  onDragLeaveSection: () => void;
  cardRef?: (element: HTMLDivElement | null) => void;
}

export default function SectionCard({
  section,
  index,
  total,
  monitors,
  derived,
  allSections,
  error,
  highlighted,
  dropActive,
  onRename,
  onRemove,
  onReorder,
  onReorderMonitor,
  onRemoveMonitor,
  onMoveMonitor,
  onAddNewSection,
  onSetDisplayName,
  onAddMonitors,
  onDragStartMonitor,
  onDragOverSection,
  onDropOnSection,
  onDragLeaveSection,
  cardRef,
}: SectionCardProps) {
  const monitorIndex = useMemo(() => {
    const map = new Map<string, Monitor>();
    monitors.forEach((monitor) => map.set(monitor.id, monitor));
    return map;
  }, [monitors]);

  const availableMonitors = useMemo(() => {
    const inThisSection = new Set(section.monitors.map((m) => m.monitor_id));
    return monitors.filter((monitor) => !inThisSection.has(monitor.id));
  }, [monitors, section.monitors]);

  return (
    <div
      ref={cardRef}
      onDragOver={onDragOverSection}
      onDrop={onDropOnSection}
      onDragLeave={onDragLeaveSection}
      className={`overflow-hidden rounded-xl border bg-slate-900/50 transition-all ${
        dropActive
          ? 'border-cyan-400/60 bg-cyan-500/[0.06] ring-1 ring-cyan-400/30'
          : highlighted
            ? 'border-cyan-500/40 ring-1 ring-cyan-500/20'
            : 'border-white/[0.08]'
      }`}
    >
      <div className="flex items-center gap-2 border-b border-white/[0.06] bg-slate-950/30 px-3 py-2.5">
        <span
          aria-hidden="true"
          className="flex h-6 min-w-[26px] flex-shrink-0 items-center justify-center rounded-md border border-white/[0.08] bg-slate-950/60 px-1.5 font-mono text-[10px] tabular-nums text-slate-400"
        >
          {String(index + 1).padStart(2, '0')}
        </span>
        <input
          type="text"
          value={section.title}
          onChange={(event) => onRename(event.target.value)}
          placeholder="Section title"
          className="input input-sm flex-1"
        />
        <span className="hidden whitespace-nowrap rounded-full border border-white/[0.06] bg-slate-950/40 px-2 py-0.5 text-[10px] tabular-nums text-slate-500 sm:inline">
          {section.monitors.length} monitor{section.monitors.length === 1 ? '' : 's'}
        </span>
        <div className="flex items-center gap-1">
          <IconButton
            label="Move section up"
            disabled={index === 0}
            onClick={() => onReorder('up')}
          >
            <ArrowUp />
          </IconButton>
          <IconButton
            label="Move section down"
            disabled={index === total - 1}
            onClick={() => onReorder('down')}
          >
            <ArrowDown />
          </IconButton>
          <IconButton label="Remove section" tone="danger" onClick={onRemove}>
            <Trash />
          </IconButton>
        </div>
      </div>

      {error && <p className="px-4 pt-3 text-xs text-rose-400">{error}</p>}

      <div className="space-y-1.5 p-3">
        {section.monitors.length === 0 ? (
          <p className="rounded-lg border border-dashed border-white/[0.08] px-3 py-6 text-center text-xs text-slate-500">
            Drop monitors here, or use “Add monitors” below.
          </p>
        ) : (
          section.monitors.map((entry, entryIndex) => {
            const monitor = monitorIndex.get(entry.monitor_id);
            if (!monitor) return null;
            return (
              <MonitorRow
                key={entry.monitor_id}
                monitor={monitor}
                displayName={entry.display_name}
                isFirst={entryIndex === 0}
                isLast={entryIndex === section.monitors.length - 1}
                allSections={allSections}
                currentSectionId={section.id}
                onSetDisplayName={(value) => onSetDisplayName(entry.monitor_id, value)}
                onReorder={(direction) => onReorderMonitor(entry.monitor_id, direction)}
                onRemove={() => onRemoveMonitor(entry.monitor_id)}
                onMoveTo={(targetSectionId) => onMoveMonitor(entry.monitor_id, targetSectionId)}
                onAddNewSection={(title) => onAddNewSection(title, [entry.monitor_id])}
                onDragStart={(event) => onDragStartMonitor(event, entry.monitor_id)}
              />
            );
          })
        )}

        <AddMonitorsComboBox
          monitors={availableMonitors}
          derived={derived}
          onPick={(monitorIds) => onAddMonitors(monitorIds)}
        />
      </div>
    </div>
  );
}

interface MonitorRowProps {
  monitor: Monitor;
  displayName: string;
  isFirst: boolean;
  isLast: boolean;
  allSections: EditableSection[];
  currentSectionId: string;
  onSetDisplayName: (value: string) => void;
  onReorder: (direction: 'up' | 'down') => void;
  onRemove: () => void;
  onMoveTo: (targetSectionId: string) => void;
  onAddNewSection: (title: string) => void;
  onDragStart: (event: React.DragEvent) => void;
}

function MonitorRow({
  monitor,
  displayName,
  isFirst,
  isLast,
  allSections,
  currentSectionId,
  onSetDisplayName,
  onReorder,
  onRemove,
  onMoveTo,
  onAddNewSection,
  onDragStart,
}: MonitorRowProps) {
  return (
    <div
      draggable
      onDragStart={onDragStart}
      className="flex flex-wrap items-center gap-2 rounded-lg border border-white/[0.06] bg-slate-950/40 px-3 py-2 transition-colors hover:border-cyan-500/20"
    >
      <span
        className={`h-2 w-2 flex-shrink-0 rounded-full ${
          monitor.enabled ? 'bg-emerald-400' : 'bg-slate-600'
        }`}
        aria-hidden="true"
      />
      <div className="min-w-0 flex-shrink basis-[180px]">
        <p className="truncate text-sm font-medium text-white">
          {displayName.trim() || monitor.name}
        </p>
        <p className="truncate text-[11px] text-slate-500">
          {monitor.type.toUpperCase()}
          {monitor.url ? ` · ${monitor.url}` : ''}
        </p>
      </div>
      <input
        type="text"
        value={displayName}
        onChange={(event) => onSetDisplayName(event.target.value)}
        placeholder="Optional public display name"
        className="input input-sm h-8 min-w-[160px] flex-1"
        aria-label={`Public display name for ${monitor.name}`}
      />
      <div className="flex items-center gap-1">
        <IconButton label="Move up" disabled={isFirst} onClick={() => onReorder('up')}>
          <ArrowUp />
        </IconButton>
        <IconButton label="Move down" disabled={isLast} onClick={() => onReorder('down')}>
          <ArrowDown />
        </IconButton>
        <AddToMenu
          label="Move ▾"
          sections={allSections}
          currentSectionId={currentSectionId}
          onPickSection={onMoveTo}
          onCreateSection={onAddNewSection}
          compact
        />
        <IconButton label="Remove from page" tone="danger" onClick={onRemove}>
          <Trash />
        </IconButton>
      </div>
    </div>
  );
}

interface AddMonitorsComboBoxProps {
  monitors: Monitor[];
  derived: DerivedState;
  onPick: (monitorIds: string[]) => void;
}

function AddMonitorsComboBox({ monitors, derived, onPick }: AddMonitorsComboBoxProps) {
  const [open, setOpen] = useState(false);
  const [search, setSearch] = useState('');
  const containerRef = useRef<HTMLDivElement>(null);
  const inputRef = useRef<HTMLInputElement>(null);

  useEffect(() => {
    if (!open) return;
    const handleClick = (event: MouseEvent) => {
      if (containerRef.current && !containerRef.current.contains(event.target as Node)) {
        setOpen(false);
        setSearch('');
      }
    };
    document.addEventListener('mousedown', handleClick);
    return () => document.removeEventListener('mousedown', handleClick);
  }, [open]);

  useEffect(() => {
    if (open) inputRef.current?.focus();
  }, [open]);

  const filtered = useMemo(() => {
    const needle = search.trim().toLowerCase();
    if (!needle) return monitors.slice(0, 50);
    return monitors
      .filter((monitor) => {
        const haystack = [monitor.name, monitor.type, monitor.url || '', ...(monitor.tags || [])]
          .join(' ')
          .toLowerCase();
        return haystack.includes(needle);
      })
      .slice(0, 50);
  }, [monitors, search]);

  return (
    <div ref={containerRef} className="relative pt-1">
      {open ? (
        <div className="rounded-lg border border-white/[0.08] bg-slate-950/70 p-2">
          <input
            ref={inputRef}
            type="search"
            value={search}
            onChange={(event) => setSearch(event.target.value)}
            placeholder="Search monitors to add…"
            className="input input-sm"
            onKeyDown={(event) => {
              if (event.key === 'Escape') {
                setOpen(false);
                setSearch('');
              } else if (event.key === 'Enter' && filtered.length > 0) {
                event.preventDefault();
                onPick([filtered[0].id]);
                setSearch('');
              }
            }}
          />
          <div className="mt-2 max-h-56 overflow-y-auto">
            {filtered.length === 0 ? (
              <p className="px-3 py-3 text-xs text-slate-500">
                {monitors.length === 0 ? 'All monitors are already in this section.' : 'No matches.'}
              </p>
            ) : (
              filtered.map((monitor) => {
                const placement = derived.monitorIdToSection.get(monitor.id);
                return (
                  <button
                    key={monitor.id}
                    type="button"
                    onClick={() => {
                      onPick([monitor.id]);
                      setSearch('');
                    }}
                    className="flex w-full items-center gap-2 rounded-md px-2 py-1.5 text-left text-xs text-slate-200 transition-colors hover:bg-cyan-500/10"
                  >
                    <span
                      className={`h-1.5 w-1.5 flex-shrink-0 rounded-full ${
                        monitor.enabled ? 'bg-emerald-400' : 'bg-slate-600'
                      }`}
                    />
                    <span className="truncate">{monitor.name}</span>
                    <span className="ml-auto text-[10px] uppercase tracking-wider text-slate-500">
                      {placement ? `from ${placement.sectionTitle}` : monitor.type}
                    </span>
                  </button>
                );
              })
            )}
          </div>
          <div className="mt-2 flex justify-end">
            <button
              type="button"
              onClick={() => {
                setOpen(false);
                setSearch('');
              }}
              className="text-[11px] uppercase tracking-wider text-slate-500 hover:text-slate-300"
            >
              Done
            </button>
          </div>
        </div>
      ) : (
        <button
          type="button"
          onClick={() => setOpen(true)}
          className="btn btn-outline btn-xs"
        >
          + Add monitors
        </button>
      )}
    </div>
  );
}

interface IconButtonProps {
  label: string;
  disabled?: boolean;
  tone?: 'default' | 'danger';
  onClick: () => void;
  children: React.ReactNode;
}

function IconButton({ label, disabled, tone = 'default', onClick, children }: IconButtonProps) {
  const hover =
    tone === 'danger'
      ? 'hover:border-rose-500/40 hover:bg-rose-500/10 hover:text-rose-300'
      : 'hover:border-cyan-500/40 hover:text-cyan-200';
  return (
    <button
      type="button"
      disabled={disabled}
      onClick={onClick}
      title={label}
      aria-label={label}
      className={`flex h-7 w-7 items-center justify-center rounded-md border border-white/[0.06] bg-slate-950/40 text-slate-300 transition-colors ${hover} ${
        disabled
          ? 'cursor-not-allowed opacity-30 hover:border-white/[0.06] hover:bg-transparent hover:text-slate-300'
          : ''
      }`}
    >
      {children}
    </button>
  );
}

function ArrowUp() {
  return (
    <svg className="h-3.5 w-3.5" viewBox="0 0 20 20" fill="currentColor" aria-hidden="true">
      <path d="M10 4.5a.75.75 0 0 1 .53.22l5 5a.75.75 0 1 1-1.06 1.06L10.75 7.06V15a.75.75 0 0 1-1.5 0V7.06l-3.72 3.72a.75.75 0 1 1-1.06-1.06l5-5A.75.75 0 0 1 10 4.5Z" />
    </svg>
  );
}

function ArrowDown() {
  return (
    <svg className="h-3.5 w-3.5" viewBox="0 0 20 20" fill="currentColor" aria-hidden="true">
      <path d="M10 15.5a.75.75 0 0 1-.53-.22l-5-5a.75.75 0 1 1 1.06-1.06l3.72 3.72V5a.75.75 0 0 1 1.5 0v7.94l3.72-3.72a.75.75 0 1 1 1.06 1.06l-5 5a.75.75 0 0 1-.53.22Z" />
    </svg>
  );
}

function Trash() {
  return (
    <svg
      className="h-3.5 w-3.5"
      viewBox="0 0 20 20"
      fill="none"
      stroke="currentColor"
      strokeWidth={1.6}
      strokeLinecap="round"
      strokeLinejoin="round"
      aria-hidden="true"
    >
      <path d="M3.5 5.5h13" />
      <path d="M8 5.5V4a1.5 1.5 0 0 1 1.5-1.5h1A1.5 1.5 0 0 1 12 4v1.5" />
      <path d="M5.5 5.5 6.3 16a1.5 1.5 0 0 0 1.5 1.4h4.4a1.5 1.5 0 0 0 1.5-1.4l.8-10.5" />
      <path d="M8.5 9v5M11.5 9v5" />
    </svg>
  );
}
