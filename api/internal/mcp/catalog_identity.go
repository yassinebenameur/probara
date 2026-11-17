package mcp

import "net/http"

// identityTools are the superadmin's controls over another user's sign-in:
// the password lockout and the user's sessions. A user's own sessions
// (/account) are browser-session routes, below.
func identityTools() []spec {
	user := map[string]string{"id": "User ID."}
	return []spec{
		{
			Name:  "unlock_user",
			Title: "Unlock user",
			Description: "Lift a user's password sign-in lockout (10 failed sign-ins in a row lock an account for " +
				"15 minutes) and clear their failure streak. was_locked says whether a lock was in force. Superadmins only.",
			Method: http.MethodPost, Route: "/users/{id}/unlock", Platform: true,
			PathDocs: user,
		},
		{
			Name:  "list_user_sessions",
			Title: "List user sessions",
			Description: "A user's signed-in sessions (password and SSO sign-ins): created, last used (moves at each " +
				"token refresh, not per request), expiry, client address and browser. Superadmins only.",
			Method: http.MethodGet, Route: "/users/{id}/sessions", Platform: true,
			PathDocs: user,
		},
		{
			Name:  "revoke_user_session",
			Title: "Revoke user session",
			Description: "Sign one of a user's sessions out; it stops working on its next request. Does not sign " +
				"the user out of the SSO identity provider. Superadmins only.",
			Method: http.MethodDelete, Route: "/users/{id}/sessions/{sessionId}", Platform: true,
			PathDocs:    map[string]string{"id": "User ID.", "sessionId": "Session ID (from list_user_sessions)."},
			Destructive: true,
		},
		{
			Name:  "revoke_user_sessions",
			Title: "Revoke all user sessions",
			Description: "Sign a user out everywhere (on your own account: everywhere but the session in use). " +
				"API keys and MCP connections are not sessions and stay valid. Superadmins only.",
			Method: http.MethodDelete, Route: "/users/{id}/sessions", Platform: true,
			PathDocs: user, Destructive: true,
		},
	}
}

// accountNotExposed are the signed-in user's own session routes: they act on
// "this device", which an MCP or API-key caller does not have.
var accountNotExposed = map[string]string{
	"GET /api/v1/account/sessions":                "the signed-in user's own sessions, browser sessions only; list_user_sessions covers superadmins",
	"DELETE /api/v1/account/sessions/{id}":        "the signed-in user's own sessions, browser sessions only; revoke_user_session covers superadmins",
	"POST /api/v1/account/sessions/revoke-others": "the signed-in user's own sessions, browser sessions only; revoke_user_sessions covers superadmins",
}

func init() {
	for route, reason := range accountNotExposed {
		notExposed[route] = reason
	}
}
