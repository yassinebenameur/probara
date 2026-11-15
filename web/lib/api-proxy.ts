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

export function badGateway(): Response {
  return Response.json({ error: 'bad_gateway', message: 'Failed to reach API upstream' }, { status: 502 });
}
