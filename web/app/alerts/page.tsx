'use client';

import { Suspense, useState, useEffect, useCallback, useRef } from 'react';
import { usePathname, useRouter, useSearchParams } from 'next/navigation';
import { RefreshCw } from 'lucide-react';
import { Alert, AlertStatus } from '@/lib/types';
import { getAlert, getAlerts, getTenants } from '@/lib/api';
import { markTriageLabelled, mergeAlertUpdate } from '@/lib/ai-triage';
import { focusTenantSwitch, parseAlertFocus, withoutAlertFocus } from '@/lib/alert-links';
import { emitTenantChange, setSelectedTenantId } from '@/lib/tenant';
import { useSelectedTenantId } from '@/components/providers/TenantProvider';
import { RANGES, RangeId, rangeStart } from '@/lib/format';
import { useAlertEvents } from '@/components/alerts/AlertStreamProvider';
import AlertTable from '@/components/alerts/AlertTable';
import Button from '@/components/ui/Button';
import Callout from '@/components/ui/Callout';
import ChipGroup from '@/components/ui/ChipGroup';
import FilterChip from '@/components/ui/FilterChip';
import IconButton from '@/components/ui/IconButton';
import PageHeader from '@/components/ui/PageHeader';
import Select from '@/components/ui/Select';

// 'open' (active + acknowledged) is the default: the queue an operator works.
type StatusFilter = 'open' | AlertStatus | 'all';
const STATUS_FILTERS: { value: StatusFilter; label: string }[] = [
  { value: 'open', label: 'Open' },
  { value: 'active', label: 'Active' },
  { value: 'acknowledged', label: 'Acknowledged' },
  { value: 'resolved', label: 'Resolved' },
  { value: 'all', label: 'All' },
];
const DEFAULT_STATUS: StatusFilter = 'open';

// The API's `since` keeps every alert open at some point after it, so the
// window reads "open during", and it only narrows resolved alerts: an
// unresolved alert is inside every window.
const WINDOWS = ['1h', '24h', '7d', '30d', 'all'] as const satisfies readonly RangeId[];
type AlertWindow = (typeof WINDOWS)[number];
const DEFAULT_WINDOW: AlertWindow = '7d';

const PAGE_SIZE = 20;
// The API caps page_size at 100.
const MAX_API_PAGE = 100;

function parseStatus(value: string | null): StatusFilter {
  return STATUS_FILTERS.some((f) => f.value === value) ? (value as StatusFilter) : DEFAULT_STATUS;
}

function parseWindow(value: string | null): AlertWindow {
  return (WINDOWS as readonly string[]).includes(value ?? '') ? (value as AlertWindow) : DEFAULT_WINDOW;
}

// The first `n` alerts of one status, newest first, plus the server total.
async function fetchFirst(status: AlertStatus, n: number): Promise<{ items: Alert[]; total: number }> {
  const size = Math.min(MAX_API_PAGE, n);
  const items: Alert[] = [];
  let total = 0;
  for (let page = 1; ; page++) {
    const res = await getAlerts({ status, page, page_size: size });
    const batch = res?.items || [];
    total = res?.total || 0;
    items.push(...batch);
    if (items.length >= n || batch.length < size || items.length >= total) break;
  }
  return { items: items.slice(0, n), total };
}

function AlertsView() {
  const router = useRouter();
  const pathname = usePathname();
  const searchParams = useSearchParams();
  const statusFilter = parseStatus(searchParams.get('status'));
  const windowFilter = parseWindow(searchParams.get('range'));
  // A notification's deep link (`?alert=&tenant=`, lib/alert-links.ts) shows
  // that one alert, in its own workspace, instead of the list.
  const focus = parseAlertFocus(searchParams);
  const focusAlertId = focus?.alertId ?? null;
  const focusTenantId = focus?.tenantId ?? null;
  const selectedTenantId = useSelectedTenantId();
  const [focused, setFocused] = useState<{ id: string; alert: Alert | null; error: string } | null>(null);

  const [alerts, setAlerts] = useState<Alert[]>([]);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string>('');
  // The page belongs to the filters it was chosen under: any filter change
  // (a chip, a sidebar link) reads as page 1 in the same render, so no
  // request ever goes out for the old page under the new filter.
  const filterKey = `${statusFilter}|${windowFilter}`;
  const [pageState, setPageState] = useState({ key: filterKey, page: 1 });
  const page = pageState.key === filterKey ? pageState.page : 1;
  const setPage = (update: (current: number) => number) =>
    setPageState({ key: filterKey, page: update(page) });
  const [total, setTotal] = useState(0);
  const [counts, setCounts] = useState<{ active: number; acknowledged: number } | null>(null);
  const requestRef = useRef(0);
  const { subscribe } = useAlertEvents();

  const totalPages = Math.max(1, Math.ceil(total / PAGE_SIZE));

  // Filters live in the URL (the sidebar links to ?status=active), so a
  // change replaces the query and the page re-renders from it.
  const setFilters = (next: { status?: StatusFilter; range?: AlertWindow }) => {
    const params = new URLSearchParams(searchParams.toString());
    const status = next.status ?? statusFilter;
    const range = next.range ?? windowFilter;
    if (status === DEFAULT_STATUS) params.delete('status');
    else params.set('status', status);
    if (range === DEFAULT_WINDOW) params.delete('range');
    else params.set('range', range);
    const query = params.toString();
    router.replace(query ? `${pathname}?${query}` : pathname, { scroll: false });
  };

  // Header counts come from the server, not from the rows on this page.
  const loadCounts = useCallback(async () => {
    try {
      const [active, acknowledged] = await Promise.all([
        getAlerts({ status: 'active', page_size: 1 }),
        getAlerts({ status: 'acknowledged', page_size: 1 }),
      ]);
      setCounts({ active: active?.total || 0, acknowledged: acknowledged?.total || 0 });
    } catch {
      // The list shows the error; keep the last counts.
    }
  }, []);

  const loadAlerts = useCallback(async () => {
    if (focusAlertId) return;
    const request = ++requestRef.current;
    try {
      setLoading(true);
      setError('');

      let items: Alert[];
      let count: number;
      if (statusFilter === 'open') {
        // No multi-status filter in the API: merge the two lists, unhandled
        // (active) first, each newest first, and page through the result.
        const want = page * PAGE_SIZE;
        const [active, acknowledged] = await Promise.all([
          fetchFirst('active', want),
          fetchFirst('acknowledged', want),
        ]);
        items = [...active.items, ...acknowledged.items].slice((page - 1) * PAGE_SIZE, want);
        count = active.total + acknowledged.total;
      } else {
        const since = rangeStart(windowFilter)?.toISOString();
        const response = await getAlerts({
          page,
          page_size: PAGE_SIZE,
          ...(statusFilter !== 'all' && { status: statusFilter }),
          ...(since && { since }),
        });
        items = response?.items || [];
        count = response?.total || 0;
      }

      if (request !== requestRef.current) return;
      setAlerts(items);
      setTotal(count);
    } catch (err: any) {
      if (request !== requestRef.current) return;
      setError(err.message || 'Failed to load alerts');
      setAlerts([]);
    } finally {
      if (request === requestRef.current) setLoading(false);
    }
  }, [page, statusFilter, windowFilter, focusAlertId]);

  useEffect(() => {
    loadAlerts();
  }, [loadAlerts]);

  // Focused view: switch to the link's workspace first when the viewer can
  // see it (TenantScope then remounts this page under it), else load the
  // alert in the current one.
  useEffect(() => {
    if (!focusAlertId) return;
    let cancelled = false;
    (async () => {
      try {
        if (focusTenantId && selectedTenantId && focusTenantId !== selectedTenantId) {
          const tenants = await getTenants().catch(() => null);
          const target = focusTenantSwitch(
            { alertId: focusAlertId, tenantId: focusTenantId },
            selectedTenantId,
            (tenants?.items ?? []).map((t) => t.id)
          );
          if (target) {
            setSelectedTenantId(target);
            emitTenantChange();
            return;
          }
        }
        const alert = await getAlert(focusAlertId);
        if (!cancelled) setFocused({ id: focusAlertId, alert, error: '' });
      } catch {
        if (!cancelled) {
          setFocused({ id: focusAlertId, alert: null, error: 'This alert is not in the current workspace, or it no longer exists.' });
        }
      }
    })();
    return () => {
      cancelled = true;
    };
  }, [focusAlertId, focusTenantId, selectedTenantId]);

  const clearFocus = () => {
    const query = withoutAlertFocus(new URLSearchParams(searchParams.toString())).toString();
    router.replace(query ? `${pathname}?${query}` : pathname, { scroll: false });
  };

  useEffect(() => {
    loadCounts();
  }, [loadCounts]);

  const refresh = () => {
    loadAlerts();
    loadCounts();
  };

  // Live updates: rows already on screen change in place (so a row does not
  // jump away mid-scan); an alert not listed yet whose new status matches the
  // filter (created, or newly acknowledged/resolved) is added on page 1, so
  // the list keeps up with the chip counts. Stream events never carry triage
  // (core NATS), so a listed row keeps its pill until the next fetch.
  const handleAlertUpdate = useCallback((alert: Alert) => {
    setAlerts(prev => {
      const exists = prev.find(a => a.id === alert.id);
      if (exists) {
        return prev.map(a => a.id === alert.id ? mergeAlertUpdate(a, alert) : a);
      }
      const matches =
        statusFilter === 'all' ||
        alert.status === statusFilter ||
        (statusFilter === 'open' && alert.status !== 'resolved');
      if (page === 1 && matches) {
        return [alert, ...prev].slice(0, PAGE_SIZE);
      }
      return prev;
    });
  }, [statusFilter, page]);

  useEffect(() => {
    return subscribe((event) => {
      handleAlertUpdate(event.alert);
      setFocused((f) =>
        f?.alert && f.alert.id === event.alert.id ? { ...f, alert: mergeAlertUpdate(f.alert, event.alert) } : f
      );
      loadCounts();
    });
  }, [handleAlertUpdate, subscribe, loadCounts]);

  const handleAlertTableUpdate = (alert: Alert) => {
    setAlerts(prev => prev.map(a => a.id === alert.id ? mergeAlertUpdate(a, alert) : a));
    loadCounts();
  };

  const handleTriageLabelled = (alertId: string) => {
    setAlerts(prev => markTriageLabelled(prev, alertId));
  };

  const chipCount = (value: StatusFilter): number | undefined => {
    if (!counts) return undefined;
    if (value === 'open') return counts.active + counts.acknowledged;
    if (value === 'active') return counts.active;
    if (value === 'acknowledged') return counts.acknowledged;
    return undefined;
  };

  const windowApplies = statusFilter === 'resolved' || statusFilter === 'all';

  if (focusAlertId) {
    const current = focused?.id === focusAlertId ? focused : null;
    return (
      <div className="space-y-6">
        <PageHeader title="Alerts" />
        <Callout
          tone="info"
          action={<Button variant="subtle" size="xs" onClick={clearFocus}>Show all alerts</Button>}
        >
          Showing one alert from a notification link.
        </Callout>
        {current?.error && <Callout tone="warning">{current.error}</Callout>}
        <AlertTable
          alerts={current?.alert ? [current.alert] : []}
          onAlertUpdate={(alert) =>
            setFocused((f) => (f?.alert && f.alert.id === alert.id ? { ...f, alert: mergeAlertUpdate(f.alert, alert) } : f))
          }
          onTriageLabelled={(alertId) =>
            setFocused((f) => (f?.alert ? { ...f, alert: markTriageLabelled([f.alert], alertId)[0] } : f))
          }
          loading={!current}
          emptyTitle="Alert not found"
        />
      </div>
    );
  }

  return (
    <div className="space-y-6">
      <PageHeader
        title="Alerts"
        subtitle={
          counts === null
            ? undefined
            : counts.active + counts.acknowledged === 0
              ? 'No open alerts'
              : [`${counts.active} active`, `${counts.acknowledged} acknowledged`]
        }
        action={
          <IconButton
            icon={<RefreshCw strokeWidth={1.75} />}
            label="Refresh"
            onClick={refresh}
            loading={loading && alerts.length > 0}
          />
        }
      />

      <div className="flex flex-wrap items-center justify-between gap-x-6 gap-y-3">
        <ChipGroup ariaLabel="Status">
          {STATUS_FILTERS.map(opt => (
            <FilterChip
              key={opt.value}
              selected={statusFilter === opt.value}
              count={chipCount(opt.value)}
              onClick={() => setFilters({ status: opt.value })}
            >
              {opt.label}
            </FilterChip>
          ))}
        </ChipGroup>

        {windowApplies && (
          <div className="flex items-center gap-2">
            <label htmlFor="alerts-window" className="text-xs font-medium text-slate-400">
              Open during
            </label>
            <Select
              id="alerts-window"
              className="input-xs w-auto rounded-lg py-0 pl-2.5 text-xs"
              value={windowFilter}
              onChange={(e) => setFilters({ range: parseWindow(e.target.value) })}
            >
              {WINDOWS.map(id => (
                <option key={id} value={id}>{RANGES[id].long}</option>
              ))}
            </Select>
          </div>
        )}
      </div>

      {error && (
        <Callout
          tone="danger"
          action={<Button variant="subtle" size="xs" onClick={refresh}>Retry</Button>}
        >
          {error}
        </Callout>
      )}

      <AlertTable
        alerts={alerts}
        onAlertUpdate={handleAlertTableUpdate}
        onTriageLabelled={handleTriageLabelled}
        loading={loading}
        emptyTitle={
          statusFilter === 'open' ? 'No open alerts' : statusFilter === 'active' ? 'No active alerts' : 'No alerts found'
        }
      />

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
          <span className="text-ui tabular-nums text-slate-400">
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

// useSearchParams needs a Suspense boundary for the production prerender.
export default function AlertsPage() {
  return (
    <Suspense>
      <AlertsView />
    </Suspense>
  );
}
