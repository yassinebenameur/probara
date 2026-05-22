'use client';

import { useParams, useRouter } from 'next/navigation';
import { useEffect, useState } from 'react';
import Toast from '@/components/ui/Toast';
import PageHeader from '@/components/ui/PageHeader';
import FormCard from '@/components/ui/FormCard';
import FormField from '@/components/ui/FormField';
import FormActions from '@/components/ui/FormActions';
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
        <div className="text-slate-500">Loading user…</div>
      </div>
    );
  }

  if (error || !user) {
    return (
      <FormCard>
        <div className="rounded-lg border border-rose-500/20 bg-rose-500/10 px-4 py-3 text-sm text-rose-400">
          {error || 'User not found'}
        </div>
      </FormCard>
    );
  }

  return (
    <div className="max-w-4xl space-y-6">
      <PageHeader
        breadcrumb={[{ label: 'Users', href: '/users' }, { label: 'Edit' }]}
        title="Edit user"
        subtitle="Update username and reset password for this admin account."
      />

      <div className="grid gap-6 lg:grid-cols-[1fr_300px]">
        <FormCard>
          <form onSubmit={handleSubmit} className="space-y-4">
            <FormField label="Username">
              <input
                type="text"
                className="input"
                value={username}
                onChange={(event) => setUsername(event.target.value)}
              />
            </FormField>
            <FormField label="New password" description="Leave empty to keep current password">
              <input
                type="password"
                className="input"
                value={password}
                onChange={(event) => setPassword(event.target.value)}
              />
            </FormField>

            <FormActions
              cancel={{ label: 'Back', onClick: () => router.push('/users'), disabled: saving }}
              submit={{
                label: saving ? 'Saving…' : 'Save changes',
                loading: saving,
                disabled: saving,
                type: 'submit',
              }}
            />
          </form>
        </FormCard>

        <FormCard>
          <h3 className="text-sm font-medium text-white">User details</h3>
          <dl className="mt-4 space-y-4">
            <div>
              <dt className="text-xs font-medium text-slate-500">User ID</dt>
              <dd className="mt-1 break-all font-mono text-xs text-slate-300">{user.id}</dd>
            </div>
            <div>
              <dt className="text-xs font-medium text-slate-500">Created</dt>
              <dd className="mt-1 text-sm text-slate-300">{formatDate(user.created_at)}</dd>
            </div>
            <div>
              <dt className="text-xs font-medium text-slate-500">Last updated</dt>
              <dd className="mt-1 text-sm text-slate-300">{formatDate(user.updated_at)}</dd>
            </div>
            <div>
              <dt className="text-xs font-medium text-slate-500">Last login</dt>
              <dd className="mt-1 text-sm text-slate-300">{formatDate(user.last_login_at)}</dd>
            </div>
          </dl>
        </FormCard>
      </div>

      {toast && (
        <Toast message={toast.message} type={toast.type} onClose={() => setToast(null)} />
      )}
    </div>
  );
}
