package monitors

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/yassinebenameur/probara/api/internal/models"
	ctxpkg "github.com/yassinebenameur/probara/shared/context"
	"github.com/yassinebenameur/probara/shared/logger"
)

type testCheckRequester struct{ calls int }

func (q *testCheckRequester) Request(context.Context, string, []byte) ([]byte, error) {
	q.calls++
	return []byte(`{"status":"success"}`), nil
}

func TestSavedSecretTestsRequireWrite(t *testing.T) {
	for _, tc := range []struct {
		name, role, scope string
		want              int
	}{
		{"viewer", "viewer", "", http.StatusForbidden},
		{"read key", "", "read", http.StatusForbidden},
		{"editor", "editor", "", http.StatusOK},
		{"write key", "", "write", http.StatusOK},
		{"superadmin", "superadmin", "", http.StatusOK},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := NewHandlers(NewMockMonitorService(), nil, nil, logger.New("test", "error"), t.TempDir())
			queue := &testCheckRequester{}
			h.jobRequester = queue
			ctx := ctxpkg.WithTenantID(context.Background(), uuid.NewString())
			if tc.role != "" {
				ctx = ctxpkg.WithRole(ctx, tc.role)
			}
			if tc.role == "superadmin" {
				ctx = ctxpkg.WithPlatformRole(ctx, "superadmin")
			}
			if tc.scope != "" {
				ctx = ctxpkg.WithAuthScope(ctx, tc.scope)
			}
			for _, saved := range []bool{true, false} {
				body := map[string]any{"type": "redis", "config": map[string]any{"host": "attacker.example", "password": "***"}}
				if saved {
					body["monitor_id"] = uuid.NewString()
				}
				encoded, _ := json.Marshal(body)
				req := httptest.NewRequest(http.MethodPost, "/api/v1/monitors/test", strings.NewReader(string(encoded))).WithContext(ctx)
				w := httptest.NewRecorder()
				before := queue.calls
				h.TestMonitorConfig(w, req)
				want := http.StatusOK
				if saved {
					want = tc.want
				}
				if w.Code != want {
					t.Fatalf("saved=%v: status %d, want %d: %s", saved, w.Code, want, w.Body.String())
				}
				if want == http.StatusForbidden && queue.calls != before {
					t.Fatal("forbidden test reached worker")
				}
			}
		})
	}
}

func TestTestMonitorRejectsCiphertextReplayWithoutSavedID(t *testing.T) {
	for _, tc := range []struct {
		monitorType string
		config      map[string]any
	}{
		{"redis", map[string]any{"host": "attacker.example", "password": `{"alg":"aes-256-gcm","ct":"replayed-ciphertext"}`}},
		{"websocket", map[string]any{"url": "wss://attacker.example", "headers": map[string]string{"Authorization": `{"alg":"aes-256-gcm","ct":"replayed-ciphertext"}`}}},
	} {
		t.Run(tc.monitorType, func(t *testing.T) {
			h := NewHandlers(NewMockMonitorService(), nil, nil, logger.New("test", "error"), t.TempDir())
			queue := &testCheckRequester{}
			h.jobRequester = queue
			ctx := ctxpkg.WithAuthScope(ctxpkg.WithTenantID(context.Background(), uuid.NewString()), "read")
			body, _ := json.Marshal(map[string]any{"type": tc.monitorType, "config": tc.config})
			w := httptest.NewRecorder()
			h.TestMonitorConfig(w, httptest.NewRequest("POST", "/api/v1/monitors/test", strings.NewReader(string(body))).WithContext(ctx))
			if w.Code != http.StatusBadRequest || queue.calls != 0 {
				t.Fatalf("ciphertext replay was accepted: status=%d, worker calls=%d", w.Code, queue.calls)
			}
		})
	}
}

func TestGroupMembersMaskSecretsForEveryIdentity(t *testing.T) {
	for _, role := range []string{"viewer", "editor", "superadmin"} {
		t.Run(role, func(t *testing.T) {
			groupSvc := &MockGroupService{currentMembers: []models.Monitor{
				{ID: uuid.New(), Type: models.MonitorTypeRedis, Config: json.RawMessage(`{"host":"redis.example","password":"legacy-plaintext"}`)},
				{ID: uuid.New(), Type: models.MonitorTypeWebSocket, Config: json.RawMessage(`{"url":"wss://socket.example","headers":{"Authorization":"{\"alg\":\"aes-256-gcm\",\"ct\":\"stored-ciphertext\"}"}}`)},
			}}
			h := NewHandlers(NewMockMonitorService(), groupSvc, nil, logger.New("test", "error"), t.TempDir())
			r := chi.NewRouter()
			r.Get("/{id}/members", h.GetGroupMembers)
			ctx := ctxpkg.WithRole(ctxpkg.WithTenantID(context.Background(), uuid.NewString()), role)
			if role == "superadmin" {
				ctx = ctxpkg.WithPlatformRole(ctx, role)
			}
			w := httptest.NewRecorder()
			r.ServeHTTP(w, httptest.NewRequest("GET", "/"+uuid.NewString()+"/members", nil).WithContext(ctx))
			if w.Code != http.StatusOK {
				t.Fatalf("status %d: %s", w.Code, w.Body.String())
			}
			for _, sensitive := range []string{"legacy-plaintext", "stored-ciphertext"} {
				if strings.Contains(w.Body.String(), sensitive) {
					t.Fatalf("response exposed %s", sensitive)
				}
			}
			if !strings.Contains(w.Body.String(), "***") {
				t.Fatal("expected masked secret placeholders")
			}
		})
	}
}

func TestMonitorResponsesProtectPushTokens(t *testing.T) {
	for _, scope := range []string{"read", "write"} {
		t.Run(scope, func(t *testing.T) {
			svc := NewMockMonitorService()
			tenantID, monitorID := uuid.New(), uuid.New()
			token := "test-push-credential"
			svc.monitors[monitorID] = &models.Monitor{ID: monitorID, TenantID: tenantID, Type: models.MonitorTypePush, PushToken: &token, Config: json.RawMessage(`{"push_token":"legacy-config-credential"}`)}
			h := NewHandlers(svc, nil, nil, logger.New("test", "error"), t.TempDir())
			r := chi.NewRouter()
			r.Get("/", h.ListMonitors)
			r.Get("/{id}", h.GetMonitor)
			ctx := ctxpkg.WithAuthScope(ctxpkg.WithTenantID(context.Background(), tenantID.String()), scope)
			for _, path := range []string{"/" + monitorID.String(), "/"} {
				w := httptest.NewRecorder()
				r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, path, nil).WithContext(ctx))
				if w.Code != http.StatusOK {
					t.Fatalf("status %d: %s", w.Code, w.Body.String())
				}
				if strings.Contains(w.Body.String(), token) != (scope == "write") {
					t.Fatalf("unexpected credential visibility for %s", scope)
				}
				if strings.Contains(w.Body.String(), "legacy-config-credential") {
					t.Fatal("config exposed legacy push token")
				}
			}
			if svc.monitors[monitorID].PushToken == nil {
				t.Fatal("redaction mutated source model")
			}
		})
	}
}
