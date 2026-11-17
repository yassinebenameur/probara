'use client';

import { useCallback, useEffect, useMemo, useState } from 'react';
import { ArrowRightLeft, Building2, Pencil, Plus, RefreshCw, Trash2 } from 'lucide-react';
import Button from '@/components/ui/Button';
import Callout from '@/components/ui/Callout';
import EmptyState from '@/components/ui/EmptyState';
import IconButton from '@/components/ui/IconButton';
import Page from '@/components/ui/Page';
import PageHeader from '@/components/ui/PageHeader';
import Panel from '@/components/ui/Panel';
import Pill from '@/components/ui/Pill';
import { RelativeTime } from '@/components/ui/RelativeTime';
import SortableHeader, { useUrlSort } from '@/components/ui/SortableHeader';
import TableSkeleton from '@/components/ui/TableSkeleton';
import { useToast } from '@/components/ui/ToastProvider';
import { useCurrentUser } from '@/components/providers/CurrentUserProvider';
import { useSelectedTenantId } from '@/components/providers/TenantProvider';
import WorkspaceNameDialog from '@/components/workspaces/WorkspaceNameDialog';
import DeleteWorkspaceDialog from '@/components/workspaces/DeleteWorkspaceDialog';
import { createWorkspace, deleteWorkspace, getWorkspaceOverview, renameWorkspace } from '@/lib/api';
import { hasApiKey } from '@/lib/auth';
import { pluralize } from '@/lib/format';
import { sortRows, type SortState, type SortValue } from '@/lib/sort';
import { emitTenantsChanged, selectTenant } from '@/lib/tenant';
import { useAsyncData } from '@/lib/useAsyncData';
import type { WorkspaceOverview } from '@/lib/types';

const SORT_KEYS = ['name', 'members', 'monitors', 'created'] as const;
type WorkspaceSortKey = (typeof SORT_KEYS)[number];
const DEFAULT_SORT: SortState<WorkspaceSortKey> = { key: 'name', dir: 'asc' };
// Largest and newest first; names A→Z.
const DEFAULT_DIR = { members: 'desc', monitors: 'desc', created: 'desc' } as const;

const SORT_VALUE: Record<WorkspaceSortKey, (w: WorkspaceOverview) => SortValue> = {
  name: (w) => w.name,
  members: (w) => w.member_count,
  monitors: (w) => w.monitor_count,
  created: (w) => Date.parse(w.created_at),
};

const loadOverview = async () => (await getWorkspaceOverview()).items ?? [];

/**
 * Workspace management for superadmins: create, rename and delete. The list
 * is every live workspace with its member and monitor counts; superadmins act
 * in all of them without a membership.
 */
export default function WorkspacesPage() {
  const { showToast } = useToast();
  const { isSuperadmin, loading: userLoading } = useCurrentUser();
  const selectedTenantId = useSelectedTenantId();
  const [apiKeyMode, setApiKeyMode] = useState(false);
  const allowed = !apiKeyMode && !userLoading && isSuperadmin;
  const load = useCallback(
    () => (allowed ? loadOverview() : Promise.resolve<WorkspaceOverview[]>([])),
    [allowed]
  );
  const { data: workspaces, loading, error, reload } = useAsyncData(load, [], 'Failed to load workspaces');
  const [sort, setSort] = useUrlSort(SORT_KEYS, DEFAULT_SORT, DEFAULT_DIR);
  const [creating, setCreating] = useState(false);
  const [renaming, setRenaming] = useState<WorkspaceOverview | null>(null);
  const [deleting, setDeleting] = useState<WorkspaceOverview | null>(null);

  useEffect(() => {
    setApiKeyMode(hasApiKey());
  }, []);

  const sorted = useMemo(
    () => sortRows(workspaces, SORT_VALUE[sort.key], sort.dir, (w) => w.name),
    [workspaces, sort]
  );

  // Every change reloads the switcher's list too (it lives outside this page).
  const create = async (name: string) => {
    const tenant = await createWorkspace(name);
    setCreating(false);
    showToast(`Workspace “${tenant.name}” created`, 'success');
    selectTenant(tenant.id); // remounts this page on the new workspace
    emitTenantsChanged();
  };

  const rename = async (name: string) => {
    if (!renaming) return;
    await renameWorkspace(renaming.id, name);
    setRenaming(null);
    emitTenantsChanged();
    await reload();
  };

  const remove = async (confirmName: string) => {
    if (!deleting) return;
    const wasSelected = deleting.id === selectedTenantId;
    await deleteWorkspace(deleting.id, confirmName);
    setDeleting(null);
    showToast({
      title: 'Workspace deleted',
      message: 'Its data is being erased in the background.',
      type: 'success',
    });
    // The switcher settles on another workspace when this was the selected
    // one, which remounts the page; otherwise only the list changes.
    emitTenantsChanged();
    if (!wasSelected) await reload();
  };

  const header = (
    <PageHeader
      title="Workspaces"
      subtitle="Each workspace has its own monitors, alerting, status pages and members."
      action={
        allowed ? (
          <div className="flex items-center gap-2">
            <Button
              variant="ghost"
              size="sm"
              icon={<RefreshCw strokeWidth={1.75} />}
              onClick={() => reload()}
              loading={loading && workspaces.length > 0}
            >
              Refresh
            </Button>
            <Button variant="accent" size="sm" icon={<Plus strokeWidth={1.75} />} onClick={() => setCreating(true)}>
              New workspace
            </Button>
          </div>
        ) : undefined
      }
    />
  );

  if (apiKeyMode || (!userLoading && !isSuperadmin)) {
    return (
      <Page width="data">
        {header}
        <Panel title="Superadmin required" subtitle="Workspace management is restricted to platform superadmins.">
          <p className="text-sm text-slate-300">
            {apiKeyMode
              ? 'You are connected with an API key, which is pinned to one workspace. Sign in as a superadmin to manage workspaces.'
              : 'Your account does not have superadmin access. Ask a platform administrator if you need a workspace created, renamed or deleted.'}
          </p>
        </Panel>
      </Page>
    );
  }

  return (
    <Page width="data">
      {header}

      {error && (
        <Callout
          tone="danger"
          action={
            <Button variant="ghost" size="xs" onClick={() => reload()} loading={loading}>
              Retry
            </Button>
          }
        >
          {error}
        </Callout>
      )}

      {userLoading || (loading && workspaces.length === 0) ? (
        <TableSkeleton rows={4} columns={4} label="Loading workspaces" />
      ) : workspaces.length === 0 ? (
        error ? null : (
          <EmptyState
            icon={<Building2 strokeWidth={1.5} />}
            title="No workspaces"
            description="Create a workspace to start monitoring."
          />
        )
      ) : (
        <div className="glass overflow-hidden">
          <div className="overflow-x-auto">
            <table className="data-table text-sm">
              <thead>
                <tr>
                  <SortableHeader label="Name" sortKey="name" sort={sort} onSort={setSort} defaultDir={DEFAULT_DIR} />
                  <SortableHeader
                    label="Members"
                    sortKey="members"
                    sort={sort}
                    onSort={setSort}
                    defaultDir={DEFAULT_DIR}
                    align="right"
                    title="Superadmins act in every workspace and are not counted"
                  />
                  <SortableHeader label="Monitors" sortKey="monitors" sort={sort} onSort={setSort} defaultDir={DEFAULT_DIR} align="right" />
                  <SortableHeader label="Created" sortKey="created" sort={sort} onSort={setSort} defaultDir={DEFAULT_DIR} />
                  <th><span className="sr-only">Actions</span></th>
                </tr>
              </thead>
              <tbody>
                {sorted.map((w) => {
                  const current = w.id === selectedTenantId;
                  return (
                    <tr key={w.id}>
                      <td>
                        <div className="flex items-center gap-2">
                          <span className="font-medium text-white">{w.name}</span>
                          {current && <Pill size="xs">Current</Pill>}
                          {w.protected && <Pill tone="info" size="xs">Default</Pill>}
                        </div>
                      </td>
                      <td className="text-right text-ui tabular-nums text-slate-400">{w.member_count}</td>
                      <td className="text-right text-ui tabular-nums text-slate-400">{w.monitor_count}</td>
                      <td className="whitespace-nowrap text-ui tabular-nums text-slate-400">
                        <RelativeTime value={w.created_at} />
                      </td>
                      <td>
                        <div className="flex items-center justify-end gap-0.5">
                          <IconButton
                            icon={<ArrowRightLeft strokeWidth={1.75} />}
                            label={current ? 'Current workspace' : 'Switch to this workspace'}
                            onClick={() => selectTenant(w.id)}
                            disabled={current}
                          />
                          <IconButton icon={<Pencil strokeWidth={1.75} />} label="Rename" onClick={() => setRenaming(w)} />
                          <IconButton
                            icon={<Trash2 strokeWidth={1.75} />}
                            label={w.protected ? 'The default workspace cannot be deleted' : 'Delete'}
                            onClick={() => setDeleting(w)}
                            disabled={w.protected}
                            danger
                          />
                        </div>
                      </td>
                    </tr>
                  );
                })}
              </tbody>
            </table>
          </div>
          <div className="border-t border-[var(--input-edge)] px-4 py-2.5 text-xs text-slate-500">
            {pluralize(workspaces.length, 'workspace')}
          </div>
        </div>
      )}

      {creating && (
        <WorkspaceNameDialog
          title="New workspace"
          description="An empty workspace with its own monitors, alerting, status pages and members. You switch to it once it is created."
          submitLabel="Create workspace"
          onSubmit={create}
          onClose={() => setCreating(false)}
        />
      )}
      {renaming && (
        <WorkspaceNameDialog
          title="Rename workspace"
          submitLabel="Rename"
          initialName={renaming.name}
          onSubmit={rename}
          onClose={() => setRenaming(null)}
        />
      )}
      {deleting && (
        <DeleteWorkspaceDialog workspace={deleting} onConfirm={remove} onClose={() => setDeleting(null)} />
      )}
    </Page>
  );
}
