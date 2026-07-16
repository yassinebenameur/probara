'use client';

import { useCallback, useEffect, useRef, useState } from 'react';
import {
  ArrowDown,
  ArrowUp,
  ChevronDown,
  GripVertical,
  Layers,
  LayoutList,
  Pencil,
  Plus,
  Trash2,
  X,
} from 'lucide-react';
import type { Monitor } from '@/lib/types';
import Button from '@/components/ui/Button';
import MonitorPicker from './MonitorPicker';
import type { Action, DerivedState, EditableSection } from './useStatusPageFormState';

export const MONITOR_DRAG_MIME = 'application/x-status-monitor';

interface SectionListProps {
  sections: EditableSection[];
  monitors: Monitor[];
  monitorsLoading: boolean;
  derived: DerivedState;
  errors: Record<string, string>;
  dispatch: React.Dispatch<Action>;
  onAddSection: () => void;
}

interface DropTarget {
  sectionId: string;
  index: number;
}

export default function SectionList({
  sections,
  monitors,
  monitorsLoading,
  derived,
  errors,
  dispatch,
  onAddSection,
}: SectionListProps) {
  const [dropTarget, setDropTarget] = useState<DropTarget | null>(null);
  const [collapsedIds, setCollapsedIds] = useState<Set<string>>(new Set());
  const monitorIndex = useRef(new Map<string, Monitor>());
  monitorIndex.current = new Map(monitors.map((m) => [m.id, m]));

  const toggleCollapsed = useCallback((sectionId: string) => {
    setCollapsedIds((prev) => {
      const next = new Set(prev);
      if (next.has(sectionId)) next.delete(sectionId);
      else next.add(sectionId);
      return next;
    });
  }, []);

  const handleDrop = useCallback(
    (event: React.DragEvent) => {
      event.preventDefault();
      const target = dropTarget;
      setDropTarget(null);
      const raw = event.dataTransfer.getData(MONITOR_DRAG_MIME);
      if (!raw || !target) return;
      let monitorId: string;
      try {
        monitorId = (JSON.parse(raw) as { monitorId: string }).monitorId;
      } catch {
        return;
      }
      if (!monitorId) return;

      // Compensate for the row leaving its old slot when moving down within
      // the same section.
      const source = sections.find((s) => s.monitors.some((m) => m.monitor_id === monitorId));
      let index = target.index;
      if (source?.id === target.sectionId) {
        const sourceIndex = source.monitors.findIndex((m) => m.monitor_id === monitorId);
        if (sourceIndex < index) index -= 1;
        if (sourceIndex === index) return;
      }
      dispatch({
        type: 'move_monitor',
        monitorId,
        targetSectionId: target.sectionId,
        targetIndex: index,
      });
    },
    [dispatch, dropTarget, sections],
  );

  return (
    <div className="space-y-3">
      <div className="flex items-center justify-between gap-3 px-1">
        <div className="flex min-w-0 items-center gap-2">
          <LayoutList className="h-3.5 w-3.5 flex-shrink-0 text-cyan-400/80" strokeWidth={1.75} aria-hidden="true" />
          <h4 className="text-[11px] font-semibold uppercase tracking-[0.14em] text-slate-300">
            Page structure
          </h4>
          <span className="hidden truncate text-[11px] text-slate-500 sm:inline">
            Sections group monitors on the public page
          </span>
        </div>
        <Button
          type="button"
          variant="ghost"
          size="xs"
          icon={<Plus strokeWidth={1.75} />}
          onClick={onAddSection}
        >
          Add section
        </Button>
      </div>

      {sections.length === 0 ? (
        <button
          type="button"
          onClick={onAddSection}
          className="flex w-full flex-col items-center gap-2 rounded-xl border border-dashed border-white/[0.1] bg-slate-950/30 px-6 py-12 text-center transition-colors hover:border-cyan-500/40"
        >
          <Plus className="h-5 w-5 text-slate-500" strokeWidth={1.5} aria-hidden="true" />
          <span className="text-sm text-slate-400">Create your first section</span>
          <span className="text-xs text-slate-600">
            e.g. “API”, “Website”, “Voice platform”
          </span>
        </button>
      ) : (
        sections.map((section, index) => (
          <SectionCard
            key={section.id}
            section={section}
            index={index}
            total={sections.length}
            monitors={monitors}
            monitorsLoading={monitorsLoading}
            monitorIndex={monitorIndex.current}
            derived={derived}
            error={errors[`section-${section.id}`]}
            collapsed={collapsedIds.has(section.id)}
            onToggleCollapsed={() => toggleCollapsed(section.id)}
            dropTarget={dropTarget?.sectionId === section.id ? dropTarget : null}
            onDropTargetChange={setDropTarget}
            onDrop={handleDrop}
            dispatch={dispatch}
          />
        ))
      )}
    </div>
  );
}

interface SectionCardProps {
  section: EditableSection;
  index: number;
  total: number;
  monitors: Monitor[];
  monitorsLoading: boolean;
  monitorIndex: Map<string, Monitor>;
  derived: DerivedState;
  error?: string;
  collapsed: boolean;
  onToggleCollapsed: () => void;
  dropTarget: DropTarget | null;
  onDropTargetChange: (target: DropTarget | null) => void;
  onDrop: (event: React.DragEvent) => void;
  dispatch: React.Dispatch<Action>;
}

function SectionCard({
  section,
  index,
  total,
  monitors,
  monitorsLoading,
  monitorIndex,
  derived,
  error,
  collapsed,
  onToggleCollapsed,
  dropTarget,
  onDropTargetChange,
  onDrop,
  dispatch,
}: SectionCardProps) {
  const bodyRef = useRef<HTMLDivElement>(null);

  const handleDragOver = (event: React.DragEvent) => {
    if (!event.dataTransfer.types.includes(MONITOR_DRAG_MIME)) return;
    event.preventDefault();
    event.dataTransfer.dropEffect = 'move';

    // Insertion index from the row midpoints under the cursor.
    const rows = bodyRef.current?.querySelectorAll<HTMLElement>('[data-row-index]');
    let insertion = section.monitors.length;
    if (rows) {
      for (const row of rows) {
        const rect = row.getBoundingClientRect();
        if (event.clientY < rect.top + rect.height / 2) {
          insertion = Number(row.dataset.rowIndex);
          break;
        }
      }
    }
    if (dropTarget?.index !== insertion || dropTarget?.sectionId !== section.id) {
      onDropTargetChange({ sectionId: section.id, index: insertion });
    }
  };

  return (
    <div
      onDragOver={handleDragOver}
      onDrop={onDrop}
      onDragLeave={(event) => {
        if (!event.currentTarget.contains(event.relatedTarget as Node)) {
          onDropTargetChange(null);
        }
      }}
      className={`group/section overflow-hidden rounded-xl border bg-slate-900/50 transition-colors ${
        dropTarget ? 'border-cyan-400/50' : error ? 'border-rose-500/40' : 'border-white/[0.07]'
      }`}
    >
      <div className="flex items-center gap-1.5 px-3 py-2.5">
        <button
          type="button"
          onClick={onToggleCollapsed}
          aria-expanded={!collapsed}
          aria-label={collapsed ? `Expand ${section.title || 'section'}` : `Collapse ${section.title || 'section'}`}
          className="flex h-6 w-6 flex-shrink-0 items-center justify-center rounded-md text-slate-500 transition-colors hover:bg-white/[0.05] hover:text-slate-200"
        >
          <ChevronDown
            className={`h-3.5 w-3.5 transition-transform duration-150 ${collapsed ? '-rotate-90' : ''}`}
            strokeWidth={2}
            aria-hidden="true"
          />
        </button>
        <input
          type="text"
          value={section.title}
          onChange={(event) => dispatch({ type: 'rename_section', sectionId: section.id, title: event.target.value })}
          placeholder="Section title"
          aria-label="Section title"
          className="min-w-0 flex-1 rounded-md border border-transparent bg-transparent px-2 py-1 text-sm font-semibold text-white placeholder:font-normal placeholder:text-slate-600 transition-colors hover:border-white/[0.08] focus:border-cyan-500/40 focus:outline-none"
        />
        <span className="flex-shrink-0 whitespace-nowrap text-[11px] tabular-nums text-slate-600">
          {section.monitors.length === 0
            ? 'empty'
            : `${section.monitors.length} monitor${section.monitors.length === 1 ? '' : 's'}`}
        </span>
        <div className="flex flex-shrink-0 items-center gap-0.5 transition-opacity lg:opacity-0 lg:focus-within:opacity-100 lg:group-hover/section:opacity-100">
          <IconButton
            label="Move section up"
            disabled={index === 0}
            onClick={() => dispatch({ type: 'reorder_section', sectionId: section.id, direction: 'up' })}
          >
            <ArrowUp className="h-3.5 w-3.5" strokeWidth={1.75} />
          </IconButton>
          <IconButton
            label="Move section down"
            disabled={index === total - 1}
            onClick={() => dispatch({ type: 'reorder_section', sectionId: section.id, direction: 'down' })}
          >
            <ArrowDown className="h-3.5 w-3.5" strokeWidth={1.75} />
          </IconButton>
          <IconButton
            label={`Delete section ${section.title || index + 1}`}
            tone="danger"
            onClick={() => dispatch({ type: 'remove_section', sectionId: section.id })}
          >
            <Trash2 className="h-3.5 w-3.5" strokeWidth={1.75} />
          </IconButton>
        </div>
      </div>

      {error && <p className="px-4 pb-2 text-xs text-rose-400">{error}</p>}

      {!collapsed && (
        <div ref={bodyRef} className="border-t border-white/[0.05] px-3 pb-3 pt-2">
          {section.monitors.length === 0 ? (
            <p
              className={`mb-2 rounded-lg border border-dashed px-3 py-5 text-center text-xs transition-colors ${
                dropTarget
                  ? 'border-cyan-400/50 text-cyan-300'
                  : 'border-white/[0.07] text-slate-600'
              }`}
            >
              Empty section — add monitors below or drag them here.
            </p>
          ) : (
            <div className="overflow-hidden rounded-lg border border-white/[0.05] bg-slate-950/30">
              {section.monitors.map((entry, entryIndex) => {
                const monitor = monitorIndex.get(entry.monitor_id);
                if (!monitor) return null;
                return (
                  <div
                    key={entry.monitor_id}
                    className={entryIndex > 0 ? 'border-t border-white/[0.04]' : ''}
                  >
                    <DropLine active={dropTarget?.index === entryIndex} />
                    <MonitorRow
                      monitor={monitor}
                      displayName={entry.display_name}
                      rowIndex={entryIndex}
                      onSetDisplayName={(value) =>
                        dispatch({
                          type: 'set_display_name',
                          sectionId: section.id,
                          monitorId: entry.monitor_id,
                          value,
                        })
                      }
                      onRemove={() => dispatch({ type: 'remove_monitor', monitorId: entry.monitor_id })}
                    />
                  </div>
                );
              })}
              <DropLine active={dropTarget?.index === section.monitors.length} />
            </div>
          )}
          <div className="mt-2">
            <MonitorPicker
              monitors={monitors}
              loading={monitorsLoading}
              derived={derived}
              sectionId={section.id}
              onAdd={(monitorIds) =>
                dispatch({ type: 'add_monitor_to_section', sectionId: section.id, monitorIds })
              }
              onRemove={(monitorId) => dispatch({ type: 'remove_monitor', monitorId })}
            />
          </div>
        </div>
      )}
    </div>
  );
}

function DropLine({ active }: { active: boolean }) {
  return (
    <div aria-hidden="true" className="px-1">
      <div
        className={`rounded-full bg-cyan-400 transition-all duration-100 ${
          active ? 'my-0.5 h-0.5 opacity-100' : 'h-0 opacity-0'
        }`}
      />
    </div>
  );
}

interface MonitorRowProps {
  monitor: Monitor;
  displayName: string;
  rowIndex: number;
  onSetDisplayName: (value: string) => void;
  onRemove: () => void;
}

function MonitorRow({ monitor, displayName, rowIndex, onSetDisplayName, onRemove }: MonitorRowProps) {
  const [editing, setEditing] = useState(false);
  const [draft, setDraft] = useState(displayName);
  const inputRef = useRef<HTMLInputElement>(null);
  const renamed = displayName.trim() !== '';
  const isGroup = monitor.type === 'group';

  useEffect(() => {
    if (editing) {
      inputRef.current?.focus();
      inputRef.current?.select();
    }
  }, [editing]);

  const startEditing = () => {
    setDraft(displayName);
    setEditing(true);
  };

  const commit = () => {
    onSetDisplayName(draft.trim());
    setEditing(false);
  };

  const subtitle = renamed
    ? monitor.name
    : isGroup
      ? `${monitor.member_ids?.length ?? 0} member${(monitor.member_ids?.length ?? 0) === 1 ? '' : 's'}`
      : monitor.url || '';

  return (
    <div
      data-row-index={rowIndex}
      draggable={!editing}
      onDragStart={(event) => {
        event.dataTransfer.effectAllowed = 'move';
        event.dataTransfer.setData(MONITOR_DRAG_MIME, JSON.stringify({ monitorId: monitor.id }));
        event.dataTransfer.setData('text/plain', monitor.id);
      }}
      className={`group/row flex items-center gap-2.5 px-2.5 py-2 transition-colors hover:bg-white/[0.03] ${
        editing ? '' : 'cursor-grab active:cursor-grabbing'
      }`}
    >
      <GripVertical
        className="h-3.5 w-3.5 flex-shrink-0 text-slate-600 opacity-0 transition-opacity group-hover/row:opacity-100"
        strokeWidth={1.75}
        aria-hidden="true"
      />
      <span
        aria-hidden="true"
        className={`h-2 w-2 flex-shrink-0 rounded-full ${
          monitor.enabled
            ? 'bg-emerald-400 shadow-[0_0_6px_rgba(52,211,153,0.6)]'
            : 'bg-slate-600'
        }`}
        title={monitor.enabled ? 'Active' : 'Paused'}
      />
      {editing ? (
        <input
          ref={inputRef}
          type="text"
          value={draft}
          onChange={(event) => setDraft(event.target.value)}
          onBlur={commit}
          onKeyDown={(event) => {
            if (event.key === 'Enter') {
              event.preventDefault();
              commit();
            } else if (event.key === 'Escape') {
              setEditing(false);
            }
          }}
          placeholder={monitor.name}
          aria-label={`Public display name for ${monitor.name}`}
          className="input input-sm min-w-0 flex-1"
        />
      ) : (
        <div className="min-w-0 flex-1">
          <p className="truncate text-sm text-slate-200">
            {renamed ? displayName : monitor.name}
            {renamed && (
              <Pencil
                className="ml-1.5 inline h-2.5 w-2.5 text-slate-600"
                strokeWidth={1.75}
                aria-hidden="true"
              />
            )}
          </p>
          {subtitle && <p className="truncate text-[11px] leading-4 text-slate-600">{subtitle}</p>}
        </div>
      )}
      {!editing && (
        <>
          <span
            className={`hidden flex-shrink-0 items-center gap-1 rounded-md border px-1.5 py-0.5 text-[9px] font-medium uppercase tracking-[0.1em] sm:inline-flex ${
              isGroup
                ? 'border-violet-400/20 bg-violet-500/[0.08] text-violet-300'
                : 'border-white/[0.07] bg-white/[0.03] text-slate-500'
            }`}
          >
            {isGroup && <Layers className="h-2.5 w-2.5" strokeWidth={1.75} aria-hidden="true" />}
            {isGroup ? 'Group' : monitor.type}
          </span>
          <div className="flex flex-shrink-0 items-center gap-0.5 opacity-0 transition-opacity focus-within:opacity-100 group-hover/row:opacity-100">
            <IconButton label={`Rename ${monitor.name} on the public page`} onClick={startEditing}>
              <Pencil className="h-3 w-3" strokeWidth={1.75} />
            </IconButton>
            <IconButton label={`Remove ${monitor.name} from page`} tone="danger" onClick={onRemove}>
              <X className="h-3.5 w-3.5" strokeWidth={1.75} />
            </IconButton>
          </div>
        </>
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
      ? 'hover:bg-rose-500/10 hover:text-rose-300'
      : 'hover:bg-white/[0.06] hover:text-slate-100';
  return (
    <button
      type="button"
      disabled={disabled}
      onClick={onClick}
      title={label}
      aria-label={label}
      className={`flex h-6 w-6 items-center justify-center rounded-md text-slate-400 transition-colors ${
        disabled ? 'cursor-not-allowed opacity-30' : hover
      }`}
    >
      {children}
    </button>
  );
}
