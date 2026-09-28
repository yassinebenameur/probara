'use client';

import { useParams } from 'next/navigation';
import { useCallback, useEffect, useState } from 'react';

import {
  addIncidentTimelineEntry,
  attachIncidentAlert,
  attachIncidentMonitor,
  getAlerts,
  getIncident,
  getMonitors,
  publishIncidentToStatusPage,
  transitionIncidentState,
  detachIncidentAlert,
  detachIncidentMonitor,
  unpublishIncidentFromStatusPage,
} from '@/lib/api';
import type { Alert, IncidentDetail, IncidentState, Monitor } from '@/lib/types';
import AIRootCausePanel from '@/components/incidents/AIRootCausePanel';
import { incidentStateTone } from '@/components/incidents/IncidentList';
import IncidentPublicationEditor from '@/components/incidents/IncidentPublicationEditor';
import IncidentTimeline from '@/components/incidents/IncidentTimeline';
import Panel from '@/components/ui/Panel';
import { useToast } from '@/components/ui/ToastProvider';
import PageHeader from '@/components/ui/PageHeader';
import Button from '@/components/ui/Button';
import Callout from '@/components/ui/Callout';
import IconButton from '@/components/ui/IconButton';
import { Unlink } from 'lucide-react';
import Pill from '@/components/ui/Pill';
import { formatDateTime } from '@/lib/format';
import { monitorTypeLabel } from '@/lib/monitor-type-labels';

const ATTACH_SELECT = 'input h-9 min-w-0 flex-1 py-0 text-[13px]';

export default function IncidentDetailPage() {
  const params = useParams<{ id: string }>();
  const incidentId = Array.isArray(params?.id) ? params.id[0] : params?.id;

  const [incident, setIncident] = useState<IncidentDetail | null>(null);
  const [alerts, setAlerts] = useState<Alert[]>([]);
  const [monitors, setMonitors] = useState<Monitor[]>([]);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState('');
  const [selectedAlertId, setSelectedAlertId] = useState('');
  const [selectedMonitorId, setSelectedMonitorId] = useState('');
  const { showToast } = useToast();

  const loadIncident = useCallback(async () => {
    if (!incidentId) {
      return;
    }

    try {
      setLoading(true);
      setError('');
      const response = await getIncident(incidentId);
      setIncident(response);
    } catch (err) {
      setError(err instanceof Error ? err.message : 'Failed to load incident');
      setIncident(null);
    } finally {
      setLoading(false);
    }
  }, [incidentId]);

  const loadResources = useCallback(async () => {
    try {
      const [alertsResponse, monitorsResponse] = await Promise.all([
        getAlerts({ page_size: 100 }),
        getMonitors({ page_size: 100 }),
      ]);
      setAlerts(alertsResponse.items || []);
      setMonitors(monitorsResponse.items || []);
    } catch {
      setAlerts([]);
      setMonitors([]);
    }
  }, []);

  useEffect(() => {
    loadIncident();
    loadResources();
  }, [loadIncident, loadResources]);

  const handleStateTransition = async (state: IncidentState) => {
    if (!incident) {
      return;
    }
    try {
      const response = await transitionIncidentState(incident.id, state);
      setIncident(response);
      showToast(`Incident moved to ${state}`, 'success');
    } catch (err) {
      showToast(err instanceof Error ? err.message : 'Failed to update incident state', 'error');
    }
  };

  const handleAddInternalNote = async (message: string) => {
    if (!incident) {
      return;
    }
    const response = await addIncidentTimelineEntry(incident.id, {
      entry_type: 'internal_note',
      message,
    });
    setIncident(response);
  };

  const handleAddPublicUpdate = async (message: string) => {
    if (!incident) {
      return;
    }
    const response = await addIncidentTimelineEntry(incident.id, {
      entry_type: 'public_update',
      message,
    });
    setIncident(response);
  };

  const handleAttachAlert = async () => {
    if (!incident || !selectedAlertId) {
      return;
    }
    try {
      const response = await attachIncidentAlert(incident.id, selectedAlertId);
      setIncident(response);
      setSelectedAlertId('');
      showToast('Alert attached to incident', 'success');
    } catch (err) {
      showToast(err instanceof Error ? err.message : 'Failed to attach alert', 'error');
    }
  };

  const handleDetachAlert = async (alertId: string) => {
    if (!incident) {
      return;
    }
    try {
      const response = await detachIncidentAlert(incident.id, alertId);
      setIncident(response);
      showToast('Alert detached from incident', 'success');
    } catch (err) {
      showToast(err instanceof Error ? err.message : 'Failed to detach alert', 'error');
    }
  };

  const handleAttachMonitor = async () => {
    if (!incident || !selectedMonitorId) {
      return;
    }
    try {
      const response = await attachIncidentMonitor(incident.id, selectedMonitorId);
      setIncident(response);
      setSelectedMonitorId('');
      showToast('Monitor attached to incident', 'success');
    } catch (err) {
      showToast(err instanceof Error ? err.message : 'Failed to attach monitor', 'error');
    }
  };

  const handleDetachMonitor = async (monitorId: string) => {
    if (!incident) {
      return;
    }
    try {
      const response = await detachIncidentMonitor(incident.id, monitorId);
      setIncident(response);
      showToast('Monitor detached from incident', 'success');
    } catch (err) {
      showToast(err instanceof Error ? err.message : 'Failed to detach monitor', 'error');
    }
  };

  const handlePublish = async (statusPageId: string, monitorIds: string[]) => {
    if (!incident) {
      return;
    }
    const response = await publishIncidentToStatusPage(incident.id, statusPageId, { monitor_ids: monitorIds });
    setIncident(response);
    showToast('Incident published to status page', 'success');
  };

  const handleUnpublish = async (statusPageId: string) => {
    if (!incident) {
      return;
    }
    const response = await unpublishIncidentFromStatusPage(incident.id, statusPageId);
    setIncident(response);
    showToast('Incident unpublished from status page', 'success');
  };

  if (loading) {
    return (
      <div className="glass p-8 text-center text-sm text-slate-500">
        Loading incident…
      </div>
    );
  }

  if (error || !incident) {
    return (
      <Callout
        tone="danger"
        action={<Button variant="subtle" size="xs" onClick={loadIncident}>Retry</Button>}
      >
        {error || 'Incident not found'}
      </Callout>
    );
  }

  const incidentAlerts = incident.alerts || [];
  const incidentMonitors = incident.monitors || [];
  const incidentPublications = incident.publications || [];
  const linkedAlertIDs = new Set(incidentAlerts.map((alert) => alert.id));
  const linkedMonitorIDs = new Set(incidentMonitors.map((monitor) => monitor.id));
  const availableAlerts = alerts.filter((alert) => !linkedAlertIDs.has(alert.id));
  const availableMonitors = monitors.filter((monitor) => !linkedMonitorIDs.has(monitor.id));

  return (
    <div className="space-y-6">
      <PageHeader
        breadcrumb={[{ label: 'Incidents', href: '/incidents' }, { label: incident.title }]}
        title={
          <span className="flex items-center gap-3">
            <span className="min-w-0 truncate">{incident.title}</span>
            <Pill tone={incidentStateTone(incident.state)} dot className="capitalize">
              {incident.state}
            </Pill>
          </span>
        }
        subtitle={incident.is_auto_created ? 'Auto-created incident' : 'Manual incident'}
        action={(
          <>
            <Button
              variant="ghost"
              onClick={() => handleStateTransition('identified')}
              disabled={incident.state === 'identified' || incident.state === 'resolved'}
            >
              Identify
            </Button>
            <Button
              variant="ghost"
              onClick={() => handleStateTransition('monitoring')}
              disabled={incident.state === 'monitoring' || incident.state === 'resolved'}
            >
              Monitor
            </Button>
            <Button
              variant="accent"
              onClick={() => handleStateTransition('resolved')}
              disabled={incident.state === 'resolved'}
            >
              Resolve
            </Button>
          </>
        )}
      />

      <div className="grid grid-cols-1 gap-6 xl:grid-cols-[minmax(0,1fr)_400px]">
        <div className="min-w-0 space-y-6">
          <Panel title="Summary">
            <p className="text-sm leading-relaxed text-slate-200">{incident.summary || 'No summary provided.'}</p>
          </Panel>

          <AIRootCausePanel incidentId={incident.id} initial={incident.ai_analysis ?? null} />

          <IncidentTimeline
            entries={incident.timeline || []}
            onAddInternalNote={handleAddInternalNote}
            onAddPublicUpdate={handleAddPublicUpdate}
          />
        </div>

        <div className="grid min-w-0 content-start gap-4 md:grid-cols-2 xl:grid-cols-1">
          <Panel title="Details">
            <dl className="space-y-2.5 text-sm">
              <div className="flex justify-between gap-3">
                <dt className="text-slate-400">Created</dt>
                <dd className="text-right text-slate-200">{formatDateTime(incident.created_at)}</dd>
              </div>
              <div className="flex justify-between gap-3">
                <dt className="text-slate-400">Last updated</dt>
                <dd className="text-right text-slate-200">{formatDateTime(incident.updated_at)}</dd>
              </div>
              <div className="flex justify-between gap-3">
                <dt className="text-slate-400">Resolved</dt>
                <dd className="text-right text-slate-200">{formatDateTime(incident.resolved_at, 'Not resolved')}</dd>
              </div>
            </dl>
          </Panel>

          <Panel title="Linked alerts">
            {incidentAlerts.length === 0 ? (
              <p className="text-sm text-slate-500">No alerts linked yet.</p>
            ) : (
              <div className="-mt-2 divide-y divide-divider">
                {incidentAlerts.map((alert) => (
                  <div key={alert.id} className="flex items-center justify-between gap-3 py-2.5">
                    <div className="min-w-0">
                      <div className="truncate text-sm font-medium text-white">{alert.monitor_name || alert.id}</div>
                      <div className="mt-0.5 text-xs text-slate-500">
                        {alert.status} · {alert.failure_count} failures
                      </div>
                    </div>
                    <IconButton size="sm" icon={<Unlink strokeWidth={1.75} />} label="Detach from incident" onClick={() => handleDetachAlert(alert.id)} />
                  </div>
                ))}
              </div>
            )}
            <div className="mt-4 flex items-center gap-2">
              <select
                className={ATTACH_SELECT}
                value={selectedAlertId}
                onChange={(event) => setSelectedAlertId(event.target.value)}
              >
                <option value="">Select alert</option>
                {availableAlerts.map((alert) => (
                  <option key={alert.id} value={alert.id}>
                    {alert.monitor_name || alert.id} · {alert.status}
                  </option>
                ))}
              </select>
              <Button
                variant="ghost"
                onClick={handleAttachAlert}
                disabled={!selectedAlertId}
              >
                Attach
              </Button>
            </div>
          </Panel>

          <Panel title="Linked monitors">
            {incidentMonitors.length === 0 ? (
              <p className="text-sm text-slate-500">No monitors linked yet.</p>
            ) : (
              <div className="-mt-2 divide-y divide-divider">
                {incidentMonitors.map((monitor) => (
                  <div key={monitor.id} className="flex items-center justify-between gap-3 py-2.5">
                    <div className="min-w-0">
                      <div className="truncate text-sm font-medium text-white">{monitor.name}</div>
                      <div className="mt-0.5 text-xs text-slate-500">{monitorTypeLabel(monitor.type)}</div>
                    </div>
                    <IconButton size="sm" icon={<Unlink strokeWidth={1.75} />} label="Detach from incident" onClick={() => handleDetachMonitor(monitor.id)} />
                  </div>
                ))}
              </div>
            )}
            <div className="mt-4 flex items-center gap-2">
              <select
                className={ATTACH_SELECT}
                value={selectedMonitorId}
                onChange={(event) => setSelectedMonitorId(event.target.value)}
              >
                <option value="">Select monitor</option>
                {availableMonitors.map((monitor) => (
                  <option key={monitor.id} value={monitor.id}>
                    {monitor.name} · {monitorTypeLabel(monitor.type)}
                  </option>
                ))}
              </select>
              <Button
                variant="ghost"
                onClick={handleAttachMonitor}
                disabled={!selectedMonitorId}
              >
                Attach
              </Button>
            </div>
          </Panel>

          <IncidentPublicationEditor
            incident={{ ...incident, alerts: incidentAlerts, monitors: incidentMonitors, publications: incidentPublications }}
            onPublish={handlePublish}
            onUnpublish={handleUnpublish}
          />
        </div>
      </div>
    </div>
  );
}
