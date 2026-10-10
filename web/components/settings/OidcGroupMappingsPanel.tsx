'use client';

import { useEffect, useRef, useState } from 'react';
import { Trash2 } from 'lucide-react';
import Panel from '@/components/ui/Panel';
import Pill from '@/components/ui/Pill';
import Button from '@/components/ui/Button';
import IconButton from '@/components/ui/IconButton';
import Callout from '@/components/ui/Callout';
import { useToast } from '@/components/ui/ToastProvider';
import { formatDateTimeFull } from '@/lib/format';
import {
  createOidcGroupMapping,
  deleteOidcGroupMapping,
  getTenants,
  listOidcGroupMappings,
  updateOidcGroupMapping,
} from '@/lib/api';
import type { OidcGroupMapping, OidcSeenGroup, Tenant } from '@/lib/types';

const TENANT_ROLES = ['admin', 'editor', 'viewer'] as const;

const LABEL_CLASS = 'mb-1.5 block text-ui font-medium text-slate-300';

const SEGMENT_CLASS = 'cursor-pointer rounded-lg px-3 py-1.5 text-ui font-medium transition-colors';
const SEGMENT_ACTIVE = 'bg-glass-strong text-white shadow-glass';
const SEGMENT_IDLE = 'text-slate-400 hover:text-slate-100';

// Superadmin editor for IdP group→role mappings. Any saved mapping makes the
// IdP the source of truth: SSO users' roles are re-derived from their groups
// on every login, overwriting manual role edits.
export function OidcGroupMappingsPanel() {
  const [mappings, setMappings] = useState<OidcGroupMapping[]>([]);
  const [seenGroups, setSeenGroups] = useState<OidcSeenGroup[]>([]);
  const [groupsClaim, setGroupsClaim] = useState('groups');
  const [scopeRequested, setScopeRequested] = useState(true);
  const [tenants, setTenants] = useState<Tenant[]>([]);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState('');
  const { showToast } = useToast();

  // Add-mapping form. Tenant mode is the deliberate default — platform
  // mappings grant superadmin and should be an explicit choice.
  const [newGroup, setNewGroup] = useState('');
  const [newLabel, setNewLabel] = useState('');
  const [newMode, setNewMode] = useState<'tenant' | 'platform'>('tenant');
  const [newTenantId, setNewTenantId] = useState('');
  const [newRole, setNewRole] = useState<string>('viewer');
  const [creating, setCreating] = useState(false);
  const groupInputRef = useRef<HTMLInputElement>(null);

  // Inline label editing, keyed by mapping id.
  const [editingLabelId, setEditingLabelId] = useState<string | null>(null);
  const [labelDraft, setLabelDraft] = useState('');

  // Two-click delete confirmation, keyed by mapping id.
  const [confirmDeleteId, setConfirmDeleteId] = useState<string | null>(null);

  const mappedGroupNames = new Set(mappings.map((m) => m.group_name));
  const unmappedSeenGroups = seenGroups.filter((g) => !mappedGroupNames.has(g.group_name));
  const seenGroupNames = new Set(seenGroups.map((g) => g.group_name));

  const load = async () => {
    try {
      setLoading(true);
      setError('');
      const [resp, tenantList] = await Promise.all([listOidcGroupMappings(), getTenants()]);
      setMappings(resp.mappings ?? []);
      setSeenGroups(resp.seen_groups ?? []);
      setGroupsClaim(resp.groups_claim || 'groups');
      setScopeRequested(resp.groups_scope_requested);
      const items = tenantList.items ?? [];
      setTenants(items);
      setNewTenantId((current) => current || items[0]?.id || '');
    } catch (err: unknown) {
      setError(err instanceof Error ? err.message : 'Failed to load OIDC group mappings');
    } finally {
      setLoading(false);
    }
  };

  useEffect(() => {
    load();
  }, []);

  const handleCreate = async () => {
    try {
      setCreating(true);
      const isPlatform = newMode === 'platform';
      const created = await createOidcGroupMapping({
        group_name: newGroup.trim(),
        label: newLabel.trim() || undefined,
        tenant_id: isPlatform ? undefined : newTenantId,
        role: isPlatform ? 'superadmin' : newRole,
      });
      setMappings((prev) => [...prev, created]);
      setNewGroup('');
      setNewLabel('');
      showToast('Mapping added — applies at each user’s next SSO login', 'success');
    } catch (err: unknown) {
      showToast(err instanceof Error ? err.message : 'Failed to add mapping', 'error');
    } finally {
      setCreating(false);
    }
  };

  const handleRoleChange = async (mapping: OidcGroupMapping, role: string) => {
    try {
      const updated = await updateOidcGroupMapping(mapping.id, { role });
      setMappings((prev) => prev.map((m) => (m.id === updated.id ? updated : m)));
      showToast('Role updated', 'success');
    } catch (err: unknown) {
      showToast(err instanceof Error ? err.message : 'Failed to update mapping', 'error');
    }
  };

  const saveLabel = async (mapping: OidcGroupMapping) => {
    const next = labelDraft.trim();
    setEditingLabelId(null);
    if (next === (mapping.label ?? '')) return;
    try {
      const updated = await updateOidcGroupMapping(mapping.id, { label: next });
      setMappings((prev) => prev.map((m) => (m.id === updated.id ? updated : m)));
      showToast(next ? 'Label saved' : 'Label removed', 'success');
    } catch (err: unknown) {
      showToast(err instanceof Error ? err.message : 'Failed to save label', 'error');
    }
  };

  const handleDelete = async (id: string) => {
    if (confirmDeleteId !== id) {
      setConfirmDeleteId(id);
      return;
    }
    setConfirmDeleteId(null);
    try {
      await deleteOidcGroupMapping(id);
      setMappings((prev) => prev.filter((m) => m.id !== id));
      showToast('Mapping deleted', 'success');
    } catch (err: unknown) {
      showToast(err instanceof Error ? err.message : 'Failed to delete mapping', 'error');
    }
  };

  const prefillGroup = (name: string) => {
    setNewGroup(name);
    groupInputRef.current?.focus();
  };

  return (
    <Panel
      title="OIDC group mappings"
      subtitle="Give roles to SSO users based on their identity-provider groups. Mappings re-apply at every SSO login and overwrite manually assigned roles."
    >
      {loading ? (
        <p className="text-sm text-slate-500">Loading group mappings…</p>
      ) : error ? (
        <Callout
          tone="danger"
          action={<Button variant="ghost" size="xs" onClick={load}>Retry</Button>}
        >
          {error}
        </Callout>
      ) : (
        <div className="space-y-5">
          {!scopeRequested && (
            <Callout tone="warning" title="The groups claim is not being requested.">
              Add <code className="font-mono text-xs">groups</code> to{' '}
              <code className="font-mono text-xs">OIDC_SCOPES</code> — without it most identity providers
              never send group membership, and mapped users would sign in with no roles.
            </Callout>
          )}

          {mappings.length === 0 ? (
            <div className="glass-well px-4 py-6 text-center">
              <p className="text-sm font-medium text-white">No group mappings yet</p>
              <p className="mx-auto mt-1 max-w-md text-ui text-slate-400">
                Roles stay manually managed until you add one. Once a mapping exists, the identity
                provider decides SSO users&apos; roles — removing someone from a group revokes the
                role at their next sign-in.
              </p>
            </div>
          ) : (
            <div className="overflow-x-auto">
              <table className="w-full text-left text-sm">
                <thead>
                  <tr className="border-b border-divider text-xs text-slate-500">
                    <th className="py-2 pr-4 font-medium">Identity-provider group</th>
                    <th className="py-2 pr-4 font-medium">Grants</th>
                    <th className="w-10 py-2 font-medium" />
                  </tr>
                </thead>
                <tbody className="divide-y divide-divider">
                  {mappings.map((m) => (
                    <tr key={m.id}>
                      <td className="py-3 pr-4">
                        {editingLabelId === m.id ? (
                          <input
                            type="text"
                            value={labelDraft}
                            onChange={(e) => setLabelDraft(e.target.value)}
                            onBlur={() => saveLabel(m)}
                            onKeyDown={(e) => {
                              if (e.key === 'Enter') saveLabel(m);
                              if (e.key === 'Escape') setEditingLabelId(null);
                            }}
                            placeholder="Label, e.g. Engineering"
                            maxLength={100}
                            aria-label={`Label for group ${m.group_name}`}
                            autoFocus
                            className="input input-sm !w-48"
                          />
                        ) : (
                          <button
                            type="button"
                            onClick={() => {
                              setEditingLabelId(m.id);
                              setLabelDraft(m.label ?? '');
                            }}
                            title="Edit label"
                            className="group cursor-pointer text-left"
                          >
                            {m.label ? (
                              <>
                                <span className="block text-sm font-medium text-white group-hover:text-cyan-300">
                                  {m.label}
                                </span>
                                <span className="block font-mono text-xs text-slate-500">
                                  {m.group_name}
                                </span>
                              </>
                            ) : (
                              <>
                                <span className="block font-mono text-xs text-slate-200 group-hover:text-cyan-300">
                                  {m.group_name}
                                </span>
                                <span className="block text-xs text-slate-500 opacity-0 transition-opacity group-hover:opacity-100">
                                  + add label
                                </span>
                              </>
                            )}
                          </button>
                        )}
                        {seenGroups.length > 0 && !seenGroupNames.has(m.group_name) && (
                          <span
                            className="mt-1 flex items-center gap-1.5 text-xs text-amber-300"
                            title="No SSO login has presented this group so far. Check the spelling against the identity provider — matching is exact and case-sensitive."
                          >
                            <span className="h-1.5 w-1.5 rounded-full bg-amber-500" />
                            never seen at a login
                          </span>
                        )}
                      </td>
                      <td className="py-3 pr-4">
                        {m.tenant_id ? (
                          <span className="inline-flex flex-wrap items-center gap-2">
                            <Pill tone="neutral" size="xs">{m.tenant_name ?? m.tenant_id}</Pill>
                            <select
                              value={m.role}
                              onChange={(e) => handleRoleChange(m, e.target.value)}
                              aria-label={`Role for group ${m.group_name}`}
                              className="input input-sm !w-auto cursor-pointer"
                            >
                              {TENANT_ROLES.map((role) => (
                                <option key={role} value={role}>{role}</option>
                              ))}
                            </select>
                          </span>
                        ) : (
                          <Pill tone="warning" size="xs" dot>Platform superadmin</Pill>
                        )}
                      </td>
                      <td className="py-3 text-right">
                        {confirmDeleteId === m.id ? (
                          <Button
                            variant="subtle"
                            size="xs"
                            onClick={() => handleDelete(m.id)}
                            className="!text-rose-300 hover:!bg-rose-500/10"
                            aria-label={`Confirm deleting mapping for ${m.group_name}`}
                          >
                            Delete?
                          </Button>
                        ) : (
                          <IconButton
                            icon={<Trash2 strokeWidth={1.75} />}
                            label={`Delete mapping for ${m.group_name}`}
                            onClick={() => handleDelete(m.id)}
                            danger
                          />
                        )}
                      </td>
                    </tr>
                  ))}
                </tbody>
              </table>
            </div>
          )}

          <div className="glass-well p-4">
            <p className="text-sm font-medium text-white">Add a mapping</p>

            <div className="mt-3 flex flex-wrap items-end gap-3">
              <div>
                <label htmlFor="new-mapping-group" className={LABEL_CLASS}>
                  Identity-provider group
                </label>
                <input
                  id="new-mapping-group"
                  ref={groupInputRef}
                  type="text"
                  value={newGroup}
                  onChange={(e) => setNewGroup(e.target.value)}
                  placeholder="engineering"
                  className="input !w-52 font-mono"
                  list="seen-oidc-groups"
                />
                <datalist id="seen-oidc-groups">
                  {seenGroups.map((g) => (
                    <option key={g.group_name} value={g.group_name} />
                  ))}
                </datalist>
              </div>
              <div>
                <label htmlFor="new-mapping-label" className={LABEL_CLASS}>
                  Label <span className="font-normal text-slate-500">(optional)</span>
                </label>
                <input
                  id="new-mapping-label"
                  type="text"
                  value={newLabel}
                  onChange={(e) => setNewLabel(e.target.value)}
                  placeholder="Engineering"
                  maxLength={100}
                  className="input !w-44"
                />
              </div>

              <div>
                <span className={LABEL_CLASS}>Grants</span>
                <div
                  className="inline-flex h-10 items-center gap-0.5 rounded-xl bg-well p-1 shadow-[inset_0_0_0_1px_var(--glass-edge)]"
                  role="group"
                  aria-label="Mapping target"
                >
                  <button
                    type="button"
                    onClick={() => setNewMode('tenant')}
                    aria-pressed={newMode === 'tenant'}
                    className={`${SEGMENT_CLASS} ${newMode === 'tenant' ? SEGMENT_ACTIVE : SEGMENT_IDLE}`}
                  >
                    Tenant role
                  </button>
                  <button
                    type="button"
                    onClick={() => setNewMode('platform')}
                    aria-pressed={newMode === 'platform'}
                    className={`${SEGMENT_CLASS} ${newMode === 'platform' ? SEGMENT_ACTIVE : SEGMENT_IDLE}`}
                  >
                    Platform superadmin
                  </button>
                </div>
              </div>

              {newMode === 'tenant' && (
                <>
                  <div>
                    <label htmlFor="new-mapping-tenant" className={LABEL_CLASS}>
                      Tenant
                    </label>
                    <select
                      id="new-mapping-tenant"
                      value={newTenantId}
                      onChange={(e) => setNewTenantId(e.target.value)}
                      className="input h-10 !w-44 cursor-pointer"
                    >
                      {tenants.map((t) => (
                        <option key={t.id} value={t.id}>{t.name}</option>
                      ))}
                    </select>
                  </div>
                  <div>
                    <label htmlFor="new-mapping-role" className={LABEL_CLASS}>
                      Role
                    </label>
                    <select
                      id="new-mapping-role"
                      value={newRole}
                      onChange={(e) => setNewRole(e.target.value)}
                      className="input h-10 !w-32 cursor-pointer"
                    >
                      {TENANT_ROLES.map((role) => (
                        <option key={role} value={role}>{role}</option>
                      ))}
                    </select>
                  </div>
                </>
              )}

            </div>

            <div className="mt-4 flex flex-wrap items-center justify-between gap-3">
              <div className="min-w-0 max-w-2xl space-y-1">
                <p className="text-xs text-slate-500">
                  Must match the value in the “{groupsClaim}” claim exactly — matching is
                  case-sensitive. Azure AD sends group IDs (GUIDs) here, not names.
                </p>
                {newMode === 'platform' && (
                  <p className="text-xs text-amber-300">
                    Everyone in this group becomes a platform superadmin with full access to all tenants.
                  </p>
                )}
              </div>
              <Button
                variant="accent"
                size="sm"
                onClick={handleCreate}
                disabled={creating || newGroup.trim() === '' || (newMode === 'tenant' && !newTenantId)}
                loading={creating}
              >
                Add mapping
              </Button>
            </div>

            {unmappedSeenGroups.length > 0 && (
              <div className="mt-4">
                <p className="mb-1.5 text-xs text-slate-500">
                  Seen at recent SSO logins, not mapped yet — select to fill in:
                </p>
                <div className="flex flex-wrap gap-1.5">
                  {unmappedSeenGroups.slice(0, 20).map((g) => (
                    <button
                      key={g.group_name}
                      type="button"
                      onClick={() => prefillGroup(g.group_name)}
                      title={`Last seen ${formatDateTimeFull(g.last_seen_at)}`}
                      className="cursor-pointer rounded-full bg-well-hover px-2.5 py-1 font-mono text-2xs text-slate-300 transition-colors hover:bg-control-hover hover:text-white"
                    >
                      {g.group_name}
                    </button>
                  ))}
                </div>
              </div>
            )}
          </div>

          {scopeRequested && (
            <p className="text-xs text-slate-500">
              Reading the “{groupsClaim}” claim with the groups scope requested. Changes are
              recorded in the audit log.
            </p>
          )}
        </div>
      )}
    </Panel>
  );
}
