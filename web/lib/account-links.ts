// Account links (invite, password reset, email confirmation) open a public
// page with their single-use token in the URL fragment: /accept-invite#<token>,
// /reset-password#<token>, /confirm-email#<token>. The browser never sends a
// fragment to a server, so the token stays out of access logs; the page hands
// it to the API in the X-Action-Token header (lib/api/account.ts), as the
// acknowledge and report-unsubscribe links do. Tokens are single use, so the
// pages read only on load and act from a button.

export { readActionToken as readLinkToken } from './alert-action';

/**
 * A link a superadmin was handed to pass on: absolute when the API knows the
 * operator UI's origin (APP_BASE_URL), else a path on this UI, made absolute
 * with the origin the superadmin is looking at.
 */
export function absoluteLink(link: string, origin: string): string {
  return link.startsWith('/') ? `${origin.replace(/\/+$/, '')}${link}` : link;
}

/** Whether a typed code looks like an authenticator code (six digits). */
export function looksLikeTotpCode(code: string): boolean {
  return /^\d{6}$/.test(code.replace(/\s+/g, ''));
}

/** The recovery codes as a text file to save, one per line. */
export function recoveryCodesFile(username: string, codes: readonly string[], now: Date = new Date()): string {
  return [
    `Probara recovery codes for ${username}`,
    `Generated ${now.toISOString().slice(0, 10)}. Each code works once, in place of an authenticator code.`,
    '',
    ...codes,
    '',
  ].join('\n');
}

/** The otpauth:// secret shown for manual entry, in groups of four. */
export function groupSecret(secret: string): string {
  return secret.replace(/\s+/g, '').match(/.{1,4}/g)?.join(' ') ?? '';
}

/** What a public link page says for an API error code. */
export function linkErrorMessage(code: string | undefined, fallback: string): string {
  switch (code) {
    case 'invalid_token':
      return 'This link is invalid or has expired. Links work once; ask for a new one.';
    case 'email_taken':
      return 'Another account already uses this email address, so it cannot be confirmed.';
    case 'unavailable':
      return 'This is not available yet: the server is being upgraded. Try again in a few minutes.';
    default:
      return fallback;
  }
}
