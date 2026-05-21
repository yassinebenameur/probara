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
import MonitorLibrary from './MonitorLibrary';
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

  // Reset state when editing a different status page
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
          if (page > 100) break; // safety
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
      <BasicsAndBranding
        basics={state.basics}
        open={basicsOpen}
        onToggle={() => setBasicsOpen((value) => !value)}
        summary={summaryParts.join(' · ')}
        errors={errors}
        onChange={setBasic}
      />

      <div className="grid gap-4 lg:grid-cols-[minmax(0,360px)_minmax(0,1fr)] xl:grid-cols-[minmax(0,420px)_minmax(0,1fr)]">
        <div className="lg:sticky lg:top-4 lg:self-start">
          <MonitorLibrary
            monitors={monitors}
            loading={monitorsLoading}
            total={monitorsTotal}
            filters={state.filters}
            filteredMonitors={filteredMonitors}
            selection={state.selection}
            derived={derived}
            sections={state.sections}
            onFilterChange={(key, value) => dispatch({ type: 'set_filter', key, value })}
            onToggleSelect={(monitorId) => dispatch({ type: 'toggle_selection', monitorId })}
            onClearSelection={() => dispatch({ type: 'clear_selection' })}
            onSelectAllFiltered={() =>
              dispatch({ type: 'set_selection', ids: filteredMonitors.map((m) => m.id) })
            }
            onAddToSection={handleAddToSection}
            onAddNewSection={handleAddNewSectionFromLibrary}
            onRevealSection={(sectionId) => setRevealSectionId(sectionId)}
            onDragStartMonitor={handleDragStartMonitor}
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

      <div className="flex flex-wrap items-center justify-between gap-3 border-t border-white/[0.06] pt-4">
        <p className="text-[11px] text-slate-500">
          {state.sections.length === 0
            ? 'Add at least one section to publish.'
            : `${state.sections.length} section${state.sections.length === 1 ? '' : 's'} · ${derived.membershipCount} monitor${derived.membershipCount === 1 ? '' : 's'} assigned`}
        </p>
        <div className="flex items-center gap-2">
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
            {loading ? 'Saving…' : statusPage ? 'Save Changes' : 'Create Status Page'}
          </button>
        </div>
      </div>
    </form>
  );
}

interface BasicsAndBrandingProps {
  basics: Basics;
  open: boolean;
  summary: string;
  errors: Record<string, string>;
  onToggle: () => void;
  onChange: (key: keyof Basics, value: string) => void;
}

function BasicsAndBranding({
  basics,
  open,
  summary,
  errors,
  onToggle,
  onChange,
}: BasicsAndBrandingProps) {
  return (
    <section className="overflow-hidden rounded-xl border border-white/[0.06] bg-slate-900/40">
      <button
        type="button"
        onClick={onToggle}
        className="group flex w-full items-center justify-between gap-3 px-4 py-3 text-left transition-colors hover:bg-white/[0.02]"
        aria-expanded={open}
      >
        <div className="flex min-w-0 items-center gap-3">
          <span
            aria-hidden="true"
            className="flex h-7 w-7 flex-shrink-0 items-center justify-center rounded-md border border-white/[0.08] bg-slate-950/60 text-slate-400 transition-colors group-hover:border-cyan-500/30 group-hover:text-cyan-300"
          >
            <svg viewBox="0 0 20 20" fill="currentColor" className="h-3.5 w-3.5">
              <path d="M3 5.5A2.5 2.5 0 0 1 5.5 3h9A2.5 2.5 0 0 1 17 5.5v2A2.5 2.5 0 0 1 14.5 10h-9A2.5 2.5 0 0 1 3 7.5v-2Zm0 7A2.5 2.5 0 0 1 5.5 10h4A2.5 2.5 0 0 1 12 12.5v2A2.5 2.5 0 0 1 9.5 17h-4A2.5 2.5 0 0 1 3 14.5v-2Zm11-2.5a2 2 0 0 0-2 2v2a2 2 0 0 0 2 2h1a2 2 0 0 0 2-2v-2a2 2 0 0 0-2-2h-1Z" />
            </svg>
          </span>
          <div className="min-w-0">
            <h3 className="text-sm font-medium text-white">Page details &amp; branding</h3>
            {!open && summary && (
              <p className="mt-0.5 truncate text-xs text-slate-500">{summary}</p>
            )}
          </div>
        </div>
        <svg
          viewBox="0 0 20 20"
          fill="currentColor"
          aria-hidden="true"
          className={`h-4 w-4 flex-shrink-0 text-slate-500 transition-transform duration-200 ${
            open ? 'rotate-180' : ''
          }`}
        >
          <path d="M5.23 7.21a.75.75 0 0 1 1.06.02L10 11.06l3.71-3.83a.75.75 0 1 1 1.08 1.04l-4.25 4.4a.75.75 0 0 1-1.08 0L5.21 8.27a.75.75 0 0 1 .02-1.06Z" />
        </svg>
      </button>

      {open && (
        <div className="space-y-4 border-t border-white/[0.06] px-4 py-4">
          <div className="grid gap-4 md:grid-cols-2">
            <div>
              <label className="mb-1.5 block text-[11px] font-medium uppercase tracking-[0.1em] text-slate-400">
                URL slug
              </label>
              <input
                type="text"
                value={basics.slug}
                onChange={(event) => onChange('slug', event.target.value.toLowerCase())}
                placeholder="my-status-page"
                className="input"
              />
              {errors.slug && <p className="mt-1 text-xs text-rose-400">{errors.slug}</p>}
              <p className="mt-1 text-xs text-slate-500">
                Lowercase letters, numbers, and hyphens only.
              </p>
            </div>
            <div>
              <label className="mb-1.5 block text-[11px] font-medium uppercase tracking-[0.1em] text-slate-400">
                Title
              </label>
              <input
                type="text"
                value={basics.title}
                onChange={(event) => onChange('title', event.target.value)}
                placeholder="My Service Status"
                className="input"
              />
              {errors.title && <p className="mt-1 text-xs text-rose-400">{errors.title}</p>}
            </div>
          </div>

          <div>
            <label className="mb-1.5 block text-[11px] font-medium uppercase tracking-[0.1em] text-slate-400">
              Description
            </label>
            <textarea
              value={basics.description}
              onChange={(event) => onChange('description', event.target.value)}
              placeholder="Describe your service status page…"
              rows={2}
              className="input min-h-[72px] resize-none"
            />
          </div>

          <div className="grid gap-4 md:grid-cols-2">
            <div>
              <label className="mb-1.5 block text-[11px] font-medium uppercase tracking-[0.1em] text-slate-400">
                Logo URL
              </label>
              <input
                type="url"
                value={basics.logo_url}
                onChange={(event) => onChange('logo_url', event.target.value)}
                placeholder="https://example.com/logo.png"
                className="input"
              />
            </div>
            <div className="grid grid-cols-2 gap-3">
              <ColorField
                label="Primary"
                value={basics.primary_color}
                onChange={(value) => onChange('primary_color', value)}
              />
              <ColorField
                label="Secondary"
                value={basics.secondary_color}
                onChange={(value) => onChange('secondary_color', value)}
              />
            </div>
          </div>
        </div>
      )}
    </section>
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
    <div>
      <label className="mb-1.5 block text-[11px] font-medium uppercase tracking-[0.1em] text-slate-400">
        {label}
      </label>
      <div className="flex items-center gap-2 rounded-md border border-white/[0.08] bg-slate-950/60 p-1 pl-1.5 transition-colors focus-within:border-cyan-500/40 focus-within:shadow-[0_0_0_3px_rgba(6,182,212,0.12)]">
        <label
          className="relative h-7 w-7 flex-shrink-0 cursor-pointer overflow-hidden rounded border border-white/[0.08]"
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
    </div>
  );
}
