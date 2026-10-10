'use client';

import { useState, useEffect } from 'react';
import Link from 'next/link';
import { ArrowUpRight, Globe, Pencil, Plus, Trash2 } from 'lucide-react';
import { StatusPage } from '@/lib/types';
import { getStatusPages, deleteStatusPage } from '@/lib/api';
import { resolveStatusPagePublicUrl } from '@/lib/statusPageUrl';
import { pluralize } from '@/lib/format';
import Button from '@/components/ui/Button';
import IconButton, { ICON_BUTTON_CLASS } from '@/components/ui/IconButton';
import Pill from '@/components/ui/Pill';
import PageHeader from '@/components/ui/PageHeader';
import EmptyState from '@/components/ui/EmptyState';
import Callout from '@/components/ui/Callout';
import ConfirmDialog from '@/components/ui/ConfirmDialog';
import { useToast } from '@/components/ui/ToastProvider';
import { useCanWrite } from '@/components/providers/CurrentUserProvider';

export default function StatusPagesPage() {
  const { showToast } = useToast();
  const canWrite = useCanWrite();
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

  // While the list is empty the empty state carries the one CTA.
  const header = (
    <PageHeader
      title="Status pages"
      subtitle="Public pages that show your customers live service health."
      action={
        canWrite && !loading && !error && statusPages.length > 0 ? (
          <Button variant="accent" size="sm" icon={<Plus strokeWidth={1.75} />} asChild>
            <Link href="/status-pages/new">New status page</Link>
          </Button>
        ) : undefined
      }
    />
  );

  if (loading) {
    return (
      <div className="space-y-6">
        {header}
        <div className="grid animate-pulse gap-4 sm:grid-cols-2 lg:grid-cols-3">
          {[0, 1, 2].map((i) => (
            <div key={i} className="glass h-[146px]" />
          ))}
        </div>
        <span className="sr-only">Loading status pages…</span>
      </div>
    );
  }

  if (error) {
    return (
      <div className="space-y-6">
        {header}
        <Callout
          tone="danger"
          action={
            <Button variant="ghost" size="xs" onClick={loadStatusPages}>
              Retry
            </Button>
          }
        >
          {error}
        </Callout>
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
          description="Create a public status page to show your customers the availability of your services."
          action={
            canWrite
              ? { label: 'New status page', href: '/status-pages/new', icon: <Plus strokeWidth={1.75} /> }
              : undefined
          }
        />
      ) : (
        <div className="grid gap-4 sm:grid-cols-2 lg:grid-cols-3">
          {statusPages.map((page) => {
            const monitorCount = page.monitor_ids?.length || 0;
            const publicUrl = resolveStatusPagePublicUrl(page);
            return (
              // The title link stretches over the card; the footer controls sit
              // above that overlay (relative) so each keeps its own target.
              <div key={page.id} className="glass glass-hover relative flex flex-col p-5">
                <Link
                  href={`/status-pages/${page.id}`}
                  className="block truncate text-title font-semibold text-white after:absolute after:inset-0 after:rounded-2xl hover:text-cyan-300 focus:outline-none focus-visible:after:ring-2 focus-visible:after:ring-cyan-500/50"
                >
                  {page.title}
                </Link>
                <p className="mt-0.5 truncate font-mono text-xs text-slate-500">/{page.slug}</p>

                <div className="flex-1">
                  {page.description && (
                    <p className="mt-3 line-clamp-2 text-ui text-slate-400">{page.description}</p>
                  )}
                </div>

                <div className="mt-4 flex items-center justify-between gap-2">
                  {monitorCount === 0 ? (
                    <span title="Published, but it lists no monitors">
                      <Pill tone="warning" size="xs">No monitors</Pill>
                    </span>
                  ) : (
                    <span className="text-xs tabular-nums text-slate-500">{pluralize(monitorCount, 'monitor')}</span>
                  )}
                  <div className="relative -mb-1.5 -mr-1.5 flex items-center gap-0.5">
                    <a
                      href={publicUrl}
                      target="_blank"
                      rel="noopener noreferrer"
                      title="Open public page"
                      aria-label={`Open the public page of ${page.title}`}
                      className={ICON_BUTTON_CLASS}
                    >
                      <ArrowUpRight strokeWidth={1.75} />
                    </a>
                    {canWrite && (
                      <>
                        <IconButton
                          icon={<Pencil strokeWidth={1.75} />}
                          label="Edit"
                          href={`/status-pages/${page.id}`}
                        />
                        <IconButton
                          icon={<Trash2 strokeWidth={1.75} />}
                          label="Delete"
                          danger
                          onClick={() => setPendingDelete(page)}
                        />
                      </>
                    )}
                  </div>
                </div>
              </div>
            );
          })}
        </div>
      )}

      <ConfirmDialog
        open={canWrite && pendingDelete !== null}
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
