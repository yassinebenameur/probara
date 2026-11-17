// Pages rendered outside the authenticated shell (no AuthGuard, no sidebar):
// sign-in, API-key connect, the MCP consent screen, and the pages behind
// emailed links, whose token is their only credential. Layout and AuthGuard
// both read this list, so a page cannot be exempt from one and not the other.
export const PUBLIC_ROUTES: readonly string[] = [
  '/connect',
  '/login',
  '/oauth/consent',
  '/alert-action',
  '/report-unsubscribe',
  '/forgot-password',
  '/reset-password',
  '/accept-invite',
  '/confirm-email',
];

export function isPublicRoute(pathname: string | null): boolean {
  return pathname !== null && PUBLIC_ROUTES.includes(pathname);
}
