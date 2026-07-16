'use client';

import { useRouter } from 'next/navigation';
import { useEffect, useState } from 'react';
import { useToast } from '@/components/ui/ToastProvider';
import PageHeader from '@/components/ui/PageHeader';
import FormCard from '@/components/ui/FormCard';
import FormField from '@/components/ui/FormField';
import FormActions from '@/components/ui/FormActions';
import Select from '@/components/ui/Select';
import MembershipsEditor from '@/components/users/MembershipsEditor';
import { createUser, getTenants } from '@/lib/api';
import type { MembershipInput, PlatformRole, Tenant } from '@/lib/types';

type SignInMethod = 'password' | 'oidc';

export default function NewUserPage() {
  const router = useRouter();
  const [username, setUsername] = useState('');
  const [email, setEmail] = useState('');
  const [password, setPassword] = useState('');
  const [signInMethod, setSignInMethod] = useState<SignInMethod>('password');
  const [platformRole, setPlatformRole] = useState<PlatformRole>('member');
  const [memberships, setMemberships] = useState<MembershipInput[]>([]);
  const [tenants, setTenants] = useState<Tenant[]>([]);
  const [loading, setLoading] = useState(false);
  const { showToast } = useToast();

  useEffect(() => {
    getTenants()
      .then((response) => setTenants(response.items || []))
      .catch(() => setTenants([]));
  }, []);

  const handleSubmit = async (event: React.FormEvent) => {
    event.preventDefault();
    if (!username.trim()) {
      showToast('Username is required', 'error');
      return;
    }
    if (signInMethod === 'password' && !password) {
      showToast('Password is required', 'error');
      return;
    }
    if (signInMethod === 'oidc' && !email.trim()) {
      showToast('Email is required for SSO-only users (it links the IdP identity)', 'error');
      return;
    }

    try {
      setLoading(true);
      await createUser({
        username: username.trim(),
        email: email.trim() || undefined,
        password: signInMethod === 'password' ? password : undefined,
        platform_role: platformRole,
        memberships: platformRole === 'member' ? memberships : undefined,
      });
      showToast('User created successfully', 'success');
      setTimeout(() => {
        router.push('/users');
      }, 900);
    } catch (err: any) {
      showToast(err.message || 'Failed to create user', 'error');
      setLoading(false);
    }
  };

  return (
    <div className="max-w-2xl space-y-6">
      <PageHeader
        breadcrumb={[{ label: 'Users', href: '/users' }, { label: 'New user' }]}
        title="Create user"
        subtitle="Add a team member with per-tenant roles, or a platform admin."
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

          <FormField
            label="Email"
            required={signInMethod === 'oidc'}
            description={
              signInMethod === 'oidc'
                ? 'Must match the verified email at the identity provider'
                : 'Optional; lets the user sign in via SSO later'
            }
          >
            <input
              type="email"
              className="input"
              placeholder="user@example.com"
              value={email}
              onChange={(event) => setEmail(event.target.value)}
            />
          </FormField>

          <FormField label="Sign-in method" required>
            <div className="flex gap-4 text-sm text-slate-300">
              <label className="flex items-center gap-2">
                <input
                  type="radio"
                  name="sign-in-method"
                  checked={signInMethod === 'password'}
                  onChange={() => setSignInMethod('password')}
                />
                Password
              </label>
              <label className="flex items-center gap-2">
                <input
                  type="radio"
                  name="sign-in-method"
                  checked={signInMethod === 'oidc'}
                  onChange={() => setSignInMethod('oidc')}
                />
                SSO only (no local password)
              </label>
            </div>
          </FormField>

          {signInMethod === 'password' && (
            <FormField label="Password" required description="Minimum 12 characters">
              <input
                type="password"
                className="input"
                value={password}
                onChange={(event) => setPassword(event.target.value)}
              />
            </FormField>
          )}

          <FormField
            label="Platform role"
            required
            description="Superadmins bypass tenant membership and manage users and tenants"
          >
            <Select
              value={platformRole}
              onChange={(event) => setPlatformRole(event.target.value as PlatformRole)}
            >
              <option value="member">Member (roles per tenant)</option>
              <option value="superadmin">Superadmin (full platform access)</option>
            </Select>
          </FormField>

          {platformRole === 'member' && (
            <FormField label="Tenant memberships">
              <MembershipsEditor
                memberships={memberships}
                tenants={tenants}
                onChange={setMemberships}
                disabled={loading}
              />
            </FormField>
          )}

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

    </div>
  );
}
