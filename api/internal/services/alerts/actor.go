package alerts

import (
	"context"

	"github.com/google/uuid"

	ctxpkg "github.com/yassinebenameur/probara/shared/context"
)

// Via values: the surface an alert action came through.
const (
	// ViaUI is an operator session (admin cookie), i.e. the web UI.
	ViaUI = "ui"
	// ViaAPI is a tenant API key.
	ViaAPI = "api"
	// ViaMCP is a REST call an MCP tool dispatched.
	ViaMCP = "mcp"
	// ViaMagicLink is a signed acknowledge link from a notification
	// (api/internal/handlers/alertactions); it carries no user.
	ViaMagicLink = "magic_link"
)

// Actor identifies who acknowledged or resolved an alert and through which
// surface. Acknowledge persists it as acknowledged_by_user_id /
// acknowledged_via, resolve as resolved_by_user_id / resolved_via.
type Actor struct {
	// UserID is the operator who acted; nil for API keys and other
	// credentials without a user.
	UserID *uuid.UUID
	// Via is the surface the action came through (Via* constants).
	Via string
}

// ActorFromContext derives the Actor of an authenticated API request from
// the identity AuthMiddleware put in ctx.
func ActorFromContext(ctx context.Context) Actor {
	var actor Actor
	if adminID, ok := ctxpkg.GetAdminID(ctx); ok {
		if parsed, err := uuid.Parse(adminID); err == nil {
			actor.UserID = &parsed
		}
	}
	switch via, _ := ctxpkg.GetVia(ctx); {
	case via == ctxpkg.ViaMCP:
		actor.Via = ViaMCP
	case actor.UserID != nil:
		actor.Via = ViaUI
	default:
		actor.Via = ViaAPI
	}
	return actor
}

// resolvedVia is the resolved_via a manual resolve records. It is never
// NULL: NULL marks the alerter's own resolves, the only ones the reopen
// cooldown may reopen, so an actor without a surface still counts as manual
// (ViaAPI).
func resolvedVia(actor Actor) string {
	if actor.Via == "" {
		return ViaAPI
	}
	return actor.Via
}
