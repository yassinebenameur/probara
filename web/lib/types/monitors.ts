import type { MonitorLocationStatus } from './locations';
import type { MonitorConfig, MonitorType } from './monitor-configs';

export type NotificationMode = 'default' | 'custom';

// How a group handles alerts when its members go down:
//   'per_monitor' — each member alerts individually; the group emits no alert.
//   'group'       — members are suppressed; one group-level alert speaks for them.
export type MemberAlertRollup = 'per_monitor' | 'group';

// Whether a monitor pages while an upstream dependency it depends on is down:
//   'inherit' — follow the workspace setting (NotificationSettings.dependency_suppression_enabled)
//   'on'      — never page while an upstream is down; the root cause's alert speaks for it
//   'off'     — always page, whatever the workspace says
export type DependencySuppression = 'inherit' | 'on' | 'off';

export interface ChannelAssignment {
  channel_id: string;
  channel_name?: string;
  channel_type?: string;
  delay_seconds: number;
}

export interface NotificationSettings {
  default_channels: ChannelAssignment[];
  alert_reminder_seconds: number;
  auto_create_incident: boolean;
  latency_anomaly_enabled: boolean;
  latency_baseline_window_hours: number;
  latency_anomaly_sensitivity: number;
  latency_anomaly_min_breach_seconds: number;
  latency_anomaly_min_delta_pct: number;
  // Dependency-aware alerting: suppress a monitor's notifications while an
  // upstream dependency is down, and for the grace period after it recovers.
  dependency_suppression_enabled: boolean;
  dependency_suppression_grace_seconds: number;
}

export type AlertKind = 'availability' | 'latency_anomaly' | 'host_metric' | 'mesh_edge' | 'tls_expiry';

export type MonitorState = 'unknown' | 'up' | 'suspect' | 'down' | 'degraded';

export interface Monitor {
  id: string;
  tenant_id: string;
  name: string;
  type: MonitorType;
  config?: MonitorConfig; // Optional for backward compatibility
  interval_seconds: number;
  timeout_seconds: number;
  alert_policy_id?: string;
  alert_policy_ids?: string[];
  enabled: boolean;
  tags?: string[];
  next_run_at?: string;
  agent_id?: string; // Unique identifier for agent monitors
  push_token?: string; // Unique token for push monitors
  member_ids?: string[]; // Populated for group monitors
  depends_on_ids?: string[]; // Upstream monitors this one depends on
  location_ids?: string[]; // Private locations; empty/absent = default fleet
  location_quorum?: number; // Locations that must fail before the monitor is down
  locations?: MonitorLocationStatus[]; // Embedded only on GET /v1/monitors/{id}
  created_at: string;
  updated_at: string;
  consecutive_failures_threshold: number;
  notification_mode: NotificationMode;
  member_alert_rollup?: MemberAlertRollup;
  dependency_suppression?: DependencySuppression;
  notification_channels?: ChannelAssignment[];
  current_state?: MonitorState;
  alert_routing?: AlertRouting; // Read-only; who this monitor's alerts reach
  in_maintenance?: boolean;
  maintenance_until?: string; // Latest ends_at among covering active windows
}

/**
 * Where a monitor's alerts actually go. Computed per request from
 * shared/alertrouting — the same rules the alerter dispatches by — so
 * `reachable: false` means an alert here would notify nobody.
 */
export interface AlertRouting {
  reachable: boolean;
  /**
   * `custom` / `tenant_default`: the monitor's own routing applies.
   * `group_rollup`: a group rolls its alerts up, so the group's routing decides.
   * `members`: it is a group that never alerts itself; members alert individually.
   */
  source: 'custom' | 'tenant_default' | 'group_rollup' | 'members';
  active_channels: number;
  /** Includes disabled channels, which never deliver. */
  assigned_channels: number;
  reason?:
    | 'no_custom_channels'
    | 'custom_channels_disabled'
    | 'no_tenant_default_channels'
    | 'tenant_default_channels_disabled'
    | 'group_rollup_unrouted'
    | 'group_rollup_paused';
  rollup_group_id?: string;
  rollup_group_name?: string;
}

export interface CreateMonitorRequest {
  name: string;
  type: MonitorType;
  config: MonitorConfig;
  interval_seconds: number;
  timeout_seconds: number;
  alert_policy_id?: string;
  alert_policy_ids?: string[];
  enabled?: boolean;
  tags?: string[];
  consecutive_failures_threshold?: number;
  notification_mode?: NotificationMode;
  member_alert_rollup?: MemberAlertRollup;
  dependency_suppression?: DependencySuppression;
  notification_channels?: ChannelAssignment[];
  depends_on_ids?: string[];
  location_ids?: string[];
  location_quorum?: number;
}

export interface UpdateMonitorRequest {
  name?: string;
  type?: MonitorType;
  config?: MonitorConfig;
  interval_seconds?: number;
  timeout_seconds?: number;
  alert_policy_id?: string;
  alert_policy_ids?: string[];
  enabled?: boolean;
  tags?: string[];
  consecutive_failures_threshold?: number;
  notification_mode?: NotificationMode;
  member_alert_rollup?: MemberAlertRollup;
  dependency_suppression?: DependencySuppression;
  notification_channels?: ChannelAssignment[];
  depends_on_ids?: string[];
  location_ids?: string[];
  location_quorum?: number;
}

export interface MonitorListResponse {
  items: Monitor[];
  page: number;
  page_size: number;
  total: number;
}

// Agent Install Command types (OpenTelemetry Collector distribution)
export interface AgentInstallCommand {
  agent_id: string;
  backend_url: string;
  install_script: string;
  windows_install_script: string;
  uninstall_script: string;
  windows_uninstall_script: string;
  // Generated collector YAML (linux variant); per-platform variants come from
  // GET /v1/monitors/{id}/agent/config.yaml?platform=linux|darwin|windows.
  collector_config: string;
  collector_version: string;
  download_url: string;
  interval_seconds: number;
}

// Push Info types
export interface PushInfo {
  push_token: string;
  webhook_url: string;
  interval_seconds: number;
  grace_period_seconds: number;
  example_curl: string;
}
