'use client';

import { Suspense, useState, useEffect, useCallback, useMemo, useRef } from 'react';
import { usePathname, useRouter, useSearchParams } from 'next/navigation';
import { Alert, AlertStatus } from '@/lib/types';
import { getAlert, getAlerts, getTenants } from '@/lib/api';
import { markTriageLabelled, mergeAlertUpdate } from '@/lib/ai-triage';
import { focusTenantSwitch, parseAlertFocus, withoutAlertFocus } from '@/lib/alert-links';
import { emitTenantChange, setSelectedTenantId } from '@/lib/tenant';
import { useSelectedTenantId } from '@/components/providers/TenantProvider';
import { RANGES, RangeId, rangeStart } from '@/lib/format';
import { useAlertEvents } from '@/components/alerts/AlertStreamProvider';
import AlertTable, {
  ALERT_SORT_DEFAULT,
  ALERT_SORT_DEFAULT_DIR,
  ALERT_SORT_KEYS,
  sortAlerts,
  type AlertSortKey,
} from '@/components/alerts/AlertTable';
import Button from '@/components/ui/Button';
import Callout from '@/components/ui/Callout';
import ChipGroup from '@/components/ui/ChipGroup';
import FilterChip from '@/components/ui/FilterChip';
import LiveStatus from '@/components/ui/LiveStatus';
import Page from '@/components/ui/Page';
import PageHeader from '@/components/ui/PageHeader';
import Select from '@/components/ui/Select';
import { useUrlSort } from '@/components/ui/SortableHeader';
import type { SortState } from '@/lib/sort';
import { useLiveRefresh } from '@/lib/useLiveRefresh';
import { useRowFlash } from '@/lib/useRowFlash';

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
// The Open view reads the whole queue (each status up to this many) so a
// column sort orders every open alert, not one page. The other views page on
// the server, newest first (the API has no sort parameter).
const MAX_OPEN = 1000;

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

  // Open view: every open alert (sorted and paged here). Other views: the
  // server's current page.
  const [rows, setRows] = useState<Alert[]>([]);
  const [error, setError] = useState<string>('');
  const [sort, setSort] = useUrlSort(ALERT_SORT_KEYS, ALERT_SORT_DEFAULT, ALERT_SORT_DEFAULT_DIR);
  const clientSorted = statusFilter === 'open';
  // The page belongs to the filters (and, in the Open view, the sort) it was
  // chosen under: any change reads as page 1 in the same render, so no
  // request ever goes out for the old page under the new filter.
  const filterKey = `${statusFilter}|${windowFilter}${clientSorted ? `|${sort.key}:${sort.dir}` : ''}`;
  const [pageState, setPageState] = useState({ key: filterKey, page: 1 });
  const page = pageState.key === filterKey ? pageState.page : 1;
  const setPage = (update: (current: number) => number) =>
    setPageState({ key: filterKey, page: update(page) });
  const [serverTotal, setServerTotal] = useState(0);
  const [openTotal, setOpenTotal] = useState(0);
  const [counts, setCounts] = useState<{ active: number; acknowledged: number } | null>(null);
  const requestRef = useRef(0);
  const { subscribe } = useAlertEvents();
  const rowFlash = useRowFlash(rows, (alert) => alert.id, (alert) => alert.status);

  const sorted = useMemo(() => (clientSorted ? sortAlerts(rows, sort) : rows), [rows, sort, clientSorted]);
  const total = clientSorted ? sorted.length : serverTotal;
  // The Open view is read whole, so its page never refetches.
  const serverPage = clientSorted ? 1 : page;
  const rowsRef = useRef(rows);
  useEffect(() => {
    rowsRef.current = rows;
  });
  const totalPages = Math.max(1, Math.ceil(total / PAGE_SIZE));
  const visible = clientSorted ? sorted.slice((page - 1) * PAGE_SIZE, page * PAGE_SIZE) : rows;

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

  // Silent loads (the poll, a stream reconnect) keep the rows and say
  // nothing on failure (LiveStatus shows it); a failed visible load keeps the
  // rows too and shows the error above them.
  const loadAlerts = useCallback(
    async ({ silent }: { silent: boolean }) => {
      if (focusAlertId) return true;
      const request = ++requestRef.current;
      void loadCounts();
      try {
        if (!silent) setError('');
        let items: Alert[];
        if (statusFilter === 'open') {
          // No multi-status filter in the API: read both queues in full.
          const [active, acknowledged] = await Promise.all([
            fetchFirst('active', MAX_OPEN),
            fetchFirst('acknowledged', MAX_OPEN),
          ]);
          items = [...active.items, ...acknowledged.items];
          if (request !== requestRef.current) return true;
          setOpenTotal(active.total + acknowledged.total);
        } else {
          const since = rangeStart(windowFilter)?.toISOString();
          const response = await getAlerts({
            page: serverPage,
            page_size: PAGE_SIZE,
            ...(statusFilter !== 'all' && { status: statusFilter }),
            ...(since && { since }),
          });
          items = response?.items || [];
          if (request !== requestRef.current) return true;
          setServerTotal(response?.total || 0);
        }
        if (request !== requestRef.current) return true;
        setRows(items);
        setError('');
        return true;
      } catch (err: any) {
        if (request !== requestRef.current) return true;
        if (!silent) setError(err.message || 'Failed to load alerts');
        return false;
      }
    },
    [serverPage, statusFilter, windowFilter, focusAlertId, loadCounts]
  );

  // Stream events update rows in place (below), so they don't reload; the
  // poll and the reconnect resync catch anything the stream missed.
  const live = useLiveRefresh(loadAlerts, { streamDebounceMs: 0 });

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
          // The viewer may have left the focused view meanwhile; switching
          // now would remount whatever page they are on under another tenant.
          if (cancelled) return;
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

  // Live updates: rows already on screen change in place (so a row does not
  // jump away mid-scan) and flash when their status changes (useRowFlash);
  // an alert not listed yet whose new status matches the filter (created, or
  // newly acknowledged/resolved) is added and flashed: in the Open view it
  // takes its place in the sort, elsewhere it leads page 1. Stream events
  // never carry triage (core NATS), so a listed row keeps its pill until the
  // next fetch.
  const { flash } = rowFlash;
  const handleAlertUpdate = useCallback(
    (alert: Alert) => {
      if (rowsRef.current.some((a) => a.id === alert.id)) {
        setRows((prev) => prev.map((a) => (a.id === alert.id ? mergeAlertUpdate(a, alert) : a)));
        return;
      }
      const matches =
        statusFilter === 'all' ||
        alert.status === statusFilter ||
        (statusFilter === 'open' && alert.status !== 'resolved');
      if (!matches || (statusFilter !== 'open' && page !== 1)) return;
      setRows((prev) => {
        if (prev.some((a) => a.id === alert.id)) return prev;
        return statusFilter === 'open' ? [...prev, alert] : [alert, ...prev].slice(0, PAGE_SIZE);
      });
      if (statusFilter !== 'open') setServerTotal((n) => n + 1);
      // After the row mounts, so the animation starts on screen.
      window.requestAnimationFrame(() => flash(alert.id));
    },
    [statusFilter, page, flash]
  );

  useEffect(() => {
    return subscribe((event) => {
      handleAlertUpdate(event.alert);
      setFocused((f) =>
        f?.alert && f.alert.id === event.alert.id ? { ...f, alert: mergeAlertUpdate(f.alert, event.alert) } : f
      );
      void loadCounts();
    });
  }, [handleAlertUpdate, subscribe, loadCounts]);

  // The user's own acknowledge/resolve: the row changes in place and flashes
  // (no success toast: the change is right there).
  const handleAlertTableUpdate = (alert: Alert) => {
    setRows((prev) => prev.map((a) => (a.id === alert.id ? mergeAlertUpdate(a, alert) : a)));
    void loadCounts();
  };

  const handleTriageLabelled = (alertId: string) => {
    setRows((prev) => markTriageLabelled(prev, alertId));
  };

  const changeSort = (next: SortState<AlertSortKey>) => setSort(next);

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
      <Page width="data">
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
      </Page>
    );
  }

  return (
    <Page width="data">
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
          <LiveStatus
            updatedAt={live.updatedAt}
            refreshing={live.refreshing}
            failed={live.failed && !error}
            onRefresh={live.refresh}
            refreshLabel="Refresh alerts"
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
          action={
            <Button variant="subtle" size="xs" onClick={live.refresh} loading={live.loading}>
              Retry
            </Button>
          }
        >
          {error}
        </Callout>
      )}

      <AlertTable
        alerts={visible}
        onAlertUpdate={handleAlertTableUpdate}
        onTriageLabelled={handleTriageLabelled}
        // Before the first load lands the table is loading, not empty.
        loading={live.loading || (live.updatedAt === null && !error)}
        flashClass={rowFlash.flashClass}
        {...(clientSorted ? { sort, onSort: changeSort } : {})}
        emptyTitle={
          statusFilter === 'open' ? 'No open alerts' : statusFilter === 'active' ? 'No active alerts' : 'No alerts found'
        }
      />

      {clientSorted && openTotal > rows.length && (
        <p className="text-center text-xs text-slate-500">
          Sorting the newest {rows.length} of {openTotal} open alerts.
        </p>
      )}

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
    </Page>
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
