import assert from 'node:assert/strict';
import test from 'node:test';
import {
  MonitorFormState,
  buildMonitorFormRequest,
  defaultTimeoutFor,
  hasAdvancedSyntheticAPIConfig,
  hasAdvancedSyntheticBrowserConfig,
  initialMonitorFormState,
  syntheticAPITemplateFields,
  syntheticBrowserGuidedTemplateFields,
  syntheticBrowserGuidedToExpertFields,
  syntheticBrowserTemplateFields,
  validateMonitorForm,
} from './monitor-form/inline';
import { DEFAULT_ALERTING, STORED_ALERTING, assertWire, storedMonitor } from './monitor-form/testing';
import type {
  CreateMonitorRequest,
  HTTPMonitorConfig,
  SyntheticAPIAssertionTarget,
  SyntheticAPIMonitorConfig,
  SyntheticBrowserMonitorConfig,
} from './types';

// MonitorForm's own types: http, ping, dns, synthetic_api, synthetic_browser.
// None of them has a monitor secret (shared/secrets MonitorSecretFields), so
// the edit round trips pin that every stored field survives a save.

/** A fresh create: the form starts on http, then the operator picks `type`. */
function freshCreate(type: CreateMonitorRequest['type'], patch: Partial<MonitorFormState> = {}): MonitorFormState {
  const state = initialMonitorFormState();
  // MonitorForm's selectType keeps an untouched timeout in step with the type.
  return { ...state, timeout_seconds: defaultTimeoutFor(type), ...patch };
}

const NEW_SCHEDULE = { interval_seconds: 60, timeout_seconds: 10, enabled: true } as const;
const NEW_TAIL = { depends_on_ids: [], location_ids: [], location_quorum: 1 } as const;
const STORED_SCHEDULE = { interval_seconds: 120, timeout_seconds: 15, enabled: false } as const;
const STORED_TAIL = {
  tags: ['prod', 'edge'],
  depends_on_ids: ['dep-1'],
  location_ids: ['loc-a', 'loc-b', 'loc-c'],
  location_quorum: 2,
} as const;

// --- http -----------------------------------------------------------------

test('http: create from defaults', () => {
  const form = freshCreate('http', { name: ' API health ', url: ' https://api.example.com/health ' });
  assert.deepEqual(validateMonitorForm('http', form, false), {});
  assertWire(buildMonitorFormRequest('http', form, false), {
    name: 'API health',
    type: 'http',
    config: {
      url: 'https://api.example.com/health',
      method: 'GET',
      follow_redirects: true,
      max_redirects: 10,
      collect_timing: false,
    },
    ...NEW_SCHEDULE,
    ...DEFAULT_ALERTING,
    ...NEW_TAIL,
  });
});

test('http: create from a fully filled form', () => {
  const form = freshCreate('http', {
    name: 'Checkout API',
    url: 'https://api.example.com/v1/ping',
    method: 'POST',
    status_rules: '200, 201-204, 3xx',
    request_headers: [
      { key: 'Authorization', value: 'Bearer abc', reveal: false },
      { key: '', value: '', reveal: false },
      { key: ' X-Trace ', value: ' 1 ', reveal: true },
    ],
    body: '  {"ping":true}  ',
    body_assertions: [
      { op: 'contains', value: ' ok ', case_insensitive: true },
      { op: 'regex', value: '   ' },
    ],
    response_header_assertions: [
      { name: ' Content-Type ', op: 'contains', value: 'json' },
      { name: '  ', op: 'exists' },
    ],
    json_assertions: [
      { path: ' data.ok ', op: 'equals', value: 'true' },
      { path: 'data.count', op: 'exists', value: '  ', case_insensitive: false },
    ],
    display_fields: [
      { label: ' Version ', path: ' build.version ' },
      { label: '', path: 'uptime' },
      { label: '', path: '  ' },
    ],
    max_latency_ms: '750',
    follow_redirects: false,
    max_redirects: 3,
    tls_skip_verify: true,
    tls_min_days_valid: '14',
    tls_server_name: ' api.internal ',
    tls_ca_pem: ' -----BEGIN CERTIFICATE----- ',
    collect_timing: true,
    interval_seconds: 30,
    timeout_seconds: 5,
    consecutive_failures_threshold: 3,
    notification_mode: 'custom',
    dependency_suppression: 'on',
    notification_channels: [{ channel_id: 'ch-9', delay_seconds: 0 }],
    tags: 'api, critical, ',
    depends_on_ids: ['dep-1'],
    location_ids: ['loc-a', 'loc-b'],
    location_quorum: 5,
  });
  assert.deepEqual(validateMonitorForm('http', form, false), {});
  assertWire(buildMonitorFormRequest('http', form, false), {
    name: 'Checkout API',
    type: 'http',
    config: {
      url: 'https://api.example.com/v1/ping',
      method: 'POST',
      headers: { Authorization: 'Bearer abc', 'X-Trace': ' 1 ' },
      expected_statuses: [200],
      expected_status_ranges: [{ min: 201, max: 204 }],
      expected_status_classes: ['3xx'],
      body: '{"ping":true}',
      body_assertions: [{ op: 'contains', value: 'ok', case_insensitive: true }],
      response_header_assertions: [{ name: 'Content-Type', op: 'contains', value: 'json' }],
      json_assertions: [
        { path: 'data.ok', op: 'equals', value: 'true' },
        { path: 'data.count', op: 'exists' },
      ],
      display_fields: [{ label: 'Version', path: 'build.version' }, { path: 'uptime' }],
      max_latency_ms: 750,
      follow_redirects: false,
      max_redirects: 3,
      collect_timing: true,
      tls_skip_verify: true,
      tls_min_days_valid: 14,
      tls_server_name: 'api.internal',
      tls_ca_pem: '-----BEGIN CERTIFICATE-----',
    },
    interval_seconds: 30,
    timeout_seconds: 5,
    enabled: true,
    consecutive_failures_threshold: 3,
    notification_mode: 'custom',
    dependency_suppression: 'on',
    notification_channels: [{ channel_id: 'ch-9', delay_seconds: 0 }],
    tags: ['api', 'critical'],
    depends_on_ids: ['dep-1'],
    location_ids: ['loc-a', 'loc-b'],
    location_quorum: 2,
  });
});

test('http: edit round trip keeps the stored config and shared fields', () => {
  const config: HTTPMonitorConfig = {
    url: 'https://api.example.com/health',
    method: 'POST',
    headers: { Authorization: 'Bearer stored' },
    expected_statuses: [200, 204],
    expected_status_ranges: [{ min: 300, max: 399 }],
    expected_status_classes: ['5xx'],
    body: '{"ping":true}',
    body_assertions: [
      { op: 'contains', value: 'healthy', case_insensitive: true },
      { op: 'not_contains', value: 'error', case_insensitive: false },
    ],
    response_header_assertions: [{ name: 'Server', op: 'exists' }],
    json_assertions: [{ path: 'status', op: 'equals', value: 'ok', case_insensitive: true }],
    display_fields: [{ label: 'Ver', path: 'version' }],
    max_latency_ms: 500,
    follow_redirects: true,
    max_redirects: 5,
    tls_skip_verify: true,
    tls_min_days_valid: 7,
    tls_server_name: 'api.internal',
    tls_ca_pem: '-----BEGIN CERTIFICATE-----\nMIIB\n-----END CERTIFICATE-----',
    collect_timing: true,
  };
  const monitor = storedMonitor('http', config);
  const form = initialMonitorFormState(monitor);
  assert.equal(form.status_rules, '200,204, 300-399, 5xx');
  assertWire(buildMonitorFormRequest('http', form, false), {
    name: 'Stored monitor',
    type: 'http',
    config: {
      url: 'https://api.example.com/health',
      method: 'POST',
      headers: { Authorization: 'Bearer stored' },
      expected_statuses: [200, 204],
      expected_status_ranges: [{ min: 300, max: 399 }],
      expected_status_classes: ['5xx'],
      body: '{"ping":true}',
      body_assertions: [
        { op: 'contains', value: 'healthy', case_insensitive: true },
        { op: 'not_contains', value: 'error' },
      ],
      response_header_assertions: [{ name: 'Server', op: 'exists' }],
      json_assertions: [{ path: 'status', op: 'equals', value: 'ok', case_insensitive: true }],
      display_fields: [{ label: 'Ver', path: 'version' }],
      max_latency_ms: 500,
      follow_redirects: true,
      max_redirects: 5,
      collect_timing: true,
      tls_skip_verify: true,
      tls_min_days_valid: 7,
      tls_server_name: 'api.internal',
      tls_ca_pem: '-----BEGIN CERTIFICATE-----\nMIIB\n-----END CERTIFICATE-----',
    },
    ...STORED_SCHEDULE,
    ...STORED_ALERTING,
    ...STORED_TAIL,
  });
});

test('http: edit rewrites legacy status and body fields into their current shape', () => {
  const monitor = storedMonitor('http', {
    url: 'http://legacy.example.com',
    method: 'GET',
    expected_status: 200,
    expected_body_substring: 'healthy',
    expected_body_regex: '^ok',
  } as HTTPMonitorConfig);
  const request = buildMonitorFormRequest('http', initialMonitorFormState(monitor), false);
  assertWire(request.config, {
    url: 'http://legacy.example.com',
    method: 'GET',
    expected_statuses: [200],
    body_assertions: [
      { op: 'contains', value: 'healthy' },
      { op: 'regex', value: '^ok' },
    ],
    follow_redirects: true,
    max_redirects: 10,
    collect_timing: false,
  });
});

test('http: a clone takes config, schedule and locations from initialData, not its alerting', () => {
  const initialData: CreateMonitorRequest = {
    name: 'API health (copy)',
    type: 'http',
    config: { url: 'https://api.example.com', method: 'GET' },
    interval_seconds: 300,
    timeout_seconds: 20,
    enabled: false,
    tags: ['copied'],
    consecutive_failures_threshold: 5,
    notification_mode: 'custom',
    notification_channels: [{ channel_id: 'ch-1', delay_seconds: 0 }],
    depends_on_ids: ['dep-2'],
    location_ids: ['loc-z'],
    location_quorum: 1,
  };
  assertWire(buildMonitorFormRequest('http', initialMonitorFormState(undefined, initialData), false), {
    name: 'API health (copy)',
    type: 'http',
    config: { url: 'https://api.example.com', method: 'GET', follow_redirects: true, max_redirects: 10, collect_timing: false },
    interval_seconds: 300,
    timeout_seconds: 20,
    enabled: false,
    ...DEFAULT_ALERTING,
    tags: ['copied'],
    depends_on_ids: ['dep-2'],
    location_ids: ['loc-z'],
    location_quorum: 1,
  });
});

test('http: validation messages', () => {
  assert.deepEqual(validateMonitorForm('http', freshCreate('http'), false), {
    name: 'Name is required',
    url: 'URL is required',
  });
  assert.deepEqual(
    validateMonitorForm(
      'http',
      freshCreate('http', {
        name: 'x',
        url: 'http://plain.example.com',
        status_rules: '200, 9xx',
        max_latency_ms: '-1',
        max_redirects: -1,
        tls_min_days_valid: '3',
        tls_skip_verify: true,
        request_headers: [{ key: 'X-Only-Name', value: '', reveal: false }],
        display_fields: [{ label: 'Orphan', path: '' }],
        interval_seconds: 10,
        timeout_seconds: 10,
      }),
      false
    ),
    {
      url: 'TLS settings require an https:// URL',
      status_rules: 'Invalid status rule: 9xx',
      max_latency_ms: 'Max latency must be a positive number (ms)',
      max_redirects: 'Max redirects must be 0 or greater',
      tls_min_days_valid: 'TLS checks require an https:// URL',
      request_headers: 'Headers must have both a name and a value',
      display_fields: 'Each display value needs a path',
      timeout_seconds: 'Timeout must be less than interval',
    }
  );
});

// --- ping / dns -------------------------------------------------------------

test('ping: create from defaults', () => {
  const form = freshCreate('ping', { name: 'Gateway', host: ' 8.8.8.8 ' });
  assert.deepEqual(validateMonitorForm('ping', { ...form, host: ' ' }, false), { host: 'Host is required' });
  assertWire(buildMonitorFormRequest('ping', form, false), {
    name: 'Gateway',
    type: 'ping',
    config: { host: '8.8.8.8' },
    ...NEW_SCHEDULE,
    ...DEFAULT_ALERTING,
    ...NEW_TAIL,
  });
});

test('ping: create from a filled form and an edit round trip', () => {
  const filled = freshCreate('ping', {
    name: 'Edge',
    host: 'edge.example.com',
    interval_seconds: 30,
    timeout_seconds: 5,
    tags: 'edge',
    location_ids: ['loc-a'],
    location_quorum: 1,
  });
  assertWire(buildMonitorFormRequest('ping', filled, false), {
    name: 'Edge',
    type: 'ping',
    config: { host: 'edge.example.com' },
    interval_seconds: 30,
    timeout_seconds: 5,
    enabled: true,
    ...DEFAULT_ALERTING,
    tags: ['edge'],
    depends_on_ids: [],
    location_ids: ['loc-a'],
    location_quorum: 1,
  });

  const monitor = storedMonitor('ping', { host: 'gw.internal' });
  assertWire(buildMonitorFormRequest('ping', initialMonitorFormState(monitor), false), {
    name: 'Stored monitor',
    type: 'ping',
    config: { host: 'gw.internal' },
    ...STORED_SCHEDULE,
    ...STORED_ALERTING,
    ...STORED_TAIL,
  });
});

test('dns: create from defaults', () => {
  const form = freshCreate('dns', { name: 'Apex', host: 'example.com' });
  assertWire(buildMonitorFormRequest('dns', form, false), {
    name: 'Apex',
    type: 'dns',
    config: { host: 'example.com', record_type: 'A' },
    ...NEW_SCHEDULE,
    ...DEFAULT_ALERTING,
    ...NEW_TAIL,
  });
});

test('dns: create from a filled form', () => {
  const form = freshCreate('dns', {
    name: 'Mail',
    host: ' example.com ',
    dns_record_type: 'MX',
    dns_expected_answers: '1.2.3.4,\n 5.6.7.8 ,',
    dns_nameserver: ' 10.0.0.2:53 ',
  });
  assertWire(buildMonitorFormRequest('dns', form, false).config, {
    host: 'example.com',
    record_type: 'MX',
    expected_answers: ['1.2.3.4', '5.6.7.8'],
    nameserver: '10.0.0.2:53',
  });
});

test('dns: edit round trip', () => {
  const monitor = storedMonitor('dns', {
    host: 'example.com',
    record_type: 'TXT',
    expected_answers: ['v=spf1 -all', 'google-site'],
    nameserver: '1.1.1.1',
  });
  const form = initialMonitorFormState(monitor);
  assert.equal(form.dns_expected_answers, 'v=spf1 -all, google-site');
  assertWire(buildMonitorFormRequest('dns', form, false), {
    name: 'Stored monitor',
    type: 'dns',
    config: {
      host: 'example.com',
      record_type: 'TXT',
      expected_answers: ['v=spf1 -all', 'google-site'],
      nameserver: '1.1.1.1',
    },
    ...STORED_SCHEDULE,
    ...STORED_ALERTING,
    ...STORED_TAIL,
  });
  // A stored record type that was never set defaults to A on save.
  const bare = initialMonitorFormState(storedMonitor('dns', { host: 'example.com' }));
  assertWire(buildMonitorFormRequest('dns', bare, false).config, { host: 'example.com', record_type: 'A' });
});

// --- synthetic_api ------------------------------------------------------------

test('synthetic_api: create from defaults', () => {
  const form = freshCreate('synthetic_api', { name: 'Journey' });
  assert.deepEqual(validateMonitorForm('synthetic_api', form, false), {});
  assertWire(buildMonitorFormRequest('synthetic_api', form, false), {
    name: 'Journey',
    type: 'synthetic_api',
    config: {
      failure_mode: 'fail_fast',
      steps: [
        {
          id: 'health',
          request: { method: 'GET', url: '/health', follow_redirects: true },
          assert: [{ target: 'status', op: 'equals', value: 200 }],
        },
      ],
    },
    interval_seconds: 60,
    timeout_seconds: 30,
    enabled: true,
    ...DEFAULT_ALERTING,
    ...NEW_TAIL,
  });
});

test('synthetic_api: create from the health + auth template, customized', () => {
  const base = freshCreate('synthetic_api', { name: 'Auth journey' });
  const templated = { ...base, ...syntheticAPITemplateFields('health_auth') };
  templated.synthetic_api_steps = templated.synthetic_api_steps.map((step, idx) =>
    idx === 0 ? { ...step, method: ' get ', timeout_seconds: '20', max_redirects: '0', follow_redirects: false } : step
  );
  // A blank trailing row, as "Add variable" leaves it, is dropped.
  templated.synthetic_api_variables = [...templated.synthetic_api_variables, { key: '  ', value: '' }];
  assert.deepEqual(validateMonitorForm('synthetic_api', templated, false), {});
  assertWire(buildMonitorFormRequest('synthetic_api', templated, false).config, {
    failure_mode: 'fail_fast',
    steps: [
      {
        id: 'health',
        request: { method: 'GET', url: '/health', timeout_seconds: 20, follow_redirects: false, max_redirects: 0 },
        name: 'Health endpoint',
        assert: [{ target: 'status', op: 'equals', value: 200 }],
      },
      {
        id: 'login',
        request: {
          method: 'POST',
          url: '/auth/login',
          headers: { 'Content-Type': 'application/json' },
          body: '{"email":"{{email}}","password":"{{password}}"}',
          follow_redirects: true,
        },
        name: 'Login',
        assert: [
          { target: 'status', op: 'in', value: [200, 201] },
          { target: 'json', op: 'exists', path: 'token' },
        ],
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

test('synthetic_api: edit round trip', () => {
  const config: SyntheticAPIMonitorConfig = {
    base_url: 'https://api.example.com',
    failure_mode: 'continue',
    variables: { token: 'abc' },
    steps: [
      {
        id: 'login',
        name: 'Login',
        request: {
          method: 'POST',
          url: '/login',
          headers: { 'X-Key': 'k' },
          body: '{}',
          timeout_seconds: 5,
          follow_redirects: false,
          max_redirects: 2,
        },
        assert: [
          { target: 'json', op: 'number_gt', path: 'count', value: 3 },
          { target: 'header', op: 'equals', path: 'X-Env', value: 'prod' },
          { target: 'status', op: 'in', value: [200, 201] },
        ],
        extract: [{ name: 'sid', from: 'header', path: 'Set-Cookie', sensitive: true }],
      },
    ],
  };
  const monitor = storedMonitor('synthetic_api', config);
  assert.equal(hasAdvancedSyntheticAPIConfig(config), true);
  assertWire(buildMonitorFormRequest('synthetic_api', initialMonitorFormState(monitor), false), {
    name: 'Stored monitor',
    type: 'synthetic_api',
    config: {
      failure_mode: 'continue',
      steps: [
        {
          id: 'login',
          request: {
            method: 'POST',
            url: '/login',
            headers: { 'X-Key': 'k' },
            body: '{}',
            timeout_seconds: 5,
            follow_redirects: false,
            max_redirects: 2,
          },
          name: 'Login',
          assert: [
            { target: 'json', op: 'number_gt', path: 'count', value: 3 },
            { target: 'header', op: 'equals', path: 'X-Env', value: 'prod' },
            { target: 'status', op: 'in', value: [200, 201] },
          ],
          extract: [{ name: 'sid', from: 'header', path: 'Set-Cookie', sensitive: true }],
        },
      ],
      base_url: 'https://api.example.com',
      variables: { token: 'abc' },
    },
    ...STORED_SCHEDULE,
    ...STORED_ALERTING,
    ...STORED_TAIL,
  });
});

test('synthetic_api: an edit round trip keeps every assertion value its type', () => {
  const assert_ = [
    // Strings that read as another JSON type, or lose edge whitespace/quotes.
    { target: 'json', op: 'equals', path: 'code', value: '123' },
    { target: 'json', op: 'equals', path: 'flag', value: 'true' },
    { target: 'json', op: 'not_equals', path: 'v', value: 'null' },
    { target: 'json', op: 'equals', path: 'q', value: '"quoted"' },
    { target: 'body', op: 'contains', value: ' padded ' },
    { target: 'header', op: 'equals', path: 'X-Version', value: '1.50' },
    { target: 'json', op: 'regex', path: 'id', value: '^\\d+$' },
    // Non-strings stay what they are.
    { target: 'json', op: 'equals', path: 'n', value: 123 },
    { target: 'json', op: 'equals', path: 'b', value: false },
    { target: 'json', op: 'number_lte', path: 'lag', value: 2.5 },
    { target: 'json', op: 'bool_is', path: 'ok', value: true },
    { target: 'status', op: 'in', value: [200, 204] },
    { target: 'json', op: 'equals', path: 'obj', value: { a: 1 } },
  ] as SyntheticAPIMonitorConfig['steps'][number]['assert'];
  const monitor = storedMonitor('synthetic_api', {
    steps: [{ id: 'check', request: { method: 'GET', url: 'https://api.example.com' }, assert: assert_ }],
  } as SyntheticAPIMonitorConfig);
  const form = initialMonitorFormState(monitor);
  assert.deepEqual(
    form.synthetic_api_steps[0].assertions.slice(0, 4).map((a) => a.value_input),
    ['"123"', '"true"', '"null"', '"\\"quoted\\""'],
  );
  const config = buildMonitorFormRequest('synthetic_api', form, false).config as SyntheticAPIMonitorConfig;
  assertWire(config.steps[0].assert, assert_);
});

test('synthetic_api: a typed value keeps the type the input expresses', () => {
  const form = freshCreate('synthetic_api', { name: 'Journey', synthetic_api_base_url: 'https://api.example.com' });
  const typed: Array<[SyntheticAPIAssertionTarget, string, string, string]> = [
    // JSON equals: a bare literal is its JSON type, a quoted one a string.
    ['json', 'equals', 'code', '"123"'],
    ['json', 'equals', 'code', '123'],
    ['json', 'equals', 'flag', '"true"'],
    ['json', 'equals', 'flag', 'true'],
    ['json', 'equals', 'name', 'plain text'],
    // Text assertions submit the text as typed, never a number or object.
    ['body', 'contains', '', '1.50'],
    ['body', 'contains', '', '{"status":"ok"}'],
    ['header', 'equals', 'X-Id', '12345678901234567890'],
    ['json', 'regex', 'id', '123'],
    ['body', 'contains', '', '"quoted"'],
    // Number and boolean ops keep their literal.
    ['json', 'number_gt', 'n', '5'],
    ['json', 'bool_is', 'ok', 'false'],
  ];
  form.synthetic_api_steps = [{
    ...form.synthetic_api_steps[0],
    assertions: typed.map(([target, op, path, value_input]) => ({ target, op, path, value_input })),
  }];
  assert.deepEqual(validateMonitorForm('synthetic_api', form, false), {});
  const config = buildMonitorFormRequest('synthetic_api', form, false).config as SyntheticAPIMonitorConfig;
  assert.deepEqual(config.steps[0].assert?.map((a) => a.value), [
    '123', 123, 'true', true, 'plain text',
    '1.50', '{"status":"ok"}', '12345678901234567890', '123', 'quoted',
    5, false,
  ]);
});

test('synthetic_api: validation messages', () => {
  const form = freshCreate('synthetic_api', { name: 'x', synthetic_api_base_url: 'api.example.com' });
  form.synthetic_api_steps = [
    { ...form.synthetic_api_steps[0], assertions: [{ target: 'status', op: 'in', path: '', value_input: '200' }] },
  ];
  assert.deepEqual(validateMonitorForm('synthetic_api', form, false), {
    synthetic_api_base_url: 'Base URL must start with http:// or https://',
    synthetic_api_steps: "Step 'health' assertion #1 expects a non-empty array of status codes",
  });
  assert.deepEqual(validateMonitorForm('synthetic_api', { ...form, synthetic_api_steps: [] }, false), {
    synthetic_api_base_url: 'Base URL must start with http:// or https://',
    synthetic_api_steps: 'At least one API step is required',
  });
});

// --- synthetic_browser --------------------------------------------------------

test('synthetic_browser: create from defaults (guided homepage smoke)', () => {
  const form = freshCreate('synthetic_browser', { name: 'Home', synthetic_browser_start_url: ' https://app.example.com ' });
  assert.equal(form.synthetic_browser_guided_template, 'homepage_smoke');
  assert.deepEqual(validateMonitorForm('synthetic_browser', form, true), {});
  assertWire(buildMonitorFormRequest('synthetic_browser', form, true), {
    name: 'Home',
    type: 'synthetic_browser',
    config: {
      start_url: 'https://app.example.com',
      device: 'Desktop Chrome',
      failure_mode: 'fail_fast',
      steps: [
        { id: 'open_homepage', action: 'goto', url: 'https://app.example.com' },
        { id: 'assert_page_ready', action: 'assert_visible', selector: 'main' },
      ],
      artifacts: { screenshot_on_failure: true, trace_on_failure: false, har_on_failure: false },
    },
    interval_seconds: 60,
    timeout_seconds: 30,
    enabled: true,
    ...DEFAULT_ALERTING,
    ...NEW_TAIL,
  });
});

const guidedLoginForm = (): MonitorFormState => {
  const base = freshCreate('synthetic_browser', { name: 'Login' });
  return {
    ...base,
    ...syntheticBrowserGuidedTemplateFields(base, 'login_flow'),
    synthetic_browser_guided_username: ' robot@example.com ',
    synthetic_browser_guided_password: 's3cret',
  };
};

const GUIDED_LOGIN_CONFIG = {
  start_url: 'https://app.example.com/login',
  device: 'Desktop Chrome',
  failure_mode: 'fail_fast',
  steps: [
    { id: 'open_login', action: 'goto', url: 'https://app.example.com/login' },
    { id: 'fill_username', action: 'fill', selector: '[name=email]', value: '{{username}}' },
    { id: 'fill_password', action: 'fill', selector: '[name=password]', value: '{{password}}' },
    { id: 'submit_login', action: 'click', selector: 'button[type=submit]' },
    { id: 'wait_dashboard', action: 'wait_for', selector: '[data-test=dashboard]' },
    { id: 'assert_url', action: 'assert_url', value: '/dashboard' },
  ],
  artifacts: { screenshot_on_failure: true, trace_on_failure: false, har_on_failure: false },
  // Not a monitor secret: synthetic variables are stored and served in plain text.
  variables: { username: 'robot@example.com', password: 's3cret' },
};

test('synthetic_browser: create from a filled guided login flow', () => {
  const form = guidedLoginForm();
  assert.deepEqual(validateMonitorForm('synthetic_browser', form, true), {});
  assertWire(buildMonitorFormRequest('synthetic_browser', form, true).config, GUIDED_LOGIN_CONFIG);
});

test('synthetic_browser: switching guided to expert keeps the same payload', () => {
  const guided = guidedLoginForm();
  const expert = { ...guided, ...syntheticBrowserGuidedToExpertFields(guided) };
  assertWire(buildMonitorFormRequest('synthetic_browser', expert, false).config, GUIDED_LOGIN_CONFIG);
});

test('synthetic_browser: create from an expert template, customized', () => {
  const base = freshCreate('synthetic_browser', { name: 'Login (expert)' });
  const form = {
    ...base,
    ...syntheticBrowserTemplateFields('login_flow'),
    synthetic_browser_device: ' Pixel 7 ',
    synthetic_browser_trace_on_failure: true,
  };
  form.synthetic_browser_steps = form.synthetic_browser_steps.map((step) =>
    step.id === 'wait_dashboard' ? { ...step, timeout_seconds: '15' } : step
  );
  assert.deepEqual(validateMonitorForm('synthetic_browser', form, false), {});
  assertWire(buildMonitorFormRequest('synthetic_browser', form, false).config, {
    start_url: 'https://app.example.com/login',
    device: 'Pixel 7',
    failure_mode: 'fail_fast',
    steps: [
      { id: 'open_login', action: 'goto', url: 'https://app.example.com/login' },
      { id: 'fill_email', action: 'fill', selector: '[name=email]', value: '{{email}}' },
      { id: 'fill_password', action: 'fill', selector: '[name=password]', value: '{{password}}' },
      { id: 'submit', action: 'click', selector: 'button[type=submit]' },
      { id: 'wait_dashboard', action: 'wait_for', selector: '[data-test=dashboard]', timeout_seconds: 15 },
      { id: 'assert_url', action: 'assert_url', value: '/dashboard' },
    ],
    artifacts: { screenshot_on_failure: true, trace_on_failure: true, har_on_failure: false },
    variables: { email: 'monitor@example.com', password: 'change-me' },
  });
});

test('synthetic_browser: edit round trip (expert mode)', () => {
  const config: SyntheticBrowserMonitorConfig = {
    start_url: 'https://shop.example.com',
    device: 'Desktop Chrome',
    failure_mode: 'continue',
    variables: { sku: 'A1' },
    artifacts: { screenshot_on_failure: false, trace_on_failure: true, har_on_failure: true },
    steps: [
      { id: 'open', action: 'goto', url: 'https://shop.example.com' },
      { id: 'search', action: 'fill', selector: '#q', value: '{{sku}}', timeout_seconds: 8 },
      { id: 'check', action: 'assert_text', selector: 'h1', value: 'A1' },
    ],
  };
  const monitor = storedMonitor('synthetic_browser', config);
  assert.equal(hasAdvancedSyntheticBrowserConfig(config), true);
  const form = initialMonitorFormState(monitor);
  assert.equal(form.synthetic_browser_guided_template, 'homepage_smoke');
  assertWire(buildMonitorFormRequest('synthetic_browser', form, false), {
    name: 'Stored monitor',
    type: 'synthetic_browser',
    config: {
      start_url: 'https://shop.example.com',
      device: 'Desktop Chrome',
      failure_mode: 'continue',
      steps: [
        { id: 'open', action: 'goto', url: 'https://shop.example.com' },
        { id: 'search', action: 'fill', selector: '#q', value: '{{sku}}', timeout_seconds: 8 },
        { id: 'check', action: 'assert_text', selector: 'h1', value: 'A1' },
      ],
      artifacts: { screenshot_on_failure: false, trace_on_failure: true, har_on_failure: true },
      variables: { sku: 'A1' },
    },
    ...STORED_SCHEDULE,
    ...STORED_ALERTING,
    ...STORED_TAIL,
  });
});

test('synthetic_browser: validation messages', () => {
  assert.deepEqual(validateMonitorForm('synthetic_browser', freshCreate('synthetic_browser', { name: 'x' }), true), {
    synthetic_browser_start_url: 'Start URL is required',
  });
  const login = { ...guidedLoginForm(), synthetic_browser_guided_submit_selector: ' ' };
  assert.deepEqual(validateMonitorForm('synthetic_browser', login, true), {
    synthetic_browser_steps: 'Submit selector is required',
  });
  const expert = freshCreate('synthetic_browser', { name: 'x', synthetic_browser_start_url: 'https://a.example.com' });
  expert.synthetic_browser_steps = [{ ...expert.synthetic_browser_steps[0], url: '' }];
  assert.deepEqual(validateMonitorForm('synthetic_browser', expert, false), {
    synthetic_browser_steps: "Step 'open' requires a URL",
  });
});
