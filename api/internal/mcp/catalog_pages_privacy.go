package mcp

import (
	"net/http"

	"github.com/yassinebenameur/probara/api/internal/models"
)

// statusPageAccessTools are the kiosk token tools. Visibility and the visitor
// password are fields of create_status_page / update_status_page.
func statusPageAccessTools() []spec {
	page := map[string]string{"id": "Status page ID."}
	return []spec{
		{
			Name:  "list_status_page_access_tokens",
			Title: "List kiosk tokens",
			Description: "A status page's kiosk tokens (label, creator, created and revoked times), revoked ones " +
				"included. Token values are never returned after creation.",
			Method: http.MethodGet, Route: "/status-pages/{id}/access-tokens",
			PathDocs: page,
		},
		{
			Name:  "create_status_page_access_token",
			Title: "Create kiosk token",
			Description: "Mint a kiosk token that opens a password or private status page: append ?token=<token> to " +
				"the public URL. The token is in this response only; store it now. At most 50 active tokens per page.",
			Method: http.MethodPost, Route: "/status-pages/{id}/access-tokens",
			PathDocs: page,
			Body:     typeOf[models.CreateStatusPageAccessTokenRequest](), Required: []string{"label"},
			FieldDocs: map[string]string{
				"label": "What the token is for, e.g. \"Lobby TV\" (1-100 characters).",
			},
		},
		{
			Name:  "revoke_status_page_access_token",
			Title: "Revoke kiosk token",
			Description: "Revoke a kiosk token. Every browser that opened the page with it loses access on its next " +
				"request.",
			Method: http.MethodDelete, Route: "/status-pages/{id}/access-tokens/{accessId}",
			PathDocs: map[string]string{
				"id":       "Status page ID.",
				"accessId": "Kiosk token ID, from list_status_page_access_tokens.",
			},
			Destructive: true,
		},
	}
}

const statusPageVisibilityDoc = "Who may view the page: public (default), unlisted (anyone with the link, not indexed), " +
	"password (the visitor password or a kiosk token) or private (kiosk tokens only)."
