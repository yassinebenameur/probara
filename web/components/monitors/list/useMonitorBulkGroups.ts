'use client';

import { useState, useEffect } from 'react';
import { useToast } from '@/components/ui/ToastProvider';
import { Monitor } from '@/lib/types';
import { createMonitor, addMonitorsToGroup, removeMonitorsFromGroup } from '@/lib/api';
import { groupContainsGroup } from '@/lib/monitor-list';
import { errorMessage } from '@/lib/errors';
import { NO_GROUP_VALUE } from '@/components/monitors/list/constants';

interface MonitorBulkGroupsOptions {
  monitors: Monitor[];
  /** The selected monitors, and the selected ids (an id can outlive its row). */
  selectedMonitors: Monitor[];
  selectedMonitorIds: ReadonlySet<string>;
  groupMembersMap: Record<string, Monitor[]>;
  /** Drops the selection after a group operation succeeded. */
  deselectAll: () => void;
  reloadAll: () => Promise<unknown>;
}

/**
 * The bulk bar's group operations over the selection: create a group from
 * it, or move it into a group (or out of every group).
 */
export function useMonitorBulkGroups({
  monitors,
  selectedMonitors,
  selectedMonitorIds,
  groupMembersMap,
  deselectAll,
  reloadAll,
}: MonitorBulkGroupsOptions) {
  const { showToast } = useToast();
  const [groupNameInput, setGroupNameInput] = useState('');
  const [targetGroupId, setTargetGroupId] = useState('');
  const availableGroups = monitors
    .filter((m) => m.type === 'group' && !selectedMonitorIds.has(m.id))
    .sort((a, b) => a.name.localeCompare(b.name));
  const canCreateGroup = selectedMonitorIds.size > 0 && groupNameInput.trim().length > 0;
  const canMoveToGroup = targetGroupId.length > 0 && selectedMonitors.length > 0;

  useEffect(() => {
    if (!targetGroupId) return;
    if (targetGroupId === NO_GROUP_VALUE) return;
    if (!availableGroups.some((group) => group.id === targetGroupId)) {
      setTargetGroupId('');
    }
  }, [targetGroupId, availableGroups]);


  const handleCreateGroup = async () => {
    const trimmedName = groupNameInput.trim();
    if (!trimmedName) {
      showToast('Group name is required', 'error');
      return;
    }
    if (selectedMonitors.length < 1) {
      showToast('Select at least one item to create a group', 'error');
      return;
    }

    try {
      await createMonitor({
        name: trimmedName,
        type: 'group',
        config: { monitor_ids: selectedMonitors.map((m) => m.id) },
        interval_seconds: 60,
        timeout_seconds: 30,
        enabled: true,
      });
      setGroupNameInput('');
      deselectAll();
      await reloadAll();
      showToast('Group created', 'success');
    } catch (err) {
      showToast(errorMessage(err, 'Failed to create group'), 'error');
    }
  };

  const handleMoveToGroup = async () => {
    if (!targetGroupId) {
      showToast('Select a destination group first', 'error');
      return;
    }
    if (selectedMonitors.length === 0) {
      showToast('Select at least one monitor or group to move', 'error');
      return;
    }

    const isNoGroupDestination = targetGroupId === NO_GROUP_VALUE;
    const targetGroup = isNoGroupDestination
      ? null
      : monitors.find((m) => m.id === targetGroupId && m.type === 'group');
    if (!isNoGroupDestination && !targetGroup) {
      showToast('Destination group not found', 'error');
      return;
    }

    const selectedIds = selectedMonitors.map((m) => m.id);
    const selectedGroupIDs = selectedMonitors
      .filter((monitor) => monitor.type === 'group')
      .map((monitor) => monitor.id);

    if (!isNoGroupDestination) {
      if (selectedGroupIDs.includes(targetGroupId)) {
        showToast('Cannot move a group into itself', 'error');
        return;
      }

      const wouldCreateCycle = selectedGroupIDs.some((groupID) =>
        groupContainsGroup(groupID, targetGroupId, (groupId) => groupMembersMap[groupId] || [])
      );
      if (wouldCreateCycle) {
        showToast('Cannot move a group into one of its descendants', 'error');
        return;
      }
    }

    const removalsByGroup: Record<string, string[]> = {};

    for (const [groupId, members] of Object.entries(groupMembersMap)) {
      if (!isNoGroupDestination && groupId === targetGroupId) continue;
      const overlappingIds = members
        .filter((member) => selectedIds.includes(member.id))
        .map((member) => member.id);
      if (overlappingIds.length > 0) {
        removalsByGroup[groupId] = overlappingIds;
      }
    }

    const hasRemovals = Object.keys(removalsByGroup).length > 0;

    if (isNoGroupDestination && !hasRemovals) {
      showToast('Selected items are not in any group', 'error');
      return;
    }

    const targetMemberIds = new Set(
      isNoGroupDestination ? [] : (groupMembersMap[targetGroupId] || []).map((m) => m.id)
    );
    const idsToAdd = isNoGroupDestination
      ? []
      : selectedIds.filter((id) => !targetMemberIds.has(id));

    if (!isNoGroupDestination && !hasRemovals && idsToAdd.length === 0) {
      showToast('Selected items are already in the target group', 'error');
      return;
    }

    try {
      if (!isNoGroupDestination && idsToAdd.length > 0) {
        await addMonitorsToGroup(targetGroupId, idsToAdd);
      }

      if (hasRemovals) {
        await Promise.all(
          Object.entries(removalsByGroup).map(([groupId, monitorIds]) =>
            removeMonitorsFromGroup(groupId, monitorIds)
          )
        );
      }

      deselectAll();
      await reloadAll();
      showToast(isNoGroupDestination
          ? `Removed ${selectedMonitors.length} item(s) from all groups`
          : `Moved ${selectedMonitors.length} item(s) to ${targetGroup?.name}`, 'success');
    } catch (err) {
      showToast(errorMessage(err, 'Failed to move monitors'), 'error');
    }
  };

  const resetGroupInputs = () => {
    setGroupNameInput('');
    setTargetGroupId('');
  };

  return {
    groupNameInput,
    setGroupNameInput,
    targetGroupId,
    setTargetGroupId,
    availableGroups,
    canCreateGroup,
    canMoveToGroup,
    handleCreateGroup,
    handleMoveToGroup,
    resetGroupInputs,
  };
}
