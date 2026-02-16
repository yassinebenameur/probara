'use client';

import Link from 'next/link';
import { useRouter } from 'next/navigation';
import { useState } from 'react';
import Toast from '@/components/ui/Toast';
import { createUser } from '@/lib/api';

type ToastState = { message: string; type: 'success' | 'error' } | null;

export default function NewUserPage() {
  const router = useRouter();
  const [username, setUsername] = useState('');
  const [password, setPassword] = useState('');
  const [loading, setLoading] = useState(false);
  const [toast, setToast] = useState<ToastState>(null);

  const handleSubmit = async (event: React.FormEvent) => {
    event.preventDefault();
    if (!username.trim() || !password) {
      setToast({ message: 'Username and password are required', type: 'error' });
      return;
    }

    try {
      setLoading(true);
      await createUser({
        username: username.trim(),
        password,
      });
      setToast({ message: 'User created successfully', type: 'success' });
      setTimeout(() => {
        router.push('/users');
      }, 900);
    } catch (err: any) {
      setToast({ message: err.message || 'Failed to create user', type: 'error' });
      setLoading(false);
    }
  };

  return (
    <div className="max-w-2xl">
      <div className="mb-6">
        <div className="flex items-center gap-2 text-xs text-slate-500">
          <Link href="/users" className="hover:text-slate-400">Users</Link>
          <svg className="h-3 w-3" fill="none" viewBox="0 0 24 24" stroke="currentColor">
            <path strokeLinecap="round" strokeLinejoin="round" strokeWidth={2} d="M9 5l7 7-7 7" />
          </svg>
          <span className="text-slate-400">New User</span>
        </div>
        <h1 className="mt-3 text-xl font-semibold text-white">Create User</h1>
        <p className="mt-1 text-sm text-slate-500">
          Add a new platform admin account.
        </p>
      </div>

      <div className="rounded-xl border border-white/[0.06] bg-slate-900/50 p-6">
        <form onSubmit={handleSubmit} className="space-y-4">
          <div>
            <label className="mb-1.5 block text-xs font-medium text-slate-400">Username</label>
            <input
              type="text"
              className="input"
              placeholder="e.g. ops-admin"
              value={username}
              onChange={(event) => setUsername(event.target.value)}
            />
          </div>
          <div>
            <label className="mb-1.5 block text-xs font-medium text-slate-400">Password</label>
            <input
              type="password"
              className="input"
              placeholder="Minimum 12 characters"
              value={password}
              onChange={(event) => setPassword(event.target.value)}
            />
          </div>

          <div className="flex items-center gap-2 pt-2">
            <button type="submit" className="btn btn-primary btn-sm disabled:opacity-50" disabled={loading}>
              {loading ? 'Creating...' : 'Create User'}
            </button>
            <button
              type="button"
              className="btn btn-secondary btn-sm"
              onClick={() => router.push('/users')}
              disabled={loading}
            >
              Cancel
            </button>
          </div>
        </form>
      </div>

      {toast && (
        <Toast message={toast.message} type={toast.type} onClose={() => setToast(null)} />
      )}
    </div>
  );
}
