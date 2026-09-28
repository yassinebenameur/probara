'use client';

import { useEffect, useMemo, useState } from 'react';
import { EyeOff } from 'lucide-react';

import { getStatusPages } from '@/lib/api';
import type { IncidentDetail, StatusPage } from '@/lib/types';
import Panel from '@/components/ui/Panel';
import Button from '@/components/ui/Button';
import Callout from '@/components/ui/Callout';
import IconButton from '@/components/ui/IconButton';
import Select from '@/components/ui/Select';
import { monitorTypeLabel } from '@/lib/monitor-type-labels';

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
      title="Status page publication"
      subtitle="Publish or remove the incident from a public status page."
    >
      <div className="space-y-4">
        {loading ? (
          <div className="text-sm text-slate-500">Loading status pages…</div>
        ) : statusPages.length === 0 ? (
          <div className="text-sm text-slate-400">
            No status pages are available for publication yet.
          </div>
        ) : (
          <>
            {incident.publications.length > 0 ? (
              <div className="glass-well divide-y divide-divider">
                {incident.publications.map((publication) => (
                  <div
                    key={publication.status_page_id}
                    className="flex items-center justify-between gap-3 px-4 py-3"
                  >
                    <div className="min-w-0">
                      <div className="text-sm font-medium text-white">
                        {publication.status_page_title}
                      </div>
                      <div className="mt-0.5 text-xs text-slate-500">
                        <span className="font-mono">/{publication.status_page_slug}</span>
                        {' · '}
                        {publicationComponentNames(publication.monitor_ids)}
                      </div>
                    </div>
                    <IconButton
                      icon={<EyeOff strokeWidth={1.75} />}
                      label="Unpublish from this status page"
                      onClick={() => onUnpublish(publication.status_page_id)}
                      disabled={saving}
                    />
                  </div>
                ))}
              </div>
            ) : null}

            <div className="max-w-md">
              <Select
                label="Status page"
                value={selectedStatusPageId}
                onChange={(event) => setSelectedStatusPageId(event.target.value)}
                disabled={saving}
                className="h-9 py-0 text-[13px]"
              >
                {statusPages.map((statusPage) => (
                  <option key={statusPage.id} value={statusPage.id}>
                    {statusPage.title} ({statusPage.slug})
                  </option>
                ))}
              </Select>
            </div>

            <div>
              <div className="text-[13px] font-medium text-slate-300">Affected components</div>
              <div className="mt-0.5 text-xs text-slate-500">
                Leave all unchecked to publish the incident platform-wide.
              </div>
              {availableComponentOptions.length === 0 ? (
                <div className="mt-3 text-sm text-slate-400">
                  No linked incident monitors are present on the selected status page.
                </div>
              ) : (
                <div className="mt-3 space-y-2">
                  {availableComponentOptions.map((monitor) => (
                    <label key={monitor.id} className="flex items-center gap-2.5 text-sm text-slate-300">
                      <input
                        type="checkbox"
                        className="h-4 w-4 accent-cyan-500"
                        checked={selectedMonitorIds.includes(monitor.id)}
                        onChange={() => toggleMonitorSelection(monitor.id)}
                        disabled={saving}
                      />
                      <span>{monitor.name}</span>
                      <span className="text-xs text-slate-500">{monitorTypeLabel(monitor.type)}</span>
                    </label>
                  ))}
                </div>
              )}
            </div>

            <div className="flex flex-wrap gap-2">
              <Button
                type="button"
                variant="ghost"
                size="sm"
                onClick={handlePublish}
                disabled={saving || !selectedStatusPageId}
                loading={saving}
              >
                {saving ? 'Saving…' : 'Publish incident'}
              </Button>
              <Button
                type="button"
                variant="subtle"
                size="sm"
                onClick={handleUnpublish}
                disabled={saving || !selectedStatusPageId}
              >
                {saving ? 'Saving…' : 'Unpublish incident'}
              </Button>
            </div>
          </>
        )}

        {error ? <Callout tone="danger">{error}</Callout> : null}
      </div>
    </Panel>
  );
}
