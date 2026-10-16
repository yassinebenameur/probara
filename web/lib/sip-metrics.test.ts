import assert from 'node:assert/strict';
import test from 'node:test';
import { certificateExpiryLabel, latestSIPMetrics, sipFacts, sipResponseLabel } from './sip-metrics';
import { formatDate } from './format';
import type { CheckResult, SIPMetrics } from './types';

const base: SIPMetrics = {
  method: 'OPTIONS',
  transport: 'udp',
  timing: { total_ms: 40 },
  retransmissions: 0,
  stray_messages: 0,
};

const result = (id: string, metrics_data?: CheckResult['metrics_data']): CheckResult => ({
  id,
  status: 'success',
  created_at: '2026-10-16T10:00:00Z',
  metrics_data,
});

test('latestSIPMetrics skips results without a SIP envelope', () => {
  const sip = { ...base, call_id: 'abc@monitor.local' };
  assert.equal(latestSIPMetrics([result('1'), result('2', { http: {} }), result('3', { sip })]), sip);
  assert.equal(latestSIPMetrics([result('1')]), null);
});

test('sipResponseLabel adds the Reason header cause', () => {
  assert.equal(sipResponseLabel(base), '');
  assert.equal(sipResponseLabel({ ...base, status_code: 200, reason: 'OK' }), '200 OK');
  assert.equal(
    sipResponseLabel({
      ...base,
      status_code: 486,
      reason: 'Busy Here',
      reason_header: { protocol: 'Q.850', cause: 17, text: 'User busy' },
    }),
    '486 Busy Here (Q.850 17 User busy)'
  );
});

test('sipFacts lists what the check learned, in reading order', () => {
  const facts = sipFacts({
    ...base,
    transport: 'tls',
    call_id: 'abc@monitor.local',
    status_code: 200,
    reason: 'OK',
    server: 'Example SBC',
    warning: '399 sbc "degraded"',
    target: { host: 'sbc.example.com', address: '203.0.113.7:5061', resolved_via: 'a' },
    timing: { connect_ms: 12, tls_handshake_ms: 30, response_ms: 41, total_ms: 95 },
    retransmissions: 2,
    stray_messages: 1,
    auth: { challenge: 407, algorithm: 'SHA-256', rounds: 1, auth_ms: 20 },
  });
  assert.deepEqual(
    facts.map((f) => [f.label, f.value]),
    [
      ['Answered by', 'Example SBC'],
      ['Address', '203.0.113.7:5061'],
      ['Transport', 'TLS'],
      ['Response', 'OPTIONS 200 OK'],
      ['Timing', 'connect 12 ms, TLS 30 ms, response 41 ms'],
      ['Auth', '407 digest SHA-256'],
      ['Retransmits', '2 resends'],
      ['Ignored', '1 unmatched response'],
      ['SIP warning', '399 sbc "degraded"'],
      ['Call-ID', 'abc@monitor.local'],
    ]
  );
  assert.equal(facts.find((f) => f.key === 'call-id')?.copyable, true);
});

test('sipFacts on a check that never got an answer', () => {
  const facts = sipFacts({ ...base, auth: { challenge: 401, rounds: 0 } });
  assert.deepEqual(
    facts.map((f) => [f.label, f.value]),
    [
      ['Transport', 'UDP'],
      ['Auth', '401 challenge, not answered'],
    ]
  );
});

test('sipFacts shows the TLS version and the certificate expiry', () => {
  const tls = { version: 'TLS 1.3', not_after: '2026-10-28T00:00:00Z', days_until_expiry: 12 };
  const facts = sipFacts({ ...base, transport: 'tls', tls });
  assert.equal(facts.find((f) => f.key === 'transport')?.value, 'TLS · TLS 1.3');
  const cert = facts.find((f) => f.key === 'certificate');
  assert.equal(cert?.value, `expires in 12 days (${formatDate('2026-10-28T00:00:00Z')})`);
  assert.equal(cert?.tone, undefined);

  // Inside the monitor's tls_min_days_valid window it reads as a warning,
  // like the tls_expiry alert it opens.
  assert.equal(sipFacts({ ...base, transport: 'tls', tls }, 14).find((f) => f.key === 'certificate')?.tone, 'warning');
  assert.equal(sipFacts({ ...base, transport: 'tls', tls }, 7).find((f) => f.key === 'certificate')?.tone, undefined);
});

test('certificateExpiryLabel', () => {
  assert.equal(certificateExpiryLabel({}), '');
  assert.equal(certificateExpiryLabel({ days_until_expiry: 1 }), 'expires in 1 day');
  assert.equal(certificateExpiryLabel({ days_until_expiry: -3 }), 'expired 3 days ago');
});
