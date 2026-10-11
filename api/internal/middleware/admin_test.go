package middleware

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

// TestRequireTenantAdminExportMatrix covers the AI label export gate, which
// sits behind RequireWrite: only tenant admins and superadmins (cookie
// sessions) pass both; editors and every API key are refused.
func TestRequireTenantAdminExportMatrix(t *testing.T) {
	cases := []struct {
		name string
		ctx  context.Context
		want int
	}{
		{"superadmin", superadminCtx(), http.StatusOK},
		{"member admin", memberCtx("admin"), http.StatusOK},
		{"member editor", memberCtx("editor"), http.StatusForbidden},
		{"member viewer", memberCtx("viewer"), http.StatusForbidden},
		{"api key write", apiKeyCtx("write"), http.StatusForbidden},
		{"api key read", apiKeyCtx("read"), http.StatusForbidden},
	}
	handler := RequireWrite(RequireTenantAdmin(okHandler()))
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodPost, "/api/v1/ai-labels/export", nil).WithContext(tc.ctx)
			w := httptest.NewRecorder()
			handler.ServeHTTP(w, req)
			if w.Code != tc.want {
				t.Fatalf("expected %d, got %d", tc.want, w.Code)
			}
		})
	}
}
