package mcp

import (
	"net/http"

	"github.com/yassinebenameur/probara/api/internal/models"
)

// identityTools are the superadmin lockout and session actions.
func identityTools() []spec {
	user := map[string]string{"id": "User ID."}
	return []spec{
		{
			Name:  "unlock_user",
			Title: "Unlock user",
			Description: "Clear a user's sign-in lockout (set after repeated failed password logins) and the " +
				"failure count behind it. Superadmins only.",
			Method: http.MethodPost, Route: "/users/{id}/unlock", Platform: true,
			PathDocs: user,
		},
		{
			Name:  "revoke_user_sessions",
			Title: "Sign user out everywhere",
			Description: "End every browser session of a user: refresh tokens and access tokens stop working " +
				"immediately. API keys and MCP connections are not affected. Superadmins only.",
			Method: http.MethodDelete, Route: "/users/{id}/sessions", Platform: true,
			PathDocs: user, Destructive: true,
		},
		{
			Name:  "send_user_invite",
			Title: "Send user invite",
			Description: "Invite a user to sign in. method \"password\" issues a one-time set-password link (7 days; it " +
				"replaces any password the account has); \"sso\" tells them to sign in with single sign-on. The " +
				"invite is emailed when SMTP and APP_BASE_URL are configured and the account has an email; " +
				"otherwise the response carries the link (path, url) to hand over, and it is a credential. " +
				"Superadmins only.",
			Method: http.MethodPost, Route: "/users/{id}/invite", Platform: true,
			PathDocs: user, Body: typeOf[models.SendInviteRequest](),
			Enums: map[string][]string{"method": {"password", "sso"}},
		},
		{
			Name:  "send_password_reset",
			Title: "Send password reset",
			Description: "Issue a one-time password reset link (1 hour) for a local password account. Emailed when " +
				"possible; otherwise the response carries the link to hand over, and it is a credential. Using it " +
				"signs the user out everywhere. Superadmins only.",
			Method: http.MethodPost, Route: "/users/{id}/password-reset", Platform: true,
			PathDocs: user,
		},
		{
			Name:  "reset_user_2fa",
			Title: "Reset user two-factor",
			Description: "Turn off a user's two-factor authentication (lost authenticator and recovery codes): their " +
				"TOTP secret and recovery codes are deleted, so they sign in with the password alone and can " +
				"enroll again. Superadmins only.",
			Method: http.MethodPost, Route: "/users/{id}/reset-2fa", Platform: true,
			PathDocs: user, Destructive: true,
		},
	}
}

// identityNotExposed are the identity routes deliberately left out of the
// catalog, merged into notExposed.
var identityNotExposed = map[string]string{
	"GET /api/v1/account/sessions":                "session management, signed-in user sessions only",
	"DELETE /api/v1/account/sessions/{id}":        "session management, signed-in user sessions only",
	"POST /api/v1/account/sessions/revoke-others": "session management, signed-in user sessions only",
	"POST /api/v1/account/sessions/revoke-all":    "session management, signed-in user sessions only",
	"PATCH /api/v1/account/password":              "own password, signed-in user sessions only",
	"POST /api/v1/account/email":                  "own email, signed-in user sessions only",
	"GET /api/v1/account/2fa":                     "own two-factor, signed-in user sessions only",
	"POST /api/v1/account/2fa/setup":              "own two-factor, signed-in user sessions only",
	"POST /api/v1/account/2fa/enable":             "own two-factor, signed-in user sessions only",
	"POST /api/v1/account/2fa/disable":            "own two-factor, signed-in user sessions only",
	"POST /api/v1/account/2fa/recovery-codes":     "own two-factor, signed-in user sessions only",
	"GET /api/v1/auth/capabilities":               "login page data, public",
	"POST /api/v1/auth/login/mfa":                 "browser session flow; MCP authenticates with its own bearer credential",
	"POST /api/v1/auth/password/forgot":           "public account-recovery flow",
	"POST /api/v1/auth/password/reset":            "public account-recovery flow (one-time link)",
	"POST /api/v1/auth/invite/inspect":            "public invite flow (one-time link)",
	"POST /api/v1/auth/invite/accept":             "public invite flow (one-time link)",
	"POST /api/v1/auth/email/confirm":             "public email-change flow (one-time link)",
}

func init() {
	for route, reason := range identityNotExposed {
		notExposed[route] = reason
	}
}
