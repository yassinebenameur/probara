'use client';

import { Suspense, useEffect, useState } from 'react';
import { useRouter, useSearchParams } from 'next/navigation';
import { Bot, Eye, PencilLine } from 'lucide-react';
import Button from '@/components/ui/Button';
import BrandMark from '@/components/ui/BrandMark';
import Callout from '@/components/ui/Callout';
import Select from '@/components/ui/Select';
import { getSelectedTenantId } from '@/lib/tenant';
import type { Tenant, TenantListResponse } from '@/lib/types';

type AuthorizationInfo = {
  client_id: string;
  client_name: string;
  redirect_uri: string;
  redirect_origin: string;
  scope: 'read' | 'write';
};

// MCP OAuth consent: an AI agent (Claude, Cursor, …) asks to act as the
// signed-in user. The API validated the request before redirecting here and
// validates it again on the decision; this screen only collects the user's
// choice of workspace and access level. Rendered outside the app shell (see
// Layout/AuthGuard) because it must also work before a workspace is chosen.
export default function ConsentPage() {
  return (
    <Suspense>
      <ConsentInner />
    </Suspense>
  );
}

function ConsentInner() {
  const router = useRouter();
  const searchParams = useSearchParams();
  const query = searchParams.toString();
  const [info, setInfo] = useState<AuthorizationInfo | null>(null);
  const [tenants, setTenants] = useState<Tenant[]>([]);
  const [tenantId, setTenantId] = useState('');
  const [scope, setScope] = useState<'read' | 'write'>('write');
  const [error, setError] = useState('');
  const [submitting, setSubmitting] = useState(false);

  useEffect(() => {
    let ignore = false;
    const load = async () => {
      const requestRes = await fetch(`/api/v1/oauth/authorize/request?${query}`, { credentials: 'include' });
      if (requestRes.status === 401) {
        router.replace(`/login?next=${encodeURIComponent(`/oauth/consent?${query}`)}`);
        return;
      }
      const body = await requestRes.json().catch(() => ({}));
      if (!requestRes.ok) {
        if (!ignore) setError(body.message || 'This authorization request is not valid.');
        return;
      }
      const tenantRes = await fetch('/api/v1/tenants', { credentials: 'include' });
      const tenantBody = (await tenantRes.json().catch(() => ({ items: [] }))) as TenantListResponse;
      if (ignore) return;
      const items = tenantBody.items || [];
      const stored = getSelectedTenantId();
      setInfo(body as AuthorizationInfo);
      setScope((body as AuthorizationInfo).scope);
      setTenants(items);
      setTenantId(items.find((t) => t.id === stored)?.id || items[0]?.id || '');
    };
    load().catch(() => {
      if (!ignore) setError('Could not load the authorization request.');
    });
    return () => {
      ignore = true;
    };
  }, [query, router]);

  const decide = async (approve: boolean) => {
    setSubmitting(true);
    setError('');
    try {
      const res = await fetch('/api/v1/oauth/authorize/decision', {
        method: 'POST',
        credentials: 'include',
        headers: { 'Content-Type': 'application/json', ...(tenantId ? { 'X-Tenant-ID': tenantId } : {}) },
        body: JSON.stringify({ query, approve, tenant_id: tenantId, scope }),
      });
      const body = await res.json().catch(() => ({}));
      if (!res.ok || !body.redirect_to) {
        setError(body.message || 'Authorization failed.');
        setSubmitting(false);
        return;
      }
      window.location.href = body.redirect_to;
    } catch {
      setError('Authorization failed. Please try again.');
      setSubmitting(false);
    }
  };

  const tenantRole = tenants.find((t) => t.id === tenantId)?.role;

  return (
    <div className="flex min-h-screen items-center justify-center px-4 py-10">
      <div className="glass-overlay w-full max-w-md p-8">
        <div className="mb-6 flex items-center justify-center gap-3 text-slate-400">
          <BrandMark size={40} />
          <span aria-hidden="true">↔</span>
          <Bot className="h-9 w-9" strokeWidth={1.5} aria-hidden="true" />
        </div>

        {error && !info ? (
          <Callout tone="danger" title="Cannot authorize">
            {error}
          </Callout>
        ) : !info ? (
          <p className="py-8 text-center text-sm text-slate-400">Loading…</p>
        ) : (
          <div className="space-y-6">
            <div className="text-center">
              <h1 className="text-title font-semibold tracking-tight text-white">
                Connect {info.client_name}
              </h1>
              <p className="mt-2 text-sm text-slate-400">
                This AI agent wants to use Probara as you, through MCP. It will be able to do what you can do
                in the workspace you pick, within the access you allow.
              </p>
            </div>

            {tenants.length === 0 ? (
              <Callout tone="warning">You are not a member of any workspace yet.</Callout>
            ) : (
              <Select label="Workspace" value={tenantId} onChange={(e) => setTenantId(e.target.value)}>
                {tenants.map((t) => (
                  <option key={t.id} value={t.id}>
                    {t.name}
                    {t.role ? ` (${t.role})` : ''}
                  </option>
                ))}
              </Select>
            )}

            <fieldset className="space-y-2">
              <legend className="mb-1 text-xs font-medium text-slate-400">Access</legend>
              <AccessOption
                checked={scope === 'read'}
                onSelect={() => setScope('read')}
                icon={<Eye className="h-4 w-4" strokeWidth={1.75} aria-hidden="true" />}
                title="Read-only"
                text="Look at monitors, alerts, incidents, analytics and settings. Changes nothing."
              />
              <AccessOption
                checked={scope === 'write'}
                disabled={info.scope === 'read'}
                onSelect={() => setScope('write')}
                icon={<PencilLine className="h-4 w-4" strokeWidth={1.75} aria-hidden="true" />}
                title="Read & write"
                text={
                  tenantRole === 'viewer'
                    ? 'Your role in this workspace is viewer, so the agent stays read-only there.'
                    : 'Also create and change monitors, acknowledge alerts, run incidents. Every change is audit-logged as you.'
                }
              />
            </fieldset>

            <p className="text-xs text-slate-500">
              After you allow it, you return to <span className="font-mono text-slate-400">{info.redirect_origin}</span>.
              Revoke the connection any time in Settings → MCP.
            </p>

            {error && <Callout tone="danger">{error}</Callout>}

            <div className="flex gap-3">
              <Button
                variant="ghost"
                size="sm"
                className="flex-1 justify-center"
                disabled={submitting}
                onClick={() => decide(false)}
              >
                Deny
              </Button>
              <Button
                variant="accent"
                size="sm"
                className="flex-1 justify-center"
                disabled={submitting || !tenantId}
                loading={submitting}
                onClick={() => decide(true)}
              >
                Allow
              </Button>
            </div>
          </div>
        )}
      </div>
    </div>
  );
}

function AccessOption({
  checked,
  disabled,
  onSelect,
  icon,
  title,
  text,
}: {
  checked: boolean;
  disabled?: boolean;
  onSelect: () => void;
  icon: React.ReactNode;
  title: string;
  text: string;
}) {
  return (
    <label
      className={`glass-well flex cursor-pointer gap-3 p-3 ${checked ? 'ring-1 ring-slate-400/60' : ''} ${
        disabled ? 'cursor-not-allowed opacity-50' : ''
      }`}
    >
      <input
        type="radio"
        name="scope"
        className="mt-1"
        checked={checked}
        disabled={disabled}
        onChange={onSelect}
      />
      <span className="min-w-0">
        <span className="flex items-center gap-1.5 text-sm font-medium text-white">
          {icon}
          {title}
        </span>
        <span className="mt-0.5 block text-xs text-slate-400">{text}</span>
      </span>
    </label>
  );
}
