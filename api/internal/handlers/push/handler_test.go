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
		// The query default ("up") survives a body without a status.
		{"omitted status", "POST", `{"cpu":42}`, "", "up", true, 200},
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
			if tc.want != 200 {
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

func servePush(t *testing.T, method, target, contentType, body string) (*httptest.ResponseRecorder, *recordingPushService) {
	t.Helper()
	svc := &recordingPushService{}
	h := NewHandler(svc, logger.New("test", "error"), "")
	r := chi.NewRouter()
	r.Get("/{token}", h.HandlePushGet)
	r.Post("/{token}", h.HandlePushPost)
	req := httptest.NewRequest(method, target, strings.NewReader(body))
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	return w, svc
}

// The JSON body overlays the query string key by key; it never discards
// query fields it does not mention.
func TestPushBodyOverlaysQuery(t *testing.T) {
	for _, tc := range []struct {
		name, query, body string
		wantStatus        string
		wantError         string
		wantMetrics       map[string]interface{}
	}{
		{
			name: "query down survives a body without status", query: "?status=down&error=disk%20full",
			body: `{"cpu":42}`, wantStatus: "down", wantError: "disk full",
			wantMetrics: map[string]interface{}{"cpu": float64(42)},
		},
		{
			name: "body status overrides query status", query: "?status=down",
			body: `{"status":"up"}`, wantStatus: "up",
			wantMetrics: map[string]interface{}{},
		},
		{
			name: "body error overrides query error", query: "?status=error&error=from%20query",
			body: `{"error":"from body"}`, wantStatus: "error", wantError: "from body",
			wantMetrics: map[string]interface{}{},
		},
		{
			name: "metrics merge, body wins on a shared key", query: "?region=eu&cpu=1",
			body: `{"cpu":42,"files":3}`, wantStatus: "up",
			wantMetrics: map[string]interface{}{"region": "eu", "cpu": float64(42), "files": float64(3)},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			w, svc := servePush(t, "POST", "/test-token"+tc.query, "application/json", tc.body)
			if w.Code != 200 {
				t.Fatalf("status %d: %s", w.Code, w.Body.String())
			}
			if len(svc.payloads) != 1 {
				t.Fatalf("payloads = %d, want 1", len(svc.payloads))
			}
			got := svc.payloads[0]
			if got.Status != tc.wantStatus || got.Error != tc.wantError {
				t.Fatalf("status/error = %q/%q, want %q/%q", got.Status, got.Error, tc.wantStatus, tc.wantError)
			}
			if len(got.Metrics) != len(tc.wantMetrics) {
				t.Fatalf("metrics = %v, want %v", got.Metrics, tc.wantMetrics)
			}
			for k, v := range tc.wantMetrics {
				if got.Metrics[k] != v {
					t.Fatalf("metric %s = %v (%T), want %v (%T)", k, got.Metrics[k], got.Metrics[k], v, v)
				}
			}
		})
	}
}

func TestPushSizeLimits(t *testing.T) {
	big := strings.Repeat("a", maxPushPayloadBytes)
	for _, tc := range []struct {
		name, method, target, body string
		want                       int
	}{
		{"oversized JSON body", "POST", "/test-token?status=down", `{"blob":"` + big + `"}`, 413},
		{"object then oversized trailing data", "POST", "/test-token", `{"status":"down"}` + strings.Repeat(" ", maxPushPayloadBytes), 413},
		{"body just under the cap", "POST", "/test-token", `{"blob":"` + big[:maxPushPayloadBytes-20] + `"}`, 200},
		{"oversized query on POST", "POST", "/test-token?blob=" + big, `{}`, 414},
		{"oversized query on GET", "GET", "/test-token?blob=" + big, "", 414},
	} {
		t.Run(tc.name, func(t *testing.T) {
			w, svc := servePush(t, tc.method, tc.target, "application/json", tc.body)
			if w.Code != tc.want {
				t.Fatalf("status %d, want %d: %.200s", w.Code, tc.want, w.Body.String())
			}
			if tc.want != 200 && len(svc.payloads) != 0 {
				t.Fatal("oversized push was recorded")
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
