'use client';

import { useEffect, useState } from 'react';
import type {
  CreateStatusPageRequest,
  Monitor,
  StatusPage,
  StatusPageSection,
  UpdateStatusPageRequest,
} from '@/lib/types';
import { getMonitors } from '@/lib/api';

interface StatusPageFormProps {
  statusPage?: StatusPage;
  onSubmit: (data: CreateStatusPageRequest | UpdateStatusPageRequest) => Promise<void>;
  onCancel?: () => void;
  loading?: boolean;
}

interface EditableSectionMonitor {
  monitor_id: string;
  display_name: string;
}

interface EditableSection {
  id: string;
  title: string;
  monitors: EditableSectionMonitor[];
}

const DEFAULT_PRIMARY = '#22d3ee';
const DEFAULT_SECONDARY = '#64748b';
const DEFAULT_SECTION_TITLE = 'Services';
const FALLBACK_UNASSIGNED_SECTION_TITLE = 'Other';

function createSection(title = 'New Section'): EditableSection {
  return {
    id: `section-${Math.random().toString(36).slice(2, 10)}`,
    title,
    monitors: [],
  };
}

function getInitialSections(statusPage?: StatusPage): EditableSection[] {
  if (statusPage?.sections?.length) {
    return statusPage.sections.map((section, index) => ({
      id: section.id || `section-${index}`,
      title: section.title || `Section ${index + 1}`,
      monitors: (section.monitors || []).map((monitor) => ({
        monitor_id: monitor.monitor_id,
        display_name: monitor.display_name || '',
      })),
    }));
  }

  if (statusPage?.monitor_ids?.length) {
    return [
      {
        id: 'legacy-services',
        title: DEFAULT_SECTION_TITLE,
        monitors: statusPage.monitor_ids.map((monitor_id) => ({ monitor_id, display_name: '' })),
      },
    ];
  }

  return [createSection(DEFAULT_SECTION_TITLE)];
}

function getInitialSelectedMonitorIds(statusPage?: StatusPage, sections?: EditableSection[]): string[] {
  const ordered = new Set<string>();
  (statusPage?.monitor_ids || []).forEach((monitorId) => ordered.add(monitorId));
  (sections || []).forEach((section) => {
    section.monitors.forEach((monitor) => ordered.add(monitor.monitor_id));
  });
  return Array.from(ordered);
}

function removeMonitorFromSections(sections: EditableSection[], monitorId: string): EditableSection[] {
  return sections.map((section) => ({
    ...section,
    monitors: section.monitors.filter((monitor) => monitor.monitor_id !== monitorId),
  }));
}

function findAssignedMonitorIds(sections: EditableSection[]): Set<string> {
  const assigned = new Set<string>();
  sections.forEach((section) => {
    section.monitors.forEach((monitor) => assigned.add(monitor.monitor_id));
  });
  return assigned;
}

export default function StatusPageForm({
  statusPage,
  onSubmit,
  onCancel,
  loading = false,
}: StatusPageFormProps) {
  const [monitors, setMonitors] = useState<Monitor[]>([]);
  const [formData, setFormData] = useState({
    slug: statusPage?.slug || '',
    title: statusPage?.title || '',
    description: statusPage?.description || '',
    logo_url: statusPage?.logo_url || '',
    primary_color: statusPage?.primary_color || DEFAULT_PRIMARY,
    secondary_color: statusPage?.secondary_color || DEFAULT_SECONDARY,
  });
  const [selectedMonitorIds, setSelectedMonitorIds] = useState<string[]>([]);
  const [sections, setSections] = useState<EditableSection[]>([]);
  const [errors, setErrors] = useState<Record<string, string>>({});
  const [draggedSectionId, setDraggedSectionId] = useState<string | null>(null);
  const [sectionDropTargetIndex, setSectionDropTargetIndex] = useState<number | null>(null);
  const [draggedMonitorId, setDraggedMonitorId] = useState<string | null>(null);
  const [monitorDropTargetKey, setMonitorDropTargetKey] = useState<string | null>(null);
  const [activeMonitorSectionId, setActiveMonitorSectionId] = useState<string | null>(null);

  useEffect(() => {
    loadMonitors();
  }, []);

  useEffect(() => {
    const nextSections = getInitialSections(statusPage);
    setSections(nextSections);
    setSelectedMonitorIds(getInitialSelectedMonitorIds(statusPage, nextSections));
    setFormData({
      slug: statusPage?.slug || '',
      title: statusPage?.title || '',
      description: statusPage?.description || '',
      logo_url: statusPage?.logo_url || '',
      primary_color: statusPage?.primary_color || DEFAULT_PRIMARY,
      secondary_color: statusPage?.secondary_color || DEFAULT_SECONDARY,
    });
  }, [statusPage]);

  const loadMonitors = async () => {
    try {
      const response = await getMonitors({ page_size: 100 });
      setMonitors(response.items || []);
    } catch (error) {
      console.error('Failed to load monitors:', error);
    }
  };

  const assignedMonitorIds = findAssignedMonitorIds(sections);
  const unassignedMonitors = selectedMonitorIds.filter((monitorId) => !assignedMonitorIds.has(monitorId));

  const getMonitor = (monitorId: string) => monitors.find((monitor) => monitor.id === monitorId);

  const handleMonitorSelection = (monitorId: string) => {
    setSelectedMonitorIds((current) => {
      if (current.includes(monitorId)) {
        setSections((existing) => removeMonitorFromSections(existing, monitorId));
        return current.filter((id) => id !== monitorId);
      }
      return [...current, monitorId];
    });
  };

  const handleSelectAll = () => {
    setSelectedMonitorIds(monitors.map((monitor) => monitor.id));
  };

  const handleDeselectAll = () => {
    setSelectedMonitorIds([]);
    setSections((existing) => existing.map((section) => ({ ...section, monitors: [] })));
  };

  const handleAddSection = () => {
    setSections((current) => [...current, createSection()]);
  };

  const handleRemoveSection = (sectionId: string) => {
    setSections((current) => current.filter((section) => section.id !== sectionId));
  };

  const handleSectionTitleChange = (sectionId: string, title: string) => {
    setSections((current) =>
      current.map((section) => (section.id === sectionId ? { ...section, title } : section)),
    );
  };

  const placeMonitor = (
    monitorId: string,
    targetSectionId: string,
    targetIndex?: number,
    preserveDisplayName = '',
  ) => {
    setSections((current) => {
      const withoutMonitor = removeMonitorFromSections(current, monitorId);
      return withoutMonitor.map((section) => {
        if (section.id !== targetSectionId) return section;
        const nextMonitors = [...section.monitors];
        const index = targetIndex === undefined ? nextMonitors.length : targetIndex;
        nextMonitors.splice(index, 0, { monitor_id: monitorId, display_name: preserveDisplayName });
        return { ...section, monitors: nextMonitors };
      });
    });
  };

  const unassignMonitor = (monitorId: string) => {
    setSections((current) => removeMonitorFromSections(current, monitorId));
  };

  const updateDisplayName = (sectionId: string, monitorId: string, value: string) => {
    setSections((current) =>
      current.map((section) => {
        if (section.id !== sectionId) return section;
        return {
          ...section,
          monitors: section.monitors.map((monitor) =>
            monitor.monitor_id === monitorId ? { ...monitor, display_name: value } : monitor,
          ),
        };
      }),
    );
  };

  const handleSectionDragStart = (event: React.DragEvent, sectionId: string) => {
    event.dataTransfer.effectAllowed = 'move';
    event.dataTransfer.setData('application/x-status-section-id', sectionId);
    event.dataTransfer.setData('text/plain', sectionId);
    setDraggedSectionId(sectionId);
  };

  const handleSectionDragEnd = () => {
    setDraggedSectionId(null);
    setSectionDropTargetIndex(null);
  };

  const handleSectionDragOver = (event: React.DragEvent, targetIndex: number) => {
    if (!draggedSectionId) return;
    event.preventDefault();
    event.dataTransfer.dropEffect = 'move';
    setSectionDropTargetIndex(targetIndex);
  };

  const handleSectionDrop = (event: React.DragEvent, targetIndex: number) => {
    event.preventDefault();
    const sourceSectionId = event.dataTransfer.getData('application/x-status-section-id');
    if (!sourceSectionId) {
      setSectionDropTargetIndex(null);
      return;
    }

    setSections((current) => {
      const sourceIndex = current.findIndex((section) => section.id === sourceSectionId);
      if (sourceIndex === -1) return current;
      const nextSections = [...current];
      const [moved] = nextSections.splice(sourceIndex, 1);
      const insertIndex = sourceIndex < targetIndex ? targetIndex - 1 : targetIndex;
      nextSections.splice(insertIndex, 0, moved);
      return nextSections;
    });
    setDraggedSectionId(null);
    setSectionDropTargetIndex(null);
  };

  const handleMonitorDragStart = (
    event: React.DragEvent,
    monitorId: string,
    sourceSectionId: string | null,
    displayName = '',
  ) => {
    event.dataTransfer.effectAllowed = 'move';
    event.dataTransfer.setData(
      'application/x-status-monitor',
      JSON.stringify({ monitorId, sourceSectionId, displayName }),
    );
    event.dataTransfer.setData('text/plain', monitorId);
    setDraggedMonitorId(monitorId);
  };

  const handleMonitorDragEnd = () => {
    setDraggedMonitorId(null);
    setMonitorDropTargetKey(null);
    setActiveMonitorSectionId(null);
  };

  const handleMonitorDragOver = (
    event: React.DragEvent,
    targetSectionId: string,
    targetIndex?: number,
  ) => {
    if (!draggedMonitorId) return;
    event.preventDefault();
    event.dataTransfer.dropEffect = 'move';
    setActiveMonitorSectionId(targetSectionId);
    setMonitorDropTargetKey(`${targetSectionId}:${targetIndex ?? 'end'}`);
  };

  const handleMonitorDrop = (
    event: React.DragEvent,
    targetSectionId: string,
    targetIndex?: number,
  ) => {
    event.preventDefault();
    const raw = event.dataTransfer.getData('application/x-status-monitor');
    if (!raw) return;

    try {
      const parsed = JSON.parse(raw) as {
        monitorId: string;
        sourceSectionId: string | null;
        displayName?: string;
      };
      if (!selectedMonitorIds.includes(parsed.monitorId)) {
        setSelectedMonitorIds((current) => [...current, parsed.monitorId]);
      }
      placeMonitor(parsed.monitorId, targetSectionId, targetIndex, parsed.displayName || '');
      setDraggedMonitorId(null);
      setMonitorDropTargetKey(null);
      setActiveMonitorSectionId(null);
    } catch (error) {
      console.error('Invalid monitor drag payload', error);
    }
  };

  const handleUnassignedDrop = (event: React.DragEvent) => {
    event.preventDefault();
    const raw = event.dataTransfer.getData('application/x-status-monitor');
    if (!raw) return;

    try {
      const parsed = JSON.parse(raw) as { monitorId: string };
      unassignMonitor(parsed.monitorId);
      setDraggedMonitorId(null);
      setMonitorDropTargetKey(null);
      setActiveMonitorSectionId(null);
    } catch (error) {
      console.error('Invalid monitor drag payload', error);
    }
  };

  const handleSubmit = async (e: React.FormEvent) => {
    e.preventDefault();
    setErrors({});

    const newErrors: Record<string, string> = {};
    if (!formData.slug.trim()) {
      newErrors.slug = 'Slug is required';
    } else if (!/^[a-z0-9-]+$/.test(formData.slug)) {
      newErrors.slug = 'Only lowercase letters, numbers, and hyphens allowed';
    }
    if (!formData.title.trim()) {
      newErrors.title = 'Title is required';
    }
    sections.forEach((section, index) => {
      if (!section.title.trim()) {
        newErrors[`section-${section.id}`] = `Section ${index + 1} needs a title`;
      }
    });

    if (Object.keys(newErrors).length > 0) {
      setErrors(newErrors);
      return;
    }

    const normalizedSections: StatusPageSection[] = sections.map((section, index) => ({
      title: section.title.trim(),
      position: index,
      monitors: section.monitors.map((monitor, monitorIndex) => ({
        monitor_id: monitor.monitor_id,
        position: monitorIndex,
        display_name: monitor.display_name.trim() || undefined,
      })),
    }));

    if (unassignedMonitors.length > 0) {
      normalizedSections.push({
        title: FALLBACK_UNASSIGNED_SECTION_TITLE,
        position: normalizedSections.length,
        monitors: unassignedMonitors.map((monitorId, monitorIndex) => ({
          monitor_id: monitorId,
          position: monitorIndex,
        })),
      });
    }

    const monitorDisplayNames: Record<string, string> = {};
    normalizedSections.forEach((section) => {
      section.monitors?.forEach((monitor) => {
        if (monitor.display_name) {
          monitorDisplayNames[monitor.monitor_id] = monitor.display_name;
        }
      });
    });

    const requestData: CreateStatusPageRequest | UpdateStatusPageRequest = {
      slug: formData.slug.trim(),
      title: formData.title.trim(),
      description: formData.description.trim(),
      logo_url: formData.logo_url.trim(),
      primary_color: formData.primary_color.trim() || DEFAULT_PRIMARY,
      secondary_color: formData.secondary_color.trim() || DEFAULT_SECONDARY,
      monitor_ids: selectedMonitorIds,
      monitor_display_names: monitorDisplayNames,
      sections: normalizedSections,
    };

    await onSubmit(requestData);
  };

  return (
    <form onSubmit={handleSubmit} className="space-y-6">
      <div>
        <h3 className="mb-4 text-sm font-medium text-white">Basic Information</h3>
        <div className="space-y-4">
          <div>
            <label className="mb-1.5 block text-xs font-medium text-slate-400">URL Slug</label>
            <input
              type="text"
              value={formData.slug}
              onChange={(e) => setFormData({ ...formData, slug: e.target.value.toLowerCase() })}
              placeholder="my-status-page"
              className="input"
            />
            {errors.slug && <p className="mt-1 text-xs text-rose-400">{errors.slug}</p>}
            <p className="mt-1 text-xs text-slate-500">Lowercase letters, numbers, and hyphens only</p>
          </div>

          <div>
            <label className="mb-1.5 block text-xs font-medium text-slate-400">Title</label>
            <input
              type="text"
              value={formData.title}
              onChange={(e) => setFormData({ ...formData, title: e.target.value })}
              placeholder="My Service Status"
              className="input"
            />
            {errors.title && <p className="mt-1 text-xs text-rose-400">{errors.title}</p>}
          </div>

          <div>
            <label className="mb-1.5 block text-xs font-medium text-slate-400">Description (optional)</label>
            <textarea
              value={formData.description}
              onChange={(e) => setFormData({ ...formData, description: e.target.value })}
              placeholder="Describe your service status page..."
              rows={3}
              className="input min-h-[96px] resize-none"
            />
          </div>
        </div>
      </div>

      <div>
        <h3 className="mb-4 text-sm font-medium text-white">Branding</h3>
        <div className="space-y-4">
          <div>
            <label className="mb-1.5 block text-xs font-medium text-slate-400">Logo URL (optional)</label>
            <input
              type="url"
              value={formData.logo_url}
              onChange={(e) => setFormData({ ...formData, logo_url: e.target.value })}
              placeholder="https://example.com/logo.png"
              className="input"
            />
          </div>

          <div className="grid grid-cols-2 gap-4">
            <div>
              <label className="mb-1.5 block text-xs font-medium text-slate-400">Primary Color</label>
              <div className="flex gap-2">
                <input
                  type="text"
                  value={formData.primary_color}
                  onChange={(e) => setFormData({ ...formData, primary_color: e.target.value })}
                  className="input font-mono"
                />
                <input
                  type="color"
                  value={formData.primary_color}
                  onChange={(e) => setFormData({ ...formData, primary_color: e.target.value })}
                  className="h-9 w-12 cursor-pointer rounded-lg border border-white/[0.08] bg-transparent"
                />
              </div>
            </div>
            <div>
              <label className="mb-1.5 block text-xs font-medium text-slate-400">Secondary Color</label>
              <div className="flex gap-2">
                <input
                  type="text"
                  value={formData.secondary_color}
                  onChange={(e) => setFormData({ ...formData, secondary_color: e.target.value })}
                  className="input font-mono"
                />
                <input
                  type="color"
                  value={formData.secondary_color}
                  onChange={(e) => setFormData({ ...formData, secondary_color: e.target.value })}
                  className="h-9 w-12 cursor-pointer rounded-lg border border-white/[0.08] bg-transparent"
                />
              </div>
            </div>
          </div>
        </div>
      </div>

      <div className="space-y-4">
        <div className="flex items-center justify-between">
          <div>
            <h3 className="text-sm font-medium text-white">Monitor Sections</h3>
            <p className="mt-1 text-xs text-slate-500">
              Select monitors, then drag unassigned items into sections or between sections.
            </p>
          </div>
          <div className="flex items-center gap-2">
            <span className="text-xs text-cyan-400">{selectedMonitorIds.length} selected</span>
            <button type="button" onClick={handleAddSection} className="btn btn-secondary btn-xs">
              Add Section
            </button>
          </div>
        </div>

        <div className="rounded-xl border border-white/[0.06] bg-slate-950/40 p-4">
          <div className="mb-3 flex items-center justify-between">
            <h4 className="text-xs font-semibold uppercase tracking-wider text-slate-400">Selected Monitors</h4>
            <div className="flex items-center gap-2">
              <button type="button" onClick={handleSelectAll} className="btn btn-outline btn-xs">
                Select All
              </button>
              <button type="button" onClick={handleDeselectAll} className="btn btn-outline btn-xs">
                Deselect All
              </button>
            </div>
          </div>
          <div className="max-h-64 space-y-1 overflow-y-auto pr-1">
            {monitors.length === 0 ? (
              <p className="p-6 text-center text-sm text-slate-500">No monitors available</p>
            ) : (
              monitors.map((monitor) => {
                const selected = selectedMonitorIds.includes(monitor.id);
                return (
                  <label
                    key={monitor.id}
                    className={`flex cursor-pointer items-center gap-3 rounded-lg border px-3 py-2.5 transition-colors ${
                      selected
                        ? 'border-cyan-500/30 bg-cyan-500/10'
                        : 'border-transparent hover:bg-white/[0.03]'
                    }`}
                  >
                    <input
                      type="checkbox"
                      checked={selected}
                      onChange={() => handleMonitorSelection(monitor.id)}
                      className="sr-only"
                    />
                    <div
                      className={`flex h-4 w-4 items-center justify-center rounded border transition-colors ${
                        selected ? 'border-cyan-500 bg-cyan-500' : 'border-slate-600'
                      }`}
                    >
                      {selected && (
                        <svg className="h-3 w-3 text-white" fill="none" viewBox="0 0 24 24" stroke="currentColor">
                          <path strokeLinecap="round" strokeLinejoin="round" strokeWidth={3} d="M5 13l4 4L19 7" />
                        </svg>
                      )}
                    </div>
                    <div className="min-w-0 flex-1">
                      <p className="truncate text-sm font-medium text-white">{monitor.name}</p>
                      <p className="truncate text-xs text-slate-500">
                        {monitor.type.toUpperCase()}
                        {monitor.url && ` · ${monitor.url}`}
                      </p>
                    </div>
                    <span className={`text-xs ${monitor.enabled ? 'text-emerald-400' : 'text-slate-500'}`}>
                      {monitor.enabled ? 'Active' : 'Paused'}
                    </span>
                  </label>
                );
              })
            )}
          </div>
        </div>

        <div
          className={`rounded-xl border border-dashed p-4 transition-colors ${
            monitorDropTargetKey === 'unassigned'
              ? 'border-cyan-400 bg-cyan-500/10 shadow-[0_0_0_1px_rgba(34,211,238,0.14)]'
              : draggedMonitorId
                ? 'border-cyan-500/35 bg-cyan-500/5'
              : 'border-white/[0.08] bg-slate-950/20'
          }`}
          onDragOver={(event) => {
            if (!draggedMonitorId) return;
            event.preventDefault();
            setMonitorDropTargetKey('unassigned');
            setActiveMonitorSectionId(null);
          }}
          onDrop={handleUnassignedDrop}
        >
          <div className="mb-3 flex items-center justify-between">
            <div>
              <h4 className="text-xs font-semibold uppercase tracking-wider text-slate-400">Unassigned</h4>
              <p className="mt-1 text-xs text-slate-500">
                These monitors will still be selected and render in the fallback Other section.
              </p>
            </div>
            <span className="text-xs text-slate-500">{unassignedMonitors.length}</span>
          </div>
          {unassignedMonitors.length === 0 ? (
            <p className="text-sm text-slate-500">All selected monitors are assigned to a section.</p>
          ) : (
            <div className="grid gap-2 md:grid-cols-2">
              {unassignedMonitors.map((monitorId) => {
                const monitor = getMonitor(monitorId);
                if (!monitor) return null;
                return (
                  <div
                    key={monitorId}
                    draggable
                    onDragStart={(event) => handleMonitorDragStart(event, monitorId, null)}
                    onDragEnd={handleMonitorDragEnd}
                    className={`rounded-lg border border-white/[0.08] bg-slate-900/60 p-3 transition-opacity ${
                      draggedMonitorId === monitorId ? 'opacity-50' : ''
                    }`}
                  >
                    <div className="flex items-start justify-between gap-3">
                      <div className="min-w-0">
                        <p className="truncate text-sm font-medium text-white">{monitor.name}</p>
                        <p className="truncate text-xs text-slate-500">
                          {monitor.type.toUpperCase()}
                          {monitor.url && ` · ${monitor.url}`}
                        </p>
                      </div>
                      <span className="cursor-grab text-[10px] uppercase tracking-wider text-cyan-300 active:cursor-grabbing">
                        Drag monitor
                      </span>
                    </div>
                  </div>
                );
              })}
            </div>
          )}
        </div>

        <div className="space-y-4">
          {sections.map((section, sectionIndex) => (
            <div key={section.id} className="space-y-2">
              <SectionDropZone
                active={sectionDropTargetIndex === sectionIndex}
                onDragOver={(event) => handleSectionDragOver(event, sectionIndex)}
                onDrop={(event) => handleSectionDrop(event, sectionIndex)}
                label={sectionIndex === 0 ? 'Drop section at top' : 'Drop section here'}
              />
              <div
                draggable
                onDragStart={(event) => handleSectionDragStart(event, section.id)}
                onDragEnd={handleSectionDragEnd}
                className={`rounded-xl border bg-slate-900/50 p-4 transition-all ${
                  activeMonitorSectionId === section.id
                    ? 'border-cyan-500/40 ring-1 ring-cyan-500/20'
                    : 'border-white/[0.08]'
                } ${draggedSectionId === section.id ? 'cursor-grabbing opacity-50' : 'cursor-grab active:cursor-grabbing'}`}
              >
                <div className="mb-4 flex items-center gap-3">
                  <div
                    className="flex h-9 w-9 flex-shrink-0 items-center justify-center rounded-lg border border-white/[0.08] bg-slate-950/60 text-slate-400 transition-colors hover:border-cyan-500/50 hover:text-cyan-300"
                    aria-hidden="true"
                  >
                    <svg className="h-4 w-4" viewBox="0 0 20 20" fill="currentColor" aria-hidden="true">
                      <path d="M7 4a1.5 1.5 0 1 1-3 0 1.5 1.5 0 0 1 3 0Zm0 6a1.5 1.5 0 1 1-3 0 1.5 1.5 0 0 1 3 0Zm0 6a1.5 1.5 0 1 1-3 0 1.5 1.5 0 0 1 3 0Zm9-12a1.5 1.5 0 1 1-3 0 1.5 1.5 0 0 1 3 0Zm0 6a1.5 1.5 0 1 1-3 0 1.5 1.5 0 0 1 3 0Zm0 6a1.5 1.5 0 1 1-3 0 1.5 1.5 0 0 1 3 0Z" />
                    </svg>
                  </div>
                  <input
                    type="text"
                    value={section.title}
                    onChange={(event) => handleSectionTitleChange(section.id, event.target.value)}
                    className="input h-9 flex-1"
                    placeholder="Section title"
                    draggable={false}
                  />
                  <span className="text-[10px] uppercase tracking-wider text-cyan-300">Drag section</span>
                  <button
                    type="button"
                    onClick={() => handleRemoveSection(section.id)}
                    className="btn btn-outline btn-xs"
                    draggable={false}
                  >
                    Remove
                  </button>
                </div>
                {errors[`section-${section.id}`] && (
                  <p className="mb-3 text-xs text-rose-400">{errors[`section-${section.id}`]}</p>
                )}

                <div
                  className={`space-y-2 rounded-lg border border-dashed bg-slate-950/30 p-3 transition-colors ${
                    activeMonitorSectionId === section.id
                      ? 'border-cyan-500/50 bg-cyan-500/5'
                      : 'border-white/[0.06]'
                  }`}
                  onDragOver={(event) => handleMonitorDragOver(event, section.id)}
                  onDrop={(event) => handleMonitorDrop(event, section.id)}
                >
                  {section.monitors.length === 0 ? (
                    <p className="text-sm text-slate-500">Drop monitors here.</p>
                  ) : (
                    section.monitors.map((sectionMonitor, monitorIndex) => {
                      const monitor = getMonitor(sectionMonitor.monitor_id);
                      if (!monitor) return null;

                      return (
                        <div key={sectionMonitor.monitor_id} className="space-y-2">
                          <MonitorDropZone
                            active={monitorDropTargetKey === `${section.id}:${monitorIndex}`}
                            onDragOver={(event) => handleMonitorDragOver(event, section.id, monitorIndex)}
                            onDrop={(event) => handleMonitorDrop(event, section.id, monitorIndex)}
                          />
                          <div
                            draggable
                            onDragStart={(event) =>
                              handleMonitorDragStart(
                                event,
                                sectionMonitor.monitor_id,
                                section.id,
                                sectionMonitor.display_name,
                              )
                            }
                            onDragEnd={handleMonitorDragEnd}
                            className={`rounded-lg border border-white/[0.08] bg-slate-900/80 p-3 transition-all ${
                              draggedMonitorId === sectionMonitor.monitor_id
                                ? 'cursor-grabbing opacity-50'
                                : 'cursor-grab hover:border-cyan-500/30 active:cursor-grabbing'
                            }`}
                          >
                            <div className="mb-3 flex items-start justify-between gap-3">
                              <div className="min-w-0">
                                <p className="truncate text-sm font-medium text-white">
                                  {sectionMonitor.display_name.trim() || monitor.name}
                                </p>
                                <p className="truncate text-xs text-slate-500">
                                  {monitor.type.toUpperCase()}
                                  {monitor.url && ` · ${monitor.url}`}
                                </p>
                              </div>
                              <div className="flex items-center gap-2">
                                <span className="rounded-full border border-white/[0.08] px-2 py-1 text-[10px] uppercase tracking-[0.22em] text-cyan-300">
                                  Drag monitor
                                </span>
                                <button
                                  type="button"
                                  onClick={() => unassignMonitor(sectionMonitor.monitor_id)}
                                  className="btn btn-outline btn-xs"
                                  draggable={false}
                                >
                                  Unassign
                                </button>
                              </div>
                            </div>
                            <input
                              type="text"
                              value={sectionMonitor.display_name}
                              onChange={(event) =>
                                updateDisplayName(section.id, sectionMonitor.monitor_id, event.target.value)
                              }
                              className="input h-9"
                              placeholder="Optional public display name"
                              draggable={false}
                            />
                          </div>
                        </div>
                      );
                    })
                  )}
                  <MonitorDropZone
                    active={monitorDropTargetKey === `${section.id}:end`}
                    onDragOver={(event) => handleMonitorDragOver(event, section.id, section.monitors.length)}
                    onDrop={(event) => handleMonitorDrop(event, section.id, section.monitors.length)}
                  />
                </div>
              </div>
            </div>
          ))}
          <SectionDropZone
            active={sectionDropTargetIndex === sections.length}
            onDragOver={(event) => handleSectionDragOver(event, sections.length)}
            onDrop={(event) => handleSectionDrop(event, sections.length)}
            label="Drop section at end"
          />
        </div>
      </div>

      <div className="flex items-center justify-end gap-3 border-t border-white/[0.06] pt-4">
        {onCancel && (
          <button
            type="button"
            onClick={onCancel}
            disabled={loading}
            className="btn btn-secondary btn-sm disabled:opacity-50"
          >
            Cancel
          </button>
        )}
        <button
          type="submit"
          disabled={loading}
          className="btn btn-primary btn-sm disabled:opacity-50"
        >
          {loading ? 'Saving...' : statusPage ? 'Save Changes' : 'Create Status Page'}
        </button>
      </div>
    </form>
  );
}

function SectionDropZone({
  active,
  label,
  onDragOver,
  onDrop,
}: {
  active: boolean;
  label: string;
  onDragOver: (event: React.DragEvent) => void;
  onDrop: (event: React.DragEvent) => void;
}) {
  return (
    <div
      onDragOver={onDragOver}
      onDrop={onDrop}
      className={`flex min-h-10 items-center justify-center rounded-xl border border-dashed px-3 text-[10px] uppercase tracking-[0.24em] transition-all ${
        active
          ? 'border-cyan-400 bg-cyan-500/12 text-cyan-200 shadow-[0_0_0_1px_rgba(34,211,238,0.18)]'
          : 'border-white/[0.08] bg-slate-950/20 text-slate-500 hover:border-cyan-500/35 hover:text-slate-300'
      }`}
    >
      {label}
    </div>
  );
}

function MonitorDropZone({
  active,
  onDragOver,
  onDrop,
}: {
  active: boolean;
  onDragOver: (event: React.DragEvent) => void;
  onDrop: (event: React.DragEvent) => void;
}) {
  return (
    <div
      onDragOver={onDragOver}
      onDrop={onDrop}
      className={`flex h-6 items-center justify-center rounded-lg border border-dashed px-2 text-[9px] uppercase tracking-[0.2em] transition-all ${
        active
          ? 'border-cyan-400/80 bg-cyan-500/10 text-cyan-200'
          : 'border-white/[0.08] bg-slate-950/10 text-slate-600'
      }`}
    >
      Drop monitor here
    </div>
  );
}
