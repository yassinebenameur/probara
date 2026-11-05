import assert from 'node:assert/strict';
import test from 'node:test';
import { buildTcpRequest, initialTcpFormState } from './monitor-form/network';
import { storedMonitor } from './monitor-form/testing';
import { certificateExpiryLabel } from './sip-metrics';
import { effectiveCertExpiry } from './tls-cert';
import type { TLSCertInfo } from './types';

test('tcp: a new TLS monitor checks the chain with its expiry threshold', () => {
  const form = {
    ...initialTcpFormState(),
    name: 'SMTPS',
    host: 'mail.example.com',
    port: 465,
    use_tls: true,
    tls_min_days_valid: '14',
    tls_server_name: ' smtp.example.com ',
    tls_ca_pem: ' -----BEGIN CERTIFICATE-----\nx\n-----END CERTIFICATE----- ',
  };
  assert.equal(initialTcpFormState().tls_check_chain, true);
  assert.deepEqual(buildTcpRequest(form).config, {
    host: 'mail.example.com',
    port: 465,
    use_tls: true,
    tls_server_name: 'smtp.example.com',
    tls_ca_pem: '-----BEGIN CERTIFICATE-----\nx\n-----END CERTIFICATE-----',
    tls_min_days_valid: 14,
    tls_check_chain: true,
  });
  // Without a threshold the chain flag has nothing to measure; without TLS
  // none of the certificate options apply.
  assert.deepEqual(buildTcpRequest({ ...form, tls_min_days_valid: '' }).config, {
    host: 'mail.example.com',
    port: 465,
    use_tls: true,
    tls_server_name: 'smtp.example.com',
    tls_ca_pem: '-----BEGIN CERTIFICATE-----\nx\n-----END CERTIFICATE-----',
  });
  assert.deepEqual(buildTcpRequest({ ...form, use_tls: false }).config, { host: 'mail.example.com', port: 465, use_tls: false });
});

test('tcp: a stored monitor keeps its chain choice', () => {
  const config = { host: 'mail.example.com', port: 465, use_tls: true, tls_min_days_valid: 7 };
  const leafOnly = initialTcpFormState(storedMonitor('tcp', config));
  assert.equal(leafOnly.tls_check_chain, false);
  assert.deepEqual(buildTcpRequest(leafOnly).config, config);
  const chain = { ...config, tls_check_chain: true, tls_server_name: 'smtp.example.com' };
  assert.deepEqual(buildTcpRequest(initialTcpFormState(storedMonitor('tcp', chain))).config, chain);
});

test('certificate expiry follows the chain opt-in', () => {
  const tls: TLSCertInfo = {
    not_after: '2027-03-01T00:00:00Z',
    days_until_expiry: 90,
    chain_not_after: '2026-12-01T00:00:00Z',
    chain_min_days: 20,
  };
  assert.deepEqual(effectiveCertExpiry(tls), { notAfter: '2027-03-01T00:00:00Z', days: 90, chain: false });
  assert.deepEqual(effectiveCertExpiry(tls, true), { notAfter: '2026-12-01T00:00:00Z', days: 20, chain: true });
  assert.deepEqual(effectiveCertExpiry({ not_after: '2027-03-01T00:00:00Z', days_until_expiry: 90 }, true), {
    notAfter: '2027-03-01T00:00:00Z',
    days: 90,
    chain: false,
  });
  assert.match(certificateExpiryLabel(tls, true), /^chain expires in 20 days/);
  assert.match(certificateExpiryLabel(tls), /^expires in 90 days/);
  // The leaf setting the chain's date reads as the certificate's own.
  assert.match(certificateExpiryLabel({ ...tls, chain_not_after: tls.not_after, chain_min_days: 90 }, true), /^expires in 90 days/);
});
