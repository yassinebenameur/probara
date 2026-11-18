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

/**
 * The header the policy ships in. Report-only for this release: violations
 * show in the browser console and nothing is blocked. Switching to the
 * enforcing 'Content-Security-Policy' is a later, separate change.
 */
export const CSP_HEADER = 'Content-Security-Policy-Report-Only';

export interface WebCspOptions {
  nonce: string;
  /** next dev needs eval for React's error overlays and fast refresh. */
  dev: boolean;
  /**
   * Plain-http status-page origins the template editor's preview iframe may
   * load (frame-src), on top of 'self' and any https: origin.
   */
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
    // The template editor previews status pages in an iframe. https: because
    // a status page can be served from any host; the configured or dev
    // origins add plain-http hosts (localhost:8082).
    `frame-src ${["'self'", 'https:', ...statusPageOrigins.filter((o) => !o.startsWith('https:'))].join(' ')}`,
    "frame-ancestors 'none'",
    "object-src 'none'",
    "base-uri 'self'",
    "form-action 'self'",
  ].join('; ');
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
 * The status-page URL the deployment configured, for the CSP: the UI
 * server's STATUS_PAGE_BASE_URL, read per request at runtime (the published
 * image is built once, so a NEXT_PUBLIC_ value is frozen empty in it), else
 * the build-time NEXT_PUBLIC_STATUS_PAGE_URL of a development build. The
 * links themselves get the same value from the API (lib/statusPageUrl.ts).
 */
export function configuredStatusPageUrl(runtime: string | undefined, buildTime: string | undefined): string | undefined {
  return originOf(runtime) ? runtime : buildTime;
}

/**
 * Where status pages live, mirroring lib/statusPageUrl.ts: the configured
 * URL (configuredStatusPageUrl), else the dev convention of :8082 on a
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
