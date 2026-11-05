package mcp

import "net/http"

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
	}
}

// identityNotExposed are the identity routes deliberately left out of the
// catalog, merged into notExposed.
var identityNotExposed = map[string]string{
	"GET /api/v1/account/sessions":                "session management, signed-in user sessions only",
	"DELETE /api/v1/account/sessions/{id}":        "session management, signed-in user sessions only",
	"POST /api/v1/account/sessions/revoke-others": "session management, signed-in user sessions only",
	"POST /api/v1/account/sessions/revoke-all":    "session management, signed-in user sessions only",
}

func init() {
	for route, reason := range identityNotExposed {
		notExposed[route] = reason
	}
}
