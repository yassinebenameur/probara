'use client';

import { useCallback, useEffect, useState } from 'react';
import { GitBranch, Pencil, Plus, Shuffle, Trash2 } from 'lucide-react';
import { deleteEscalationPolicy, getEscalationPolicies } from '@/lib/api';
import { describeRepeat, hasPeople, type EscalationPolicy } from '@/lib/escalation';
import Button from '@/components/ui/Button';
import IconButton from '@/components/ui/IconButton';
import PageHeader from '@/components/ui/PageHeader';
import Page from '@/components/ui/Page';
import Pill from '@/components/ui/Pill';
import EmptyState from '@/components/ui/EmptyState';
import Callout from '@/components/ui/Callout';
import TableSkeleton from '@/components/ui/TableSkeleton';
import ConfirmDialog from '@/components/ui/ConfirmDialog';
import { useToast } from '@/components/ui/ToastProvider';
import { useCanWrite } from '@/components/providers/CurrentUserProvider';
import { PolicyFormModal } from '@/components/escalation/PolicyFormModal';
import { ConvertRoutesDialog } from '@/components/escalation/ConvertRoutesDialog';
import { PolicySteps } from '@/components/escalation/PolicySteps';

export default function EscalationPoliciesPage() {
  const { showToast } = useToast();
  const canWrite = useCanWrite();
  const [policies, setPolicies] = useState<EscalationPolicy[]>([]);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState('');
  const [modalOpen, setModalOpen] = useState(false);
  const [converting, setConverting] = useState(false);
  const [editing, setEditing] = useState<EscalationPolicy | undefined>(undefined);
  const [pendingDelete, setPendingDelete] = useState<EscalationPolicy | null>(null);
  const [deleting, setDeleting] = useState(false);

  const load = useCallback(async () => {
    try {
      setLoading(true);
      setError('');
      setPolicies(await getEscalationPolicies());
    } catch (err: any) {
      setError(err.message || 'Failed to load escalation policies');
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
      await deleteEscalationPolicy(pendingDelete.id);
      setPolicies((prev) => prev.filter((p) => p.id !== pendingDelete.id));
      showToast('Policy deleted', 'success');
      setPendingDelete(null);
    } catch (err: any) {
      showToast(err.message || 'Failed to delete policy', 'error');
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
        title="Escalation"
        subtitle="Who gets paged, in what order, until someone acknowledges. Assign a policy to a monitor in its Alerting section, or make one the workspace default in Settings."
        action={
          canWrite && !(loading || error) ? (
            <div className="flex gap-2">
              <Button variant="ghost" size="sm" icon={<Shuffle strokeWidth={1.75} />} onClick={() => setConverting(true)}>
                Convert routes
              </Button>
              {policies.length > 0 && (
                <Button variant="accent" size="sm" icon={<Plus strokeWidth={1.75} />} onClick={openCreate}>
                  New policy
                </Button>
              )}
            </div>
          ) : undefined
        }
      />

      {error && (
        <Callout tone="danger" action={<Button variant="ghost" size="xs" onClick={load} loading={loading}>Retry</Button>}>
          {error}
        </Callout>
      )}

      {loading && policies.length === 0 ? (
        <TableSkeleton rows={3} columns={3} label="Loading escalation policies" />
      ) : error && policies.length === 0 ? null : policies.length === 0 ? (
        <EmptyState
          icon={<GitBranch strokeWidth={1.5} />}
          title="No escalation policies yet"
          description="Page a person first, then whoever is on call, then the whole team — each after a delay, until someone acknowledges. Channels can be steps too."
          action={canWrite ? { label: 'New policy', onClick: openCreate, icon: <Plus strokeWidth={1.75} /> } : undefined}
        />
      ) : (
        <div className="glass overflow-hidden">
          <div className="overflow-x-auto">
            <table className="data-table min-w-[720px] text-sm">
              <thead>
                <tr>
                  <th>Policy</th>
                  <th>Steps</th>
                  <th>Used by</th>
                  <th><span className="sr-only">Actions</span></th>
                </tr>
              </thead>
              <tbody>
                {policies.map((p) => (
                  <tr key={p.id}>
                    <td className="align-top">
                      <div className="flex items-center gap-2 font-medium text-white">
                        {p.name}
                        {p.tenant_default && <Pill tone="info" size="xs">Workspace default</Pill>}
                      </div>
                      {p.description && <div className="mt-0.5 text-xs text-slate-500">{p.description}</div>}
                      {hasPeople(p.steps) && <div className="mt-0.5 text-xs text-slate-500">{describeRepeat(p)}</div>}
                    </td>
                    <td className="align-top"><PolicySteps steps={p.steps} /></td>
                    <td className="align-top text-slate-300">
                      {p.monitor_count === 0 && !p.tenant_default
                        ? <span className="text-slate-500">Unused</span>
                        : `${p.monitor_count} monitor${p.monitor_count === 1 ? '' : 's'}${p.tenant_default ? ' + default' : ''}`}
                    </td>
                    <td className="align-top">
                      {canWrite && (
                        <div className="flex items-center justify-end gap-0.5">
                          <IconButton icon={<Pencil strokeWidth={1.75} />} label="Edit" onClick={() => {
                            setEditing(p);
                            setModalOpen(true);
                          }} />
                          <IconButton icon={<Trash2 strokeWidth={1.75} />} label="Delete" danger
                            onClick={() => setPendingDelete(p)} />
                        </div>
                      )}
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        </div>
      )}

      {canWrite && modalOpen && (
        <PolicyFormModal
          policy={editing}
          onDone={() => {
            setModalOpen(false);
            showToast(editing ? 'Policy updated' : 'Policy created', 'success');
            setEditing(undefined);
            load();
          }}
          onCancel={() => {
            setModalOpen(false);
            setEditing(undefined);
          }}
        />
      )}

      {canWrite && converting && (
        <ConvertRoutesDialog
          onDone={(p) => {
            setConverting(false);
            showToast(`Created “${p.name}”`, 'success');
            load();
          }}
          onCancel={() => setConverting(false)}
        />
      )}

      <ConfirmDialog
        open={pendingDelete !== null}
        title="Delete escalation policy"
        description={pendingDelete ? `Delete “${pendingDelete.name}”? A policy still assigned to a monitor or used as the workspace default cannot be deleted.` : ''}
        confirmLabel="Delete"
        tone="danger"
        onConfirm={handleDelete}
        onCancel={() => setPendingDelete(null)}
        loading={deleting}
      />
    </Page>
  );
}
