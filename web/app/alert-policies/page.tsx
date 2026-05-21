'use client';

import { useState, useEffect } from 'react';
import { useRouter } from 'next/navigation';
import Link from 'next/link';
import { AlertTriangle, Pencil, Plus, Trash2 } from 'lucide-react';
import { AlertPolicy } from '@/lib/types';
import { getAlertPolicies, deleteAlertPolicy } from '@/lib/api';
import Panel from '@/components/ui/Panel';
import Pill from '@/components/ui/Pill';
import Toast from '@/components/ui/Toast';
import Button from '@/components/ui/Button';
import PageHeader from '@/components/ui/PageHeader';
import EmptyState from '@/components/ui/EmptyState';

export default function AlertPoliciesPage() {
  const router = useRouter();
  const [policies, setPolicies] = useState<AlertPolicy[]>([]);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string>('');
  const [toast, setToast] = useState<{ message: string; type: 'success' | 'error' } | null>(null);

  useEffect(() => {
    loadPolicies();
  }, []);

  const loadPolicies = async () => {
    try {
      setLoading(true);
      setError('');
      const response = await getAlertPolicies({ page_size: 100 });
      setPolicies(response?.items || []);
    } catch (err: any) {
      setError(err.message || 'Failed to load alert policies');
      setPolicies([]);
    } finally {
      setLoading(false);
    }
  };

  const handleDelete = async (id: string) => {
    if (!confirm('Are you sure you want to delete this alert policy?')) {
      return;
    }

    try {
      await deleteAlertPolicy(id);
      setPolicies((policies || []).filter((p) => p.id !== id));
      setToast({ message: 'Alert policy deleted successfully', type: 'success' });
    } catch (err: any) {
      setToast({ message: err.message || 'Failed to delete alert policy', type: 'error' });
    }
  };

  const header = (
    <PageHeader
      title="Alert policies"
      subtitle="Define when incidents trigger alerts."
      action={
        <Button variant="ghost" size="sm" icon={<Plus strokeWidth={1.75} />} asChild>
          <Link href="/alert-policies/new">Create alert policy</Link>
        </Button>
      }
    />
  );

  return (
    <div className="space-y-6">
      {header}

      {loading ? (
        <Panel title="Alert policies" subtitle="Loading policies">
          <div className="text-sm text-slate-500">Loading alert policies…</div>
        </Panel>
      ) : error ? (
        <Panel title="Error" subtitle="Failed to load alert policies">
          <div className="rounded-lg border border-rose-500/20 bg-rose-500/10 px-4 py-3 text-sm text-rose-400">
            {error}
            <button onClick={loadPolicies} className="ml-2 underline hover:no-underline">
              Retry
            </button>
          </div>
        </Panel>
      ) : (
        <Panel
          title="Alert policies"
          subtitle={`${policies.length} polic${policies.length !== 1 ? 'ies' : 'y'} configured`}
        >
          <div className="table-card mt-1">
            {policies.length === 0 ? (
              <EmptyState
                icon={<AlertTriangle strokeWidth={1.5} />}
                title="No alert policies yet"
                description="Create your first alert policy to define when alerts should be triggered."
                action={
                  <Button variant="ghost" size="sm" icon={<Plus strokeWidth={1.75} />} asChild>
                    <Link href="/alert-policies/new">Create alert policy</Link>
                  </Button>
                }
              />
            ) : (
              <table className="data-table text-xs">
                <thead>
                  <tr>
                    <th>Name</th>
                    <th>Description</th>
                    <th>Threshold</th>
                    <th>Window</th>
                    <th className="text-right">Actions</th>
                  </tr>
                </thead>
                <tbody>
                  {policies.map((policy, idx) => (
                    <tr key={policy.id} className={idx % 2 === 1 ? 'bg-[rgba(15,23,42,0.35)]' : ''}>
                      <td>
                        <div className="text-sm font-medium text-white">{policy.name}</div>
                      </td>
                      <td>
                        <div className="text-slate-400">{policy.description || '-'}</div>
                      </td>
                      <td>
                        <Pill tone="neutral" size="xs">{policy.failure_threshold} failures</Pill>
                      </td>
                      <td className="text-slate-400">
                        {Math.floor(policy.failure_window_seconds / 60)}m
                      </td>
                      <td className="text-right">
                        <div className="inline-flex items-center gap-1.5">
                          <Button variant="ghost" size="xs" icon={<Pencil strokeWidth={1.75} />} asChild>
                            <Link href={`/alert-policies/${policy.id}`}>Edit</Link>
                          </Button>
                          <Button
                            variant="danger"
                            size="xs"
                            icon={<Trash2 strokeWidth={1.75} />}
                            onClick={() => handleDelete(policy.id)}
                          >
                            Delete
                          </Button>
                        </div>
                      </td>
                    </tr>
                  ))}
                </tbody>
              </table>
            )}
          </div>
        </Panel>
      )}

      {toast && (
        <Toast
          message={toast.message}
          type={toast.type}
          onClose={() => setToast(null)}
        />
      )}
    </div>
  );
}
