'use client';

import Link from 'next/link';
import { useRouter } from 'next/navigation';
import { useEffect, useMemo, useState } from 'react';
import { Pencil, Plus, RefreshCw, Trash2, Users as UsersIcon } from 'lucide-react';
import Panel from '@/components/ui/Panel';
import Button from '@/components/ui/Button';
import Callout from '@/components/ui/Callout';
import IconButton from '@/components/ui/IconButton';
import Pill from '@/components/ui/Pill';
import PageHeader from '@/components/ui/PageHeader';
import EmptyState from '@/components/ui/EmptyState';
import ConfirmDialog from '@/components/ui/ConfirmDialog';
import { useToast } from '@/components/ui/ToastProvider';
import { clearApiKey, hasApiKey } from '@/lib/auth';
import { useCurrentUser } from '@/components/providers/CurrentUserProvider';
import { deleteUser, getUsers } from '@/lib/api';
import { formatDateTime } from '@/lib/format';
import type { AdminUser } from '@/lib/types';

const PAGE_SIZE = 20;

export default function UsersPage() {
  const router = useRouter();
  const { showToast } = useToast();
  const { isSuperadmin, loading: userLoading } = useCurrentUser();
  const [apiKeyMode, setApiKeyMode] = useState(false);
  const [users, setUsers] = useState<AdminUser[]>([]);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState('');
  const [page, setPage] = useState(1);
  const [total, setTotal] = useState(0);
  const [pendingDelete, setPendingDelete] = useState<AdminUser | null>(null);
  const [deleting, setDeleting] = useState(false);

  const totalPages = useMemo(() => Math.max(1, Math.ceil(total / PAGE_SIZE)), [total]);

  const loadUsers = async (targetPage = page) => {
    try {
      setLoading(true);
      setError('');
      const response = await getUsers({ page: targetPage, page_size: PAGE_SIZE });
      setUsers(response.items || []);
      setTotal(response.total || 0);
      setPage(response.page || targetPage);
    } catch (err: any) {
      setError(err.message || 'Failed to load users');
      setUsers([]);
      setTotal(0);
    } finally {
      setLoading(false);
    }
  };

  useEffect(() => {
    const usingApiKey = hasApiKey();
    setApiKeyMode(usingApiKey);
    if (!usingApiKey) {
      loadUsers(1);
    } else {
      setLoading(false);
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);

  const handleDelete = async () => {
    if (!pendingDelete) return;
    setDeleting(true);
    try {
      await deleteUser(pendingDelete.id);
      await loadUsers(page);
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
              onClick={() => loadUsers(page)}
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
      <div className="space-y-6">
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
      </div>
    );
  }

  if (!userLoading && !isSuperadmin) {
    return (
      <div className="space-y-6">
        {header}
        <Panel title="Superadmin required" subtitle="User management is restricted to platform superadmins.">
          <p className="text-sm text-slate-300">
            Your account does not have superadmin access. Ask a platform administrator if you
            need to manage users.
          </p>
        </Panel>
      </div>
    );
  }

  return (
    <div className="space-y-6">
      {header}

      {loading ? (
        <p className="text-sm text-slate-500">Loading users…</p>
      ) : error ? (
        <Callout
          tone="danger"
          action={<Button variant="ghost" size="xs" onClick={() => loadUsers(page)}>Retry</Button>}
        >
          {error}
        </Callout>
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
        <div className="glass overflow-hidden">
          <div className="overflow-x-auto">
            <table className="data-table text-sm">
              <thead>
                <tr>
                  <th>Username</th>
                  <th>Email</th>
                  <th>Role</th>
                  <th>Sign-in</th>
                  <th>Tenants</th>
                  <th>Last login</th>
                  <th><span className="sr-only">Actions</span></th>
                </tr>
              </thead>
              <tbody>
                {users.map((user) => (
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
                    <td className="whitespace-nowrap text-ui text-slate-400">
                      {formatDateTime(user.last_login_at)}
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
              Page {page} of {totalPages} ({total} users)
            </span>
            <div className="flex items-center gap-2">
              <Button
                variant="ghost"
                size="xs"
                disabled={page <= 1}
                onClick={() => loadUsers(page - 1)}
              >
                Previous
              </Button>
              <Button
                variant="ghost"
                size="xs"
                disabled={page >= totalPages}
                onClick={() => loadUsers(page + 1)}
              >
                Next
              </Button>
            </div>
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
    </div>
  );
}
