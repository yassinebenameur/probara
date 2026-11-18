// Server-side helpers for the route handlers that forward to the API
// (app/api, app/static/collector, app/.well-known). Not for client code.

export const API_PROXY_TARGET = process.env.API_PROXY_TARGET || 'http://localhost:8080';

// The API believes X-Forwarded-For / X-Real-IP only from peers listed in
// TRUSTED_PROXY_CIDRS (shared/clientip). Next.js keeps a client-sent
// X-Forwarded-For and fills it from the socket only when it is absent, and a
// route handler cannot see its own TCP peer, so this proxy cannot tell a
// browser's forged value from a real one. It drops both headers (the API then
// records this frontend's address) unless the operator declares, with
// API_PROXY_TRUST_FORWARDED_FOR=true, that a proxy they run in front of the
// UI replaces or appends to X-Forwarded-For; only then list the frontend in
// the API's TRUSTED_PROXY_CIDRS.
const API_PROXY_TRUST_FORWARDED_FOR = process.env.API_PROXY_TRUST_FORWARDED_FOR === 'true';
const CLIENT_IP_HEADERS = new Set(['x-forwarded-for', 'x-real-ip']);

const HOP_BY_HOP_HEADERS = new Set([
  'connection',
  'keep-alive',
  'proxy-authenticate',
  'proxy-authorization',
  'te',
  'trailer',
  'transfer-encoding',
  'upgrade',
  'host',
  'content-length',
]);

export type ProxyRouteContext = {
  params: Promise<{ path: string[] }>;
};

/** `<API>/<prefix>/<path…>` with the incoming query string. */
export function buildTargetUrl(requestUrl: string, prefix: string, path: string[]): string {
  const incoming = new URL(requestUrl);
  return `${API_PROXY_TARGET}/${prefix}/${path.join('/')}${incoming.search || ''}`;
}

/**
 * The request headers minus the hop-by-hop ones, and minus the client-IP
 * headers unless forwarding them is trusted (see
 * API_PROXY_TRUST_FORWARDED_FOR above).
 */
export function copyHeaders(
  requestHeaders: Headers,
  trustForwardedFor: boolean = API_PROXY_TRUST_FORWARDED_FOR,
): Headers {
  const headers = new Headers();
  requestHeaders.forEach((value, key) => {
    const lower = key.toLowerCase();
    if (HOP_BY_HOP_HEADERS.has(lower)) return;
    if (!trustForwardedFor && CLIENT_IP_HEADERS.has(lower)) return;
    headers.set(key, value);
  });
  return headers;
}

/** The upstream response headers, with each Set-Cookie kept as its own header. */
export function copyResponseHeaders(upstream: Response): Headers {
  const headers = new Headers();
  upstream.headers.forEach((value, key) => {
    const lower = key.toLowerCase();
    if (lower === 'content-length' || lower === 'set-cookie') {
      return;
    }
    headers.append(key, value);
  });

  // Preserve each cookie header independently. Collapsing them into one
  // header can break parsing and cause immediate auth logout.
  const getSetCookie = (upstream.headers as unknown as { getSetCookie?: () => string[] }).getSetCookie;
  let setCookies = typeof getSetCookie === 'function' ? getSetCookie.call(upstream.headers) : [];

  // Fallback for environments where getSetCookie is unavailable.
  if (setCookies.length === 0) {
    const combined = upstream.headers.get('set-cookie');
    if (combined) {
      // Cookies emitted by this API use Max-Age (no Expires), so splitting
      // on ", " between cookie name-value pairs is safe here.
      setCookies = combined
        .split(/,\s*(?=[^;,=\s]+=[^;,]+)/g)
        .map((v) => v.trim())
        .filter(Boolean);
    }
  }

  for (const cookie of setCookies) {
    headers.append('set-cookie', cookie);
  }

  return headers;
}

// Cross-site request forgery: the API refuses a session-cookie write from a
// browser page that is not the operator UI (api/internal/middleware/origin.go).
// Behind this proxy the API cannot see which host the browser used -- Host is
// the API's internal name -- but this proxy can: the Host (or the fronting
// proxy's X-Forwarded-Host) of the request it received is the host the
// browser sent the request to. A cross-site page cannot change it: the
// browser sets Host from the URL, and a custom header such as
// X-Forwarded-Host needs a CORS preflight, which the API refuses to unlisted
// origins. So when the page's origin equals this proxy's own origin, the
// request comes from the UI, and the proxy drops Origin, Referer and
// Sec-Fetch-Site: the API reads a request without them as one no browser
// attached a cookie to on another page's behalf, and lets it through with no
// origin configuration at all. Any other request keeps its headers and the
// API judges it (APP_BASE_URL, API_ALLOWED_ORIGINS, its own origin).
const BROWSER_ORIGIN_HEADERS = ['origin', 'referer', 'sec-fetch-site'];

function safeMethod(method: string): boolean {
  const m = method.toUpperCase();
  return m === 'GET' || m === 'HEAD' || m === 'OPTIONS';
}

/** `host:port` of an http(s) origin, the scheme's default port made explicit; null otherwise. */
function originHostPort(origin: string): { scheme: string; hostPort: string } | null {
  let url: URL;
  try {
    url = new URL(origin);
  } catch {
    return null;
  }
  if ((url.protocol !== 'http:' && url.protocol !== 'https:') || !url.hostname) return null;
  const scheme = url.protocol.slice(0, -1);
  const port = url.port || (scheme === 'https' ? '443' : '80');
  return { scheme, hostPort: `${url.hostname.toLowerCase()}:${port}` };
}

/** The hosts this proxy was reached as: the fronting proxy's forwarded host, the Host header, the URL. */
function ownHosts(incoming: Headers, requestUrl: string): string[] {
  const hosts: string[] = [];
  const forwarded = incoming.get('x-forwarded-host');
  if (forwarded) hosts.push(forwarded.split(',')[0].trim());
  const host = incoming.get('host');
  if (host) hosts.push(host.trim());
  try {
    hosts.push(new URL(requestUrl).host);
  } catch {
    // Not a URL: nothing to add.
  }
  return hosts.filter(Boolean);
}

function reachedOverHttps(incoming: Headers, requestUrl: string): boolean {
  const proto = incoming.get('x-forwarded-proto');
  if (proto) return proto.split(',')[0].trim().toLowerCase() === 'https';
  try {
    return new URL(requestUrl).protocol === 'https:';
  } catch {
    return false;
  }
}

/**
 * Whether a write comes from a page on this proxy's own origin, judged on the
 * first of Origin, Sec-Fetch-Site and Referer it carries (the API's order).
 * False when it carries none: that is not a browser, and the API lets it
 * through unchanged anyway.
 */
export function isSameOriginWrite(method: string, incoming: Headers, requestUrl: string): boolean {
  if (safeMethod(method)) return false;
  const origin = incoming.get('origin');
  const site = incoming.get('sec-fetch-site');
  let page: string | null = null;
  if (origin) {
    page = origin;
  } else if (site) {
    // Relative to this proxy's URL, so same-origin means the UI's own page.
    return site.trim().toLowerCase() === 'same-origin';
  } else {
    page = incoming.get('referer');
  }
  if (!page) return false;
  const from = originHostPort(page);
  if (!from) return false;
  // The scheme counts only when this request is known to be HTTPS (as the
  // API does): behind a TLS proxy that sets no X-Forwarded-Proto it reads as
  // http while the page is https.
  if (reachedOverHttps(incoming, requestUrl) && from.scheme !== 'https') return false;
  return ownHosts(incoming, requestUrl).some((host) => {
    const own = originHostPort(`${from.scheme}://${host}`);
    return own !== null && own.hostPort === from.hostPort;
  });
}

/** Drops the browser-origin headers from outgoing when the write comes from this proxy's own origin (see above). */
export function vouchForSameOriginWrite(method: string, incoming: Headers, outgoing: Headers, requestUrl: string): void {
  if (!isSameOriginWrite(method, incoming, requestUrl)) return;
  for (const name of BROWSER_ORIGIN_HEADERS) outgoing.delete(name);
}

export function badGateway(): Response {
  return Response.json({ error: 'bad_gateway', message: 'Failed to reach API upstream' }, { status: 502 });
}
