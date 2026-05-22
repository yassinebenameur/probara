'use client';

import { useState, useEffect } from 'react';
import Link from 'next/link';
import { ArrowUpRight, Globe, Pencil, Plus, Trash2 } from 'lucide-react';
import { StatusPage } from '@/lib/types';
import { getStatusPages, deleteStatusPage } from '@/lib/api';
import { resolveStatusPagePublicUrl } from '@/lib/statusPageUrl';
import Button from '@/components/ui/Button';
import Pill from '@/components/ui/Pill';
import PageHeader from '@/components/ui/PageHeader';
import EmptyState from '@/components/ui/EmptyState';

export default function StatusPagesPage() {
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

  const header = (
    <PageHeader
      title="Status pages"
      subtitle="Public pages to display your service status."
      action={
        <Button variant="ghost" size="sm" icon={<Plus strokeWidth={1.75} />} asChild>
          <Link href="/status-pages/new">Create status page</Link>
        </Button>
      }
    />
  );

  if (loading) {
    return (
      <div className="space-y-6">
        {header}
        <div className="text-sm text-slate-500">Loading status pages…</div>
      </div>
    );
  }

  if (error) {
    return (
      <div className="space-y-6">
        {header}
        <div className="rounded-lg border border-rose-500/20 bg-rose-500/10 px-4 py-3 text-sm text-rose-400">
          {error}
          <Button variant="ghost" size="xs" className="ml-3" onClick={loadStatusPages}>
            Retry
          </Button>
        </div>
      </div>
    );
  }

  return (
    <div className="space-y-6">
      {header}

      {statusPages.length === 0 ? (
        <EmptyState
          icon={<Globe strokeWidth={1.5} />}
          title="No status pages yet"
          description="Create a public status page to display your service availability."
          action={
            <Button variant="ghost" size="sm" icon={<Plus strokeWidth={1.75} />} asChild>
              <Link href="/status-pages/new">Create status page</Link>
            </Button>
          }
        />
      ) : (
        <div className="grid gap-4 sm:grid-cols-2 lg:grid-cols-3">
          {statusPages.map((page) => (
            <div
              key={page.id}
              className="group rounded-xl border border-white/[0.06] bg-slate-900/50 p-5 transition-colors hover:border-white/[0.1]"
            >
              <div className="flex items-start justify-between mb-3">
                <div className="flex-1 min-w-0">
                  <h3 className="text-sm font-medium text-white truncate">{page.title}</h3>
                  <p className="text-xs text-slate-500 mt-0.5 font-mono">/{page.slug}</p>
                </div>
                <Pill tone="success" size="xs" dot>Active</Pill>
              </div>

              {page.description && (
                <p className="text-xs text-slate-400 mb-4 line-clamp-2">{page.description}</p>
              )}

              <div className="flex items-center gap-4 mb-4 text-xs text-slate-500">
                <span>{page.monitor_ids?.length || 0} monitors</span>
              </div>

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

              <div className="flex items-center gap-1.5 pt-4 border-t border-white/[0.06]">
                <Button variant="ghost" size="xs" icon={<ArrowUpRight strokeWidth={1.75} />} className="flex-1" asChild>
                  <a href={resolveStatusPagePublicUrl(page)} target="_blank" rel="noopener noreferrer">
                    View
                  </a>
                </Button>
                <Button variant="ghost" size="xs" icon={<Pencil strokeWidth={1.75} />} className="flex-1" asChild>
                  <Link href={`/status-pages/${page.id}`}>Edit</Link>
                </Button>
                <Button
                  variant="danger"
                  size="xs"
                  icon={<Trash2 strokeWidth={1.75} />}
                  onClick={() => handleDelete(page.id)}
                  aria-label="Delete"
                >
                  <span className="sr-only">Delete</span>
                </Button>
              </div>
            </div>
          ))}
        </div>
      )}

      {toast && (
        <div className={`fixed bottom-4 right-4 rounded-lg px-4 py-3 shadow-lg ${
          toast.type === 'success' ? 'bg-emerald-500' : 'bg-rose-500'
        }`}>
          <div className="flex items-center gap-3">
            <p className="text-sm text-white">{toast.message}</p>
            <button
              onClick={() => setToast(null)}
              className="text-white/80 hover:text-white"
              aria-label="Dismiss"
            >
              <span aria-hidden="true">×</span>
            </button>
          </div>
        </div>
      )}
    </div>
  );
}
