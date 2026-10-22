package audit

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/google/uuid"

	ctxpkg "github.com/yassinebenameur/probara/shared/context"
	"github.com/yassinebenameur/probara/shared/logger"
)

// An unknown sort column or direction is a 400, never a silent default: the
// whitelist is what keeps request text out of ORDER BY. The request must not
// reach the (nil) service.
func TestList_RejectsUnknownSort(t *testing.T) {
	h := NewHandlers(nil, logger.New("test", "error"))
	for _, query := range []string{"sort=bogus", "sort=id", "dir=sideways", "sort=action&dir=up"} {
		r := httptest.NewRequest(http.MethodGet, "/api/v1/audit-log?"+query, nil)
		r = r.WithContext(ctxpkg.WithTenantID(r.Context(), uuid.NewString()))
		w := httptest.NewRecorder()
		h.List(w, r)
		if w.Code != http.StatusBadRequest {
			t.Errorf("%s: status = %d, want 400", query, w.Code)
		}
	}
}
