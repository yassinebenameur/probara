'use client';

import { Suspense, useState, useEffect, useCallback, useMemo, useRef } from 'react';
import { usePathname, useRouter, useSearchParams } from 'next/navigation';
import { Alert, AlertBulkResult, AlertStatus } from '@/lib/types';
import { getAlert, getAlerts, getMonitors, getTenantSettings, getTenants } from '@/lib/api';
import { markTriageLabelled, mergeAlertUpdate } from '@/lib/ai-triage';
import { focusTenantSwitch, parseAlertFocus, withoutAlertFocus } from '@/lib/alert-links';
import { emitTenantChange, setSelectedTenantId } from '@/lib/tenant';
import { useSelectedTenantId } from '@/components/providers/TenantProvider';
import { RANGES, RangeId } from '@/lib/format';
import { monitorTypeLabel } from '@/lib/monitor-type-labels';
import { useAlertEvents } from '@/components/alerts/AlertStreamProvider';
import AlertFeed, { type FeedMonitor, type FeedSection } from '@/components/alerts/AlertFeed';
import AlertTimeline from '@/components/alerts/AlertTimeline';
import {
  FEED_PAGE,
  alertInView,
  feedGrain,
  feedItems,
  feedRows,
  mergeLiveAlert,
  type AlertStatusFilter,
} from '@/lib/alert-list';
import {
  buildTimeline,
  occurrences,
  timelineGroupOf,
  timelineNow,
  type TimelineMonitor,
} from '@/lib/alert-timeline';
import AlertBulkBar from '@/components/alerts/AlertBulkBar';
import { useAlertSelection } from '@/components/alerts/useAlertSelection';
import Button from '@/components/ui/Button';
import Callout from '@/components/ui/Callout';
import ChipGroup from '@/components/ui/ChipGroup';
import FilterChip from '@/components/ui/FilterChip';
import LiveStatus from '@/components/ui/LiveStatus';
import Page from '@/components/ui/Page';
import PageHeader from '@/components/ui/PageHeader';
import RangePicker from '@/components/ui/RangePicker';
import { useLiveRefresh } from '@/lib/useLiveRefresh';
import { useNow } from '@/lib/useNow';
import { useRowFlash } from '@/lib/useRowFlash';
import { errorMessage } from '@/lib/errors';

// 'all' is the default: every alert of the range, on its calendar. The other
// chips narrow it to the open queue or to one status.
type StatusFilter = AlertStatusFilter;
const STATUS_FILTERS: { value: StatusFilter; label: string }[] = [
  { value: 'all', label: 'All' },
  { value: 'open', label: 'Open' },
  { value: 'active', label: 'Active' },
  { value: 'acknowledged', label: 'Acknowledged' },
  { value: 'resolved', label: 'Resolved' },
];
const DEFAULT_STATUS: StatusFilter = 'all';

// The range scopes the timeline and the history. The API's `since` keeps
// every alert open at some point after it, and an unresolved alert is inside
// every range, so the open queue never depends on it.
const WINDOWS = ['1h', '6h', '24h', '7d', '30d', '90d', '365d'] as const satisfies readonly RangeId[];
type AlertWindow = (typeof WINDOWS)[number];
const DEFAULT_WINDOW: AlertWindow = '24h';

// The API caps page_size at 100.
const MAX_API_PAGE = 100;
// The page reads the whole open queue (each status up to this many) and the
// newest resolved alerts of the range up to RESOLVED_CAP, so the timeline and
// the list show the same alerts. Past a cap the page says so (and the
// timeline hatches the span it has no alerts for) rather than reading as
// quiet; History's "Load older alerts" raises the resolved cap a step.
const MAX_OPEN = 1000;
const RESOLVED_STEP = 500;
// (1h to a year; an older `?range=all` link falls back to the default.)
// Monitors read for the type badges and the timeline's groups.
const MONITOR_CAP = 2000;

function parseStatus(value: string | null): StatusFilter {
  return STATUS_FILTERS.some((f) => f.value === value) ? (value as StatusFilter) : DEFAULT_STATUS;
}

function parseWindow(value: string | null): AlertWindow {
  return (WINDOWS as readonly string[]).includes(value ?? '') ? (value as AlertWindow) : DEFAULT_WINDOW;
}

// The first `n` alerts of one status, newest first, plus the server total.
async function fetchFirst(
  filter: { status: AlertStatus; since?: string },
  n: number
): Promise<{ items: Alert[]; total: number }> {
  const size = Math.min(MAX_API_PAGE, n);
  const items: Alert[] = [];
  let total = 0;
  for (let page = 1; ; page++) {
    const res = await getAlerts({ ...filter, page, page_size: size });
    const batch = res?.items || [];
    total = res?.total || 0;
    items.push(...batch);
    if (items.length >= n || batch.length < size || items.length >= total) break;
  }
  return { items: items.slice(0, n), total };
}

type MonitorInfo = FeedMonitor & TimelineMonitor;

// Every monitor's type, config and tags, by id. Best effort: without it the
// rows lose their type badge and the timeline groups everything as "Other".
async function fetchMonitors(): Promise<{ byId: Map<string, MonitorInfo>; total: number }> {
  const byId = new Map<string, MonitorInfo>();
  let total = 0;
  for (let page = 1; byId.size < MONITOR_CAP; page++) {
    const res = await getMonitors({ page, page_size: MAX_API_PAGE });
    const batch = res?.items || [];
    total = res?.total || 0;
    batch.forEach((m) => byId.set(m.id, { type: m.type, config: m.config, tags: m.tags }));
    if (batch.length < MAX_API_PAGE || byId.size >= total) break;
  }
  return { byId, total };
}

function AlertsView() {
  const router = useRouter();
  const pathname = usePathname();
  const searchParams = useSearchParams();
  const statusFilter = parseStatus(searchParams.get('status'));
  const windowFilter = parseWindow(searchParams.get('range'));
  const rangeMs = RANGES[windowFilter].ms as number;
  // A notification's deep link (`?alert=&tenant=`, lib/alert-links.ts) shows
  // that one alert, in its own workspace, instead of the list.
  const focus = parseAlertFocus(searchParams);
  const focusAlertId = focus?.alertId ?? null;
  const focusTenantId = focus?.tenantId ?? null;
  const selectedTenantId = useSelectedTenantId();
  const [focused, setFocused] = useState<{ id: string; alert: Alert | null; error: string } | null>(null);
  const [focusedOpen, setFocusedOpen] = useState(true);

  // Every alert the page shows: the open queue and the range's resolved
  // alerts. The timeline, the sections and the counts all derive from it.
  const [alerts, setAlerts] = useState<Alert[]>([]);
  const [error, setError] = useState<string>('');
  // What the last load read: server totals, and how far back the resolved
  // alerts reach when there were more than the cap (null when it read them all).
  const [totals, setTotals] = useState<{ open: number; resolved: number; resolvedRead: number; coveredFrom: number | null }>({
    open: 0,
    resolved: 0,
    resolvedRead: 0,
    coveredFrom: null,
  });
  // The resolved cap belongs to the range it was raised in.
  const [capState, setCapState] = useState({ key: windowFilter as string, cap: RESOLVED_STEP });
  const resolvedCap = capState.key === windowFilter ? capState.cap : RESOLVED_STEP;
  const [counts, setCounts] = useState<{ active: number; acknowledged: number } | null>(null);
  const [monitors, setMonitors] = useState<{ byId: Map<string, MonitorInfo>; total: number }>(() => ({ byId: new Map(), total: 0 }));
  const [groupTags, setGroupTags] = useState<string[]>([]);
  const [expandedId, setExpandedId] = useState<string | null>(null);
  const [unfolded, setUnfolded] = useState<ReadonlySet<string>>(() => new Set());
  // "Show more" belongs to the view it was pressed in: a change of filter or
  // range reads as the first page in the same render.
  const filterKey = `${statusFilter}|${windowFilter}`;
  const [limitState, setLimitState] = useState({ key: filterKey, rows: FEED_PAGE });
  const limit = limitState.key === filterKey ? limitState.rows : FEED_PAGE;
  const requestRef = useRef(0);
  const { subscribe } = useAlertEvents();
  const rowFlash = useRowFlash(alerts, (alert) => alert.id, (alert) => alert.status);
  const alertsRef = useRef(alerts);
  useEffect(() => {
    alertsRef.current = alerts;
  });

  // The window ends now and slides with the clock, in steps of what one
  // pixel of the track spans, so the lines move without re-laying every tick.
  const clock = useNow(rangeMs <= 3_600_000 ? 'second' : 'minute');
  const end = timelineNow(clock, rangeMs);
  const window_ = useMemo(() => ({ start: end - rangeMs, end }), [end, rangeMs]);

  const feed = useMemo(() => feedRows(alerts, statusFilter, window_.start), [alerts, statusFilter, window_.start]);
  const loadedResolved = useMemo(() => alerts.filter((a) => a.status === 'resolved'), [alerts]);
  // More resolved alerts in the range than were read: everything that counts
  // alerts over the range stands down, and the timeline marks the gap. Both
  // come from the load itself, never from the rows: a stream event adds a
  // resolved alert without making the set any more complete.
  const truncated = totals.resolved > totals.resolvedRead;
  const coveredFrom = totals.coveredFrom;
  const timeline = useMemo(
    () =>
      buildTimeline(alerts, window_, (alert) =>
        timelineGroupOf(alert, alert.monitor_id ? monitors.byId.get(alert.monitor_id) : undefined, groupTags, monitorTypeLabel)
      ),
    [alerts, window_, monitors, groupTags]
  );
  const occurrence = useMemo(() => (truncated ? undefined : occurrences(alerts, window_)), [truncated, alerts, window_]);

  const visibleRows = useMemo(() => feed.rows.slice(0, limit), [feed.rows, limit]);
  // Bulk triage selects among the open alerts on screen, in the order shown
  // (a folded alert only once its parent is unfolded): a Shift range or
  // "select all" never reaches a row the operator cannot see.
  const selectableRows = useMemo(
    () =>
      visibleRows
        .flatMap((alert) => [alert, ...(unfolded.has(alert.id) ? feed.children.get(alert.id) ?? [] : [])])
        .filter((alert) => alert.status !== 'resolved'),
    [visibleRows, unfolded, feed.children]
  );
  const selectableIds = useMemo(() => selectableRows.map((a) => a.id), [selectableRows]);
  const selection = useAlertSelection(selectableIds, filterKey);
  const selectedAlerts = useMemo(
    () => selectableRows.filter((a) => selection.selected.has(a.id)),
    [selectableRows, selection.selected]
  );

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

  // Header counts come from the server, not from the rows read.
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
      try {
        if (!silent) setError('');
        const since = new Date(Date.now() - rangeMs).toISOString();
        // No multi-status filter in the API: read each status.
        const [active, acknowledged, resolved] = await Promise.all([
          fetchFirst({ status: 'active' }, MAX_OPEN),
          fetchFirst({ status: 'acknowledged' }, MAX_OPEN),
          fetchFirst({ status: 'resolved', since }, resolvedCap),
        ]);
        if (request !== requestRef.current) return true;
        // An alert that changed status between the three reads is in two of
        // them: the later read wins.
        const byId = new Map<string, Alert>();
        [...active.items, ...acknowledged.items, ...resolved.items].forEach((a) => byId.set(a.id, a));
        setAlerts([...byId.values()]);
        setCounts({ active: active.total, acknowledged: acknowledged.total });
        setTotals({
          open: active.total + acknowledged.total,
          resolved: resolved.total,
          resolvedRead: resolved.items.length,
          coveredFrom:
            resolved.total > resolved.items.length && resolved.items.length > 0
              ? Math.min(...resolved.items.map((a) => Date.parse(a.triggered_at)))
              : null,
        });
        setError('');
        return true;
      } catch (err) {
        if (request !== requestRef.current) return true;
        if (!silent) setError(errorMessage(err, 'Failed to load alerts'));
        return false;
      }
    },
    [rangeMs, focusAlertId, resolvedCap]
  );

  // Stream events update rows in place (below), so they don't reload; the
  // poll and the reconnect resync catch anything the stream missed.
  const live = useLiveRefresh(loadAlerts, { streamDebounceMs: 0 });

  // Monitor types, tags and the dashboard's group tags, once per mount (the
  // page remounts on a tenant switch).
  useEffect(() => {
    let cancelled = false;
    fetchMonitors()
      .then((result) => !cancelled && setMonitors(result))
      .catch(() => undefined);
    getTenantSettings()
      .then((settings) => !cancelled && setGroupTags(settings.dashboard_group_tags || []))
      .catch(() => undefined);
    return () => {
      cancelled = true;
    };
  }, []);

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

  // Live updates: an alert already read changes in place (mergeLiveAlert
  // keeps what the event never carries) and flashes when its status changes
  // (useRowFlash); a new one joins the set, takes its place in the feed, and
  // flashes.
  const { flash } = rowFlash;
  const handleLiveAlert = useCallback(
    (alert: Alert) => {
      const known = alertsRef.current.some((a) => a.id === alert.id);
      setAlerts((prev) =>
        prev.some((a) => a.id === alert.id)
          ? prev.map((a) => (a.id === alert.id ? mergeLiveAlert(a, alert) : a))
          : [...prev, mergeLiveAlert(undefined, alert)]
      );
      // After the row mounts, so the animation starts on screen.
      if (!known) window.requestAnimationFrame(() => flash(alert.id));
    },
    [flash]
  );

  useEffect(() => {
    return subscribe((event) => {
      handleLiveAlert(event.alert);
      setFocused((f) =>
        f?.alert && f.alert.id === event.alert.id ? { ...f, alert: mergeLiveAlert(f.alert, event.alert) } : f
      );
      void loadCounts();
    });
  }, [handleLiveAlert, subscribe, loadCounts]);

  // The user's own acknowledge/resolve returns the whole alert: the row
  // changes in place and flashes (no success toast: the change is right there).
  const handleOwnUpdate = (alert: Alert) => {
    setAlerts((prev) => prev.map((a) => (a.id === alert.id ? mergeAlertUpdate(a, alert) : a)));
    void loadCounts();
  };

  // A bulk action's changed alerts, in place like a single action's. Skipped
  // ones changed elsewhere meanwhile (already resolved, acknowledged, gone):
  // a silent reload shows their real state instead of the stale row.
  const handleBulkApplied = (result: AlertBulkResult) => {
    const changed = new Map(result.items.map((a) => [a.id, a]));
    setAlerts((prev) => prev.map((a) => (changed.has(a.id) ? mergeAlertUpdate(a, changed.get(a.id)!) : a)));
    if (result.failed.length > 0) void loadAlerts({ silent: true });
    else void loadCounts();
  };

  const handleTriageLabelled = (alertId: string) => {
    setAlerts((prev) => markTriageLabelled(prev, alertId));
  };

  const toggleFold = (alertId: string) =>
    setUnfolded((prev) => {
      const next = new Set(prev);
      if (next.has(alertId)) next.delete(alertId);
      else next.add(alertId);
      return next;
    });

  // A lane chosen on the timeline opens that alert in the list: whatever
  // hides its row gives way (the status filter, "Show more", a fold), then
  // the row scrolls into view once it has rendered.
  const [reveal, setReveal] = useState<string | null>(null);
  const selectFromTimeline = (alertId: string) => {
    const alert = alerts.find((a) => a.id === alertId);
    if (!alert) return;
    // A folded alert is listed under its parent wherever folding applies.
    const hidden = !alertInView(alert, statusFilter);
    const view = hidden ? DEFAULT_STATUS : statusFilter;
    if (hidden) setFilters({ status: view });
    const target = hidden ? feedRows(alerts, view, window_.start) : feed;
    const parent = [...target.children.entries()].find(([, kids]) => kids.some((kid) => kid.id === alertId))?.[0];
    if (parent) setUnfolded((prev) => new Set(prev).add(parent));
    setLimitState({
      key: `${view}|${windowFilter}`,
      rows: Math.max(hidden ? FEED_PAGE : limit, target.rows.findIndex((a) => a.id === (parent ?? alertId)) + 1),
    });
    setExpandedId(alertId);
    setReveal(alertId);
  };
  useEffect(() => {
    if (!reveal) return;
    const frame = window.requestAnimationFrame(() => {
      // Not rendered yet when the filter had to change first (the URL
      // settles a render later): this runs again once it has.
      const row = document.getElementById(`alert-${reveal}`);
      if (!row) return;
      row.scrollIntoView({ block: 'center' });
      setReveal(null);
    });
    return () => window.cancelAnimationFrame(frame);
  }, [reveal, limit, statusFilter, unfolded]);

  const chipCount = (value: StatusFilter): number | undefined => {
    if (!counts) return undefined;
    if (value === 'open') return counts.active + counts.acknowledged;
    if (value === 'active') return counts.active;
    if (value === 'acknowledged') return counts.acknowledged;
    return undefined;
  };

  if (focusAlertId) {
    const current = focused?.id === focusAlertId ? focused : null;
    const section: FeedSection = { key: 'focus', alerts: current?.alert ? [current.alert] : [], empty: 'Alert not found' };
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
        <AlertFeed
          sections={[section]}
          monitors={monitors.byId}
          expandedId={focusedOpen ? focusAlertId : null}
          onExpand={(id) => setFocusedOpen(id !== null)}
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

  const rangeLong = RANGES[windowFilter].long.toLowerCase();
  const loadedOpen = alerts.length - loadedResolved.length;
  // The view lists resolved alerts, and the load did not read them all.
  const olderUnread = truncated && (statusFilter === 'all' || statusFilter === 'resolved');
  const allShown = visibleRows.length === feed.rows.length;
  const grain = feedGrain(rangeMs);
  const notes = [
    olderUnread && `The newest ${totals.resolvedRead} of ${totals.resolved} resolved alerts in this range are loaded.`,
    statusFilter !== 'resolved' && totals.open > loadedOpen && `The newest ${loadedOpen} of ${totals.open} open alerts are loaded.`,
  ].filter(Boolean);
  const sections: FeedSection[] = [
    {
      key: 'feed',
      title: `${STATUS_FILTERS.find((f) => f.value === statusFilter)?.label ?? 'All'} alerts`,
      caption: `By start time, newest first · ${rangeLong}`,
      alerts: visibleRows,
      // Every hour or date of the range, empty ones included; it stops at
      // the last row while more are still to come.
      items: feedItems(visibleRows, window_, grain, allShown && !olderUnread),
      grain,
      // First the rows already read, then older ones from the server. Either
      // way it also loads as the end of the list scrolls into view.
      more: !allShown
        ? {
            auto: true,
            label: `Show ${Math.min(FEED_PAGE, feed.rows.length - visibleRows.length)} more`,
            onMore: () => setLimitState({ key: filterKey, rows: limit + FEED_PAGE }),
          }
        : olderUnread
          ? {
              auto: true,
              label: 'Load older alerts',
              onMore: () => {
                setCapState({ key: windowFilter, cap: resolvedCap + RESOLVED_STEP });
                setLimitState({ key: filterKey, rows: limit + FEED_PAGE });
              },
            }
          : undefined,
      note: notes.length ? notes.join(' ') : undefined,
      empty: 'No alerts',
      selectable: true,
    },
  ];

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
        <RangePicker ranges={WINDOWS} value={windowFilter} onChange={(range) => setFilters({ range })} />
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

      {live.updatedAt !== null && (
        // Dimmed while a range's alerts load: until they land, the new window
        // would draw the old range's alerts and read as quiet.
        <div className={`transition-opacity duration-base ${live.loading ? 'opacity-60 delay-300' : 'delay-0'}`}>
        <AlertTimeline
          timeline={timeline}
          window={window_}
          rangeLabel={rangeLong}
          selectedId={expandedId}
          onSelect={selectFromTimeline}
          monitorCount={truncated ? undefined : monitors.total}
          coveredFrom={coveredFrom}
        />
        </div>
      )}

      <AlertFeed
        sections={sections}
        folded={feed.children}
        unfolded={unfolded}
        onToggleFold={toggleFold}
        monitors={monitors.byId}
        occurrence={occurrence}
        rangeShort={RANGES[windowFilter].short}
        expandedId={expandedId}
        onExpand={setExpandedId}
        onAlertUpdate={handleOwnUpdate}
        onTriageLabelled={handleTriageLabelled}
        loading={live.loading || (live.updatedAt === null && !error)}
        flashClass={rowFlash.flashClass}
        selection={selection}
      />

      <AlertBulkBar selected={selectedAlerts} onApplied={handleBulkApplied} onClear={selection.clear} />
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
