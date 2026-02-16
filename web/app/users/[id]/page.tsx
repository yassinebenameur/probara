'use client';

import Link from 'next/link';
import { useParams, useRouter } from 'next/navigation';
import { useEffect, useState } from 'react';
import Toast from '@/components/ui/Toast';
import { getUser, updateUser } from '@/lib/api';
import type { AdminUser } from '@/lib/types';

type ToastState = { message: string; type: 'success' | 'error' } | null;

function formatDate(value?: string): string {
  if (!value) return '-';
  return new Date(value).toLocaleString();
}

export default function EditUserPage() {
  const router = useRouter();
  const params = useParams();
  const id = params.id as string;

  const [user, setUser] = useState<AdminUser | null>(null);
  const [username, setUsername] = useState('');
  const [password, setPassword] = useState('');
  const [loading, setLoading] = useState(true);
  const [saving, setSaving] = useState(false);
  const [error, setError] = useState('');
  const [toast, setToast] = useState<ToastState>(null);

  useEffect(() => {
    const load = async () => {
      try {
        setLoading(true);
        setError('');
        const data = await getUser(id);
        setUser(data);
        setUsername(data.username);
      } catch (err: any) {
        setError(err.message || 'Failed to load user');
      } finally {
        setLoading(false);
      }
    };
    load();
  }, [id]);

  const handleSubmit = async (event: React.FormEvent) => {
    event.preventDefault();
    const payload: { username?: string; password?: string } = {};
    if (username.trim()) payload.username = username.trim();
    if (password) payload.password = password;

    if (!payload.username && !payload.password) {
      setToast({ message: 'Provide at least one field to update', type: 'error' });
      return;
    }

    try {
      setSaving(true);
      const updated = await updateUser(id, payload);
      setUser(updated);
      setUsername(updated.username);
      setPassword('');
      setToast({ message: 'User updated successfully', type: 'success' });
    } catch (err: any) {
      setToast({ message: err.message || 'Failed to update user', type: 'error' });
    } finally {
      setSaving(false);
    }
  };

  if (loading) {
    return (
      <div className="flex h-64 items-center justify-center">
        <div className="text-slate-500">Loading user...</div>
      </div>
    );
  }

  if (error || !user) {
    return (
      <div className="rounded-xl border border-white/[0.06] bg-slate-900/50 p-6">
        <div className="rounded-lg border border-rose-500/20 bg-rose-500/10 px-4 py-3 text-sm text-rose-400">
          {error || 'User not found'}
        </div>
      </div>
    );
  }

  return (
    <div className="max-w-4xl">
      <div className="mb-6">
        <div className="flex items-center gap-2 text-xs text-slate-500">
          <Link href="/users" className="hover:text-slate-400">Users</Link>
          <svg className="h-3 w-3" fill="none" viewBox="0 0 24 24" stroke="currentColor">
            <path strokeLinecap="round" strokeLinejoin="round" strokeWidth={2} d="M9 5l7 7-7 7" />
          </svg>
          <span className="text-slate-400">Edit</span>
        </div>
        <h1 className="mt-3 text-xl font-semibold text-white">Edit User</h1>
        <p className="mt-1 text-sm text-slate-500">
          Update username and reset password for this admin account.
        </p>
      </div>

      <div className="grid gap-6 lg:grid-cols-[1fr_300px]">
        <div className="rounded-xl border border-white/[0.06] bg-slate-900/50 p-6">
          <form onSubmit={handleSubmit} className="space-y-4">
            <div>
              <label className="mb-1.5 block text-xs font-medium text-slate-400">Username</label>
              <input
                type="text"
                className="input"
                value={username}
                onChange={(event) => setUsername(event.target.value)}
              />
            </div>
            <div>
              <label className="mb-1.5 block text-xs font-medium text-slate-400">New Password</label>
              <input
                type="password"
                className="input"
                placeholder="Leave empty to keep current password"
                value={password}
                onChange={(event) => setPassword(event.target.value)}
              />
            </div>

            <div className="flex items-center gap-2 pt-2">
              <button type="submit" className="btn btn-primary btn-sm disabled:opacity-50" disabled={saving}>
                {saving ? 'Saving...' : 'Save Changes'}
              </button>
              <button
                type="button"
                className="btn btn-secondary btn-sm"
                onClick={() => router.push('/users')}
                disabled={saving}
              >
                Back
              </button>
            </div>
          </form>
        </div>

        <div className="rounded-xl border border-white/[0.06] bg-slate-900/50 p-6">
          <h3 className="text-sm font-medium text-white">User Details</h3>
          <dl className="mt-4 space-y-4">
            <div>
              <dt className="text-xs font-medium text-slate-500">User ID</dt>
              <dd className="mt-1 text-xs font-mono text-slate-300 break-all">{user.id}</dd>
            </div>
            <div>
              <dt className="text-xs font-medium text-slate-500">Created</dt>
              <dd className="mt-1 text-sm text-slate-300">{formatDate(user.created_at)}</dd>
            </div>
            <div>
              <dt className="text-xs font-medium text-slate-500">Last Updated</dt>
              <dd className="mt-1 text-sm text-slate-300">{formatDate(user.updated_at)}</dd>
            </div>
            <div>
              <dt className="text-xs font-medium text-slate-500">Last Login</dt>
              <dd className="mt-1 text-sm text-slate-300">{formatDate(user.last_login_at)}</dd>
            </div>
          </dl>
        </div>
      </div>

      {toast && (
        <Toast message={toast.message} type={toast.type} onClose={() => setToast(null)} />
      )}
    </div>
  );
}
