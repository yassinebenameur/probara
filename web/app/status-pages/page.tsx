'use client';

import { useState, useEffect } from 'react';
import { useRouter } from 'next/navigation';
import Link from 'next/link';
import { StatusPage } from '@/lib/types';
import { getStatusPages, deleteStatusPage } from '@/lib/api';
import { resolveStatusPagePublicUrl } from '@/lib/statusPageUrl';

export default function StatusPagesPage() {
  const router = useRouter();
  const [statusPages, setStatusPages] = useState<StatusPage[]>([]);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string>('');
  const [toast, setToast] = useState<{ message: string; type: 'success' | 'error' } | null>(null);

  useEffect(() => {
    loadStatusPages();
  }, []);

  const loadStatusPages = async () => {
    try {
      setLoading(true);
      setError('');
      const response = await getStatusPages({ page_size: 100 });
      setStatusPages(response?.items || []);
    } catch (err: any) {
      setError(err.message || 'Failed to load status pages');
      setStatusPages([]);
    } finally {
      setLoading(false);
    }
  };

  const handleDelete = async (id: string) => {
    if (!confirm('Delete this status page? This action cannot be undone.')) return;

    try {
      await deleteStatusPage(id);
      setStatusPages(statusPages.filter((p) => p.id !== id));
      setToast({ message: 'Status page deleted', type: 'success' });
    } catch (err: any) {
      setToast({ message: err.message || 'Failed to delete', type: 'error' });
    }
  };

  if (loading) {
    return (
      <div className="flex items-center justify-center h-64">
        <div className="text-slate-500">Loading status pages...</div>
      </div>
    );
  }

  if (error) {
    return (
      <div className="flex flex-col items-center justify-center h-64">
        <p className="text-rose-400 mb-4">{error}</p>
        <button
          onClick={loadStatusPages}
          className="btn btn-primary btn-sm"
        >
          Retry
        </button>
      </div>
    );
  }

  return (
    <div className="space-y-6">
      {/* Header */}
      <div className="flex items-center justify-between">
        <div>
          <h1 className="text-xl font-semibold text-white">Status Pages</h1>
          <p className="text-sm text-slate-500 mt-1">
            Public pages to display your service status
          </p>
        </div>
        <div className="flex items-center gap-3">
          <Link
            href="/status-pages/builder"
            className="inline-flex items-center gap-2 rounded-lg border border-white/[0.1] bg-slate-800/60 px-4 py-2 text-sm font-medium text-slate-200 transition-colors hover:bg-slate-800"
          >
            <svg className="h-4 w-4" fill="none" viewBox="0 0 24 24" stroke="currentColor">
              <path strokeLinecap="round" strokeLinejoin="round" strokeWidth={2} d="M4 7h16M4 12h16M4 17h16" />
            </svg>
            Open Builder
          </Link>
          <button
            onClick={() => router.push('/status-pages/new')}
            className="inline-flex items-center gap-2 rounded-lg bg-cyan-500 px-4 py-2 text-sm font-medium text-white transition-colors hover:bg-cyan-400"
          >
            <svg className="h-4 w-4" fill="none" viewBox="0 0 24 24" stroke="currentColor">
              <path strokeLinecap="round" strokeLinejoin="round" strokeWidth={2} d="M12 4v16m8-8H4" />
            </svg>
            Create Status Page
          </button>
        </div>
      </div>

      {/* Content */}
      {statusPages.length === 0 ? (
        <div className="rounded-xl border border-white/[0.06] bg-slate-900/50 p-12 text-center">
          <div className="inline-flex items-center justify-center w-16 h-16 rounded-full bg-slate-800/50 mb-4">
            <svg className="w-8 h-8 text-slate-500" fill="none" viewBox="0 0 24 24" stroke="currentColor">
              <path strokeLinecap="round" strokeLinejoin="round" strokeWidth={2} d="M9 12h6m-6 4h6m2 5H7a2 2 0 01-2-2V5a2 2 0 012-2h5.586a1 1 0 01.707.293l5.414 5.414a1 1 0 01.293.707V19a2 2 0 01-2 2z" />
            </svg>
          </div>
          <h3 className="text-lg font-medium text-white mb-2">No status pages yet</h3>
          <p className="text-sm text-slate-500 mb-6 max-w-sm mx-auto">
            Create a public status page to display your service availability to your users.
          </p>
          <button
            onClick={() => router.push('/status-pages/new')}
            className="btn btn-primary btn-sm"
          >
            Create Your First Status Page
          </button>
        </div>
      ) : (
        <div className="grid gap-4 sm:grid-cols-2 lg:grid-cols-3">
          {statusPages.map((page) => (
            <div
              key={page.id}
              className="group rounded-xl border border-white/[0.06] bg-slate-900/50 p-5 transition-colors hover:border-white/[0.1]"
            >
              {/* Header */}
              <div className="flex items-start justify-between mb-3">
                <div className="flex-1 min-w-0">
                  <h3 className="text-sm font-medium text-white truncate">{page.title}</h3>
                  <p className="text-xs text-slate-500 mt-0.5 font-mono">/{page.slug}</p>
                </div>
                <span className="ml-2 badge badge-success">
                  Active
                </span>
              </div>

              {/* Description */}
              {page.description && (
                <p className="text-xs text-slate-400 mb-4 line-clamp-2">{page.description}</p>
              )}

              {/* Stats */}
              <div className="flex items-center gap-4 mb-4 text-xs text-slate-500">
                <span className="flex items-center gap-1">
                  <svg className="h-3.5 w-3.5" fill="none" viewBox="0 0 24 24" stroke="currentColor">
                    <path strokeLinecap="round" strokeLinejoin="round" strokeWidth={2} d="M9 19v-6a2 2 0 00-2-2H5a2 2 0 00-2 2v6a2 2 0 002 2h2a2 2 0 002-2zm0 0V9a2 2 0 012-2h2a2 2 0 012 2v10m-6 0a2 2 0 002 2h2a2 2 0 002-2m0 0V5a2 2 0 012-2h2a2 2 0 012 2v14a2 2 0 01-2 2h-2a2 2 0 01-2-2z" />
                  </svg>
                  {page.monitor_ids?.length || 0} monitors
                </span>
              </div>

              {/* Color Preview */}
              <div className="flex items-center gap-2 mb-4">
                <div
                  className="h-5 w-5 rounded border border-white/[0.1]"
                  style={{ backgroundColor: page.primary_color || '#3b82f6' }}
                  title="Primary color"
                />
                <div
                  className="h-5 w-5 rounded border border-white/[0.1]"
                  style={{ backgroundColor: page.secondary_color || '#64748b' }}
                  title="Secondary color"
                />
              </div>

              {/* Actions */}
              <div className="flex items-center gap-2 pt-4 border-t border-white/[0.06]">
                <a
                  href={resolveStatusPagePublicUrl(page)}
                  target="_blank"
                  rel="noopener noreferrer"
                  className="flex-1 btn btn-outline btn-sm"
                >
                  <svg className="h-3.5 w-3.5" fill="none" viewBox="0 0 24 24" stroke="currentColor">
                    <path strokeLinecap="round" strokeLinejoin="round" strokeWidth={2} d="M10 6H6a2 2 0 00-2 2v10a2 2 0 002 2h10a2 2 0 002-2v-4M14 4h6m0 0v6m0-6L10 14" />
                  </svg>
                  View
                </a>
                <Link href={`/status-pages/${page.id}`} className="flex-1">
                  <button className="w-full btn btn-secondary btn-sm">
                    Edit
                  </button>
                </Link>
                <button
                  onClick={() => handleDelete(page.id)}
                  className="btn btn-danger btn-sm"
                >
                  Delete
                </button>
              </div>
            </div>
          ))}
        </div>
      )}

      {/* Toast */}
      {toast && (
        <div className={`fixed bottom-4 right-4 rounded-lg px-4 py-3 shadow-lg ${
          toast.type === 'success' ? 'bg-emerald-500' : 'bg-rose-500'
        }`}>
          <div className="flex items-center gap-3">
            <p className="text-sm text-white">{toast.message}</p>
            <button onClick={() => setToast(null)} className="text-white/80 hover:text-white">
              <svg className="h-4 w-4" fill="none" viewBox="0 0 24 24" stroke="currentColor">
                <path strokeLinecap="round" strokeLinejoin="round" strokeWidth={2} d="M6 18L18 6M6 6l12 12" />
              </svg>
            </button>
          </div>
        </div>
      )}
    </div>
  );
}
