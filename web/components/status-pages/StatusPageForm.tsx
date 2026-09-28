'use client';

import { useCallback, useEffect, useMemo, useRef, useState } from 'react';
import { CircleCheck, CircleDot } from 'lucide-react';
import type {
  CreateStatusPageRequest,
  Monitor,
  StatusPage,
  StatusPageSection,
  UpdateStatusPageRequest,
} from '@/lib/types';
import { getMonitors } from '@/lib/api';
import { resolveStatusPagePublicUrl } from '@/lib/statusPageUrl';
import FormField from '@/components/ui/FormField';
import Button from '@/components/ui/Button';
import SectionList from './SectionList';
import PagePreview from './PagePreview';
import {
  DEFAULT_PRIMARY,
  DEFAULT_SECONDARY,
  initialState,
  newSectionId,
  useDerived,
  useStatusPageFormState,
  type Basics,
  type FormState,
} from './useStatusPageFormState';

interface StatusPageFormProps {
  statusPage?: StatusPage;
  onSubmit: (data: CreateStatusPageRequest | UpdateStatusPageRequest) => Promise<void>;
  onCancel?: () => void;
  loading?: boolean;
}

const MONITOR_PAGE_SIZE = 100;
const SLUG_RE = /^[a-z0-9-]+$/;

function slugify(value: string): string {
  return value
    .toLowerCase()
    .replace(/[^a-z0-9]+/g, '-')
    .replace(/^-+|-+$/g, '')
    .slice(0, 64);
}

/** Serialized form of everything the submit payload carries — used for dirty tracking. */
function snapshot(state: FormState): string {
  return JSON.stringify({
    basics: {
      slug: state.basics.slug.trim(),
      title: state.basics.title.trim(),
      description: state.basics.description.trim(),
      logo_url: state.basics.logo_url.trim(),
      primary_color: state.basics.primary_color.trim(),
      secondary_color: state.basics.secondary_color.trim(),
    },
    enablePushNotifications: state.enablePushNotifications,
    sections: state.sections.map((section) => ({
      title: section.title.trim(),
      monitors: section.monitors.map((m) => ({
        id: m.monitor_id,
        name: m.display_name.trim(),
      })),
    })),
  });
}

export default function StatusPageForm({
  statusPage,
  onSubmit,
  onCancel,
  loading = false,
}: StatusPageFormProps) {
  const { state, dispatch } = useStatusPageFormState(statusPage);
  const [monitors, setMonitors] = useState<Monitor[]>([]);
  const [monitorsLoading, setMonitorsLoading] = useState(true);
  const [errors, setErrors] = useState<Record<string, string>>({});
  const formRef = useRef<HTMLFormElement>(null);
  // Once the user edits the slug by hand, stop deriving it from the title.
  const slugTouchedRef = useRef(!!statusPage);

  const derived = useDerived(state);

  const initialSnapshot = useMemo(() => snapshot(initialState(statusPage)), [statusPage]);
  const isDirty = snapshot(state) !== initialSnapshot;

  useEffect(() => {
    dispatch({ type: 'reset', statusPage });
    slugTouchedRef.current = !!statusPage;
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
        if (!cancelled) setMonitors(all);
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

  // ⌘S / Ctrl+S saves the page.
  useEffect(() => {
    const handleKey = (event: KeyboardEvent) => {
      if ((event.metaKey || event.ctrlKey) && event.key.toLowerCase() === 's') {
        event.preventDefault();
        formRef.current?.requestSubmit();
      }
    };
    window.addEventListener('keydown', handleKey);
    return () => window.removeEventListener('keydown', handleKey);
  }, []);

  const setBasic = useCallback(
    (key: keyof Basics, value: string) => {
      dispatch({ type: 'set_basic', key, value });
    },
    [dispatch],
  );

  const handleTitleChange = useCallback(
    (value: string) => {
      setBasic('title', value);
      if (!slugTouchedRef.current) setBasic('slug', slugify(value));
    },
    [setBasic],
  );

  const handleSlugChange = useCallback(
    (value: string) => {
      slugTouchedRef.current = value.trim() !== '';
      setBasic('slug', value.toLowerCase());
    },
    [setBasic],
  );

  const handleAddSection = useCallback(() => {
    dispatch({ type: 'add_section', id: newSectionId(), title: '' });
  }, [dispatch]);

  const liveSlugError =
    state.basics.slug.trim() && !SLUG_RE.test(state.basics.slug.trim())
      ? 'Only lowercase letters, numbers, and hyphens allowed'
      : undefined;

  const handleSubmit = async (event: React.FormEvent) => {
    event.preventDefault();
    setErrors({});

    const nextErrors: Record<string, string> = {};
    const basics = state.basics;
    if (!basics.slug.trim()) {
      nextErrors.slug = 'Slug is required';
    } else if (!SLUG_RE.test(basics.slug.trim())) {
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
      if (nextErrors.slug || nextErrors.title) {
        formRef.current?.scrollIntoView({ behavior: 'smooth', block: 'start' });
      }
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
      settings: { enable_push_notifications: state.enablePushNotifications },
    };

    await onSubmit(requestData);
  };

  const publicUrl = resolveStatusPagePublicUrl({
    slug: state.basics.slug.trim() || 'your-page',
    public_url: statusPage?.public_url,
  });

  const summary =
    state.sections.length === 0
      ? 'Add at least one section to publish.'
      : `${state.sections.length} section${state.sections.length === 1 ? '' : 's'} · ${derived.membershipCount} monitor${derived.membershipCount === 1 ? '' : 's'}`;

  return (
    <form ref={formRef} onSubmit={handleSubmit} className="space-y-6">
      <section className="glass p-6">
        <h3 className="mb-5 text-[15px] font-semibold text-white">Identity & branding</h3>
        <div className="grid grid-cols-1 gap-6 lg:grid-cols-[minmax(0,1fr)_minmax(0,360px)]">
          <div className="space-y-4">
            <div className="grid gap-4 sm:grid-cols-2">
              <FormField label="Title" required error={errors.title}>
                <input
                  type="text"
                  value={state.basics.title}
                  onChange={(event) => handleTitleChange(event.target.value)}
                  placeholder="My service status"
                  className="input"
                  autoFocus={!statusPage}
                />
              </FormField>
              <FormField label="URL slug" required error={errors.slug || liveSlugError}>
                <input
                  type="text"
                  value={state.basics.slug}
                  onChange={(event) => handleSlugChange(event.target.value)}
                  placeholder="my-status-page"
                  className="input font-mono"
                  spellCheck={false}
                />
                <p className="mt-1 text-xs text-slate-500">
                  {!statusPage && !slugTouchedRef.current
                    ? 'Generated from the title — edit to customize'
                    : 'Lowercase letters, numbers, and hyphens'}
                </p>
              </FormField>
            </div>

            <FormField label="Description">
              <textarea
                value={state.basics.description}
                onChange={(event) => setBasic('description', event.target.value)}
                placeholder="Shown under the page title — e.g. “Live status for all Acme services.”"
                rows={2}
                className="input min-h-[72px] resize-none"
              />
            </FormField>

            <FormField label="Logo URL">
              <input
                type="url"
                value={state.basics.logo_url}
                onChange={(event) => setBasic('logo_url', event.target.value)}
                placeholder="https://example.com/logo.png"
                className="input"
              />
              <p className="mt-1 text-xs text-slate-500">
                Square image works best; leave empty for a brand-colored mark
              </p>
            </FormField>

            <div className="grid grid-cols-2 gap-4">
              <ColorField
                label="Primary color"
                value={state.basics.primary_color}
                onChange={(value) => setBasic('primary_color', value)}
              />
              <ColorField
                label="Secondary color"
                value={state.basics.secondary_color}
                onChange={(value) => setBasic('secondary_color', value)}
              />
            </div>

            <label className="glass-well flex cursor-pointer items-start gap-3 p-4">
              <input
                type="checkbox"
                className="mt-0.5 h-4 w-4 flex-shrink-0 accent-cyan-500"
                checked={state.enablePushNotifications}
                onChange={(event) =>
                  dispatch({ type: 'set_push_notifications', value: event.target.checked })
                }
              />
              <span className="min-w-0">
                <span className="block text-sm font-medium text-white">
                  Browser notifications
                </span>
                <span className="mt-0.5 block text-xs leading-relaxed text-slate-400">
                  Let visitors opt in to a browser notification when a component here goes
                  down, and when it recovers. Requires a VAPID keypair on the status-page
                  service; without one the control is not shown.
                </span>
              </span>
            </label>
          </div>

          <PagePreview
            basics={state.basics}
            sections={state.sections}
            monitors={monitors}
            publicUrl={publicUrl}
          />
        </div>
      </section>

      <SectionList
        sections={state.sections}
        monitors={monitors}
        monitorsLoading={monitorsLoading}
        derived={derived}
        errors={errors}
        dispatch={dispatch}
        onAddSection={handleAddSection}
      />

      <div className="glass-overlay sticky bottom-4 z-20 flex flex-wrap items-center gap-3 !rounded-2xl px-4 py-3">
        <div className="mr-auto flex min-w-0 items-center gap-2 text-xs">
          {isDirty ? (
            <span className="flex items-center gap-1.5 text-amber-300">
              <CircleDot className="h-3.5 w-3.5" strokeWidth={2} aria-hidden="true" />
              Unsaved changes
            </span>
          ) : statusPage ? (
            <span className="flex items-center gap-1.5 text-emerald-300">
              <CircleCheck className="h-3.5 w-3.5" strokeWidth={2} aria-hidden="true" />
              All changes saved
            </span>
          ) : null}
          <span className="truncate text-slate-500">
            {(isDirty || statusPage) && <span aria-hidden="true">· </span>}
            {summary}
          </span>
        </div>
        <div className="ml-auto flex items-center gap-2">
          {onCancel && (
            <Button variant="ghost" size="sm" type="button" onClick={onCancel} disabled={loading}>
              Cancel
            </Button>
          )}
          <Button
            variant="accent"
            size="sm"
            type="submit"
            loading={loading}
            disabled={loading || (!!statusPage && !isDirty)}
            title="⌘S"
          >
            {statusPage ? 'Save changes' : 'Create status page'}
          </Button>
        </div>
      </div>
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
      <div className="flex items-center gap-2.5 rounded-lg border border-edge-control bg-control p-[5px] pr-3 transition-colors hover:border-[var(--control-edge-strong)] focus-within:border-cyan-500 focus-within:bg-control-hover focus-within:shadow-[0_0_0_3px_rgba(var(--accent-rgb),0.16)]">
        <label
          className="relative h-7 w-7 flex-shrink-0 cursor-pointer overflow-hidden rounded-md border border-edge-control"
          style={{ backgroundColor: safeValue }}
          aria-label={`${label} picker`}
          title={`Pick ${label.toLowerCase()}`}
        >
          <input
            type="color"
            value={safeValue}
            onChange={(event) => onChange(event.target.value)}
            className="absolute inset-0 h-full w-full cursor-pointer opacity-0"
            aria-label={`${label} picker input`}
          />
        </label>
        <input
          type="text"
          value={value}
          onChange={(event) => onChange(event.target.value)}
          className="w-full bg-transparent font-mono text-sm text-slate-200 placeholder:text-slate-500 focus:outline-none"
          placeholder="#ff5a24"
          spellCheck={false}
        />
      </div>
    </FormField>
  );
}
