'use client';

import { ReactNode, useState } from 'react';
import Link from 'next/link';
import { Bell, BellOff, CircleCheck, Eye, Network } from 'lucide-react';
import { Alert, AlertStatus } from '@/lib/types';
import { acknowledgeAlert, resolveAlert } from '@/lib/api';
import { formatDateTime, pluralize } from '@/lib/format';
import { formatAlertValue, formatSeriesLabel } from '@/lib/metrics';
import IconButton from '@/components/ui/IconButton';
import EmptyState from '@/components/ui/EmptyState';
import Pill from '@/components/ui/Pill';

interface AlertTableProps {
  alerts: Alert[];
  onAlertUpdate?: (alert: Alert) => void;
  loading?: boolean;
}

function getStatusDot(status: AlertStatus): string {
  switch (status) {
    case 'active':
      return 'bg-rose-500';
    case 'acknowledged':
      return 'bg-amber-500';
    case 'resolved':
      return 'bg-emerald-500';
    default:
      return 'bg-slate-500';
  }
}

function getStatusText(status: AlertStatus): string {
  switch (status) {
    case 'active':
      return 'text-rose-300';
    case 'acknowledged':
      return 'text-amber-300';
    case 'resolved':
      return 'text-emerald-300';
    default:
      return 'text-slate-400';
  }
}

const DETAIL = 'mt-0.5 text-xs tabular-nums text-slate-400';
const CHIP = 'inline-flex max-w-full items-center gap-1 rounded-full px-2 py-0.5 text-[11px] font-medium';

function KindTag({ title, children }: { title: string; children: ReactNode }) {
  return (
    <span className="ml-2 align-middle" title={title}>
      <Pill size="xs">{children}</Pill>
    </span>
  );
}

function formatDuration(startDate: string, endDate?: string): string {
  const start = new Date(startDate);
  const end = endDate ? new Date(endDate) : new Date();
  const seconds = Math.floor((end.getTime() - start.getTime()) / 1000);

  if (seconds < 60) return `${seconds}s`;
  if (seconds < 3600) return `${Math.floor(seconds / 60)}m`;
  if (seconds < 86400) return `${Math.floor(seconds / 3600)}h ${Math.floor((seconds % 3600) / 60)}m`;
  return `${Math.floor(seconds / 86400)}d ${Math.floor((seconds % 86400) / 3600)}h`;
}

export default function AlertTable({ alerts, onAlertUpdate, loading }: AlertTableProps) {
  const [processingId, setProcessingId] = useState<string | null>(null);

  const handleAcknowledge = async (alertId: string) => {
    try {
      setProcessingId(alertId);
      const updated = await acknowledgeAlert(alertId);
      onAlertUpdate?.(updated);
    } catch (err: any) {
      console.error('Failed to acknowledge alert:', err);
    } finally {
      setProcessingId(null);
    }
  };

  const handleResolve = async (alertId: string) => {
    try {
      setProcessingId(alertId);
      const updated = await resolveAlert(alertId);
      onAlertUpdate?.(updated);
    } catch (err: any) {
      console.error('Failed to resolve alert:', err);
    } finally {
      setProcessingId(null);
    }
  };

  if (loading) {
    return (
      <div className="glass p-8 text-center text-sm text-slate-500">Loading alerts…</div>
    );
  }

  if (alerts.length === 0) {
    return (
      <EmptyState
        icon={<Bell strokeWidth={1.75} />}
        title="No alerts found"
        description="Alerts appear here when a monitor goes down."
      />
    );
  }

  return (
    <div className="glass overflow-hidden">
      <div className="overflow-x-auto">
        <table className="data-table text-[13px]">
          <thead>
            <tr>
              <th className="max-sm:w-8">
                <span className="max-sm:sr-only">Status</span>
              </th>
              <th className="w-full">Monitor</th>
              <th className="hidden md:table-cell">Triggered</th>
              <th className="hidden md:table-cell">Duration</th>
              <th className="hidden md:table-cell">Failures</th>
              <th>
                <span className="sr-only">Actions</span>
              </th>
            </tr>
          </thead>
          <tbody>
            {alerts.map((alert) => (
              <tr key={alert.id} className="transition-colors hover:bg-well">
                <td className="max-sm:pr-0">
                  <span className={`inline-flex items-center gap-2 whitespace-nowrap capitalize ${getStatusText(alert.status)}`}>
                    <span className={`h-2 w-2 shrink-0 rounded-full ${getStatusDot(alert.status)}`} />
                    <span className="max-sm:sr-only">{alert.status}</span>
                  </span>
                </td>
                <td className="max-w-0">
                  {alert.kind === 'mesh_edge' ? (
                    <Link
                      href="/locations?tab=mesh"
                      className="inline-flex items-center gap-1.5 text-sm font-medium text-white transition-colors hover:text-cyan-300"
                      title="Inter-location mesh path down — view the connectivity matrix"
                    >
                      <Network className="h-3.5 w-3.5 text-slate-400" strokeWidth={1.75} />
                      {alert.source_location_name || 'unknown'} → {alert.target_location_name || 'unknown'}
                    </Link>
                  ) : (
                    <Link
                      href={`/monitors/${alert.monitor_id}`}
                      className="text-sm font-medium text-white transition-colors hover:text-cyan-300"
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
                      {alert.last_error}
                    </p>
                  )}
                  <p className={`${DETAIL} md:hidden`}>
                    {formatDuration(alert.triggered_at, alert.resolved_at || undefined)} · {pluralize(alert.failure_count, 'failure')}
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
                            {alert.suppression_reason === 'dependency' ? 'Suppressed · caused by' : 'Likely caused by'}: {alert.root_cause_monitor_name}
                          </span>
                        </Link>
                      )}
                      {alert.suppression_reason === 'dependency' && !alert.root_cause_monitor_name && (
                        <span
                          className={`${CHIP} bg-well-hover text-slate-400`}
                          title="Notifications suppressed: the upstream dependency recovered moments ago; this pages if still down after the grace period"
                        >
                          <BellOff className="h-3 w-3 shrink-0" strokeWidth={1.75} />
                          Suppressed · upstream just recovered
                        </span>
                      )}
                      {(alert.impacted_count ?? 0) > 0 && (
                        <span
                          className={`${CHIP} bg-amber-500/[0.15] text-amber-300`}
                          title="Downstream monitors whose notifications this alert stands in for"
                        >
                          Affects {alert.impacted_count} monitor{alert.impacted_count === 1 ? '' : 's'}
                        </span>
                      )}
                    </div>
                  )}
                </td>
                <td className="hidden whitespace-nowrap text-slate-400 md:table-cell">
                  {formatDateTime(alert.triggered_at)}
                </td>
                <td className="hidden whitespace-nowrap tabular-nums text-slate-400 md:table-cell">
                  {formatDuration(alert.triggered_at, alert.resolved_at || undefined)}
                </td>
                <td className="hidden tabular-nums text-slate-300 md:table-cell">
                  {alert.failure_count}
                </td>
                <td className="text-right">
                  <div className="flex items-center justify-end gap-1">
                    {alert.status === 'active' && (
                      <IconButton
                        icon={<Eye strokeWidth={1.75} />}
                        label="Acknowledge"
                        onClick={() => handleAcknowledge(alert.id)}
                        disabled={processingId === alert.id}
                      />
                    )}
                    {(alert.status === 'active' || alert.status === 'acknowledged') && (
                      <IconButton
                        icon={<CircleCheck strokeWidth={1.75} />}
                        label="Resolve"
                        onClick={() => handleResolve(alert.id)}
                        disabled={processingId === alert.id}
                        loading={processingId === alert.id}
                      />
                    )}
                    {alert.status === 'resolved' && (
                      <span className="whitespace-nowrap text-xs text-slate-500 max-md:hidden">
                        Resolved {alert.resolved_at ? formatDateTime(alert.resolved_at) : ''}
                      </span>
                    )}
                  </div>
                </td>
              </tr>
            ))}
          </tbody>
        </table>
      </div>
    </div>
  );
}
