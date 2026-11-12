'use client';

import { createPortal } from 'react-dom';
import { FolderPlus, Move, Trash2, Bell, X } from 'lucide-react';
import Button from '@/components/ui/Button';
import IconButton from '@/components/ui/IconButton';
import { Monitor } from '@/lib/types';
import { NO_GROUP_VALUE } from '@/components/monitors/list/constants';

interface MonitorBulkBarProps {
  selectedCount: number;
  /** Every shown top-level row is selected. */
  allSelected: boolean;
  shownCount: number;
  onToggleAll: () => void;
  groupNameInput: string;
  onGroupNameChange: (name: string) => void;
  canCreateGroup: boolean;
  onCreateGroup: () => void;
  targetGroupId: string;
  onTargetGroupChange: (groupId: string) => void;
  availableGroups: Monitor[];
  canMoveToGroup: boolean;
  onMoveToGroup: () => void;
  onEditAlerting: () => void;
  onDelete: () => void;
  onClear: () => void;
}

/**
 * Bulk actions: floats over the page while a selection exists, so the list
 * never shifts. Portaled (fixed inside a .glass would be offset). The page
 * renders it only for writers with a selection.
 */
export default function MonitorBulkBar({
  selectedCount,
  allSelected,
  shownCount,
  onToggleAll,
  groupNameInput,
  onGroupNameChange,
  canCreateGroup,
  onCreateGroup,
  targetGroupId,
  onTargetGroupChange,
  availableGroups,
  canMoveToGroup,
  onMoveToGroup,
  onEditAlerting,
  onDelete,
  onClear,
}: MonitorBulkBarProps) {
  if (typeof document === 'undefined') return null;
  return createPortal(
      <div
        data-floating-bar
        className="pointer-events-none fixed inset-x-0 bottom-4 z-50 flex justify-center px-4 transition-[left] duration-slow ease-emphasized sm:bottom-6 md:left-[var(--sidebar-offset)]"
      >
        <div
          role="toolbar"
          aria-label="Bulk actions"
          className="glass-overlay pointer-events-auto flex max-w-full animate-toast-in flex-wrap items-center gap-2 rounded-xl px-3 py-2"
        >
          <span className="px-1 text-ui font-medium tabular-nums text-white" aria-live="polite">
            {selectedCount} selected
          </span>
          <Button variant="ghost" size="xs" onClick={onToggleAll}>
            {allSelected ? 'Deselect all' : `Select all ${shownCount}`}
          </Button>

          <div className="h-4 w-px bg-divider" />

          {/* Group creation */}
          <div className="flex items-center gap-1.5">
            <input
              type="text"
              placeholder="New group name…"
              aria-label="New group name"
              value={groupNameInput}
              onChange={(e) => onGroupNameChange(e.target.value)}
              onKeyDown={(e) => {
                if (e.key === 'Enter' && canCreateGroup) onCreateGroup();
              }}
              className="input input-xs h-7 !w-32"
            />
            <Button
              variant="ghost"
              size="xs"
              icon={<FolderPlus strokeWidth={1.75} />}
              onClick={onCreateGroup}
              disabled={!canCreateGroup}
            >
              Group
            </Button>
          </div>

          {/* Move to existing group */}
          <div className="flex items-center gap-1.5">
            <select
              value={targetGroupId}
              onChange={(e) => onTargetGroupChange(e.target.value)}
              aria-label="Move selected to group"
              className="input input-xs h-7 !w-36"
            >
              <option value="">Move to group…</option>
              <option value={NO_GROUP_VALUE}>No group</option>
              {availableGroups.map((group) => (
                <option key={group.id} value={group.id}>
                  {group.name}
                </option>
              ))}
            </select>
            <Button
              variant="ghost"
              size="xs"
              icon={<Move strokeWidth={1.75} />}
              onClick={onMoveToGroup}
              disabled={!canMoveToGroup}
            >
              Move
            </Button>
          </div>

          <div className="h-4 w-px bg-divider" />

          <Button variant="ghost" size="xs" icon={<Bell strokeWidth={1.75} />} onClick={onEditAlerting}>
            Edit alerting…
          </Button>
          <Button variant="danger" size="xs" icon={<Trash2 strokeWidth={1.75} />} onClick={onDelete}>
            Delete
          </Button>

          <div className="h-4 w-px bg-divider" />

          <IconButton label="Clear selection (Esc)" icon={<X strokeWidth={1.75} />} onClick={onClear} size="sm" />
        </div>
      </div>,
    document.body
  );
}
