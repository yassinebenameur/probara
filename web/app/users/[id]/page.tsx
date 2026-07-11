'use client';

import { useParams, useRouter } from 'next/navigation';
import { useEffect, useState } from 'react';
import { useToast } from '@/components/ui/ToastProvider';
import PageHeader from '@/components/ui/PageHeader';
import FormCard from '@/components/ui/FormCard';
import FormField from '@/components/ui/FormField';
import FormActions from '@/components/ui/FormActions';
import Select from '@/components/ui/Select';
import Pill from '@/components/ui/Pill';
import MembershipsEditor from '@/components/users/MembershipsEditor';
import { getTenants, getUser, updateUser } from '@/lib/api';
import { formatDateTime } from '@/lib/format';
import type { AdminUser, MembershipInput, PlatformRole, Tenant } from '@/lib/types';

export default function EditUserPage() {
  const router = useRouter();
  const params = useParams();
  const id = params.id as string;

  const [user, setUser] = useState<AdminUser | null>(null);
  const [username, setUsername] = useState('');
  const [email, setEmail] = useState('');
  const [password, setPassword] = useState('');
  const [platformRole, setPlatformRole] = useState<PlatformRole>('member');
  const [memberships, setMemberships] = useState<MembershipInput[]>([]);
  const [tenants, setTenants] = useState<Tenant[]>([]);
  const [loading, setLoading] = useState(true);
  const [saving, setSaving] = useState(false);
  const [error, setError] = useState('');
  const { showToast } = useToast();

  useEffect(() => {
    const load = async () => {
      try {
        setLoading(true);
        setError('');
        const [data, tenantsResponse] = await Promise.all([
          getUser(id),
          getTenants().catch(() => ({ items: [] as Tenant[] })),
        ]);
        setUser(data);
        setUsername(data.username);
        setEmail(data.email || '');
        setPlatformRole(data.platform_role || 'member');
        setMemberships(
          (data.memberships || []).map((m) => ({ tenant_id: m.tenant_id, role: m.role }))
        );
        setTenants(tenantsResponse.items || []);
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

    try {
      setSaving(true);
      const updated = await updateUser(id, {
        username: username.trim() || undefined,
        email: email.trim() || undefined,
        password: password || undefined,
        platform_role: platformRole,
        memberships,
      });
      setUser(updated);
      setUsername(updated.username);
      setEmail(updated.email || '');
      setPlatformRole(updated.platform_role || 'member');
      setMemberships(
        (updated.memberships || []).map((m) => ({ tenant_id: m.tenant_id, role: m.role }))
      );
      setPassword('');
      showToast('User updated successfully', 'success');
    } catch (err: any) {
      showToast(err.message || 'Failed to update user', 'error');
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

  const isOidcUser = user.auth_method === 'oidc';

  return (
    <div className="max-w-4xl space-y-6">
      <PageHeader
        breadcrumb={[{ label: 'Users', href: '/users' }, { label: 'Edit' }]}
        title="Edit user"
        subtitle="Update identity, roles and tenant memberships."
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
            <FormField
              label="Email"
              description={isOidcUser ? 'Linked to the SSO identity' : undefined}
            >
              <input
                type="email"
                className="input"
                value={email}
                onChange={(event) => setEmail(event.target.value)}
              />
            </FormField>
            <FormField
              label="New password"
              description={
                isOidcUser
                  ? 'This account signs in via SSO; setting a password also enables local login'
                  : 'Leave empty to keep current password'
              }
            >
              <input
                type="password"
                className="input"
                value={password}
                onChange={(event) => setPassword(event.target.value)}
              />
            </FormField>
            <FormField label="Platform role">
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
                  disabled={saving}
                />
              </FormField>
            )}

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
              <dt className="text-xs font-medium text-slate-500">Sign-in method</dt>
              <dd className="mt-1">
                <Pill tone={isOidcUser ? 'info' : 'neutral'}>
                  {isOidcUser ? 'SSO (OIDC)' : 'Password'}
                </Pill>
              </dd>
            </div>
            <div>
              <dt className="text-xs font-medium text-slate-500">Created</dt>
              <dd className="mt-1 text-sm text-slate-300">{formatDateTime(user.created_at, "-")}</dd>
            </div>
            <div>
              <dt className="text-xs font-medium text-slate-500">Last updated</dt>
              <dd className="mt-1 text-sm text-slate-300">{formatDateTime(user.updated_at, "-")}</dd>
            </div>
            <div>
              <dt className="text-xs font-medium text-slate-500">Last login</dt>
              <dd className="mt-1 text-sm text-slate-300">{formatDateTime(user.last_login_at, "-")}</dd>
            </div>
          </dl>
        </FormCard>
      </div>

    </div>
  );
}
