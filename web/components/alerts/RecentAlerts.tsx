'use client';

import { useState, useEffect, useCallback } from 'react';
import Link from 'next/link';
import { ArrowRight } from 'lucide-react';
import { Alert, AlertStatus } from '@/lib/types';
import { getRecentAlerts, acknowledgeAlert, resolveAlert } from '@/lib/api';
import { useAlertEvents } from '@/components/alerts/AlertStreamProvider';
import Button from '@/components/ui/Button';
import Callout from '@/components/ui/Callout';
import Pill from '@/components/ui/Pill';

interface RecentAlertsProps {
  limit?: number;
  showActions?: boolean;
  onAlertUpdate?: (alert: Alert) => void;
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

function getStatusTextColor(status: AlertStatus): string {
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

function formatTimeAgo(dateString: string): string {
  const date = new Date(dateString);
  const now = new Date();
  const seconds = Math.floor((now.getTime() - date.getTime()) / 1000);

  if (seconds < 60) return 'just now';
  if (seconds < 3600) return `${Math.floor(seconds / 60)}m ago`;
  if (seconds < 86400) return `${Math.floor(seconds / 3600)}h ago`;
  return `${Math.floor(seconds / 86400)}d ago`;
}

export default function RecentAlerts({ 
  limit = 5, 
  showActions = true,
  onAlertUpdate 
}: RecentAlertsProps) {
  const [alerts, setAlerts] = useState<Alert[]>([]);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);
  const [processingId, setProcessingId] = useState<string | null>(null);
  const { subscribe } = useAlertEvents();

  const loadAlerts = useCallback(async () => {
    try {
      setLoading(true);
      setError(null);
      const data = await getRecentAlerts(limit);
      setAlerts(data || []);
    } catch (err: any) {
      console.error('Failed to load alerts:', err);
      setError(err.message || 'Failed to load alerts');
    } finally {
      setLoading(false);
    }
  }, [limit]);

  useEffect(() => {
    loadAlerts();
  }, [loadAlerts]);

  // Handle real-time updates via SSE
  const handleAlertCreated = useCallback((alert: Alert) => {
    setAlerts(prev => {
      const updated = [alert, ...prev.filter(a => a.id !== alert.id)].slice(0, limit);
      return updated;
    });
    onAlertUpdate?.(alert);
  }, [limit, onAlertUpdate]);

  const handleAlertAcknowledged = useCallback((alert: Alert) => {
    setAlerts(prev => prev.map(a => a.id === alert.id ? alert : a));
    onAlertUpdate?.(alert);
  }, [onAlertUpdate]);

  const handleAlertResolved = useCallback((alert: Alert) => {
    setAlerts(prev => prev.map(a => a.id === alert.id ? alert : a));
    onAlertUpdate?.(alert);
  }, [onAlertUpdate]);

  useEffect(() => {
    return subscribe((event) => {
      switch (event.type) {
        case 'created':
          handleAlertCreated(event.alert);
          break;
        case 'acknowledged':
          handleAlertAcknowledged(event.alert);
          break;
        case 'resolved':
          handleAlertResolved(event.alert);
          break;
        default:
          break;
      }
    });
  }, [handleAlertAcknowledged, handleAlertCreated, handleAlertResolved, subscribe]);

  const handleAcknowledge = async (alertId: string) => {
    try {
      setProcessingId(alertId);
      const updated = await acknowledgeAlert(alertId);
      setAlerts(prev => prev.map(a => a.id === alertId ? updated : a));
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
      setAlerts(prev => prev.map(a => a.id === alertId ? updated : a));
      onAlertUpdate?.(updated);
    } catch (err: any) {
      console.error('Failed to resolve alert:', err);
    } finally {
      setProcessingId(null);
    }
  };

  if (loading) {
    return (
      <div className="glass p-5">
        <div className="mb-4">
          <h3 className="text-[15px] font-semibold text-white">Recent alerts</h3>
          <p className="text-xs text-slate-500">Loading...</p>
        </div>
        <div className="space-y-3">
          {[...Array(3)].map((_, i) => (
            <div key={i} className="h-12 animate-pulse rounded-lg bg-well-hover" />
          ))}
        </div>
      </div>
    );
  }

  const activeAlerts = alerts.filter(a => a.status === 'active');
  const activeCount = activeAlerts.length;

  return (
    <div className="glass overflow-hidden">
      <div className="flex items-center justify-between gap-3 px-5 py-4">
        <div>
          <div className="flex items-center gap-2">
            <h3 className="text-[15px] font-semibold text-white">Recent alerts</h3>
            {activeCount > 0 && (
              <Pill tone="danger" size="xs">{activeCount}</Pill>
            )}
          </div>
          <p className="text-xs text-slate-500">
            {alerts.length === 0 ? 'No alerts' : `${alerts.length} recent alert${alerts.length !== 1 ? 's' : ''}`}
          </p>
        </div>
        <Link href="/alerts" className="inline-flex items-center gap-1.5 text-[13px] text-cyan-300 transition-colors hover:text-cyan-200">
          View all <ArrowRight className="h-3.5 w-3.5" strokeWidth={1.75} />
        </Link>
      </div>

      {error && (
        <Callout
          tone="danger"
          className="mx-5 mb-4"
          action={<Button variant="subtle" size="xs" onClick={loadAlerts}>Retry</Button>}
        >
          {error}
        </Callout>
      )}

      {alerts.length === 0 ? (
        <div className="flex flex-col items-center justify-center px-5 pb-10 pt-6 text-center">
          <p className="text-sm text-slate-400">No alerts yet</p>
          <p className="mt-1 text-xs text-slate-500">
            Alerts will appear when monitors fail
          </p>
        </div>
      ) : (
        <div className="divide-y divide-divider pb-1.5">
          {alerts.map((alert) => (
            <div
              key={alert.id}
              className="px-5 py-3.5 transition-colors hover:bg-well"
            >
              <div className="flex items-start justify-between gap-2">
                <div className="flex-1 min-w-0">
                  <div className="flex items-center gap-2">
                    <div className={`h-2 w-2 shrink-0 rounded-full ${getStatusColor(alert.status)}`} />
                    <span className="truncate text-sm font-medium text-white">
                      {alert.kind === 'mesh_edge'
                        ? `mesh: ${alert.source_location_name || 'unknown'} → ${alert.target_location_name || 'unknown'}`
                        : alert.monitor_name || 'Unknown Monitor'}
                    </span>
                  </div>
                  <div className="mt-1 flex items-center gap-1.5 text-xs text-slate-400">
                    <span className={`capitalize ${getStatusTextColor(alert.status)}`}>{alert.status}</span>
                    <span className="text-slate-500">·</span>
                    <span>{formatTimeAgo(alert.triggered_at)}</span>
                    <span className="text-slate-500">·</span>
                    <span>{alert.failure_count} failure{alert.failure_count !== 1 ? 's' : ''}</span>
                  </div>
                  {alert.last_error && (
                    <p className="mt-1 truncate text-xs text-slate-500" title={alert.last_error}>
                      {alert.last_error}
                    </p>
                  )}
                  {alert.root_cause_monitor_name && alert.root_cause_monitor_id !== alert.monitor_id && (
                    <Link
                      href={`/monitors/${alert.root_cause_monitor_id}`}
                      className={`mt-1.5 inline-flex max-w-full items-center gap-1 truncate rounded-full px-2 py-0.5 text-[11px] font-medium transition-colors ${
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
                      {alert.suppression_reason === 'dependency' ? '🔕 suppressed · caused by' : 'likely caused by'}: {alert.root_cause_monitor_name}
                    </Link>
                  )}
                  {(alert.impacted_count ?? 0) > 0 && (
                    <span
                      className="mt-1.5 ml-1 inline-flex items-center rounded-full bg-amber-500/[0.15] px-2 py-0.5 text-[11px] font-medium text-amber-300"
                      title="Downstream monitors whose notifications this alert stands in for"
                    >
                      affects {alert.impacted_count} monitor{alert.impacted_count === 1 ? '' : 's'}
                    </span>
                  )}
                </div>
                {showActions && alert.status !== 'resolved' && (
                  <div className="flex gap-1">
                    {alert.status === 'active' && (
                      <Button
                        variant="subtle"
                        size="xs"
                        onClick={() => handleAcknowledge(alert.id)}
                        disabled={processingId === alert.id}
                        loading={processingId === alert.id}
                      >
                        {processingId === alert.id ? '…' : 'Ack'}
                      </Button>
                    )}
                    <Button
                      variant="ghost"
                      size="xs"
                      onClick={() => handleResolve(alert.id)}
                      disabled={processingId === alert.id}
                      loading={processingId === alert.id}
                    >
                      {processingId === alert.id ? '…' : 'Resolve'}
                    </Button>
                  </div>
                )}
              </div>
            </div>
          ))}
        </div>
      )}
    </div>
  );
}
