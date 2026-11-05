package models

import "time"

// AuthCapabilities tells the login page which flows this installation
// offers (GET /auth/capabilities, public).
type AuthCapabilities struct {
	// PasswordReset: "Forgot password" works (SMTP and APP_BASE_URL set).
	PasswordReset bool `json:"password_reset"`
	// EmailDelivery: account mail (invites, email change) can be sent.
	EmailDelivery bool   `json:"email_delivery"`
	OIDC          bool   `json:"oidc"`
	OIDCLabel     string `json:"oidc_label,omitempty"`
}

// MFAChallengeResponse is POST /auth/login's answer when the password was
// right and the account has two-factor enabled. No session is issued yet:
// POST /auth/login/mfa with the token and a code completes the sign-in.
type MFAChallengeResponse struct {
	MFARequired bool   `json:"mfa_required"`
	MFAToken    string `json:"mfa_token"`
	// ExpiresIn is the token's lifetime in seconds.
	ExpiresIn int `json:"expires_in"`
}

// MFALoginRequest completes a two-factor sign-in. Code is a 6-digit TOTP
// code or a recovery code.
type MFALoginRequest struct {
	MFAToken string `json:"mfa_token"`
	Code     string `json:"code"`
}

// ForgotPasswordRequest starts a reset. Login is a username or an email.
type ForgotPasswordRequest struct {
	Login string `json:"login"`
}

// TokenPasswordRequest redeems an invite or reset link.
type TokenPasswordRequest struct {
	Token    string `json:"token"`
	Password string `json:"password"`
}

// TokenRequest carries a one-time link token in the body (never the path).
type TokenRequest struct {
	Token string `json:"token"`
}

// InviteInfo is what the accept-invite page shows before the password is set.
type InviteInfo struct {
	Username  string    `json:"username"`
	Email     *string   `json:"email,omitempty"`
	ExpiresAt time.Time `json:"expires_at"`
}

// EmailConfirmedResponse reports a completed email change.
type EmailConfirmedResponse struct {
	Email string `json:"email"`
}

// ChangePasswordRequest is PATCH /account/password.
type ChangePasswordRequest struct {
	CurrentPassword string `json:"current_password"`
	NewPassword     string `json:"new_password"`
}

// ChangeEmailRequest is POST /account/email.
type ChangeEmailRequest struct {
	NewEmail        string `json:"new_email"`
	CurrentPassword string `json:"current_password"`
}

// EmailChangePendingResponse: a confirmation link went to PendingEmail.
type EmailChangePendingResponse struct {
	PendingEmail string    `json:"pending_email"`
	ExpiresAt    time.Time `json:"expires_at"`
}

// AccountMFAStatus is GET /account/2fa.
type AccountMFAStatus struct {
	// Available is false before the database is migrated.
	Available bool `json:"available"`
	// Eligible: a local password account (SSO accounts use the IdP's MFA).
	Eligible               bool       `json:"eligible"`
	Enabled                bool       `json:"enabled"`
	EnabledAt              *time.Time `json:"enabled_at,omitempty"`
	RecoveryCodesRemaining int        `json:"recovery_codes_remaining"`
}

// PasswordConfirmRequest re-confirms the current password.
type PasswordConfirmRequest struct {
	CurrentPassword string `json:"current_password"`
}

// TOTPSetupResponse is a pending enrollment: add Secret (or scan
// OTPAuthURI as a QR code) in an authenticator app, then confirm a code.
type TOTPSetupResponse struct {
	Secret     string `json:"secret"`
	OTPAuthURI string `json:"otpauth_uri"`
}

// TOTPCodeRequest confirms an enrollment.
type TOTPCodeRequest struct {
	Code string `json:"code"`
}

// TOTPProofRequest proves both factors for a second-factor change.
type TOTPProofRequest struct {
	CurrentPassword string `json:"current_password"`
	Code            string `json:"code"`
}

// RecoveryCodesResponse lists fresh recovery codes, shown once.
type RecoveryCodesResponse struct {
	RecoveryCodes []string `json:"recovery_codes"`
}

// SendInviteRequest is POST /users/{id}/invite. Method "sso" sends a
// sign-in-with-SSO notice instead of a set-password link; empty picks
// "sso" for an account linked to the IdP, or for one without a password
// while SSO is enabled, and "password" otherwise.
type SendInviteRequest struct {
	Method string `json:"method,omitempty"`
}

// AccountLinkResponse reports an invite or reset a superadmin issued.
// Delivered: it was emailed. Otherwise Path (UI path with the token in the
// fragment) and, with APP_BASE_URL, URL are the link to hand over.
type AccountLinkResponse struct {
	Method    string     `json:"method"`
	Delivered bool       `json:"delivered"`
	Email     *string    `json:"email,omitempty"`
	Path      string     `json:"path,omitempty"`
	URL       string     `json:"url,omitempty"`
	ExpiresAt *time.Time `json:"expires_at,omitempty"`
	// Reason says why it was not emailed.
	Reason string `json:"reason,omitempty"`
}

// ResetMFAResponse reports whether a superadmin reset turned 2FA off.
type ResetMFAResponse struct {
	WasEnabled bool `json:"was_enabled"`
}
