import type { AdminUser } from '../types';
import { getApiUrl } from './core';

// Session probes run where a redirect would be wrong (the auth guard decides
// where to go, the connect page is testing a key the user is still typing), so
// they are plain fetches against the client's base URL rather than apiRequest,
// whose 401 handling refreshes the session and navigates to /login.

/** Whether a cookie session is live, and its user. Never refreshes or redirects. */
export async function getSession(): Promise<{ authenticated: boolean; user: AdminUser | null }> {
  const response = await fetch(getApiUrl('/v1/auth/me'), { credentials: 'include' });
  if (!response.ok) {
    return { authenticated: false, user: null };
  }
  // A 2xx is a live session even if the body cannot be read.
  const data = await response.json().catch(() => null);
  return { authenticated: true, user: (data?.user as AdminUser) ?? null };
}

/** Ends the cookie session. */
export async function logout(): Promise<void> {
  await fetch(getApiUrl('/v1/auth/logout'), { method: 'POST', credentials: 'include' });
}

/** False when the server rejects the key (401); throws when it cannot be checked. */
export async function validateApiKey(apiKey: string): Promise<boolean> {
  const response = await fetch(getApiUrl('/v1/monitors?page_size=1'), {
    headers: { Authorization: `Bearer ${apiKey}` },
  });
  if (response.status === 401) {
    return false;
  }
  if (!response.ok) {
    throw new Error('Failed to validate API key');
  }
  return true;
}
