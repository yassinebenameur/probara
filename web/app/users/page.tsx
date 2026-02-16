'use client';

import Link from 'next/link';
import { useRouter } from 'next/navigation';
import { useEffect, useMemo, useState } from 'react';
import Panel from '@/components/ui/Panel';
import Toast from '@/components/ui/Toast';
import { clearApiKey, hasApiKey } from '@/lib/auth';
import { deleteUser, getUsers } from '@/lib/api';
import type { AdminUser } from '@/lib/types';

type ToastState = { message: string; type: 'success' | 'error' } | null;

const PAGE_SIZE = 20;

function formatDate(value?: string): string {
  if (!value) return '-';
  return new Date(value).toLocaleString();
}

export default function UsersPage() {
  const router = useRouter();
  const [apiKeyMode, setApiKeyMode] = useState(false);
  const [users, setUsers] = useState<AdminUser[]>([]);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState('');
  const [page, setPage] = useState(1);
  const [total, setTotal] = useState(0);
  const [deletingId, setDeletingId] = useState<string | null>(null);
  const [toast, setToast] = useState<ToastState>(null);

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

  const handleDelete = async (user: AdminUser) => {
    if (!confirm(`Delete user "${user.username}"? This action cannot be undone.`)) {
      return;
    }

    try {
      setDeletingId(user.id);
      await deleteUser(user.id);
      await loadUsers(page);
      setToast({ message: 'User deleted', type: 'success' });
    } catch (err: any) {
      setToast({ message: err.message || 'Failed to delete user', type: 'error' });
    } finally {
      setDeletingId(null);
    }
  };

  const handleSwitchToAdmin = () => {
    clearApiKey();
    router.push('/login');
  };

  if (apiKeyMode) {
    return (
      <div className="space-y-6">
        <div>
          <h1 className="text-xl font-semibold text-white">Users</h1>
          <p className="text-sm text-muted">Manage platform admin accounts.</p>
        </div>
        <Panel title="Admin Session Required" subtitle="User management is available only for admin login mode.">
          <div className="space-y-3">
            <p className="text-sm text-slate-300">
              You are currently connected with an API key. Switch to admin login to manage users.
            </p>
            <button type="button" onClick={handleSwitchToAdmin} className="btn btn-primary btn-sm">
              Go to Admin Login
            </button>
          </div>
        </Panel>
      </div>
    );
  }

  return (
    <div className="space-y-6">
      <div>
        <h1 className="text-xl font-semibold text-white">Users</h1>
        <p className="text-sm text-muted">Manage platform admin accounts.</p>
      </div>

      <Panel
        title="Admin Users"
        subtitle="Create, update, and remove admin accounts."
        actions={(
          <div className="flex items-center gap-2">
            <button onClick={() => loadUsers(page)} className="btn btn-secondary btn-sm">
              Refresh
            </button>
            <Link href="/users/new" className="btn btn-primary btn-sm">
              Add User
            </Link>
          </div>
        )}
      >
        {loading ? (
          <p className="text-sm text-muted">Loading users...</p>
        ) : error ? (
          <div className="space-y-3">
            <p className="text-sm text-rose-400">{error}</p>
            <button onClick={() => loadUsers(page)} className="btn btn-danger btn-sm">
              Retry
            </button>
          </div>
        ) : users.length === 0 ? (
          <p className="text-sm text-muted">No users found.</p>
        ) : (
          <div className="space-y-3">
            <div className="overflow-x-auto">
              <table className="min-w-full divide-y divide-white/[0.06]">
                <thead>
                  <tr className="text-left text-xs uppercase tracking-wide text-slate-400">
                    <th className="px-3 py-2">Username</th>
                    <th className="px-3 py-2">Created</th>
                    <th className="px-3 py-2">Updated</th>
                    <th className="px-3 py-2">Last Login</th>
                    <th className="px-3 py-2">Actions</th>
                  </tr>
                </thead>
                <tbody className="divide-y divide-white/[0.04] text-sm text-slate-200">
                  {users.map((user) => (
                    <tr key={user.id}>
                      <td className="px-3 py-3">{user.username}</td>
                      <td className="px-3 py-3 text-xs text-slate-400">{formatDate(user.created_at)}</td>
                      <td className="px-3 py-3 text-xs text-slate-400">{formatDate(user.updated_at)}</td>
                      <td className="px-3 py-3 text-xs text-slate-400">{formatDate(user.last_login_at)}</td>
                      <td className="px-3 py-3">
                        <div className="flex items-center gap-2">
                          <Link href={`/users/${user.id}`} className="btn btn-secondary btn-sm">
                            Edit
                          </Link>
                          <button
                            type="button"
                            className="btn btn-danger btn-sm disabled:opacity-50"
                            onClick={() => handleDelete(user)}
                            disabled={deletingId === user.id}
                          >
                            {deletingId === user.id ? 'Deleting...' : 'Delete'}
                          </button>
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
                <button
                  type="button"
                  className="btn btn-secondary btn-sm disabled:opacity-50"
                  disabled={page <= 1}
                  onClick={() => loadUsers(page - 1)}
                >
                  Previous
                </button>
                <button
                  type="button"
                  className="btn btn-secondary btn-sm disabled:opacity-50"
                  disabled={page >= totalPages}
                  onClick={() => loadUsers(page + 1)}
                >
                  Next
                </button>
              </div>
            </div>
          </div>
        )}
      </Panel>

      {toast && (
        <Toast message={toast.message} type={toast.type} onClose={() => setToast(null)} />
      )}
    </div>
  );
}
