import { formatDate, pluralize } from './format';
import type { TLSCertInfo } from './types';

// The certificate expiry a check recorded (Go shared/tlscert): the earliest
// across the leaf and the intermediates the server presented, which is what
// the tls_expiry alert and the status page act on (tlscert.Expiry), else the
// leaf's for snapshots recorded before the chain was measured.

export interface CertificateExpiry {
  days?: number;
  notAfter?: string;
  /** Subject of the expiring certificate. */
  subject?: string;
  /** The expiring certificate is an intermediate, not the server's own. */
  intermediate: boolean;
}

export function certificateExpiry(tls: TLSCertInfo): CertificateExpiry {
  if (tls.chain_not_after) {
    return {
      days: tls.chain_min_days,
      notAfter: tls.chain_not_after,
      subject: tls.chain_expiring_subject,
      intermediate: Boolean(tls.chain_expiring_subject && tls.chain_expiring_subject !== tls.subject),
    };
  }
  return { days: tls.days_until_expiry, notAfter: tls.not_after, subject: tls.subject, intermediate: false };
}

/**
 * `expires in 12 days (Nov 1, 2026)`, or `intermediate CN=R3 expires in …`
 * when an intermediate expires first; empty without an expiry. Days come
 * from the worker's snapshot (whole days, truncated), the same number the
 * status page shows.
 */
export function certificateExpiryLabel(tls: TLSCertInfo): string {
  const { days, notAfter, subject, intermediate } = certificateExpiry(tls);
  if (days === undefined) return '';
  const when = notAfter ? ` (${formatDate(notAfter)})` : '';
  const which = intermediate && subject ? `intermediate ${subject} ` : '';
  if (days < 0) return `${which}expired ${pluralize(-days, 'day')} ago${when}`;
  return `${which}expires in ${pluralize(days, 'day')}${when}`;
}

/** Whether the expiry reads as a warning: expired, or inside tls_min_days_valid. */
export function certificateExpiryWarns(tls: TLSCertInfo, minDaysValid?: number): boolean {
  const days = certificateExpiry(tls).days ?? 0;
  return days < 0 || (minDaysValid !== undefined && days < minDaysValid);
}
