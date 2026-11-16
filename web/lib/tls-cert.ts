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

/**
 * Which certificate a tls_expiry alert is about, read from its last_error
 * (written by Go notifications.FormatTLSExpiryError; this is the twin of
 * ParseTLSExpiryError, both pinned by
 * shared/notifications/testdata/tls_expiry_errors.json). Null for any other
 * text, e.g. alerts opened before the certificate was named.
 */
export interface TLSExpiryCertificate {
  subject: string;
  intermediate: boolean;
  /** YYYY-MM-DD, empty when unknown. */
  notAfter: string;
}

const TLS_EXPIRY_ERROR =
  /^tls: (intermediate certificate|certificate)(?: (.+))? (?:expires|expired)(?: on (\d{4}-\d{2}-\d{2}))? \((?:in -?\d+d, < \d+d|\d+d ago, threshold \d+d)\)$/;

export function tlsExpiryCertificate(lastError: string | null | undefined): TLSExpiryCertificate | null {
  const m = TLS_EXPIRY_ERROR.exec((lastError ?? '').trim());
  if (!m) return null;
  return { intermediate: m[1] === 'intermediate certificate', subject: m[2] ?? '', notAfter: m[3] ?? '' };
}

/** The CN of an RFC 4514 subject (`CN=R3,O=Example` is `R3`), else the whole subject. */
export function commonName(subject: string): string {
  const parts: string[] = [];
  let start = 0;
  for (let i = 0; i < subject.length; i++) {
    if (subject[i] === '\\') i++;
    else if (subject[i] === ',' || subject[i] === '+') {
      parts.push(subject.slice(start, i));
      start = i + 1;
    }
  }
  parts.push(subject.slice(start));
  for (const part of parts) {
    const eq = part.indexOf('=');
    if (eq > 0 && part.slice(0, eq).trim().toUpperCase() === 'CN') return part.slice(eq + 1).replace(/\\(.)/g, '$1');
  }
  return subject;
}

/**
 * The tls_expiry condition in a few words, as the notification summary puts
 * it: an intermediate is named, the server's own certificate is "certificate".
 */
export function tlsExpiryCondition(lastError: string | null | undefined, days: number | null | undefined): string {
  const cert = tlsExpiryCertificate(lastError);
  const cn = cert?.intermediate ? commonName(cert.subject) : '';
  const which = cert?.intermediate ? `intermediate certificate${cn ? ` (${cn})` : ''}` : 'certificate';
  if (days == null) return `${which} is about to expire`;
  if (days < 0 && cert?.intermediate) return `${which} has expired`;
  return `${which} expires in ${Math.max(0, Math.round(days))}d`;
}
