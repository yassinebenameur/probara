import type { AdminSession, AdminUser } from './types';

/** Whether password sign-in is locked for the user at `now`. */
export function isLocked(user: Pick<AdminUser, 'locked_until'>, now: Date = new Date()): boolean {
  if (!user.locked_until) return false;
  const until = Date.parse(user.locked_until);
  return Number.isFinite(until) && until > now.getTime();
}

/** How the session signed in, for display; '' when unknown. */
export function sessionMethodLabel(method: AdminSession['auth_method']): string {
  if (method === 'oidc') return 'SSO';
  if (method === 'password') return 'Password';
  return '';
}

/** How a two-factor sign-in passed its second step, for display; '' without one. */
export function sessionSecondFactorLabel(factor: AdminSession['second_factor']): string {
  if (factor === 'totp') return 'Authenticator code';
  if (factor === 'recovery_code') return 'Recovery code';
  return '';
}

/** The session's browser label, falling back when the agent was not recognized. */
export function sessionClientLabel(session: Pick<AdminSession, 'client'>): string {
  return session.client?.trim() || 'Unknown browser';
}

/** This device first, then the most recently used. */
export function sortSessions(sessions: AdminSession[]): AdminSession[] {
  return [...sessions].sort((a, b) => {
    if (a.current !== b.current) return a.current ? -1 : 1;
    return Date.parse(b.last_used_at) - Date.parse(a.last_used_at);
  });
}
