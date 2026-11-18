package api

import (
	"github.com/go-chi/chi/v5"

	mcpserver "github.com/yassinebenameur/probara/api/internal/mcp"
)

// mountOpenAPIRoute serves the OpenAPI document generated from the MCP tool
// catalog. Mount it inside the protected group: it is for signed-in users
// and API keys only, and it is not workspace-scoped.
func mountOpenAPIRoute(r chi.Router, mcpHandler *mcpserver.Handler) {
	r.Get("/openapi.json", mcpHandler.ServeOpenAPI)
}
