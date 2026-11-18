import type {
  AdminSession,
  AdminSessionListResponse,
  RevokeSessionsResponse,
  UnlockUserResponse,
} from '../types';
import { apiRequest } from './core';

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
