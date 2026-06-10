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
import IncidentPublicationEditor from '@/components/incidents/IncidentPublicationEditor';
import IncidentTimeline from '@/components/incidents/IncidentTimeline';
import Panel from '@/components/ui/Panel';
import { useToast } from '@/components/ui/ToastProvider';
import PageHeader from '@/components/ui/PageHeader';
import Button from '@/components/ui/Button';
import Pill from '@/components/ui/Pill';
import { formatDateTime } from '@/lib/format';

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
      <div className="rounded-xl border border-white/[0.06] bg-slate-900/50 p-6 text-sm text-slate-500">
        Loading incident…
      </div>
    );
  }

  if (error || !incident) {
    return (
      <div className="space-y-4">
        <div className="rounded-xl border border-rose-500/20 bg-rose-500/10 px-4 py-3 text-sm text-rose-300">
          {error || 'Incident not found'}
        </div>
        <Button variant="ghost" size="sm" onClick={loadIncident}>Retry</Button>
      </div>
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
        title={incident.title}
        subtitle={incident.is_auto_created ? 'Auto-created incident' : 'Manual incident'}
      />

      <Panel
        title="Status"
        actions={(
          <div className="flex items-center gap-1.5">
            <Button
              variant="ghost"
              size="xs"
              onClick={() => handleStateTransition('identified')}
              disabled={incident.state === 'identified' || incident.state === 'resolved'}
            >
              Identify
            </Button>
            <Button
              variant="ghost"
              size="xs"
              onClick={() => handleStateTransition('monitoring')}
              disabled={incident.state === 'monitoring' || incident.state === 'resolved'}
            >
              Monitor
            </Button>
            <Button
              variant="accent"
              size="xs"
              onClick={() => handleStateTransition('resolved')}
              disabled={incident.state === 'resolved'}
            >
              Resolve
            </Button>
          </div>
        )}
      >
        <div className="grid gap-6 lg:grid-cols-[minmax(0,1.6fr)_minmax(280px,1fr)]">
          <div className="space-y-3">
            <p className="text-sm text-slate-300">{incident.summary || 'No summary provided.'}</p>
            <div className="flex flex-wrap gap-1.5">
              <Pill tone="danger" size="xs" dot>{incident.state}</Pill>
              <Pill tone={incident.is_auto_created ? 'warning' : 'neutral'} size="xs">
                {incident.is_auto_created ? 'Auto' : 'Manual'}
              </Pill>
            </div>
          </div>

          <div className="grid gap-3 rounded-xl border border-white/[0.06] bg-slate-950/40 p-4 text-sm">
            <div>
              <div className="text-xs uppercase tracking-wide text-slate-500">Created</div>
              <div className="mt-1 text-slate-300">{formatDateTime(incident.created_at)}</div>
            </div>
            <div>
              <div className="text-xs uppercase tracking-wide text-slate-500">Last updated</div>
              <div className="mt-1 text-slate-300">{formatDateTime(incident.updated_at)}</div>
            </div>
            <div>
              <div className="text-xs uppercase tracking-wide text-slate-500">Resolved</div>
              <div className="mt-1 text-slate-300">{formatDateTime(incident.resolved_at, 'Not resolved')}</div>
            </div>
          </div>
        </div>
      </Panel>

      <div className="grid gap-6 xl:grid-cols-2">
        <Panel
          title="Linked alerts"
          subtitle={`${incidentAlerts.length} linked alert${incidentAlerts.length === 1 ? '' : 's'}`}
          actions={(
            <div className="flex items-center gap-2">
              <select
                className="input h-8 min-w-[220px] py-0 text-xs"
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
                size="xs"
                onClick={handleAttachAlert}
                disabled={!selectedAlertId}
              >
                Attach
              </Button>
            </div>
          )}
        >
          <div className="space-y-3">
            {incidentAlerts.length === 0 ? (
              <div className="text-sm text-slate-500">No alerts linked yet.</div>
            ) : (
              incidentAlerts.map((alert) => (
                <div key={alert.id} className="flex items-start justify-between gap-4 rounded-xl border border-white/[0.06] bg-slate-950/40 px-4 py-3">
                  <div>
                    <div className="text-sm font-medium text-white">{alert.monitor_name || alert.id}</div>
                    <div className="mt-1 text-xs text-slate-500">
                      {alert.policy_name || 'Unknown policy'} · {alert.status} · {alert.failure_count} failures
                    </div>
                  </div>
                  <Button variant="ghost" size="xs" onClick={() => handleDetachAlert(alert.id)}>
                    Detach
                  </Button>
                </div>
              ))
            )}
          </div>
        </Panel>

        <Panel
          title="Linked monitors"
          subtitle={`${incidentMonitors.length} linked monitor${incidentMonitors.length === 1 ? '' : 's'}`}
          actions={(
            <div className="flex items-center gap-2">
              <select
                className="input h-8 min-w-[220px] py-0 text-xs"
                value={selectedMonitorId}
                onChange={(event) => setSelectedMonitorId(event.target.value)}
              >
                <option value="">Select monitor</option>
                {availableMonitors.map((monitor) => (
                  <option key={monitor.id} value={monitor.id}>
                    {monitor.name} · {monitor.type}
                  </option>
                ))}
              </select>
              <Button
                variant="ghost"
                size="xs"
                onClick={handleAttachMonitor}
                disabled={!selectedMonitorId}
              >
                Attach
              </Button>
            </div>
          )}
        >
          <div className="space-y-3">
            {incidentMonitors.length === 0 ? (
              <div className="text-sm text-slate-500">No monitors linked yet.</div>
            ) : (
              incidentMonitors.map((monitor) => (
                <div key={monitor.id} className="flex items-start justify-between gap-4 rounded-xl border border-white/[0.06] bg-slate-950/40 px-4 py-3">
                  <div>
                    <div className="text-sm font-medium text-white">{monitor.name}</div>
                    <div className="mt-1 text-xs uppercase tracking-wide text-slate-500">{monitor.type}</div>
                  </div>
                  <Button variant="ghost" size="xs" onClick={() => handleDetachMonitor(monitor.id)}>
                    Detach
                  </Button>
                </div>
              ))
            )}
          </div>
        </Panel>
      </div>

      <IncidentTimeline
        entries={incident.timeline || []}
        onAddInternalNote={handleAddInternalNote}
        onAddPublicUpdate={handleAddPublicUpdate}
      />

      <IncidentPublicationEditor
        incident={{ ...incident, alerts: incidentAlerts, monitors: incidentMonitors, publications: incidentPublications }}
        onPublish={handlePublish}
        onUnpublish={handleUnpublish}
      />

    </div>
  );
}
