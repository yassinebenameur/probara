'use client';

import { useCallback, useEffect, useRef, useState } from 'react';
import type { Monitor } from '@/lib/types';
import SectionCard from './SectionCard';
import type { DerivedState, EditableSection } from './useStatusPageFormState';

interface SectionsEditorProps {
  sections: EditableSection[];
  monitors: Monitor[];
  derived: DerivedState;
  errors: Record<string, string>;
  revealSectionId: string | null;
  onConsumeReveal: () => void;
  onAddSection: () => void;
  onRenameSection: (sectionId: string, title: string) => void;
  onRemoveSection: (sectionId: string) => void;
  onReorderSection: (sectionId: string, direction: 'up' | 'down') => void;
  onReorderMonitor: (sectionId: string, monitorId: string, direction: 'up' | 'down') => void;
  onRemoveMonitor: (monitorId: string) => void;
  onMoveMonitor: (monitorId: string, targetSectionId: string) => void;
  onAddNewSection: (title: string, monitorIds: string[]) => void;
  onSetDisplayName: (sectionId: string, monitorId: string, value: string) => void;
  onAddMonitorsToSection: (sectionId: string, monitorIds: string[]) => void;
  onDragStartMonitor: (event: React.DragEvent, monitorId: string) => void;
  onDropMonitorOnSection: (monitorId: string, sectionId: string) => void;
}

export default function SectionsEditor({
  sections,
  monitors,
  derived,
  errors,
  revealSectionId,
  onConsumeReveal,
  onAddSection,
  onRenameSection,
  onRemoveSection,
  onReorderSection,
  onReorderMonitor,
  onRemoveMonitor,
  onMoveMonitor,
  onAddNewSection,
  onSetDisplayName,
  onAddMonitorsToSection,
  onDragStartMonitor,
  onDropMonitorOnSection,
}: SectionsEditorProps) {
  const cardRefs = useRef<Map<string, HTMLDivElement>>(new Map());
  const [dropTargetId, setDropTargetId] = useState<string | null>(null);

  useEffect(() => {
    if (!revealSectionId) return;
    const element = cardRefs.current.get(revealSectionId);
    if (element) {
      element.scrollIntoView({ behavior: 'smooth', block: 'center' });
      element.classList.add('ring-1', 'ring-cyan-400/40');
      const timeout = setTimeout(() => {
        element.classList.remove('ring-1', 'ring-cyan-400/40');
      }, 1200);
      onConsumeReveal();
      return () => clearTimeout(timeout);
    }
  }, [revealSectionId, onConsumeReveal]);

  const handleSectionDragOver = useCallback(
    (sectionId: string) => (event: React.DragEvent) => {
      if (!event.dataTransfer.types.includes('application/x-status-monitor')) return;
      event.preventDefault();
      event.dataTransfer.dropEffect = 'move';
      setDropTargetId(sectionId);
    },
    [],
  );

  const handleSectionDrop = useCallback(
    (sectionId: string) => (event: React.DragEvent) => {
      event.preventDefault();
      const raw = event.dataTransfer.getData('application/x-status-monitor');
      setDropTargetId(null);
      if (!raw) return;
      try {
        const parsed = JSON.parse(raw) as { monitorId: string };
        if (parsed.monitorId) {
          onDropMonitorOnSection(parsed.monitorId, sectionId);
        }
      } catch {
        // ignore
      }
    },
    [onDropMonitorOnSection],
  );

  const registerCardRef = useCallback(
    (sectionId: string) => (element: HTMLDivElement | null) => {
      if (element) cardRefs.current.set(sectionId, element);
      else cardRefs.current.delete(sectionId);
    },
    [],
  );

  return (
    <div className="flex h-full flex-col gap-3">
      <div className="flex items-start justify-between gap-2">
        <div>
          <div className="flex items-center gap-2">
            <span aria-hidden="true" className="h-1 w-6 rounded-full bg-gradient-to-r from-cyan-500/60 to-cyan-500/0" />
            <h4 className="text-[11px] font-semibold uppercase tracking-[0.14em] text-slate-300">
              Sections
            </h4>
          </div>
          <p className="mt-1 text-[11px] text-slate-500">
            Monitors are on the page only when assigned to a section. {derived.membershipCount} monitor
            {derived.membershipCount === 1 ? '' : 's'} assigned.
          </p>
        </div>
        <button type="button" onClick={onAddSection} className="btn btn-secondary btn-xs">
          + New section
        </button>
      </div>

      {sections.length === 0 ? (
        <div className="flex flex-1 items-center justify-center rounded-xl border border-dashed border-white/[0.08] bg-slate-950/30 p-10 text-center">
          <div>
            <p className="text-sm text-slate-400">No sections yet.</p>
            <p className="mt-1 text-xs text-slate-500">
              Create a section to start grouping monitors on the public page.
            </p>
            <button type="button" onClick={onAddSection} className="btn btn-primary btn-xs mt-3">
              + Create your first section
            </button>
          </div>
        </div>
      ) : (
        <div className="space-y-3">
          {sections.map((section, index) => (
            <SectionCard
              key={section.id}
              section={section}
              index={index}
              total={sections.length}
              monitors={monitors}
              derived={derived}
              allSections={sections}
              error={errors[`section-${section.id}`]}
              highlighted={false}
              dropActive={dropTargetId === section.id}
              cardRef={registerCardRef(section.id)}
              onRename={(title) => onRenameSection(section.id, title)}
              onRemove={() => onRemoveSection(section.id)}
              onReorder={(direction) => onReorderSection(section.id, direction)}
              onReorderMonitor={(monitorId, direction) =>
                onReorderMonitor(section.id, monitorId, direction)
              }
              onRemoveMonitor={(monitorId) => onRemoveMonitor(monitorId)}
              onMoveMonitor={(monitorId, targetSectionId) =>
                onMoveMonitor(monitorId, targetSectionId)
              }
              onAddNewSection={(title, monitorIds) => onAddNewSection(title, monitorIds)}
              onSetDisplayName={(monitorId, value) =>
                onSetDisplayName(section.id, monitorId, value)
              }
              onAddMonitors={(monitorIds) => onAddMonitorsToSection(section.id, monitorIds)}
              onDragStartMonitor={onDragStartMonitor}
              onDragOverSection={handleSectionDragOver(section.id)}
              onDropOnSection={handleSectionDrop(section.id)}
              onDragLeaveSection={() => setDropTargetId(null)}
            />
          ))}
        </div>
      )}
    </div>
  );
}
