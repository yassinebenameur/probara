'use client';

import { useCallback, useEffect, useRef, useState } from 'react';
import {
  ArrowDown,
  ArrowUp,
  ChevronDown,
  GripVertical,
  Pencil,
  Plus,
  Trash2,
  X,
} from 'lucide-react';
import type { Monitor } from '@/lib/types';
import Button from '@/components/ui/Button';
import IconButton, { ICON_BUTTON_CLASS } from '@/components/ui/IconButton';
import TypeBadge from '@/components/ui/TypeBadge';
import MonitorPicker from './MonitorPicker';
import type { Action, DerivedState, EditableSection } from './useStatusPageFormState';
import { MONITOR_STATUS_DOT } from '@/lib/status-tones';
import { COMPONENT_STATUS_LABEL, type ComponentStatus } from '@/lib/status-page-status';

export const MONITOR_DRAG_MIME = 'application/x-status-monitor';

interface SectionListProps {
  sections: EditableSection[];
  monitors: Monitor[];
  monitorsLoading: boolean;
  /** What visitors see per monitor id now; null while a group's members load. */
  statuses: ReadonlyMap<string, ComponentStatus | null>;
  /** Direct members of the groups on the page, once loaded. */
  groupMembers: ReadonlyMap<string, readonly string[]>;
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
  statuses,
  groupMembers,
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
        <div className="flex min-w-0 items-baseline gap-3">
          <h3 className="text-title font-semibold text-white">Page structure</h3>
          <span className="hidden truncate text-xs text-slate-500 sm:inline">
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
          className="glass glass-hover flex w-full flex-col items-center gap-2 px-6 py-12 text-center"
        >
          <span className="mb-2 inline-flex h-11 w-11 items-center justify-center rounded-full bg-well-hover text-slate-400">
            <Plus className="h-5 w-5" strokeWidth={1.75} aria-hidden="true" />
          </span>
          <span className="text-title font-semibold text-white">Create your first section</span>
          <span className="text-sm text-slate-400">
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
            statuses={statuses}
            groupMembers={groupMembers}
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
  statuses: ReadonlyMap<string, ComponentStatus | null>;
  groupMembers: ReadonlyMap<string, readonly string[]>;
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
  statuses,
  groupMembers,
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
      className={`glass group/section relative transition-colors has-[[aria-haspopup=listbox][aria-expanded=true]]:z-30 ${
        dropTarget ? 'border-cyan-500/50' : error ? 'border-rose-500/40' : ''
      }`}
    >
      <div className="flex items-center gap-1.5 px-3 py-2.5">
        <button
          type="button"
          onClick={onToggleCollapsed}
          aria-expanded={!collapsed}
          aria-label={collapsed ? `Expand ${section.title || 'section'}` : `Collapse ${section.title || 'section'}`}
          className={ICON_BUTTON_CLASS}
        >
          <ChevronDown
            className={`transition-transform duration-fast ${collapsed ? '-rotate-90' : ''}`}
            strokeWidth={1.75}
            aria-hidden="true"
          />
        </button>
        <input
          type="text"
          value={section.title}
          onChange={(event) => dispatch({ type: 'rename_section', sectionId: section.id, title: event.target.value })}
          placeholder="Section title"
          aria-label="Section title"
          // Inline-edit look, but with a resting --input-edge underline
          // (>= 3:1) so the title reads as editable without a hover (touch
          // has none); the full box arrives on hover and focus.
          className="min-w-0 flex-1 rounded-lg border border-transparent border-b-[var(--input-edge)] bg-transparent px-2 py-1 text-title font-semibold text-white placeholder:font-normal placeholder:text-slate-500 transition-colors hover:border-[var(--input-edge)] focus:border-[var(--focus)] focus:bg-control focus:shadow-[0_0_0_1px_var(--focus)] focus:outline-none"
        />
        <div className="flex flex-shrink-0 items-center gap-0.5 transition-opacity lg:opacity-0 lg:focus-within:opacity-100 lg:group-hover/section:opacity-100">
          <IconButton
            icon={<ArrowUp strokeWidth={1.75} />}
            label="Move section up"
            disabled={index === 0}
            onClick={() => dispatch({ type: 'reorder_section', sectionId: section.id, direction: 'up' })}
          />
          <IconButton
            icon={<ArrowDown strokeWidth={1.75} />}
            label="Move section down"
            disabled={index === total - 1}
            onClick={() => dispatch({ type: 'reorder_section', sectionId: section.id, direction: 'down' })}
          />
          <IconButton
            icon={<Trash2 strokeWidth={1.75} />}
            label={`Delete section ${section.title || index + 1}`}
            danger
            onClick={() => dispatch({ type: 'remove_section', sectionId: section.id })}
          />
        </div>
        <span className="hidden flex-shrink-0 whitespace-nowrap pl-1 pr-2.5 text-xs tabular-nums text-slate-500 sm:inline">
          {section.monitors.length === 0
            ? 'empty'
            : `${section.monitors.length} monitor${section.monitors.length === 1 ? '' : 's'}`}
        </span>
      </div>

      {error && <p className="px-5 pb-2 text-xs text-rose-300">{error}</p>}

      {!collapsed && (
        <div ref={bodyRef} className="px-3 pb-3">
          {section.monitors.length === 0 ? (
            <p
              className={`glass-well mb-2 px-3 py-5 text-center text-xs transition-colors ${
                dropTarget ? 'text-cyan-300 ring-1 ring-inset ring-cyan-500/50' : 'text-slate-500'
              }`}
            >
              Empty section — add monitors below or drag them here.
            </p>
          ) : (
            <div className="space-y-1">
              {section.monitors.map((entry, entryIndex) => {
                const monitor = monitorIndex.get(entry.monitor_id);
                if (!monitor) return null;
                return (
                  <div key={entry.monitor_id}>
                    <DropLine active={dropTarget?.index === entryIndex} />
                    <MonitorRow
                      monitor={monitor}
                      status={statuses.get(entry.monitor_id) ?? null}
                      memberCount={groupMembers.get(entry.monitor_id)?.length}
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
        className={`rounded-full bg-cyan-400 transition-[height,margin,opacity] duration-fast ${
          active ? 'my-0.5 h-0.5 opacity-100' : 'h-0 opacity-0'
        }`}
      />
    </div>
  );
}

interface MonitorRowProps {
  monitor: Monitor;
  /** What visitors see; null while unknown (a group whose members are loading). */
  status: ComponentStatus | null;
  /** Direct members, for groups, once loaded. */
  memberCount?: number;
  displayName: string;
  rowIndex: number;
  onSetDisplayName: (value: string) => void;
  onRemove: () => void;
}

function MonitorRow({
  monitor,
  status,
  memberCount,
  displayName,
  rowIndex,
  onSetDisplayName,
  onRemove,
}: MonitorRowProps) {
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

  // A renamed row says what it stands for; the rename button is beside it.
  const subtitle = renamed
    ? `Renamed from ${monitor.name}`
    : isGroup
      ? memberCount === undefined
        ? ''
        : `${memberCount} member${memberCount === 1 ? '' : 's'}`
      : monitor.url || '';

  // The dot is what a visitor sees now; a paused monitor says so too.
  const statusTitle = status
    ? `${COMPONENT_STATUS_LABEL[status]} on the public page${monitor.enabled ? '' : ' (monitor paused)'}`
    : 'Loading status';

  return (
    <div
      data-row-index={rowIndex}
      draggable={!editing}
      onDragStart={(event) => {
        event.dataTransfer.effectAllowed = 'move';
        event.dataTransfer.setData(MONITOR_DRAG_MIME, JSON.stringify({ monitorId: monitor.id }));
        event.dataTransfer.setData('text/plain', monitor.id);
      }}
      className={`glass-well group/row flex items-center gap-2.5 px-2.5 py-2 transition-colors hover:bg-well-hover ${
        editing ? '' : 'cursor-grab active:cursor-grabbing'
      }`}
    >
      <GripVertical
        className="h-3.5 w-3.5 flex-shrink-0 text-slate-500 opacity-0 transition-opacity group-hover/row:opacity-100"
        strokeWidth={1.75}
        aria-hidden="true"
      />
      <span
        className={`h-2 w-2 flex-shrink-0 rounded-full ${
          status ? MONITOR_STATUS_DOT[status] : 'bg-well-hover'
        }`}
        title={statusTitle}
      >
        <span className="sr-only">{statusTitle}</span>
      </span>
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
          <p className="truncate text-sm text-slate-200">{renamed ? displayName : monitor.name}</p>
          {subtitle && <p className="truncate text-xs text-slate-500">{subtitle}</p>}
        </div>
      )}
      {!editing && (
        <>
          <div className="flex flex-shrink-0 items-center gap-0.5 transition-opacity lg:opacity-0 lg:focus-within:opacity-100 lg:group-hover/row:opacity-100">
            <IconButton
              icon={<Pencil strokeWidth={1.75} />}
              label={`Rename ${monitor.name} on the public page`}
              onClick={startEditing}
            />
            <IconButton
              icon={<X strokeWidth={1.75} />}
              label={`Remove ${monitor.name} from page`}
              danger
              onClick={onRemove}
            />
          </div>
          <TypeBadge type={monitor.type} config={monitor.config} className="hidden sm:inline-flex" />
        </>
      )}
    </div>
  );
}
