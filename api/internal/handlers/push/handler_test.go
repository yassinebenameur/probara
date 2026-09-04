package push

import (
	"context"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/yassinebenameur/probara/api/internal/models"
	pushservice "github.com/yassinebenameur/probara/api/internal/services/push"
	ctxpkg "github.com/yassinebenameur/probara/shared/context"
	"github.com/yassinebenameur/probara/shared/logger"
)

type recordingPushService struct {
	pushservice.PushService
	payloads  []pushservice.PushPayload
	infoCalls int
}

func (s *recordingPushService) ProcessPush(_ context.Context, _ string, payload pushservice.PushPayload) error {
	s.payloads = append(s.payloads, payload)
	return nil
}

func (s *recordingPushService) GetPushInfo(context.Context, uuid.UUID, uuid.UUID, string) (*models.PushInfo, error) {
	s.infoCalls++
	return &models.PushInfo{PushToken: "test-credential"}, nil
}

func TestPushPayloadParsing(t *testing.T) {
	for _, tc := range []struct {
		name, method, body, query, wantStatus string
		unknownLength                         bool
		want                                  int
	}{
		{"chunked down", "POST", `{"status":"down","error":"offline","cpu":42}`, "", "down", true, 200},
		{"chunked error", "POST", `{"status":"error"}`, "", "error", true, 200},
		{"ordinary up", "POST", `{"status":"up"}`, "", "up", false, 200},
		{"empty heartbeat", "POST", "", "", "up", false, 200},
		{"empty chunked query", "POST", "", "?status=down", "down", true, 200},
		{"omitted status", "POST", `{"cpu":42}`, "", "", true, 200},
		{"unknown JSON status", "POST", `{"status":"dowm"}`, "", "", true, 400},
		{"numeric status", "POST", `{"status":0}`, "", "", true, 400},
		{"null status", "POST", `{"status":null}`, "", "", true, 400},
		{"bad JSON", "POST", `{"status":`, "", "", true, 400},
		{"multiple JSON objects", "POST", `{} {"status":"down"}`, "", "", true, 400},
		{"null body", "POST", `null`, "", "", true, 400},
		{"invalid GET status", "GET", "", "?status=dowm", "", false, 400},
		{"GET down", "GET", "", "?status=down", "down", false, 200},
	} {
		t.Run(tc.name, func(t *testing.T) {
			svc := &recordingPushService{}
			h := NewHandler(svc, logger.New("test", "error"), "")
			r := chi.NewRouter()
			r.Get("/{token}", h.HandlePushGet)
			r.Post("/{token}", h.HandlePushPost)
			req := httptest.NewRequest(tc.method, "/test-token"+tc.query, strings.NewReader(tc.body))
			req.Header.Set("Content-Type", "application/json")
			if tc.unknownLength {
				req.ContentLength = -1
				req.TransferEncoding = []string{"chunked"}
			}
			w := httptest.NewRecorder()
			r.ServeHTTP(w, req)
			if w.Code != tc.want {
				t.Fatalf("status %d, want %d: %s", w.Code, tc.want, w.Body.String())
			}
			if tc.want == 400 {
				if len(svc.payloads) != 0 {
					t.Fatal("invalid payload was persisted")
				}
				return
			}
			if len(svc.payloads) != 1 || svc.payloads[0].Status != tc.wantStatus {
				t.Fatalf("unexpected payload: %+v", svc.payloads)
			}
			if tc.name == "chunked down" && (svc.payloads[0].Error != "offline" || svc.payloads[0].Metrics["cpu"] != float64(42)) {
				t.Fatalf("chunked fields lost: %+v", svc.payloads[0])
			}
		})
	}
}

func TestPushInfoRequiresWrite(t *testing.T) {
	for _, tc := range []struct {
		name, role, scope string
		want              int
	}{
		{"viewer", "viewer", "", 403},
		{"read key", "", "read", 403},
		{"editor", "editor", "", 200},
		{"write key", "", "write", 200},
		{"superadmin", "superadmin", "", 200},
	} {
		t.Run(tc.name, func(t *testing.T) {
			svc := &recordingPushService{}
			h := NewHandler(svc, logger.New("test", "error"), "")
			r := chi.NewRouter()
			r.Get("/{id}/push/info", h.HandleGetPushInfo)
			ctx := ctxpkg.WithTenantID(context.Background(), uuid.NewString())
			if tc.role != "" {
				ctx = ctxpkg.WithRole(ctx, tc.role)
			}
			if tc.role == "superadmin" {
				ctx = ctxpkg.WithPlatformRole(ctx, tc.role)
			}
			if tc.scope != "" {
				ctx = ctxpkg.WithAuthScope(ctx, tc.scope)
			}
			w := httptest.NewRecorder()
			r.ServeHTTP(w, httptest.NewRequest("GET", "/"+uuid.NewString()+"/push/info", nil).WithContext(ctx))
			if w.Code != tc.want {
				t.Fatalf("status %d, want %d", w.Code, tc.want)
			}
			if tc.want == 403 && svc.infoCalls != 0 {
				t.Fatal("read-only request retrieved credentials")
			}
		})
	}
}
