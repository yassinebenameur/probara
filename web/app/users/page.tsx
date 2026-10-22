'use client';

import Link from 'next/link';
import { useRouter } from 'next/navigation';
import { useCallback, useEffect, useMemo, useState } from 'react';
import { Pencil, Plus, RefreshCw, Trash2, Users as UsersIcon } from 'lucide-react';
import Panel from '@/components/ui/Panel';
import Button from '@/components/ui/Button';
import Callout from '@/components/ui/Callout';
import IconButton from '@/components/ui/IconButton';
import Pill from '@/components/ui/Pill';
import PageHeader from '@/components/ui/PageHeader';
import Page from '@/components/ui/Page';
import SortableHeader, { useUrlSort } from '@/components/ui/SortableHeader';
import TableSkeleton from '@/components/ui/TableSkeleton';
import { RelativeTime } from '@/components/ui/RelativeTime';
import EmptyState from '@/components/ui/EmptyState';
import ConfirmDialog from '@/components/ui/ConfirmDialog';
import { useToast } from '@/components/ui/ToastProvider';
import { clearApiKey, hasApiKey } from '@/lib/auth';
import { useCurrentUser } from '@/components/providers/CurrentUserProvider';
import { deleteUser, getUsers } from '@/lib/api';
import { pluralize } from '@/lib/format';
import { sortRows, type SortState, type SortValue } from '@/lib/sort';
import type { AdminUser } from '@/lib/types';

const PAGE_SIZE = 50;
// The API caps page_size at 100; the page reads every account (a platform
// has tens to hundreds) so a column sort orders all of them, not one page.
const API_PAGE = 100;

const SORT_KEYS = ['username', 'email', 'role', 'sign_in', 'last_login'] as const;
type UserSortKey = (typeof SORT_KEYS)[number];
const DEFAULT_SORT: SortState<UserSortKey> = { key: 'username', dir: 'asc' };
// Most recent sign-in first; everything else A→Z (superadmins lead Role).
const DEFAULT_DIR = { last_login: 'desc' } as const;

const SORT_VALUE: Record<UserSortKey, (user: AdminUser) => SortValue> = {
  username: (user) => user.username,
  email: (user) => user.email || null,
  role: (user) => (user.platform_role === 'superadmin' ? 0 : 1),
  sign_in: (user) => (user.auth_method === 'oidc' ? 'SSO' : 'Password'),
  last_login: (user) => (user.last_login_at ? Date.parse(user.last_login_at) : null),
};

async function fetchAllUsers(): Promise<AdminUser[]> {
  const all: AdminUser[] = [];
  for (let page = 1; ; page++) {
    const response = await getUsers({ page, page_size: API_PAGE });
    const batch = response.items || [];
    all.push(...batch);
    if (batch.length < API_PAGE || all.length >= (response.total || 0)) return all;
  }
}

export default function UsersPage() {
  const router = useRouter();
  const { showToast } = useToast();
  const { isSuperadmin, loading: userLoading } = useCurrentUser();
  const [apiKeyMode, setApiKeyMode] = useState(false);
  const [users, setUsers] = useState<AdminUser[]>([]);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState('');
  const [loaded, setLoaded] = useState(false);
  const [page, setPage] = useState(1);
  const [sort, setSort] = useUrlSort(SORT_KEYS, DEFAULT_SORT, DEFAULT_DIR);
  const [pendingDelete, setPendingDelete] = useState<AdminUser | null>(null);
  const [deleting, setDeleting] = useState(false);

  // A failed refresh keeps the accounts already listed; only the first load
  // has nothing to fall back on.
  const loadUsers = useCallback(async () => {
    try {
      setLoading(true);
      setError('');
      setUsers(await fetchAllUsers());
      setLoaded(true);
    } catch (err: any) {
      setError(err.message || 'Failed to load users');
    } finally {
      setLoading(false);
    }
  }, []);

  useEffect(() => {
    const usingApiKey = hasApiKey();
    setApiKeyMode(usingApiKey);
    if (!usingApiKey) {
      loadUsers();
    } else {
      setLoading(false);
    }
  }, [loadUsers]);

  // Sort everything, then page.
  const sorted = useMemo(
    () => sortRows(users, SORT_VALUE[sort.key], sort.dir, (user) => user.username),
    [users, sort]
  );
  const totalPages = Math.max(1, Math.ceil(sorted.length / PAGE_SIZE));
  const currentPage = Math.min(page, totalPages);
  const visible = sorted.slice((currentPage - 1) * PAGE_SIZE, currentPage * PAGE_SIZE);
  const changeSort = (next: SortState<UserSortKey>) => {
    setSort(next);
    setPage(1);
  };

  const handleDelete = async () => {
    if (!pendingDelete) return;
    setDeleting(true);
    try {
      await deleteUser(pendingDelete.id);
      await loadUsers();
      showToast('User deleted', 'success');
      setPendingDelete(null);
    } catch (err: any) {
      showToast(err.message || 'Failed to delete user', 'error');
    } finally {
      setDeleting(false);
    }
  };

  const handleSwitchToAdmin = () => {
    clearApiKey();
    router.push('/login');
  };

  const header = (
    <PageHeader
      title="Users"
      subtitle="Platform accounts and the role each one has in every workspace."
      action={
        !apiKeyMode && isSuperadmin && !userLoading ? (
          <div className="flex items-center gap-2">
            <Button
              variant="ghost"
              size="sm"
              icon={<RefreshCw strokeWidth={1.75} />}
              onClick={() => loadUsers()}
              loading={loading && loaded}
            >
              Refresh
            </Button>
            <Button variant="accent" size="sm" icon={<Plus strokeWidth={1.75} />} asChild>
              <Link href="/users/new">New user</Link>
            </Button>
          </div>
        ) : undefined
      }
    />
  );

  if (apiKeyMode) {
    return (
      <Page width="data">
        {header}
        <Panel title="Admin session required" subtitle="User management is available only for admin login mode.">
          <div className="space-y-3">
            <p className="text-sm text-slate-300">
              You are currently connected with an API key. Switch to admin login to manage users.
            </p>
            <Button variant="ghost" size="sm" onClick={handleSwitchToAdmin}>
              Go to admin login
            </Button>
          </div>
        </Panel>
      </Page>
    );
  }

  if (!userLoading && !isSuperadmin) {
    return (
      <Page width="data">
        {header}
        <Panel title="Superadmin required" subtitle="User management is restricted to platform superadmins.">
          <p className="text-sm text-slate-300">
            Your account does not have superadmin access. Ask a platform administrator if you
            need to manage users.
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
            <Button variant="ghost" size="xs" onClick={() => loadUsers()} loading={loading}>
              Retry
            </Button>
          }
        >
          {error}
        </Callout>
      )}

      {!loaded ? (
        loading ? <TableSkeleton rows={5} columns={4} label="Loading users" /> : null
      ) : users.length === 0 ? (
        <EmptyState
          icon={<UsersIcon strokeWidth={1.5} />}
          title="No users yet"
          description="Add admin accounts to allow more team members to log in."
          action={
            <Button variant="ghost" size="sm" icon={<Plus strokeWidth={1.75} />} asChild>
              <Link href="/users/new">New user</Link>
            </Button>
          }
        />
      ) : (
        <div
          className={`glass overflow-hidden transition-opacity duration-base ${
            loading ? 'pointer-events-none opacity-60 delay-300' : 'delay-0'
          }`}
          aria-busy={loading || undefined}
        >
          <div className="overflow-x-auto">
            <table className="data-table text-sm">
              <thead>
                <tr>
                  <SortableHeader label="Username" sortKey="username" sort={sort} onSort={changeSort} defaultDir={DEFAULT_DIR} />
                  <SortableHeader label="Email" sortKey="email" sort={sort} onSort={changeSort} defaultDir={DEFAULT_DIR} />
                  <SortableHeader
                    label="Role"
                    sortKey="role"
                    sort={sort}
                    onSort={changeSort}
                    defaultDir={DEFAULT_DIR}
                    title="Superadmins first"
                  />
                  <SortableHeader label="Sign-in" sortKey="sign_in" sort={sort} onSort={changeSort} defaultDir={DEFAULT_DIR} />
                  <th>Tenants</th>
                  <SortableHeader
                    label="Last login"
                    sortKey="last_login"
                    sort={sort}
                    onSort={changeSort}
                    defaultDir={DEFAULT_DIR}
                    title="Most recent first"
                  />
                  <th><span className="sr-only">Actions</span></th>
                </tr>
              </thead>
              <tbody>
                {visible.map((user) => (
                  <tr key={user.id}>
                    <td className="font-medium text-white">{user.username}</td>
                    <td className="text-ui text-slate-400">{user.email || '—'}</td>
                    <td>
                      {user.platform_role === 'superadmin' ? (
                        <Pill tone="info" size="xs">Superadmin</Pill>
                      ) : (
                        <span className="text-ui text-slate-400">Member</span>
                      )}
                    </td>
                    <td className="text-ui text-slate-400">
                      {user.auth_method === 'oidc' ? 'SSO' : 'Password'}
                    </td>
                    <td className="text-ui text-slate-400">
                      {user.platform_role === 'superadmin'
                        ? 'All'
                        : (user.memberships || [])
                            .map((m) => `${m.tenant_name || m.tenant_id} (${m.role})`)
                            .join(', ') || '—'}
                    </td>
                    <td className="whitespace-nowrap text-ui tabular-nums text-slate-400">
                      <RelativeTime value={user.last_login_at} fallback="Never" />
                    </td>
                    <td>
                      <div className="flex items-center justify-end gap-0.5">
                        <IconButton icon={<Pencil strokeWidth={1.75} />} label="Edit" href={`/users/${user.id}`} />
                        <IconButton
                          icon={<Trash2 strokeWidth={1.75} />}
                          label="Delete"
                          onClick={() => setPendingDelete(user)}
                          danger
                        />
                      </div>
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>

          <div className="flex items-center justify-between gap-3 border-t border-divider px-4 py-3 text-xs text-slate-500">
            <span className="tabular-nums">
              {totalPages > 1 ? `Page ${currentPage} of ${totalPages} · ` : ''}
              {pluralize(sorted.length, 'user')}
            </span>
            {totalPages > 1 && (
              <div className="flex items-center gap-2">
                <Button
                  variant="ghost"
                  size="xs"
                  disabled={currentPage <= 1}
                  onClick={() => setPage(currentPage - 1)}
                >
                  Previous
                </Button>
                <Button
                  variant="ghost"
                  size="xs"
                  disabled={currentPage >= totalPages}
                  onClick={() => setPage(currentPage + 1)}
                >
                  Next
                </Button>
              </div>
            )}
          </div>
        </div>
      )}

      <ConfirmDialog
        open={pendingDelete !== null}
        title="Delete user"
        description={
          pendingDelete
            ? `“${pendingDelete.username}” will lose access immediately. This cannot be undone.`
            : undefined
        }
        confirmLabel="Delete user"
        loading={deleting}
        onConfirm={handleDelete}
        onCancel={() => !deleting && setPendingDelete(null)}
      />
    </Page>
  );
}
