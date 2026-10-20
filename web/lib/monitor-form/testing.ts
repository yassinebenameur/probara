import assert from 'node:assert/strict';
import { CreateMonitorRequest, Monitor, MonitorConfig, MonitorType } from '../types';

// Fixtures shared by the lib/monitor-form-*.test.ts payload tests. Not
// imported by the app.

/**
 * A monitor as GET /monitors/{id} returns it: every shared field set to a
 * non-default value, so an edit round trip shows each one surviving. Secret
 * config fields must be passed already masked ("***"), as the API serves them.
 */
export function storedMonitor(type: MonitorType, config: MonitorConfig, overrides: Partial<Monitor> = {}): Monitor {
  return {
    id: 'mon-1',
    tenant_id: 'tenant-1',
    name: 'Stored monitor',
    type,
    config,
    interval_seconds: 120,
    timeout_seconds: 15,
    enabled: false,
    tags: ['prod', 'edge'],
    depends_on_ids: ['dep-1'],
    location_ids: ['loc-a', 'loc-b', 'loc-c'],
    location_quorum: 2,
    created_at: '2026-01-01T00:00:00Z',
    updated_at: '2026-01-02T00:00:00Z',
    consecutive_failures_threshold: 4,
    notification_mode: 'custom',
    dependency_suppression: 'off',
    notification_channels: [{ channel_id: 'ch-1', channel_name: 'On-call', channel_type: 'slack', delay_seconds: 300 }],
    ...overrides,
  };
}

/**
 * A clone source as the Clone action hands it over (`initialData`): every
 * shared field set to a non-default value, alerting included, so a clone test
 * shows which fields the form takes and which it leaves at the defaults.
 */
export function cloneSource(
  type: MonitorType,
  config: MonitorConfig,
  overrides: Partial<CreateMonitorRequest> = {}
): CreateMonitorRequest {
  return {
    name: 'Source (copy)',
    type,
    config,
    interval_seconds: 300,
    timeout_seconds: 20,
    enabled: false,
    tags: ['copied', 'clone'],
    consecutive_failures_threshold: 5,
    notification_mode: 'custom',
    dependency_suppression: 'off',
    notification_channels: [{ channel_id: 'ch-7', delay_seconds: 30 }],
    depends_on_ids: ['dep-2'],
    location_ids: ['loc-y', 'loc-z'],
    location_quorum: 2,
    ...overrides,
  };
}

/** What a scheduled form takes from cloneSource besides its config: no alerting. */
export const CLONED_SCHEDULE = { interval_seconds: 300, timeout_seconds: 20, enabled: false } as const;
export const CLONED_TAIL = {
  tags: ['copied', 'clone'],
  depends_on_ids: ['dep-2'],
  location_ids: ['loc-y', 'loc-z'],
  location_quorum: 2,
} as const;

/** The shared request fields storedMonitor round-trips to, in wire order. */
export const STORED_ALERTING = {
  consecutive_failures_threshold: 4,
  notification_mode: 'custom',
  dependency_suppression: 'off',
  notification_channels: [{ channel_id: 'ch-1', channel_name: 'On-call', channel_type: 'slack', delay_seconds: 300 }],
} as const;

/** Defaults a fresh create submits for the shared alerting fields. */
export const DEFAULT_ALERTING = {
  consecutive_failures_threshold: 2,
  notification_mode: 'default',
  dependency_suppression: 'inherit',
  notification_channels: [],
} as const;

/**
 * Exact wire equality: the request body as JSON.stringify serializes it, so
 * key order and dropped `undefined` fields count. These payloads are the
 * regression oracle for the MonitorForm split.
 */
export function assertWire(actual: unknown, expected: unknown): void {
  assert.equal(JSON.stringify(actual, null, 2), JSON.stringify(expected, null, 2));
}
