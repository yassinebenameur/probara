// Sessions: one per sign-in (password or SSO), lasting across token refreshes.
export interface AdminSession {
  id: string;
  /** Empty for sign-ins made before sessions were tracked. */
  auth_method?: 'password' | 'oidc';
  created_at: string;
  /** Moves at sign-in and at each token refresh, not on every request. */
  last_used_at: string;
  expires_at: string;
  ip?: string;
  user_agent?: string;
  /** Short browser/platform summary of user_agent ("Firefox on macOS"). */
  client?: string;
  /** The session this request was made with. */
  current: boolean;
}

export interface AdminSessionListResponse {
  items: AdminSession[];
}

export interface RevokeSessionsResponse {
  revoked: number;
}

export interface UnlockUserResponse {
  was_locked: boolean;
}
