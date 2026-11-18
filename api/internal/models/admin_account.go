package models

import "time"

// Account self-service, account links and two-factor sign-in
// (api/internal/handlers/auth, handlers/account, handlers/users).

// MFAChallengeResponse is the first step's answer for a correct password on
// an account with two-factor sign-in on: no session yet, a single-use
// challenge for POST /auth/login/mfa.
type MFAChallengeResponse struct {
	MFARequired bool      `json:"mfa_required"`
	MFAToken    string    `json:"mfa_token"`
	ExpiresAt   time.Time `json:"expires_at"`
}

// LoginMFARequest is the second step: the challenge and a code from the
// authenticator app, or a recovery code.
type LoginMFARequest struct {
	MFAToken string `json:"mfa_token"`
	Code     string `json:"code"`
}

// AuthCapabilities tells the public pages what this installation offers.
type AuthCapabilities struct {
	// EmailDelivery: SMTP and APP_BASE_URL are configured, so the
	// forgot-password form mails reset links.
	EmailDelivery bool `json:"email_delivery"`
}

// ForgotPasswordRequest names the account by username or email address.
type ForgotPasswordRequest struct {
	Login string `json:"login"`
}

// SetPasswordRequest is the new password chosen through an invite or reset
// link (the link's token rides in the X-Action-Token header).
type SetPasswordRequest struct {
	Password string `json:"password"`
}

// InviteInfo is the account an invite link sets up.
type InviteInfo struct {
	Username  string    `json:"username"`
	ExpiresAt time.Time `json:"expires_at"`
}

// EmailConfirmedResponse is the address an email-change link just set.
type EmailConfirmedResponse struct {
	Email string `json:"email"`
}

// AccountSecurity is the signed-in user's sign-in state for the account
// page.
type AccountSecurity struct {
	// AuthMethod is "password" or "oidc" (an identity provider is linked).
	AuthMethod string `json:"auth_method"`
	// Local: a password account without a linked identity provider, the
	// only kind that may change its password or email or use two-factor
	// sign-in here.
	Local       bool    `json:"local"`
	HasPassword bool    `json:"has_password"`
	Email       *string `json:"email,omitempty"`
	// PendingEmail is an address waiting for confirmation, if any.
	PendingEmail          *string    `json:"pending_email,omitempty"`
	PendingEmailExpiresAt *time.Time `json:"pending_email_expires_at,omitempty"`
	// EmailDelivery: account mail can be sent (SMTP and APP_BASE_URL).
	EmailDelivery bool              `json:"email_delivery"`
	TwoFactor     TwoFactorSettings `json:"two_factor"`
}

// TwoFactorSettings is a user's second-factor state.
type TwoFactorSettings struct {
	Enabled                bool       `json:"enabled"`
	EnabledAt              *time.Time `json:"enabled_at,omitempty"`
	RecoveryCodesRemaining int        `json:"recovery_codes_remaining"`
	// EnrolmentAvailable is false without PROBARA_SECRETS_KEY (the
	// authenticator secret could not be stored encrypted) or before the
	// database is migrated.
	EnrolmentAvailable bool `json:"enrolment_available"`
}

// ChangePasswordRequest changes the signed-in user's own password.
type ChangePasswordRequest struct {
	CurrentPassword string `json:"current_password"`
	NewPassword     string `json:"new_password"`
}

// ChangeEmailRequest asks for the signed-in user's address to change; a
// confirmation link goes to the new address.
type ChangeEmailRequest struct {
	CurrentPassword string `json:"current_password"`
	NewEmail        string `json:"new_email"`
}

// EmailChangePendingResponse is a requested change awaiting confirmation.
type EmailChangePendingResponse struct {
	PendingEmail string    `json:"pending_email"`
	ExpiresAt    time.Time `json:"expires_at"`
}

// PasswordConfirmRequest re-checks the current password before a change.
type PasswordConfirmRequest struct {
	CurrentPassword string `json:"current_password"`
}

// TOTPSetupResponse is a pending enrolment: the secret for manual entry and
// the otpauth:// URI the page draws as a QR code. Shown once.
type TOTPSetupResponse struct {
	Secret     string `json:"secret"`
	OTPAuthURI string `json:"otpauth_uri"`
}

// TOTPCodeRequest confirms an enrolment with a code from the app.
type TOTPCodeRequest struct {
	Code string `json:"code"`
}

// TwoFactorProofRequest is the current password and a code from the app (or
// a recovery code), for turning two-factor off or replacing recovery codes.
type TwoFactorProofRequest struct {
	CurrentPassword string `json:"current_password"`
	Code            string `json:"code"`
}

// RecoveryCodesResponse carries new recovery codes, shown once.
type RecoveryCodesResponse struct {
	RecoveryCodes []string `json:"recovery_codes"`
}

// SendInviteRequest picks how an invited account signs in: "password" (a
// set-your-password link) or "sso" (a note to sign in with single sign-on).
// Empty means "sso" for an account linked to the identity provider, else
// "password".
type SendInviteRequest struct {
	Method string `json:"method,omitempty"`
}

// AccountLinkResponse answers a superadmin's invite or reset-link request.
// With email delivery the link is mailed to the account and never returned;
// without it (or without an address on the account) Link is returned once
// for the superadmin to hand over, and never logged.
type AccountLinkResponse struct {
	// Method is "password" or "sso".
	Method    string     `json:"method"`
	Delivered bool       `json:"delivered"`
	Email     *string    `json:"email,omitempty"`
	ExpiresAt *time.Time `json:"expires_at,omitempty"`
	// Link is absolute with APP_BASE_URL, else a path on the operator UI.
	Link string `json:"link,omitempty"`
	// Reason says why the link was not mailed.
	Reason string `json:"reason,omitempty"`
}

// ResetTwoFactorResponse reports whether a second factor was on.
type ResetTwoFactorResponse struct {
	WasEnabled bool `json:"was_enabled"`
}
