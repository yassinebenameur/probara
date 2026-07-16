'use client';

import { useState, useEffect, useCallback } from 'react';
import Link from 'next/link';
import { Alert, AlertStatus } from '@/lib/types';
import { getRecentAlerts, acknowledgeAlert, resolveAlert } from '@/lib/api';
import { useAlertEvents } from '@/components/alerts/AlertStreamProvider';
import Button from '@/components/ui/Button';
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

function getStatusBgColor(status: AlertStatus): string {
  switch (status) {
    case 'active':
      return 'bg-rose-500/10 text-rose-400 border-rose-500/20';
    case 'acknowledged':
      return 'bg-amber-500/10 text-amber-400 border-amber-500/20';
    case 'resolved':
      return 'bg-emerald-500/10 text-emerald-400 border-emerald-500/20';
    default:
      return 'bg-slate-500/10 text-slate-400 border-slate-500/20';
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
      <div className="rounded-xl border border-white/[0.06] bg-slate-900/50 p-5">
        <div className="mb-4">
          <h3 className="font-medium text-white">Recent Alerts</h3>
          <p className="text-xs text-slate-500">Loading...</p>
        </div>
        <div className="space-y-3">
          {[...Array(3)].map((_, i) => (
            <div key={i} className="h-12 animate-pulse rounded-lg bg-slate-800/50" />
          ))}
        </div>
      </div>
    );
  }

  const activeAlerts = alerts.filter(a => a.status === 'active');
  const activeCount = activeAlerts.length;

  return (
    <div className="rounded-xl border border-white/[0.06] bg-slate-900/50 p-5">
      <div className="mb-4 flex items-center justify-between">
        <div>
          <div className="flex items-center gap-2">
            <h3 className="font-medium text-white">Recent Alerts</h3>
            {activeCount > 0 && (
              <Pill tone="danger" size="xs">{activeCount}</Pill>
            )}
          </div>
          <p className="text-xs text-slate-500">
            {alerts.length === 0 ? 'No alerts' : `${alerts.length} recent alert${alerts.length !== 1 ? 's' : ''}`}
          </p>
        </div>
        <Link href="/alerts" className="text-xs text-cyan-400 hover:text-cyan-300">
          View all -&gt;
        </Link>
      </div>

      {error && (
        <div className="mb-4 rounded-lg border border-rose-500/20 bg-rose-500/10 px-3 py-2 text-sm text-rose-400">
          {error}
          <button onClick={loadAlerts} className="ml-2 underline hover:no-underline">
            Retry
          </button>
        </div>
      )}

      {alerts.length === 0 ? (
        <div className="flex flex-col items-center justify-center py-8 text-center">
          <div className="mb-2 text-xl font-semibold text-slate-500">Alert</div>
          <p className="text-sm text-slate-500">No alerts yet</p>
          <p className="mt-1 text-xs text-slate-600">
            Alerts will appear when monitors fail
          </p>
        </div>
      ) : (
        <div className="space-y-2">
          {alerts.map((alert) => (
            <div
              key={alert.id}
              className={`rounded-lg border p-3 transition-all ${getStatusBgColor(alert.status)}`}
            >
              <div className="flex items-start justify-between gap-2">
                <div className="flex-1 min-w-0">
                  <div className="flex items-center gap-2">
                    <div className={`h-2 w-2 rounded-full ${getStatusColor(alert.status)}`} />
                    <span className="truncate text-sm font-medium text-white">
                      {alert.kind === 'mesh_edge'
                        ? `mesh: ${alert.source_location_name || 'unknown'} → ${alert.target_location_name || 'unknown'}`
                        : alert.monitor_name || 'Unknown Monitor'}
                    </span>
                  </div>
                  <div className="mt-1 flex items-center gap-2 text-xs">
                    <span className="capitalize">{alert.status}</span>
                    <span className="text-slate-600">|</span>
                    <span>{formatTimeAgo(alert.triggered_at)}</span>
                    <span className="text-slate-600">|</span>
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
                      className="mt-1 inline-flex max-w-full items-center gap-1 truncate rounded-full border border-amber-500/20 bg-amber-500/10 px-2 py-0.5 text-[10px] font-medium text-amber-400 hover:border-amber-500/40"
                      title={`Likely caused by ${alert.root_cause_monitor_name}`}
                    >
                      likely caused by: {alert.root_cause_monitor_name}
                    </Link>
                  )}
                </div>
                {showActions && alert.status !== 'resolved' && (
                  <div className="flex gap-1">
                    {alert.status === 'active' && (
                      <Button
                        variant="ghost"
                        size="xs"
                        onClick={() => handleAcknowledge(alert.id)}
                        disabled={processingId === alert.id}
                        loading={processingId === alert.id}
                      >
                        {processingId === alert.id ? '…' : 'Ack'}
                      </Button>
                    )}
                    <Button
                      variant="accent"
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
