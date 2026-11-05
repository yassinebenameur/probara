// Account links (invites, password resets, email confirmation) carry their
// one-time token in the URL fragment: /accept-invite#token=…. Browsers never
// send the fragment to a server, so the token stays out of proxy and access
// logs; the page reads it here and posts it to the API in a body.

/** The token in a location hash ("#token=…"), or "" when there is none. */
export function readLinkToken(hash: string): string {
  const raw = hash.startsWith('#') ? hash.slice(1) : hash;
  const value = new URLSearchParams(raw).get('token');
  return value ? value.trim() : '';
}

/**
 * Routes rendered without the app shell and without a session: sign-in, the
 * consent and connect flows, signed alert actions, and the account links.
 */
export function isBareRoute(pathname: string): boolean {
  return (
    pathname === '/connect' ||
    pathname === '/login' ||
    pathname === '/oauth/consent' ||
    pathname === '/accept-invite' ||
    pathname === '/reset-password' ||
    pathname === '/confirm-email' ||
    pathname.startsWith('/alert-action/') ||
    pathname.startsWith('/report-unsubscribe/')
  );
}
