import { formatLatency, pluralize } from './format';
import type { CheckResult, SIPMetrics, SIPMetricsEnvelope } from './types';

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

export function sipFacts(m: SIPMetrics): SIPFact[] {
  const facts: SIPFact[] = [];
  if (m.server) facts.push({ key: 'server', label: 'Answered by', value: m.server });
  if (m.target?.address) facts.push({ key: 'address', label: 'Address', value: m.target.address });
  facts.push({ key: 'transport', label: 'Transport', value: m.transport.toUpperCase() });

  const response = sipResponseLabel(m);
  if (response) facts.push({ key: 'response', label: 'Response', value: `${m.method} ${response}` });

  const timing = [
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
