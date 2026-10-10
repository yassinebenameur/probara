'use client';

import { Suspense, useCallback, useEffect, useMemo, useState } from 'react';
import { usePathname, useRouter, useSearchParams } from 'next/navigation';

import { getIncidents, getUsers } from '@/lib/api';
import type { AdminUser, IncidentDetail } from '@/lib/types';
import IncidentQuickCreateButton from '@/components/incidents/IncidentQuickCreateButton';
import IncidentList, { type IncidentRow } from '@/components/incidents/IncidentList';
import { INCIDENT_SEVERITY_RANK } from '@/lib/status-tones';
import { useToast } from '@/components/ui/ToastProvider';
import PageHeader from '@/components/ui/PageHeader';
import Button from '@/components/ui/Button';
import Callout from '@/components/ui/Callout';
import ChipGroup from '@/components/ui/ChipGroup';
import FilterChip from '@/components/ui/FilterChip';
import TableSkeleton from '@/components/ui/TableSkeleton';

type StateFilter = 'open' | 'resolved' | 'all';
const STATE_FILTERS: { value: StateFilter; label: string }[] = [
  { value: 'open', label: 'Open' },
  { value: 'resolved', label: 'Resolved' },
  { value: 'all', label: 'All' },
];
const DEFAULT_FILTER: StateFilter = 'open';

const PAGE_SIZE = 25;
// The API caps page_size at 100 and has no state filter, so the list is read
// in full (up to MAX_FETCH) and filtered and paged here.
const API_PAGE = 100;
const MAX_FETCH = 1000;

function parseFilter(value: string | null): StateFilter {
  return STATE_FILTERS.some((f) => f.value === value) ? (value as StateFilter) : DEFAULT_FILTER;
}

// Open incidents worst first, then most recently updated; resolved ones
// after them, most recently updated first (the API order).
function triageOrder(a: IncidentRow, b: IncidentRow): number {
  const aOpen = a.state !== 'resolved';
  const bOpen = b.state !== 'resolved';
  if (aOpen !== bOpen) return aOpen ? -1 : 1;
  if (aOpen) {
    const rank = (i: IncidentRow) => (i.severity ? INCIDENT_SEVERITY_RANK[i.severity] ?? 4 : 4);
    const diff = rank(a) - rank(b);
    if (diff !== 0) return diff;
  }
  return Date.parse(b.updated_at) - Date.parse(a.updated_at);
}

function IncidentsView() {
  const router = useRouter();
  const pathname = usePathname();
  const searchParams = useSearchParams();
  const filter = parseFilter(searchParams.get('status'));

  const [incidents, setIncidents] = useState<IncidentRow[]>([]);
  const [totals, setTotals] = useState<{ total: number; open: number } | null>(null);
  const [users, setUsers] = useState<AdminUser[]>([]);
  const [loading, setLoading] = useState(true);
  const [usersLoading, setUsersLoading] = useState(true);
  const [error, setError] = useState('');
  const [usersError, setUsersError] = useState('');
  const [page, setPage] = useState(1);
  const { showToast } = useToast();

  const loadIncidents = useCallback(async () => {
    try {
      setLoading(true);
      setError('');
      const items: IncidentRow[] = [];
      let total = 0;
      let open = 0;
      for (let p = 1; items.length < MAX_FETCH; p++) {
        const response = await getIncidents({ page: p, page_size: API_PAGE });
        const batch = response.items || [];
        total = response.total || 0;
        open = response.open_total || 0;
        items.push(...batch);
        if (batch.length < API_PAGE || items.length >= total) break;
      }
      setIncidents(items);
      setTotals({ total, open });
    } catch (err) {
      setError(err instanceof Error ? err.message : 'Failed to load incidents');
      setIncidents([]);
    } finally {
      setLoading(false);
    }
  }, []);

  const loadUsers = async () => {
    try {
      setUsersLoading(true);
      setUsersError('');
      const response = await getUsers({ page_size: 1000 });
      setUsers(response.items || []);
    } catch (err) {
      setUsersError(err instanceof Error ? err.message : 'Failed to load users');
      setUsers([]);
    } finally {
      setUsersLoading(false);
    }
  };

  useEffect(() => {
    void loadIncidents();
    void loadUsers();
  }, [loadIncidents]);

  useEffect(() => {
    setPage(1);
  }, [filter]);

  const setFilter = (next: StateFilter) => {
    const params = new URLSearchParams(searchParams.toString());
    if (next === DEFAULT_FILTER) params.delete('status');
    else params.set('status', next);
    const query = params.toString();
    router.replace(query ? `${pathname}?${query}` : pathname, { scroll: false });
  };

  const handleCreated = async (_incident: IncidentDetail) => {
    await loadIncidents();
    showToast('Incident created', 'success');
  };

  const filtered = useMemo(
    () =>
      incidents
        .filter((i) => filter === 'all' || (filter === 'open' ? i.state !== 'resolved' : i.state === 'resolved'))
        .sort(triageOrder),
    [incidents, filter]
  );
  const totalPages = Math.max(1, Math.ceil(filtered.length / PAGE_SIZE));
  const visible = filtered.slice((page - 1) * PAGE_SIZE, page * PAGE_SIZE);

  const resolvedTotal = totals ? totals.total - totals.open : 0;
  const chipCount = (value: StateFilter): number | undefined => {
    if (!totals) return undefined;
    if (value === 'open') return totals.open;
    if (value === 'resolved') return resolvedTotal;
    return totals.total;
  };

  return (
    <div className="space-y-6">
      <PageHeader
        title="Incidents"
        subtitle={totals ? [`${totals.open} open`, `${resolvedTotal} resolved`] : undefined}
        action={
          <IncidentQuickCreateButton
            users={users}
            onCreated={handleCreated}
            disabled={usersLoading || Boolean(usersError) || users.length === 0}
          />
        }
      />

      {usersLoading ? null : usersError ? (
        <Callout
          tone="danger"
          action={<Button variant="subtle" size="xs" onClick={loadUsers}>Retry</Button>}
        >
          {usersError}
        </Callout>
      ) : users.length === 0 ? (
        <Callout tone="warning">No users are available to assign as incident owners.</Callout>
      ) : null}

      <ChipGroup ariaLabel="State">
        {STATE_FILTERS.map((opt) => (
          <FilterChip
            key={opt.value}
            selected={filter === opt.value}
            count={chipCount(opt.value)}
            onClick={() => setFilter(opt.value)}
          >
            {opt.label}
          </FilterChip>
        ))}
      </ChipGroup>

      {loading && incidents.length === 0 ? (
        <TableSkeleton rows={4} columns={3} label="Loading incidents" />
      ) : error ? (
        <Callout
          tone="danger"
          action={<Button variant="subtle" size="xs" onClick={loadIncidents}>Retry</Button>}
        >
          {error}
        </Callout>
      ) : (
        <IncidentList
          incidents={visible}
          {...(filter === 'open' && incidents.length > 0 && {
            emptyTitle: 'No open incidents',
            emptyDescription: 'Resolved incidents are under Resolved.',
          })}
          {...(filter === 'resolved' && {
            emptyTitle: 'No resolved incidents',
            emptyDescription: 'Incidents appear here once they are resolved.',
          })}
        />
      )}

      {totals && totals.total > incidents.length && !loading && (
        <p className="text-center text-xs text-slate-500">
          Showing the {incidents.length} most recently updated of {totals.total} incidents.
        </p>
      )}

      {totalPages > 1 && (
        <div className="flex items-center justify-center gap-2">
          <Button
            variant="ghost"
            size="sm"
            onClick={() => setPage((p) => Math.max(1, p - 1))}
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
            onClick={() => setPage((p) => Math.min(totalPages, p + 1))}
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
export default function IncidentsPage() {
  return (
    <Suspense>
      <IncidentsView />
    </Suspense>
  );
}
