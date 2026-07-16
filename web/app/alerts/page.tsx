'use client';

import { useState, useEffect, useCallback } from 'react';
import { RefreshCw } from 'lucide-react';
import { Alert, AlertStatus } from '@/lib/types';
import { getAlerts } from '@/lib/api';
import { useAlertEvents } from '@/components/alerts/AlertStreamProvider';
import AlertTable from '@/components/alerts/AlertTable';
import Panel from '@/components/ui/Panel';
import Button from '@/components/ui/Button';
import FilterChip from '@/components/ui/FilterChip';
import Pill from '@/components/ui/Pill';
import PageHeader from '@/components/ui/PageHeader';

type TimeFilter = '1h' | '24h' | '7d' | '30d' | 'all';

export default function AlertsPage() {
  const [alerts, setAlerts] = useState<Alert[]>([]);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string>('');
  const [statusFilter, setStatusFilter] = useState<AlertStatus | 'all'>('all');
  const [timeFilter, setTimeFilter] = useState<TimeFilter>('7d');
  const [page, setPage] = useState(1);
  const [totalPages, setTotalPages] = useState(1);
  const pageSize = 20;
  const { subscribe } = useAlertEvents();

  const loadAlerts = useCallback(async () => {
    try {
      setLoading(true);
      setError('');

      const params: any = {
        page,
        page_size: pageSize,
      };

      if (statusFilter !== 'all') {
        params.status = statusFilter;
      }

      if (timeFilter !== 'all') {
        const now = new Date();
        let since: Date;
        switch (timeFilter) {
          case '1h':
            since = new Date(now.getTime() - 60 * 60 * 1000);
            break;
          case '24h':
            since = new Date(now.getTime() - 24 * 60 * 60 * 1000);
            break;
          case '7d':
            since = new Date(now.getTime() - 7 * 24 * 60 * 60 * 1000);
            break;
          case '30d':
            since = new Date(now.getTime() - 30 * 24 * 60 * 60 * 1000);
            break;
          default:
            since = new Date(0);
        }
        params.since = since.toISOString();
      }

      const response = await getAlerts(params);
      setAlerts(response?.items || []);
      setTotalPages(Math.ceil((response?.total || 0) / pageSize));
    } catch (err: any) {
      setError(err.message || 'Failed to load alerts');
      setAlerts([]);
    } finally {
      setLoading(false);
    }
  }, [page, statusFilter, timeFilter]);

  useEffect(() => {
    loadAlerts();
  }, [loadAlerts]);

  // Handle real-time updates
  const handleAlertUpdate = useCallback((alert: Alert, eventType: 'created' | 'acknowledged' | 'resolved') => {
    setAlerts(prev => {
      const exists = prev.find(a => a.id === alert.id);
      if (exists) {
        return prev.map(a => a.id === alert.id ? alert : a);
      }
      // New alert - add to top if it matches filters
      if (statusFilter === 'all' || alert.status === statusFilter) {
        return [alert, ...prev].slice(0, pageSize);
      }
      return prev;
    });
    
  }, [statusFilter]);

  useEffect(() => {
    return subscribe((event) => {
      handleAlertUpdate(event.alert, event.type);
    });
  }, [handleAlertUpdate, subscribe]);

  const handleAlertTableUpdate = (alert: Alert) => {
    setAlerts(prev => prev.map(a => a.id === alert.id ? alert : a));
  };

  const statusOptions: { value: AlertStatus | 'all'; label: string }[] = [
    { value: 'all', label: 'All Statuses' },
    { value: 'active', label: 'Active' },
    { value: 'acknowledged', label: 'Acknowledged' },
    { value: 'resolved', label: 'Resolved' },
  ];

  const timeOptions: { value: TimeFilter; label: string }[] = [
    { value: '1h', label: 'Last Hour' },
    { value: '24h', label: 'Last 24 Hours' },
    { value: '7d', label: 'Last 7 Days' },
    { value: '30d', label: 'Last 30 Days' },
    { value: 'all', label: 'All Time' },
  ];

  const activeCount = alerts.filter(a => a.status === 'active').length;
  const acknowledgedCount = alerts.filter(a => a.status === 'acknowledged').length;

  return (
    <div className="flex flex-col gap-4">
      {/* Header */}
      <PageHeader
        title="Alerts"
        subtitle={
          activeCount > 0 || acknowledgedCount > 0
            ? `${activeCount} active · ${acknowledgedCount} acknowledged`
            : 'No active alerts'
        }
        action={
          <div className="flex items-center gap-2">
            {activeCount > 0 && <Pill tone="danger" size="sm" dot>{activeCount} active</Pill>}
            {acknowledgedCount > 0 && <Pill tone="warning" size="sm" dot>{acknowledgedCount} acknowledged</Pill>}
            <Button variant="ghost" size="sm" icon={<RefreshCw strokeWidth={1.75} />} onClick={loadAlerts}>
              Refresh
            </Button>
          </div>
        }
      />

      {/* Filters */}
      <div className="flex flex-wrap items-center gap-4">
        <div className="flex items-center gap-2">
          <span className="text-xs text-slate-500">Status:</span>
          <div className="flex flex-wrap gap-1.5">
            {statusOptions.map(opt => (
              <FilterChip
                key={opt.value}
                selected={statusFilter === opt.value}
                onClick={() => { setStatusFilter(opt.value); setPage(1); }}
              >
                {opt.label}
              </FilterChip>
            ))}
          </div>
        </div>

        <div className="flex items-center gap-2">
          <span className="text-xs text-slate-500">Time:</span>
          <div className="flex flex-wrap gap-1.5">
            {timeOptions.map(opt => (
              <FilterChip
                key={opt.value}
                selected={timeFilter === opt.value}
                onClick={() => { setTimeFilter(opt.value); setPage(1); }}
              >
                {opt.label}
              </FilterChip>
            ))}
          </div>
        </div>
      </div>

      {/* Error */}
      {error && (
        <div className="rounded-lg border border-rose-500/20 bg-rose-500/10 px-4 py-3 text-sm text-rose-400">
          {error}
          <button onClick={loadAlerts} className="ml-2 underline hover:no-underline">
            Retry
          </button>
        </div>
      )}

      {/* Alerts Table */}
      <Panel
        title="Alerts"
        subtitle={`${alerts.length} alert${alerts.length !== 1 ? 's' : ''}`}
      >
        <AlertTable 
          alerts={alerts} 
          onAlertUpdate={handleAlertTableUpdate}
          loading={loading}
        />
      </Panel>

      {/* Pagination */}
      {totalPages > 1 && (
        <div className="flex items-center justify-center gap-2">
          <Button
            variant="ghost"
            size="sm"
            onClick={() => setPage(p => Math.max(1, p - 1))}
            disabled={page === 1}
          >
            Previous
          </Button>
          <span className="text-sm text-slate-500">
            Page {page} of {totalPages}
          </span>
          <Button
            variant="ghost"
            size="sm"
            onClick={() => setPage(p => Math.min(totalPages, p + 1))}
            disabled={page === totalPages}
          >
            Next
          </Button>
        </div>
      )}
    </div>
  );
}
