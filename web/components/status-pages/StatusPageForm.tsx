'use client';

import { useCallback, useEffect, useMemo, useState } from 'react';
import type {
  CreateStatusPageRequest,
  Monitor,
  StatusPage,
  StatusPageSection,
  UpdateStatusPageRequest,
} from '@/lib/types';
import { getMonitors } from '@/lib/api';
import CollapsibleSection from '@/components/ui/CollapsibleSection';
import FormField from '@/components/ui/FormField';
import FormActions from '@/components/ui/FormActions';
import MonitorLibrary, { AddToMenu } from './MonitorLibrary';
import SectionsEditor from './SectionsEditor';
import {
  DEFAULT_PRIMARY,
  DEFAULT_SECONDARY,
  newSectionId,
  useDerived,
  useFilteredMonitors,
  useStatusPageFormState,
  type Basics,
} from './useStatusPageFormState';

interface StatusPageFormProps {
  statusPage?: StatusPage;
  onSubmit: (data: CreateStatusPageRequest | UpdateStatusPageRequest) => Promise<void>;
  onCancel?: () => void;
  loading?: boolean;
}

const MONITOR_PAGE_SIZE = 100;
const MONITOR_DRAG_MIME = 'application/x-status-monitor';

export default function StatusPageForm({
  statusPage,
  onSubmit,
  onCancel,
  loading = false,
}: StatusPageFormProps) {
  const { state, dispatch } = useStatusPageFormState(statusPage);
  const [monitors, setMonitors] = useState<Monitor[]>([]);
  const [monitorsLoading, setMonitorsLoading] = useState(true);
  const [monitorsTotal, setMonitorsTotal] = useState(0);
  const [errors, setErrors] = useState<Record<string, string>>({});
  const [basicsOpen, setBasicsOpen] = useState(!statusPage);
  const [revealSectionId, setRevealSectionId] = useState<string | null>(null);

  const derived = useDerived(state, monitors);
  const filteredMonitors = useFilteredMonitors(monitors, state.filters);

  useEffect(() => {
    dispatch({ type: 'reset', statusPage });
    setBasicsOpen(!statusPage);
    setErrors({});
  }, [statusPage, dispatch]);

  useEffect(() => {
    let cancelled = false;
    async function load() {
      setMonitorsLoading(true);
      try {
        const all: Monitor[] = [];
        let page = 1;
        let total = 0;
        while (true) {
          const response = await getMonitors({ page, page_size: MONITOR_PAGE_SIZE });
          const items = response.items || [];
          all.push(...items);
          total = response.total ?? all.length;
          if (items.length < MONITOR_PAGE_SIZE || all.length >= total) break;
          page += 1;
          if (page > 100) break;
        }
        if (!cancelled) {
          setMonitors(all);
          setMonitorsTotal(total || all.length);
        }
      } catch (error) {
        console.error('Failed to load monitors:', error);
      } finally {
        if (!cancelled) setMonitorsLoading(false);
      }
    }
    load();
    return () => {
      cancelled = true;
    };
  }, []);

  const setBasic = useCallback(
    (key: keyof Basics, value: string) => {
      dispatch({ type: 'set_basic', key, value });
    },
    [dispatch],
  );

  const handleAddNewSectionWith = useCallback(
    (title: string, monitorIds: string[]) => {
      const id = newSectionId();
      dispatch({ type: 'add_section_with_monitors', id, title, monitorIds });
      dispatch({ type: 'clear_selection' });
      setRevealSectionId(id);
    },
    [dispatch],
  );

  const handleAddEmptySection = useCallback(() => {
    const id = newSectionId();
    dispatch({ type: 'add_section', id });
    setRevealSectionId(id);
  }, [dispatch]);

  const handleConsumeReveal = useCallback(() => {
    setRevealSectionId(null);
  }, []);

  const handleAddToSection = useCallback(
    (monitorIds: string[], targetSectionId: string) => {
      if (monitorIds.length === 0) return;
      dispatch({ type: 'add_monitor_to_section', sectionId: targetSectionId, monitorIds });
      dispatch({ type: 'clear_selection' });
      setRevealSectionId(targetSectionId);
    },
    [dispatch],
  );

  const handleAddNewSectionFromLibrary = useCallback(
    (monitorIds: string[], title: string) => {
      handleAddNewSectionWith(title, monitorIds);
    },
    [handleAddNewSectionWith],
  );

  const handleDragStartMonitor = useCallback((event: React.DragEvent, monitorId: string) => {
    event.dataTransfer.effectAllowed = 'move';
    event.dataTransfer.setData(MONITOR_DRAG_MIME, JSON.stringify({ monitorId }));
    event.dataTransfer.setData('text/plain', monitorId);
  }, []);

  const handleDropMonitorOnSection = useCallback(
    (monitorId: string, sectionId: string) => {
      dispatch({ type: 'add_monitor_to_section', sectionId, monitorIds: [monitorId] });
      setRevealSectionId(sectionId);
    },
    [dispatch],
  );

  const handleSubmit = async (event: React.FormEvent) => {
    event.preventDefault();
    setErrors({});

    const nextErrors: Record<string, string> = {};
    const basics = state.basics;
    if (!basics.slug.trim()) {
      nextErrors.slug = 'Slug is required';
    } else if (!/^[a-z0-9-]+$/.test(basics.slug)) {
      nextErrors.slug = 'Only lowercase letters, numbers, and hyphens allowed';
    }
    if (!basics.title.trim()) {
      nextErrors.title = 'Title is required';
    }
    state.sections.forEach((section, index) => {
      if (!section.title.trim()) {
        nextErrors[`section-${section.id}`] = `Section ${index + 1} needs a title`;
      }
    });

    if (Object.keys(nextErrors).length > 0) {
      setErrors(nextErrors);
      if (nextErrors.slug || nextErrors.title) setBasicsOpen(true);
      return;
    }

    const normalizedSections: StatusPageSection[] = state.sections.map((section, index) => ({
      title: section.title.trim(),
      position: index,
      monitors: section.monitors.map((monitor, monitorIndex) => ({
        monitor_id: monitor.monitor_id,
        position: monitorIndex,
        display_name: monitor.display_name.trim() || undefined,
      })),
    }));

    const orderedMonitorIds: string[] = [];
    const seen = new Set<string>();
    state.sections.forEach((section) => {
      section.monitors.forEach((monitor) => {
        if (!seen.has(monitor.monitor_id)) {
          seen.add(monitor.monitor_id);
          orderedMonitorIds.push(monitor.monitor_id);
        }
      });
    });

    const monitorDisplayNames: Record<string, string> = {};
    state.sections.forEach((section) => {
      section.monitors.forEach((monitor) => {
        const trimmed = monitor.display_name.trim();
        if (trimmed) monitorDisplayNames[monitor.monitor_id] = trimmed;
      });
    });

    const requestData: CreateStatusPageRequest | UpdateStatusPageRequest = {
      slug: basics.slug.trim(),
      title: basics.title.trim(),
      description: basics.description.trim(),
      logo_url: basics.logo_url.trim(),
      primary_color: basics.primary_color.trim() || DEFAULT_PRIMARY,
      secondary_color: basics.secondary_color.trim() || DEFAULT_SECONDARY,
      monitor_ids: orderedMonitorIds,
      monitor_display_names: monitorDisplayNames,
      sections: normalizedSections,
    };

    await onSubmit(requestData);
  };

  const summaryParts = useMemo(() => {
    const parts = [];
    if (state.basics.slug) parts.push(`/${state.basics.slug}`);
    if (state.basics.title) parts.push(state.basics.title);
    return parts;
  }, [state.basics.slug, state.basics.title]);

  return (
    <form onSubmit={handleSubmit} className="space-y-5">
      <CollapsibleSection
        title="Page details & branding"
        summary={summaryParts.join(' · ')}
        isOpen={basicsOpen}
        onToggle={setBasicsOpen}
      >
        <div className="grid gap-4 md:grid-cols-2">
          <FormField
            label="URL slug"
            required
            error={errors.slug}
            description="Lowercase letters, numbers, and hyphens only"
          >
            <input
              type="text"
              value={state.basics.slug}
              onChange={(event) => setBasic('slug', event.target.value.toLowerCase())}
              placeholder="my-status-page"
              className="input"
            />
          </FormField>
          <FormField label="Title" required error={errors.title}>
            <input
              type="text"
              value={state.basics.title}
              onChange={(event) => setBasic('title', event.target.value)}
              placeholder="My service status"
              className="input"
            />
          </FormField>
        </div>

        <FormField label="Description">
          <textarea
            value={state.basics.description}
            onChange={(event) => setBasic('description', event.target.value)}
            placeholder="Describe your service status page…"
            rows={2}
            className="input min-h-[72px] resize-none"
          />
        </FormField>

        <div className="grid gap-4 md:grid-cols-2">
          <FormField label="Logo URL">
            <input
              type="url"
              value={state.basics.logo_url}
              onChange={(event) => setBasic('logo_url', event.target.value)}
              placeholder="https://example.com/logo.png"
              className="input"
            />
          </FormField>
          <div className="grid grid-cols-2 gap-3">
            <ColorField
              label="Primary"
              value={state.basics.primary_color}
              onChange={(value) => setBasic('primary_color', value)}
            />
            <ColorField
              label="Secondary"
              value={state.basics.secondary_color}
              onChange={(value) => setBasic('secondary_color', value)}
            />
          </div>
        </div>
      </CollapsibleSection>

      <div className="grid grid-cols-[minmax(0,1fr)] gap-4 lg:grid-cols-[minmax(0,360px)_minmax(0,1fr)] xl:grid-cols-[minmax(0,420px)_minmax(0,1fr)]">
        <div className="h-[440px] lg:sticky lg:top-4 lg:h-[calc(100vh-6rem)] lg:max-h-[900px] lg:self-start">
          <MonitorLibrary
            monitors={monitors}
            loading={monitorsLoading}
            total={monitorsTotal}
            filters={state.filters}
            filteredMonitors={filteredMonitors}
            selection={state.selection}
            derived={derived}
            onFilterChange={(key, value) => dispatch({ type: 'set_filter', key, value })}
            onToggleSelect={(monitorId) => dispatch({ type: 'toggle_selection', monitorId })}
            onClearSelection={() => dispatch({ type: 'clear_selection' })}
            onSelectAllFiltered={() =>
              dispatch({ type: 'set_selection', ids: filteredMonitors.map((m) => m.id) })
            }
            onRevealSection={(sectionId) => setRevealSectionId(sectionId)}
            onDragStartMonitor={handleDragStartMonitor}
            bulkAction={
              <AddToMenu
                label="Add to ▾"
                disabled={state.selection.length === 0}
                sections={state.sections}
                onPickSection={(sectionId) => handleAddToSection(state.selection, sectionId)}
                onCreateSection={(title) => handleAddNewSectionFromLibrary(state.selection, title)}
              />
            }
            rowAction={(monitor, { placement }) => (
              <AddToMenu
                label={placement ? 'Move ▾' : 'Add ▾'}
                sections={state.sections}
                currentSectionId={placement?.sectionId}
                onPickSection={(sectionId) => handleAddToSection([monitor.id], sectionId)}
                onCreateSection={(title) => handleAddNewSectionFromLibrary([monitor.id], title)}
                compact
              />
            )}
          />
        </div>

        <SectionsEditor
          sections={state.sections}
          monitors={monitors}
          derived={derived}
          errors={errors}
          revealSectionId={revealSectionId}
          onConsumeReveal={handleConsumeReveal}
          onAddSection={handleAddEmptySection}
          onRenameSection={(sectionId, title) =>
            dispatch({ type: 'rename_section', sectionId, title })
          }
          onRemoveSection={(sectionId) => dispatch({ type: 'remove_section', sectionId })}
          onReorderSection={(sectionId, direction) =>
            dispatch({ type: 'reorder_section', sectionId, direction })
          }
          onReorderMonitor={(sectionId, monitorId, direction) =>
            dispatch({ type: 'reorder_monitor', sectionId, monitorId, direction })
          }
          onRemoveMonitor={(monitorId) => dispatch({ type: 'remove_monitor', monitorId })}
          onMoveMonitor={(monitorId, targetSectionId) =>
            dispatch({ type: 'move_monitor', monitorId, targetSectionId })
          }
          onAddNewSection={handleAddNewSectionWith}
          onSetDisplayName={(sectionId, monitorId, value) =>
            dispatch({ type: 'set_display_name', sectionId, monitorId, value })
          }
          onAddMonitorsToSection={(sectionId, monitorIds) =>
            dispatch({ type: 'add_monitor_to_section', sectionId, monitorIds })
          }
          onDragStartMonitor={handleDragStartMonitor}
          onDropMonitorOnSection={handleDropMonitorOnSection}
        />
      </div>

      <FormActions
        cancel={onCancel ? { label: 'Cancel', onClick: onCancel, disabled: loading } : undefined}
        submit={{
          label: loading ? 'Saving…' : statusPage ? 'Save changes' : 'Create status page',
          loading,
          disabled: loading,
          type: 'submit',
        }}
        middle={
          state.sections.length === 0
            ? 'Add at least one section to publish.'
            : `${state.sections.length} section${state.sections.length === 1 ? '' : 's'} · ${derived.membershipCount} monitor${derived.membershipCount === 1 ? '' : 's'} assigned`
        }
      />
    </form>
  );
}

interface ColorFieldProps {
  label: string;
  value: string;
  onChange: (value: string) => void;
}

function ColorField({ label, value, onChange }: ColorFieldProps) {
  const safeValue = /^#([0-9a-f]{3}|[0-9a-f]{6})$/i.test(value) ? value : '#000000';
  return (
    <FormField label={label}>
      <div className="flex items-center gap-2 rounded-md border border-white/[0.06] bg-slate-900/60 p-1 pl-1.5 transition-colors focus-within:border-cyan-500/40 focus-within:shadow-[0_0_0_3px_rgba(6,182,212,0.12)]">
        <label
          className="relative h-7 w-7 flex-shrink-0 cursor-pointer overflow-hidden rounded border border-white/[0.06]"
          style={{ backgroundColor: safeValue }}
          aria-label={`${label} color picker`}
          title={`Pick ${label.toLowerCase()} color`}
        >
          <input
            type="color"
            value={safeValue}
            onChange={(event) => onChange(event.target.value)}
            className="absolute inset-0 h-full w-full cursor-pointer opacity-0"
            aria-label={`${label} color picker input`}
          />
        </label>
        <input
          type="text"
          value={value}
          onChange={(event) => onChange(event.target.value)}
          className="w-full bg-transparent font-mono text-xs uppercase tracking-wider text-slate-200 placeholder:text-slate-600 focus:outline-none"
          placeholder="#06b6d4"
          spellCheck={false}
        />
      </div>
    </FormField>
  );
}
