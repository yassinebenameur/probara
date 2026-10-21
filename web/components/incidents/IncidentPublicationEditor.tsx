'use client';

import { useEffect, useMemo, useState } from 'react';
import { ArrowUpRight, EyeOff, Globe } from 'lucide-react';

import { getStatusPages } from '@/lib/api';
import type { IncidentDetail, StatusPage } from '@/lib/types';
import { resolveStatusPagePublicUrl } from '@/lib/statusPageUrl';
import { sameIdSet } from '@/lib/incident-view';
import Panel from '@/components/ui/Panel';
import Button from '@/components/ui/Button';
import Callout from '@/components/ui/Callout';
import Checkbox from '@/components/ui/Checkbox';
import IconButton from '@/components/ui/IconButton';
import Select from '@/components/ui/Select';
import TypeBadge from '@/components/ui/TypeBadge';

interface IncidentPublicationEditorProps {
  incident: IncidentDetail;
  /** Write affordances; viewers see where the incident is published, nothing else. */
  canWrite: boolean;
  onPublish: (statusPageId: string, monitorIds: string[]) => Promise<void>;
  onUnpublish: (statusPageId: string) => Promise<void>;
}

export default function IncidentPublicationEditor({
  incident,
  canWrite,
  onPublish,
  onUnpublish,
}: IncidentPublicationEditorProps) {
  const [statusPages, setStatusPages] = useState<StatusPage[]>([]);
  const [selectedStatusPageId, setSelectedStatusPageId] = useState('');
  const [selectedMonitorIds, setSelectedMonitorIds] = useState<string[]>([]);
  const [loading, setLoading] = useState(true);
  // Which action is in flight: the form's publish, or an unpublish by page id.
  const [saving, setSaving] = useState<string | null>(null);
  const [error, setError] = useState('');

  useEffect(() => {
    const loadStatusPages = async () => {
      try {
        const response = await getStatusPages({ page_size: 100 });
        const items = response.items || [];
        setStatusPages(items);
        // Start on the first page the incident is not on yet: the form's job
        // is usually to publish somewhere new.
        const published = new Set(incident.publications.map((p) => p.status_page_id));
        setSelectedStatusPageId(
          (current) => current || items.find((p) => !published.has(p.id))?.id || items[0]?.id || '',
        );
        setError('');
      } catch (err) {
        setError(err instanceof Error ? err.message : 'Failed to load status pages');
      } finally {
        setLoading(false);
      }
    };

    loadStatusPages();
    // Only the initial selection reads the publications.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);

  const selectedStatusPage = useMemo(
    () => statusPages.find((statusPage) => statusPage.id === selectedStatusPageId) || null,
    [selectedStatusPageId, statusPages]
  );

  const availableComponentOptions = useMemo(() => {
    const pageMonitorIDs = new Set(selectedStatusPage?.monitor_ids || []);
    return incident.monitors.filter((monitor) => pageMonitorIDs.has(monitor.id));
  }, [incident.monitors, selectedStatusPage]);

  const existingPublication = incident.publications.find(
    (publication) => publication.status_page_id === selectedStatusPageId
  );

  useEffect(() => {
    const publication = incident.publications.find((p) => p.status_page_id === selectedStatusPageId);
    setSelectedMonitorIds(publication ? publication.monitor_ids : []);
  }, [incident.publications, selectedStatusPageId]);

  const run = async (key: string, action: () => Promise<void>) => {
    setSaving(key);
    setError('');
    try {
      await action();
    } catch (err) {
      setError(err instanceof Error ? err.message : 'Failed to update the publication');
    } finally {
      setSaving(null);
    }
  };

  const handlePublish = () => {
    if (!selectedStatusPageId) return;
    void run('publish', () => onPublish(selectedStatusPageId, selectedMonitorIds));
  };

  const publicationComponentNames = (monitorIds: string[]): string => {
    if (monitorIds.length === 0) {
      return 'Platform-wide';
    }
    const nameByID = new Map(incident.monitors.map((monitor) => [monitor.id, monitor.name]));
    return monitorIds.map((monitorId) => nameByID.get(monitorId) || monitorId).join(', ');
  };

  const toggleMonitorSelection = (monitorId: string) => {
    setSelectedMonitorIds((current) => (
      current.includes(monitorId)
        ? current.filter((id) => id !== monitorId)
        : [...current, monitorId]
    ));
  };

  const publishedIds = new Set(incident.publications.map((p) => p.status_page_id));
  const unchanged = !!existingPublication && sameIdSet(existingPublication.monitor_ids, selectedMonitorIds);
  const busy = saving !== null;
  const publishedCount = incident.publications.length;

  return (
    <Panel
      title="Status page publication"
      subtitle={
        publishedCount > 0
          ? `Public on ${publishedCount} status page${publishedCount === 1 ? '' : 's'}`
          : 'Not public yet: publish it to show it on a status page'
      }
    >
      <div className="space-y-4">
        {publishedCount > 0 && (
          <ul className="glass-well divide-y divide-divider">
            {incident.publications.map((publication) => {
              const href = resolveStatusPagePublicUrl({ slug: publication.status_page_slug });
              return (
                <li key={publication.status_page_id} className="flex items-center gap-3 py-2.5 pl-3.5 pr-1.5">
                  <Globe className="h-4 w-4 flex-shrink-0 text-emerald-400" strokeWidth={1.75} aria-hidden="true" />
                  <div className="min-w-0 flex-1">
                    <div className="truncate text-ui font-medium text-white">{publication.status_page_title}</div>
                    <div className="mt-0.5 truncate text-xs text-slate-500">
                      {publicationComponentNames(publication.monitor_ids)}
                    </div>
                  </div>
                  <div className="flex flex-shrink-0 items-center gap-0.5">
                    <IconButton
                      size="sm"
                      href={href}
                      external
                      icon={<ArrowUpRight strokeWidth={1.75} />}
                      label={`Open ${publication.status_page_title} in a new tab`}
                    />
                    {canWrite && (
                      <IconButton
                        size="sm"
                        danger
                        icon={<EyeOff strokeWidth={1.75} />}
                        label={`Unpublish from ${publication.status_page_title}`}
                        onClick={() => run(publication.status_page_id, () => onUnpublish(publication.status_page_id))}
                        disabled={busy}
                        loading={saving === publication.status_page_id}
                      />
                    )}
                  </div>
                </li>
              );
            })}
          </ul>
        )}

        {!canWrite ? null : loading ? (
          <div className="text-sm text-slate-500">Loading status pages…</div>
        ) : statusPages.length === 0 ? (
          <div className="text-sm text-slate-400">
            No status pages exist yet. Create one under Status pages to publish incidents.
          </div>
        ) : (
          <>
            <Select
              label={publishedCount > 0 ? 'Publish to another page, or edit one' : 'Status page'}
              value={selectedStatusPageId}
              onChange={(event) => setSelectedStatusPageId(event.target.value)}
              disabled={busy}
              className="h-9 py-0 text-ui"
            >
              {statusPages.map((statusPage) => (
                <option key={statusPage.id} value={statusPage.id}>
                  {statusPage.title}{publishedIds.has(statusPage.id) ? ' · published' : ''}
                </option>
              ))}
            </Select>

            <fieldset aria-describedby="incident-components-hint">
              <legend className="text-ui font-medium text-slate-300">Affected components</legend>
              <div id="incident-components-hint" className="mt-0.5 text-xs text-slate-500">
                {availableComponentOptions.length === 0
                  ? 'None of this incident’s monitors are on this page, so it publishes platform-wide.'
                  : 'Leave all unchecked to publish platform-wide.'}
              </div>
              {availableComponentOptions.length > 0 && (
                <div className="mt-2 flex flex-col items-start">
                  {availableComponentOptions.map((monitor) => (
                    <Checkbox
                      key={monitor.id}
                      checked={selectedMonitorIds.includes(monitor.id)}
                      onChange={() => toggleMonitorSelection(monitor.id)}
                      disabled={busy}
                      label={
                        <span className="flex items-center gap-2.5 text-slate-300">
                          <TypeBadge type={monitor.type} />
                          <span>{monitor.name}</span>
                        </span>
                      }
                    />
                  ))}
                </div>
              )}
            </fieldset>

            <div className="flex flex-wrap items-center gap-2">
              <Button
                type="button"
                variant="ghost"
                size="sm"
                onClick={handlePublish}
                disabled={busy || !selectedStatusPageId || unchanged}
                loading={saving === 'publish'}
                title={unchanged ? 'Already published with these components' : undefined}
              >
                {existingPublication ? 'Update publication' : 'Publish to page'}
              </Button>
              {existingPublication && (
                <span className="text-xs text-emerald-300">Published here</span>
              )}
            </div>
          </>
        )}

        {error ? <Callout tone="danger">{error}</Callout> : null}
      </div>
    </Panel>
  );
}
