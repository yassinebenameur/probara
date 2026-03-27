'use client';

import { useEffect, useMemo, useState } from 'react';

import { getStatusPages } from '@/lib/api';
import type { IncidentDetail, StatusPage } from '@/lib/types';
import Panel from '@/components/ui/Panel';

interface IncidentPublicationEditorProps {
  incident: IncidentDetail;
  onPublish: (statusPageId: string, monitorIds: string[]) => Promise<void>;
  onUnpublish: (statusPageId: string) => Promise<void>;
}

export default function IncidentPublicationEditor({
  incident,
  onPublish,
  onUnpublish,
}: IncidentPublicationEditorProps) {
  const [statusPages, setStatusPages] = useState<StatusPage[]>([]);
  const [selectedStatusPageId, setSelectedStatusPageId] = useState('');
  const [selectedMonitorIds, setSelectedMonitorIds] = useState<string[]>([]);
  const [loading, setLoading] = useState(true);
  const [saving, setSaving] = useState(false);
  const [error, setError] = useState('');

  useEffect(() => {
    const loadStatusPages = async () => {
      try {
        const response = await getStatusPages({ page_size: 100 });
        setStatusPages(response.items || []);
        setSelectedStatusPageId((current) => current || response.items?.[0]?.id || '');
        setError('');
      } catch (err) {
        setError(err instanceof Error ? err.message : 'Failed to load status pages');
      } finally {
        setLoading(false);
      }
    };

    loadStatusPages();
  }, []);

  const selectedStatusPage = useMemo(
    () => statusPages.find((statusPage) => statusPage.id === selectedStatusPageId) || null,
    [selectedStatusPageId, statusPages]
  );

  const availableComponentOptions = useMemo(() => {
    const pageMonitorIDs = new Set(selectedStatusPage?.monitor_ids || []);
    return incident.monitors.filter((monitor) => pageMonitorIDs.has(monitor.id));
  }, [incident.monitors, selectedStatusPage]);

  useEffect(() => {
    const existingPublication = incident.publications.find(
      (publication) => publication.status_page_id === selectedStatusPageId
    );
    if (existingPublication) {
      setSelectedMonitorIds(existingPublication.monitor_ids);
      return;
    }
    setSelectedMonitorIds([]);
  }, [incident.publications, selectedStatusPageId]);

  const handlePublish = async () => {
    if (!selectedStatusPageId) {
      return;
    }
    setSaving(true);
    await onPublish(selectedStatusPageId, selectedMonitorIds);
    setSaving(false);
  };

  const handleUnpublish = async () => {
    if (!selectedStatusPageId) {
      return;
    }
    setSaving(true);
    await onUnpublish(selectedStatusPageId);
    setSaving(false);
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

  return (
    <Panel
      title="Status Page Publication"
      subtitle="Publish or remove the incident from a public status page."
      dotColor="var(--info)"
    >
      <div className="space-y-4">
        {loading ? (
          <div className="text-sm text-slate-500">Loading status pages...</div>
        ) : statusPages.length === 0 ? (
          <div className="rounded-lg border border-dashed border-white/[0.08] px-4 py-6 text-sm text-slate-500">
            No status pages are available for publication yet.
          </div>
        ) : (
          <>
            {incident.publications.length > 0 ? (
              <div className="space-y-2">
                {incident.publications.map((publication) => (
                  <div
                    key={publication.status_page_id}
                    className="rounded-lg border border-white/[0.06] bg-slate-950/40 px-4 py-3"
                  >
                    <div className="flex items-start justify-between gap-3">
                      <div>
                        <div className="text-sm font-medium text-white">
                          {publication.status_page_title}
                        </div>
                        <div className="mt-1 text-xs text-slate-500">
                          /{publication.status_page_slug} · {publicationComponentNames(publication.monitor_ids)}
                        </div>
                      </div>
                      <button
                        type="button"
                        onClick={() => onUnpublish(publication.status_page_id)}
                        className="btn btn-secondary btn-xs"
                        disabled={saving}
                      >
                        Unpublish
                      </button>
                    </div>
                  </div>
                ))}
              </div>
            ) : null}

            <div>
              <label className="mb-1.5 block text-xs font-medium text-slate-400">
                Status page
              </label>
              <select
                className="input"
                value={selectedStatusPageId}
                onChange={(event) => setSelectedStatusPageId(event.target.value)}
                disabled={saving}
              >
                {statusPages.map((statusPage) => (
                  <option key={statusPage.id} value={statusPage.id}>
                    {statusPage.title} ({statusPage.slug})
                  </option>
                ))}
              </select>
            </div>

            <div className="rounded-lg border border-white/[0.06] bg-slate-950/40 px-4 py-4">
              <div className="text-sm font-medium text-white">Affected components</div>
              <div className="mt-1 text-xs text-slate-500">
                Leave all unchecked to publish the incident platform-wide.
              </div>
              {availableComponentOptions.length === 0 ? (
                <div className="mt-3 text-sm text-slate-500">
                  No linked incident monitors are present on the selected status page.
                </div>
              ) : (
                <div className="mt-3 space-y-2">
                  {availableComponentOptions.map((monitor) => (
                    <label key={monitor.id} className="flex items-center gap-3 text-sm text-slate-300">
                      <input
                        type="checkbox"
                        checked={selectedMonitorIds.includes(monitor.id)}
                        onChange={() => toggleMonitorSelection(monitor.id)}
                        disabled={saving}
                      />
                      <span>{monitor.name}</span>
                      <span className="text-xs uppercase tracking-wide text-slate-500">{monitor.type}</span>
                    </label>
                  ))}
                </div>
              )}
            </div>

            <div className="flex flex-wrap gap-3">
              <button
                type="button"
                onClick={handlePublish}
                className="btn btn-primary btn-sm"
                disabled={saving || !selectedStatusPageId}
              >
                {saving ? 'Saving...' : 'Publish Incident'}
              </button>
              <button
                type="button"
                onClick={handleUnpublish}
                className="btn btn-secondary btn-sm"
                disabled={saving || !selectedStatusPageId}
              >
                {saving ? 'Saving...' : 'Unpublish Incident'}
              </button>
            </div>
          </>
        )}

        {error ? (
          <div className="rounded-lg border border-rose-500/20 bg-rose-500/10 px-4 py-3 text-sm text-rose-300">
            {error}
          </div>
        ) : null}
      </div>
    </Panel>
  );
}
