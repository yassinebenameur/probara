import { ACTION_TOKEN_HEADER } from '../alert-action';
import { apiErrorFromBody } from '../errors';
import type {
  AccountLinkResponse,
  AccountSecurity,
  AdminSession,
  AdminSessionListResponse,
  AuthCapabilities,
  EmailChangePendingResponse,
  InviteInfo,
  RecoveryCodesResponse,
  ResetTwoFactorResponse,
  RevokeSessionsResponse,
  TotpSetupResponse,
  UnlockUserResponse,
} from '../types';
import { apiRequest, getApiUrl } from './core';

// The signed-in user's own sessions (browser sessions only; not tenant-scoped).
export async function getMySessions(): Promise<AdminSession[]> {
  return (await apiRequest<AdminSessionListResponse>('GET', '/v1/account/sessions')).items || [];
}

export async function revokeMySession(id: string): Promise<void> {
  return apiRequest<void>('DELETE', `/v1/account/sessions/${id}`);
}

export async function revokeMyOtherSessions(): Promise<RevokeSessionsResponse> {
  return apiRequest<RevokeSessionsResponse>('POST', '/v1/account/sessions/revoke-others');
}

// Another user's sign-in state (superadmin only).
export async function getUserSessions(userId: string): Promise<AdminSession[]> {
  return (await apiRequest<AdminSessionListResponse>('GET', `/v1/users/${userId}/sessions`)).items || [];
}

export async function revokeUserSession(userId: string, sessionId: string): Promise<void> {
  return apiRequest<void>('DELETE', `/v1/users/${userId}/sessions/${sessionId}`);
}

export async function revokeUserSessions(userId: string): Promise<RevokeSessionsResponse> {
  return apiRequest<RevokeSessionsResponse>('DELETE', `/v1/users/${userId}/sessions`);
}

export async function unlockUser(userId: string): Promise<UnlockUserResponse> {
  return apiRequest<UnlockUserResponse>('POST', `/v1/users/${userId}/unlock`);
}

export async function inviteUser(userId: string, method?: 'password' | 'sso'): Promise<AccountLinkResponse> {
  return apiRequest<AccountLinkResponse>('POST', `/v1/users/${userId}/invite`, method ? { method } : undefined);
}

export async function createPasswordResetLink(userId: string): Promise<AccountLinkResponse> {
  return apiRequest<AccountLinkResponse>('POST', `/v1/users/${userId}/password-reset`);
}

export async function resetUserTwoFactor(userId: string): Promise<ResetTwoFactorResponse> {
  return apiRequest<ResetTwoFactorResponse>('POST', `/v1/users/${userId}/reset-2fa`);
}

// The signed-in user's password, email and second factor (browser sessions
// only; a wrong current password is a 400, never a 401).
export async function getAccountSecurity(): Promise<AccountSecurity> {
  return apiRequest<AccountSecurity>('GET', '/v1/account/security');
}

export async function changeMyPassword(currentPassword: string, newPassword: string): Promise<RevokeSessionsResponse> {
  return apiRequest<RevokeSessionsResponse>('POST', '/v1/account/password', {
    current_password: currentPassword,
    new_password: newPassword,
  });
}

export async function changeMyEmail(currentPassword: string, newEmail: string): Promise<EmailChangePendingResponse> {
  return apiRequest<EmailChangePendingResponse>('POST', '/v1/account/email', {
    current_password: currentPassword,
    new_email: newEmail,
  });
}

export async function startTwoFactorSetup(currentPassword: string): Promise<TotpSetupResponse> {
  return apiRequest<TotpSetupResponse>('POST', '/v1/account/two-factor/setup', { current_password: currentPassword });
}

export async function enableTwoFactor(code: string): Promise<RecoveryCodesResponse> {
  return apiRequest<RecoveryCodesResponse>('POST', '/v1/account/two-factor/enable', { code });
}

export async function disableTwoFactor(currentPassword: string, code: string): Promise<RevokeSessionsResponse> {
  return apiRequest<RevokeSessionsResponse>('POST', '/v1/account/two-factor/disable', {
    current_password: currentPassword,
    code,
  });
}

export async function regenerateRecoveryCodes(currentPassword: string, code: string): Promise<RecoveryCodesResponse> {
  return apiRequest<RecoveryCodesResponse>('POST', '/v1/account/two-factor/recovery-codes', {
    current_password: currentPassword,
    code,
  });
}

// Public routes behind emailed links and the sign-in page. They run signed
// out, so they are plain fetches (apiRequest would refresh and redirect on a
// 401); a link's token goes in the X-Action-Token header, never the URL.

async function publicRequest<T>(method: string, path: string, token?: string, body?: unknown): Promise<T> {
  const headers: Record<string, string> = {};
  if (token !== undefined) headers[ACTION_TOKEN_HEADER] = token;
  if (body !== undefined) headers['Content-Type'] = 'application/json';
  const res = await fetch(getApiUrl(path), {
    method,
    headers,
    body: body === undefined ? undefined : JSON.stringify(body),
    credentials: 'include',
    cache: 'no-store',
  });
  const data = await res.json().catch(() => null);
  if (!res.ok) {
    if (data) throw apiErrorFromBody(data);
    throw new Error(`The request failed (HTTP ${res.status}). Please retry.`);
  }
  return data as T;
}

export async function getAuthCapabilities(): Promise<AuthCapabilities> {
  try {
    return await publicRequest<AuthCapabilities>('GET', '/v1/auth/capabilities');
  } catch {
    return { email_delivery: false };
  }
}

export async function requestPasswordReset(login: string): Promise<void> {
  await publicRequest<unknown>('POST', '/v1/auth/password/forgot', undefined, { login });
}

export async function resetPasswordWithLink(token: string, password: string): Promise<void> {
  await publicRequest<unknown>('POST', '/v1/auth/password/reset', token, { password });
}

export async function getInviteInfo(token: string): Promise<InviteInfo> {
  return publicRequest<InviteInfo>('GET', '/v1/auth/invite', token);
}

export async function acceptInvite(token: string, password: string): Promise<void> {
  await publicRequest<unknown>('POST', '/v1/auth/invite/accept', token, { password });
}

export async function confirmEmailChange(token: string): Promise<{ email: string }> {
  return publicRequest<{ email: string }>('POST', '/v1/auth/email/confirm', token);
}
