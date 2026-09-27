import type {
  ChannelAssignment,
  CreateMonitorRequest,
  DependencySuppression,
  Monitor,
  MonitorConfig,
  MonitorType,
  NotificationMode,
} from '../types';
import type { FormErrors } from './common';

/** Fields every inline monitor type shares: naming, schedule, alerting, placement, meta. */
export interface SharedMonitorFields {
  name: string;
  interval_seconds: number;
  timeout_seconds: number;
  consecutive_failures_threshold: number;
  notification_mode: NotificationMode;
  dependency_suppression: DependencySuppression;
  notification_channels: ChannelAssignment[];
  enabled: boolean;
  /** Comma-separated, as typed. */
  tags: string;
  depends_on_ids: string[];
  location_ids: string[];
  location_quorum: number;
}

/**
 * Seeds the shared fields from the monitor being edited, or from clone data.
 * Alerting fields only come from a saved monitor: clone data does not carry them.
 */
export function initialSharedFields(monitor?: Monitor, initialData?: CreateMonitorRequest): SharedMonitorFields {
  return {
    name: monitor?.name || initialData?.name || '',
    interval_seconds: monitor?.interval_seconds || initialData?.interval_seconds || 60,
    timeout_seconds: monitor?.timeout_seconds || initialData?.timeout_seconds || 30,
    consecutive_failures_threshold: monitor?.consecutive_failures_threshold ?? 2,
    notification_mode: monitor?.notification_mode ?? 'default',
    dependency_suppression: monitor?.dependency_suppression ?? 'inherit',
    notification_channels: monitor?.notification_channels ?? [],
    enabled: monitor?.enabled ?? initialData?.enabled ?? true,
    tags: (monitor?.tags || initialData?.tags || []).join(', '),
    depends_on_ids: monitor?.depends_on_ids ?? initialData?.depends_on_ids ?? [],
    location_ids: monitor?.location_ids ?? initialData?.location_ids ?? [],
    location_quorum: monitor?.location_quorum ?? initialData?.location_quorum ?? 1,
  };
}

export function validateSharedFields(fields: SharedMonitorFields): FormErrors {
  const errors: FormErrors = {};
  if (!fields.name.trim()) errors.name = 'Name is required';
  if (fields.timeout_seconds >= fields.interval_seconds) {
    errors.timeout_seconds = 'Timeout must be less than interval';
  }
  return errors;
}

export const parseTags = (tags: string): string[] => tags.split(',').map((t) => t.trim()).filter((t) => t);

/** Quorum only means something with two or more locations, and never exceeds their count. */
export const effectiveLocationQuorum = (fields: Pick<SharedMonitorFields, 'location_ids' | 'location_quorum'>): number =>
  fields.location_ids.length >= 2 ? Math.min(fields.location_quorum, fields.location_ids.length) : 1;

/**
 * Assembles the create/update body. The same shape serves both endpoints: the
 * form always sends the full field set. `tags` is omitted when blank.
 */
export function buildMonitorRequest(
  type: MonitorType,
  config: MonitorConfig,
  fields: SharedMonitorFields
): CreateMonitorRequest {
  const request: CreateMonitorRequest = {
    name: fields.name.trim(),
    type,
    config,
    interval_seconds: fields.interval_seconds,
    timeout_seconds: fields.timeout_seconds,
    enabled: fields.enabled,
    consecutive_failures_threshold: fields.consecutive_failures_threshold,
    notification_mode: fields.notification_mode,
    dependency_suppression: fields.dependency_suppression,
    notification_channels: fields.notification_mode === 'custom' ? fields.notification_channels : [],
  };
  if (fields.tags.trim()) request.tags = parseTags(fields.tags);
  request.depends_on_ids = fields.depends_on_ids;
  request.location_ids = fields.location_ids;
  request.location_quorum = effectiveLocationQuorum(fields);
  return request;
}
