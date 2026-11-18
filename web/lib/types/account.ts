// Sessions: one per sign-in (password or SSO), lasting across token refreshes.
export interface AdminSession {
  id: string;
  /** Empty for sign-ins made before sessions were tracked. */
  auth_method?: 'password' | 'oidc';
  /** How a two-factor sign-in passed its second step; absent without one. */
  second_factor?: 'totp' | 'recovery_code';
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

// Account self-service and two-factor sign-in.

/** The password step's answer for an account with two-factor sign-in on. */
export interface MfaChallengeResponse {
  mfa_required: true;
  mfa_token: string;
  expires_at: string;
}

export interface AuthCapabilities {
  /** SMTP and APP_BASE_URL are set: the forgot-password form mails links. */
  email_delivery: boolean;
}

export interface InviteInfo {
  username: string;
  expires_at: string;
}

export interface TwoFactorSettings {
  enabled: boolean;
  enabled_at?: string;
  recovery_codes_remaining: number;
  /** False without PROBARA_SECRETS_KEY on the API. */
  enrolment_available: boolean;
}

/** The signed-in user's sign-in state (account page). */
export interface AccountSecurity {
  auth_method: 'password' | 'oidc';
  /** A password account without a linked identity provider. */
  local: boolean;
  has_password: boolean;
  email?: string;
  pending_email?: string;
  pending_email_expires_at?: string;
  email_delivery: boolean;
  two_factor: TwoFactorSettings;
}

export interface EmailChangePendingResponse {
  pending_email: string;
  expires_at: string;
}

export interface TotpSetupResponse {
  secret: string;
  otpauth_uri: string;
}

export interface RecoveryCodesResponse {
  recovery_codes: string[];
}

/** A superadmin's invite or reset link: mailed, or returned to hand over. */
export interface AccountLinkResponse {
  method: 'password' | 'sso';
  delivered: boolean;
  email?: string;
  expires_at?: string;
  /** Absolute with APP_BASE_URL, else a path on this UI. Shown once. */
  link?: string;
  reason?: string;
}

export interface ResetTwoFactorResponse {
  was_enabled: boolean;
}
