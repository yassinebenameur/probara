import assert from 'node:assert/strict';
import test from 'node:test';
import { buildGrpcRequest, buildTcpRequest, initialGrpcFormState, initialTcpFormState } from './monitor-form/network';
import {
  buildWebsocketRequest,
  initialWebsocketFormState,
  renameWebsocketHeaderRow,
} from './monitor-form/websocket';
import { buildSipRequest, initialSipFormState, sipLoadedConfig } from './monitor-form/sip';
import {
  CLONED_SCHEDULE,
  CLONED_TAIL,
  DEFAULT_ALERTING,
  STORED_ALERTING,
  assertWire,
  cloneSource,
  storedMonitor,
} from './monitor-form/testing';
import type { CreateMonitorRequest, SIPMonitorConfig, WebSocketMonitorConfig } from './types';

// gRPC, TCP, WebSocket and SIP: the delegated forms with a schedule and
// locations. WebSocket headers (a secret map) and the SIP password are the
// secrets here.

const NEW_ENVELOPE = { interval_seconds: 60, timeout_seconds: 10, enabled: true } as const;
const NEW_TAIL = { depends_on_ids: [], location_ids: [], location_quorum: 1 } as const;
const STORED_ENVELOPE = { interval_seconds: 120, timeout_seconds: 15, enabled: false } as const;
const STORED_TAIL = {
  tags: ['prod', 'edge'],
  depends_on_ids: ['dep-1'],
  location_ids: ['loc-a', 'loc-b', 'loc-c'],
  location_quorum: 2,
} as const;

// --- grpc -------------------------------------------------------------------

test('grpc: create from defaults', () => {
  const form = { ...initialGrpcFormState(), name: 'Payments', host: ' grpc.example.com ' };
  assertWire(buildGrpcRequest(form), {
    name: 'Payments',
    type: 'grpc',
    config: { host: 'grpc.example.com', port: 443, use_tls: true },
    ...NEW_ENVELOPE,
    ...DEFAULT_ALERTING,
    ...NEW_TAIL,
  });
});

test('grpc: create from a filled form', () => {
  const form = {
    ...initialGrpcFormState(),
    name: 'Payments',
    host: 'grpc.internal',
    port: 50051,
    service: ' grpc.health.v1.Health ',
    use_tls: false,
    interval_seconds: 30,
    timeout_seconds: 5,
    tags: 'payments,  grpc',
    notification_mode: 'custom' as const,
    notification_channels: [{ channel_id: 'ch-2', delay_seconds: 60 }],
    location_ids: ['loc-a', 'loc-b'],
    location_quorum: 2,
  };
  assertWire(buildGrpcRequest(form), {
    name: 'Payments',
    type: 'grpc',
    config: { host: 'grpc.internal', port: 50051, use_tls: false, service: 'grpc.health.v1.Health' },
    interval_seconds: 30,
    timeout_seconds: 5,
    enabled: true,
    consecutive_failures_threshold: 2,
    notification_mode: 'custom',
    dependency_suppression: 'inherit',
    notification_channels: [{ channel_id: 'ch-2', delay_seconds: 60 }],
    tags: ['payments', 'grpc'],
    depends_on_ids: [],
    location_ids: ['loc-a', 'loc-b'],
    location_quorum: 2,
  });
});

test('grpc: edit round trip', () => {
  const monitor = storedMonitor('grpc', { host: 'grpc.internal', port: 8443, service: 'svc', use_tls: false });
  assertWire(buildGrpcRequest(initialGrpcFormState(monitor)), {
    name: 'Stored monitor',
    type: 'grpc',
    config: { host: 'grpc.internal', port: 8443, use_tls: false, service: 'svc' },
    ...STORED_ENVELOPE,
    ...STORED_ALERTING,
    ...STORED_TAIL,
  });
  // A stored config without port or TLS saves the form defaults back.
  assertWire(buildGrpcRequest(initialGrpcFormState(storedMonitor('grpc', { host: 'h' }))).config, {
    host: 'h',
    port: 443,
    use_tls: true,
  });
});

test('grpc: a clone takes config, schedule, tags and locations from initialData, not its alerting', () => {
  const initialData = cloneSource('grpc', { host: 'grpc.internal', port: 50051, service: 'svc', use_tls: false });
  assertWire(buildGrpcRequest(initialGrpcFormState(undefined, initialData)), {
    name: 'Source (copy)',
    type: 'grpc',
    config: { host: 'grpc.internal', port: 50051, use_tls: false, service: 'svc' },
    ...CLONED_SCHEDULE,
    ...DEFAULT_ALERTING,
    ...CLONED_TAIL,
  });
});

// --- tcp --------------------------------------------------------------------

test('tcp: create from defaults', () => {
  const defaults = initialTcpFormState();
  assert.equal(defaults.port, 0); // required: the form asks for one
  assertWire(buildTcpRequest({ ...defaults, name: 'DB port', host: 'db.internal', port: 5432 }), {
    name: 'DB port',
    type: 'tcp',
    config: { host: 'db.internal', port: 5432, use_tls: false },
    ...NEW_ENVELOPE,
    ...DEFAULT_ALERTING,
    ...NEW_TAIL,
  });
});

test('tcp: create from a filled form; skip-verify only rides with TLS', () => {
  const form = {
    ...initialTcpFormState(),
    name: 'SMTPS',
    host: ' mail.example.com ',
    port: 465,
    use_tls: true,
    tls_skip_verify: true,
    depends_on_ids: ['dep-1'],
  };
  assertWire(buildTcpRequest(form).config, { host: 'mail.example.com', port: 465, use_tls: true, tls_skip_verify: true });
  assertWire(buildTcpRequest({ ...form, use_tls: false }).config, { host: 'mail.example.com', port: 465, use_tls: false });
  assertWire(buildTcpRequest(form).depends_on_ids, ['dep-1']);
});

test('tcp: edit round trip', () => {
  const monitor = storedMonitor('tcp', { host: 'mail.example.com', port: 465, use_tls: true, tls_skip_verify: true });
  assertWire(buildTcpRequest(initialTcpFormState(monitor)), {
    name: 'Stored monitor',
    type: 'tcp',
    config: { host: 'mail.example.com', port: 465, use_tls: true, tls_skip_verify: true },
    ...STORED_ENVELOPE,
    ...STORED_ALERTING,
    ...STORED_TAIL,
  });
});

test('tcp: a clone takes config, schedule, tags and locations from initialData, not its alerting', () => {
  const initialData = cloneSource('tcp', { host: 'mail.example.com', port: 465, use_tls: true, tls_skip_verify: true });
  assertWire(buildTcpRequest(initialTcpFormState(undefined, initialData)), {
    name: 'Source (copy)',
    type: 'tcp',
    config: { host: 'mail.example.com', port: 465, use_tls: true, tls_skip_verify: true },
    ...CLONED_SCHEDULE,
    ...DEFAULT_ALERTING,
    ...CLONED_TAIL,
  });
});

// --- websocket ----------------------------------------------------------------

test('websocket: create from defaults', () => {
  const form = { ...initialWebsocketFormState(), name: 'Socket', url: ' wss://ws.example.com/socket ' };
  assertWire(buildWebsocketRequest(form, false), {
    name: 'Socket',
    type: 'websocket',
    config: { url: 'wss://ws.example.com/socket' },
    ...NEW_ENVELOPE,
    ...DEFAULT_ALERTING,
    ...NEW_TAIL,
  });
});

test('websocket: create from a filled form', () => {
  const form = {
    ...initialWebsocketFormState(),
    name: 'Socket',
    url: 'wss://ws.example.com/socket',
    headers: [
      { key: 'Authorization', value: 'Bearer x', hasStoredValue: false },
      { key: ' Origin ', value: 'https://app.example.com', hasStoredValue: false },
      { key: '', value: '', hasStoredValue: false },
    ],
    tls_skip_verify: true,
    send_message: '{"type":"ping"}',
    expected_substring: 'pong',
    warn_latency_ms: '200',
    max_latency_ms: '500',
  };
  assertWire(buildWebsocketRequest(form, false).config, {
    url: 'wss://ws.example.com/socket',
    headers: { Authorization: 'Bearer x', Origin: 'https://app.example.com' },
    tls_skip_verify: true,
    send_message: '{"type":"ping"}',
    expected_substring: 'pong',
    max_latency_ms: 500,
    warn_latency_ms: 200,
  });
  // Skip-verify only applies to wss://.
  assertWire(buildWebsocketRequest({ ...form, url: 'ws://ws.example.com', headers: [] }, false).config, {
    url: 'ws://ws.example.com',
    send_message: '{"type":"ping"}',
    expected_substring: 'pong',
    max_latency_ms: 500,
    warn_latency_ms: 200,
  });
});

const wsStored = () =>
  storedMonitor('websocket', {
    url: 'wss://ws.example.com/socket',
    headers: { Authorization: '***', 'X-Env': '***' },
    tls_skip_verify: true,
    send_message: 'hello',
    expected_substring: 'welcome',
    max_latency_ms: 1000,
    warn_latency_ms: 400,
  });

test('websocket: edit round trip submits every masked header back as "***" (keep)', () => {
  const form = initialWebsocketFormState(wsStored());
  assertWire(form.headers, [
    { key: 'Authorization', value: '', hasStoredValue: true, originalKey: 'Authorization' },
    { key: 'X-Env', value: '', hasStoredValue: true, originalKey: 'X-Env' },
  ]);
  assertWire(buildWebsocketRequest(form, true), {
    name: 'Stored monitor',
    type: 'websocket',
    config: {
      url: 'wss://ws.example.com/socket',
      headers: { Authorization: '***', 'X-Env': '***' },
      tls_skip_verify: true,
      send_message: 'hello',
      expected_substring: 'welcome',
      max_latency_ms: 1000,
      warn_latency_ms: 400,
    },
    ...STORED_ENVELOPE,
    ...STORED_ALERTING,
    ...STORED_TAIL,
  });
});

const wsHeaders = (request: CreateMonitorRequest) => (request.config as WebSocketMonitorConfig).headers;

test('websocket: edit drops, replaces and renames stored headers per the map merge contract', () => {
  const form = initialWebsocketFormState(wsStored());
  // A removed row is left out of the submitted map: the server drops that key.
  assertWire(wsHeaders(buildWebsocketRequest({ ...form, headers: [form.headers[0]] }, true)), { Authorization: '***' });
  // Removing every row still sends the map, empty, so the stored headers clear.
  assertWire(wsHeaders(buildWebsocketRequest({ ...form, headers: [] }, true)), {});
  // A new value replaces the stored one.
  const replaced = [{ ...form.headers[0], value: 'Bearer new', hasStoredValue: false }, form.headers[1]];
  assertWire(wsHeaders(buildWebsocketRequest({ ...form, headers: replaced }, true)), {
    Authorization: 'Bearer new',
    'X-Env': '***',
  });
  // Changing only the case keeps the stored value; a real rename loses it.
  assert.equal(renameWebsocketHeaderRow(form.headers[1], 'x-env').hasStoredValue, true);
  const renamed = renameWebsocketHeaderRow(form.headers[1], 'X-Environment');
  assertWire(renamed, { key: 'X-Environment', value: '', hasStoredValue: false, originalKey: 'X-Env' });
  assertWire(wsHeaders(buildWebsocketRequest({ ...form, headers: [form.headers[0], renamed] }, true)), {
    Authorization: '***',
    'X-Environment': '',
  });
});

test('websocket: a clone takes config, schedule, tags and locations from initialData, not its alerting', () => {
  const initialData = cloneSource('websocket', {
    url: 'wss://ws.example.com/socket',
    headers: { 'X-Env': 'prod' },
    tls_skip_verify: true,
    send_message: 'hello',
    expected_substring: 'welcome',
    max_latency_ms: 1000,
    warn_latency_ms: 400,
  });
  assertWire(buildWebsocketRequest(initialWebsocketFormState(undefined, initialData), false), {
    name: 'Source (copy)',
    type: 'websocket',
    config: {
      url: 'wss://ws.example.com/socket',
      headers: { 'X-Env': 'prod' },
      tls_skip_verify: true,
      send_message: 'hello',
      expected_substring: 'welcome',
      max_latency_ms: 1000,
      warn_latency_ms: 400,
    },
    ...CLONED_SCHEDULE,
    ...DEFAULT_ALERTING,
    ...CLONED_TAIL,
  });
});

// --- sip ----------------------------------------------------------------------

test('sip: create from defaults', () => {
  const form = { ...initialSipFormState(), name: 'Trunk', host: ' sbc.example.com ' };
  assertWire(buildSipRequest(form, undefined), {
    name: 'Trunk',
    type: 'sip',
    config: { host: 'sbc.example.com', port: 5060, transport: 'udp' },
    ...NEW_ENVELOPE,
    ...DEFAULT_ALERTING,
    ...NEW_TAIL,
  });
});

test('sip: create from a filled REGISTER form', () => {
  const form = {
    ...initialSipFormState(),
    name: 'Registrar',
    host: 'sbc.example.com',
    port: 5061,
    transport: 'tls' as const,
    method: 'register' as const,
    username: ' agent42 ',
    password: 'pw',
    domain: ' example.com ',
    tls_skip_verify: true,
    tls_server_name: ' sip.example.com ',
    tls_min_days_valid: '14',
    expected_status: '200',
    tags: 'voice',
  };
  assertWire(buildSipRequest(form, undefined), {
    name: 'Registrar',
    type: 'sip',
    config: {
      host: 'sbc.example.com',
      port: 5061,
      transport: 'tls',
      method: 'register',
      username: 'agent42',
      password: 'pw',
      domain: 'example.com',
      tls_skip_verify: true,
      tls_server_name: 'sip.example.com',
      tls_min_days_valid: 14,
      expected_status: 200,
    },
    ...NEW_ENVELOPE,
    ...DEFAULT_ALERTING,
    tags: ['voice'],
    ...NEW_TAIL,
  });
});

test('sip: create from a filled test-call (INVITE) form under RFC 3263', () => {
  const form = {
    ...initialSipFormState(),
    name: 'Test call',
    host: 'example.com',
    port: 0,
    transport: 'auto' as const,
    resolution: 'rfc3263' as const,
    nameserver: ' 10.0.0.2 ',
    method: 'invite' as const,
    call_number: ' 1000 ',
    call_from_user: 'probe',
    call_mode: 'answer' as const,
    call_hold_ms: '3000',
    call_warn_pdd_ms: '1500',
    call_max_pdd_ms: '4000',
    call_probe_header: true,
    interval_seconds: 300,
    timeout_seconds: 20,
  };
  assertWire(buildSipRequest(form, undefined), {
    name: 'Test call',
    type: 'sip',
    config: {
      host: 'example.com',
      port: 0,
      transport: '',
      resolution: 'rfc3263',
      nameserver: '10.0.0.2',
      method: 'invite',
      call: {
        number: '1000',
        from_user: 'probe',
        mode: 'answer',
        hold_ms: 3000,
        warn_pdd_ms: 1500,
        max_pdd_ms: 4000,
        probe_header: true,
      },
    },
    interval_seconds: 300,
    timeout_seconds: 20,
    enabled: true,
    ...DEFAULT_ALERTING,
    ...NEW_TAIL,
  });
});

const sipStored = (): SIPMonitorConfig =>
  ({
    host: 'sbc.example.com',
    port: 5060,
    transport: 'UDP',
    method: 'register',
    username: 'agent42',
    password: '***',
    domain: 'example.com',
    expected_status: 200,
    future_field: { kept: true },
  }) as unknown as SIPMonitorConfig;

test('sip: edit round trip keeps the masked password and unrendered keys', () => {
  const monitor = storedMonitor('sip', sipStored());
  const form = initialSipFormState(monitor);
  assert.equal(form.password, '***');
  assert.equal(form.transport, 'udp');
  assertWire(buildSipRequest(form, sipLoadedConfig(monitor)), {
    name: 'Stored monitor',
    type: 'sip',
    config: {
      future_field: { kept: true },
      host: 'sbc.example.com',
      port: 5060,
      transport: 'udp',
      method: 'register',
      username: 'agent42',
      password: '***',
      domain: 'example.com',
      expected_status: 200,
    },
    ...STORED_ENVELOPE,
    ...STORED_ALERTING,
    ...STORED_TAIL,
  });
});

test('sip: dropping auth on edit clears the stored password with ""', () => {
  const monitor = storedMonitor('sip', sipStored());
  const form = { ...initialSipFormState(monitor), username: '', password: '' };
  const config = buildSipRequest(form, sipLoadedConfig(monitor)).config as SIPMonitorConfig;
  assert.equal(config.password, '');
  assert.equal(config.username, undefined);
  // A new password replaces it.
  const replaced = { ...initialSipFormState(monitor), password: 'new-secret' };
  assert.equal((buildSipRequest(replaced, sipLoadedConfig(monitor)).config as SIPMonitorConfig).password, 'new-secret');
});

test('sip: edit round trip of a stored test call', () => {
  const monitor = storedMonitor('sip', {
    host: 'example.com',
    port: 5061,
    transport: 'tls',
    method: 'invite',
    domain: 'example.com',
    call: { target_uri: 'sips:1000@example.com', mode: 'ring', warn_pdd_ms: 0, max_pdd_ms: 3000, sdp_address: '192.0.2.10' },
  });
  assertWire(buildSipRequest(initialSipFormState(monitor), sipLoadedConfig(monitor)).config, {
    host: 'example.com',
    port: 5061,
    transport: 'tls',
    method: 'invite',
    domain: 'example.com',
    call: { target_uri: 'sips:1000@example.com', mode: 'ring', warn_pdd_ms: 0, max_pdd_ms: 3000, sdp_address: '192.0.2.10' },
  });
});

test('sip: create from a filled test-call form that measures call quality', () => {
  const form = {
    ...initialSipFormState(),
    name: 'Call quality',
    host: 'sbc.example.com',
    method: 'invite' as const,
    call_number: '1000',
    call_hold_ms: '5000',
    call_media: true,
    call_media_expect: 'echo' as const,
    call_media_warn_loss_pct: '1',
    call_media_max_loss_pct: '2.5',
    call_media_warn_jitter_ms: '30',
    call_media_max_jitter_ms: '60',
    call_media_warn_mos: '4',
    call_media_min_mos: '3.6',
    call_media_warn_rtt_ms: '300',
    call_media_max_rtt_ms: '500',
    interval_seconds: 300,
  };
  assertWire(buildSipRequest(form, undefined), {
    name: 'Call quality',
    type: 'sip',
    config: {
      host: 'sbc.example.com',
      port: 5060,
      transport: 'udp',
      method: 'invite',
      call: {
        number: '1000',
        mode: 'answer',
        hold_ms: 5000,
        media: {
          expect: 'echo',
          warn_loss_pct: 1,
          max_loss_pct: 2.5,
          warn_jitter_ms: 30,
          max_jitter_ms: 60,
          warn_mos: 4,
          min_mos: 3.6,
          warn_rtt_ms: 300,
          max_rtt_ms: 500,
        },
      },
    },
    interval_seconds: 300,
    timeout_seconds: 10,
    enabled: true,
    ...DEFAULT_ALERTING,
    ...NEW_TAIL,
  });
});

test('sip: a test call turned to Measure only submits expect alone', () => {
  const form = {
    ...initialSipFormState(),
    name: 'Measure only',
    host: 'sbc.example.com',
    method: 'invite' as const,
    call_number: '1000',
    call_hold_ms: '3000',
    call_media: true,
    call_media_expect: 'none' as const,
    // Typed under another Expect, then hidden: never sent.
    call_media_min_mos: '3.6',
  };
  assertWire((buildSipRequest(form, undefined).config as SIPMonitorConfig).call, {
    number: '1000',
    mode: 'answer',
    hold_ms: 3000,
    media: { expect: 'none' },
  });
});

test('sip: edit round trip of a stored test call that measures call quality', () => {
  // Every call field the form hydrates, media included; the unknown media key
  // stays for the API to refuse (media keys are strict).
  const config = {
    host: 'sbc.example.com',
    port: 5060,
    transport: 'udp',
    method: 'invite',
    expected_status: 200,
    call: {
      number: '+33123456789',
      from_user: 'probe',
      from_display: 'Probara probe',
      from_domain: 'example.com',
      mode: 'answer',
      hold_ms: 6000,
      warn_pdd_ms: 3000,
      max_pdd_ms: 5000,
      warn_answer_ms: 8000,
      max_answer_ms: 12000,
      sdp_address: '203.0.113.10',
      probe_header: true,
      media: {
        future_media_field: 1,
        expect: 'two_way',
        warn_loss_pct: 1,
        max_loss_pct: 2.5,
        warn_jitter_ms: 30,
        max_jitter_ms: 60,
        warn_mos: 4,
        min_mos: 3.6,
        warn_rtt_ms: 300,
        max_rtt_ms: 500,
      },
    },
  } as unknown as SIPMonitorConfig;
  const monitor = storedMonitor('sip', config);
  const form = initialSipFormState(monitor);
  assert.equal(form.call_media, true);
  assert.equal(form.call_media_expect, 'two_way');
  assert.equal(form.call_media_min_mos, '3.6');
  assertWire(buildSipRequest(form, sipLoadedConfig(monitor)), {
    name: 'Stored monitor',
    type: 'sip',
    config,
    ...STORED_ENVELOPE,
    ...STORED_ALERTING,
    ...STORED_TAIL,
  });

  // Turning call quality off drops the whole media object.
  const off = buildSipRequest({ ...form, call_media: false }, sipLoadedConfig(monitor)).config as SIPMonitorConfig;
  assert.equal(off.call?.media, undefined);
  assert.equal(off.call?.hold_ms, 6000);
});

test('sip: edit round trip of a stored RFC 3263 monitor keeps SRV resolution', () => {
  // As the API may store it: resolution in any case, port and transport left
  // to SRV (0 and '').
  const monitor = storedMonitor('sip', {
    host: 'example.com',
    port: 0,
    transport: '',
    resolution: 'RFC3263',
    nameserver: '10.0.0.2',
  } as unknown as SIPMonitorConfig);
  const form = initialSipFormState(monitor);
  assert.equal(form.resolution, 'rfc3263');
  assert.equal(form.port, 0);
  assert.equal(form.transport, 'auto');
  assert.equal(form.nameserver, '10.0.0.2');
  assertWire(buildSipRequest(form, sipLoadedConfig(monitor)), {
    name: 'Stored monitor',
    type: 'sip',
    config: {
      host: 'example.com',
      port: 0,
      transport: '',
      resolution: 'rfc3263',
      nameserver: '10.0.0.2',
    },
    ...STORED_ENVELOPE,
    ...STORED_ALERTING,
    ...STORED_TAIL,
  });
});

test('sip: edit round trip of a stored TLS monitor keeps its TLS options', () => {
  const tlsConfig = (minDays: number): SIPMonitorConfig => ({
    host: 'sbc.example.com',
    port: 5061,
    transport: 'tls',
    tls_skip_verify: true,
    tls_server_name: 'sip.example.com',
    tls_min_days_valid: minDays,
  });
  const monitor = storedMonitor('sip', tlsConfig(14));
  const form = initialSipFormState(monitor);
  assert.equal(form.tls_skip_verify, true);
  assert.equal(form.tls_server_name, 'sip.example.com');
  assert.equal(form.tls_min_days_valid, '14');
  assertWire(buildSipRequest(form, sipLoadedConfig(monitor)), {
    name: 'Stored monitor',
    type: 'sip',
    config: {
      host: 'sbc.example.com',
      port: 5061,
      transport: 'tls',
      tls_skip_verify: true,
      tls_server_name: 'sip.example.com',
      tls_min_days_valid: 14,
    },
    ...STORED_ENVELOPE,
    ...STORED_ALERTING,
    ...STORED_TAIL,
  });
  // A stored threshold of 0 is a value, not unset: it saves back as 0.
  const zero = storedMonitor('sip', tlsConfig(0));
  assertWire(buildSipRequest(initialSipFormState(zero), sipLoadedConfig(zero)).config, tlsConfig(0));
});

test('sip: a clone builds on the source config but takes no alerting from it', () => {
  const initialData: CreateMonitorRequest = {
    name: 'Trunk (copy)',
    type: 'sip',
    config: { host: 'sbc.example.com', port: 5062, transport: 'tcp', extra: 1 } as unknown as SIPMonitorConfig,
    interval_seconds: 90,
    timeout_seconds: 9,
    notification_mode: 'custom',
  };
  assertWire(buildSipRequest(initialSipFormState(undefined, initialData), sipLoadedConfig(undefined, initialData)), {
    name: 'Trunk (copy)',
    type: 'sip',
    config: { extra: 1, host: 'sbc.example.com', port: 5062, transport: 'tcp' },
    interval_seconds: 90,
    timeout_seconds: 9,
    enabled: true,
    ...DEFAULT_ALERTING,
    ...NEW_TAIL,
  });
});

test('websocket: a clone whose initialData still carries masked headers never submits "***"', () => {
  // buildClonedMonitorInitialData strips masks, but the form must not trust
  // that: on create there is no stored value for a "***" to keep.
  const initialData = cloneSource('websocket', { url: 'wss://ws.example.com/socket', headers: { Authorization: '***' } });
  const form = initialWebsocketFormState(undefined, initialData);
  assertWire(form.headers, [{ key: 'Authorization', value: '', hasStoredValue: false }]);
  assertWire(wsHeaders(buildWebsocketRequest(form, false)), { Authorization: '' });
});

test('sip: a clone whose initialData still carries a masked password never submits "***"', () => {
  const initialData = cloneSource('sip', { host: 'sbc.example.com', port: 5060, transport: 'udp', username: 'u', password: '***' });
  const form = initialSipFormState(undefined, initialData);
  assert.equal(form.password, '');
  assert.ok(!JSON.stringify(buildSipRequest(form, sipLoadedConfig(undefined, initialData))).includes('***'));
});
