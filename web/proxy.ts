import { NextRequest, NextResponse } from 'next/server';
import {
  CSP_HEADER,
  buildWebCsp,
  configuredStatusPageUrl,
  newNonce,
  originOf,
  statusPageFrameOrigins,
} from '@/lib/csp';

// Per-request Content-Security-Policy (lib/csp.ts), report-only for this
// release. The policy goes on the request too: Next.js reads the nonce from
// it while rendering and stamps it on its own scripts, and app/layout.tsx
// reads x-nonce for the inline boot script. Static headers (nosniff,
// framing, referrer, permissions) live in next.config.js.
export function proxy(request: NextRequest) {
  const nonce = newNonce();
  const policy = buildWebCsp({
    nonce,
    dev: process.env.NODE_ENV === 'development',
    statusPageOrigins: statusPageFrameOrigins(
      configuredStatusPageUrl(process.env.STATUS_PAGE_BASE_URL, process.env.NEXT_PUBLIC_STATUS_PAGE_URL),
      request.nextUrl.hostname,
      request.nextUrl.protocol
    ),
    apiOrigin: originOf(process.env.NEXT_PUBLIC_API_URL) ?? undefined,
  });

  const requestHeaders = new Headers(request.headers);
  requestHeaders.set('x-nonce', nonce);
  requestHeaders.set(CSP_HEADER, policy);

  const response = NextResponse.next({ request: { headers: requestHeaders } });
  response.headers.set(CSP_HEADER, policy);
  return response;
}

export const config = {
  matcher: [
    // Pages only: not the API and asset proxies (the API sets its own
    // headers), static assets, or prefetches.
    {
      // Each exclusion ends at a path boundary, so a page such as
      // /api-keys still gets the policy.
      source: '/((?!(?:api|static|\\.well-known|_next/static|_next/image)(?:/|$)|favicon\\.ico$|icon\\.svg$).*)',
      missing: [
        { type: 'header', key: 'next-router-prefetch' },
        { type: 'header', key: 'purpose', value: 'prefetch' },
      ],
    },
  ],
};
