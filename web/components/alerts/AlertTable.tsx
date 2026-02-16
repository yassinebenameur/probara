'use client';

import { useState } from 'react';
import Link from 'next/link';
import { Alert, AlertStatus } from '@/lib/types';
import { acknowledgeAlert, resolveAlert } from '@/lib/api';

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

function formatDate(dateString: string): string {
  return new Date(dateString).toLocaleString();
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
      <div className="table-card">
        <div className="py-12 text-center">
          <div className="text-4xl mb-3">🔔</div>
          <div className="text-lg font-medium mb-1">No alerts found</div>
          <div className="text-sm text-slate-500">
            Alerts will appear here when monitors fail and trigger alert policies.
          </div>
        </div>
      </div>
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
              className={`transition-colors hover:bg-slate-800/30 ${idx % 2 === 1 ? 'bg-[rgba(15,23,42,0.35)]' : ''}`}
            >
              <td>
                <span className={`inline-flex items-center gap-1.5 rounded-full px-2 py-0.5 text-[10px] font-medium ${getStatusBadgeClass(alert.status)}`}>
                  <span className={`h-1.5 w-1.5 rounded-full ${getStatusColor(alert.status)}`} />
                  {alert.status}
                </span>
              </td>
              <td>
                <Link 
                  href={`/monitors/${alert.monitor_id}`}
                  className="font-medium text-white hover:text-cyan-400"
                >
                  {alert.monitor_name || alert.monitor_id}
                </Link>
                {alert.last_error && (
                  <p className="mt-0.5 text-[10px] text-rose-400 truncate max-w-[200px]" title={alert.last_error}>
                    {alert.last_error}
                  </p>
                )}
              </td>
              <td>
                <Link 
                  href={`/alert-policies/${alert.alert_policy_id}`}
                  className="text-slate-400 hover:text-cyan-400"
                >
                  {alert.policy_name || 'Unknown'}
                </Link>
              </td>
              <td className="text-slate-400">
                <div>{formatDate(alert.triggered_at)}</div>
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
                    <button
                      onClick={() => handleAcknowledge(alert.id)}
                      disabled={processingId === alert.id}
                      className="btn btn-warning btn-xs disabled:opacity-50"
                    >
                      {processingId === alert.id ? '...' : 'Acknowledge'}
                    </button>
                  )}
                  {(alert.status === 'active' || alert.status === 'acknowledged') && (
                    <button
                      onClick={() => handleResolve(alert.id)}
                      disabled={processingId === alert.id}
                      className="btn btn-success btn-xs disabled:opacity-50"
                    >
                      {processingId === alert.id ? '...' : 'Resolve'}
                    </button>
                  )}
                  {alert.status === 'resolved' && (
                    <span className="text-[10px] text-slate-500">
                      Resolved {alert.resolved_at ? formatDate(alert.resolved_at) : ''}
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
