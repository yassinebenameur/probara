'use client';

import { useEffect, useRef, useState } from 'react';
import { Trash2 } from 'lucide-react';
import Panel from '@/components/ui/Panel';
import Pill from '@/components/ui/Pill';
import Button from '@/components/ui/Button';
import { useToast } from '@/components/ui/ToastProvider';
import {
  createOidcGroupMapping,
  deleteOidcGroupMapping,
  getTenants,
  listOidcGroupMappings,
  updateOidcGroupMapping,
} from '@/lib/api';
import type { OidcGroupMapping, OidcSeenGroup, Tenant } from '@/lib/types';

const TENANT_ROLES = ['admin', 'editor', 'viewer'] as const;

const inputClass =
  'rounded border border-white/[0.08] bg-slate-800 px-3 py-2 text-xs text-slate-300 focus:border-cyan-500/50 focus:outline-none';

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
    // eslint-disable-next-line react-hooks/exhaustive-deps
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
        <div className="text-sm text-slate-500">Loading group mappings…</div>
      ) : error ? (
        <div className="space-y-3">
          <p className="text-sm text-rose-400">{error}</p>
          <Button variant="ghost" size="sm" onClick={load}>Retry</Button>
        </div>
      ) : (
        <div className="space-y-5">
          {!scopeRequested && (
            <div className="rounded-lg border border-amber-500/30 bg-amber-500/10 px-4 py-3 text-xs text-amber-300">
              <span className="font-medium">The groups claim is not being requested.</span> Add{' '}
              <code className="font-mono">groups</code> to <code className="font-mono">OIDC_SCOPES</code>{' '}
              — without it most identity providers never send group membership, and mapped users
              would sign in with no roles.
            </div>
          )}

          {/* Current mappings */}
          {mappings.length === 0 ? (
            <div className="rounded-lg border border-dashed border-white/[0.08] px-4 py-5 text-center">
              <p className="text-sm text-slate-400">No group mappings yet</p>
              <p className="mx-auto mt-1 max-w-md text-xs text-slate-500">
                Roles stay manually managed until you add one. Once a mapping exists, the identity
                provider decides SSO users&apos; roles — removing someone from a group revokes the
                role at their next sign-in.
              </p>
            </div>
          ) : (
            <div className="overflow-x-auto">
              <table className="w-full text-left text-sm">
                <thead>
                  <tr className="border-b border-white/[0.06] text-xs text-slate-500">
                    <th className="py-2 pr-4 font-medium">Identity-provider group</th>
                    <th className="py-2 pr-4 font-medium">Grants</th>
                    <th className="w-10 py-2 font-medium" />
                  </tr>
                </thead>
                <tbody>
                  {mappings.map((m) => (
                    <tr key={m.id} className="border-b border-white/[0.04]">
                      <td className="py-2.5 pr-4">
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
                            className={`${inputClass} w-48`}
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
                                <span className="block text-sm text-white group-hover:text-cyan-300">
                                  {m.label}
                                </span>
                                <span className="block font-mono text-[11px] text-slate-500">
                                  {m.group_name}
                                </span>
                              </>
                            ) : (
                              <>
                                <span className="block font-mono text-xs text-slate-300 group-hover:text-cyan-300">
                                  {m.group_name}
                                </span>
                                <span className="block text-[11px] text-slate-600 opacity-0 transition-opacity group-hover:opacity-100">
                                  + add label
                                </span>
                              </>
                            )}
                          </button>
                        )}
                        {seenGroups.length > 0 && !seenGroupNames.has(m.group_name) && (
                          <span
                            className="mt-1 flex items-center gap-1 text-[11px] text-amber-400/90"
                            title="No SSO login has presented this group so far. Check the spelling against the identity provider — matching is exact and case-sensitive."
                          >
                            <span className="h-1.5 w-1.5 rounded-full bg-amber-400" />
                            never seen at a login
                          </span>
                        )}
                      </td>
                      <td className="py-2.5 pr-4">
                        {m.tenant_id ? (
                          <span className="inline-flex flex-wrap items-center gap-2">
                            <Pill tone="neutral" size="xs">{m.tenant_name ?? m.tenant_id}</Pill>
                            <select
                              value={m.role}
                              onChange={(e) => handleRoleChange(m, e.target.value)}
                              aria-label={`Role for group ${m.group_name}`}
                              className={`${inputClass} cursor-pointer`}
                            >
                              {TENANT_ROLES.map((role) => (
                                <option key={role} value={role}>{role}</option>
                              ))}
                            </select>
                          </span>
                        ) : (
                          <Pill tone="warning" size="xs" dot>Platform · superadmin</Pill>
                        )}
                      </td>
                      <td className="py-2.5 text-right">
                        <Button
                          variant="ghost"
                          size="sm"
                          onClick={() => handleDelete(m.id)}
                          icon={confirmDeleteId === m.id ? undefined : <Trash2 strokeWidth={1.75} />}
                          className={confirmDeleteId === m.id ? 'text-rose-400 hover:text-rose-300' : ''}
                          aria-label={
                            confirmDeleteId === m.id
                              ? `Confirm deleting mapping for ${m.group_name}`
                              : `Delete mapping for ${m.group_name}`
                          }
                        >
                          {confirmDeleteId === m.id ? 'Delete?' : ''}
                        </Button>
                      </td>
                    </tr>
                  ))}
                </tbody>
              </table>
            </div>
          )}

          {/* Add a mapping */}
          <div className="rounded-lg border border-white/[0.06] bg-slate-900/40 p-4">
            <p className="text-xs font-medium text-slate-300">Add a mapping</p>

            <div className="mt-3 flex flex-wrap items-end gap-3">
              <div>
                <label htmlFor="new-mapping-group" className="mb-1.5 block text-xs font-medium text-slate-400">
                  Identity-provider group
                </label>
                <input
                  id="new-mapping-group"
                  ref={groupInputRef}
                  type="text"
                  value={newGroup}
                  onChange={(e) => setNewGroup(e.target.value)}
                  placeholder="engineering"
                  className={`${inputClass} w-52 font-mono`}
                  list="seen-oidc-groups"
                />
                <datalist id="seen-oidc-groups">
                  {seenGroups.map((g) => (
                    <option key={g.group_name} value={g.group_name} />
                  ))}
                </datalist>
              </div>
              <div>
                <label htmlFor="new-mapping-label" className="mb-1.5 block text-xs font-medium text-slate-400">
                  Label <span className="font-normal text-slate-600">(optional)</span>
                </label>
                <input
                  id="new-mapping-label"
                  type="text"
                  value={newLabel}
                  onChange={(e) => setNewLabel(e.target.value)}
                  placeholder="Engineering"
                  maxLength={100}
                  className={inputClass}
                />
              </div>

              <div>
                <span className="mb-1.5 block text-xs font-medium text-slate-400">Grants</span>
                <div className="inline-flex overflow-hidden rounded border border-white/[0.08]" role="group" aria-label="Mapping target">
                  <button
                    type="button"
                    onClick={() => setNewMode('tenant')}
                    aria-pressed={newMode === 'tenant'}
                    className={`cursor-pointer px-3 py-2 text-xs transition-colors ${
                      newMode === 'tenant'
                        ? 'bg-cyan-500/15 text-cyan-300'
                        : 'bg-slate-800 text-slate-400 hover:text-slate-200'
                    }`}
                  >
                    Tenant role
                  </button>
                  <button
                    type="button"
                    onClick={() => setNewMode('platform')}
                    aria-pressed={newMode === 'platform'}
                    className={`cursor-pointer border-l border-white/[0.08] px-3 py-2 text-xs transition-colors ${
                      newMode === 'platform'
                        ? 'bg-amber-500/15 text-amber-300'
                        : 'bg-slate-800 text-slate-400 hover:text-slate-200'
                    }`}
                  >
                    Platform superadmin
                  </button>
                </div>
              </div>

              {newMode === 'tenant' && (
                <>
                  <div>
                    <label htmlFor="new-mapping-tenant" className="mb-1.5 block text-xs font-medium text-slate-400">
                      Tenant
                    </label>
                    <select
                      id="new-mapping-tenant"
                      value={newTenantId}
                      onChange={(e) => setNewTenantId(e.target.value)}
                      className={`${inputClass} cursor-pointer`}
                    >
                      {tenants.map((t) => (
                        <option key={t.id} value={t.id}>{t.name}</option>
                      ))}
                    </select>
                  </div>
                  <div>
                    <label htmlFor="new-mapping-role" className="mb-1.5 block text-xs font-medium text-slate-400">
                      Role
                    </label>
                    <select
                      id="new-mapping-role"
                      value={newRole}
                      onChange={(e) => setNewRole(e.target.value)}
                      className={`${inputClass} cursor-pointer`}
                    >
                      {TENANT_ROLES.map((role) => (
                        <option key={role} value={role}>{role}</option>
                      ))}
                    </select>
                  </div>
                </>
              )}

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

            <p className="mt-2 text-[11px] text-slate-600">
              Must match the value in the “{groupsClaim}” claim exactly — matching is
              case-sensitive. Azure AD sends group IDs (GUIDs) here, not names.
            </p>

            {newMode === 'platform' && (
              <p className="mt-1 text-[11px] text-amber-400/80">
                Everyone in this group becomes a platform superadmin with full access to all tenants.
              </p>
            )}

            {unmappedSeenGroups.length > 0 && (
              <div className="mt-3 border-t border-white/[0.06] pt-3">
                <p className="mb-1.5 text-xs text-slate-500">
                  Seen at recent SSO logins, not mapped yet — select to fill in:
                </p>
                <div className="flex flex-wrap gap-1.5">
                  {unmappedSeenGroups.slice(0, 20).map((g) => (
                    <button
                      key={g.group_name}
                      type="button"
                      onClick={() => prefillGroup(g.group_name)}
                      title={`Last seen ${new Date(g.last_seen_at).toLocaleString()}`}
                      className="cursor-pointer rounded-full border border-white/[0.08] bg-slate-800 px-2.5 py-1 font-mono text-[11px] text-slate-300 transition-colors hover:border-cyan-500/50 hover:text-cyan-300"
                    >
                      {g.group_name}
                    </button>
                  ))}
                </div>
              </div>
            )}
          </div>

          {scopeRequested && (
            <p className="text-[11px] text-slate-600">
              Reading the “{groupsClaim}” claim · groups scope requested ✓ · changes are recorded
              in the audit log
            </p>
          )}
        </div>
      )}
    </Panel>
  );
}
