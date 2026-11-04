package alerts

import (
	"context"
	"testing"

	"github.com/google/uuid"

	ctxpkg "github.com/yassinebenameur/probara/shared/context"
)

func TestActorFromContext(t *testing.T) {
	user := uuid.New()
	session := ctxpkg.WithAdminID(context.Background(), user.String())

	tests := []struct {
		name     string
		ctx      context.Context
		wantUser *uuid.UUID
		wantVia  string
	}{
		{"session", session, &user, ViaUI},
		{"api_key", ctxpkg.WithActorType(context.Background(), ctxpkg.ActorTypeAPIKey), nil, ViaAPI},
		{"mcp_user", ctxpkg.WithVia(session, ctxpkg.ViaMCP), &user, ViaMCP},
		{"mcp_api_key", ctxpkg.WithVia(context.Background(), ctxpkg.ViaMCP), nil, ViaMCP},
		{"bad_admin_id", ctxpkg.WithAdminID(context.Background(), "not-a-uuid"), nil, ViaAPI},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := ActorFromContext(tc.ctx)
			if got.Via != tc.wantVia {
				t.Errorf("Via = %q, want %q", got.Via, tc.wantVia)
			}
			if (got.UserID == nil) != (tc.wantUser == nil) || (got.UserID != nil && *got.UserID != *tc.wantUser) {
				t.Errorf("UserID = %v, want %v", got.UserID, tc.wantUser)
			}
		})
	}
}
