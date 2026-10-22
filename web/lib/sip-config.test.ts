import assert from 'node:assert/strict';
import test from 'node:test';
import {
  buildSipConfig,
  isHostOrIP,
  isSipCall,
  parseDecimal,
  parseWhole,
  sipCallFormFields,
  sipMediaMinHoldMs,
  sipCallsPerDay,
  sipCallTarget,
  sipCallURIError,
  SipFormFields,
  validateSipCallFields,
} from './sip-config';
import type { SIPMonitorConfig } from './types';

const stored = {
  host: 'sbc.example.com',
  port: 5061,
  transport: 'tls',
  method: 'register',
  username: 'agent42',
  password: '***',
  domain: 'example.com',
  expected_status: 200,
  tls_skip_verify: true,
  tls_server_name: 'sip.example.com',
  tls_min_days_valid: 14,
  future_field: { kept: true },
} as unknown as SIPMonitorConfig;

function formFrom(overrides: Partial<SipFormFields> = {}): SipFormFields {
  return {
    host: 'sbc.example.com',
    port: 5061,
    transport: 'tls',
    method: 'register',
    username: 'agent42',
    password: '***',
    domain: 'example.com',
    tls_skip_verify: true,
    tls_server_name: 'sip.example.com',
    tls_min_days_valid: '14',
    expected_status: '200',
    resolution: 'a',
    nameserver: '',
    ...sipCallFormFields(undefined),
    ...overrides,
  };
}

test('an unchanged save round-trips the stored SIP config, unknown keys included', () => {
  assert.deepEqual(buildSipConfig(stored, formFrom()), stored);
});

test('a save keeps tls_server_name and keys the form does not render', () => {
  const config = buildSipConfig(stored, formFrom({ host: ' sbc2.example.com ' })) as SIPMonitorConfig & {
    future_field?: unknown;
  };
  assert.equal(config.host, 'sbc2.example.com');
  assert.equal(config.tls_server_name, 'sip.example.com');
  assert.deepEqual(config.future_field, { kept: true });
});

test('form-owned fields the user cleared are dropped, not resurrected from the loaded config', () => {
  const config = buildSipConfig(
    stored,
    formFrom({
      method: 'options',
      domain: '',
      expected_status: '',
      tls_skip_verify: false,
      tls_server_name: '',
      tls_min_days_valid: '',
    }),
  );
  assert.equal(config.method, undefined);
  assert.equal(config.domain, undefined);
  assert.equal(config.expected_status, undefined);
  assert.equal(config.tls_skip_verify, undefined);
  assert.equal(config.tls_server_name, undefined);
  assert.equal(config.tls_min_days_valid, undefined);
});

test('a zero-day certificate-expiry threshold is kept', () => {
  assert.equal(buildSipConfig(stored, formFrom({ tls_min_days_valid: '0' })).tls_min_days_valid, 0);
});

test('a certificate threshold the form would refuse is never saved as a different number', () => {
  for (const typed of ['1e2', '1.5', '-1', '14 days']) {
    const config = buildSipConfig(stored, formFrom({ tls_min_days_valid: typed }));
    assert.equal(config.tls_min_days_valid, undefined, typed);
    assert.equal(parseWhole(typed), null, typed);
  }
  assert.equal(buildSipConfig(stored, formFrom({ tls_min_days_valid: ' 30 ' })).tls_min_days_valid, 30);
});

test('TLS-only fields are dropped when the transport leaves TLS', () => {
  const config = buildSipConfig(stored, formFrom({ transport: 'udp', port: 5060 }));
  assert.equal(config.transport, 'udp');
  assert.equal(config.tls_skip_verify, undefined);
  assert.equal(config.tls_server_name, undefined);
  assert.equal(config.tls_min_days_valid, undefined);
});

test('dropping auth clears a stored password with an empty string', () => {
  const config = buildSipConfig(stored, formFrom({ username: '', password: '***' }));
  assert.equal(config.username, undefined);
  assert.equal(config.password, '');
});

test('the masked password is submitted as is so the server keeps the stored one', () => {
  assert.equal(buildSipConfig(stored, formFrom()).password, '***');
});

test('a new monitor with no loaded config omits the password when auth is off', () => {
  const config = buildSipConfig(undefined, formFrom({ username: '', password: '', transport: 'udp', method: 'options' }));
  assert.equal('password' in config, false);
  assert.equal('username' in config, false);
});

test('locate-via-DNS saves rfc3263 with SRV port and transport left unset', () => {
  const config = buildSipConfig(stored, formFrom({
    host: 'example.com', port: 0, transport: 'auto', resolution: 'rfc3263', nameserver: ' 10.0.0.2:5353 ',
  }));
  assert.equal(config.resolution, 'rfc3263');
  assert.equal(config.port, 0);
  assert.equal(config.transport, '');
  assert.equal(config.nameserver, '10.0.0.2:5353');
  // Auto transport is not TLS, so the TLS-only fields go.
  assert.equal(config.tls_min_days_valid, undefined);
});

test('turning locate-via-DNS off drops resolution and nameserver', () => {
  const dns = { ...stored, resolution: 'rfc3263', nameserver: '10.0.0.2', port: 0 } as SIPMonitorConfig;
  const config = buildSipConfig(dns, formFrom({ port: 5061 }));
  assert.equal(config.resolution, undefined);
  assert.equal(config.nameserver, undefined);
  assert.equal(config.port, 5061);
});

test('an rfc3263 save round-trips', () => {
  const dns = { host: 'example.com', port: 0, transport: '', resolution: 'rfc3263', nameserver: '10.0.0.2' } as SIPMonitorConfig;
  const config = buildSipConfig(dns, formFrom({
    host: 'example.com', port: 0, transport: 'auto', method: 'options', username: '', password: '', domain: '',
    tls_skip_verify: false, tls_server_name: '', tls_min_days_valid: '', expected_status: '',
    resolution: 'rfc3263', nameserver: '10.0.0.2',
  }));
  assert.deepEqual(config, dns);
});

const storedCall = {
  host: 'trunk.example.com',
  port: 5061,
  transport: 'tls',
  method: 'invite',
  username: 'probe',
  password: '***',
  domain: 'example.com',
  call: {
    number: '+33123456789',
    from_display: 'Probara probe',
    mode: 'answer',
    hold_ms: 2000,
    warn_pdd_ms: 3000,
    max_pdd_ms: 5000,
    probe_header: true,
    future_call_field: 'kept',
  },
  future_field: 1,
} as unknown as SIPMonitorConfig;

function callFormFrom(config: SIPMonitorConfig, overrides: Partial<SipFormFields> = {}): SipFormFields {
  return {
    host: config.host,
    port: config.port,
    transport: config.transport as SipFormFields['transport'],
    resolution: 'a',
    nameserver: '',
    method: 'invite',
    username: config.username ?? '',
    password: config.password ?? '',
    domain: config.domain ?? '',
    tls_skip_verify: false,
    tls_server_name: '',
    tls_min_days_valid: '',
    expected_status: '',
    ...sipCallFormFields(config.call),
    ...overrides,
  };
}

test('a test-call save round-trips the stored call, unknown keys included', () => {
  assert.deepEqual(buildSipConfig(storedCall, callFormFrom(storedCall)), storedCall);
});

test('a save keeps tls_server_name and unknown fields of a test call', () => {
  const withSNI = { ...storedCall, tls_server_name: 'sbc.example.com' } as SIPMonitorConfig;
  const config = buildSipConfig(withSNI, callFormFrom(withSNI, { tls_server_name: 'sbc.example.com' }));
  assert.equal(config.tls_server_name, 'sbc.example.com');
  assert.equal((config.call as unknown as { future_call_field: string }).future_call_field, 'kept');
});

test('ring mode drops what the API refuses there', () => {
  const config = buildSipConfig(
    storedCall,
    callFormFrom(storedCall, { call_mode: 'ring', expected_status: '486', call_warn_answer_ms: '8000' }),
  );
  assert.equal(config.call?.mode, 'ring');
  assert.equal(config.call?.hold_ms, undefined);
  assert.equal(config.call?.warn_answer_ms, undefined);
  assert.equal(config.expected_status, undefined);
  // PDD thresholds still apply to ringing.
  assert.equal(config.call?.max_pdd_ms, 5000);
});

test('a target URI replaces the number, and leaving invite drops the call', () => {
  const uri = buildSipConfig(
    storedCall,
    callFormFrom(storedCall, { call_target_kind: 'uri', call_target_uri: ' sips:ivr@pbx.example.com ' }),
  );
  assert.equal(uri.call?.target_uri, 'sips:ivr@pbx.example.com');
  assert.equal(uri.call?.number, undefined);

  const options = buildSipConfig(storedCall, callFormFrom(storedCall, { method: 'options' }));
  assert.equal(options.call, undefined);
  assert.equal(options.method, undefined); // options is the default and is omitted
});

test('a new test call always says its mode', () => {
  const config = buildSipConfig(undefined, callFormFrom(storedCall, { call_number: '1000', call_hold_ms: '' }));
  assert.deepEqual(config.call, {
    number: '1000',
    from_display: 'Probara probe',
    mode: 'answer',
    warn_pdd_ms: 3000,
    max_pdd_ms: 5000,
    probe_header: true,
  });
});

test('isSipCall mirrors the API gate: method invite or a call object, sip only', () => {
  assert.equal(isSipCall('sip', { method: 'invite' }), true);
  assert.equal(isSipCall('sip', { method: ' INVITE ' }), true);
  assert.equal(isSipCall('sip', { method: 'options', call: {} }), true);
  assert.equal(isSipCall('sip', { method: 'options' }), false);
  assert.equal(isSipCall('sip', undefined), false);
  assert.equal(isSipCall('http', { method: 'invite' }), false);
  // Any spelling of the key, as the server's gate reads it.
  assert.equal(isSipCall('sip', { method: 'options', Method: 'invite' }), true);
  assert.equal(isSipCall('sip', { Call: { number: '1' }, call: null }), true);
  assert.equal(isSipCall('sip', { Call: null }), false);
});

test('sipCallTarget names who the call rings', () => {
  assert.equal(sipCallTarget(storedCall), '+33123456789@example.com');
  assert.equal(sipCallTarget({ ...storedCall, domain: undefined } as SIPMonitorConfig), '+33123456789@trunk.example.com');
  assert.equal(
    sipCallTarget({ ...storedCall, call: { target_uri: 'sip:ivr@pbx.example.com' } } as SIPMonitorConfig),
    'sip:ivr@pbx.example.com',
  );
  assert.equal(sipCallTarget({ host: 'x', port: 0, transport: '' }), null);
});

test('sipCallsPerDay counts one call per check per location', () => {
  assert.equal(sipCallsPerDay(60, 0), 1440);
  assert.equal(sipCallsPerDay(60, 3), 4320);
  assert.equal(sipCallsPerDay(300, 1), 288);
});

test('the caller name is counted in code points, like the server', () => {
  const ok = callFormFrom(storedCall);
  assert.equal(validateSipCallFields({ ...ok, call_from_display: '\u{1F4DE}'.repeat(40) }, 60, 10).call_from_display, undefined);
  assert.ok(validateSipCallFields({ ...ok, call_from_display: 'x'.repeat(65) }, 60, 10).call_from_display);
  assert.ok(validateSipCallFields({ ...ok, call_from_display: 'Probe\tone' }, 60, 10).call_from_display);
  assert.ok(validateSipCallFields({ ...ok, call_from_display: 'say "hi"' }, 60, 10).call_from_display);
});

test('validateSipCallFields applies the test-call rules', () => {
  const ok = callFormFrom(storedCall);
  assert.deepEqual(validateSipCallFields(ok, 60, 10), {});
  assert.deepEqual(validateSipCallFields({ ...ok, method: 'options', call_number: '' }, 30, 30), {});

  const errors = validateSipCallFields(
    { ...ok, call_number: 'bad number', call_warn_pdd_ms: '5000', call_max_pdd_ms: '5000', expected_status: '180' },
    30,
    25,
  );
  assert.ok(errors.call_number);
  assert.ok(errors.call_warn_pdd_ms);
  assert.ok(errors.expected_status);
  assert.ok(errors.interval_seconds);
  assert.ok(errors.timeout_seconds);

  // The hold plus 2 s to ring and answer must fit in the timeout.
  assert.ok(validateSipCallFields({ ...ok, call_hold_ms: '9000' }, 60, 10).call_hold_ms);
  assert.deepEqual(validateSipCallFields({ ...ok, call_hold_ms: '8000' }, 60, 10), {});
  // Ring mode ignores the hold and the expected status.
  assert.deepEqual(validateSipCallFields({ ...ok, call_mode: 'ring', call_hold_ms: '9000', expected_status: '180' }, 60, 10), {});

  const uri = { ...ok, call_target_kind: 'uri' as const, call_target_uri: 'sips:ivr@pbx.example.com' };
  assert.deepEqual(validateSipCallFields(uri, 60, 10), {});
  assert.ok(validateSipCallFields({ ...uri, transport: 'udp' }, 60, 10).call_target_uri);
  assert.ok(validateSipCallFields({ ...uri, call_target_uri: 'tel:+331' }, 60, 10).call_target_uri);
});

test('target URIs follow the server rules', () => {
  for (const uri of [
    'sip:ivr@pbx.example.com;transport=tcp',
    'sip:%2B331@carrier.example.com:5070;user=phone',
    'sip:ivr%23@pbx.example.com',
    'sips:100@[2001:db8::1]:5061',
  ]) {
    assert.equal(sipCallURIError(uri), null, uri);
  }
  for (const [uri, fragment] of [
    ['sip:u@h?x=y', '?headers'],
    ['sip:u@pbx.example.com;maddr=1.2.3.4', 'maddr'],
    ['sip:1;phone-context=+44@carrier.example.com', ';parameters'],
    ['sip:u:pw@pbx.example.com', ':password'],
    ['sip:u@pbx.example.com:70000', 'Port'],
    ['sip:u@a@b.example.com', 'single @'],
    ['sip:ivr#@pbx.example.com', 'User part'],
    ['sip:' + 'a'.repeat(510) + '@h', '512 bytes'],
    ['tel:+331', 'sip: or sips:'],
    ['sip:u@bad_host', 'hostname'],
  ] as const) {
    assert.ok(sipCallURIError(uri)?.includes(fragment), `${uri}: ${sipCallURIError(uri)}`);
  }
});

test('host fields of a test call must be bare hosts', () => {
  assert.ok(isHostOrIP('carrier.example.com'));
  assert.ok(isHostOrIP('203.0.113.10'));
  assert.ok(isHostOrIP('2001:db8::1'));
  assert.ok(!isHostOrIP('a b'));
  assert.ok(!isHostOrIP('203.0.113.10:4000'));
  assert.ok(!isHostOrIP('[2001:db8::1]'));

  const ok = callFormFrom(storedCall);
  const errors = validateSipCallFields(
    { ...ok, domain: 'carrier.example.com;maddr=203.0.113.9', call_from_domain: 'a b', call_sdp_address: '203.0.113.10:4000' },
    60,
    10,
  );
  assert.ok(errors.domain);
  assert.ok(errors.call_from_domain);
  assert.ok(errors.call_sdp_address);
  // OPTIONS and REGISTER keep their looser domain.
  assert.equal(validateSipCallFields({ ...ok, method: 'register', domain: 'example.com:5060' }, 60, 10).domain, undefined);
});

const storedMediaCall = {
  ...storedCall,
  call: {
    ...storedCall.call,
    hold_ms: 6000,
    media: { expect: 'two_way', max_loss_pct: 2.5, warn_jitter_ms: 30, min_mos: 3.6, warn_mos: 4, future_media_field: true },
  },
} as unknown as SIPMonitorConfig;

test('a call-quality save round-trips the stored media, unknown media keys included', () => {
  // Kept, not dropped: the API refuses an unknown media key, which is the
  // fail-closed intent, rather than the form silently losing an assertion.
  assert.deepEqual(buildSipConfig(storedMediaCall, callFormFrom(storedMediaCall)), storedMediaCall);
});

test('the call-quality form fields: off without media, Expect preselecting audio', () => {
  const blank = sipCallFormFields(undefined);
  assert.equal(blank.call_media, false);
  assert.equal(blank.call_media_expect, 'audio');
  assert.equal(blank.call_media_min_mos, '');

  const loaded = sipCallFormFields(storedMediaCall.call);
  assert.equal(loaded.call_media, true);
  assert.equal(loaded.call_media_expect, 'two_way');
  assert.equal(loaded.call_media_max_loss_pct, '2.5');
  assert.equal(loaded.call_media_warn_jitter_ms, '30');
  assert.equal(loaded.call_media_warn_mos, '4');
  assert.equal(loaded.call_media_warn_rtt_ms, '');

  // An expect this release does not know still shows the section; Expect
  // falls back to the preselection.
  const unknown = sipCallFormFields({ mode: 'answer', media: { expect: 'dtmf' } } as unknown as SIPMonitorConfig['call']);
  assert.equal(unknown.call_media, true);
  assert.equal(unknown.call_media_expect, 'audio');
});

test('a new measured call submits every threshold, parsed as the API decodes them', () => {
  const config = buildSipConfig(
    undefined,
    callFormFrom(storedCall, {
      call_number: '1000',
      call_hold_ms: '5000',
      call_media: true,
      call_media_expect: 'audio',
      call_media_warn_loss_pct: ' 1 ',
      call_media_max_loss_pct: '5.5',
      call_media_warn_jitter_ms: '20',
      call_media_max_jitter_ms: '50',
      call_media_warn_mos: '4.1',
      call_media_min_mos: '3.6',
      call_media_warn_rtt_ms: '250',
      call_media_max_rtt_ms: '400',
    }),
  );
  assert.deepEqual(config.call, {
    number: '1000',
    from_display: 'Probara probe',
    mode: 'answer',
    hold_ms: 5000,
    warn_pdd_ms: 3000,
    max_pdd_ms: 5000,
    probe_header: true,
    media: {
      expect: 'audio',
      warn_loss_pct: 1,
      max_loss_pct: 5.5,
      warn_jitter_ms: 20,
      max_jitter_ms: 50,
      warn_mos: 4.1,
      min_mos: 3.6,
      warn_rtt_ms: 250,
      max_rtt_ms: 400,
    },
  });
});

test('call quality is dropped when turned off or in ring mode, and Measure only carries no thresholds', () => {
  const off = buildSipConfig(storedMediaCall, callFormFrom(storedMediaCall, { call_media: false }));
  assert.equal(off.call?.media, undefined);

  const ring = buildSipConfig(storedMediaCall, callFormFrom(storedMediaCall, { call_mode: 'ring' }));
  assert.equal(ring.call?.media, undefined);

  const none = buildSipConfig(storedMediaCall, callFormFrom(storedMediaCall, { call_media_expect: 'none' }));
  assert.deepEqual(none.call?.media, { future_media_field: true, expect: 'none' });

  // A cleared threshold is dropped, not resurrected from the loaded media.
  const cleared = buildSipConfig(storedMediaCall, callFormFrom(storedMediaCall, { call_media_min_mos: '' }));
  assert.equal(cleared.call?.media?.min_mos, undefined);
  assert.equal(cleared.call?.media?.warn_mos, 4);
});

test('a measured call needs a 3 s hold, 6 s for two-way audio, and an answered call', () => {
  const measured = callFormFrom(storedCall, { call_media: true, call_media_expect: 'audio', call_hold_ms: '3000' });
  assert.deepEqual(validateSipCallFields(measured, 60, 10), {});
  assert.ok(validateSipCallFields({ ...measured, call_hold_ms: '2999' }, 60, 10).call_hold_ms?.includes('3000'));
  assert.ok(validateSipCallFields({ ...measured, call_hold_ms: '' }, 60, 10).call_hold_ms);
  // The hold still has to fit in the timeout.
  assert.ok(validateSipCallFields(measured, 60, 4).call_hold_ms?.includes('timeout'));

  const twoWay = { ...measured, call_media_expect: 'two_way' as const };
  assert.ok(validateSipCallFields(twoWay, 60, 10).call_hold_ms?.includes('6000'));
  assert.deepEqual(validateSipCallFields({ ...twoWay, call_hold_ms: '6000' }, 60, 10), {});
  assert.equal(sipMediaMinHoldMs('two_way'), 6000);
  assert.equal(sipMediaMinHoldMs('echo'), 3000);
  assert.equal(sipMediaMinHoldMs('none'), 3000);

  assert.ok(validateSipCallFields({ ...measured, expected_status: '486' }, 60, 10).expected_status);
  assert.deepEqual(validateSipCallFields({ ...measured, expected_status: '200' }, 60, 10), {});
  // Ring mode and an unmeasured call keep their own rules.
  assert.deepEqual(validateSipCallFields({ ...measured, call_mode: 'ring', call_hold_ms: '' }, 60, 10), {});
  assert.deepEqual(
    validateSipCallFields({ ...measured, call_media: false, call_hold_ms: '0', expected_status: '486' }, 60, 10),
    {},
  );
});

test('call-quality thresholds follow the server ranges and orders', () => {
  const measured = callFormFrom(storedCall, { call_media: true, call_media_expect: 'echo', call_hold_ms: '5000' });
  const errors = validateSipCallFields(
    {
      ...measured,
      call_media_warn_loss_pct: '0',
      call_media_max_loss_pct: '100',
      call_media_warn_jitter_ms: '1.5',
      call_media_max_jitter_ms: '1001',
      call_media_warn_mos: '4.4',
      call_media_min_mos: '0.9',
      call_media_warn_rtt_ms: '0',
      call_media_max_rtt_ms: '5001',
    },
    60,
    10,
  );
  for (const key of [
    'call_media_warn_loss_pct',
    'call_media_max_loss_pct',
    'call_media_warn_jitter_ms',
    'call_media_max_jitter_ms',
    'call_media_warn_mos',
    'call_media_min_mos',
    'call_media_warn_rtt_ms',
    'call_media_max_rtt_ms',
  ]) {
    assert.ok(errors[key], key);
  }

  const order = validateSipCallFields(
    {
      ...measured,
      call_media_warn_loss_pct: '2',
      call_media_max_loss_pct: '2',
      call_media_warn_jitter_ms: '40',
      call_media_max_jitter_ms: '30',
      call_media_warn_mos: '3.6',
      call_media_min_mos: '3.6',
      call_media_warn_rtt_ms: '300',
      call_media_max_rtt_ms: '300',
    },
    60,
    10,
  );
  assert.deepEqual(Object.keys(order).sort(), [
    'call_media_warn_jitter_ms',
    'call_media_warn_loss_pct',
    'call_media_warn_mos',
    'call_media_warn_rtt_ms',
  ]);
  assert.ok(order.call_media_warn_mos.includes('above'));

  const good = {
    ...measured,
    call_media_warn_loss_pct: '0.5',
    call_media_max_loss_pct: '99.9',
    call_media_warn_mos: '4.3',
    call_media_min_mos: '1',
    call_media_warn_rtt_ms: '1',
    call_media_max_rtt_ms: '5000',
  };
  assert.deepEqual(validateSipCallFields(good, 60, 10), {});
  // Measure only hides and drops the thresholds, so they are not checked.
  assert.deepEqual(validateSipCallFields({ ...good, call_media_expect: 'none', call_media_min_mos: '9' }, 60, 10), {});
});

test('parseDecimal reads plain decimals only', () => {
  assert.equal(parseDecimal(' 2.5 '), 2.5);
  assert.equal(parseDecimal('4'), 4);
  for (const typed of ['', '-1', '1e2', '.5', '1.', '2 %']) assert.equal(parseDecimal(typed), null, typed);
});
