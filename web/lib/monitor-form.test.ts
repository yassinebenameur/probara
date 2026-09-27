import assert from 'node:assert/strict';
import test from 'node:test';
import {
  type InlineMonitorFormState,
  type InlineMonitorType,
  describeCheckPlan,
  initialInlineMonitorDrafts,
  isInlineMonitorType,
  prepareInlineMonitorSubmission,
} from './monitor-form';
import { buildHttpConfig, initialHttpFormData, parseStatusRules, validateHttpForm } from './monitor-form/http';
import { buildDNSConfig, validateDNSForm } from './monitor-form/dns';
import { buildPingConfig, validatePingForm } from './monitor-form/ping';
import { buildMonitorRequest, initialSharedFields, validateSharedFields } from './monitor-form/shared';
import {
  type SyntheticAPIFormData,
  buildSyntheticAPIConfig,
  defaultSyntheticAPIStep,
  hasAdvancedSyntheticAPIConfig,
  initialSyntheticAPIFormData,
  syntheticAPITemplate,
  validateSyntheticAPIForm,
} from './monitor-form/synthetic-api';
import {
  type SyntheticBrowserFormData,
  applyExpertTemplate,
  applyGuidedTemplate,
  buildSyntheticBrowserConfig,
  defaultSyntheticBrowserStep,
  hasAdvancedSyntheticBrowserConfig,
  initialSyntheticBrowserFormData,
  syncGuidedIntoExpert,
  validateSyntheticBrowserForm,
} from './monitor-form/synthetic-browser';
import type { CreateMonitorRequest, Monitor, MonitorConfig } from './types';

// Expected payloads below are the exact request bodies the pre-refactor
// MonitorForm submitted for the same inputs (captured by driving the rendered
// form in a browser). They are compared as serialized JSON, key order included,
// so the wire format is pinned, not just the values.

const wire = (value: unknown) => JSON.stringify(value);
const assertWire = (actual: unknown, expected: unknown) => assert.equal(wire(actual), wire(expected));

const baseMonitor = {
  id: 'mon-1', tenant_id: 't', interval_seconds: 120, timeout_seconds: 20, enabled: false,
  created_at: '2026-01-01T00:00:00Z', updated_at: '2026-01-01T00:00:00Z',
  consecutive_failures_threshold: 4, notification_mode: 'custom' as const,
  dependency_suppression: 'on' as const,
  notification_channels: [{ channel_id: 'ch-1', events: ['down'] }] as unknown as Monitor['notification_channels'],
  tags: ['prod', 'api'], depends_on_ids: ['dep-1'], location_ids: ['loc-a', 'loc-b', 'loc-c'], location_quorum: 2,
};

const savedMonitor = (type: Monitor['type'], config: unknown, overrides: Partial<Monitor> = {}): Monitor =>
  ({ ...baseMonitor, name: type, type, config: config as MonitorConfig, ...overrides }) as Monitor;

const savedSharedWire = {
  interval_seconds: 120, timeout_seconds: 20, enabled: false, consecutive_failures_threshold: 4,
  notification_mode: 'custom', dependency_suppression: 'on',
  notification_channels: [{ channel_id: 'ch-1', events: ['down'] }],
};
const newSharedWire = {
  interval_seconds: 60, timeout_seconds: 30, enabled: true, consecutive_failures_threshold: 2,
  notification_mode: 'default', dependency_suppression: 'inherit', notification_channels: [],
};
const noPlacement = { depends_on_ids: [], location_ids: [], location_quorum: 1 };

/** Form state as MonitorForm seeds it for a saved monitor or clone data. */
function stateFor(monitor?: Monitor, initialData?: CreateMonitorRequest): InlineMonitorFormState {
  const type = monitor?.type || initialData?.type || 'http';
  assert.ok(isInlineMonitorType(type));
  return {
    type,
    shared: initialSharedFields(monitor, initialData),
    ...initialInlineMonitorDrafts(monitor, initialData),
  };
}

const blankState = (type: InlineMonitorType, name = 'N'): InlineMonitorFormState => {
  const state = stateFor();
  return { ...state, type, shared: { ...state.shared, name } };
};

function submit(state: InlineMonitorFormState) {
  const result = prepareInlineMonitorSubmission(state);
  if (!result.ok) assert.fail(`unexpected errors: ${JSON.stringify(result.errors)}`);
  return result.request;
}

function errorsOf(state: InlineMonitorFormState) {
  const result = prepareInlineMonitorSubmission(state);
  assert.equal(result.ok, false, 'expected validation to fail');
  return result.ok ? {} : result.errors;
}

// ---------------------------------------------------------------- shared fields

test('shared fields: new monitor defaults', () => {
  assert.deepEqual(initialSharedFields(), {
    name: '', interval_seconds: 60, timeout_seconds: 30, consecutive_failures_threshold: 2,
    notification_mode: 'default', dependency_suppression: 'inherit', notification_channels: [],
    enabled: true, tags: '', depends_on_ids: [], location_ids: [], location_quorum: 1,
  });
});

test('shared fields: clone data seeds schedule, tags and placement but not alerting', () => {
  const fields = initialSharedFields(undefined, {
    name: 'X (Copy)', type: 'http', config: { url: '', method: 'GET' }, interval_seconds: 90, timeout_seconds: 15,
    enabled: false, tags: ['a', 'b'], depends_on_ids: ['d'], location_ids: ['l1', 'l2'], location_quorum: 2,
    consecutive_failures_threshold: 9, notification_mode: 'custom',
  });
  assert.equal(fields.name, 'X (Copy)');
  assert.equal(fields.interval_seconds, 90);
  assert.equal(fields.timeout_seconds, 15);
  assert.equal(fields.enabled, false);
  assert.equal(fields.tags, 'a, b');
  assert.deepEqual(fields.depends_on_ids, ['d']);
  assert.deepEqual(fields.location_ids, ['l1', 'l2']);
  assert.equal(fields.location_quorum, 2);
  assert.equal(fields.consecutive_failures_threshold, 2);
  assert.equal(fields.notification_mode, 'default');
});

test('shared fields: validation messages', () => {
  const fields = { ...initialSharedFields(), name: '   ', timeout_seconds: 60 };
  assert.deepEqual(validateSharedFields(fields), {
    name: 'Name is required',
    timeout_seconds: 'Timeout must be less than interval',
  });
  assert.deepEqual(validateSharedFields({ ...fields, name: 'x', timeout_seconds: 59 }), {});
});

test('shared request: omits blank tags, drops channels outside custom mode, clamps quorum', () => {
  const fields = {
    ...initialSharedFields(),
    name: '  Name  ', tags: ' a, ,b ', notification_channels: [{ channel_id: 'c' }] as unknown as Monitor['notification_channels'] & object,
    location_ids: ['l1', 'l2', 'l3'], location_quorum: 5,
  };
  const request = buildMonitorRequest('ping', { host: 'h' }, fields);
  assert.equal(request.name, 'Name');
  assert.deepEqual(request.tags, ['a', 'b']);
  assert.deepEqual(request.notification_channels, []);
  assert.equal(request.location_quorum, 3);

  const custom = buildMonitorRequest('ping', { host: 'h' }, { ...fields, notification_mode: 'custom', tags: '  ' });
  assert.deepEqual(custom.notification_channels, [{ channel_id: 'c' }]);
  assert.equal('tags' in custom, false);

  assert.equal(buildMonitorRequest('ping', { host: 'h' }, { ...fields, location_ids: ['l1'] }).location_quorum, 1);
});

// ------------------------------------------------------------------------ HTTP

test('http: create with defaults', () => {
  const state = blankState('http', '  My API  ');
  state.http = { ...state.http, method: 'POST', url: ' https://a.example.com/h ' };
  state.shared = { ...state.shared, tags: ' a, ,b ', interval_seconds: 30, timeout_seconds: 10 };
  assertWire(submit(state), {
    name: 'My API', type: 'http',
    config: { url: 'https://a.example.com/h', method: 'POST', follow_redirects: true, max_redirects: 10, collect_timing: false },
    interval_seconds: 30, timeout_seconds: 10, enabled: true, consecutive_failures_threshold: 2,
    notification_mode: 'default', dependency_suppression: 'inherit', notification_channels: [],
    tags: ['a', 'b'], ...noPlacement,
  });
});

test('http: legacy config (expected_status, body substring/regex) is re-saved in the current shape', () => {
  const monitor = savedMonitor('http', {
    url: 'https://legacy.example.com/x', method: 'POST', expected_status: 201,
    expected_body_substring: 'ok', expected_body_regex: '^a+$',
    headers: { Authorization: 'Bearer abc', 'X-Trace': '1' }, body: '  {"a":1}  ',
  }, { name: 'Legacy', tags: [], location_ids: ['loc-a'], location_quorum: 1 });
  const state = stateFor(monitor);
  assert.equal(state.http.status_rules, '201');
  assertWire(submit(state), {
    name: 'Legacy', type: 'http',
    config: {
      url: 'https://legacy.example.com/x', method: 'POST', headers: { Authorization: 'Bearer abc', 'X-Trace': '1' },
      expected_statuses: [201], body: '{"a":1}',
      body_assertions: [{ op: 'contains', value: 'ok' }, { op: 'regex', value: '^a+$' }],
      follow_redirects: true, max_redirects: 10, collect_timing: false,
    },
    ...savedSharedWire, depends_on_ids: ['dep-1'], location_ids: ['loc-a'], location_quorum: 1,
  });
});

test('http: full config round-trips, trimming and dropping empty assertions', () => {
  const monitor = savedMonitor('http', {
    url: 'https://api.example.com/health', method: 'GET', expected_statuses: [200, 204],
    expected_status_ranges: [{ min: 300, max: 302 }], expected_status_classes: ['2xx'],
    body_assertions: [{ op: 'contains', value: ' hello ', case_insensitive: true }, { op: 'regex', value: '' }],
    response_header_assertions: [{ name: ' Content-Type ', op: 'contains', value: 'json' }, { name: 'X-Empty', op: 'exists', value: '  ' }, { name: '', op: 'exists' }],
    json_assertions: [{ path: ' data.ok ', op: 'equals', value: 'true', case_insensitive: true }, { path: 'x', op: 'exists' }],
    max_latency_ms: 750, follow_redirects: false, max_redirects: 3, tls_skip_verify: true, tls_min_days_valid: 14,
    tls_server_name: ' sni.example.com ', tls_ca_pem: ' PEM ', collect_timing: true,
  }, { name: '  Full HTTP  ' });
  const state = stateFor(monitor);
  assert.equal(state.http.status_rules, '200,204, 300-302, 2xx');
  assertWire(submit(state), {
    name: 'Full HTTP', type: 'http',
    config: {
      url: 'https://api.example.com/health', method: 'GET', expected_statuses: [200, 204],
      expected_status_ranges: [{ min: 300, max: 302 }], expected_status_classes: ['2xx'],
      body_assertions: [{ op: 'contains', value: 'hello', case_insensitive: true }],
      response_header_assertions: [{ name: 'Content-Type', op: 'contains', value: 'json' }, { name: 'X-Empty', op: 'exists' }],
      json_assertions: [{ path: 'data.ok', op: 'equals', value: 'true', case_insensitive: true }, { path: 'x', op: 'exists' }],
      max_latency_ms: 750, follow_redirects: false, max_redirects: 3, collect_timing: true, tls_skip_verify: true,
      tls_min_days_valid: 14, tls_server_name: 'sni.example.com', tls_ca_pem: 'PEM',
    },
    ...savedSharedWire, tags: ['prod', 'api'], depends_on_ids: ['dep-1'], location_ids: ['loc-a', 'loc-b', 'loc-c'], location_quorum: 2,
  });
});

test('http: clone data seeds the draft like a saved config', () => {
  const state = stateFor(undefined, {
    name: 'Clone (Copy)', type: 'http', interval_seconds: 90, timeout_seconds: 15, enabled: true, depends_on_ids: ['dep-9'],
    config: { url: 'http://example.com', method: 'HEAD', expected_status: 204 },
  });
  assertWire(submit(state), {
    name: 'Clone (Copy)', type: 'http',
    config: { url: 'http://example.com', method: 'HEAD', expected_statuses: [204], follow_redirects: true, max_redirects: 10, collect_timing: false },
    interval_seconds: 90, timeout_seconds: 15, enabled: true, consecutive_failures_threshold: 2,
    notification_mode: 'default', dependency_suppression: 'inherit', notification_channels: [],
    depends_on_ids: ['dep-9'], location_ids: [], location_quorum: 1,
  });
});

test('http: validation messages', () => {
  const empty = blankState('http', '');
  assert.deepEqual(errorsOf(empty), { name: 'Name is required', url: 'URL is required' });

  const base = initialHttpFormData();
  assert.deepEqual(validateHttpForm({ ...base, url: 'ftp://x' }), { url: 'URL must start with http:// or https://' });

  const tlsOnPlainHTTP = initialHttpFormData({
    url: 'http://api.example.com/health', method: 'GET', tls_skip_verify: true, tls_min_days_valid: 14,
  });
  assert.deepEqual(validateHttpForm(tlsOnPlainHTTP), {
    url: 'TLS settings require an https:// URL',
    tls_min_days_valid: 'TLS checks require an https:// URL',
  });

  const https = { ...base, url: 'https://ok.example.com' };
  assert.deepEqual(validateHttpForm({ ...https, max_latency_ms: '0' }), { max_latency_ms: 'Max latency must be a positive number (ms)' });
  assert.deepEqual(validateHttpForm({ ...https, max_latency_ms: 'abc' }), { max_latency_ms: 'Max latency must be a positive number (ms)' });
  assert.deepEqual(validateHttpForm({ ...https, max_redirects: -1 }), { max_redirects: 'Max redirects must be 0 or greater' });
  assert.deepEqual(validateHttpForm({ ...https, tls_min_days_valid: '-2' }), { tls_min_days_valid: 'TLS min days valid must be 0 or greater' });
  assert.deepEqual(validateHttpForm({ ...https, request_headers: [{ key: 'X-A', value: '' }] }), {
    request_headers: 'Headers must have both a name and a value',
  });
  assert.deepEqual(validateHttpForm({ ...https, status_rules: '200, 99x' }), { status_rules: 'Invalid status rule: 99x' });
  assert.deepEqual(validateHttpForm({ ...https, request_headers: [{ key: '  ', value: ' ' }] }), {});
});

test('http: status rule parsing', () => {
  assert.deepEqual(parseStatusRules(' 200, 3XX , 400-404 '), { codes: [200], ranges: [{ min: 400, max: 404 }], classes: ['3xx'] });
  assert.equal(parseStatusRules('700').error, 'Invalid status code: 700');
  assert.equal(parseStatusRules('404-400').error, 'Invalid status range (min > max): 404-400');
  assert.equal(parseStatusRules('100-700').error, 'Invalid status range: 100-700');
  assert.deepEqual(parseStatusRules(''), { codes: [], ranges: [], classes: [] });
});

test('http: header values are sent untrimmed, names trimmed, blank rows skipped', () => {
  const config = buildHttpConfig({
    ...initialHttpFormData(),
    url: 'https://x.example.com',
    request_headers: [{ key: ' X-A ', value: ' v ' }, { key: '', value: '' }],
  });
  assert.deepEqual(config.headers, { 'X-A': ' v ' });
});

// ------------------------------------------------------------------ ping & DNS

test('ping: create, edit and validation', () => {
  const state = blankState('ping', 'P');
  assert.deepEqual(errorsOf({ ...state, shared: { ...state.shared, name: '' } }), { name: 'Name is required', host: 'Host is required' });
  state.host = { ...state.host, host: ' 1.1.1.1 ' };
  assertWire(submit(state), { name: 'P', type: 'ping', config: { host: '1.1.1.1' }, ...newSharedWire, ...noPlacement });

  const edit = stateFor(savedMonitor('ping', { host: ' 8.8.8.8 ' }, { name: 'Ping', tags: undefined, location_ids: [] }));
  assertWire(submit(edit), {
    name: 'Ping', type: 'ping', config: { host: '8.8.8.8' }, ...savedSharedWire,
    depends_on_ids: ['dep-1'], location_ids: [], location_quorum: 1,
  });
  assert.deepEqual(validatePingForm({ host: '  ' }), { host: 'Host is required' });
  assert.deepEqual(buildPingConfig({ host: ' h ' }), { host: 'h' });
});

test('dns: create omits blank optional fields and splits answers on commas and newlines', () => {
  const state = blankState('dns', 'D');
  state.host = { host: ' example.org ', record_type: 'AAAA', expected_answers: ' ::1 ,, 2001:db8::1 ', nameserver: ' 9.9.9.9 ' };
  assertWire(submit(state), {
    name: 'D', type: 'dns',
    config: { host: 'example.org', record_type: 'AAAA', expected_answers: ['::1', '2001:db8::1'], nameserver: '9.9.9.9' },
    ...newSharedWire, ...noPlacement,
  });
  assertWire(buildDNSConfig({ host: 'h', record_type: '', expected_answers: ' \n ', nameserver: ' ' }), { host: 'h', record_type: 'A' });
  assert.deepEqual(buildDNSConfig({ host: 'h', record_type: 'TXT', expected_answers: 'a\nb, c', nameserver: '' }).expected_answers, ['a', 'b', 'c']);
  assert.deepEqual(validateDNSForm({ host: '', record_type: 'A', expected_answers: '', nameserver: '' }), { host: 'Host is required' });
});

test('dns: edit round-trips the saved config', () => {
  const state = stateFor(savedMonitor('dns', {
    host: 'example.com', record_type: 'MX', expected_answers: ['mx1.example.com', 'mx2.example.com'], nameserver: '10.0.0.2',
  }, { name: 'DNS' }));
  assert.equal(state.host.expected_answers, 'mx1.example.com, mx2.example.com');
  assertWire(submit(state), {
    name: 'DNS', type: 'dns',
    config: { host: 'example.com', record_type: 'MX', expected_answers: ['mx1.example.com', 'mx2.example.com'], nameserver: '10.0.0.2' },
    ...savedSharedWire, tags: ['prod', 'api'], depends_on_ids: ['dep-1'], location_ids: ['loc-a', 'loc-b', 'loc-c'], location_quorum: 2,
  });
});

test('ping and dns share the host draft when switching types', () => {
  const state = blankState('ping', 'T');
  state.host = { ...state.host, host: 'h.example.com' };
  assertWire(submit({ ...state, type: 'dns' }).config, { host: 'h.example.com', record_type: 'A' });
});

// --------------------------------------------------------------- synthetic API

const editApiConfig = {
  base_url: 'https://api.example.com', failure_mode: 'continue', variables: { email: 'a@b.c', n: '1' },
  steps: [
    {
      id: 'login', name: 'Login',
      request: { method: 'post', url: '/login', headers: { 'Content-Type': 'application/json' }, body: '{"e":"{{email}}"}', timeout_seconds: 5, follow_redirects: false, max_redirects: 2 },
      assert: [
        { target: 'status', op: 'in', value: [200, 201] }, { target: 'json', op: 'number_gt', path: 'count', value: 3 },
        { target: 'header', op: 'exists', path: 'X-Id' }, { target: 'body', op: 'regex', value: '^ok' },
        { target: 'json', op: 'bool_is', path: 'ok', value: true }, { target: 'json', op: 'equals', path: 'o', value: { a: 1 } },
      ],
      extract: [{ name: 'token', from: 'json', path: 'token', sensitive: true }, { name: 'rid', from: 'header', path: 'X-Request-Id' }],
    },
    { id: 'me', request: { method: 'GET', url: '/me' } },
  ],
};

test('synthetic api: editing a saved journey round-trips it', () => {
  const state = stateFor(savedMonitor('synthetic_api', editApiConfig, { name: 'API' }));
  assertWire(submit(state), {
    name: 'API', type: 'synthetic_api',
    config: {
      failure_mode: 'continue',
      steps: [
        {
          id: 'login',
          request: { method: 'POST', url: '/login', headers: { 'Content-Type': 'application/json' }, body: '{"e":"{{email}}"}', timeout_seconds: 5, follow_redirects: false, max_redirects: 2 },
          name: 'Login',
          assert: [
            { target: 'status', op: 'in', value: [200, 201] }, { target: 'json', op: 'number_gt', path: 'count', value: 3 },
            { target: 'header', op: 'exists', path: 'X-Id' }, { target: 'body', op: 'regex', value: '^ok' },
            { target: 'json', op: 'bool_is', path: 'ok', value: true }, { target: 'json', op: 'equals', path: 'o', value: { a: 1 } },
          ],
          extract: [{ name: 'token', from: 'json', path: 'token', sensitive: true }, { name: 'rid', from: 'header', path: 'X-Request-Id' }],
        },
        { id: 'me', request: { method: 'GET', url: '/me', follow_redirects: true } },
      ],
      base_url: 'https://api.example.com',
      variables: { email: 'a@b.c', n: '1' },
    },
    ...savedSharedWire, tags: ['prod', 'api'], depends_on_ids: ['dep-1'], location_ids: ['loc-a', 'loc-b', 'loc-c'], location_quorum: 2,
  });
});

test('synthetic api: a new monitor starts with a /health step', () => {
  assertWire(submit(blankState('synthetic_api', 'A')), {
    name: 'A', type: 'synthetic_api',
    config: { failure_mode: 'fail_fast', steps: [{ id: 'health', request: { method: 'GET', url: '/health', follow_redirects: true }, assert: [{ target: 'status', op: 'equals', value: 200 }] }] },
    ...newSharedWire, ...noPlacement,
  });
});

test('synthetic api: quick-start templates', () => {
  const health = { id: 'health', request: { method: 'GET', url: '/health', follow_redirects: true }, name: 'Health endpoint', assert: [{ target: 'status', op: 'equals', value: 200 }] };
  assertWire(buildSyntheticAPIConfig(syntheticAPITemplate('single_health')), {
    failure_mode: 'fail_fast', steps: [health], base_url: 'https://api.example.com',
  });
  assertWire(buildSyntheticAPIConfig(syntheticAPITemplate('health_auth')), {
    failure_mode: 'fail_fast',
    steps: [
      health,
      {
        id: 'login',
        request: { method: 'POST', url: '/auth/login', headers: { 'Content-Type': 'application/json' }, body: '{"email":"{{email}}","password":"{{password}}"}', follow_redirects: true },
        name: 'Login',
        assert: [{ target: 'status', op: 'in', value: [200, 201] }, { target: 'json', op: 'exists', path: 'token' }],
        extract: [{ name: 'token', from: 'json', path: 'token', sensitive: true }],
      },
      {
        id: 'profile',
        request: { method: 'GET', url: '/me', headers: { Authorization: 'Bearer {{token}}' }, follow_redirects: true },
        name: 'Fetch profile',
        assert: [{ target: 'status', op: 'equals', value: 200 }],
      },
    ],
    base_url: 'https://api.example.com',
    variables: { email: 'monitor@example.com', password: 'change-me' },
  });
});

test('synthetic api: advanced mode is detected from saved config', () => {
  assert.equal(hasAdvancedSyntheticAPIConfig(undefined), false);
  assert.equal(hasAdvancedSyntheticAPIConfig({ steps: [{ id: 'a', request: { method: 'GET', url: '/' } }] }), false);
  assert.equal(hasAdvancedSyntheticAPIConfig(editApiConfig as never), true);
  assert.equal(hasAdvancedSyntheticAPIConfig({ variables: { a: 'b' }, steps: [] }), true);
  assert.equal(hasAdvancedSyntheticAPIConfig({ steps: [{ id: 'a', request: { method: 'GET', url: '/', max_redirects: 0 } }] }), false);
});

test('synthetic api: unknown saved operators fall back to the target default', () => {
  const data = initialSyntheticAPIFormData({
    steps: [{ id: 'a', request: { method: 'GET', url: '/' }, assert: [{ target: 'weird' as never, op: 'nope', value: 1 }] }],
  });
  assert.deepEqual(data.steps[0].assertions, [{ target: 'status', op: 'equals', path: '', value_input: '1' }]);
});

function apiWith(patch: Partial<SyntheticAPIFormData['steps'][number]>, extra: Partial<SyntheticAPIFormData> = {}): SyntheticAPIFormData {
  return {
    base_url: '', failure_mode: 'fail_fast', variables: [{ key: '', value: '' }],
    steps: [{ ...defaultSyntheticAPIStep(1), id: 'a', url: '/a', ...patch }],
    ...extra,
  };
}

test('synthetic api: validation messages', () => {
  const stepError = (data: SyntheticAPIFormData) => validateSyntheticAPIForm(data).synthetic_api_steps;
  const assertion = (target: string, op: string, value_input = '', path = 'p') =>
    apiWith({ assertions: [{ target: target as never, op, path, value_input }] });

  assert.deepEqual(validateSyntheticAPIForm(apiWith({}, { base_url: 'api.example.com', variables: [{ key: ' ', value: 'v' }] })), {
    synthetic_api_base_url: 'Base URL must start with http:// or https://',
    synthetic_api_variables: 'Each variable requires a key',
  });
  assert.equal(stepError(apiWith({}, { steps: [] })), 'At least one API step is required');
  assert.equal(stepError(apiWith({ id: ' ' })), 'Each API step requires an ID');
  assert.equal(stepError(apiWith({}, { steps: [{ ...defaultSyntheticAPIStep(1), url: '/x' }, { ...defaultSyntheticAPIStep(2), id: 'step_1', url: '/y' }] })), 'API step IDs must be unique');
  assert.equal(stepError(apiWith({ url: ' ' })), "Step 'a' requires a URL");
  assert.equal(stepError(apiWith({ method: '' })), "Step 'a' requires an HTTP method");
  assert.equal(stepError(apiWith({ timeout_seconds: '0' })), "Step 'a' has invalid timeout");
  assert.equal(stepError(apiWith({ max_redirects: '-1' })), "Step 'a' has invalid max redirects");
  assert.equal(stepError(apiWith({ headers: [{ key: '', value: 'v' }] })), "Step 'a' has invalid headers");
  assert.equal(stepError(assertion('status', 'exists')), "Step 'a' assertion #1 has invalid operator");
  assert.equal(stepError(assertion('json', 'exists', '', ' ')), "Step 'a' assertion #1 requires a path");
  assert.equal(stepError(assertion('body', 'contains', '  ')), "Step 'a' assertion #1 requires a value");
  assert.equal(stepError(assertion('body', 'contains', '""')), "Step 'a' assertion #1 requires a value");
  assert.equal(stepError(assertion('status', 'equals', 'abc')), "Step 'a' assertion #1 expects an integer status code");
  assert.equal(stepError(assertion('status', 'in', '[]')), "Step 'a' assertion #1 expects a non-empty array of status codes");
  assert.equal(stepError(assertion('status', 'in', '[200, "x"]')), "Step 'a' assertion #1 expects a non-empty array of status codes");
  assert.equal(stepError(assertion('json', 'number_lt', 'abc')), "Step 'a' assertion #1 expects a numeric value");
  assert.equal(stepError(assertion('json', 'bool_is', 'yes')), "Step 'a' assertion #1 expects true or false");
  assert.equal(stepError(assertion('body', 'regex', '(')), "Step 'a' assertion #1 has invalid regex");
  assert.equal(stepError(apiWith({ extracts: [{ name: '', from: 'json', path: 'p', sensitive: false }] })), "Step 'a' extract #1 requires a name");
  assert.equal(stepError(apiWith({ extracts: [{ name: 't', from: 'json', path: ' ', sensitive: false }] })), "Step 'a' extract #1 requires a path");
  assert.equal(stepError(apiWith({ extracts: [{ name: 't', from: 'xml' as never, path: 'p', sensitive: false }] })), "Step 'a' extract #1 has invalid source");
  assert.equal(
    stepError(apiWith({}, { steps: [
      { ...defaultSyntheticAPIStep(1), url: '/x', extracts: [{ name: 't', from: 'json', path: 'p', sensitive: false }] },
      { ...defaultSyntheticAPIStep(2), url: '/y', extracts: [{ name: 't', from: 'header', path: 'H', sensitive: false }] },
    ] })),
    "Step 'step_2' extract #1 name must be globally unique"
  );

  // Valid edge cases: blank extract rows are ignored, `exists` needs no value, status `in` accepts numeric strings.
  assert.equal(stepError(apiWith({ extracts: [{ name: ' ', from: 'json', path: '', sensitive: false }] })), undefined);
  assert.equal(stepError(assertion('header', 'exists')), undefined);
  assert.equal(stepError(assertion('status', 'in', '["200", 201]')), undefined);
  assert.equal(stepError(apiWith({ assertions: [] })), undefined);
});

test('synthetic api: build trims ids and names, upper-cases methods and skips partial extracts', () => {
  const config = buildSyntheticAPIConfig(apiWith({
    id: ' a ', name: '  ', method: 'delete', body: '  ', timeout_seconds: '7', max_redirects: '0',
    assertions: [{ target: 'body', op: ' Contains ', path: '  ', value_input: ' "ok" ' }, { target: 'status', op: ' ', path: '', value_input: '' }],
    extracts: [{ name: 'x', from: 'json', path: '', sensitive: true }],
  }, { variables: [{ key: ' k ', value: ' v ' }, { key: ' ', value: '' }] }));
  assertWire(config, {
    failure_mode: 'fail_fast',
    steps: [{ id: 'a', request: { method: 'DELETE', url: '/a', timeout_seconds: 7, follow_redirects: true, max_redirects: 0 }, assert: [{ target: 'body', op: 'contains', value: 'ok' }] }],
    variables: { k: ' v ' },
  });
});

// ----------------------------------------------------------- synthetic browser

const editBrowserConfig = {
  start_url: 'https://app.example.com/login', device: 'Pixel 7', failure_mode: 'continue', variables: { email: 'u@e.com' },
  artifacts: { screenshot_on_failure: false, trace_on_failure: true, har_on_failure: true },
  steps: [
    { id: 'open', action: 'goto', url: ' https://app.example.com/login ' },
    { id: 'fill', action: 'fill', selector: '#email', value: ' {{email}} ', timeout_seconds: 7 },
    { id: 'text', action: 'assert_text', selector: 'h1', value: 'Welcome' },
  ],
};

const guidedArtifacts = { screenshot_on_failure: true, trace_on_failure: false, har_on_failure: false };

test('synthetic browser: edits open in expert mode and round-trip the saved journey', () => {
  const state = stateFor(savedMonitor('synthetic_browser', editBrowserConfig, { name: 'Browser' }));
  assert.equal(state.syntheticBrowser.setup_mode, 'expert');
  assertWire(submit(state), {
    name: 'Browser', type: 'synthetic_browser',
    config: {
      start_url: 'https://app.example.com/login', device: 'Pixel 7', failure_mode: 'continue',
      steps: [
        { id: 'open', action: 'goto', url: 'https://app.example.com/login' },
        { id: 'fill', action: 'fill', selector: '#email', value: ' {{email}} ', timeout_seconds: 7 },
        { id: 'text', action: 'assert_text', selector: 'h1', value: 'Welcome' },
      ],
      artifacts: { screenshot_on_failure: false, trace_on_failure: true, har_on_failure: true },
      variables: { email: 'u@e.com' },
    },
    ...savedSharedWire, tags: ['prod', 'api'], depends_on_ids: ['dep-1'], location_ids: ['loc-a', 'loc-b', 'loc-c'], location_quorum: 2,
  });
});

test('synthetic browser: switching an edit to guided rebuilds the journey from the inferred template', () => {
  const state = stateFor(savedMonitor('synthetic_browser', editBrowserConfig, { name: 'Browser' }));
  state.syntheticBrowser = { ...state.syntheticBrowser, setup_mode: 'guided' };
  assertWire(submit(state).config, {
    start_url: 'https://app.example.com/login', device: 'Desktop Chrome', failure_mode: 'fail_fast',
    steps: [
      { id: 'open_homepage', action: 'goto', url: 'https://app.example.com/login' },
      { id: 'assert_page_ready', action: 'assert_visible', selector: 'main' },
    ],
    artifacts: guidedArtifacts,
    variables: { username: 'u@e.com' },
  });
});

test('synthetic browser: clones start guided with the template and credentials inferred', () => {
  const state = stateFor(undefined, {
    name: 'Browser (Copy)', type: 'synthetic_browser', interval_seconds: 300, timeout_seconds: 60, enabled: true, tags: ['x'],
    config: {
      start_url: 'https://app.example.com/login', variables: { username: 'bob', password: 'pw' },
      steps: [{ id: 'open_login', action: 'goto', url: 'https://app.example.com/login' }, { id: 'go', action: 'click', selector: '#go' }],
    },
  });
  assert.equal(state.syntheticBrowser.setup_mode, 'guided');
  assert.equal(state.syntheticBrowser.guided.template, 'login_flow');
  assertWire(submit(state), {
    name: 'Browser (Copy)', type: 'synthetic_browser',
    config: {
      start_url: 'https://app.example.com/login', device: 'Desktop Chrome', failure_mode: 'fail_fast',
      steps: [
        { id: 'open_login', action: 'goto', url: 'https://app.example.com/login' },
        { id: 'fill_username', action: 'fill', selector: '[name=email]', value: '{{username}}' },
        { id: 'fill_password', action: 'fill', selector: '[name=password]', value: '{{password}}' },
        { id: 'submit_login', action: 'click', selector: 'button[type=submit]' },
        { id: 'wait_dashboard', action: 'wait_for', selector: '[data-test=dashboard]' },
        { id: 'assert_url', action: 'assert_url', value: '/dashboard' },
      ],
      artifacts: guidedArtifacts,
      variables: { username: 'bob', password: 'pw' },
    },
    interval_seconds: 300, timeout_seconds: 60, enabled: true, consecutive_failures_threshold: 2,
    notification_mode: 'default', dependency_suppression: 'inherit', notification_channels: [],
    tags: ['x'], ...noPlacement,
  });
});

const newBrowser = (): SyntheticBrowserFormData => initialSyntheticBrowserFormData(undefined, false);

test('synthetic browser: guided homepage smoke falls back to `main` when the selector is blank', () => {
  let data = applyGuidedTemplate(newBrowser(), 'homepage_smoke');
  data = { ...data, start_url: ' https://site.example.com ', guided: { ...data.guided, must_see_selector: '  ' } };
  assertWire(buildSyntheticBrowserConfig(data), {
    start_url: 'https://site.example.com', device: 'Desktop Chrome', failure_mode: 'fail_fast',
    steps: [
      { id: 'open_homepage', action: 'goto', url: 'https://site.example.com' },
      { id: 'assert_page_ready', action: 'assert_visible', selector: 'main' },
    ],
    artifacts: guidedArtifacts,
  });
});

test('synthetic browser: guided login trims credentials into variables and skips absent fill steps', () => {
  const data = applyGuidedTemplate(newBrowser(), 'login_flow');
  assert.equal(data.start_url, 'https://app.example.com/login');
  const withUser = { ...data, guided: { ...data.guided, username: ' bob ', password: ' hunter2 ' } };
  assertWire(buildSyntheticBrowserConfig(withUser).variables, { username: 'bob', password: 'hunter2' });

  const userOnly = { ...data, guided: { ...data.guided, username: 'bob' } };
  assert.deepEqual(buildSyntheticBrowserConfig(userOnly).steps.map((s) => s.id), [
    'open_login', 'fill_username', 'submit_login', 'wait_dashboard', 'assert_url',
  ]);
});

test('synthetic browser: picking a guided template keeps a typed start URL and credentials', () => {
  const typed = { ...newBrowser(), start_url: 'https://mine.example.com' };
  const withCreds = { ...typed, guided: { ...typed.guided, username: 'u', password: 'p', submit_selector: '#custom' } };
  const next = applyGuidedTemplate(withCreds, 'login_flow');
  assert.equal(next.start_url, 'https://mine.example.com');
  assert.equal(next.guided.username, 'u');
  assert.equal(next.guided.password, 'p');
  assert.equal(next.guided.submit_selector, 'button[type=submit]');
  assert.equal(next.guided.template, 'login_flow');
});

test('synthetic browser: guided validation messages', () => {
  const errors = (data: SyntheticBrowserFormData) => validateSyntheticBrowserForm(data);
  assert.deepEqual(errors(newBrowser()), { synthetic_browser_start_url: 'Start URL is required' });

  const login = applyGuidedTemplate(newBrowser(), 'login_flow');
  const withGuided = (patch: Partial<SyntheticBrowserFormData['guided']>, start_url = login.start_url) =>
    ({ ...login, start_url, guided: { ...login.guided, ...patch } });
  assert.deepEqual(errors(withGuided({ submit_selector: '' }, 'app.example.com')), {
    synthetic_browser_start_url: 'Start URL must start with http:// or https://',
    synthetic_browser_steps: 'Submit selector is required',
  });
  assert.equal(errors(withGuided({ post_login_selector: ' ' })).synthetic_browser_steps, 'Post-login selector is required');
  assert.equal(errors(withGuided({ expected_url: '' })).synthetic_browser_steps, 'Expected URL value is required');
  assert.equal(errors(withGuided({ username: 'u', email_selector: '' })).synthetic_browser_steps, 'Email selector is required when username is set');
  assert.equal(errors(withGuided({ password: 'p', password_selector: '' })).synthetic_browser_steps, 'Password selector is required when password is set');
  // Expert-only fields are not validated while guided.
  assert.deepEqual(errors({ ...withGuided({}), steps: [], variables: [{ key: '', value: 'v' }] }), {});
});

test('synthetic browser: switching guided → expert copies the guided journey and run settings', () => {
  const login = applyGuidedTemplate({ ...newBrowser(), device: 'Pixel 7', trace_on_failure: true }, 'login_flow');
  const expert = syncGuidedIntoExpert({ ...login, guided: { ...login.guided, username: 'bob' } });
  assert.equal(expert.device, 'Desktop Chrome');
  assert.equal(expert.trace_on_failure, false);
  assert.deepEqual(expert.variables, [{ key: 'username', value: 'bob' }]);
  assertWire(buildSyntheticBrowserConfig({ ...expert, setup_mode: 'expert' }).steps, [
    { id: 'open_login', action: 'goto', url: 'https://app.example.com/login' },
    { id: 'fill_username', action: 'fill', selector: '[name=email]', value: '{{username}}' },
    { id: 'submit_login', action: 'click', selector: 'button[type=submit]' },
    { id: 'wait_dashboard', action: 'wait_for', selector: '[data-test=dashboard]' },
    { id: 'assert_url', action: 'assert_url', value: '/dashboard' },
  ]);
});

test('synthetic browser: expert templates', () => {
  const expert = { ...newBrowser(), setup_mode: 'expert' as const };
  assertWire(buildSyntheticBrowserConfig(applyExpertTemplate(expert, 'login_flow')), {
    start_url: 'https://app.example.com/login', device: 'Desktop Chrome', failure_mode: 'fail_fast',
    steps: [
      { id: 'open_login', action: 'goto', url: 'https://app.example.com/login' },
      { id: 'fill_email', action: 'fill', selector: '[name=email]', value: '{{email}}' },
      { id: 'fill_password', action: 'fill', selector: '[name=password]', value: '{{password}}' },
      { id: 'submit', action: 'click', selector: 'button[type=submit]' },
      { id: 'wait_dashboard', action: 'wait_for', selector: '[data-test=dashboard]' },
      { id: 'assert_url', action: 'assert_url', value: '/dashboard' },
    ],
    artifacts: guidedArtifacts,
    variables: { email: 'monitor@example.com', password: 'change-me' },
  });
  const smoke = applyExpertTemplate(expert, 'homepage_smoke');
  assert.deepEqual(smoke.steps.map((s) => s.id), ['open', 'hero_visible', 'cta_visible']);
  assert.equal('variables' in buildSyntheticBrowserConfig(smoke), false);
  // A blank device is omitted from the wire body.
  assert.equal(wire(buildSyntheticBrowserConfig({ ...smoke, device: '  ' })).includes('device'), false);
});

test('synthetic browser: expert validation messages', () => {
  const expert = { ...applyExpertTemplate({ ...newBrowser(), setup_mode: 'expert' as const }, 'homepage_smoke') };
  const withStep = (patch: Partial<SyntheticBrowserFormData['steps'][number]>) =>
    ({ ...expert, steps: [...expert.steps, { ...defaultSyntheticBrowserStep(4), ...patch }] });
  const stepError = (data: SyntheticBrowserFormData) => validateSyntheticBrowserForm(data).synthetic_browser_steps;

  assert.deepEqual(validateSyntheticBrowserForm(expert), {});
  assert.equal(stepError({ ...expert, steps: [] }), 'At least one browser step is required');
  assert.equal(stepError(withStep({ id: ' ' })), 'Each browser step requires an ID');
  assert.equal(stepError(withStep({ id: 'open', url: 'https://x' })), 'Browser step IDs must be unique');
  assert.equal(stepError(withStep({ action: 'goto' })), "Step 'step_4' requires a URL");
  for (const action of ['click', 'fill', 'wait_for', 'assert_visible', 'assert_text'] as const) {
    assert.equal(stepError(withStep({ action, value: 'v' })), "Step 'step_4' requires a selector", action);
  }
  for (const action of ['fill', 'assert_text', 'assert_url'] as const) {
    assert.equal(stepError(withStep({ action, selector: 's' })), "Step 'step_4' requires a value", action);
  }
  assert.equal(stepError(withStep({ url: 'https://x', timeout_seconds: '0' })), "Step 'step_4' has invalid timeout");
  assert.deepEqual(validateSyntheticBrowserForm({ ...expert, start_url: '', variables: [{ key: '', value: 'v' }] }), {
    synthetic_browser_start_url: 'Start URL is required',
    synthetic_browser_variables: 'Each variable requires a key',
  });
});

test('synthetic browser: advanced mode is detected from saved config', () => {
  assert.equal(hasAdvancedSyntheticBrowserConfig(undefined), false);
  assert.equal(hasAdvancedSyntheticBrowserConfig({ start_url: 'x', steps: [{ id: 'a', action: 'goto', url: 'x' }] }), false);
  assert.equal(hasAdvancedSyntheticBrowserConfig({ start_url: 'x', device: 'Pixel 7', steps: [] }), true);
  assert.equal(hasAdvancedSyntheticBrowserConfig({ start_url: 'x', steps: [{ id: 'a', action: 'goto', timeout_seconds: 3 }] }), true);
});

// ------------------------------------------------------------------ check plan

test('check plan summarises the target per type', () => {
  const http = blankState('http');
  assert.equal(describeCheckPlan(http), null);
  http.http = { ...http.http, url: 'https://api.example.com/v1/health', method: 'HEAD' };
  assert.equal(describeCheckPlan(http), 'Every 60s · HEAD api.example.com/v1/health · expect 2xx · down after 2 failed checks');

  const dns = blankState('dns');
  dns.host = { ...dns.host, host: 'example.com' };
  dns.shared = { ...dns.shared, consecutive_failures_threshold: 1 };
  assert.equal(describeCheckPlan(dns), 'Every 60s · resolve A for example.com · down after 1 failed check');
  assert.equal(describeCheckPlan({ ...dns, type: 'ping' }), 'Every 60s · ping example.com · down after 1 failed check');

  assert.equal(describeCheckPlan(blankState('synthetic_api')), 'Every 60s · run 1-step API journey · down after 2 failed checks');
  const browser = blankState('synthetic_browser');
  assert.equal(describeCheckPlan(browser), 'Every 60s · run 2-step browser journey · down after 2 failed checks');
  browser.syntheticBrowser = { ...browser.syntheticBrowser, setup_mode: 'expert', steps: [defaultSyntheticBrowserStep(1)] };
  assert.equal(describeCheckPlan(browser), 'Every 60s · run 1-step browser journey · down after 2 failed checks');
});
