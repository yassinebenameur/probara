// Content-Security-Policy for the operator UI, built per request by
// proxy.ts. Pure functions so the policy is unit-tested (csp.test.ts).
//
// Scripts: a per-request nonce plus 'strict-dynamic'. Next.js reads the
// nonce back from the request's CSP header and stamps it on its own
// framework and page scripts; scripts those load are trusted transitively.
// Any other inline <script> must carry the nonce (see app/layout.tsx), and
// no page may render one without it.
//
// Styles stay 'unsafe-inline': server-rendered style="" attributes (charts,
// layout) cannot carry a nonce, and a nonce in style-src would disable
// 'unsafe-inline' for them.

export interface WebCspOptions {
  nonce: string;
  /** next dev needs eval for React's error overlays. */
  dev: boolean;
  /** Origins the status-page preview iframe may load (frame-src). */
  statusPageOrigins: string[];
  /** Absolute API origin when the browser calls the API directly. */
  apiOrigin?: string;
}

export function buildWebCsp({ nonce, dev, statusPageOrigins, apiOrigin }: WebCspOptions): string {
  const connect = ["'self'", ...(apiOrigin ? [apiOrigin] : [])];
  return [
    "default-src 'self'",
    `script-src 'self' 'nonce-${nonce}' 'strict-dynamic'${dev ? " 'unsafe-eval'" : ''}`,
    "style-src 'self' 'unsafe-inline'",
    "img-src 'self' data: blob: https:",
    "font-src 'self' data:",
    `connect-src ${connect.join(' ')}`,
    `frame-src ${["'self'", ...statusPageOrigins].join(' ')}`,
    "frame-ancestors 'none'",
    "object-src 'none'",
    "base-uri 'self'",
    "form-action 'self'",
  ].join('; ');
}

/**
 * The header the policy ships in. Report-only until WEB_CSP_ENFORCE=true:
 * violations show in the browser console without breaking anything.
 */
export function cspHeaderName(enforce: boolean): string {
  return enforce ? 'Content-Security-Policy' : 'Content-Security-Policy-Report-Only';
}

/** The origin of an absolute http(s) URL, or null. */
export function originOf(url: string | undefined): string | null {
  if (!url) return null;
  try {
    const parsed = new URL(url.trim());
    if (parsed.protocol !== 'http:' && parsed.protocol !== 'https:') return null;
    return parsed.origin;
  } catch {
    return null;
  }
}

/**
 * Where status pages live, mirroring lib/statusPageUrl.ts: the configured
 * NEXT_PUBLIC_STATUS_PAGE_URL, else the dev convention of :8082 on a
 * localhost UI, else the UI's own origin ('self', already allowed).
 */
export function statusPageFrameOrigins(configured: string | undefined, hostname: string, protocol: string): string[] {
  const origin = originOf(configured);
  if (origin) return [origin];
  if (hostname === 'localhost' || hostname === '127.0.0.1') {
    return [`${protocol}//localhost:8082`, `${protocol}//127.0.0.1:8082`];
  }
  return [];
}

/** Strict-Transport-Security value, or null when HSTS must not be sent. */
export function hstsHeader(maxAge: string | undefined, https: boolean): string | null {
  const seconds = Number.parseInt(maxAge ?? '', 10);
  if (!https || !Number.isFinite(seconds) || seconds <= 0) return null;
  return `max-age=${seconds}; includeSubDomains`;
}

/** A fresh 128-bit nonce, base64. */
export function newNonce(): string {
  const bytes = new Uint8Array(16);
  crypto.getRandomValues(bytes);
  let binary = '';
  bytes.forEach((b) => {
    binary += String.fromCharCode(b);
  });
  return btoa(binary);
}
