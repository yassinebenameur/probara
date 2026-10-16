import { formatDate, formatLatency, pluralize } from './format';
import type { TimedSnapshot } from './metric-snapshots';
import type { CheckResult, SIPCallMetrics, SIPMetrics, SIPMetricsEnvelope, TLSCertInfo } from './types';

// Facts the monitor detail page's Server panel shows for a SIP monitor, read
// from the worker's metrics_data.sip envelope (worker sip_metrics.go).

export interface SIPFact {
  key: string;
  label: string;
  value: string;
  tone?: 'warning' | 'note';
  /** Rendered as a copy control: an identifier to paste into server logs. */
  copyable?: boolean;
}

/** The newest result that carries a SIP envelope (older results may not). */
export function latestSIPMetrics(results: CheckResult[]): SIPMetrics | null {
  for (const result of results) {
    const sip = (result.metrics_data as SIPMetricsEnvelope | undefined)?.sip;
    if (sip) return sip;
  }
  return null;
}

/** `486 Busy Here (Q.850 17 User busy)`; empty when no final arrived. */
export function sipResponseLabel(m: SIPMetrics): string {
  if (!m.status_code) return '';
  let label = [m.status_code, m.reason].filter(Boolean).join(' ');
  const rh = m.reason_header;
  if (rh) {
    const cause = [rh.protocol, rh.cause, rh.text].filter((part) => part !== undefined && part !== '').join(' ');
    if (cause) label += ` (${cause})`;
  }
  return label;
}

/**
 * `expires in 12 days (Nov 1, 2026)`; empty without an expiry. Days come
 * from the worker's snapshot (whole days, truncated), the same number the
 * status page shows.
 */
export function certificateExpiryLabel(tls: TLSCertInfo): string {
  const days = tls.days_until_expiry;
  if (days === undefined) return '';
  const when = tls.not_after ? ` (${formatDate(tls.not_after)})` : '';
  if (days < 0) return `expired ${pluralize(-days, 'day')} ago${when}`;
  return `expires in ${pluralize(days, 'day')}${when}`;
}

/**
 * The facts for one envelope. minDaysValid is the monitor's
 * tls_min_days_valid: a certificate inside it reads as a warning, matching
 * the tls_expiry alert the alerter opens for it.
 */
export function sipFacts(m: SIPMetrics, minDaysValid?: number): SIPFact[] {
  const facts: SIPFact[] = [];
  if (m.server) facts.push({ key: 'server', label: 'Answered by', value: m.server });
  const address = sipAddressLabel(m);
  if (address) facts.push({ key: 'address', label: 'Address', value: address });
  const attempts = m.resolution?.attempts ?? [];
  if (attempts.length > 1) {
    // The failover path, so a dead or refusing server shows even when the
    // check passed on the next one.
    facts.push({
      key: 'attempts',
      label: 'Tried',
      value: attempts.map((a) => `${a.address} ${a.outcome}`).join(' → '),
      tone: 'note',
    });
  }
  if (m.transport) {
    const transport = m.transport.toUpperCase();
    facts.push({
      key: 'transport',
      label: 'Transport',
      value: m.tls?.version ? `${transport} · ${m.tls.version}` : transport,
    });
  }
  const expiry = m.tls ? certificateExpiryLabel(m.tls) : '';
  if (expiry) {
    const days = m.tls?.days_until_expiry ?? 0;
    const warn = days < 0 || (minDaysValid !== undefined && days < minDaysValid);
    facts.push({ key: 'certificate', label: 'Certificate', value: expiry, tone: warn ? 'warning' : undefined });
  }

  const response = sipResponseLabel(m);
  if (response) facts.push({ key: 'response', label: 'Response', value: `${m.method} ${response}` });

  const timing = [
    m.timing.dns_ms !== undefined ? `DNS ${formatLatency(m.timing.dns_ms)}` : '',
    m.timing.connect_ms !== undefined ? `connect ${formatLatency(m.timing.connect_ms)}` : '',
    m.timing.tls_handshake_ms !== undefined ? `TLS ${formatLatency(m.timing.tls_handshake_ms)}` : '',
    m.timing.response_ms !== undefined ? `response ${formatLatency(m.timing.response_ms)}` : '',
  ].filter(Boolean);
  if (timing.length > 0) facts.push({ key: 'timing', label: 'Timing', value: timing.join(', ') });

  if (m.auth) {
    const scheme = m.auth.algorithm ? `digest ${m.auth.algorithm}` : 'digest';
    facts.push({
      key: 'auth',
      label: 'Auth',
      value: m.auth.rounds > 0 ? `${m.auth.challenge} ${scheme}` : `${m.auth.challenge} challenge, not answered`,
    });
  }
  if (m.retransmissions > 0) {
    facts.push({ key: 'retransmissions', label: 'Retransmits', value: pluralize(m.retransmissions, 'resend'), tone: 'note' });
  }
  if (m.stray_messages > 0) {
    facts.push({ key: 'stray', label: 'Ignored', value: pluralize(m.stray_messages, 'unmatched response'), tone: 'note' });
  }
  if (m.call) facts.push(...sipCallFacts(m.call));
  // The server's own Warning header (RFC 3261 §20.43): diagnostic text, not
  // a Probara threshold warning, so it reads as a note.
  if (m.warning) facts.push({ key: 'sip-warning', label: 'SIP warning', value: m.warning, tone: 'note' });
  // The check's own warnings (a warn threshold, an unconfirmed teardown, a
  // ring-mode call that was answered): the status stayed, the operator
  // should still know.
  // An unconfirmed teardown already reads as a warning on its Teardown row.
  const teardownShown = m.call?.teardown === 'bye_unconfirmed' || m.call?.teardown === 'cancel_unconfirmed';
  (m.warnings ?? []).forEach((warning, i) => {
    if (teardownShown && warning.startsWith('teardown:')) return;
    facts.push({ key: `warning-${i}`, label: 'Warning', value: warning, tone: 'warning' });
  });
  if (m.latency_warn_ms !== undefined && !(m.warnings && m.warnings.length > 0)) {
    facts.push({ key: 'latency-warn', label: 'Warning', value: `over the ${formatLatency(m.latency_warn_ms)} warning threshold`, tone: 'warning' });
  }
  if (m.call_id) facts.push({ key: 'call-id', label: 'Call-ID', value: m.call_id, copyable: true });
  return facts;
}

/**
 * The call's own facts (metrics_data.sip.call): post-dial delay with what
 * ended it, answer time, the negotiated codec and how the call was torn
 * down. An unconfirmed teardown is a warning: the far end may still hold a
 * billed call open.
 */
export function sipCallFacts(call: SIPCallMetrics): SIPFact[] {
  const facts: SIPFact[] = [];
  if (call.pdd_ms !== undefined) {
    const source = call.pdd_source ? ` (${call.pdd_source})` : '';
    facts.push({ key: 'pdd', label: 'Post-dial delay', value: `${formatLatency(call.pdd_ms)}${source}` });
  }
  if (call.answer_ms !== undefined) {
    const hold = call.hold_ms > 0 ? `, held ${formatLatency(call.hold_ms)}` : '';
    facts.push({ key: 'answer', label: 'Answered in', value: `${formatLatency(call.answer_ms)}${hold}` });
  } else if (call.mode === 'ring' && call.pdd_ms !== undefined) {
    facts.push({ key: 'answer', label: 'Call', value: 'rang, cancelled before answer (ring mode)' });
  }
  const media = [
    call.sdp_answer?.codec ?? '',
    call.early_media ? 'early media' : '',
  ].filter(Boolean);
  if (media.length > 0) facts.push({ key: 'media', label: 'Media', value: media.join(' · ') });
  if (call.forked_answers > 0) {
    facts.push({ key: 'forks', label: 'Forked', value: `${pluralize(call.forked_answers, 'extra answer')}, each hung up`, tone: 'note' });
  }
  const teardown = sipTeardownLabel(call);
  if (teardown) {
    const unconfirmed = call.teardown === 'bye_unconfirmed' || call.teardown === 'cancel_unconfirmed';
    facts.push({ key: 'teardown', label: 'Teardown', value: teardown, tone: unconfirmed ? 'warning' : undefined });
  }
  return facts;
}

/** How the call ended, in words; empty for "none" (nothing to tear down). */
export function sipTeardownLabel(call: SIPCallMetrics): string {
  const bye = call.bye_ms !== undefined ? ` in ${formatLatency(call.bye_ms)}` : '';
  switch (call.teardown) {
    case 'bye_ok':
      return `BYE confirmed${bye}`;
    case 'bye_481':
      return 'BYE answered 481: the far end had already ended the call';
    case 'bye_unconfirmed':
      return 'BYE unconfirmed: the far end may still hold the call';
    case 'cancelled':
      return 'CANCEL confirmed (487)';
    case 'cancel_unconfirmed':
      return 'CANCEL unconfirmed: the far end may still be ringing';
    case 'answered_after_cancel':
      return `answered after CANCEL, hung up with BYE${bye}`;
    case 'remote_bye':
      return 'the callee hung up first';
    case 'no_provisional':
      return 'no response, nothing to cancel';
    case 'none':
    case '':
      return '';
    default: {
      const code = /^bye_(\d{3})$/.exec(call.teardown)?.[1];
      return code ? `BYE answered ${code}` : call.teardown;
    }
  }
}

/**
 * One line for a test-call Test result: `ringing after 850 ms, answered in
 * 4.2 s`. Empty without call metrics.
 */
export function sipCallSummary(call: SIPCallMetrics | undefined): string {
  if (!call) return '';
  const parts: string[] = [];
  // pdd_source 200: answered without ringing, so the PDD is the answer time.
  if (call.pdd_ms !== undefined && call.pdd_source !== '200') {
    parts.push(`ringing after ${formatLatency(call.pdd_ms)}`);
  }
  if (call.answer_ms !== undefined) parts.push(`answered in ${formatLatency(call.answer_ms)}`);
  return parts.join(', ');
}

/**
 * `203.0.113.7:5061 (sbc2.example.com via SRV, after failover)`: the address
 * that decided the check, and how DNS led there when it was not the
 * monitor's own host (an SRV target, reached through NAPTR or not; a
 * failover).
 */
export function sipAddressLabel(m: SIPMetrics): string {
  const target = m.target;
  if (!target?.address) return '';
  const notes: string[] = [];
  if (target.resolved_via === 'srv') notes.push(`${target.host} via SRV`);
  if (target.resolved_via === 'naptr') notes.push(`${target.host} via NAPTR→SRV`);
  if (target.failover) notes.push('after failover');
  return notes.length > 0 ? `${target.address} (${notes.join(', ')})` : target.address;
}

/** metrics_data.sip.call of one snapshot, or undefined (metric-snapshots pickSnapshots). */
export function pickSIPCall(metricsData: unknown): SIPCallMetrics | undefined {
  return (metricsData as SIPMetricsEnvelope | null | undefined)?.sip?.call ?? undefined;
}

type Point = [number, number];

/**
 * The test-call timing series the detail page charts (SipCallPanels), one
 * point per check that measured it:
 * - pdd: post-dial delay from the INVITE that drew it (the authenticated one
 *   when challenged) to the first 18x, or to the 200 when none came;
 * - answer: from that INVITE to the 200;
 * - setupPdd / setup: the same ends measured from the first INVITE, so they
 *   include the digest round trip. Empty unless some check was challenged
 *   (they then differ from pdd / answer), so an unchallenged trunk draws one
 *   line per panel.
 * Checks that stopped before the event (no ringing, no answer, no response)
 * add no point, which leaves a gap in the line.
 */
export function sipCallSeries(snapshots: TimedSnapshot<SIPCallMetrics>[]): {
  pdd: Point[];
  setupPdd: Point[];
  answer: Point[];
  setup: Point[];
} {
  const pdd: Point[] = [];
  const setupPdd: Point[] = [];
  const answer: Point[] = [];
  const setup: Point[] = [];
  let pddDiffers = false;
  let answerDiffers = false;
  for (const { ts, m } of snapshots) {
    if (m.pdd_ms !== undefined) {
      pdd.push([ts, m.pdd_ms]);
      const viaSetup = m.setup_pdd_ms ?? m.pdd_ms;
      setupPdd.push([ts, viaSetup]);
      if (viaSetup !== m.pdd_ms) pddDiffers = true;
    }
    if (m.answer_ms !== undefined) {
      answer.push([ts, m.answer_ms]);
      const viaSetup = m.setup_ms ?? m.answer_ms;
      setup.push([ts, viaSetup]);
      if (viaSetup !== m.answer_ms) answerDiffers = true;
    }
  }
  return {
    pdd,
    setupPdd: pddDiffers ? setupPdd : [],
    answer,
    setup: answerDiffers ? setup : [],
  };
}

/**
 * The warn / max thresholds of one call timing as chart reference lines
 * (`warn 3.0 s`, `fail 5.0 s`), in the MongoDB panels' wording.
 */
export function sipCallThresholdLines(warnMs?: number, maxMs?: number): { y: number; label: string }[] {
  const lines: { y: number; label: string }[] = [];
  if (warnMs !== undefined && warnMs > 0) lines.push({ y: warnMs, label: `warn ${formatLatency(warnMs)}` });
  if (maxMs !== undefined && maxMs > 0) lines.push({ y: maxMs, label: `fail ${formatLatency(maxMs)}` });
  return lines;
}
