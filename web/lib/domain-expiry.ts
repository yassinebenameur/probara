import { formatDate, pluralize } from './format';
import { expiryDaysTone, type UptimeTone } from './status-tones';
import type { DomainInfo, DomainMonitorConfig } from './types';

// What a domain monitor's latest check says, for the monitor page, the list's
// detail panel and the form's test lookup. The snapshot is
// metrics_data.domain (shared/domainexpiry Info).

/** The warning window a monitor applies (30 days when unset, as the API does). */
export function domainWarnDays(config: DomainMonitorConfig | undefined): number {
  return config?.min_days_valid ?? 30;
}

/** Said wherever a registry known to publish no date is looked up. */
export const NO_EXPIRY_PUBLISHED_NOTE =
  'This registry does not publish an expiry date; this monitor can only detect that the domain stops being registered.';

export interface DomainExpiryState {
  /** Short headline: "expires in 12 days", "expired 3 days ago", "not registered", "expiry unknown", … */
  headline: string;
  tone: UptimeTone;
  /** Why the expiry is unknown, or what the headline means, when it needs saying. */
  reason?: string;
}

/**
 * The headline and tone of a snapshot. Days are recomputed from expires_at
 * at render time (rounded down, as the alerter does), so a day-old check
 * still reads right.
 */
export function domainExpiryState(info: DomainInfo, warnDays: number, now: Date = new Date()): DomainExpiryState {
  if (info.registered === false) {
    return info.not_found_confirmed
      ? { headline: 'not registered', tone: 'bad' }
      : {
          headline: 'reported as not registered',
          tone: 'bad',
          reason: 'One registry source said so; the monitor goes down only when the next lookup says it again.',
        };
  }
  if (info.no_expiry_published) return { headline: 'no expiry date published', tone: 'none', reason: NO_EXPIRY_PUBLISHED_NOTE };
  if (info.expires_at) {
    const at = new Date(info.expires_at);
    if (!Number.isNaN(at.getTime())) {
      const days = Math.floor((at.getTime() - now.getTime()) / 86_400_000);
      if (days < 0 && info.renewal_grace) {
        return { headline: 'in renew grace period', tone: 'warn', reason: `The date passed ${pluralize(-days, 'day')} ago; the registry still holds the name.` };
      }
      const tone = expiryDaysTone(days, warnDays);
      if (days < 0) return { headline: `expired ${pluralize(-days, 'day')} ago`, tone };
      return { headline: `expires in ${pluralize(days, 'day')}`, tone };
    }
  }
  return { headline: 'expiry unknown', tone: 'warn', reason: info.unknown_reason || undefined };
}

export interface DomainFact {
  label: string;
  value: string;
  /** Worth a warning colour (a hold). */
  warning?: boolean;
}

/** The registration facts worth showing, in display order. */
export function domainFacts(info: DomainInfo): DomainFact[] {
  const facts: DomainFact[] = [];
  if (info.on_hold) {
    facts.push({ label: 'Hold', value: 'on hold at the registry, so the domain does not resolve', warning: true });
  }
  if (info.expires_at) facts.push({ label: 'Expires', value: formatDate(info.expires_at) });
  if (info.registrar) facts.push({ label: 'Registrar', value: info.registrar });
  if (info.registered_at) facts.push({ label: 'Registered', value: formatDate(info.registered_at) });
  if (info.source) {
    const source = info.source === 'rdap' ? 'RDAP' : 'WHOIS';
    facts.push({ label: 'Source', value: info.server ? `${source} · ${info.server}` : source });
  }
  if (info.statuses && info.statuses.length > 0) facts.push({ label: 'Status', value: info.statuses.join(', ') });
  return facts;
}
