package monitors

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/uuid"
	ctxpkg "github.com/yassinebenameur/probara/shared/context"
	"github.com/yassinebenameur/probara/shared/logger"
)

// An inline domain lookup spends the registries' rate limit on the worker's
// shared egress address, so POST /monitors/test needs write access for it,
// saved monitor or not, through the same gate as saved-credential tests.
func TestDomainTestLookupRequiresWrite(t *testing.T) {
	for _, tc := range []struct {
		name, role, scope string
		want              int
	}{
		{"viewer", "viewer", "", http.StatusForbidden},
		{"read key", "", "read", http.StatusForbidden},
		{"editor", "editor", "", http.StatusOK},
		{"write key", "", "write", http.StatusOK},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := NewHandlers(NewMockMonitorService(), nil, nil, logger.New("test", "error"), t.TempDir())
			queue := &testCheckRequester{}
			h.jobRequester = queue
			ctx := ctxpkg.WithTenantID(context.Background(), uuid.NewString())
			if tc.role != "" {
				ctx = ctxpkg.WithRole(ctx, tc.role)
			}
			if tc.scope != "" {
				ctx = ctxpkg.WithAuthScope(ctx, tc.scope)
			}
			body, _ := json.Marshal(map[string]any{"type": "domain", "config": map[string]any{"domain": "example.com"}})
			w := httptest.NewRecorder()
			h.TestMonitorConfig(w, httptest.NewRequest(http.MethodPost, "/api/v1/monitors/test", strings.NewReader(string(body))).WithContext(ctx))
			if w.Code != tc.want {
				t.Fatalf("status %d, want %d: %s", w.Code, tc.want, w.Body.String())
			}
			if tc.want == http.StatusForbidden && queue.calls != 0 {
				t.Fatal("forbidden test lookup reached a worker")
			}
		})
	}
}
