'use client';

import { useRouter, useSearchParams } from 'next/navigation';
import { FormEvent, Suspense, useCallback, useEffect, useMemo, useState } from 'react';
import { Building2, Pencil, Plus, RefreshCw, Trash2 } from 'lucide-react';
import Panel from '@/components/ui/Panel';
import Button from '@/components/ui/Button';
import Callout from '@/components/ui/Callout';
import Dialog from '@/components/ui/Dialog';
import EmptyState from '@/components/ui/EmptyState';
import IconButton from '@/components/ui/IconButton';
import Input from '@/components/ui/Input';
import Page from '@/components/ui/Page';
import PageHeader from '@/components/ui/PageHeader';
import Pill from '@/components/ui/Pill';
import TableSkeleton from '@/components/ui/TableSkeleton';
import { RelativeTime } from '@/components/ui/RelativeTime';
import { useToast } from '@/components/ui/ToastProvider';
import { useCurrentUser } from '@/components/providers/CurrentUserProvider';
import { useSelectedTenantId } from '@/components/providers/TenantProvider';
import { clearApiKey, hasApiKey } from '@/lib/auth';
import { createTenant, deleteTenant, getTenants, renameTenant } from '@/lib/api';
import { pluralize } from '@/lib/format';
import { emitTenantChange, emitTenantsChange, setSelectedTenantId } from '@/lib/tenant';
import type { Tenant } from '@/lib/types';

// The workspace migration 000030 seeds; the API refuses to delete it (and the
// OIDC JIT default workspace, which only the API knows).
const DEFAULT_TENANT_ID = '00000000-0000-0000-0000-000000000001';
const MAX_NAME_LENGTH = 100;

type NameDialog = { mode: 'create' } | { mode: 'rename'; tenant: Tenant };

export default function WorkspacesPage() {
  const router = useRouter();
  const { showToast } = useToast();
  const { isSuperadmin, loading: userLoading } = useCurrentUser();
  const selectedTenantId = useSelectedTenantId();
  const [apiKeyMode, setApiKeyMode] = useState(false);
  const [tenants, setTenants] = useState<Tenant[]>([]);
  const [loading, setLoading] = useState(true);
  const [loaded, setLoaded] = useState(false);
  const [error, setError] = useState('');
  const [nameDialog, setNameDialog] = useState<NameDialog | null>(null);
  const [pendingDelete, setPendingDelete] = useState<Tenant | null>(null);

  const load = useCallback(async () => {
    try {
      setLoading(true);
      setError('');
      const response = await getTenants();
      setTenants(response.items || []);
      setLoaded(true);
    } catch (err: any) {
      setError(err.message || 'Failed to load workspaces');
    } finally {
      setLoading(false);
    }
  }, []);

  useEffect(() => {
    const usingApiKey = hasApiKey();
    setApiKeyMode(usingApiKey);
    if (usingApiKey) {
      setLoading(false);
      return;
    }
    load();
  }, [load]);

  const openCreate = useCallback(() => setNameDialog({ mode: 'create' }), []);

  const sorted = useMemo(
    () => [...tenants].sort((a, b) => a.name.localeCompare(b.name, undefined, { sensitivity: 'base' })),
    [tenants]
  );

  // Created, renamed or deleted: reload this list and the switcher's.
  const changed = async () => {
    emitTenantsChange();
    await load();
  };

  const onDeleted = async (tenant: Tenant) => {
    setPendingDelete(null);
    // Leave the deleted workspace before anything else asks for it: every
    // request scoped to it is now refused.
    if (tenant.id === selectedTenantId) {
      const next = sorted.find((t) => t.id !== tenant.id);
      if (next) {
        setSelectedTenantId(next.id);
        emitTenantChange();
      }
    }
    showToast(`Workspace “${tenant.name}” deleted`, 'success');
    await changed();
  };

  const header = (
    <PageHeader
      title="Workspaces"
      subtitle="Every workspace on this installation. Monitors, alerts, status pages and members belong to one workspace."
      action={
        !apiKeyMode && isSuperadmin && !userLoading ? (
          <div className="flex items-center gap-2">
            <Button
              variant="ghost"
              size="sm"
              icon={<RefreshCw strokeWidth={1.75} />}
              onClick={() => load()}
              loading={loading && loaded}
            >
              Refresh
            </Button>
            <Button
              variant="accent"
              size="sm"
              icon={<Plus strokeWidth={1.75} />}
              onClick={openCreate}
            >
              New workspace
            </Button>
          </div>
        ) : undefined
      }
    />
  );

  if (apiKeyMode) {
    return (
      <Page width="data">
        {header}
        <Panel title="Admin session required" subtitle="Workspace management is available only for admin login mode.">
          <div className="space-y-3">
            <p className="text-sm text-slate-300">
              You are connected with an API key, which is pinned to one workspace. Switch to admin login to
              manage workspaces.
            </p>
            <Button
              variant="ghost"
              size="sm"
              onClick={() => {
                clearApiKey();
                router.push('/login');
              }}
            >
              Go to admin login
            </Button>
          </div>
        </Panel>
      </Page>
    );
  }

  if (!userLoading && !isSuperadmin) {
    return (
      <Page width="data">
        {header}
        <Panel title="Superadmin required" subtitle="Workspace management is restricted to platform superadmins.">
          <p className="text-sm text-slate-300">
            Your account does not have superadmin access. Ask a platform administrator to create or remove
            workspaces.
          </p>
        </Panel>
      </Page>
    );
  }

  return (
    <Page width="data">
      {header}
      <Suspense fallback={null}>
        <NewWorkspaceUrlSync onNew={openCreate} />
      </Suspense>

      {error && (
        <Callout
          tone="danger"
          action={
            <Button variant="ghost" size="xs" onClick={() => load()} loading={loading}>
              Retry
            </Button>
          }
        >
          {error}
        </Callout>
      )}

      {!loaded ? (
        loading ? <TableSkeleton rows={3} columns={3} label="Loading workspaces" /> : null
      ) : sorted.length === 0 ? (
        <EmptyState
          icon={<Building2 strokeWidth={1.5} />}
          title="No workspaces"
          description="Create a workspace to start adding monitors."
        />
      ) : (
        <div
          className={`glass overflow-hidden transition-opacity duration-base ${
            loading ? 'pointer-events-none opacity-60 delay-300' : 'delay-0'
          }`}
          aria-busy={loading || undefined}
        >
          <div className="overflow-x-auto">
            <table className="data-table text-sm">
              <thead>
                <tr>
                  <th>Name</th>
                  <th>ID</th>
                  <th>Created</th>
                  <th><span className="sr-only">Actions</span></th>
                </tr>
              </thead>
              <tbody>
                {sorted.map((tenant) => {
                  const deleteBlocked =
                    tenant.id === DEFAULT_TENANT_ID
                      ? 'The default workspace cannot be deleted'
                      : sorted.length <= 1
                        ? 'The last workspace cannot be deleted'
                        : '';
                  return (
                    <tr key={tenant.id}>
                      <td>
                        <span className="font-medium text-white">{tenant.name}</span>
                        {tenant.id === selectedTenantId && (
                          <Pill tone="neutral" size="xs" className="ml-2">
                            Current
                          </Pill>
                        )}
                      </td>
                      <td className="font-mono text-xs text-slate-400">{tenant.id}</td>
                      <td className="whitespace-nowrap text-ui tabular-nums text-slate-400">
                        <RelativeTime value={tenant.created_at} />
                      </td>
                      <td>
                        <div className="flex items-center justify-end gap-0.5">
                          <IconButton
                            icon={<Pencil strokeWidth={1.75} />}
                            label="Rename"
                            onClick={() => setNameDialog({ mode: 'rename', tenant })}
                          />
                          <IconButton
                            icon={<Trash2 strokeWidth={1.75} />}
                            label={deleteBlocked || 'Delete'}
                            onClick={() => setPendingDelete(tenant)}
                            disabled={deleteBlocked !== ''}
                            danger
                          />
                        </div>
                      </td>
                    </tr>
                  );
                })}
              </tbody>
            </table>
          </div>
          <div className="border-t border-divider px-4 py-3 text-xs tabular-nums text-slate-500">
            {pluralize(sorted.length, 'workspace')}
          </div>
        </div>
      )}

      {nameDialog && (
        <WorkspaceNameDialog
          dialog={nameDialog}
          onClose={() => setNameDialog(null)}
          onSaved={async (tenant) => {
            const created = nameDialog.mode === 'create';
            setNameDialog(null);
            showToast(created ? `Workspace “${tenant.name}” created` : 'Workspace renamed', 'success');
            await changed();
          }}
        />
      )}

      {pendingDelete && (
        <DeleteWorkspaceDialog
          tenant={pendingDelete}
          onClose={() => setPendingDelete(null)}
          onDeleted={() => onDeleted(pendingDelete)}
        />
      )}
    </Page>
  );
}

// Opens the create dialog for ?new=1 (the switcher's "New workspace" entry),
// on first load and on a same-route navigation, then drops the parameter so a
// reload does not reopen it. useSearchParams needs a Suspense boundary for
// the prerender, so it lives in this leaf.
function NewWorkspaceUrlSync({ onNew }: { onNew: () => void }) {
  const searchParams = useSearchParams();
  const wantsNew = searchParams.get('new') === '1';
  useEffect(() => {
    if (!wantsNew) return;
    onNew();
    const url = new URL(window.location.href);
    url.searchParams.delete('new');
    window.history.replaceState(null, '', url);
  }, [wantsNew, onNew]);
  return null;
}

function WorkspaceNameDialog({
  dialog,
  onClose,
  onSaved,
}: {
  dialog: NameDialog;
  onClose: () => void;
  onSaved: (tenant: Tenant) => void;
}) {
  const initial = dialog.mode === 'rename' ? dialog.tenant.name : '';
  const [name, setName] = useState(initial);
  const [saving, setSaving] = useState(false);
  const [error, setError] = useState('');
  const trimmed = name.trim();
  const unchanged = dialog.mode === 'rename' && trimmed === initial;

  const submit = async (event: FormEvent) => {
    event.preventDefault();
    if (!trimmed) {
      setError('Enter a name');
      return;
    }
    setSaving(true);
    setError('');
    try {
      const tenant =
        dialog.mode === 'create'
          ? await createTenant({ name: trimmed })
          : await renameTenant(dialog.tenant.id, { name: trimmed });
      onSaved(tenant);
    } catch (err: any) {
      setError(err.message || 'Failed to save the workspace');
      setSaving(false);
    }
  };

  const formId = 'workspace-name-form';
  return (
    <Dialog
      onClose={onClose}
      busy={saving}
      size="sm"
      title={dialog.mode === 'create' ? 'New workspace' : 'Rename workspace'}
      description={
        dialog.mode === 'create'
          ? 'Superadmins can use it at once. Give members access from Users.'
          : 'The name changes everywhere it is shown; IDs, API keys and links keep working.'
      }
      footer={
        <>
          <Button variant="ghost" size="sm" type="button" onClick={onClose} disabled={saving}>
            Cancel
          </Button>
          <Button
            variant="accent"
            size="sm"
            type="submit"
            form={formId}
            loading={saving}
            disabled={saving || !trimmed || unchanged}
          >
            {dialog.mode === 'create' ? 'Create workspace' : 'Rename'}
          </Button>
        </>
      }
    >
      <form id={formId} onSubmit={submit} noValidate>
        <Input
          label="Name"
          value={name}
          maxLength={MAX_NAME_LENGTH}
          autoFocus
          autoComplete="off"
          onChange={(event) => {
            setName(event.target.value);
            if (error) setError('');
          }}
          error={error || undefined}
        />
      </form>
    </Dialog>
  );
}

function DeleteWorkspaceDialog({
  tenant,
  onClose,
  onDeleted,
}: {
  tenant: Tenant;
  onClose: () => void;
  onDeleted: () => void;
}) {
  const [confirmName, setConfirmName] = useState('');
  const [deleting, setDeleting] = useState(false);
  const [error, setError] = useState('');
  const matches = confirmName.trim() === tenant.name;

  const submit = async (event: FormEvent) => {
    event.preventDefault();
    if (!matches) return;
    setDeleting(true);
    setError('');
    try {
      await deleteTenant(tenant.id, { confirm_name: confirmName.trim() });
      onDeleted();
    } catch (err: any) {
      setError(err.message || 'Failed to delete the workspace');
      setDeleting(false);
    }
  };

  const formId = 'delete-workspace-form';
  return (
    <Dialog
      onClose={onClose}
      busy={deleting}
      size="sm"
      tone="danger"
      title={`Delete “${tenant.name}”`}
      description="Everything in it goes: monitors and their history, alerts, incidents, status pages, channels, API keys and memberships. Access stops at once and the data is purged in the background. This cannot be undone."
      footer={
        <>
          <Button variant="ghost" size="sm" type="button" onClick={onClose} disabled={deleting}>
            Cancel
          </Button>
          <Button
            variant="danger"
            size="sm"
            type="submit"
            form={formId}
            loading={deleting}
            disabled={deleting || !matches}
          >
            Delete workspace
          </Button>
        </>
      }
    >
      <form id={formId} onSubmit={submit} noValidate className="space-y-3">
        <Input
          label={`Type “${tenant.name}” to confirm`}
          value={confirmName}
          autoFocus
          autoComplete="off"
          spellCheck={false}
          onChange={(event) => {
            setConfirmName(event.target.value);
            if (error) setError('');
          }}
        />
        {error && <Callout tone="danger">{error}</Callout>}
      </form>
    </Dialog>
  );
}
