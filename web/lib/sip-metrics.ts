import { formatDate, formatLatency, pluralize } from './format';
import type { CheckResult, SIPMetrics, SIPMetricsEnvelope, TLSCertInfo } from './types';

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
  // The server's own Warning header (RFC 3261 §20.43): diagnostic text, not
  // a Probara threshold warning, so it reads as a note.
  if (m.warning) facts.push({ key: 'sip-warning', label: 'SIP warning', value: m.warning, tone: 'note' });
  if (m.call_id) facts.push({ key: 'call-id', label: 'Call-ID', value: m.call_id, copyable: true });
  return facts;
}

/**
 * `203.0.113.7:5061 (sbc2.example.com via SRV, after failover)`: the address
 * that decided the check, and how DNS led there when it was not the
 * monitor's own host (an SRV target, a failover).
 */
export function sipAddressLabel(m: SIPMetrics): string {
  const target = m.target;
  if (!target?.address) return '';
  const notes: string[] = [];
  if (target.resolved_via === 'srv') notes.push(`${target.host} via SRV`);
  if (target.failover) notes.push('after failover');
  return notes.length > 0 ? `${target.address} (${notes.join(', ')})` : target.address;
}
