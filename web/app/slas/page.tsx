'use client';

import { useCallback, useEffect, useMemo, useState } from 'react';
import Link from 'next/link';
import { Pencil, Plus, Target, Trash2 } from 'lucide-react';
import { deleteSla, getSlas } from '@/lib/api';
import {
  SLA_AGGREGATION_SHORT, SLA_PERIOD_LABELS, formatSlaPct, formatSlaPeriodRange, formatTarget, type Sla,
} from '@/lib/sla';
import { pluralize } from '@/lib/format';
import { sortRows, type SortState, type SortValue } from '@/lib/sort';
import SortableHeader, { useUrlSort } from '@/components/ui/SortableHeader';
import { FOCUS_RING } from '@/components/ui/focus';
import Button from '@/components/ui/Button';
import IconButton from '@/components/ui/IconButton';
import PageHeader from '@/components/ui/PageHeader';
import Page from '@/components/ui/Page';
import EmptyState from '@/components/ui/EmptyState';
import Callout from '@/components/ui/Callout';
import TableSkeleton from '@/components/ui/TableSkeleton';
import ConfirmDialog from '@/components/ui/ConfirmDialog';
import { useToast } from '@/components/ui/ToastProvider';
import { useCanWrite } from '@/components/providers/CurrentUserProvider';
import { SlaFormModal } from '@/components/slas/SlaFormModal';
import { BudgetBar, SlaStatusPill } from '@/components/slas/SlaVisuals';

// Below this much tracked time a period's availability is thin evidence; say
// so next to the figure so a near-empty period never reads as an outage.
const LOW_COVERAGE_PCT = 99;

const SORT_KEYS = ['name', 'target', 'current', 'budget'] as const;
type SlaSortKey = (typeof SORT_KEYS)[number];
const DEFAULT_SORT: SortState<SlaSortKey> = { key: 'name', dir: 'asc' };
// Strictest target first; worst attainment and least budget left first.
const DEFAULT_DIR = { target: 'desc' } as const;

// A period without data has no figure: it sorts last either way.
const SORT_VALUE: Record<SlaSortKey, (sla: Sla) => SortValue> = {
  name: (sla) => sla.name,
  target: (sla) => sla.target_pct,
  current: (sla) => (sla.current?.has_data ? sla.current.availability_pct : null),
  budget: (sla) => (sla.current?.has_data ? sla.current.budget_remaining_pct : null),
};

export default function SlasPage() {
  const { showToast } = useToast();
  const canWrite = useCanWrite();
  const [slas, setSlas] = useState<Sla[]>([]);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState('');
  const [modalOpen, setModalOpen] = useState(false);
  const [editing, setEditing] = useState<Sla | undefined>(undefined);
  const [pendingDelete, setPendingDelete] = useState<Sla | null>(null);
  const [deleting, setDeleting] = useState(false);
  const [sort, setSort] = useUrlSort(SORT_KEYS, DEFAULT_SORT, DEFAULT_DIR);
  const sorted = useMemo(() => sortRows(slas, SORT_VALUE[sort.key], sort.dir, (sla) => sla.name), [slas, sort]);

  const load = useCallback(async () => {
    try {
      setLoading(true);
      setError('');
      setSlas(await getSlas());
    } catch (err: any) {
      setError(err.message || 'Failed to load SLAs');
    } finally {
      setLoading(false);
    }
  }, []);

  useEffect(() => {
    load();
  }, [load]);

  const handleDelete = async () => {
    if (!pendingDelete) return;
    setDeleting(true);
    try {
      await deleteSla(pendingDelete.id);
      setSlas((prev) => prev.filter((s) => s.id !== pendingDelete.id));
      showToast('SLA deleted', 'success');
      setPendingDelete(null);
    } catch (err: any) {
      showToast(err.message || 'Failed to delete SLA', 'error');
    } finally {
      setDeleting(false);
    }
  };

  const openCreate = () => {
    setEditing(undefined);
    setModalOpen(true);
  };

  return (
    <Page width="data">
      <PageHeader
        title="SLAs"
        subtitle="Availability targets for sets of monitors, reported per calendar period with error budgets and exports."
        action={
          canWrite && !(loading || error || slas.length === 0) ? (
            <Button variant="accent" size="sm" icon={<Plus strokeWidth={1.75} />} onClick={openCreate}>
              New SLA
            </Button>
          ) : undefined
        }
      />

      {error && (
        <Callout
          tone="danger"
          action={<Button variant="ghost" size="xs" onClick={load} loading={loading}>Retry</Button>}
        >
          {error}
        </Callout>
      )}

      {loading && slas.length === 0 ? (
        <TableSkeleton rows={3} columns={3} label="Loading SLAs" />
      ) : error && slas.length === 0 ? null : slas.length === 0 ? (
        <EmptyState
          icon={<Target strokeWidth={1.5} />}
          title="No SLAs yet"
          description="Define a target for a service — pick its monitors or a tag — to track availability per month, week or quarter and export reports for customers."
          action={canWrite ? { label: 'New SLA', onClick: openCreate, icon: <Plus strokeWidth={1.75} /> } : undefined}
        />
      ) : (
        <div className="glass overflow-hidden">
          <div className="overflow-x-auto">
            <table className="data-table min-w-[720px] text-sm">
              <thead>
                <tr>
                  <SortableHeader label="SLA" sortKey="name" sort={sort} onSort={setSort} defaultDir={DEFAULT_DIR} />
                  <SortableHeader
                    label="Target"
                    sortKey="target"
                    sort={sort}
                    onSort={setSort}
                    defaultDir={DEFAULT_DIR}
                    align="right"
                    title="Strictest first"
                  />
                  <SortableHeader
                    label="Current period"
                    sortKey="current"
                    sort={sort}
                    onSort={setSort}
                    defaultDir={DEFAULT_DIR}
                    title="Lowest availability first"
                  />
                  <SortableHeader
                    label="Error budget left"
                    sortKey="budget"
                    sort={sort}
                    onSort={setSort}
                    defaultDir={DEFAULT_DIR}
                    title="Least budget left first"
                    className="w-56"
                  />
                  <th><span className="sr-only">Actions</span></th>
                </tr>
              </thead>
              <tbody>
                {sorted.map((sla) => {
                  const cur = sla.current;
                  const lowCoverage = cur?.has_data && cur.coverage_pct < LOW_COVERAGE_PCT;
                  return (
                    <tr key={sla.id}>
                      <td>
                        <Link
                          href={`/slas/${sla.id}`}
                          className={`rounded-sm font-medium text-white transition-colors hover:text-cyan-300 ${FOCUS_RING}`}
                        >
                          {sla.name}
                        </Link>
                        <div className="mt-0.5 flex flex-wrap gap-x-3 text-xs text-slate-500">
                          <span>{SLA_PERIOD_LABELS[sla.period]}</span>
                          <span>{SLA_AGGREGATION_SHORT[sla.aggregation]}</span>
                          <span className="tabular-nums">{pluralize(cur?.monitor_count ?? 0, 'monitor')}</span>
                        </div>
                      </td>
                      <td className="whitespace-nowrap text-right tabular-nums text-slate-300">
                        {formatTarget(sla.target_pct)}
                      </td>
                      <td>
                        {/* The pill carries the tone; the figure stays neutral. */}
                        <div className="flex items-center gap-2">
                          <span className="tabular-nums text-slate-200">{formatSlaPct(cur?.availability_pct ?? null)}</span>
                          <SlaStatusPill met={cur?.met} running />
                        </div>
                        {cur && (
                          <div className="mt-0.5 flex flex-wrap gap-x-3 text-xs tabular-nums text-slate-500">
                            <span title={`${cur.period_key}, ${sla.timezone}`}>
                              {formatSlaPeriodRange(cur.period_start, cur.period_end, sla.timezone)}, to date
                            </span>
                            {lowCoverage && (
                              <span className="text-amber-300" title="Share of the period so far with monitor state on record">
                                {Math.floor(cur.coverage_pct)}% tracked
                              </span>
                            )}
                          </div>
                        )}
                      </td>
                      <td>
                        <BudgetBar remainingPct={cur?.budget_remaining_pct} />
                      </td>
                      <td>
                        {canWrite && (
                          <div className="flex items-center justify-end gap-0.5">
                            <IconButton icon={<Pencil strokeWidth={1.75} />} label="Edit" onClick={() => {
                              setEditing(sla);
                              setModalOpen(true);
                            }} />
                            <IconButton icon={<Trash2 strokeWidth={1.75} />} label="Delete" danger
                              onClick={() => setPendingDelete(sla)} />
                          </div>
                        )}
                      </td>
                    </tr>
                  );
                })}
              </tbody>
            </table>
          </div>
        </div>
      )}

      {canWrite && modalOpen && (
        <SlaFormModal
          sla={editing}
          onDone={() => {
            setModalOpen(false);
            showToast(editing ? 'SLA updated' : 'SLA created', 'success');
            setEditing(undefined);
            load();
          }}
          onCancel={() => {
            setModalOpen(false);
            setEditing(undefined);
          }}
        />
      )}

      <ConfirmDialog
        open={pendingDelete !== null}
        title="Delete SLA"
        description={pendingDelete ? `Delete “${pendingDelete.name}” and all of its issued reports? Monitors are not affected.` : ''}
        confirmLabel="Delete"
        onConfirm={handleDelete}
        onCancel={() => setPendingDelete(null)}
        loading={deleting}
      />
    </Page>
  );
}
