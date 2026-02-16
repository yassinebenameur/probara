'use client';

import { useState, useEffect } from 'react';
import { useRouter } from 'next/navigation';
import { AlertPolicy } from '@/lib/types';
import { getAlertPolicies, deleteAlertPolicy } from '@/lib/api';
import Panel from '@/components/ui/Panel';
import TagPill from '@/components/ui/TagPill';
import Toast from '@/components/ui/Toast';
import Link from 'next/link';

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

  if (loading) {
    return (
      <div className="space-y-6">
        <div className="flex items-center justify-between">
          <div>
            <h1 className="text-xl font-semibold text-white">Alert Policies</h1>
            <p className="mt-1 text-sm text-slate-500">
              Define when incidents trigger alerts and notifications.
            </p>
          </div>
          <button
            onClick={() => router.push('/alert-policies/new')}
            className="btn btn-primary btn-sm"
          >
            <svg className="h-4 w-4" fill="none" viewBox="0 0 24 24" stroke="currentColor">
              <path strokeLinecap="round" strokeLinejoin="round" strokeWidth={2} d="M12 4v16m8-8H4" />
            </svg>
            Create Alert Policy
          </button>
        </div>
        <Panel title="Alert Policies" subtitle="Loading policies">
          <div className="text-sm text-muted">Loading alert policies...</div>
        </Panel>
      </div>
    );
  }

  if (error) {
    return (
      <div className="space-y-6">
        <div className="flex items-center justify-between">
          <div>
            <h1 className="text-xl font-semibold text-white">Alert Policies</h1>
            <p className="mt-1 text-sm text-slate-500">
              Define when incidents trigger alerts and notifications.
            </p>
          </div>
          <button
            onClick={() => router.push('/alert-policies/new')}
            className="btn btn-primary btn-sm"
          >
            <svg className="h-4 w-4" fill="none" viewBox="0 0 24 24" stroke="currentColor">
              <path strokeLinecap="round" strokeLinejoin="round" strokeWidth={2} d="M12 4v16m8-8H4" />
            </svg>
            Create Alert Policy
          </button>
        </div>
        <Panel title="Error" subtitle="Failed to load alert policies">
          <div className="rounded-lg border border-rose-500/20 bg-rose-500/10 px-4 py-3 text-sm text-rose-400">
            {error}
            <button onClick={loadPolicies} className="ml-2 underline hover:no-underline">
              Retry
            </button>
          </div>
        </Panel>
      </div>
    );
  }

  return (
    <div className="space-y-6">
      {/* Header */}
      <div className="flex items-center justify-between">
        <div>
          <h1 className="text-xl font-semibold text-white">Alert Policies</h1>
          <p className="mt-1 text-sm text-slate-500">
            Define when incidents trigger alerts and notifications.
          </p>
        </div>
        <button
          onClick={() => router.push('/alert-policies/new')}
          className="btn btn-primary btn-sm"
        >
          <svg className="h-4 w-4" fill="none" viewBox="0 0 24 24" stroke="currentColor">
            <path strokeLinecap="round" strokeLinejoin="round" strokeWidth={2} d="M12 4v16m8-8H4" />
          </svg>
          Create Alert Policy
        </button>
      </div>

      {/* Alert Policies Table */}
      <Panel
        title="Alert Policies"
        subtitle={`${policies.length} policy${policies.length !== 1 ? 'ies' : ''} configured`}
      >
        <div className="table-card mt-1">
          {policies.length === 0 ? (
            <div className="flex flex-col items-center justify-center border border-dashed border-white/[0.08] py-12 text-center">
              <svg className="h-10 w-10 text-slate-600" fill="none" viewBox="0 0 24 24" stroke="currentColor">
                <path strokeLinecap="round" strokeLinejoin="round" strokeWidth={1} d="M12 9v4m0 4h.01M5.07 19a10 10 0 1113.86 0H5.07z" />
              </svg>
              <div className="mt-3 text-sm font-medium text-white">No alert policies yet</div>
              <div className="mt-1 text-xs text-slate-500">
                Create your first alert policy to define when alerts should be triggered.
              </div>
              <button
                onClick={() => router.push('/alert-policies/new')}
                className="btn btn-primary btn-sm mt-4"
              >
                <svg className="h-3.5 w-3.5" fill="none" viewBox="0 0 24 24" stroke="currentColor">
                  <path strokeLinecap="round" strokeLinejoin="round" strokeWidth={2} d="M12 4v16m8-8H4" />
                </svg>
                Create Alert Policy
              </button>
            </div>
          ) : (
            <table className="data-table text-xs">
              <thead>
                <tr>
                  <th>
                    Name
                  </th>
                  <th>
                    Description
                  </th>
                  <th>
                    Threshold
                  </th>
                  <th>
                    Window
                  </th>
                  <th className="text-right">
                    Actions
                  </th>
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
                      <TagPill>{policy.failure_threshold} failures</TagPill>
                    </td>
                    <td className="text-slate-400">
                      {Math.floor(policy.failure_window_seconds / 60)}m
                    </td>
                    <td className="text-right">
                      <Link href={`/alert-policies/${policy.id}`} className="inline-flex">
                        <button className="btn btn-secondary btn-xs">
                          Edit
                        </button>
                      </Link>
                      <button
                        onClick={() => handleDelete(policy.id)}
                        className="ml-2 btn btn-danger btn-xs"
                      >
                        Delete
                      </button>
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
          )}
        </div>
      </Panel>

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
