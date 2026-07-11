'use client';

import { Fragment, useCallback, useEffect, useMemo, useState } from 'react';
import { ChevronDown, ChevronRight, RefreshCw, ScrollText } from 'lucide-react';
import Panel from '@/components/ui/Panel';
import Button from '@/components/ui/Button';
import Pill from '@/components/ui/Pill';
import PageHeader from '@/components/ui/PageHeader';
import EmptyState from '@/components/ui/EmptyState';
import Select from '@/components/ui/Select';
import { useCurrentUser } from '@/components/providers/CurrentUserProvider';
import { getAuditActions, getAuditLog } from '@/lib/api';
import { formatDateTime } from '@/lib/format';
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

function actorLabel(event: AuditEvent): string {
  if (event.actor_label) return event.actor_label;
  if (event.actor_type === 'api_key') return 'API key';
  if (event.actor_type === 'anonymous') return 'anonymous';
  return event.actor_id ? event.actor_id.slice(0, 8) : '—';
}

export default function AuditLogPage() {
  const { isSuperadmin, role, loading: userLoading } = useCurrentUser();
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

      <Panel title="Events" subtitle="Most recent first.">
        <div className="mb-4 flex flex-wrap items-end gap-3">
          <div className="w-full sm:w-56">
            <label className="mb-1.5 block text-xs font-medium text-slate-400">Action</label>
            <Select value={actionFilter} onChange={(e) => setActionFilter(e.target.value)}>
              <option value="">All actions</option>
              {actions.map((action) => (
                <option key={action} value={action}>
                  {action}
                </option>
              ))}
            </Select>
          </div>
          <div className="w-full sm:w-44">
            <label className="mb-1.5 block text-xs font-medium text-slate-400">Outcome</label>
            <Select value={outcomeFilter} onChange={(e) => setOutcomeFilter(e.target.value)}>
              <option value="">All outcomes</option>
              <option value="success">Success</option>
              <option value="denied">Denied</option>
              <option value="failure">Failure</option>
            </Select>
          </div>
          <div className="w-full sm:w-44">
            <label className="mb-1.5 block text-xs font-medium text-slate-400">Time range</label>
            <Select value={String(rangeHours)} onChange={(e) => setRangeHours(Number(e.target.value))}>
              {RANGE_PRESETS.map((preset) => (
                <option key={preset.label} value={String(preset.hours)}>
                  {preset.label}
                </option>
              ))}
            </Select>
          </div>
        </div>

        {loading ? (
          <p className="text-sm text-slate-500">Loading audit events…</p>
        ) : error ? (
          <div className="space-y-3">
            <p className="text-sm text-rose-400">{error}</p>
            <Button variant="ghost" size="sm" onClick={() => loadEvents(page)}>
              Retry
            </Button>
          </div>
        ) : events.length === 0 ? (
          <EmptyState
            icon={<ScrollText strokeWidth={1.5} />}
            title="No audit events"
            description="Events appear here as users and API keys make changes."
          />
        ) : (
          <div className="space-y-3">
            <div className="overflow-x-auto">
              <table className="min-w-full divide-y divide-white/[0.06]">
                <thead>
                  <tr className="text-left text-xs uppercase tracking-wide text-slate-400">
                    <th className="w-8 px-2 py-2" />
                    <th className="px-3 py-2">Time</th>
                    <th className="px-3 py-2">Actor</th>
                    <th className="px-3 py-2">Action</th>
                    <th className="px-3 py-2">Resource</th>
                    <th className="px-3 py-2">Outcome</th>
                    <th className="px-3 py-2">IP</th>
                  </tr>
                </thead>
                <tbody className="divide-y divide-white/[0.04] text-sm text-slate-200">
                  {events.map((event) => {
                    const expanded = expandedId === event.id;
                    const hasDetails =
                      (event.details && Object.keys(event.details).length > 0) ||
                      event.user_agent;
                    return (
                      <Fragment key={event.id}>
                        <tr
                          className={hasDetails ? 'cursor-pointer hover:bg-white/[0.02]' : undefined}
                          onClick={() => hasDetails && setExpandedId(expanded ? null : event.id)}
                        >
                          <td className="px-2 py-3 text-slate-500">
                            {hasDetails &&
                              (expanded ? (
                                <ChevronDown className="h-3.5 w-3.5" strokeWidth={1.75} />
                              ) : (
                                <ChevronRight className="h-3.5 w-3.5" strokeWidth={1.75} />
                              ))}
                          </td>
                          <td className="whitespace-nowrap px-3 py-3 text-xs text-slate-400">
                            {formatDateTime(event.occurred_at)}
                          </td>
                          <td className="px-3 py-3">
                            <div className="flex items-center gap-2">
                              <span>{actorLabel(event)}</span>
                              <Pill
                                tone={event.actor_type === 'admin_user' ? 'neutral' : 'info'}
                                size="xs"
                              >
                                {event.actor_type === 'admin_user'
                                  ? 'user'
                                  : event.actor_type === 'api_key'
                                    ? 'key'
                                    : 'anon'}
                              </Pill>
                            </div>
                          </td>
                          <td className="px-3 py-3 font-mono text-xs">{event.action}</td>
                          <td className="px-3 py-3 text-xs text-slate-400">
                            {event.resource_type}
                            {event.resource_id && (
                              <span className="font-mono"> {event.resource_id.slice(0, 8)}</span>
                            )}
                          </td>
                          <td className="px-3 py-3">
                            <Pill tone={OUTCOME_TONE[event.outcome] || 'neutral'} size="xs" dot>
                              {event.outcome}
                            </Pill>
                          </td>
                          <td className="px-3 py-3 font-mono text-xs text-slate-400">{event.ip || '—'}</td>
                        </tr>
                        {expanded && (
                          <tr>
                            <td />
                            <td colSpan={6} className="px-3 pb-3">
                              <div className="rounded-lg border border-white/[0.06] bg-slate-900/40 px-3 py-2">
                                {event.user_agent && (
                                  <p className="text-xs text-slate-400">
                                    <span className="text-slate-500">User agent:</span>{' '}
                                    {event.user_agent}
                                  </p>
                                )}
                                {event.details && Object.keys(event.details).length > 0 && (
                                  <pre className="mt-1 overflow-x-auto text-xs text-slate-300">
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

            <div className="flex items-center justify-between text-xs text-slate-400">
              <span>
                Page {page} of {totalPages} ({total} events)
              </span>
              <div className="flex items-center gap-2">
                <Button
                  variant="ghost"
                  size="sm"
                  disabled={page <= 1}
                  onClick={() => loadEvents(page - 1)}
                >
                  Previous
                </Button>
                <Button
                  variant="ghost"
                  size="sm"
                  disabled={page >= totalPages}
                  onClick={() => loadEvents(page + 1)}
                >
                  Next
                </Button>
              </div>
            </div>
          </div>
        )}
      </Panel>
    </div>
  );
}
