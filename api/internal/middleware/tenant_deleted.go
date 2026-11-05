package middleware

import (
	"net/http"

	"github.com/google/uuid"

	"github.com/yassinebenameur/probara/shared/db"
	"github.com/yassinebenameur/probara/shared/logger"
	"github.com/yassinebenameur/probara/shared/tenancy"
)

// rejectDeletedTenant answers 403 and returns true when a session request is
// scoped to a soft-deleted workspace — superadmins included, since they
// otherwise act on any tenant ID. API keys are checked in their own lookup
// (auth.ValidateAPIKey adds tenancy.LiveTenantSQL), which also refuses a key
// created while the delete was committing.
func rejectDeletedTenant(w http.ResponseWriter, r *http.Request, dbClient *db.Client, log *logger.Logger, tenantID uuid.UUID) bool {
	if tenantID == uuid.Nil {
		return false
	}
	deleted, err := tenancy.IsDeleted(r.Context(), dbClient, tenantID)
	if err != nil {
		log.WithError(err).Error("Failed to resolve workspace state")
		writeAuthError(w, "failed to resolve workspace")
		return true
	}
	if deleted {
		writeForbiddenError(w, "this workspace has been deleted")
		return true
	}
	return false
}
