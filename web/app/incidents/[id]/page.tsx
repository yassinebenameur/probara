'use client';

import Link from 'next/link';
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
import Toast from '@/components/ui/Toast';

function formatTimestamp(value?: string): string {
  if (!value) {
    return 'Not resolved';
  }
  const date = new Date(value);
  if (Number.isNaN(date.getTime())) {
    return value;
  }
  return date.toLocaleString();
}

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
  const [toast, setToast] = useState<{ message: string; type: 'success' | 'error' } | null>(null);

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
      setToast({ message: `Incident moved to ${state}`, type: 'success' });
    } catch (err) {
      setToast({ message: err instanceof Error ? err.message : 'Failed to update incident state', type: 'error' });
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
      setToast({ message: 'Alert attached to incident', type: 'success' });
    } catch (err) {
      setToast({ message: err instanceof Error ? err.message : 'Failed to attach alert', type: 'error' });
    }
  };

  const handleDetachAlert = async (alertId: string) => {
    if (!incident) {
      return;
    }
    try {
      const response = await detachIncidentAlert(incident.id, alertId);
      setIncident(response);
      setToast({ message: 'Alert detached from incident', type: 'success' });
    } catch (err) {
      setToast({ message: err instanceof Error ? err.message : 'Failed to detach alert', type: 'error' });
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
      setToast({ message: 'Monitor attached to incident', type: 'success' });
    } catch (err) {
      setToast({ message: err instanceof Error ? err.message : 'Failed to attach monitor', type: 'error' });
    }
  };

  const handleDetachMonitor = async (monitorId: string) => {
    if (!incident) {
      return;
    }
    try {
      const response = await detachIncidentMonitor(incident.id, monitorId);
      setIncident(response);
      setToast({ message: 'Monitor detached from incident', type: 'success' });
    } catch (err) {
      setToast({ message: err instanceof Error ? err.message : 'Failed to detach monitor', type: 'error' });
    }
  };

  const handlePublish = async (statusPageId: string, monitorIds: string[]) => {
    if (!incident) {
      return;
    }
    const response = await publishIncidentToStatusPage(incident.id, statusPageId, { monitor_ids: monitorIds });
    setIncident(response);
    setToast({ message: 'Incident published to status page', type: 'success' });
  };

  const handleUnpublish = async (statusPageId: string) => {
    if (!incident) {
      return;
    }
    const response = await unpublishIncidentFromStatusPage(incident.id, statusPageId);
    setIncident(response);
    setToast({ message: 'Incident unpublished from status page', type: 'success' });
  };

  if (loading) {
    return (
      <div className="rounded-xl border border-white/[0.06] bg-slate-900/50 p-6 text-sm text-slate-500">
        Loading incident...
      </div>
    );
  }

  if (error || !incident) {
    return (
      <div className="space-y-4">
        <div className="rounded-xl border border-rose-500/20 bg-rose-500/10 px-4 py-3 text-sm text-rose-300">
          {error || 'Incident not found'}
        </div>
        <button onClick={loadIncident} className="btn btn-primary btn-sm">
          Retry
        </button>
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
      <div className="flex items-center gap-2 text-xs text-slate-500">
        <Link href="/incidents" className="hover:text-slate-400">Incidents</Link>
        <svg className="h-3 w-3" fill="none" viewBox="0 0 24 24" stroke="currentColor">
          <path strokeLinecap="round" strokeLinejoin="round" strokeWidth={2} d="M9 5l7 7-7 7" />
        </svg>
        <span className="text-slate-400">{incident.title}</span>
      </div>

      <Panel
        title={incident.title}
        subtitle={incident.is_auto_created ? 'Auto-created incident' : 'Manual incident'}
        dotColor="var(--danger)"
        actions={(
          <>
            <button
              onClick={() => handleStateTransition('identified')}
              className="btn btn-secondary btn-xs"
              disabled={incident.state === 'identified' || incident.state === 'resolved'}
            >
              Identify
            </button>
            <button
              onClick={() => handleStateTransition('monitoring')}
              className="btn btn-secondary btn-xs"
              disabled={incident.state === 'monitoring' || incident.state === 'resolved'}
            >
              Monitor
            </button>
            <button
              onClick={() => handleStateTransition('resolved')}
              className="btn btn-success btn-xs"
              disabled={incident.state === 'resolved'}
            >
              Resolve
            </button>
          </>
        )}
      >
        <div className="grid gap-6 lg:grid-cols-[minmax(0,1.6fr)_minmax(280px,1fr)]">
          <div className="space-y-3">
            <p className="text-sm text-slate-300">{incident.summary || 'No summary provided.'}</p>
            <div className="flex flex-wrap gap-2">
              <span className="badge badge-danger">{incident.state}</span>
              <span className={`badge ${incident.is_auto_created ? 'badge-warning' : 'badge-default'}`}>
                {incident.is_auto_created ? 'Auto' : 'Manual'}
              </span>
            </div>
          </div>

          <div className="grid gap-3 rounded-xl border border-white/[0.06] bg-slate-950/40 p-4 text-sm">
            <div>
              <div className="text-xs uppercase tracking-wide text-slate-500">Created</div>
              <div className="mt-1 text-slate-200">{formatTimestamp(incident.created_at)}</div>
            </div>
            <div>
              <div className="text-xs uppercase tracking-wide text-slate-500">Last updated</div>
              <div className="mt-1 text-slate-200">{formatTimestamp(incident.updated_at)}</div>
            </div>
            <div>
              <div className="text-xs uppercase tracking-wide text-slate-500">Resolved</div>
              <div className="mt-1 text-slate-200">{formatTimestamp(incident.resolved_at)}</div>
            </div>
          </div>
        </div>
      </Panel>

      <div className="grid gap-6 xl:grid-cols-2">
        <Panel
          title="Linked Alerts"
          subtitle={`${incidentAlerts.length} linked alert${incidentAlerts.length === 1 ? '' : 's'}`}
          dotColor="var(--warning)"
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
              <button
                onClick={handleAttachAlert}
                className="btn btn-secondary btn-xs"
                disabled={!selectedAlertId}
              >
                Attach
              </button>
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
                  <button onClick={() => handleDetachAlert(alert.id)} className="btn btn-secondary btn-xs">
                    Detach
                  </button>
                </div>
              ))
            )}
          </div>
        </Panel>

        <Panel
          title="Linked Monitors"
          subtitle={`${incidentMonitors.length} linked monitor${incidentMonitors.length === 1 ? '' : 's'}`}
          dotColor="var(--info)"
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
              <button
                onClick={handleAttachMonitor}
                className="btn btn-secondary btn-xs"
                disabled={!selectedMonitorId}
              >
                Attach
              </button>
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
                  <button onClick={() => handleDetachMonitor(monitor.id)} className="btn btn-secondary btn-xs">
                    Detach
                  </button>
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

      {toast ? (
        <Toast
          message={toast.message}
          type={toast.type}
          onClose={() => setToast(null)}
        />
      ) : null}
    </div>
  );
}
