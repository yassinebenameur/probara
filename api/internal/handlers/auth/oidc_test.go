package auth

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/yassinebenameur/probara/api/internal/services/oidcauth"
	"github.com/yassinebenameur/probara/shared/config"
	"github.com/yassinebenameur/probara/shared/logger"
)

func testHandlers() *Handlers {
	return &Handlers{
		cfg:    &config.APIConfig{AdminJWTSecret: "test-secret-test-secret-test-secret"},
		logger: logger.New("test", "error"),
	}
}

func TestFlowCookieRoundTrip(t *testing.T) {
	h := testHandlers()
	flow := &oidcauth.FlowState{State: "state123", Nonce: "nonce456", Verifier: "verifier789", Next: "/monitors"}

	encoded, err := h.encodeFlowCookie(flow)
	if err != nil {
		t.Fatalf("encodeFlowCookie: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/callback", nil)
	req.AddCookie(&http.Cookie{Name: oidcFlowCookieName, Value: encoded})

	decoded, err := h.readFlowCookie(req)
	if err != nil {
		t.Fatalf("readFlowCookie: %v", err)
	}
	if *decoded != *flow {
		t.Fatalf("round trip mismatch: %+v != %+v", decoded, flow)
	}
}

func TestFlowCookieTamperRejected(t *testing.T) {
	h := testHandlers()
	flow := &oidcauth.FlowState{State: "state123", Nonce: "nonce456", Verifier: "verifier789"}

	encoded, err := h.encodeFlowCookie(flow)
	if err != nil {
		t.Fatalf("encodeFlowCookie: %v", err)
	}

	cases := map[string]string{
		"flipped payload byte": "X" + encoded[1:],
		"truncated signature":  encoded[:len(encoded)-2],
		"missing signature":    "eyJmb28iOiJiYXIifQ",
		"empty":                "",
	}
	for name, value := range cases {
		t.Run(name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "/callback", nil)
			req.AddCookie(&http.Cookie{Name: oidcFlowCookieName, Value: value})
			if _, err := h.readFlowCookie(req); err == nil {
				t.Fatal("expected tampered cookie to be rejected")
			}
		})
	}

	t.Run("wrong key", func(t *testing.T) {
		other := &Handlers{cfg: &config.APIConfig{AdminJWTSecret: "different-secret-different-secret"}}
		req := httptest.NewRequest(http.MethodGet, "/callback", nil)
		req.AddCookie(&http.Cookie{Name: oidcFlowCookieName, Value: encoded})
		if _, err := other.readFlowCookie(req); err == nil {
			t.Fatal("expected cookie signed with another key to be rejected")
		}
	})
}

func TestSanitizeNextPath(t *testing.T) {
	cases := map[string]string{
		"/monitors":              "/monitors",
		"/monitors?page=2":       "/monitors?page=2",
		"":                       "",
		"https://evil.example":   "",
		"//evil.example":         "",
		"monitors":               "",
		"/ok\r\nSet-Cookie: x=1": "",
		"\\evil":                 "",
	}
	for input, want := range cases {
		if got := sanitizeNextPath(input); got != want {
			t.Errorf("sanitizeNextPath(%q) = %q, want %q", input, got, want)
		}
	}
}

func TestOIDCEndpointsDisabled(t *testing.T) {
	h := testHandlers() // no OIDC service attached

	for _, path := range []string{"/start", "/callback"} {
		req := httptest.NewRequest(http.MethodGet, path, nil)
		w := httptest.NewRecorder()
		if path == "/start" {
			h.OIDCStart(w, req)
		} else {
			h.OIDCCallback(w, req)
		}
		if w.Code != http.StatusNotFound {
			t.Errorf("%s: expected 404 when OIDC disabled, got %d", path, w.Code)
		}
	}

	req := httptest.NewRequest(http.MethodGet, "/status", nil)
	w := httptest.NewRecorder()
	h.OIDCStatus(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status should always answer, got %d", w.Code)
	}
	if body := w.Body.String(); !strings.Contains(body, `"enabled":false`) {
		t.Fatalf("expected enabled:false, got %s", body)
	}
}
