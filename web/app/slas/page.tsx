'use client';

import { useCallback, useEffect, useState } from 'react';
import Link from 'next/link';
import { Pencil, Plus, Target, Trash2 } from 'lucide-react';
import { deleteSla, getSlas } from '@/lib/api';
import { SLA_PERIOD_LABELS, formatTarget, slaTone, type Sla } from '@/lib/sla';
import { pluralize } from '@/lib/format';
import Button from '@/components/ui/Button';
import IconButton from '@/components/ui/IconButton';
import PageHeader from '@/components/ui/PageHeader';
import EmptyState from '@/components/ui/EmptyState';
import Callout from '@/components/ui/Callout';
import ConfirmDialog from '@/components/ui/ConfirmDialog';
import { useToast } from '@/components/ui/ToastProvider';
import { SlaFormModal } from '@/components/slas/SlaFormModal';
import { AvailabilityFigure, BudgetBar, SlaStatusPill } from '@/components/slas/SlaVisuals';

export default function SlasPage() {
  const { showToast } = useToast();
  const [slas, setSlas] = useState<Sla[]>([]);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState('');
  const [modalOpen, setModalOpen] = useState(false);
  const [editing, setEditing] = useState<Sla | undefined>(undefined);
  const [pendingDelete, setPendingDelete] = useState<Sla | null>(null);
  const [deleting, setDeleting] = useState(false);

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
    <div className="space-y-6">
      <PageHeader
        title="SLAs"
        subtitle="Availability targets for sets of monitors, reported per calendar period with error budgets and exports."
        action={
          <Button variant="accent" size="sm" icon={<Plus strokeWidth={1.75} />} onClick={openCreate}>
            New SLA
          </Button>
        }
      />

      {error && <Callout tone="danger">{error}</Callout>}

      {loading ? (
        <div className="glass p-8 text-center text-sm text-slate-500">Loading…</div>
      ) : slas.length === 0 ? (
        <EmptyState
          icon={<Target strokeWidth={1.5} />}
          title="No SLAs yet"
          description="Define a target for a service — pick its monitors or a tag — to track availability per month, week or quarter and export reports for customers."
          action={<Button size="sm" icon={<Plus strokeWidth={1.75} />} onClick={openCreate}>New SLA</Button>}
        />
      ) : (
        <div className="glass overflow-hidden">
          <div className="overflow-x-auto">
            <table className="data-table min-w-[720px] text-sm">
              <thead>
                <tr>
                  <th>SLA</th>
                  <th>Target</th>
                  <th>Current period</th>
                  <th className="w-48">Error budget left</th>
                  <th />
                </tr>
              </thead>
              <tbody>
                {slas.map((sla) => {
                  const cur = sla.current;
                  const tone = slaTone(cur?.met, cur?.budget_remaining_pct);
                  return (
                    <tr key={sla.id} className="transition-colors">
                      <td>
                        <Link
                          href={`/slas/${sla.id}`}
                          className="font-medium text-white transition-colors hover:text-cyan-300"
                        >
                          {sla.name}
                        </Link>
                        <div className="mt-0.5 text-xs text-slate-500">
                          {SLA_PERIOD_LABELS[sla.period]} · {sla.aggregation} · {pluralize(cur?.monitor_count ?? 0, 'monitor')}
                        </div>
                      </td>
                      <td className="whitespace-nowrap font-mono tabular-nums text-slate-300">
                        {formatTarget(sla.target_pct)}
                      </td>
                      <td>
                        <div className="flex items-center gap-2">
                          <AvailabilityFigure value={cur?.availability_pct ?? null} tone={tone} />
                          <SlaStatusPill met={cur?.met} running />
                        </div>
                        {cur && <div className="mt-0.5 text-xs tabular-nums text-slate-500">{cur.period_key} · to date</div>}
                      </td>
                      <td>
                        <BudgetBar remainingPct={cur?.budget_remaining_pct} />
                      </td>
                      <td>
                        <div className="flex items-center justify-end gap-0.5">
                          <IconButton icon={<Pencil strokeWidth={1.75} />} label="Edit" onClick={() => {
                            setEditing(sla);
                            setModalOpen(true);
                          }} />
                          <IconButton icon={<Trash2 strokeWidth={1.75} />} label="Delete" danger
                            onClick={() => setPendingDelete(sla)} />
                        </div>
                      </td>
                    </tr>
                  );
                })}
              </tbody>
            </table>
          </div>
        </div>
      )}

      {modalOpen && (
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
    </div>
  );
}
