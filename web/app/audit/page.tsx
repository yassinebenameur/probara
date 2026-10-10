'use client';

import { Fragment, useCallback, useEffect, useMemo, useState } from 'react';
import { ChevronDown, ChevronRight, RefreshCw, ScrollText } from 'lucide-react';
import Panel from '@/components/ui/Panel';
import Button from '@/components/ui/Button';
import Callout from '@/components/ui/Callout';
import Pill from '@/components/ui/Pill';
import PageHeader from '@/components/ui/PageHeader';
import EmptyState from '@/components/ui/EmptyState';
import Select from '@/components/ui/Select';
import { useCurrentUser } from '@/components/providers/CurrentUserProvider';
import TableSkeleton from '@/components/ui/TableSkeleton';
import { getAuditActions, getAuditLog, getUsers } from '@/lib/api';
import { formatDateTimeFull, formatTimestamp, timezoneLabel } from '@/lib/format';
import { auditActor, auditIp, knownActorLabels } from '@/lib/audit';
import type { AuditEvent, AuditOutcome } from '@/lib/types';

const PAGE_SIZE = 50;

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

  const totalPages = useMemo(() => Math.max(1, Math.ceil(total / PAGE_SIZE)), [total]);
  const allowed = isSuperadmin || role === 'admin';

  const loadEvents = useCallback(
    async (targetPage: number) => {
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
        });
        setEvents(response.items || []);
        setTotal(response.total || 0);
        setPage(response.page || targetPage);
      } catch (err: any) {
        setError(err.message || 'Failed to load audit log');
        setEvents([]);
        setTotal(0);
      } finally {
        setLoading(false);
      }
    },
    [actionFilter, outcomeFilter, rangeHours]
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
    getAuditActions()
      .then((response) => setActions(response.actions || []))
      .catch(() => setActions([]));
  }, [userLoading, allowed, loadEvents]);

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
          >
            Refresh
          </Button>
        ) : undefined
      }
    />
  );

  if (!userLoading && !allowed) {
    return (
      <div className="space-y-6">
        {header}
        <Panel title="Tenant admin required" subtitle="The audit log is visible to tenant admins and superadmins.">
          <p className="text-sm text-slate-300">
            Your current role does not include audit access.
          </p>
        </Panel>
      </div>
    );
  }

  return (
    <div className="space-y-6">
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

      {loading && events.length === 0 ? (
        <TableSkeleton rows={8} columns={4} label="Loading audit events" />
      ) : error ? (
        <Callout
          tone="danger"
          action={
            <Button variant="ghost" size="xs" onClick={() => loadEvents(page)}>
              Retry
            </Button>
          }
        >
          {error}
        </Callout>
      ) : events.length === 0 ? (
        <EmptyState
          icon={<ScrollText strokeWidth={1.5} />}
          title="No audit events"
          description="Events appear here as users and API keys make changes."
        />
      ) : (
        <div
          className={`glass overflow-hidden transition-opacity ${loading ? 'opacity-60' : ''}`}
          aria-busy={loading}
        >
          <div className="overflow-x-auto">
            <table className="data-table text-sm">
              <thead>
                <tr>
                  <th className="w-8 !pr-0" />
                  <th>Time ({timezoneLabel()})</th>
                  <th>Actor</th>
                  <th>Action</th>
                  <th>Resource</th>
                  <th>Outcome</th>
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
                  return (
                    <Fragment key={event.id}>
                      <tr
                        className={`${hasDetails ? 'cursor-pointer' : ''} ${expanded ? '[&>td]:!border-b-0' : ''}`}
                        onClick={() => hasDetails && setExpandedId(expanded ? null : event.id)}
                      >
                        <td className="!pr-0 text-slate-500">
                          {hasDetails &&
                            (expanded ? (
                              <ChevronDown className="h-3.5 w-3.5" strokeWidth={1.75} />
                            ) : (
                              <ChevronRight className="h-3.5 w-3.5" strokeWidth={1.75} />
                            ))}
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
                        <tr>
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
    </div>
  );
}
