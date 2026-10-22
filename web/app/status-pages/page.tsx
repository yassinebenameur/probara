'use client';

import { useState, useEffect, useMemo } from 'react';
import Link from 'next/link';
import { ArrowUpRight, Globe, Pencil, Plus, Trash2 } from 'lucide-react';
import { StatusPage } from '@/lib/types';
import { getStatusPages, deleteStatusPage } from '@/lib/api';
import { resolveStatusPagePublicUrl } from '@/lib/statusPageUrl';
import { pluralize } from '@/lib/format';
import { sortRows, type SortState, type SortValue } from '@/lib/sort';
import Button from '@/components/ui/Button';
import IconButton from '@/components/ui/IconButton';
import Pill from '@/components/ui/Pill';
import PageHeader from '@/components/ui/PageHeader';
import Page from '@/components/ui/Page';
import Select from '@/components/ui/Select';
import { useUrlSort } from '@/components/ui/SortableHeader';
import EmptyState from '@/components/ui/EmptyState';
import Callout from '@/components/ui/Callout';
import ConfirmDialog from '@/components/ui/ConfirmDialog';
import { useToast } from '@/components/ui/ToastProvider';
import { useCanWrite } from '@/components/providers/CurrentUserProvider';

const GRID = 'sm:grid-cols-2 lg:grid-cols-3 3xl:grid-cols-4 4xl:grid-cols-5';

const SORT_KEYS = ['name', 'monitors', 'updated'] as const;
type StatusPageSortKey = (typeof SORT_KEYS)[number];
const DEFAULT_SORT: SortState<StatusPageSortKey> = { key: 'name', dir: 'asc' };
const DEFAULT_DIR = { monitors: 'desc', updated: 'desc' } as const;

// Cards have no column headers, so the order is a select (same URL state).
const SORT_OPTIONS: { value: string; label: string; sort: SortState<StatusPageSortKey> }[] = [
  { value: 'name:asc', label: 'Name, A–Z', sort: { key: 'name', dir: 'asc' } },
  { value: 'name:desc', label: 'Name, Z–A', sort: { key: 'name', dir: 'desc' } },
  { value: 'monitors:desc', label: 'Most monitors', sort: { key: 'monitors', dir: 'desc' } },
  { value: 'monitors:asc', label: 'Fewest monitors', sort: { key: 'monitors', dir: 'asc' } },
  { value: 'updated:desc', label: 'Recently updated', sort: { key: 'updated', dir: 'desc' } },
];

const SORT_VALUE: Record<StatusPageSortKey, (page: StatusPage) => SortValue> = {
  name: (page) => page.title,
  monitors: (page) => page.monitor_ids?.length || 0,
  updated: (page) => Date.parse(page.updated_at),
};

export default function StatusPagesPage() {
  const { showToast } = useToast();
  const canWrite = useCanWrite();
  const [statusPages, setStatusPages] = useState<StatusPage[]>([]);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string>('');
  const [pendingDelete, setPendingDelete] = useState<StatusPage | null>(null);
  const [deleting, setDeleting] = useState(false);
  const [sort, setSort] = useUrlSort(SORT_KEYS, DEFAULT_SORT, DEFAULT_DIR);
  const sorted = useMemo(
    () => sortRows(statusPages, SORT_VALUE[sort.key], sort.dir, (page) => page.title),
    [statusPages, sort]
  );

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

  const sortControl =
    statusPages.length > 1 ? (
      <div className="flex items-center gap-2">
        <label htmlFor="status-pages-sort" className="text-xs font-medium text-slate-400">
          Sort
        </label>
        <Select
          id="status-pages-sort"
          className="input-xs w-auto rounded-lg py-0 pl-2.5 text-xs"
          value={`${sort.key}:${sort.dir}`}
          onChange={(e) => {
            const option = SORT_OPTIONS.find((o) => o.value === e.target.value);
            if (option) setSort(option.sort);
          }}
        >
          {SORT_OPTIONS.map((option) => (
            <option key={option.value} value={option.value}>
              {option.label}
            </option>
          ))}
        </Select>
      </div>
    ) : null;

  // While the list is empty the empty state carries the one CTA.
  const header = (
    <PageHeader
      title="Status pages"
      subtitle="Public pages that show your customers live service health."
      action={
        sortControl || (canWrite && !loading && !error && statusPages.length > 0) ? (
          <>
            {sortControl}
            {canWrite && !loading && !error && statusPages.length > 0 && (
              <Button variant="accent" size="sm" icon={<Plus strokeWidth={1.75} />} asChild>
                <Link href="/status-pages/new">New status page</Link>
              </Button>
            )}
          </>
        ) : undefined
      }
    />
  );

  if (loading && statusPages.length === 0) {
    return (
      <Page width="data">
        {header}
        <div
          role="status"
          aria-label="Loading status pages"
          className={`grid animate-pulse gap-4 ${GRID}`}
        >
          {[0, 1, 2].map((i) => (
            <div key={i} className="glass h-[146px]" aria-hidden="true" />
          ))}
        </div>
      </Page>
    );
  }

  if (error && statusPages.length === 0) {
    return (
      <Page width="data">
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
      </Page>
    );
  }

  return (
    <Page width="data">
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
        <div className={`grid gap-4 ${GRID}`}>
          {sorted.map((page) => {
            const monitorCount = page.monitor_ids?.length || 0;
            const publicUrl = resolveStatusPagePublicUrl(page);
            return (
              // The title link stretches over the card; the footer controls sit
              // above that overlay (relative) so each keeps its own target.
              <div key={page.id} className="glass glass-hover relative flex flex-col p-5">
                <Link
                  href={`/status-pages/${page.id}`}
                  className="block truncate text-title font-semibold text-white after:absolute after:inset-0 after:rounded-2xl hover:text-cyan-300 focus:outline-none focus-visible:after:ring-2 focus-visible:after:ring-focus"
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
                    <IconButton
                      icon={<ArrowUpRight strokeWidth={1.75} />}
                      label={`Open the public page of ${page.title}`}
                      href={publicUrl}
                      external
                    />
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
    </Page>
  );
}
