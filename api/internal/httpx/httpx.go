// Package httpx holds what every API handler does the same way: resolve the
// request's tenant, read pagination, write a JSON body. Handlers used to carry
// private copies of each, and the copies disagreed (a malformed tenant ID was
// a 401 in one package and a 500 in another; page_size above the maximum was
// reset to the default in the handler but clamped in the service).
package httpx

import (
	"encoding/json"
	"net/http"
	"strconv"

	"github.com/google/uuid"

	apierrors "github.com/yassinebenameur/probara/api/internal/errors"
	"github.com/yassinebenameur/probara/api/internal/middleware"
	"github.com/yassinebenameur/probara/api/internal/pagination"
)

// TenantID returns the tenant the request is scoped to. When it cannot, it has
// already written the error response and ok is false, so a handler just
// returns: 401 when authentication attached no tenant, 400 when the tenant
// value is not a UUID.
func TenantID(w http.ResponseWriter, r *http.Request) (tenantID uuid.UUID, ok bool) {
	raw, err := middleware.GetTenantID(r.Context())
	if err != nil {
		apierrors.WriteUnauthorizedError(w, "tenant ID not found")
		return uuid.Nil, false
	}
	tenantID, err = uuid.Parse(raw)
	if err != nil {
		apierrors.WriteValidationError(w, "invalid tenant ID")
		return uuid.Nil, false
	}
	return tenantID, true
}

// WriteJSON writes v as the JSON response body with the given status.
func WriteJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	// The status line is already sent, so an encode error (a client that went
	// away) has nowhere to be reported.
	_ = json.NewEncoder(w).Encode(v)
}

// Page reads ?page= and ?page_size= with the standard limits.
func Page(r *http.Request) (page, size int) {
	return PageWithLimits(r, pagination.DefaultSize, pagination.MaxSize)
}

// PageWithLimits reads ?page= and ?page_size=. A missing or non-numeric value
// takes the default; out-of-range values follow pagination.Normalize.
func PageWithLimits(r *http.Request, defaultSize, maxSize int) (page, size int) {
	query := r.URL.Query()
	page, _ = strconv.Atoi(query.Get("page"))
	size, _ = strconv.Atoi(query.Get("page_size"))
	return pagination.Normalize(page, size, defaultSize, maxSize)
}
