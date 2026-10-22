'use client';

import { Fragment, useCallback, useEffect, useMemo, useRef, useState } from 'react';
import { ChevronDown, ChevronRight, RefreshCw, ScrollText } from 'lucide-react';
import Panel from '@/components/ui/Panel';
import Button from '@/components/ui/Button';
import Callout from '@/components/ui/Callout';
import Pill from '@/components/ui/Pill';
import PageHeader from '@/components/ui/PageHeader';
import Page from '@/components/ui/Page';
import { ICON_BUTTON_CLASS, ICON_BUTTON_SM_CLASS } from '@/components/ui/IconButton';
import EmptyState from '@/components/ui/EmptyState';
import Select from '@/components/ui/Select';
import { useCurrentUser } from '@/components/providers/CurrentUserProvider';
import TableSkeleton from '@/components/ui/TableSkeleton';
import SortableHeader, { useUrlSort } from '@/components/ui/SortableHeader';
import { getAuditActions, getAuditLog, getUsers } from '@/lib/api';
import { formatDateTimeFull, formatTimestamp, timezoneLabel } from '@/lib/format';
import { auditActor, auditIp, knownActorLabels } from '@/lib/audit';
import type { AuditEvent, AuditOutcome, AuditSortColumn } from '@/lib/types';

const PAGE_SIZE = 50;

// The log is paged on the server, so its columns sort there (sort/dir, a
// whitelist with newest-first ties); the URL keeps the order (?sort=&dir=).
const AUDIT_SORT_KEYS = ['time', 'action', 'resource', 'outcome'] as const;
type AuditSortKey = (typeof AUDIT_SORT_KEYS)[number];
const AUDIT_SORT_DEFAULT = { key: 'time', dir: 'desc' } as const;
/** Newest first; the text columns start A-Z. */
const AUDIT_SORT_DEFAULT_DIR = { time: 'desc' } as const;
const AUDIT_SORT_COLUMN: Record<AuditSortKey, AuditSortColumn> = {
  time: 'occurred_at',
  action: 'action',
  resource: 'resource',
  outcome: 'outcome',
};

const RANGE_PRESETS = [
  { label: 'Last 24 hours', hours: 24 },
  { label: 'Last 7 days', hours: 24 * 7 },
  { label: 'Last 30 days', hours: 24 * 30 },
  { label: 'All time', hours: 0 },
];

const OUTCOME_TONE: Record<AuditOutcome, 'success' | 'warning' | 'danger'> = {
  success: 'success',
  denied: 'warning',
  failure: 'danger',
};

export default function AuditLogPage() {
  const { isSuperadmin, role, user, loading: userLoading } = useCurrentUser();
  // Usernames by id, for events stored without an actor label: every user a
  // superadmin can list, plus the signed-in user. Names seen on loaded events
  // are added in actorNames below.
  const [userNames, setUserNames] = useState<Map<string, string>>(() => new Map());
  const [events, setEvents] = useState<AuditEvent[]>([]);
  const [actions, setActions] = useState<string[]>([]);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState('');
  const [page, setPage] = useState(1);
  const [total, setTotal] = useState(0);
  const [actionFilter, setActionFilter] = useState('');
  const [outcomeFilter, setOutcomeFilter] = useState('');
  const [rangeHours, setRangeHours] = useState(24 * 7);
  const [expandedId, setExpandedId] = useState<number | null>(null);
  const [sort, setSort] = useUrlSort<AuditSortKey>(AUDIT_SORT_KEYS, AUDIT_SORT_DEFAULT, AUDIT_SORT_DEFAULT_DIR);
  // Primitives, so re-reading the same sort from the URL never refetches.
  const { key: sortKey, dir: sortDir } = sort;
  // Rows on screen: a failed refresh keeps them (with the error above).
  const [loaded, setLoaded] = useState(false);
  // Only the latest load may land: a slow response for an earlier sort or
  // filter must not overwrite a newer one.
  const requestRef = useRef(0);

  const totalPages = useMemo(() => Math.max(1, Math.ceil(total / PAGE_SIZE)), [total]);
  const allowed = isSuperadmin || role === 'admin';

  const loadEvents = useCallback(
    async (targetPage: number) => {
      const request = ++requestRef.current;
      try {
        setLoading(true);
        setError('');
        const from =
          rangeHours > 0
            ? new Date(Date.now() - rangeHours * 60 * 60 * 1000).toISOString()
            : undefined;
        const response = await getAuditLog({
          action: actionFilter || undefined,
          outcome: outcomeFilter || undefined,
          from,
          page: targetPage,
          page_size: PAGE_SIZE,
          sort: AUDIT_SORT_COLUMN[sortKey],
          dir: sortDir,
        });
        if (request !== requestRef.current) return;
        setEvents(response.items || []);
        setTotal(response.total || 0);
        setPage(response.page || targetPage);
        setLoaded(true);
      } catch (err: any) {
        if (request !== requestRef.current) return;
        setError(err.message || 'Failed to load audit log');
      } finally {
        if (request === requestRef.current) setLoading(false);
      }
    },
    [actionFilter, outcomeFilter, rangeHours, sortKey, sortDir]
  );

  useEffect(() => {
    if (userLoading || !isSuperadmin) return;
    let cancelled = false;
    getUsers({ page: 1, page_size: 200 })
      .then((response) => {
        if (cancelled) return;
        setUserNames(new Map((response.items || []).map((u) => [u.id, u.username])));
      })
      .catch(() => {
        // Best effort: without the list, unlabeled actors read "User <id>".
      });
    return () => {
      cancelled = true;
    };
  }, [userLoading, isSuperadmin]);

  const actorNames = useMemo(() => {
    const names = new Map(userNames);
    if (user?.id && user.username) names.set(user.id, user.username);
    knownActorLabels(events).forEach((label, id) => names.set(id, label));
    return names;
  }, [userNames, user, events]);

  useEffect(() => {
    if (userLoading || !allowed) return;
    loadEvents(1);
  }, [userLoading, allowed, loadEvents]);

  // The action filter's options: once per visit, not on every filter or sort.
  useEffect(() => {
    if (userLoading || !allowed) return;
    let cancelled = false;
    getAuditActions()
      .then((response) => !cancelled && setActions(response.actions || []))
      .catch(() => !cancelled && setActions([]));
    return () => {
      cancelled = true;
    };
  }, [userLoading, allowed]);

  const header = (
    <PageHeader
      title="Audit log"
      subtitle="Who did what, when — mutations, sign-ins and denied attempts."
      action={
        allowed ? (
          <Button
            variant="ghost"
            size="sm"
            icon={<RefreshCw strokeWidth={1.75} />}
            onClick={() => loadEvents(page)}
            loading={loading && loaded}
          >
            Refresh
          </Button>
        ) : undefined
      }
    />
  );

  if (!userLoading && !allowed) {
    return (
      <Page width="data">
        {header}
        <Panel title="Tenant admin required" subtitle="The audit log is visible to tenant admins and superadmins.">
          <p className="text-sm text-slate-300">
            Your current role does not include audit access.
          </p>
        </Panel>
      </Page>
    );
  }

  return (
    <Page width="data">
      {header}

      <div className="flex flex-wrap items-end gap-3">
        <div className="w-full sm:w-56">
          <Select label="Action" value={actionFilter} onChange={(e) => setActionFilter(e.target.value)}>
            <option value="">All actions</option>
            {actions.map((action) => (
              <option key={action} value={action}>
                {action}
              </option>
            ))}
          </Select>
        </div>
        <div className="w-full sm:w-44">
          <Select label="Outcome" value={outcomeFilter} onChange={(e) => setOutcomeFilter(e.target.value)}>
            <option value="">All outcomes</option>
            <option value="success">Success</option>
            <option value="denied">Denied</option>
            <option value="failure">Failure</option>
          </Select>
        </div>
        <div className="w-full sm:w-44">
          <Select label="Time range" value={String(rangeHours)} onChange={(e) => setRangeHours(Number(e.target.value))}>
            {RANGE_PRESETS.map((preset) => (
              <option key={preset.label} value={String(preset.hours)}>
                {preset.label}
              </option>
            ))}
          </Select>
        </div>
      </div>

      {error && (
        <Callout
          tone="danger"
          action={
            <Button variant="ghost" size="xs" onClick={() => loadEvents(page)} loading={loading}>
              Retry
            </Button>
          }
        >
          {error}
        </Callout>
      )}

      {!loaded ? (
        loading ? <TableSkeleton rows={8} columns={4} label="Loading audit events" /> : null
      ) : events.length === 0 ? (
        <EmptyState
          icon={<ScrollText strokeWidth={1.5} />}
          title="No audit events"
          description="Events appear here as users and API keys make changes."
        />
      ) : (
        // The dim waits 300ms, so a quick refresh never blinks the table.
        <div
          className={`glass overflow-hidden transition-opacity duration-base ${
            loading ? 'opacity-60 delay-300' : 'delay-0'
          }`}
          aria-busy={loading || undefined}
        >
          <div className="overflow-x-auto">
            <table className="data-table text-sm">
              <thead>
                <tr>
                  <th className="w-8 !pr-0">
                    <span className="sr-only">Details</span>
                  </th>
                  {/* Sorted on the server; ties read newest first. */}
                  <SortableHeader
                    label={`Time (${timezoneLabel()})`}
                    sortKey="time"
                    sort={sort}
                    onSort={setSort}
                    defaultDir={AUDIT_SORT_DEFAULT_DIR}
                    title={sortKey === 'time' && sortDir === 'asc' ? 'Oldest first' : 'Newest first'}
                  />
                  <th>Actor</th>
                  <SortableHeader label="Action" sortKey="action" sort={sort} onSort={setSort} defaultDir={AUDIT_SORT_DEFAULT_DIR} />
                  <SortableHeader label="Resource" sortKey="resource" sort={sort} onSort={setSort} defaultDir={AUDIT_SORT_DEFAULT_DIR} />
                  <SortableHeader label="Outcome" sortKey="outcome" sort={sort} onSort={setSort} defaultDir={AUDIT_SORT_DEFAULT_DIR} />
                  <th>IP</th>
                </tr>
              </thead>
              <tbody>
                {events.map((event) => {
                  const expanded = expandedId === event.id;
                  const hasDetails =
                    (event.details && Object.keys(event.details).length > 0) ||
                    event.user_agent;
                  const actor = auditActor(event, actorNames);
                  const ip = auditIp(event.ip);
                  const detailsId = `audit-${event.id}-details`;
                  const toggle = () => setExpandedId(expanded ? null : event.id);
                  return (
                    <Fragment key={event.id}>
                      <tr
                        className={`${hasDetails ? 'cursor-pointer' : ''} ${expanded ? '[&>td]:!border-b-0' : ''}`}
                        // Pointer convenience; the chevron button is the
                        // keyboard and screen-reader control.
                        onClick={() => hasDetails && toggle()}
                      >
                        <td className="!py-1 !pl-2 !pr-0 text-slate-500">
                          {hasDetails && (
                            <button
                              type="button"
                              aria-expanded={expanded}
                              aria-controls={expanded ? detailsId : undefined}
                              aria-label={`${expanded ? 'Hide' : 'Show'} details for ${event.action}`}
                              className={`${ICON_BUTTON_CLASS} ${ICON_BUTTON_SM_CLASS}`}
                              onClick={(e) => {
                                e.stopPropagation();
                                toggle();
                              }}
                            >
                              {expanded ? (
                                <ChevronDown strokeWidth={1.75} aria-hidden="true" />
                              ) : (
                                <ChevronRight strokeWidth={1.75} aria-hidden="true" />
                              )}
                            </button>
                          )}
                        </td>
                        <td
                          className="whitespace-nowrap font-mono text-xs tabular-nums text-slate-400"
                          title={formatDateTimeFull(event.occurred_at)}
                        >
                          {formatTimestamp(event.occurred_at)}
                        </td>
                        <td>
                          <div className="flex items-center gap-2">
                            <span className="text-slate-200" title={actor.title}>
                              {actor.label}
                            </span>
                            {/* Users are the common case; only mark keys and anonymous calls. */}
                            {actor.kind !== 'user' && (
                              <Pill tone="neutral" size="xs">
                                {actor.kind === 'key' ? 'key' : 'anon'}
                              </Pill>
                            )}
                          </div>
                        </td>
                        <td className="font-mono text-xs text-slate-200">{event.action}</td>
                        <td className="text-ui text-slate-400">
                          {event.resource_type}
                          {event.resource_id && (
                            <span className="font-mono text-xs"> {event.resource_id.slice(0, 8)}</span>
                          )}
                        </td>
                        <td>
                          {event.outcome === 'success' ? (
                            <span className="text-ui text-slate-500">success</span>
                          ) : (
                            <Pill tone={OUTCOME_TONE[event.outcome] || 'neutral'} size="xs" dot>
                              {event.outcome}
                            </Pill>
                          )}
                        </td>
                        <td
                          className="font-mono text-xs text-slate-400"
                          title={ip && ip !== event.ip ? event.ip : undefined}
                        >
                          {ip || '—'}
                        </td>
                      </tr>
                      {expanded && (
                        <tr id={detailsId}>
                          <td className="!pr-0" />
                          <td colSpan={6} className="!pt-0">
                            <div className="glass-well px-3 py-2">
                              {event.user_agent && (
                                <p className="text-xs text-slate-400">
                                  <span className="text-slate-500">User agent:</span>{' '}
                                  {event.user_agent}
                                </p>
                              )}
                              {event.details && Object.keys(event.details).length > 0 && (
                                <pre className="mt-1 overflow-x-auto font-mono text-xs text-slate-300">
                                  {JSON.stringify(event.details, null, 2)}
                                </pre>
                              )}
                            </div>
                          </td>
                        </tr>
                      )}
                    </Fragment>
                  );
                })}
              </tbody>
            </table>
          </div>

          <div className="flex items-center justify-between gap-3 border-t border-divider px-4 py-3 text-xs text-slate-500">
            <span className="tabular-nums">
              Page {page} of {totalPages} ({total} events)
            </span>
            <div className="flex items-center gap-2">
              <Button
                variant="ghost"
                size="xs"
                disabled={page <= 1}
                onClick={() => loadEvents(page - 1)}
              >
                Previous
              </Button>
              <Button
                variant="ghost"
                size="xs"
                disabled={page >= totalPages}
                onClick={() => loadEvents(page + 1)}
              >
                Next
              </Button>
            </div>
          </div>
        </div>
      )}
    </Page>
  );
}
