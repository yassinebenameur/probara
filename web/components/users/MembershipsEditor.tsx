'use client';

import { Plus, Trash2 } from 'lucide-react';
import Button from '@/components/ui/Button';
import IconButton from '@/components/ui/IconButton';
import Select from '@/components/ui/Select';
import type { MembershipInput, Tenant, TenantRole } from '@/lib/types';

const ROLE_OPTIONS: { value: TenantRole; label: string; hint: string }[] = [
  { value: 'admin', label: 'Admin', hint: 'manage members, keys and settings' },
  { value: 'editor', label: 'Editor', hint: 'create and edit monitors, alerts…' },
  { value: 'viewer', label: 'Viewer', hint: 'read-only' },
];

interface MembershipsEditorProps {
  memberships: MembershipInput[];
  tenants: Tenant[];
  onChange: (memberships: MembershipInput[]) => void;
  disabled?: boolean;
}

export default function MembershipsEditor({
  memberships,
  tenants,
  onChange,
  disabled,
}: MembershipsEditorProps) {
  const usedTenantIds = new Set(memberships.map((m) => m.tenant_id));
  const firstFreeTenant = tenants.find((t) => !usedTenantIds.has(t.id));

  const update = (index: number, patch: Partial<MembershipInput>) => {
    onChange(memberships.map((m, i) => (i === index ? { ...m, ...patch } : m)));
  };

  return (
    <div className="space-y-2">
      {memberships.length === 0 && (
        <p className="text-ui text-slate-500">
          No tenant memberships. The user will not be able to access any workspace.
        </p>
      )}
      {memberships.map((membership, index) => (
        <div key={membership.tenant_id || index} className="flex items-center gap-2">
          <div className="flex-1">
            <Select
              className="h-10"
              value={membership.tenant_id}
              disabled={disabled}
              onChange={(e) => update(index, { tenant_id: e.target.value })}
            >
              {tenants.map((tenant) => (
                <option
                  key={tenant.id}
                  value={tenant.id}
                  disabled={tenant.id !== membership.tenant_id && usedTenantIds.has(tenant.id)}
                >
                  {tenant.name}
                </option>
              ))}
            </Select>
          </div>
          <div className="w-40">
            <Select
              className="h-10"
              value={membership.role}
              disabled={disabled}
              onChange={(e) => update(index, { role: e.target.value as TenantRole })}
            >
              {ROLE_OPTIONS.map((role) => (
                <option key={role.value} value={role.value}>
                  {role.label}
                </option>
              ))}
            </Select>
          </div>
          <IconButton
            icon={<Trash2 strokeWidth={1.75} />}
            label="Remove membership"
            disabled={disabled}
            onClick={() => onChange(memberships.filter((_, i) => i !== index))}
            danger
          />
        </div>
      ))}
      <div className="flex flex-wrap items-center justify-between gap-2 pt-1">
        <Button
          variant="ghost"
          size="xs"
          icon={<Plus strokeWidth={1.75} />}
          disabled={disabled || !firstFreeTenant}
          onClick={() =>
            firstFreeTenant &&
            onChange([...memberships, { tenant_id: firstFreeTenant.id, role: 'viewer' }])
          }
        >
          Add tenant
        </Button>
        <p className="flex flex-wrap gap-x-3 text-xs text-slate-500">
          <span>
            <span className="text-slate-400">Admin</span> manages
          </span>
          <span>
            <span className="text-slate-400">Editor</span> modifies
          </span>
          <span>
            <span className="text-slate-400">Viewer</span> reads only
          </span>
        </p>
      </div>
    </div>
  );
}
