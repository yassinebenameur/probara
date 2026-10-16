import assert from 'node:assert/strict';
import test from 'node:test';
import {
  certificateExpiryLabel,
  latestSIPMetrics,
  sipAddressLabel,
  sipCallFacts,
  sipCallSummary,
  sipFacts,
  sipResponseLabel,
  sipTeardownLabel,
} from './sip-metrics';
import { formatDate } from './format';
import type { CheckResult, SIPCallMetrics, SIPMetrics } from './types';

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

test('an SRV failover shows the target, the failover and the path tried', () => {
  const m: SIPMetrics = {
    ...base,
    transport: 'tls',
    target: { host: 'sbc2.example.com', address: '203.0.113.7:5061', resolved_via: 'srv', failover: true },
    resolution: {
      mode: 'rfc3263',
      srv: [
        { target: 'sbc1.example.com', port: 5061, priority: 10, weight: 50 },
        { target: 'sbc2.example.com', port: 5061, priority: 20, weight: 0 },
      ],
      attempts: [
        { address: '203.0.113.6:5061', transport: 'tls', outcome: 'refused', ms: 3 },
        { address: '203.0.113.7:5061', transport: 'tls', outcome: 'ok', ms: 40 },
      ],
    },
    timing: { dns_ms: 4, connect_ms: 12, total_ms: 60 },
  };
  assert.equal(sipAddressLabel(m), '203.0.113.7:5061 (sbc2.example.com via SRV, after failover)');
  const facts = sipFacts(m);
  assert.equal(facts.find((f) => f.key === 'attempts')?.value, '203.0.113.6:5061 refused → 203.0.113.7:5061 ok');
  assert.equal(facts.find((f) => f.key === 'timing')?.value, 'DNS 4 ms, connect 12 ms');
});

test('a target found through NAPTR says so', () => {
  const m: SIPMetrics = {
    ...base,
    transport: 'tls',
    target: { host: 'sbc1.example.com', address: '203.0.113.6:5061', resolved_via: 'naptr' },
    resolution: {
      mode: 'rfc3263',
      naptr_status: 'ok',
      naptr: [{ order: 10, pref: 10, service: 'SIPS+D2T', replacement: '_sips._tcp.example.com' }],
      srv: [{ target: 'sbc1.example.com', port: 5061, priority: 10, weight: 0 }],
    },
    timing: { total_ms: 30 },
  };
  assert.equal(sipAddressLabel(m), '203.0.113.6:5061 (sbc1.example.com via NAPTR→SRV)');
});

test('a single attempt lists no path, and an undecided transport is not shown', () => {
  const m: SIPMetrics = {
    ...base,
    transport: undefined,
    resolution: { mode: 'rfc3263' },
    timing: { dns_ms: 20, total_ms: 25 },
  };
  const facts = sipFacts(m);
  assert.equal(facts.find((f) => f.key === 'attempts'), undefined);
  assert.equal(facts.find((f) => f.key === 'transport'), undefined);
  assert.equal(sipAddressLabel(m), '');
});

const answeredCall: SIPCallMetrics = {
  mode: 'answer',
  trying_ms: 20,
  pdd_ms: 850,
  pdd_source: '180',
  answer_ms: 4200,
  hold_ms: 2000,
  bye_ms: 35,
  early_media: true,
  answered: true,
  forked_answers: 0,
  route_hops: 1,
  require_100rel: false,
  sdp_answer: { codec: 'PCMA/8000', payload_type: 8, remote_media: '198.51.100.7:40012', direction: 'sendrecv' },
  rtp_rx_packets: 0,
  teardown: 'bye_ok',
  teardown_status: 200,
};

test('a test call shows PDD, answer time, media and teardown', () => {
  assert.deepEqual(
    sipCallFacts(answeredCall).map((f) => [f.label, f.value, f.tone]),
    [
      ['Post-dial delay', '850 ms (180)', undefined],
      ['Answered in', '4.2 s, held 2.0 s', undefined],
      ['Media', 'PCMA/8000 · early media', undefined],
      ['Teardown', 'BYE confirmed in 35 ms', undefined],
    ]
  );
});

test('an unconfirmed teardown is one warning row, not two', () => {
  const facts = sipFacts({
    ...base,
    method: 'INVITE',
    call: { ...answeredCall, teardown: 'bye_unconfirmed', bye_ms: undefined },
    warnings: ['pdd: 3400ms > 3000ms (warn)', 'teardown: BYE unconfirmed'],
    latency_warn_ms: 3000,
  });
  const warnings = facts.filter((f) => f.tone === 'warning').map((f) => [f.label, f.value]);
  assert.deepEqual(warnings, [
    ['Teardown', 'BYE unconfirmed: the far end may still hold the call'],
    ['Warning', 'pdd: 3400ms > 3000ms (warn)'],
  ]);
});

test('latency_warn_ms reads as a warning when no warnings came with it', () => {
  const facts = sipFacts({ ...base, latency_warn_ms: 3000 });
  assert.equal(facts.find((f) => f.key === 'latency-warn')?.value, 'over the 3.0 s warning threshold');
});

test('ring mode and the teardown wording', () => {
  const ringing: SIPCallMetrics = {
    ...answeredCall,
    mode: 'ring',
    answer_ms: undefined,
    hold_ms: 0,
    answered: false,
    teardown: 'cancelled',
    sdp_answer: undefined,
    early_media: false,
  };
  assert.deepEqual(
    sipCallFacts(ringing).map((f) => f.value),
    ['850 ms (180)', 'rang, cancelled before answer (ring mode)', 'CANCEL confirmed (487)']
  );
  assert.equal(sipTeardownLabel({ ...answeredCall, teardown: 'bye_503' }), 'BYE answered 503');
  assert.equal(sipTeardownLabel({ ...answeredCall, teardown: 'none' }), '');
  assert.equal(sipTeardownLabel({ ...answeredCall, teardown: 'remote_bye' }), 'the callee hung up first');
});

test('sipCallSummary for a Test result', () => {
  assert.equal(sipCallSummary(answeredCall), 'ringing after 850 ms, answered in 4.2 s');
  assert.equal(sipCallSummary({ ...answeredCall, pdd_ms: 4200, pdd_source: '200' }), 'answered in 4.2 s');
  assert.equal(sipCallSummary(undefined), '');
});
