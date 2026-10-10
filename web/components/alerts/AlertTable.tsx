'use client';

import { ReactNode, useState } from 'react';
import Link from 'next/link';
import { Bell, BellOff, CircleCheck, Network, UserCheck } from 'lucide-react';
import { Alert } from '@/lib/types';
import { acknowledgeAlert, resolveAlert } from '@/lib/api';
import { formatDateTime, formatDateTimeFull, formatDurationBetween, pluralize } from '@/lib/format';
import { formatAlertValue, formatSeriesLabel } from '@/lib/metrics';
import { shortFailureReason } from '@/lib/monitor-utils';
import { ALERT_STATUS_LABEL, TONE_DOT, alertStatusTone } from '@/lib/status-tones';
import { useCanWrite } from '@/components/providers/CurrentUserProvider';
import IconButton from '@/components/ui/IconButton';
import EmptyState from '@/components/ui/EmptyState';
import Pill from '@/components/ui/Pill';
import TableSkeleton from '@/components/ui/TableSkeleton';
import { useToast } from '@/components/ui/ToastProvider';

interface AlertTableProps {
  alerts: Alert[];
  onAlertUpdate?: (alert: Alert) => void;
  /** A fetch is in flight: skeleton on the first load, dimmed rows after. */
  loading?: boolean;
  emptyTitle?: string;
}

const DETAIL = 'mt-0.5 text-xs tabular-nums text-slate-400';
const CHIP = 'inline-flex max-w-full items-center gap-1 rounded-full px-2 py-0.5 text-2xs font-medium';

function KindTag({ title, children }: { title: string; children: ReactNode }) {
  return (
    <span className="ml-2 align-middle" title={title}>
      <Pill size="xs">{children}</Pill>
    </span>
  );
}

function errorMessage(err: unknown, fallback: string): string {
  return err instanceof Error && err.message ? err.message : fallback;
}

export default function AlertTable({ alerts, onAlertUpdate, loading, emptyTitle = 'No alerts found' }: AlertTableProps) {
  const [processing, setProcessing] = useState<{ id: string; action: 'ack' | 'resolve' } | null>(null);
  const canWrite = useCanWrite();
  const { showToast } = useToast();

  const handleAcknowledge = async (alertId: string) => {
    try {
      setProcessing({ id: alertId, action: 'ack' });
      const updated = await acknowledgeAlert(alertId);
      onAlertUpdate?.(updated);
      showToast('Alert acknowledged', 'success');
    } catch (err) {
      showToast(errorMessage(err, 'Failed to acknowledge alert'), 'error');
    } finally {
      setProcessing(null);
    }
  };

  const handleResolve = async (alertId: string) => {
    try {
      setProcessing({ id: alertId, action: 'resolve' });
      const updated = await resolveAlert(alertId);
      onAlertUpdate?.(updated);
      showToast('Alert resolved', 'success');
    } catch (err) {
      showToast(errorMessage(err, 'Failed to resolve alert'), 'error');
    } finally {
      setProcessing(null);
    }
  };

  // The skeleton is for the first load only; a refetch keeps the rows on
  // screen (dimmed) so the page neither jumps nor loses its scroll.
  if (alerts.length === 0) {
    if (loading) return <TableSkeleton rows={5} columns={3} label="Loading alerts" />;
    return (
      <EmptyState
        icon={<Bell strokeWidth={1.75} />}
        title={emptyTitle}
        description="Alerts appear here when a monitor goes down."
      />
    );
  }

  // No actions column when nothing listed can be acted on (the Resolved
  // view): two empty slots per row would only squeeze the monitor column.
  const showActions = canWrite && alerts.some((alert) => alert.status !== 'resolved');

  return (
    <div
      className={`glass overflow-hidden transition-opacity ${loading ? 'pointer-events-none opacity-60' : ''}`}
      aria-busy={loading || undefined}
    >
      <div className="overflow-x-auto">
        <table className="data-table text-ui">
          <thead>
            <tr>
              {/* Fixed-width labels (Status fits "Acknowledged") set each
                  column's minimum, so switching the status filter never
                  shifts the columns; Monitor (w-full) takes the rest. */}
              <th className="max-sm:w-8">
                <span className="max-sm:sr-only sm:inline-block sm:w-28">Status</span>
              </th>
              <th className="w-full">Monitor</th>
              <th className="hidden md:table-cell">
                <span className="inline-block w-24">Started</span>
              </th>
              <th className="hidden md:table-cell">
                <span className="inline-block w-32">Duration</span>
              </th>
              {showActions && (
                <th>
                  <span className="sr-only">Actions</span>
                </th>
              )}
            </tr>
          </thead>
          <tbody>
            {alerts.map((alert) => (
              <tr key={alert.id}>
                <td className="max-sm:pr-0">
                  <span className="max-sm:hidden">
                    <Pill size="sm" dot tone={alertStatusTone(alert.status)}>{ALERT_STATUS_LABEL[alert.status]}</Pill>
                  </span>
                  <span
                    className={`block h-2 w-2 rounded-full sm:hidden ${TONE_DOT[alertStatusTone(alert.status)]}`}
                    title={ALERT_STATUS_LABEL[alert.status]}
                  >
                    <span className="sr-only">{ALERT_STATUS_LABEL[alert.status]}</span>
                  </span>
                </td>
                <td className="max-w-0">
                  {alert.kind === 'mesh_edge' ? (
                    <Link
                      href="/locations?tab=mesh"
                      className="inline-flex items-center gap-1.5 text-sm font-medium text-white transition-colors hover:text-cyan-300 rounded-sm focus:outline-none focus-visible:ring-2 focus-visible:ring-cyan-500/50"
                      title="Inter-location mesh path down — view the connectivity matrix"
                    >
                      <Network className="h-3.5 w-3.5 text-slate-400" strokeWidth={1.75} />
                      {alert.source_location_name || 'unknown'} → {alert.target_location_name || 'unknown'}
                    </Link>
                  ) : (
                    <Link
                      href={`/monitors/${alert.monitor_id}`}
                      className="text-sm font-medium text-white transition-colors hover:text-cyan-300 rounded-sm focus:outline-none focus-visible:ring-2 focus-visible:ring-cyan-500/50"
                    >
                      {alert.monitor_name || alert.monitor_id}
                    </Link>
                  )}
                  {alert.kind === 'mesh_edge' && (
                    <KindTag title="Directed inter-location connectivity path">mesh</KindTag>
                  )}
                  {alert.kind === 'latency_anomaly' && (
                    <KindTag title="Latency degraded relative to baseline">latency</KindTag>
                  )}
                  {alert.kind === 'latency_anomaly' && alert.observed_latency_ms != null && alert.baseline_latency_ms != null && (
                    <p className={DETAIL}>
                      {Math.round(alert.observed_latency_ms)} ms vs ~{Math.round(alert.baseline_latency_ms)} ms baseline
                      {alert.anomaly_score != null && ` (${alert.anomaly_score.toFixed(1)}σ)`}
                    </p>
                  )}
                  {alert.kind === 'host_metric' && (
                    <KindTag title="Host metric breached its configured threshold">
                      {formatSeriesLabel(alert.metric_name)}
                    </KindTag>
                  )}
                  {alert.kind === 'host_metric' && alert.metric_value != null && (
                    <p className={DETAIL}>
                      {formatAlertValue(alert.metric_name, alert.metric_value)}
                      {alert.threshold_value != null &&
                        ` (threshold ${formatAlertValue(alert.metric_name, alert.threshold_value)})`}
                    </p>
                  )}
                  {alert.kind === 'tls_expiry' && (
                    <KindTag title="TLS certificate is inside its expiry warning window — the endpoint itself is still up">
                      certificate
                    </KindTag>
                  )}
                  {alert.kind === 'tls_expiry' && alert.metric_value != null && (
                    <p className={DETAIL}>
                      expires in {Math.round(alert.metric_value)}d
                      {alert.threshold_value != null && ` (threshold ${Math.round(alert.threshold_value)}d)`}
                    </p>
                  )}
                  {alert.last_error && (
                    <p className="mt-0.5 truncate text-xs text-slate-400" title={alert.last_error}>
                      {shortFailureReason(alert.last_error) || alert.last_error}
                    </p>
                  )}
                  <p className={`${DETAIL} flex flex-wrap gap-x-3 md:hidden`}>
                    <span>{formatDurationBetween(alert.triggered_at, alert.resolved_at)}</span>
                    <span>{alert.resolved_at ? `ended ${formatDateTime(alert.resolved_at)}` : 'ongoing'}</span>
                    <span>{pluralize(alert.failure_count, 'recent failure')}</span>
                  </p>
                  {((alert.root_cause_monitor_name && alert.root_cause_monitor_id !== alert.monitor_id) ||
                    (alert.suppression_reason === 'dependency' && !alert.root_cause_monitor_name) ||
                    (alert.impacted_count ?? 0) > 0) && (
                    <div className="mt-1.5 flex flex-wrap gap-1">
                      {alert.root_cause_monitor_name && alert.root_cause_monitor_id !== alert.monitor_id && (
                        <Link
                          href={`/monitors/${alert.root_cause_monitor_id}`}
                          className={`${CHIP} min-w-0 transition-colors ${
                            alert.suppression_reason === 'dependency'
                              ? 'bg-well-hover text-slate-400 hover:text-slate-200'
                              : 'bg-amber-500/[0.15] text-amber-300 hover:text-amber-200'
                          }`}
                          title={
                            alert.suppression_reason === 'dependency'
                              ? `Notifications suppressed: caused by ${alert.root_cause_monitor_name}`
                              : `Likely caused by ${alert.root_cause_monitor_name}`
                          }
                        >
                          {alert.suppression_reason === 'dependency' && <BellOff className="h-3 w-3 shrink-0" strokeWidth={1.75} />}
                          <span className="truncate">
                            {alert.suppression_reason === 'dependency' ? 'Suppressed, caused by' : 'Likely caused by'} {alert.root_cause_monitor_name}
                          </span>
                        </Link>
                      )}
                      {alert.suppression_reason === 'dependency' && !alert.root_cause_monitor_name && (
                        <span
                          className={`${CHIP} bg-well-hover text-slate-400`}
                          title="Notifications suppressed: the upstream dependency recovered moments ago; this pages if still down after the grace period"
                        >
                          <BellOff className="h-3 w-3 shrink-0" strokeWidth={1.75} />
                          Suppressed, upstream just recovered
                        </span>
                      )}
                      {(alert.impacted_count ?? 0) > 0 && (
                        <span
                          className={`${CHIP} bg-amber-500/[0.15] text-amber-300`}
                          title="Downstream monitors whose notifications this alert stands in for"
                        >
                          Affects {pluralize(alert.impacted_count ?? 0, 'monitor')}
                        </span>
                      )}
                    </div>
                  )}
                </td>
                <td className="hidden whitespace-nowrap tabular-nums md:table-cell">
                  <div className="text-slate-300" title={formatDateTimeFull(alert.triggered_at)}>
                    {formatDateTime(alert.triggered_at)}
                  </div>
                </td>
                <td className="hidden whitespace-nowrap tabular-nums md:table-cell">
                  <div className="text-slate-300">
                    {formatDurationBetween(alert.triggered_at, alert.resolved_at)}
                  </div>
                  <div
                    className="mt-0.5 text-xs text-slate-500"
                    title={alert.resolved_at ? formatDateTimeFull(alert.resolved_at) : undefined}
                  >
                    {alert.resolved_at ? `ended ${formatDateTime(alert.resolved_at)}` : 'ongoing'}
                  </div>
                  {/* The alerter rewrites failure_count with the failures in
                      its current window on every evaluation, so it is a
                      recent count, not the number that triggered the alert. */}
                  <div className="mt-0.5 text-xs text-slate-500">
                    {pluralize(alert.failure_count, 'recent failure')}
                  </div>
                </td>
                {showActions && (
                  <td className="text-right">
                    {/* Two fixed slots, acknowledge then resolve, so Resolve
                        lines up down the column whatever the row's state. */}
                    <div className="flex items-center justify-end gap-1">
                      {alert.status === 'active' ? (
                        <IconButton
                          icon={<UserCheck strokeWidth={1.75} />}
                          label="Acknowledge"
                          onClick={() => handleAcknowledge(alert.id)}
                          disabled={processing?.id === alert.id}
                          loading={processing?.id === alert.id && processing.action === 'ack'}
                        />
                      ) : alert.status === 'acknowledged' ? (
                        <span
                          className="inline-flex h-8 w-8 items-center justify-center text-amber-300 [&_svg]:h-4 [&_svg]:w-4"
                          title={alert.acknowledged_at ? `Acknowledged ${formatDateTime(alert.acknowledged_at)}` : 'Acknowledged'}
                        >
                          <UserCheck strokeWidth={1.75} aria-hidden="true" />
                          <span className="sr-only">Acknowledged</span>
                        </span>
                      ) : (
                        <span className="h-8 w-8" aria-hidden="true" />
                      )}
                      {alert.status !== 'resolved' ? (
                        <IconButton
                          icon={<CircleCheck strokeWidth={1.75} />}
                          label="Resolve"
                          onClick={() => handleResolve(alert.id)}
                          disabled={processing?.id === alert.id}
                          loading={processing?.id === alert.id && processing.action === 'resolve'}
                        />
                      ) : (
                        <span className="h-8 w-8" aria-hidden="true" />
                      )}
                    </div>
                  </td>
                )}
              </tr>
            ))}
          </tbody>
        </table>
      </div>
    </div>
  );
}
