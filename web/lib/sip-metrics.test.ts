import assert from 'node:assert/strict';
import test from 'node:test';
import {
  certificateExpiryLabel,
  latestSIPMetrics,
  pickSIPCall,
  sipAddressLabel,
  sipCallFacts,
  sipCallSeries,
  sipCallSummary,
  sipTestWarnings,
  sipCallThresholdLines,
  sipFacts,
  sipMediaFacts,
  sipMediaJudged,
  sipMediaOutdatedWorkersHint,
  sipMediaSeries,
  sipMediaUnsupportedHint,
  sipMediaWorst,
  sipResponseLabel,
  sipTeardownLabel,
} from './sip-metrics';
import { formatDate, formatMOS } from './format';
import type { CheckResult, SIPCallMetrics, SIPMediaMetrics, SIPMetrics } from './types';

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

test('only the teardown warning the Teardown row shows is hidden', () => {
  const facts = sipFacts({
    ...base,
    method: 'INVITE',
    call: { ...answeredCall, teardown: 'cancel_unconfirmed', bye_ms: undefined },
    warnings: ['teardown: CANCEL unconfirmed', 'teardown: BYE unconfirmed'],
  });
  const warnings = facts.filter((f) => f.tone === 'warning').map((f) => [f.label, f.value]);
  assert.equal(warnings.filter(([label]) => label === 'Teardown').length, 1);
  assert.deepEqual(
    warnings.filter(([label]) => label === 'Warning'),
    [['Warning', 'teardown: BYE unconfirmed']],
  );
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

test('sipTestWarnings keeps what a green Passed would hide', () => {
  assert.deepEqual(sipTestWarnings(undefined), []);
  assert.deepEqual(sipTestWarnings({ ...base }), []);
  assert.deepEqual(
    sipTestWarnings({ ...base, warnings: ['answered in ring mode — billable', 'teardown: BYE unconfirmed'] }),
    ['answered in ring mode — billable', 'teardown: BYE unconfirmed'],
  );
  assert.deepEqual(sipTestWarnings({ ...base, latency_warn_ms: 3000 }), ['over the 3.0 s warning threshold']);
});

test('sipCallSummary for a Test result', () => {
  assert.equal(sipCallSummary(answeredCall), 'ringing after 850 ms, answered in 4.2 s');
  assert.equal(sipCallSummary({ ...answeredCall, pdd_ms: 4200, pdd_source: '200' }), 'answered in 4.2 s');
  assert.equal(sipCallSummary(undefined), '');
});

const callMetrics = (over: Partial<SIPCallMetrics>): SIPCallMetrics => ({
  mode: 'answer',
  hold_ms: 0,
  early_media: false,
  answered: false,
  forked_answers: 0,
  route_hops: 0,
  require_100rel: false,
  rtp_rx_packets: 0,
  teardown: 'none',
  ...over,
});

test('pickSIPCall reads metrics_data.sip.call only', () => {
  const call = callMetrics({ pdd_ms: 850 });
  assert.equal(pickSIPCall({ sip: { ...base, method: 'INVITE', call } }), call);
  assert.equal(pickSIPCall({ sip: base }), undefined);
  assert.equal(pickSIPCall({ mongodb: {} }), undefined);
  assert.equal(pickSIPCall(null), undefined);
});

test('sipCallSeries plots PDD and answer time, leaving gaps for calls that stopped early', () => {
  const series = sipCallSeries([
    { ts: 1, m: callMetrics({ pdd_ms: 850, pdd_source: '180', setup_pdd_ms: 850, answer_ms: 4200, setup_ms: 4200 }) },
    // Rang, never answered: a PDD point, no answer point.
    { ts: 2, m: callMetrics({ pdd_ms: 900, pdd_source: '180' }) },
    // No response at all: no point anywhere.
    { ts: 3, m: callMetrics({ teardown: 'no_provisional' }) },
    // Answered without ringing: PDD is the answer time (source 200).
    { ts: 4, m: callMetrics({ pdd_ms: 300, pdd_source: '200', answer_ms: 300 }) },
  ]);
  assert.deepEqual(series.pdd, [
    [1, 850],
    [2, 900],
    [4, 300],
  ]);
  assert.deepEqual(series.answer, [
    [1, 4200],
    [4, 300],
  ]);
  // Never challenged: the from-first-INVITE lines would duplicate, so none.
  assert.deepEqual(series.setupPdd, []);
  assert.deepEqual(series.setup, []);
});

test('sipCallSeries adds the from-first-INVITE lines once a call was challenged', () => {
  const series = sipCallSeries([
    { ts: 1, m: callMetrics({ pdd_ms: 800, answer_ms: 4000 }) },
    { ts: 2, m: callMetrics({ pdd_ms: 850, setup_pdd_ms: 910, answer_ms: 4200, setup_ms: 4260 }) },
  ]);
  // Unchallenged checks plot their own value on the setup line.
  assert.deepEqual(series.setupPdd, [
    [1, 800],
    [2, 910],
  ]);
  assert.deepEqual(series.setup, [
    [1, 4000],
    [2, 4260],
  ]);
});

test('sipCallThresholdLines labels warn and fail lines', () => {
  assert.deepEqual(sipCallThresholdLines(3000, 5000), [
    { y: 3000, label: 'warn 3.0 s' },
    { y: 5000, label: 'fail 5.0 s' },
  ]);
  assert.deepEqual(sipCallThresholdLines(undefined, 800), [{ y: 800, label: 'fail 800 ms' }]);
  assert.deepEqual(sipCallThresholdLines(), []);
});

// --- call quality -------------------------------------------------------------

const quality = (mos: number, delay_source: 'echo' | 'rtcp' | 'unmeasured' = 'rtcp') => ({
  r_factor: 89.2,
  mos_cqe: mos,
  delay_ms: 99,
  delay_source,
});

const measuredMedia: SIPMediaMetrics = {
  expect: 'two_way',
  status: 'measured',
  codec: 'PCMU/8000',
  ptime_ms: 20,
  direction: 'sendrecv',
  destination: '198.51.100.7:40012',
  window_ms: 5500,
  warmup_ms: 500,
  tx: { packets: 300, rtcp: 2, skipped_slots: 0, timer_p95_ms: 0.4, reliable: true },
  rx: { packets: 301, invalid: 0, foreign: 0, overflow: 0, unknown_pt: 0, after_bye_packets: 0 },
  inbound: {
    ssrc: '9f00aa01',
    ssrc_changes: 0,
    expected: 275,
    lost: 1,
    loss_pct: 0.36,
    cumulative_lost: 1,
    duplicates: 0,
    reordered: 0,
    late: 1,
    effective_loss_pct: 0.73,
    burst_r: 1,
    jitter_ms: 2.14,
    jitter_max_ms: 4.8,
    jitter_last_ms: 2,
    max_gap_ms: 41,
    stall_ms: 18,
    comfort_noise: 0,
    telephone_events: 0,
    quality: quality(4.31),
  },
  outbound: {
    reports: 2,
    expected: 250,
    loss_pct: 0,
    cumulative_lost: 0,
    jitter_ms: 1.3,
    rtt_ms: 41.2,
    quality: { ...quality(4.36), burst_r: 1, burst_r_source: 'assumed', discards: 'not_reported' },
  },
  model: 'G.107 E-model (R0-Is 93.2, Cole-Rosenbluth Id, G.711 Ie=0 Bpl=25.1, 60 ms reference jitter buffer); MOS-CQE estimate',
  assumptions: ['g711_plc'],
  two_way: 'verified',
  two_way_evidence: ['rx', 'rtcp'],
};

test('sipMediaFacts reads every judged basis, the round trip and two-way evidence', () => {
  const facts = sipMediaFacts(measuredMedia);
  assert.deepEqual(
    facts.map((f) => [f.label, f.value, f.tone]),
    [
      ['Call quality', 'MOS 4.31 in · 4.36 out (estimate)', undefined],
      ['Loss', '0.73% in · 0% out', undefined],
      ['Jitter', '2.1 ms in (max 4.8 ms) · 1.3 ms out', undefined],
      ['Round trip', '41 ms (RTCP)', undefined],
      ['Two-way audio', 'verified (RTCP)', undefined],
    ]
  );
  // The model and its assumption ride along as the MOS row's info tip.
  assert.ok(facts[0].tip?.includes('G.107 E-model'));
  assert.ok(facts[0].tip?.includes('conceals lost packets'));
});

test('sipMediaFacts tones MOS and loss by the monitor thresholds, else the default bands', () => {
  const poor: SIPMediaMetrics = {
    ...measuredMedia,
    inbound: { ...measuredMedia.inbound!, effective_loss_pct: 3.2, quality: quality(3.9, 'unmeasured') },
  };
  const byDefault = sipMediaFacts(poor);
  assert.equal(byDefault.find((f) => f.key === 'media-mos')?.tone, 'warning');
  assert.equal(byDefault.find((f) => f.key === 'media-mos')?.value, 'MOS 3.90 in · 4.36 out (estimate, delay unmeasured)');
  assert.equal(byDefault.find((f) => f.key === 'media-loss')?.tone, 'warning');
  // The monitor's own thresholds are looser: nothing to flag.
  const own = sipMediaFacts(poor, { expect: 'two_way', warn_mos: 3.8, max_loss_pct: 5 });
  assert.equal(own.find((f) => f.key === 'media-mos')?.tone, undefined);
  assert.equal(own.find((f) => f.key === 'media-loss')?.tone, undefined);
});

test('sipMediaFacts tones jitter and the round trip by the monitor thresholds only', () => {
  const jittery: SIPMediaMetrics = { ...measuredMedia, inbound: { ...measuredMedia.inbound!, jitter_ms: 34 } };
  // No jitter or RTT threshold: nothing to flag, whatever the figure.
  assert.equal(sipMediaFacts(jittery).find((f) => f.key === 'media-jitter')?.tone, undefined);
  const facts = sipMediaFacts(jittery, { expect: 'two_way', max_jitter_ms: 30, warn_rtt_ms: 40 });
  assert.equal(facts.find((f) => f.key === 'media-jitter')?.tone, 'warning');
  assert.equal(facts.find((f) => f.key === 'media-rtt')?.tone, 'warning'); // 41.2 ms RTCP > 40
  const loose = sipMediaFacts(jittery, { expect: 'two_way', max_jitter_ms: 50, max_rtt_ms: 500 });
  assert.equal(loose.find((f) => f.key === 'media-jitter')?.tone, undefined);
  assert.equal(loose.find((f) => f.key === 'media-rtt')?.tone, undefined);
});

test('a run the worker was too busy to time is shown but not judged', () => {
  const busy: SIPMediaMetrics = {
    ...measuredMedia,
    tx: { ...measuredMedia.tx!, reliable: false, timer_p95_ms: 9.1 },
    inbound: { ...measuredMedia.inbound!, effective_loss_pct: 6, jitter_ms: 40, quality: quality(3.4) },
  };
  assert.equal(sipMediaJudged(busy), false);
  assert.equal(sipMediaJudged(measuredMedia), true);
  const facts = sipMediaFacts(busy, { expect: 'two_way', max_jitter_ms: 30, max_rtt_ms: 10 });
  assert.deepEqual(
    facts.filter((f) => f.key !== 'media-two-way').map((f) => [f.label, f.value, f.tone]),
    [
      ['Call quality', 'MOS 3.40 in · 4.36 out (estimate, not judged: the worker was too busy to time media)', 'note'],
      ['Loss', '6.00% in · 0% out', undefined],
      ['Jitter', '40 ms in (max 4.8 ms) · 1.3 ms out', undefined],
      ['Round trip', '41 ms (RTCP)', undefined],
    ]
  );
  // No worst figure, no summary MOS, no chart point.
  assert.deepEqual(sipMediaWorst(busy), {});
  assert.equal(sipCallSummary({ ...answeredCall, media: busy }), 'ringing after 850 ms, answered in 4.2 s');
  const series = sipMediaSeries([{ ts: 1, m: callMetrics({ media: busy }) }]);
  assert.deepEqual(series.mos, { inbound: [], outbound: [], round_trip: [] });
});

test('a refused address with the far end\'s audio still measured shows what was judged', () => {
  const refused: SIPMediaMetrics = {
    ...measuredMedia,
    expect: 'audio',
    status: 'refused',
    detail: 'private_behind_public',
    destination: undefined,
    outbound: undefined,
    two_way: 'inbound_only',
    two_way_evidence: ['rx'],
  };
  assert.deepEqual(
    sipMediaFacts(refused, { expect: 'audio', min_mos: 4.35 })
      .slice(0, 2)
      .map((f) => [f.label, f.value, f.tone]),
    [
      [
        'Call audio',
        "far end's media address refused (a private address behind a public peer): probe audio not sent, inbound measured",
        'warning',
      ],
      ['Call quality', 'MOS 4.31 in (estimate)', 'warning'],
    ]
  );
});

test('the round trip is the echo only once enough echoes matched, as the worker judges it', () => {
  const echoes = (matched: number): SIPMediaMetrics => ({
    ...measuredMedia,
    round_trip: {
      matched_by: 'marker',
      expected: 250,
      matched,
      loss_pct: 99,
      duplicates: 0,
      late: 0,
      effective_loss_pct: 99,
      burst_r: 1,
      rtt_ms: { p50: 40, p95: 44, max: 51 },
    },
    outbound: { ...measuredMedia.outbound!, rtt_ms: 600 },
  });
  const rtt = (m: SIPMediaMetrics) => sipMediaFacts(m, { expect: 'two_way', max_rtt_ms: 500 }).find((f) => f.key === 'media-rtt');
  assert.deepEqual([rtt(echoes(2))?.value, rtt(echoes(2))?.tone], ['600 ms (RTCP)', 'warning']);
  assert.deepEqual([rtt(echoes(5))?.value, rtt(echoes(5))?.tone], ['40 ms (echo, p95 44 ms)', undefined]);
});

test('sipMediaFacts on an echo line: the echo round trip and its evidence', () => {
  const echo: SIPMediaMetrics = {
    ...measuredMedia,
    expect: 'echo',
    inbound: undefined,
    outbound: undefined,
    round_trip: {
      matched_by: 'ssrc',
      expected: 250,
      matched: 250,
      loss_pct: 0,
      duplicates: 0,
      late: 0,
      effective_loss_pct: 0,
      burst_r: 1,
      jitter_ms: 0.9,
      rtt_ms: { p50: 38, p95: 44, max: 51 },
      quality: quality(4.36, 'echo'),
    },
    two_way_evidence: ['rx', 'echo'],
  };
  assert.deepEqual(
    sipMediaFacts(echo).map((f) => [f.label, f.value]),
    [
      ['Call quality', 'MOS 4.36 round trip (estimate)'],
      ['Loss', '0% round trip'],
      ['Jitter', '0.9 ms round trip'],
      ['Round trip', '38 ms (echo, p95 44 ms)'],
      ['Two-way audio', 'verified (echo)'],
    ]
  );
});

test('sipMediaFacts leaves out a basis too short to judge and flags evidence short of expect', () => {
  const oneWay: SIPMediaMetrics = {
    ...measuredMedia,
    status: 'truncated',
    window_ms: 1200,
    inbound: { ...measuredMedia.inbound!, quality: undefined },
    outbound: undefined,
    two_way: 'inbound_only',
    two_way_evidence: ['rx'],
  };
  assert.deepEqual(
    sipMediaFacts(oneWay).map((f) => [f.label, f.value, f.tone]),
    [
      ['Call audio', 'only 1.2 s measured: quality not judged', 'note'],
      ['Two-way audio', 'inbound only: nothing showed the far end heard the probe', 'warning'],
    ]
  );
  // audio asks only for the far end's audio; measure-only asks nothing.
  assert.equal(sipMediaFacts({ ...oneWay, expect: 'audio' }).at(-1)?.tone, undefined);
  assert.equal(sipMediaFacts({ ...oneWay, expect: 'none', two_way: 'none' }).at(-1)?.tone, undefined);
  assert.equal(sipMediaFacts({ ...oneWay, expect: 'audio', two_way: 'none' }).at(-1)?.tone, 'warning');
});

test('sipMediaFacts says in words why nothing was measured', () => {
  const unmeasured = (over: Partial<SIPMediaMetrics>) =>
    sipMediaFacts({ expect: 'audio', status: 'measured', window_ms: 0, warmup_ms: 500, ...over }).map((f) => [
      f.value,
      f.tone,
    ]);
  assert.deepEqual(unmeasured({ status: 'unusable_answer', detail: 'inactive' }), [
    ['far end answered without a usable audio stream (inactive)', 'warning'],
  ]);
  assert.deepEqual(unmeasured({ status: 'unusable_answer', detail: 'codec_not_offered' }), [
    ['far end answered without a usable audio stream (neither PCMU nor PCMA)', 'warning'],
  ]);
  assert.deepEqual(unmeasured({ status: 'refused', detail: 'private_behind_public' }), [
    ["far end's media address refused (a private address behind a public peer), nothing measured", 'warning'],
  ]);
  assert.deepEqual(unmeasured({ status: 'refused', detail: 'broadcast' }), [
    ["far end's media address refused (a broadcast address of the worker's own network), nothing measured", 'warning'],
  ]);
  // A code this release does not know reads as itself.
  assert.deepEqual(unmeasured({ status: 'refused', detail: 'future_reason' }), [
    ["far end's media address refused (future_reason), nothing measured", 'warning'],
  ]);
  assert.deepEqual(unmeasured({ status: 'not_held' }), [['not measured: the call never reached its hold', 'note']]);
  assert.deepEqual(unmeasured({ status: 'interrupted' }), [['not measured: the worker shut down mid-call', 'note']]);
});

test('a latched destination is a note', () => {
  const peer = sipMediaFacts({ ...measuredMedia, latched: 'peer' });
  assert.deepEqual(peer.at(-1), {
    key: 'media-latched',
    label: 'Audio sent to',
    value: "the signalling peer (latched: the answer's address was refused)",
    tone: 'note',
  });
  assert.equal(
    sipMediaFacts({ ...measuredMedia, latched: 'port' }).at(-1)?.value,
    "the port the far end's audio came from (latched)"
  );
});

test('the call facts carry the media rows after the codec, and audio after an unconfirmed BYE stays a warning row', () => {
  const facts = sipFacts(
    {
      ...base,
      method: 'INVITE',
      call: { ...answeredCall, teardown: 'bye_unconfirmed', bye_ms: undefined, media: measuredMedia },
      warnings: ['teardown: BYE unconfirmed; far end still sending audio after the BYE'],
    },
    undefined,
    { expect: 'two_way' }
  );
  const labels = facts.map((f) => f.label);
  assert.ok(labels.indexOf('Media') < labels.indexOf('Call quality'));
  assert.ok(labels.indexOf('Two-way audio') < labels.indexOf('Teardown'));
  assert.deepEqual(
    facts.filter((f) => f.tone === 'warning').map((f) => [f.label, f.value]),
    [
      ['Teardown', 'BYE unconfirmed: the far end may still hold the call'],
      ['Warning', 'teardown: BYE unconfirmed; far end still sending audio after the BYE'],
    ]
  );
});

test('sipCallSummary adds the worst MOS', () => {
  assert.equal(
    sipCallSummary({ ...answeredCall, media: measuredMedia }),
    'ringing after 850 ms, answered in 4.2 s, MOS 4.31'
  );
  const unjudged = { ...measuredMedia, inbound: undefined, outbound: undefined };
  assert.equal(sipCallSummary({ ...answeredCall, media: unjudged }), 'ringing after 850 ms, answered in 4.2 s');
});

test('sipMediaWorst picks the lowest MOS and the highest loss and jitter, each with its basis', () => {
  const worst = sipMediaWorst(measuredMedia);
  assert.deepEqual(worst.mos, { value: 4.31, basis: 'inbound' });
  assert.deepEqual(worst.loss, { value: 0.73, basis: 'inbound' });
  assert.deepEqual(worst.jitter, { value: 2.14, basis: 'inbound' });
  assert.deepEqual(sipMediaWorst({ ...measuredMedia, inbound: undefined, outbound: undefined }), {});
  assert.equal(formatMOS(worst.mos?.value), '4.31');
});

test('an older worker\'s refusal maps to an upgrade hint', () => {
  const refused = 'invalid SIP config: call.media: media analysis is not available yet';
  assert.ok(sipMediaUnsupportedHint(refused).includes('upgrade'));
  assert.equal(sipMediaUnsupportedHint('timeout: no response to INVITE'), '');
  assert.equal(sipMediaUnsupportedHint(undefined), '');

  const at = (id: string | undefined, name: string | undefined, error?: string): CheckResult => ({
    ...result(`${id}-${error ?? 'ok'}`),
    status: error ? 'error' : 'success',
    location_id: id,
    location_name: name,
    error_message: error,
  });
  // Newest first: Paris was upgraded since its refusal, Tokyo and the default
  // fleet were not.
  const hint = sipMediaOutdatedWorkersHint([
    at('paris', 'Paris'),
    at('tokyo', 'Tokyo', refused),
    at(undefined, undefined, refused),
    at('paris', 'Paris', refused),
  ]);
  assert.equal(
    hint,
    'Upgrade the workers at Tokyo, the default fleet: they predate call-quality measurement and refuse this monitor'
  );
  assert.equal(sipMediaOutdatedWorkersHint([at('paris', 'Paris')]), '');
  assert.equal(
    sipMediaOutdatedWorkersHint([at('tokyo', 'Tokyo', refused)]),
    'Upgrade the worker at Tokyo: it predates call-quality measurement and refuses this monitor'
  );
});

test('sipMediaSeries plots each judged basis, leaving gaps for calls too short to judge', () => {
  const series = sipMediaSeries([
    { ts: 1, m: callMetrics({ media: measuredMedia }) },
    // Too short to judge: no point on any basis.
    {
      ts: 2,
      m: callMetrics({
        media: { ...measuredMedia, inbound: { ...measuredMedia.inbound!, quality: undefined }, outbound: undefined },
      }),
    },
    // No media at all (call quality was off then).
    { ts: 3, m: callMetrics({}) },
  ]);
  assert.deepEqual(series.mos, { inbound: [[1, 4.31]], outbound: [[1, 4.36]], round_trip: [] });
  assert.deepEqual(series.loss, { inbound: [[1, 0.73]], outbound: [[1, 0]], round_trip: [] });
  assert.deepEqual(series.jitter, { inbound: [[1, 2.14]], outbound: [[1, 1.3]], round_trip: [] });
});

test('sipCallThresholdLines labels other units through its formatter', () => {
  assert.deepEqual(sipCallThresholdLines(4, 3.6, formatMOS), [
    { y: 4, label: 'warn 4.00' },
    { y: 3.6, label: 'fail 3.60' },
  ]);
});
