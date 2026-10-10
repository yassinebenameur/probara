package locations

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	ctxpkg "github.com/yassinebenameur/probara/shared/context"
)

func TestDeployCredentialsRejectReadOnlyIdentities(t *testing.T) {
	for name, ctx := range map[string]context.Context{
		"viewer":   ctxpkg.WithRole(context.Background(), "viewer"),
		"read key": ctxpkg.WithAuthScope(context.Background(), "read"),
	} {
		t.Run(name, func(t *testing.T) {
			w := httptest.NewRecorder()
			// A nil service ensures credentials cannot be loaded before authorization.
			(&Handlers{}).GetDeployInfo(w, httptest.NewRequest(http.MethodGet, "/locations/id/deploy", nil).WithContext(ctx))
			if w.Code != http.StatusForbidden {
				t.Fatalf("status %d, want 403", w.Code)
			}
		})
	}
}
