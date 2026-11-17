package mcp

import (
	"net/http"

	"github.com/yassinebenameur/probara/api/internal/models"
)

// accountActionTools are a superadmin's account actions for another user:
// invites, password reset links and the two-factor reset. A link is returned
// only when it cannot be mailed (no SMTP or APP_BASE_URL on the API, or no
// address on the account).
func accountActionTools() []spec {
	user := map[string]string{"id": "User ID."}
	return []spec{
		{
			Name:  "invite_user",
			Title: "Invite user",
			Description: "Invite a user to set up their account. method \"password\" (the default unless the account is " +
				"linked to the SSO identity provider) issues a single-use set-your-password link valid 7 days, for an " +
				"account that has no password yet; a newer invite replaces it. method \"sso\" tells the user to sign in " +
				"with single sign-on. The link is emailed to the account; without email delivery or an address, the " +
				"response's link is the one copy to hand over (delivered=false, reason says why). Superadmins only.",
			Method: http.MethodPost, Route: "/users/{id}/invite", Platform: true,
			PathDocs: user,
			Body:     typeOf[models.SendInviteRequest](),
			Enums:    map[string][]string{"method": {"password", "sso"}},
		},
		{
			Name:  "create_password_reset_link",
			Title: "Create password reset link",
			Description: "Issue a single-use password reset link (valid 1 hour; a newer one replaces it) for a " +
				"password account. Emailed to the account when email delivery is configured; otherwise the response's " +
				"link is the one copy to hand over. Completing the reset signs the user out everywhere. Not for SSO " +
				"accounts or accounts without a password (invite_user). Superadmins only.",
			Method: http.MethodPost, Route: "/users/{id}/password-reset", Platform: true,
			PathDocs: user,
		},
		{
			Name:  "reset_user_two_factor",
			Title: "Reset user two-factor",
			Description: "Turn a user's two-factor sign-in off (lost authenticator): they sign in with the password " +
				"alone and can enrol again. Removes their recovery codes and cancels a pending email change; sessions " +
				"stay signed in. was_enabled says whether two-factor was on. Superadmins only.",
			Method: http.MethodPost, Route: "/users/{id}/reset-2fa", Platform: true,
			PathDocs: user, Destructive: true,
		},
	}
}

// accountSecurityNotExposed are browser flows: sign-in steps, the link pages
// (the token is the credential, held by a person) and the signed-in user's
// own password, email and second factor, which need that user's session and
// current password.
var accountSecurityNotExposed = map[string]string{
	"POST /api/v1/auth/login/mfa":       "browser sign-in, second step; MCP authenticates with its own bearer credential",
	"GET /api/v1/auth/capabilities":     "public sign-in page data",
	"POST /api/v1/auth/password/forgot": "public forgot-password form; create_password_reset_link covers superadmins",
	"POST /api/v1/auth/password/reset":  "public page behind an emailed reset link",
	"GET /api/v1/auth/invite":           "public page behind an emailed invite link",
	"POST /api/v1/auth/invite/accept":   "public page behind an emailed invite link",
	"POST /api/v1/auth/email/confirm":   "public page behind an emailed confirmation link",

	"GET /api/v1/account/security":                   "the signed-in user's own account, browser sessions only",
	"POST /api/v1/account/password":                  "the signed-in user's own password, browser sessions only",
	"POST /api/v1/account/email":                     "the signed-in user's own email address, browser sessions only",
	"POST /api/v1/account/two-factor/setup":          "the signed-in user's own second factor, browser sessions only",
	"POST /api/v1/account/two-factor/enable":         "the signed-in user's own second factor, browser sessions only",
	"POST /api/v1/account/two-factor/disable":        "the signed-in user's own second factor, browser sessions only; reset_user_two_factor covers superadmins",
	"POST /api/v1/account/two-factor/recovery-codes": "the signed-in user's own recovery codes, browser sessions only",
}

func init() {
	for route, reason := range accountSecurityNotExposed {
		notExposed[route] = reason
	}
}
