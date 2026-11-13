import {
  ChannelAssignment,
  CreateMonitorRequest,
  DependencySuppression,
  Monitor,
  NotificationMode,
  PrometheusMonitorConfig,
} from '../types';
import { buildScheduledMonitorRequest, unmaskedCloneSecret } from './common';

// PrometheusForm's state and request. password and bearer_token are monitor
// secrets: the stored monitor carries "***", which the form submits back
// untouched (keep), and the credential of an auth type no longer selected is
// sent as "" (clear).

/**
 * PrometheusForm's initial state: the stored monitor on edit, else the clone
 * source. Unlike the other forms, alerting settings also come from a clone.
 */
export function initialPrometheusFormState(monitor?: Monitor, initialData?: CreateMonitorRequest) {
  const isEditMode = Boolean(monitor);
  const initialConfig =
    !isEditMode && initialData?.type === 'prometheus' ? (initialData.config as PrometheusMonitorConfig) : undefined;
  const existingConfig = monitor && monitor.type === 'prometheus' ? (monitor.config as PrometheusMonitorConfig) : undefined;

  return {
    name: monitor?.name || initialData?.name || '',
    url: existingConfig?.url ?? initialConfig?.url ?? '',
    query: existingConfig?.query ?? initialConfig?.query ?? '',
    operator: existingConfig?.operator ?? initialConfig?.operator ?? 'lt',
    threshold: existingConfig?.threshold ?? initialConfig?.threshold ?? 5,
    no_data_status: existingConfig?.no_data_status ?? initialConfig?.no_data_status ?? 'failure',
    auth_type: existingConfig?.auth_type ?? initialConfig?.auth_type ?? 'none',
    username: existingConfig?.username ?? initialConfig?.username ?? '',
    password: existingConfig?.password ?? unmaskedCloneSecret(initialConfig?.password) ?? '',
    bearer_token: existingConfig?.bearer_token ?? unmaskedCloneSecret(initialConfig?.bearer_token) ?? '',
    interval_seconds: monitor?.interval_seconds || initialData?.interval_seconds || 60,
    timeout_seconds: monitor?.timeout_seconds || initialData?.timeout_seconds || 10,
    enabled: monitor?.enabled ?? initialData?.enabled ?? true,
    tags: monitor?.tags?.join(', ') || (initialData?.tags || []).join(', '),
    consecutive_failures_threshold: monitor?.consecutive_failures_threshold ?? initialData?.consecutive_failures_threshold ?? 2,
    notification_mode: (monitor?.notification_mode ?? initialData?.notification_mode ?? 'default') as NotificationMode,
    dependency_suppression: (monitor?.dependency_suppression ?? initialData?.dependency_suppression ?? 'inherit') as DependencySuppression,
    notification_channels: monitor?.notification_channels ?? initialData?.notification_channels ?? [] as ChannelAssignment[],
    location_ids: monitor?.location_ids ?? initialData?.location_ids ?? ([] as string[]),
    location_quorum: monitor?.location_quorum ?? initialData?.location_quorum ?? 1,
    depends_on_ids: monitor?.depends_on_ids ?? initialData?.depends_on_ids ?? ([] as string[]),
  };
}

export type PrometheusFormState = ReturnType<typeof initialPrometheusFormState>;

/** The config PrometheusForm submits and previews. */
export const buildPrometheusConfig = (form: PrometheusFormState): PrometheusMonitorConfig => ({
  url: form.url.trim(), query: form.query.trim(),
  operator: form.operator, threshold: form.threshold,
  no_data_status: form.no_data_status, auth_type: form.auth_type,
  username: form.auth_type === 'basic' ? form.username : '',
  password: form.auth_type === 'basic' ? form.password : '',
  bearer_token: form.auth_type === 'bearer' ? form.bearer_token : '',
});

/** The request PrometheusForm submits. Tags are always sent, so a save can clear them. */
export function buildPrometheusRequest(form: PrometheusFormState): CreateMonitorRequest {
  return buildScheduledMonitorRequest('prometheus', buildPrometheusConfig(form), form, { alwaysSendTags: true });
}
