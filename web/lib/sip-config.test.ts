import assert from 'node:assert/strict';
import test from 'node:test';
import { buildSipConfig, SipFormFields } from './sip-config';
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
