import {
  ChannelAssignment,
  CreateMonitorRequest,
  DependencySuppression,
  Monitor,
  MonitorConfig,
  MonitorType,
  NotificationMode,
} from '../types';

// Fields every monitor form shares (name, schedule, locations, alerting,
// tags), and the request envelope they serialize into. The per-type modules
// next to this one build the `config` that goes inside it.

/** The alerting fields of a form's state. */
export interface AlertingFormFields {
  consecutive_failures_threshold: number;
  notification_mode: NotificationMode;
  dependency_suppression: DependencySuppression;
  notification_channels: ChannelAssignment[];
}

/** The shared fields of a form for a scheduled (actively checked) monitor. */
export interface ScheduledFormFields extends AlertingFormFields {
  name: string;
  interval_seconds: number;
  timeout_seconds: number;
  enabled: boolean;
  /** Comma-separated, as typed. */
  tags: string;
  depends_on_ids: string[];
  location_ids: string[];
  location_quorum: number;
}

/** The shared fields of a push or agent form: no schedule, no locations. */
export interface HeartbeatFormFields extends AlertingFormFields {
  name: string;
  enabled: boolean;
  tags: string;
  depends_on_ids: string[];
}

/** "a, b,, c" -> ["a", "b", "c"]. */
export const parseTagInput = (tags: string): string[] =>
  tags.split(',').map((t) => t.trim()).filter((t) => t);

/** The quorum a submit sends: only meaningful with two or more locations. */
export const effectiveLocationQuorum = (locationIds: string[], quorum: number): number =>
  locationIds.length >= 2 ? Math.min(quorum, locationIds.length) : 1;

/**
 * The shared form state of the scheduled-monitor forms that hydrate it the
 * same way (gRPC, TCP, WebSocket, the database forms, SIP): the stored
 * monitor on edit, else the clone source, else the defaults.
 */
export function initialScheduledFormFields(monitor?: Monitor, initialData?: CreateMonitorRequest) {
  return {
    name: monitor?.name || initialData?.name || '',
    interval_seconds: monitor?.interval_seconds || initialData?.interval_seconds || 60,
    timeout_seconds: monitor?.timeout_seconds || initialData?.timeout_seconds || 10,
    enabled: monitor?.enabled ?? initialData?.enabled ?? true,
    tags: monitor?.tags?.join(', ') || (initialData?.tags || []).join(', '),
    consecutive_failures_threshold: monitor?.consecutive_failures_threshold ?? 2,
    notification_mode: (monitor?.notification_mode ?? 'default') as NotificationMode,
    dependency_suppression: (monitor?.dependency_suppression ?? 'inherit') as DependencySuppression,
    notification_channels: monitor?.notification_channels ?? ([] as ChannelAssignment[]),
    location_ids: monitor?.location_ids ?? initialData?.location_ids ?? ([] as string[]),
    location_quorum: monitor?.location_quorum ?? initialData?.location_quorum ?? 1,
    depends_on_ids: monitor?.depends_on_ids ?? initialData?.depends_on_ids ?? ([] as string[]),
  };
}

/**
 * The request a scheduled-monitor form submits. Channels are sent only in
 * custom mode (an empty list otherwise); tags are omitted when the input is
 * blank unless `alwaysSendTags`, which sends `[]` so a save clears them.
 */
export function buildScheduledMonitorRequest(
  type: MonitorType,
  config: MonitorConfig,
  form: ScheduledFormFields,
  options: { alwaysSendTags?: boolean } = {}
): CreateMonitorRequest {
  const requestData: CreateMonitorRequest = {
    name: form.name.trim(),
    type,
    config,
    interval_seconds: form.interval_seconds,
    timeout_seconds: form.timeout_seconds,
    enabled: form.enabled,
  };

  requestData.consecutive_failures_threshold = form.consecutive_failures_threshold;
  requestData.notification_mode = form.notification_mode;
  requestData.dependency_suppression = form.dependency_suppression;
  requestData.notification_channels = form.notification_mode === 'custom' ? form.notification_channels : [];
  if (options.alwaysSendTags || form.tags.trim()) {
    requestData.tags = parseTagInput(form.tags);
  }
  requestData.depends_on_ids = form.depends_on_ids;
  requestData.location_ids = form.location_ids;
  requestData.location_quorum = effectiveLocationQuorum(form.location_ids, form.location_quorum);
  return requestData;
}

/**
 * The request a push or agent form submits: the heartbeat interval doubles
 * as interval and timeout, and these monitors take no locations.
 */
export function buildHeartbeatMonitorRequest(
  type: MonitorType,
  config: MonitorConfig,
  form: HeartbeatFormFields,
  expectedIntervalSeconds: number
): CreateMonitorRequest {
  const requestData: CreateMonitorRequest = {
    name: form.name.trim(),
    type,
    config,
    interval_seconds: expectedIntervalSeconds,
    timeout_seconds: expectedIntervalSeconds,
    enabled: form.enabled,
  };

  requestData.consecutive_failures_threshold = form.consecutive_failures_threshold;
  requestData.notification_mode = form.notification_mode;
  requestData.dependency_suppression = form.dependency_suppression;
  requestData.notification_channels = form.notification_mode === 'custom' ? form.notification_channels : [];
  if (form.tags.trim()) {
    requestData.tags = parseTagInput(form.tags);
  }
  requestData.depends_on_ids = form.depends_on_ids;
  return requestData;
}

/**
 * The alerting change a Notify choice makes, or null for none. A segmented
 * control re-fires its selected segment (a click, Home/End), so re-choosing
 * the current mode is a no-op: re-picking Custom must not re-tick the
 * defaults on a monitor muted on purpose (custom, no channels). Switching to
 * Custom with nothing picked pre-checks the workspace default channels.
 */
export function notifyModeChange(
  next: NotificationMode,
  current: NotificationMode,
  customChannels: ChannelAssignment[],
  defaultChannels: ChannelAssignment[],
): { mode: NotificationMode; channels?: ChannelAssignment[] } | null {
  if (next === current) return null;
  if (next === 'custom' && customChannels.length === 0 && defaultChannels.length > 0) {
    return {
      mode: next,
      channels: defaultChannels.map((c) => ({ channel_id: c.channel_id, delay_seconds: c.delay_seconds })),
    };
  }
  return { mode: next };
}
