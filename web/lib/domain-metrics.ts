import { formatDate } from './format';
import type { CheckResult, DomainMetrics, DomainMetricsEnvelope } from './types';

/** One detail-page fact about the latest domain lookup. */
export interface DomainFact {
  key: string;
  label: string;
  value: string;
  tone?: 'warning';
}

/** The newest result that carries a domain envelope (failed lookups carry none). */
export function latestDomainMetrics(results: CheckResult[]): DomainMetrics | null {
  for (const result of results) {
    const domain = (result.metrics_data as DomainMetricsEnvelope | undefined)?.domain;
    if (domain) return domain;
  }
  return null;
}

/** Whole days from now to an RFC 3339 instant, as the alerter counts them (floor). */
export function daysUntil(iso: string, now: Date = new Date()): number | null {
  const at = Date.parse(iso);
  if (Number.isNaN(at)) return null;
  return Math.floor((at - now.getTime()) / 86_400_000);
}

/**
 * Expiry (amber inside the alert window or once expired), registrar,
 * statuses and where the answer came from.
 */
export function domainFacts(m: DomainMetrics, minDaysValid?: number, now: Date = new Date()): DomainFact[] {
  const facts: DomainFact[] = [];
  if (!m.registered) {
    facts.push({ key: 'registration', label: 'Registration', value: 'Not registered', tone: 'warning' });
  } else if (m.expires_at) {
    const days = daysUntil(m.expires_at, now) ?? m.days_until_expiry ?? 0;
    const when = formatDate(m.expires_at, m.expires_at, now);
    const value = days < 0 ? `Expired ${when} (${-days}d ago)` : `${when} (in ${days}d)`;
    const warn = days < 0 || (minDaysValid !== undefined && days < minDaysValid);
    facts.push({ key: 'expires', label: 'Expires', value, tone: warn ? 'warning' : undefined });
  } else {
    facts.push({ key: 'expires', label: 'Expires', value: m.note || 'Not published by the registry' });
  }
  if (m.registrar) facts.push({ key: 'registrar', label: 'Registrar', value: m.registrar });
  if (m.statuses && m.statuses.length > 0) facts.push({ key: 'statuses', label: 'Status', value: m.statuses.join(', ') });
  facts.push({ key: 'source', label: 'Looked up via', value: m.source === 'whois' ? 'WHOIS' : 'RDAP' });
  return facts;
}
