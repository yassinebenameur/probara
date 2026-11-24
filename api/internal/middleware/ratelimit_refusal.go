package middleware

import (
	"context"
	"net/http"

	"github.com/google/uuid"

	ctxpkg "github.com/yassinebenameur/probara/shared/context"
)

// A request whose credential verifies but which AuthMiddleware then refuses
// -- a malformed workspace identifier (400), a workspace the caller cannot
// open (404 workspace_not_found) -- costs the same lookups as a served one
// (the session row, the identity query) yet is neither a 401 failure nor a
// pass the limiter charges. Without a charge a signed-in member looping
// random X-Tenant-ID values would buy unlimited lookups, so AuthMiddleware
// names the identity it refused and the limiter's authGate charges that
// credential's own bucket, exactly as if the request had been served.

type refusedIdentity struct{ key string }

type refusedIdentityKey struct{}

// withRefusalSlot gives r a slot AuthMiddleware fills when it refuses an
// authenticated request (noteAuthenticatedRefusal).
func withRefusalSlot(r *http.Request) (*http.Request, *refusedIdentity) {
	slot := &refusedIdentity{}
	return r.WithContext(context.WithValue(r.Context(), refusedIdentityKey{}, slot)), slot
}

// noteAuthenticatedRefusal records that adminID's request (a session, or an
// MCP token acting as that user) authenticated and was refused, so the
// limiter charges the bucket a served request would have spent from.
func noteAuthenticatedRefusal(r *http.Request, adminID uuid.UUID) {
	slot, ok := r.Context().Value(refusedIdentityKey{}).(*refusedIdentity)
	if !ok {
		return
	}
	slot.key = identityKey(r.WithContext(ctxpkg.WithAdminID(r.Context(), adminID.String())))
}
