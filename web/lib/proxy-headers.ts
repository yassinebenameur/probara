// Request headers the /api proxy route (app/api/[...path]/route.ts) must not
// forward to the API.
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

// The API believes X-Forwarded-For / X-Real-IP only from peers listed in
// TRUSTED_PROXY_CIDRS (shared/clientip). A Next.js route handler cannot see
// its own TCP peer, so this proxy can never append to the chain the way a
// real reverse proxy does; passing a browser's value through unchanged would
// let anyone who reaches the UI directly forge the IP the API records once
// the frontend's address is trusted. Drop them unless the operator declares
// (API_PROXY_TRUST_FORWARDED_FOR=true) that an appending proxy they run sits
// in front of the UI, and only then list the frontend in TRUSTED_PROXY_CIDRS.
const CLIENT_IP_HEADERS = new Set(['x-forwarded-for', 'x-real-ip']);

export function copyProxyRequestHeaders(requestHeaders: Headers, trustForwardedFor: boolean): Headers {
  const headers = new Headers();
  requestHeaders.forEach((value, key) => {
    const lower = key.toLowerCase();
    if (HOP_BY_HOP_HEADERS.has(lower)) return;
    if (!trustForwardedFor && CLIENT_IP_HEADERS.has(lower)) return;
    headers.set(key, value);
  });
  return headers;
}
