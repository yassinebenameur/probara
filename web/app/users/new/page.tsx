'use client';

import { useRouter } from 'next/navigation';
import { useState } from 'react';
import Toast from '@/components/ui/Toast';
import PageHeader from '@/components/ui/PageHeader';
import FormCard from '@/components/ui/FormCard';
import FormField from '@/components/ui/FormField';
import FormActions from '@/components/ui/FormActions';
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
    <div className="max-w-2xl space-y-6">
      <PageHeader
        breadcrumb={[{ label: 'Users', href: '/users' }, { label: 'New user' }]}
        title="Create user"
        subtitle="Add a new platform admin account."
      />

      <FormCard>
        <form onSubmit={handleSubmit} className="space-y-4">
          <FormField label="Username" required>
            <input
              type="text"
              className="input"
              placeholder="e.g. ops-admin"
              value={username}
              onChange={(event) => setUsername(event.target.value)}
            />
          </FormField>
          <FormField label="Password" required description="Minimum 12 characters">
            <input
              type="password"
              className="input"
              value={password}
              onChange={(event) => setPassword(event.target.value)}
            />
          </FormField>

          <FormActions
            cancel={{ label: 'Cancel', onClick: () => router.push('/users'), disabled: loading }}
            submit={{
              label: loading ? 'Creating…' : 'Create user',
              loading,
              disabled: loading,
              type: 'submit',
            }}
          />
        </form>
      </FormCard>

      {toast && (
        <Toast message={toast.message} type={toast.type} onClose={() => setToast(null)} />
      )}
    </div>
  );
}
