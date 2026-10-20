import {
  ChannelAssignment,
  CreateMonitorRequest,
  DependencySuppression,
  GroupMonitorConfig,
  MemberAlertRollup,
  Monitor,
  NotificationMode,
} from '../types';
import { parseTagInput } from './common';

// GroupForm's state and request. A group's members live in the
// monitor_groups junction table: the stored monitor reports them as
// member_ids, and config.monitor_ids is what a create or update sends.

/** GroupForm's initial state: the stored group on edit, else the clone source. */
export function initialGroupFormState(monitor?: Monitor, initialData?: CreateMonitorRequest) {
  const isEditMode = Boolean(monitor);
  const initialGroupConfig =
    !isEditMode && initialData?.type === 'group' ? (initialData.config as GroupMonitorConfig) : undefined;
  const existingMemberIds = monitor?.member_ids || initialGroupConfig?.monitor_ids || [];

  return {
    name: monitor?.name || initialData?.name || '',
    monitor_ids: existingMemberIds,
    enabled: monitor?.enabled ?? initialData?.enabled ?? true,
    tags: monitor?.tags?.join(', ') || (initialData?.tags || []).join(', '),
    notification_mode: (monitor?.notification_mode ?? 'default') as NotificationMode,
    dependency_suppression: (monitor?.dependency_suppression ?? 'inherit') as DependencySuppression,
    member_alert_rollup: (monitor?.member_alert_rollup ?? 'per_monitor') as MemberAlertRollup,
    notification_channels: monitor?.notification_channels ?? [] as ChannelAssignment[],
  };
}

export type GroupFormState = ReturnType<typeof initialGroupFormState>;

/** The state GroupForm re-seeds from when a different group is loaded. */
export function groupFormStateFromMonitor(monitor: Monitor): GroupFormState {
  return {
    name: monitor.name || '',
    monitor_ids: monitor.member_ids || [],
    enabled: monitor.enabled ?? true,
    tags: monitor.tags?.join(', ') || '',
    notification_mode: (monitor.notification_mode ?? 'default') as NotificationMode,
    dependency_suppression: (monitor.dependency_suppression ?? 'inherit') as DependencySuppression,
    member_alert_rollup: (monitor.member_alert_rollup ?? 'per_monitor') as MemberAlertRollup,
    notification_channels: monitor.notification_channels ?? [],
  };
}

/** A blank group, for when the loaded monitor goes away. */
export const emptyGroupFormState = (): GroupFormState => ({
  name: '',
  monitor_ids: [],
  enabled: true,
  tags: '',
  notification_mode: 'default',
  member_alert_rollup: 'per_monitor',
  dependency_suppression: 'inherit',
  notification_channels: [],
});

/**
 * The request GroupForm submits. Groups are never checked themselves, so the
 * schedule is fixed and they carry no threshold, locations or dependencies.
 */
export function buildGroupRequest(form: GroupFormState): CreateMonitorRequest {
  const config: GroupMonitorConfig = { monitor_ids: form.monitor_ids };

  const requestData: CreateMonitorRequest = {
    name: form.name.trim(),
    type: 'group',
    config,
    interval_seconds: 60,
    timeout_seconds: 30,
    enabled: form.enabled,
  };

  requestData.notification_mode = form.notification_mode;
  requestData.dependency_suppression = form.dependency_suppression;
  requestData.member_alert_rollup = form.member_alert_rollup;
  requestData.notification_channels = form.notification_mode === 'custom' ? form.notification_channels : [];
  if (form.tags.trim()) {
    requestData.tags = parseTagInput(form.tags);
  }
  return requestData;
}
