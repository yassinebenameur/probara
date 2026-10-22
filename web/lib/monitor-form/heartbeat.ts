import {
  AgentMonitorConfig,
  ChannelAssignment,
  CreateMonitorRequest,
  DependencySuppression,
  MetricRule,
  Monitor,
  NotificationMode,
  PushMonitorConfig,
} from '../types';
import { buildHeartbeatMonitorRequest } from './common';

// PushForm's and AgentForm's state and requests: monitors fed by the
// monitored side (heartbeats, collector exports) rather than checked on a
// schedule, so they have no timeout or locations of their own.

/** PushForm's initial state: the stored monitor on edit, else the clone source. */
export function initialPushFormState(monitor?: Monitor, initialData?: CreateMonitorRequest) {
  const isEditMode = Boolean(monitor);
  const initialPushConfig =
    !isEditMode && initialData?.type === 'push' ? (initialData.config as PushMonitorConfig) : undefined;

  return {
    name: monitor?.name || initialData?.name || '',
    expected_interval_seconds: monitor && monitor.type === 'push'
      ? (monitor.config as PushMonitorConfig)?.expected_interval_seconds || 60
      : initialPushConfig?.expected_interval_seconds || 60,
    grace_period_seconds: monitor && monitor.type === 'push'
      ? (monitor.config as PushMonitorConfig)?.grace_period_seconds || 120
      : initialPushConfig?.grace_period_seconds || 120,
    enabled: monitor?.enabled ?? initialData?.enabled ?? true,
    tags: monitor?.tags?.join(', ') || (initialData?.tags || []).join(', '),
    consecutive_failures_threshold: monitor?.consecutive_failures_threshold ?? 2,
    notification_mode: (monitor?.notification_mode ?? 'default') as NotificationMode,
    dependency_suppression: (monitor?.dependency_suppression ?? 'inherit') as DependencySuppression,
    notification_channels: monitor?.notification_channels ?? [] as ChannelAssignment[],
    depends_on_ids: monitor?.depends_on_ids ?? initialData?.depends_on_ids ?? ([] as string[]),
  };
}

export type PushFormState = ReturnType<typeof initialPushFormState>;

/**
 * The request PushForm submits. push_token is the redundant config copy of
 * monitors.push_token (a monitor secret): the stored monitor's top-level
 * token on edit, "" on create, where the API mints one.
 */
export function buildPushRequest(form: PushFormState, monitor?: Monitor): CreateMonitorRequest {
  const isEditMode = Boolean(monitor);
  const config: PushMonitorConfig = {
    push_token: isEditMode ? monitor?.push_token || '' : '',
    expected_interval_seconds: form.expected_interval_seconds,
    grace_period_seconds: form.grace_period_seconds,
  };

  return buildHeartbeatMonitorRequest('push', config, form, form.expected_interval_seconds);
}

/** The metric rules an agent monitor starts with: stored, else the clone's. */
export function initialAgentRules(monitor?: Monitor, initialData?: CreateMonitorRequest): MetricRule[] {
  const isEditMode = Boolean(monitor);
  const initialAgentConfig =
    !isEditMode && initialData?.type === 'agent' ? (initialData.config as AgentMonitorConfig) : undefined;
  return (
    (monitor && monitor.type === 'agent'
      ? (monitor.config as AgentMonitorConfig).metric_rules
      : initialAgentConfig?.metric_rules) || []
  );
}

/** AgentForm's initial state (its metric rules are a separate draft list). */
export function initialAgentFormState(monitor?: Monitor, initialData?: CreateMonitorRequest) {
  const isEditMode = Boolean(monitor);
  const initialAgentConfig =
    !isEditMode && initialData?.type === 'agent' ? (initialData.config as AgentMonitorConfig) : undefined;

  return {
    name: monitor?.name || initialData?.name || '',
    expected_interval_seconds: monitor && monitor.type === 'agent'
      ? (monitor.config as AgentMonitorConfig).expected_interval_seconds
      : initialAgentConfig?.expected_interval_seconds || 60,
    enabled: monitor?.enabled ?? initialData?.enabled ?? true,
    tags: monitor?.tags?.join(', ') || (initialData?.tags || []).join(', '),
    consecutive_failures_threshold: monitor?.consecutive_failures_threshold ?? 2,
    notification_mode: (monitor?.notification_mode ?? 'default') as NotificationMode,
    dependency_suppression: (monitor?.dependency_suppression ?? 'inherit') as DependencySuppression,
    notification_channels: monitor?.notification_channels ?? [] as ChannelAssignment[],
    depends_on_ids: monitor?.depends_on_ids ?? initialData?.depends_on_ids ?? ([] as string[]),
  };
}

export type AgentFormState = ReturnType<typeof initialAgentFormState>;

/**
 * The request AgentForm submits, with the rules already parsed from their
 * drafts (rulesFromDrafts). agent_id is the stored monitor's on edit and ""
 * on create, where the API assigns one.
 */
export function buildAgentRequest(form: AgentFormState, rules: MetricRule[], monitor?: Monitor): CreateMonitorRequest {
  const isEditMode = Boolean(monitor);
  const config: AgentMonitorConfig = {
    agent_id: isEditMode ? monitor?.agent_id || '' : '',
    expected_interval_seconds: form.expected_interval_seconds,
  };
  if (rules.length > 0) {
    config.metric_rules = rules;
  }

  return buildHeartbeatMonitorRequest('agent', config, form, form.expected_interval_seconds);
}
