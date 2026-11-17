package middleware

import (
	"net/http"

	"github.com/google/uuid"

	"github.com/yassinebenameur/probara/api/internal/services/adminauth"
	"github.com/yassinebenameur/probara/shared/db"
	"github.com/yassinebenameur/probara/shared/logger"
)

// checkSession refuses a cookie session whose sign-in (the access token's
// sid, a session family) is revoked, expired or not the token's user, and
// otherwise returns the request with the session in its context
// (adminauth.SessionIDFromContext). It runs on every request a session cookie
// authenticates: one primary-key lookup (adminauth.SessionActive), no cache,
// so a revocation takes effect on the next request on every replica. A token
// without a sid was minted before session families existed and lapses at its
// own expiry, within the access-token lifetime.
func checkSession(w http.ResponseWriter, r *http.Request, dbClient *db.Client, log *logger.Logger, adminID uuid.UUID, sid string) (*http.Request, bool) {
	if sid == "" {
		return r, true
	}
	familyID, err := uuid.Parse(sid)
	if err != nil {
		writeAuthError(w, "invalid session")
		return nil, false
	}
	active, err := adminauth.SessionActive(r.Context(), dbClient, adminID, familyID)
	if err != nil {
		log.WithError(err).Error("Failed to check admin session")
		writeAuthError(w, "failed to resolve identity")
		return nil, false
	}
	if !active {
		writeAuthError(w, "session revoked")
		return nil, false
	}
	return r.WithContext(adminauth.WithSessionID(r.Context(), familyID)), true
}
