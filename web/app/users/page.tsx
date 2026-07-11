'use client';

import Link from 'next/link';
import { useRouter } from 'next/navigation';
import { useEffect, useMemo, useState } from 'react';
import { Pencil, Plus, RefreshCw, Trash2, Users as UsersIcon } from 'lucide-react';
import Panel from '@/components/ui/Panel';
import Button from '@/components/ui/Button';
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
      subtitle="Manage platform admin accounts."
      action={
        !apiKeyMode ? (
          <div className="flex items-center gap-2">
            <Button
              variant="ghost"
              size="sm"
              icon={<RefreshCw strokeWidth={1.75} />}
              onClick={() => loadUsers(page)}
            >
              Refresh
            </Button>
            <Button variant="ghost" size="sm" icon={<Plus strokeWidth={1.75} />} asChild>
              <Link href="/users/new">Add user</Link>
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

      <Panel
        title="Admin users"
        subtitle="Create, update, and remove admin accounts."
      >
        {loading ? (
          <p className="text-sm text-slate-500">Loading users…</p>
        ) : error ? (
          <div className="space-y-3">
            <p className="text-sm text-rose-400">{error}</p>
            <Button variant="ghost" size="sm" onClick={() => loadUsers(page)}>Retry</Button>
          </div>
        ) : users.length === 0 ? (
          <EmptyState
            icon={<UsersIcon strokeWidth={1.5} />}
            title="No users yet"
            description="Add admin accounts to allow more team members to log in."
            action={
              <Button variant="ghost" size="sm" icon={<Plus strokeWidth={1.75} />} asChild>
                <Link href="/users/new">Add user</Link>
              </Button>
            }
          />
        ) : (
          <div className="space-y-3">
            <div className="overflow-x-auto">
              <table className="min-w-full divide-y divide-white/[0.06]">
                <thead>
                  <tr className="text-left text-xs uppercase tracking-wide text-slate-400">
                    <th className="px-3 py-2">Username</th>
                    <th className="px-3 py-2">Email</th>
                    <th className="px-3 py-2">Role</th>
                    <th className="px-3 py-2">Sign-in</th>
                    <th className="px-3 py-2">Tenants</th>
                    <th className="px-3 py-2">Last login</th>
                    <th className="px-3 py-2">Actions</th>
                  </tr>
                </thead>
                <tbody className="divide-y divide-white/[0.04] text-sm text-slate-200">
                  {users.map((user) => (
                    <tr key={user.id}>
                      <td className="px-3 py-3">{user.username}</td>
                      <td className="px-3 py-3 text-xs text-slate-400">{user.email || '—'}</td>
                      <td className="px-3 py-3">
                        <Pill tone={user.platform_role === 'superadmin' ? 'info' : 'neutral'}>
                          {user.platform_role === 'superadmin' ? 'Superadmin' : 'Member'}
                        </Pill>
                      </td>
                      <td className="px-3 py-3">
                        <Pill tone={user.auth_method === 'oidc' ? 'info' : 'neutral'}>
                          {user.auth_method === 'oidc' ? 'SSO' : 'Password'}
                        </Pill>
                      </td>
                      <td className="px-3 py-3 text-xs text-slate-400">
                        {user.platform_role === 'superadmin'
                          ? 'All'
                          : (user.memberships || [])
                              .map((m) => `${m.tenant_name || m.tenant_id} (${m.role})`)
                              .join(', ') || '—'}
                      </td>
                      <td className="px-3 py-3 text-xs text-slate-400">{formatDateTime(user.last_login_at)}</td>
                      <td className="px-3 py-3">
                        <div className="flex items-center gap-1.5">
                          <Button variant="ghost" size="xs" icon={<Pencil strokeWidth={1.75} />} asChild>
                            <Link href={`/users/${user.id}`}>Edit</Link>
                          </Button>
                          <Button
                            variant="danger"
                            size="xs"
                            icon={<Trash2 strokeWidth={1.75} />}
                            onClick={() => setPendingDelete(user)}
                          >
                            Delete
                          </Button>
                        </div>
                      </td>
                    </tr>
                  ))}
                </tbody>
              </table>
            </div>

            <div className="flex items-center justify-between text-xs text-slate-400">
              <span>
                Page {page} of {totalPages} ({total} users)
              </span>
              <div className="flex items-center gap-2">
                <Button
                  variant="ghost"
                  size="sm"
                  disabled={page <= 1}
                  onClick={() => loadUsers(page - 1)}
                >
                  Previous
                </Button>
                <Button
                  variant="ghost"
                  size="sm"
                  disabled={page >= totalPages}
                  onClick={() => loadUsers(page + 1)}
                >
                  Next
                </Button>
              </div>
            </div>
          </div>
        )}
      </Panel>

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
