'use client';

import { useState } from 'react';
import Link from 'next/link';
import { Bell, Network } from 'lucide-react';
import { Alert, AlertStatus } from '@/lib/types';
import { acknowledgeAlert, resolveAlert } from '@/lib/api';
import { formatDateTime } from '@/lib/format';
import { formatAlertValue, formatSeriesLabel } from '@/lib/metrics';
import Button from '@/components/ui/Button';
import EmptyState from '@/components/ui/EmptyState';

interface AlertTableProps {
  alerts: Alert[];
  onAlertUpdate?: (alert: Alert) => void;
  loading?: boolean;
}

function getStatusColor(status: AlertStatus): string {
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

function getStatusBadgeClass(status: AlertStatus): string {
  switch (status) {
    case 'active':
      return 'bg-rose-500/10 text-rose-400 border border-rose-500/20';
    case 'acknowledged':
      return 'bg-amber-500/10 text-amber-400 border border-amber-500/20';
    case 'resolved':
      return 'bg-emerald-500/10 text-emerald-400 border border-emerald-500/20';
    default:
      return 'bg-slate-500/10 text-slate-400 border border-slate-500/20';
  }
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
      <div className="table-card">
        <div className="p-8 text-center text-slate-500">Loading alerts...</div>
      </div>
    );
  }

  if (alerts.length === 0) {
    return (
      <EmptyState
        icon={<Bell className="h-9 w-9" strokeWidth={1.5} />}
        title="No alerts found"
        description="Alerts will appear here when monitors fail and trigger alert policies."
      />
    );
  }

  return (
    <div className="table-card">
      <table className="data-table text-xs">
        <thead>
          <tr>
            <th>
              Status
            </th>
            <th>
              Monitor
            </th>
            <th>
              Policy
            </th>
            <th>
              Triggered
            </th>
            <th>
              Duration
            </th>
            <th>
              Failures
            </th>
            <th className="text-right">
              Actions
            </th>
          </tr>
        </thead>
        <tbody>
          {alerts.map((alert, idx) => (
            <tr 
              key={alert.id} 
              className={`transition-colors hover:bg-slate-800/30 ${idx % 2 === 1 ? 'bg-slate-900/[0.35]' : ''}`}
            >
              <td>
                <span className={`inline-flex items-center gap-1.5 rounded-full px-2 py-0.5 text-[10px] font-medium ${getStatusBadgeClass(alert.status)}`}>
                  <span className={`h-1.5 w-1.5 rounded-full ${getStatusColor(alert.status)}`} />
                  {alert.status}
                </span>
              </td>
              <td>
                {alert.kind === 'mesh_edge' ? (
                  <Link
                    href="/mesh"
                    className="inline-flex items-center gap-1.5 font-medium text-white hover:text-cyan-400"
                    title="Inter-location mesh path down — view the connectivity matrix"
                  >
                    <Network className="h-3.5 w-3.5 text-slate-400" strokeWidth={1.75} />
                    {alert.source_location_name || 'unknown'} → {alert.target_location_name || 'unknown'}
                  </Link>
                ) : (
                  <Link
                    href={`/monitors/${alert.monitor_id}`}
                    className="font-medium text-white hover:text-cyan-400"
                  >
                    {alert.monitor_name || alert.monitor_id}
                  </Link>
                )}
                {alert.kind === 'mesh_edge' && (
                  <span
                    className="ml-2 inline-flex items-center rounded-full border border-cyan-500/20 bg-cyan-500/10 px-2 py-0.5 text-[10px] font-medium text-cyan-300"
                    title="Directed inter-location connectivity path"
                  >
                    mesh
                  </span>
                )}
                {alert.kind === 'latency_anomaly' && (
                  <span
                    className="ml-2 inline-flex items-center rounded-full border border-violet-500/20 bg-violet-500/10 px-2 py-0.5 text-[10px] font-medium text-violet-300"
                    title="Latency degraded relative to baseline"
                  >
                    latency
                  </span>
                )}
                {alert.kind === 'latency_anomaly' && alert.observed_latency_ms != null && alert.baseline_latency_ms != null && (
                  <p className="mt-0.5 text-[10px] text-violet-300/80">
                    {Math.round(alert.observed_latency_ms)} ms vs ~{Math.round(alert.baseline_latency_ms)} ms baseline
                    {alert.anomaly_score != null && ` (${alert.anomaly_score.toFixed(1)}σ)`}
                  </p>
                )}
                {alert.kind === 'host_metric' && (
                  <span
                    className="ml-2 inline-flex items-center rounded-full border border-amber-500/20 bg-amber-500/10 px-2 py-0.5 text-[10px] font-medium text-amber-300"
                    title="Host metric breached its configured threshold"
                  >
                    {formatSeriesLabel(alert.metric_name)}
                  </span>
                )}
                {alert.kind === 'host_metric' && alert.metric_value != null && (
                  <p className="mt-0.5 text-[10px] text-amber-300/80">
                    {formatAlertValue(alert.metric_name, alert.metric_value)}
                    {alert.threshold_value != null &&
                      ` (threshold ${formatAlertValue(alert.metric_name, alert.threshold_value)})`}
                  </p>
                )}
                {alert.kind === 'tls_expiry' && (
                  <span
                    className="ml-2 inline-flex items-center rounded-full border border-orange-500/20 bg-orange-500/10 px-2 py-0.5 text-[10px] font-medium text-orange-300"
                    title="TLS certificate is inside its expiry warning window — the endpoint itself is still up"
                  >
                    certificate
                  </span>
                )}
                {alert.kind === 'tls_expiry' && alert.metric_value != null && (
                  <p className="mt-0.5 text-[10px] text-orange-300/80">
                    expires in {Math.round(alert.metric_value)}d
                    {alert.threshold_value != null && ` (threshold ${Math.round(alert.threshold_value)}d)`}
                  </p>
                )}
                {alert.last_error && (
                  <p className="mt-0.5 text-[10px] text-rose-400 truncate max-w-[200px]" title={alert.last_error}>
                    {alert.last_error}
                  </p>
                )}
                {alert.root_cause_monitor_name && alert.root_cause_monitor_id !== alert.monitor_id && (
                  <Link
                    href={`/monitors/${alert.root_cause_monitor_id}`}
                    className="mt-1 inline-flex max-w-[220px] items-center gap-1 truncate rounded-full border border-amber-500/20 bg-amber-500/10 px-2 py-0.5 text-[10px] font-medium text-amber-400 hover:border-amber-500/40"
                    title={`Likely caused by ${alert.root_cause_monitor_name}`}
                  >
                    likely caused by: {alert.root_cause_monitor_name}
                  </Link>
                )}
              </td>
              <td>
                <span className="text-slate-400">
                  {alert.policy_name || 'Unknown'}
                </span>
              </td>
              <td className="text-slate-400">
                <div>{formatDateTime(alert.triggered_at)}</div>
              </td>
              <td className="text-slate-400">
                {formatDuration(alert.triggered_at, alert.resolved_at || undefined)}
              </td>
              <td>
                <span className="inline-flex h-5 min-w-[20px] items-center justify-center rounded bg-slate-800 px-1.5 text-[10px] font-medium text-white">
                  {alert.failure_count}
                </span>
              </td>
              <td className="text-right">
                <div className="flex items-center justify-end gap-1">
                  {alert.status === 'active' && (
                    <Button
                      variant="ghost"
                      size="xs"
                      onClick={() => handleAcknowledge(alert.id)}
                      disabled={processingId === alert.id}
                      loading={processingId === alert.id}
                    >
                      {processingId === alert.id ? '…' : 'Acknowledge'}
                    </Button>
                  )}
                  {(alert.status === 'active' || alert.status === 'acknowledged') && (
                    <Button
                      variant="accent"
                      size="xs"
                      onClick={() => handleResolve(alert.id)}
                      disabled={processingId === alert.id}
                      loading={processingId === alert.id}
                    >
                      {processingId === alert.id ? '…' : 'Resolve'}
                    </Button>
                  )}
                  {alert.status === 'resolved' && (
                    <span className="text-[10px] text-slate-500">
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
  );
}
