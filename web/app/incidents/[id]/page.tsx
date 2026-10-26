'use client';

import { useParams } from 'next/navigation';
import { useCallback, useEffect, useState } from 'react';

import {
  addIncidentTimelineEntry,
  attachIncidentAlert,
  attachIncidentMonitor,
  getIncident,
  publishIncidentToStatusPage,
  transitionIncidentState,
  detachIncidentAlert,
  detachIncidentMonitor,
  unpublishIncidentFromStatusPage,
} from '@/lib/api';
import type { IncidentDetail, IncidentState, Monitor } from '@/lib/types';
import { getAllMonitors } from '@/lib/monitor-list';
import AIRootCausePanel from '@/components/incidents/AIRootCausePanel';
import AttachAlertsDialog from '@/components/incidents/AttachAlertsDialog';
import TriagePill from '@/components/alerts/TriagePill';
import IncidentPublicationEditor from '@/components/incidents/IncidentPublicationEditor';
import IncidentTimeline from '@/components/incidents/IncidentTimeline';
import Panel from '@/components/ui/Panel';
import { useToast } from '@/components/ui/ToastProvider';
import PageHeader from '@/components/ui/PageHeader';
import Page from '@/components/ui/Page';
import Button from '@/components/ui/Button';
import Callout from '@/components/ui/Callout';
import IconButton from '@/components/ui/IconButton';
import { Unlink } from 'lucide-react';
import Pill from '@/components/ui/Pill';
import ConfirmDialog from '@/components/ui/ConfirmDialog';
import { useCanWrite } from '@/components/providers/CurrentUserProvider';
import { formatDateTime, formatDateTimeFull, formatDurationBetween, pluralize } from '@/lib/format';
import {
  ALERT_STATUS_LABEL,
  INCIDENT_STATE_LABEL,
  alertStatusTone,
  incidentSeverityLabel,
  incidentSeverityTone,
  incidentStateTone,
} from '@/lib/status-tones';
import Link from 'next/link';
import { monitorTypeLabel } from '@/lib/monitor-type-labels';
import TypeBadge from '@/components/ui/TypeBadge';
import { ElapsedTime } from '@/components/ui/RelativeTime';
import useDocumentTitle from '@/lib/useDocumentTitle';

const ATTACH_SELECT = 'input h-9 min-w-0 flex-1 py-0 text-ui';

export default function IncidentDetailPage() {
  const params = useParams<{ id: string }>();
  const incidentId = Array.isArray(params?.id) ? params.id[0] : params?.id;

  const [incident, setIncident] = useState<IncidentDetail | null>(null);
  const [monitors, setMonitors] = useState<Monitor[]>([]);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState('');
  const [attachAlertsOpen, setAttachAlertsOpen] = useState(false);
  const [selectedMonitorId, setSelectedMonitorId] = useState('');
  const [transitioning, setTransitioning] = useState<IncidentState | null>(null);
  const [confirmResolve, setConfirmResolve] = useState(false);
  const { showToast } = useToast();
  const canWrite = useCanWrite();
  useDocumentTitle(incident?.title);

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

  // Every monitor (attach can target any of them). Alerts are not preloaded:
  // AttachAlertsDialog searches and pages the whole alert history itself.
  const loadResources = useCallback(async () => {
    try {
      setMonitors(await getAllMonitors());
    } catch {
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
    setTransitioning(state);
    try {
      const response = await transitionIncidentState(incident.id, state);
      setIncident(response);
      showToast(`Incident marked ${INCIDENT_STATE_LABEL[state].toLowerCase()}`, 'success');
    } catch (err) {
      showToast(err instanceof Error ? err.message : 'Failed to update incident state', 'error');
    } finally {
      setTransitioning(null);
      setConfirmResolve(false);
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
    showToast(
      response.publications?.length ? 'Public update posted' : 'Public update saved; publish the incident to show it',
      'success',
    );
  };

  // One request per alert (the API attaches one at a time). Each response is
  // the updated incident, so a failure part-way keeps what was attached and
  // the dialog stays open on the error.
  const handleAttachAlerts = async (alertIds: string[]) => {
    if (!incident) {
      return;
    }
    for (const alertId of alertIds) {
      const response = await attachIncidentAlert(incident.id, alertId);
      setIncident(response);
    }
    showToast(alertIds.length === 1 ? 'Alert attached to incident' : `${alertIds.length} alerts attached to incident`, 'success');
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
      <div className="mx-auto w-full max-w-[100rem] animate-pulse space-y-6" role="status" aria-label="Loading incident">
        <div className="space-y-2" aria-hidden="true">
          <div className="h-4 w-48 rounded-md bg-well-hover" />
          <div className="h-8 w-96 max-w-full rounded-lg bg-well-hover" />
          <div className="h-4 w-64 rounded-md bg-well-hover" />
        </div>
        <div className="grid grid-cols-1 gap-6 xl:grid-cols-[minmax(0,1fr)_400px] 2xl:grid-cols-[minmax(0,1fr)_440px]" aria-hidden="true">
          <div className="space-y-6">
            <div className="glass h-28" />
            <div className="glass h-[480px]" />
          </div>
          <div className="space-y-4">
            <div className="glass h-56" />
            <div className="glass h-40" />
          </div>
        </div>
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
  const availableMonitors = monitors.filter((monitor) => !linkedMonitorIDs.has(monitor.id));

  const resolved = incident.state === 'resolved';
  const countLabel = (n: number) => (
    <span className="ml-2 text-xs font-normal tabular-nums text-slate-500">{n}</span>
  );

  return (
    <Page width="detail">
      <PageHeader
        breadcrumb={[{ label: 'Incidents', href: '/incidents' }, { label: incident.title }]}
        title={incident.title}
        titleAside={
          <Pill tone={incidentStateTone(incident.state)} dot>
            {INCIDENT_STATE_LABEL[incident.state] ?? incident.state}
          </Pill>
        }
        subtitle={[
          <Pill key="sev" size="xs" tone={incidentSeverityTone(incident.severity)}>
            {incidentSeverityLabel(incident.severity)} severity
          </Pill>,
          <span key="opened" title={formatDateTimeFull(incident.created_at)}>
            Opened {formatDateTime(incident.created_at)}
          </span>,
          incident.resolved_at
            ? `Resolved after ${formatDurationBetween(incident.created_at, incident.resolved_at)}`
            : (
                <span key="open">
                  Open for <ElapsedTime start={incident.created_at} />
                </span>
              ),
        ]}
        action={
          // Offer only the moves out of the current state: the state itself is
          // already the pill beside the title, and a resolved incident has none
          // (the API refuses to reopen one).
          resolved || !canWrite ? undefined : (
            <>
              {incident.state !== 'identified' && (
                <Button
                  variant="ghost"
                  onClick={() => handleStateTransition('identified')}
                  disabled={transitioning !== null}
                  loading={transitioning === 'identified'}
                >
                  Mark identified
                </Button>
              )}
              {incident.state !== 'monitoring' && (
                <Button
                  variant="ghost"
                  onClick={() => handleStateTransition('monitoring')}
                  disabled={transitioning !== null}
                  loading={transitioning === 'monitoring'}
                >
                  Mark monitoring
                </Button>
              )}
              <Button variant="accent" onClick={() => setConfirmResolve(true)} disabled={transitioning !== null}>
                Resolve
              </Button>
            </>
          )
        }
      />

      <ConfirmDialog
        open={confirmResolve}
        title="Resolve this incident?"
        description={
          incidentPublications.length > 0
            ? 'A resolved incident cannot be reopened. Its status pages show it as resolved; post a closing public update first if customers need one.'
            : 'A resolved incident cannot be reopened.'
        }
        confirmLabel="Resolve incident"
        tone="default"
        loading={transitioning === 'resolved'}
        onConfirm={() => handleStateTransition('resolved')}
        onCancel={() => setConfirmResolve(false)}
      />

      <div className="grid grid-cols-1 gap-6 xl:grid-cols-[minmax(0,1fr)_400px] 2xl:grid-cols-[minmax(0,1fr)_440px]">
        <div className="min-w-0 space-y-6">
          <Panel title="Summary">
            <p className="max-w-[80ch] text-sm leading-relaxed text-slate-200">{incident.summary || 'No summary provided.'}</p>
          </Panel>

          <AIRootCausePanel incidentId={incident.id} initial={incident.ai_analysis ?? null} />

          <IncidentTimeline
            entries={incident.timeline || []}
            startedAt={incident.created_at}
            publishedTo={incidentPublications.map((p) => p.status_page_title)}
            resolved={resolved}
            canWrite={canWrite}
            onAddInternalNote={handleAddInternalNote}
            onAddPublicUpdate={handleAddPublicUpdate}
          />
        </div>

        <div className="grid min-w-0 content-start gap-4 md:grid-cols-2 xl:grid-cols-1">
          <Panel title="Details">
            <dl className="space-y-2.5 text-sm">
              <div className="flex justify-between gap-3">
                <dt className="text-slate-400">Owner</dt>
                <dd className="min-w-0 truncate text-right text-slate-200">{incident.owner_username || 'Unassigned'}</dd>
              </div>
              <div className="flex justify-between gap-3">
                <dt className="text-slate-400">Source</dt>
                <dd className="text-right text-slate-200">{incident.is_auto_created ? 'Opened automatically' : 'Opened manually'}</dd>
              </div>
              <div className="flex justify-between gap-3">
                <dt className="text-slate-400">Opened</dt>
                <dd className="text-right tabular-nums text-slate-200" title={formatDateTimeFull(incident.created_at)}>
                  {formatDateTime(incident.created_at)}
                </dd>
              </div>
              <div className="flex justify-between gap-3">
                <dt className="text-slate-400">Resolved</dt>
                <dd
                  className="text-right tabular-nums text-slate-200"
                  title={incident.resolved_at ? formatDateTimeFull(incident.resolved_at) : undefined}
                >
                  {formatDateTime(incident.resolved_at, 'Not yet')}
                </dd>
              </div>
              <div className="flex justify-between gap-3">
                <dt className="text-slate-400">Duration</dt>
                <dd className="text-right tabular-nums text-slate-200">
                  {incident.resolved_at
                    ? formatDurationBetween(incident.created_at, incident.resolved_at)
                    : <ElapsedTime start={incident.created_at} />}
                </dd>
              </div>
              <div className="flex justify-between gap-3">
                <dt className="text-slate-400">Last updated</dt>
                <dd className="text-right tabular-nums text-slate-200" title={formatDateTimeFull(incident.updated_at)}>
                  {formatDateTime(incident.updated_at)}
                </dd>
              </div>
            </dl>
          </Panel>

          <Panel title={<>Linked alerts{countLabel(incidentAlerts.length)}</>}>
            {incidentAlerts.length === 0 ? (
              <p className="text-sm text-slate-500">No alerts linked yet.</p>
            ) : (
              <ul className="-mt-2 divide-y divide-divider">
                {incidentAlerts.map((alert) => (
                  <li key={alert.id} className="flex items-center justify-between gap-3 py-2.5">
                    <div className="min-w-0">
                      <Link
                        href={`/alerts?alert=${alert.id}`}
                        className="block truncate text-sm font-medium text-white transition-colors hover:text-cyan-300"
                      >
                        {alert.monitor_name || alert.id}
                      </Link>
                      <div className="mt-1 flex flex-wrap items-center gap-2 text-xs tabular-nums text-slate-500">
                        <Pill size="xs" dot tone={alertStatusTone(alert.status)}>{ALERT_STATUS_LABEL[alert.status]}</Pill>
                        <TriagePill triage={alert.triage} />
                        <span>{pluralize(alert.failure_count, 'failure')}</span>
                      </div>
                    </div>
                    {canWrite && (
                      <IconButton size="sm" icon={<Unlink strokeWidth={1.75} />} label="Detach from incident" onClick={() => handleDetachAlert(alert.id)} />
                    )}
                  </li>
                ))}
              </ul>
            )}
            {canWrite && (
              <div className="mt-4">
                <Button variant="ghost" onClick={() => setAttachAlertsOpen(true)}>
                  Attach alerts…
                </Button>
              </div>
            )}
            {attachAlertsOpen && (
              <AttachAlertsDialog
                linkedAlertIds={linkedAlertIDs}
                onAttach={handleAttachAlerts}
                onClose={() => setAttachAlertsOpen(false)}
              />
            )}
          </Panel>

          <Panel title={<>Linked monitors{countLabel(incidentMonitors.length)}</>}>
            {incidentMonitors.length === 0 ? (
              <p className="text-sm text-slate-500">No monitors linked yet.</p>
            ) : (
              <ul className="-mt-2 divide-y divide-divider">
                {incidentMonitors.map((monitor) => (
                  <li key={monitor.id} className="flex items-center justify-between gap-3 py-2">
                    <div className="flex min-w-0 items-center gap-2.5">
                      <TypeBadge type={monitor.type} />
                      <Link
                        href={`/monitors/${monitor.id}`}
                        className="truncate text-sm font-medium text-white transition-colors hover:text-cyan-300"
                      >
                        {monitor.name}
                      </Link>
                    </div>
                    {canWrite && (
                      <IconButton size="sm" icon={<Unlink strokeWidth={1.75} />} label="Detach from incident" onClick={() => handleDetachMonitor(monitor.id)} />
                    )}
                  </li>
                ))}
              </ul>
            )}
            {canWrite && (
              <div className="mt-4 flex items-center gap-2">
                <select
                  className={ATTACH_SELECT}
                  value={selectedMonitorId}
                  onChange={(event) => setSelectedMonitorId(event.target.value)}
                  aria-label="Monitor to attach"
                >
                  <option value="" data-prompt>Attach a monitor…</option>
                  {availableMonitors.map((monitor) => (
                    <option key={monitor.id} value={monitor.id}>
                      {monitor.name} ({monitorTypeLabel(monitor.type)})
                    </option>
                  ))}
                </select>
                <Button variant="ghost" onClick={handleAttachMonitor} disabled={!selectedMonitorId}>
                  Attach
                </Button>
              </div>
            )}
          </Panel>

          <IncidentPublicationEditor
            incident={{ ...incident, alerts: incidentAlerts, monitors: incidentMonitors, publications: incidentPublications }}
            canWrite={canWrite}
            onPublish={handlePublish}
            onUnpublish={handleUnpublish}
          />
        </div>
      </div>
    </Page>
  );
}
