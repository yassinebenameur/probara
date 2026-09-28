'use client';

import { useState, useEffect } from 'react';
import Link from 'next/link';
import { ArrowUpRight, Globe, Plus, Trash2 } from 'lucide-react';
import { StatusPage } from '@/lib/types';
import { getStatusPages, deleteStatusPage } from '@/lib/api';
import { resolveStatusPagePublicUrl } from '@/lib/statusPageUrl';
import { pluralize } from '@/lib/format';
import Button from '@/components/ui/Button';
import PageHeader from '@/components/ui/PageHeader';
import EmptyState from '@/components/ui/EmptyState';
import ConfirmDialog from '@/components/ui/ConfirmDialog';
import { useToast } from '@/components/ui/ToastProvider';
import { useCurrentUser } from '@/components/providers/CurrentUserProvider';

export default function StatusPagesPage() {
  const { showToast } = useToast();
  const { canWrite } = useCurrentUser();
  const [statusPages, setStatusPages] = useState<StatusPage[]>([]);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string>('');
  const [pendingDelete, setPendingDelete] = useState<StatusPage | null>(null);
  const [deleting, setDeleting] = useState(false);

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

  const handleDelete = async () => {
    if (!pendingDelete) return;
    setDeleting(true);
    try {
      await deleteStatusPage(pendingDelete.id);
      setStatusPages(statusPages.filter((p) => p.id !== pendingDelete.id));
      showToast('Status page deleted', 'success');
      setPendingDelete(null);
    } catch (err: any) {
      showToast(err.message || 'Failed to delete', 'error');
    } finally {
      setDeleting(false);
    }
  };

  const header = (
    <PageHeader
      title="Status pages"
      subtitle="Public pages to display your service status."
      action={
        canWrite ? (
          <Button variant="accent" size="sm" icon={<Plus strokeWidth={1.75} />} asChild>
            <Link href="/status-pages/new">Create status page</Link>
          </Button>
        ) : undefined
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
            canWrite ? (
              <Button variant="ghost" size="sm" icon={<Plus strokeWidth={1.75} />} asChild>
                <Link href="/status-pages/new">Create status page</Link>
              </Button>
            ) : undefined
          }
        />
      ) : (
        <div className="grid gap-4 sm:grid-cols-2 lg:grid-cols-3">
          {statusPages.map((page) => (
            <div
              key={page.id}
              className="group rounded-xl border border-white/[0.06] bg-slate-900/50 p-5 transition-colors hover:border-white/[0.1]"
            >
              <div className="flex items-start justify-between gap-2">
                <div className="min-w-0 flex-1">
                  <Link
                    href={`/status-pages/${page.id}`}
                    className="block truncate text-sm font-medium text-white hover:text-cyan-400"
                  >
                    {page.title}
                  </Link>
                  <a
                    href={resolveStatusPagePublicUrl(page)}
                    target="_blank"
                    rel="noopener noreferrer"
                    className="mt-0.5 inline-flex items-center gap-1 font-mono text-xs text-slate-500 hover:text-slate-300"
                  >
                    /{page.slug}
                    <ArrowUpRight className="h-3 w-3" strokeWidth={1.75} aria-hidden="true" />
                  </a>
                </div>
                <Button
                  variant="subtle"
                  size="xs"
                  icon={<Trash2 strokeWidth={1.75} />}
                  onClick={() => setPendingDelete(page)}
                  title="Delete"
                >
                  <span className="sr-only">Delete</span>
                </Button>
              </div>

              {page.description && (
                <p className="mt-3 text-xs text-slate-400 line-clamp-2">{page.description}</p>
              )}

              <p className="mt-3 text-xs text-slate-500">
                {pluralize(page.monitor_ids?.length || 0, 'monitor')}
              </p>
            </div>
          ))}
        </div>
      )}

      <ConfirmDialog
        open={pendingDelete !== null}
        title="Delete status page"
        description={
          pendingDelete
            ? `“${pendingDelete.title}” and its public page at /${pendingDelete.slug} will be removed. This cannot be undone.`
            : undefined
        }
        confirmLabel="Delete page"
        loading={deleting}
        onConfirm={handleDelete}
        onCancel={() => !deleting && setPendingDelete(null)}
      />
    </div>
  );
}
