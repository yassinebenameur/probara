import { NextRequest, NextResponse } from 'next/server';
import { buildWebCsp, cspHeaderName, hstsHeader, newNonce, originOf, statusPageFrameOrigins } from '@/lib/csp';

// Per-request Content-Security-Policy (lib/csp.ts). The policy goes on the
// request too: Next.js reads the nonce from it while rendering and stamps
// it on its own scripts, and app/layout.tsx reads x-nonce for the inline
// boot script. Static headers (nosniff, framing, referrer) live in
// next.config.js.
//
// Report-only for one release: WEB_CSP_ENFORCE=true switches to enforcing.
export function proxy(request: NextRequest) {
  const nonce = newNonce();
  const policy = buildWebCsp({
    nonce,
    dev: process.env.NODE_ENV === 'development',
    statusPageOrigins: statusPageFrameOrigins(
      process.env.NEXT_PUBLIC_STATUS_PAGE_URL,
      request.nextUrl.hostname,
      request.nextUrl.protocol
    ),
    apiOrigin: originOf(process.env.NEXT_PUBLIC_API_URL) ?? undefined,
  });
  const header = cspHeaderName(process.env.WEB_CSP_ENFORCE === 'true');

  const requestHeaders = new Headers(request.headers);
  requestHeaders.set('x-nonce', nonce);
  requestHeaders.set(header, policy);

  const response = NextResponse.next({ request: { headers: requestHeaders } });
  response.headers.set(header, policy);

  // The UI has no trusted-proxy list, but browsers ignore HSTS received over
  // plain HTTP, so believing X-Forwarded-Proto here can only add a header a
  // real HTTPS hop would have earned anyway.
  const forwardedProto = request.headers.get('x-forwarded-proto')?.split(',')[0]?.trim().toLowerCase();
  const https = request.nextUrl.protocol === 'https:' || forwardedProto === 'https';
  const sts = hstsHeader(process.env.SECURITY_HSTS_MAX_AGE, https);
  if (sts) response.headers.set('Strict-Transport-Security', sts);

  return response;
}

export const config = {
  matcher: [
    // Pages only: not the /api proxy (the API sets its own headers), static
    // assets, or prefetches.
    {
      source: '/((?!api|_next/static|_next/image|favicon.ico|icon.svg).*)',
      missing: [
        { type: 'header', key: 'next-router-prefetch' },
        { type: 'header', key: 'purpose', value: 'prefetch' },
      ],
    },
  ],
};
