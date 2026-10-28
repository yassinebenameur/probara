import assert from 'node:assert/strict';
import test from 'node:test';
import {
  buildAgentRequest,
  buildPushRequest,
  initialAgentFormState,
  initialAgentRules,
  initialPushFormState,
} from './monitor-form/heartbeat';
import {
  buildGroupRequest,
  emptyGroupFormState,
  groupFormStateFromMonitor,
  initialGroupFormState,
} from './monitor-form/group';
import { buildPrometheusRequest, initialPrometheusFormState } from './monitor-form/prometheus';
import { draftsFromRules, rulesFromDrafts } from './monitor-form/metric-rules';
import { DEFAULT_ALERTING, STORED_ALERTING, assertWire, cloneSource, storedMonitor } from './monitor-form/testing';
import type { AgentMonitorConfig, CreateMonitorRequest, PrometheusMonitorConfig } from './types';

// Agent, push, group and Prometheus. Push carries the push_token secret
// copy, Prometheus password and bearer_token; agent and group have none.

// --- agent --------------------------------------------------------------------

test('agent: create from defaults', () => {
  const form = { ...initialAgentFormState(), name: 'web-01' };
  const { rules, errors } = rulesFromDrafts(draftsFromRules(initialAgentRules()));
  assert.deepEqual(errors, {});
  assertWire(buildAgentRequest(form, rules), {
    name: 'web-01',
    type: 'agent',
    config: { agent_id: '', expected_interval_seconds: 60 },
    interval_seconds: 60,
    timeout_seconds: 60,
    enabled: true,
    ...DEFAULT_ALERTING,
    depends_on_ids: [],
  });
});

test('agent: create from a filled form with metric rules', () => {
  const form = {
    ...initialAgentFormState(),
    name: 'web-01',
    expected_interval_seconds: 30,
    tags: 'hosts',
    depends_on_ids: ['dep-1'],
  };
  const { rules, errors } = rulesFromDrafts([
    {
      metric_name: ' system.cpu.utilization ',
      filters: [{ key: 'state', value: 'used' }],
      operator: '>=',
      threshold: '85',
      for_duration_seconds: 300,
    },
    { metric_name: 'system.disk.io', filters: [], operator: '<=', threshold: '1000', for_duration_seconds: 0 },
  ]);
  assert.deepEqual(errors, {});
  assertWire(buildAgentRequest(form, rules), {
    name: 'web-01',
    type: 'agent',
    config: {
      agent_id: '',
      expected_interval_seconds: 30,
      metric_rules: [
        {
          metric_name: 'system.cpu.utilization',
          operator: '>=',
          threshold: 0.85,
          attribute_filters: { state: 'used' },
          for_duration_seconds: 300,
        },
        { metric_name: 'system.disk.io', operator: '<=', threshold: 1000 },
      ],
    },
    interval_seconds: 30,
    timeout_seconds: 30,
    enabled: true,
    ...DEFAULT_ALERTING,
    tags: ['hosts'],
    depends_on_ids: ['dep-1'],
  });
});

test('agent: edit round trip keeps agent_id from the monitor and the rules in native units', () => {
  const config: AgentMonitorConfig = {
    agent_id: 'agent-xyz',
    expected_interval_seconds: 45,
    metric_rules: [
      { metric_name: 'system.memory.utilization', attribute_filters: { state: 'used' }, operator: '>=', threshold: 0.9 },
    ],
  };
  const monitor = storedMonitor('agent', config, { agent_id: 'agent-xyz' });
  const drafts = draftsFromRules(initialAgentRules(monitor));
  assert.equal(drafts[0].threshold, '90');
  const { rules } = rulesFromDrafts(drafts);
  assertWire(buildAgentRequest(initialAgentFormState(monitor), rules, monitor), {
    name: 'Stored monitor',
    type: 'agent',
    config: {
      agent_id: 'agent-xyz',
      expected_interval_seconds: 45,
      metric_rules: [
        { metric_name: 'system.memory.utilization', operator: '>=', threshold: 0.9, attribute_filters: { state: 'used' } },
      ],
    },
    interval_seconds: 45,
    timeout_seconds: 45,
    enabled: false,
    ...STORED_ALERTING,
    tags: ['prod', 'edge'],
    depends_on_ids: ['dep-1'],
  });
  // agent_id comes from the monitor, not its config.
  const withoutTopLevelId = storedMonitor('agent', config);
  const request = buildAgentRequest(initialAgentFormState(withoutTopLevelId), rules, withoutTopLevelId);
  assert.equal((request.config as AgentMonitorConfig).agent_id, '');
});

test('agent: a clone takes the interval and metric rules from initialData, not its alerting', () => {
  const initialData = cloneSource('agent', {
    agent_id: '',
    expected_interval_seconds: 30,
    metric_rules: [{ metric_name: 'system.cpu.utilization', operator: '>=', threshold: 0.8, for_duration_seconds: 120 }],
  } as AgentMonitorConfig);
  const drafts = draftsFromRules(initialAgentRules(undefined, initialData));
  assert.equal(drafts[0].threshold, '80');
  const { rules } = rulesFromDrafts(drafts);
  assertWire(buildAgentRequest(initialAgentFormState(undefined, initialData), rules), {
    name: 'Source (copy)',
    type: 'agent',
    config: {
      agent_id: '',
      expected_interval_seconds: 30,
      metric_rules: [{ metric_name: 'system.cpu.utilization', operator: '>=', threshold: 0.8, for_duration_seconds: 120 }],
    },
    interval_seconds: 30,
    timeout_seconds: 30,
    enabled: false,
    ...DEFAULT_ALERTING,
    tags: ['copied', 'clone'],
    depends_on_ids: ['dep-2'],
  });
});

// --- push -----------------------------------------------------------------------

test('push: create from defaults', () => {
  const form = { ...initialPushFormState(), name: 'Nightly job' };
  assertWire(buildPushRequest(form), {
    name: 'Nightly job',
    type: 'push',
    config: { push_token: '', expected_interval_seconds: 60, grace_period_seconds: 120 },
    interval_seconds: 60,
    timeout_seconds: 60,
    enabled: true,
    ...DEFAULT_ALERTING,
    depends_on_ids: [],
  });
});

test('push: create from a filled form', () => {
  const form = {
    ...initialPushFormState(),
    name: 'Nightly job',
    // PushForm doubles the interval into the grace period on create.
    expected_interval_seconds: 300,
    grace_period_seconds: 600,
    enabled: false,
    tags: 'batch',
    consecutive_failures_threshold: 1,
  };
  assertWire(buildPushRequest(form), {
    name: 'Nightly job',
    type: 'push',
    config: { push_token: '', expected_interval_seconds: 300, grace_period_seconds: 600 },
    interval_seconds: 300,
    timeout_seconds: 300,
    enabled: false,
    consecutive_failures_threshold: 1,
    notification_mode: 'default',
    dependency_suppression: 'inherit',
    notification_channels: [],
    tags: ['batch'],
    depends_on_ids: [],
  });
});

test('push: edit round trip sends the monitor\'s push_token, never the masked config copy', () => {
  const monitor = storedMonitor(
    'push',
    { push_token: '***', expected_interval_seconds: 45, grace_period_seconds: 90 },
    { push_token: 'tok-123' }
  );
  assertWire(buildPushRequest(initialPushFormState(monitor), monitor), {
    name: 'Stored monitor',
    type: 'push',
    config: { push_token: 'tok-123', expected_interval_seconds: 45, grace_period_seconds: 90 },
    interval_seconds: 45,
    timeout_seconds: 45,
    enabled: false,
    ...STORED_ALERTING,
    tags: ['prod', 'edge'],
    depends_on_ids: ['dep-1'],
  });
  // Without the top-level token the config copy is cleared with "".
  const noToken = storedMonitor('push', { push_token: '***', expected_interval_seconds: 45, grace_period_seconds: 90 });
  assert.equal((buildPushRequest(initialPushFormState(noToken), noToken).config as { push_token: string }).push_token, '');
});

test('push: a clone takes the heartbeat timing from initialData, not its alerting or token', () => {
  const initialData = cloneSource('push', { push_token: '', expected_interval_seconds: 45, grace_period_seconds: 90 });
  assertWire(buildPushRequest(initialPushFormState(undefined, initialData)), {
    name: 'Source (copy)',
    type: 'push',
    config: { push_token: '', expected_interval_seconds: 45, grace_period_seconds: 90 },
    interval_seconds: 45,
    timeout_seconds: 45,
    enabled: false,
    ...DEFAULT_ALERTING,
    tags: ['copied', 'clone'],
    depends_on_ids: ['dep-2'],
  });
});

// --- group ------------------------------------------------------------------------

test('group: create from defaults', () => {
  const form = { ...initialGroupFormState(), name: 'Production', monitor_ids: ['m1'] };
  assertWire(buildGroupRequest(form), {
    name: 'Production',
    type: 'group',
    config: { monitor_ids: ['m1'] },
    interval_seconds: 60,
    timeout_seconds: 30,
    enabled: true,
    notification_mode: 'default',
    dependency_suppression: 'inherit',
    member_alert_rollup: 'per_monitor',
    notification_channels: [],
  });
});

test('group: create from a filled form', () => {
  const form = {
    ...initialGroupFormState(),
    name: ' Production ',
    monitor_ids: ['m1', 'm2'],
    tags: 'prod, ',
    notification_mode: 'custom' as const,
    dependency_suppression: 'on' as const,
    member_alert_rollup: 'group' as const,
    notification_channels: [{ channel_id: 'ch-3', delay_seconds: 0 }],
  };
  assertWire(buildGroupRequest(form), {
    name: 'Production',
    type: 'group',
    config: { monitor_ids: ['m1', 'm2'] },
    interval_seconds: 60,
    timeout_seconds: 30,
    enabled: true,
    notification_mode: 'custom',
    dependency_suppression: 'on',
    member_alert_rollup: 'group',
    notification_channels: [{ channel_id: 'ch-3', delay_seconds: 0 }],
    tags: ['prod'],
  });
});

test('group: edit round trip reads members from member_ids, not config.monitor_ids', () => {
  const monitor = storedMonitor('group', { monitor_ids: ['stale'] }, { member_ids: ['m1', 'm2'], member_alert_rollup: 'group' });
  const form = initialGroupFormState(monitor);
  assert.deepEqual(groupFormStateFromMonitor(monitor), form);
  assertWire(buildGroupRequest(form), {
    name: 'Stored monitor',
    type: 'group',
    config: { monitor_ids: ['m1', 'm2'] },
    interval_seconds: 60,
    timeout_seconds: 30,
    enabled: false,
    notification_mode: 'custom',
    dependency_suppression: 'off',
    member_alert_rollup: 'group',
    notification_channels: STORED_ALERTING.notification_channels,
    tags: ['prod', 'edge'],
  });
  assertWire(buildGroupRequest(emptyGroupFormState()).config, { monitor_ids: [] });
});

test('group: a clone takes its members from config.monitor_ids', () => {
  const initialData: CreateMonitorRequest = {
    name: 'Copy',
    type: 'group',
    config: { monitor_ids: ['m3'] },
    interval_seconds: 60,
    timeout_seconds: 30,
  };
  assertWire(buildGroupRequest(initialGroupFormState(undefined, initialData)).config, { monitor_ids: ['m3'] });
  // Name, enabled and tags carry over; alerting and roll-up do not.
  const full = cloneSource('group', { monitor_ids: ['m3', 'm4'] }, { member_alert_rollup: 'group' });
  assertWire(buildGroupRequest(initialGroupFormState(undefined, full)), {
    name: 'Source (copy)',
    type: 'group',
    config: { monitor_ids: ['m3', 'm4'] },
    interval_seconds: 60,
    timeout_seconds: 30,
    enabled: false,
    notification_mode: 'default',
    dependency_suppression: 'inherit',
    member_alert_rollup: 'per_monitor',
    notification_channels: [],
    tags: ['copied', 'clone'],
  });
});

// --- prometheus -----------------------------------------------------------------------

test('prometheus: create from defaults', () => {
  const form = {
    ...initialPrometheusFormState(),
    name: 'Error rate',
    url: ' https://prom.example.com ',
    query: ' sum(rate(errors[5m])) ',
  };
  assertWire(buildPrometheusRequest(form), {
    name: 'Error rate',
    type: 'prometheus',
    config: {
      url: 'https://prom.example.com',
      query: 'sum(rate(errors[5m]))',
      operator: 'lt',
      threshold: 5,
      no_data_status: 'failure',
      auth_type: 'none',
      username: '',
      password: '',
      bearer_token: '',
    },
    interval_seconds: 60,
    timeout_seconds: 10,
    enabled: true,
    ...DEFAULT_ALERTING,
    // Always sent, even blank.
    tags: [],
    depends_on_ids: [],
    location_ids: [],
    location_quorum: 1,
  });
});

test('prometheus: create from a filled form with basic auth', () => {
  const form = {
    ...initialPrometheusFormState(),
    name: 'Queue depth',
    url: 'https://prom.example.com/prefix',
    query: 'max(queue_depth)',
    operator: 'gte' as const,
    threshold: 0,
    no_data_status: 'error' as const,
    auth_type: 'basic' as const,
    username: 'prom',
    password: 'pw',
    bearer_token: 'left-over',
    tags: 'queues',
  };
  assertWire(buildPrometheusRequest(form).config, {
    url: 'https://prom.example.com/prefix',
    query: 'max(queue_depth)',
    operator: 'gte',
    threshold: 0,
    no_data_status: 'error',
    auth_type: 'basic',
    username: 'prom',
    password: 'pw',
    bearer_token: '',
  });
});

test('prometheus: edit round trip submits the masked bearer token back as "***"', () => {
  const monitor = storedMonitor('prometheus', {
    url: 'https://prom.example.com',
    query: 'up',
    operator: 'gt',
    threshold: 0,
    no_data_status: 'error',
    auth_type: 'bearer',
    bearer_token: '***',
  });
  assertWire(buildPrometheusRequest(initialPrometheusFormState(monitor)), {
    name: 'Stored monitor',
    type: 'prometheus',
    config: {
      url: 'https://prom.example.com',
      query: 'up',
      operator: 'gt',
      threshold: 0,
      no_data_status: 'error',
      auth_type: 'bearer',
      username: '',
      password: '',
      bearer_token: '***',
    },
    interval_seconds: 120,
    timeout_seconds: 15,
    enabled: false,
    ...STORED_ALERTING,
    tags: ['prod', 'edge'],
    depends_on_ids: ['dep-1'],
    location_ids: ['loc-a', 'loc-b', 'loc-c'],
    location_quorum: 2,
  });
});

test('prometheus: switching auth type clears the abandoned credential with ""', () => {
  const basic = storedMonitor('prometheus', {
    url: 'https://prom.example.com',
    query: 'up',
    operator: 'gt',
    threshold: 0,
    auth_type: 'basic',
    username: 'prom',
    password: '***',
  });
  // An untouched save keeps the username and the masked password.
  assertWire(buildPrometheusRequest(initialPrometheusFormState(basic)).config, {
    url: 'https://prom.example.com',
    query: 'up',
    operator: 'gt',
    threshold: 0,
    no_data_status: 'failure',
    auth_type: 'basic',
    username: 'prom',
    password: '***',
    bearer_token: '',
  });
  const none = buildPrometheusRequest({ ...initialPrometheusFormState(basic), auth_type: 'none' })
    .config as PrometheusMonitorConfig;
  assertWire([none.username, none.password, none.bearer_token], ['', '', '']);
  const bearer = buildPrometheusRequest({ ...initialPrometheusFormState(basic), auth_type: 'bearer', bearer_token: 'tok' })
    .config as PrometheusMonitorConfig;
  assertWire([bearer.username, bearer.password, bearer.bearer_token], ['', '', 'tok']);
});

test('prometheus: a clone also copies the source alerting settings', () => {
  const initialData: CreateMonitorRequest = {
    name: 'Copy',
    type: 'prometheus',
    config: { url: 'https://prom.example.com', query: 'up', operator: 'lt', threshold: 1 },
    interval_seconds: 60,
    timeout_seconds: 10,
    consecutive_failures_threshold: 5,
    notification_mode: 'custom',
    dependency_suppression: 'off',
    notification_channels: [{ channel_id: 'ch-1', delay_seconds: 0 }],
  };
  const request = buildPrometheusRequest(initialPrometheusFormState(undefined, initialData));
  assertWire(
    [request.consecutive_failures_threshold, request.notification_mode, request.dependency_suppression, request.notification_channels],
    [5, 'custom', 'off', [{ channel_id: 'ch-1', delay_seconds: 0 }]]
  );
});

test('prometheus: a clone whose initialData still carries masked credentials never submits "***"', () => {
  const initialData: CreateMonitorRequest = {
    name: 'Copy',
    type: 'prometheus',
    config: { url: 'https://prom.example.com', query: 'up', operator: 'lt', threshold: 1, auth_type: 'bearer', bearer_token: '***', password: '***' },
    interval_seconds: 60,
    timeout_seconds: 10,
  };
  const form = initialPrometheusFormState(undefined, initialData);
  assertWire([form.password, form.bearer_token], ['', '']);
  assert.ok(!JSON.stringify(buildPrometheusRequest(form)).includes('***'));
});
