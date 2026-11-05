package middleware

import (
	"context"
	"net/http"

	"github.com/google/uuid"

	"github.com/yassinebenameur/probara/api/internal/services/adminauth"
	"github.com/yassinebenameur/probara/shared/db"
	"github.com/yassinebenameur/probara/shared/logger"
)

// Session-family check for cookie sessions (migration 000104). An access JWT
// names its sign-in in the sid claim; revoking that family must end the JWT
// at once, not at its expiry, so every cookie request re-reads the family.
// This is the only session-specific step in AuthMiddleware: roles and the
// disabled flag are still resolved by identityQuery, independently.

type sessionFamilyCtxKey struct{}

// SessionFamilyID returns the session family of the cookie session that
// authenticated the request (false for API keys, MCP tokens and access
// tokens minted before session families).
func SessionFamilyID(ctx context.Context) (uuid.UUID, bool) {
	id, ok := ctx.Value(sessionFamilyCtxKey{}).(uuid.UUID)
	return id, ok && id != uuid.Nil
}

// checkSessionFamily refuses a cookie session whose family is revoked (or
// unknown) and otherwise returns the request with the family in its context.
// A token without a sid predates session families and expires on its own.
func checkSessionFamily(w http.ResponseWriter, r *http.Request, dbClient *db.Client, log *logger.Logger, adminID uuid.UUID, sid string) (*http.Request, bool) {
	if sid == "" {
		return r, true
	}
	familyID, err := uuid.Parse(sid)
	if err != nil {
		writeAuthError(w, "invalid session")
		return nil, false
	}
	active, err := adminauth.SessionFamilyActive(r.Context(), dbClient, adminID, familyID)
	if err != nil {
		log.WithError(err).Error("Failed to check admin session")
		writeAuthError(w, "failed to resolve identity")
		return nil, false
	}
	if !active {
		writeAuthError(w, "session revoked")
		return nil, false
	}
	return r.WithContext(context.WithValue(r.Context(), sessionFamilyCtxKey{}, familyID)), true
}
