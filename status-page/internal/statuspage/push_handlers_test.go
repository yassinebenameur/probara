package statuspage

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/yassinebenameur/probara/shared/config"
	"github.com/yassinebenameur/probara/shared/logger"
	"github.com/yassinebenameur/probara/shared/statustemplate"
)

// pushTestHandlers builds Handlers with no database. Every test below is
// rejected by a guard that runs before any query, which is the point: these
// are the checks that must hold without touching Postgres.
func pushTestHandlers(t *testing.T, cfg *config.StatusPageConfig) *Handlers {
	t.Helper()
	return NewHandlers(nil, cfg, logger.New("status-page", "error"), NewHub(), newRenderCache(0), nil)
}

func pushConfiguredConfig() *config.StatusPageConfig {
	return &config.StatusPageConfig{
		VAPIDPublicKey:  "BExamplePublicKeyValue",
		VAPIDPrivateKey: "ExamplePrivateKeyValue",
		VAPIDSubject:    "mailto:ops@example.com",
	}
}

func postJSON(t *testing.T, h *Handlers, path, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	h.HandleStatusPage(rec, req)
	return rec
}

// A GET route must still reject non-GET, but the push routes are POST. The
// method guard was moved below the sub-routing for exactly this reason, so
// pin both halves.
func TestHandleStatusPage_MethodGuardStillRejectsWritesToReadRoutes(t *testing.T) {
	h := pushTestHandlers(t, pushConfiguredConfig())

	for _, path := range []string{"/public/status/acme", "/public/status/acme/data", "/public/status/acme/stream"} {
		req := httptest.NewRequest(http.MethodPost, path, nil)
		rec := httptest.NewRecorder()
		h.HandleStatusPage(rec, req)
		if rec.Code != http.StatusMethodNotAllowed {
			t.Errorf("POST %s = %d, want 405", path, rec.Code)
		}
	}
}

func TestHandlePushSubscribe_RejectsNonPost(t *testing.T) {
	h := pushTestHandlers(t, pushConfiguredConfig())

	req := httptest.NewRequest(http.MethodGet, "/public/status/acme/push/subscribe", nil)
	rec := httptest.NewRecorder()
	h.HandleStatusPage(rec, req)

	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("GET subscribe = %d, want 405", rec.Code)
	}
	if got := rec.Header().Get("Allow"); got != http.MethodPost {
		t.Fatalf("Allow = %q, want POST", got)
	}
}

// Requiring JSON is what forces a cross-origin browser POST to preflight;
// since no CORS headers are emitted, the preflight fails.
func TestHandlePushSubscribe_RequiresJSONContentType(t *testing.T) {
	h := pushTestHandlers(t, pushConfiguredConfig())

	req := httptest.NewRequest(http.MethodPost, "/public/status/acme/push/subscribe", strings.NewReader("{}"))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec := httptest.NewRecorder()
	h.HandleStatusPage(rec, req)

	if rec.Code != http.StatusUnsupportedMediaType {
		t.Fatalf("form content type = %d, want 415", rec.Code)
	}
}

func TestHandlePushSubscribe_RejectsCrossOrigin(t *testing.T) {
	h := pushTestHandlers(t, pushConfiguredConfig())

	req := httptest.NewRequest(http.MethodPost, "/public/status/acme/push/subscribe", strings.NewReader("{}"))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Origin", "https://evil.example.com")
	rec := httptest.NewRecorder()
	h.HandleStatusPage(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("cross-origin subscribe = %d, want 403", rec.Code)
	}
}

func TestHandlePushSubscribe_RejectsOversizeBody(t *testing.T) {
	h := pushTestHandlers(t, pushConfiguredConfig())

	rec := postJSON(t, h, "/public/status/acme/push/subscribe",
		`{"endpoint":"`+strings.Repeat("a", pushSubscribeMaxBody+100)+`"}`)

	if rec.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("oversize body = %d, want 413", rec.Code)
	}
}

// The allowlist must be enforced at subscribe time, not at send time: a
// stored bad endpoint outlives the request, and by the time the sender would
// notice, the visitor who submitted it is long gone.
func TestHandlePushSubscribe_RejectsDisallowedEndpointBeforeAnyQuery(t *testing.T) {
	h := pushTestHandlers(t, pushConfiguredConfig())

	for _, endpoint := range []string{
		"https://evil.example.com/collect/aaaaaaaaaaaaaaaaaaaa",
		"http://fcm.googleapis.com/fcm/send/aaaaaaaaaaaaaaaaaa",
		"https://169.254.169.254/latest/meta-data/aaaaaaaaaaaa",
	} {
		rec := postJSON(t, h, "/public/status/acme/push/subscribe",
			`{"endpoint":"`+endpoint+`","keys":{"p256dh":"x","auth":"y"}}`)
		// nil pushStore would panic if the guard did not run first, so
		// reaching a clean 400 also proves the ordering.
		if rec.Code != http.StatusBadRequest {
			t.Errorf("endpoint %q = %d, want 400", endpoint, rec.Code)
		}
	}
}

func TestHandlePushSubscribe_RejectsMalformedKeys(t *testing.T) {
	h := pushTestHandlers(t, pushConfiguredConfig())

	rec := postJSON(t, h, "/public/status/acme/push/subscribe",
		`{"endpoint":"https://fcm.googleapis.com/fcm/send/abcdefghijklmnop","keys":{"p256dh":"notakey","auth":"short"}}`)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("malformed keys = %d, want 400", rec.Code)
	}
}

// Without VAPID keys the feature does not exist. A 404 (rather than 503 or
// 501) also means an unauthenticated caller cannot distinguish "push is off"
// from "no such page".
func TestHandlePushSubscribe_NotFoundWhenPushUnconfigured(t *testing.T) {
	h := pushTestHandlers(t, &config.StatusPageConfig{})

	rec := postJSON(t, h, "/public/status/acme/push/subscribe",
		`{"endpoint":"https://fcm.googleapis.com/fcm/send/abcdefghijklmnop","keys":{"p256dh":"BCVxsr7N_eNgVRqvHtD0zTZsEc6-VV-JvLexhqUzORcxaOzi6-AYWXvTBHm4bjyPjs7Vd8pZGH6SRpkNtoIAiw4","auth":"BTBZMqHH6r4Tts7J_aSIgg"}}`)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("subscribe with push unconfigured = %d, want 404", rec.Code)
	}
}

func TestHandlePushSubscribe_RateLimits(t *testing.T) {
	h := pushTestHandlers(t, pushConfiguredConfig())

	var limited bool
	for i := 0; i < pushRateBurst+5; i++ {
		rec := postJSON(t, h, "/public/status/acme/push/subscribe", `{"endpoint":"https://evil.example.com/x/aaaaaaaaaaaaaaaaaa"}`)
		if rec.Code == http.StatusTooManyRequests {
			limited = true
			if rec.Header().Get("Retry-After") == "" {
				t.Fatalf("429 without Retry-After")
			}
			break
		}
	}
	if !limited {
		t.Fatalf("no request was rate limited after %d attempts", pushRateBurst+5)
	}
}

// Unsubscribe is deliberately permissive: refusing to forget is worse than
// forgetting something that was never there, and a uniform 204 means it
// cannot be used to probe which endpoints exist.
func TestHandlePushUnsubscribe_AlwaysSucceeds(t *testing.T) {
	h := pushTestHandlers(t, pushConfiguredConfig())

	rec := postJSON(t, h, "/public/status/acme/push/unsubscribe", `{"endpoint":"https://anything.example/x"}`)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("unsubscribe = %d, want 204", rec.Code)
	}
	if got := rec.Header().Get("Cache-Control"); got != "no-store" {
		t.Fatalf("Cache-Control = %q, want no-store", got)
	}
}

func TestHandlePushServiceWorker_ServesRevalidatingScript(t *testing.T) {
	h := pushTestHandlers(t, pushConfiguredConfig())

	req := httptest.NewRequest(http.MethodGet, "/public/status/sw.js", nil)
	rec := httptest.NewRecorder()
	h.HandlePushServiceWorker(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("sw.js = %d, want 200", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "application/javascript") {
		t.Fatalf("Content-Type = %q, want application/javascript", ct)
	}
	// Browsers byte-compare the worker on every update check; a cached copy
	// would mean a fixed worker never reaches browsers that hold the old one.
	if cc := rec.Header().Get("Cache-Control"); cc != "no-cache" {
		t.Fatalf("Cache-Control = %q, want no-cache", cc)
	}
	if rec.Header().Get("ETag") == "" {
		t.Fatalf("missing ETag")
	}
	if body := rec.Body.String(); body != statustemplate.ServiceWorkerSource {
		t.Fatalf("body is not the embedded worker source")
	}
	if !strings.Contains(rec.Body.String(), "notificationclick") {
		t.Fatalf("worker is missing its notificationclick handler")
	}
}

// The worker is served even with push unconfigured: a browser that already
// registered one polls this URL, and 404ing it is not a clean retirement.
func TestHandlePushServiceWorker_ServedWhenPushUnconfigured(t *testing.T) {
	h := pushTestHandlers(t, &config.StatusPageConfig{})

	req := httptest.NewRequest(http.MethodGet, "/public/status/sw.js", nil)
	rec := httptest.NewRecorder()
	h.HandlePushServiceWorker(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("sw.js with push unconfigured = %d, want 200", rec.Code)
	}
}

func TestHandlePushServiceWorker_HonoursIfNoneMatch(t *testing.T) {
	h := pushTestHandlers(t, pushConfiguredConfig())

	req := httptest.NewRequest(http.MethodGet, "/public/status/sw.js", nil)
	req.Header.Set("If-None-Match", `"`+serviceWorkerETag+`"`)
	rec := httptest.NewRecorder()
	h.HandlePushServiceWorker(rec, req)

	if rec.Code != http.StatusNotModified {
		t.Fatalf("matching If-None-Match = %d, want 304", rec.Code)
	}
}

// The exact body a browser sends. PushSubscription.toJSON() always includes
// expirationTime, and the decoder rejects unknown fields, so a struct without
// it makes every REAL subscription fail with 400 while hand-written test
// payloads pass -- which is precisely how this was missed until the page was
// driven in an actual browser.
func TestHandlePushSubscribe_AcceptsRealBrowserPayloadShape(t *testing.T) {
	// Verbatim shape of PushSubscription.toJSON(), keys from the RFC example.
	const browserBody = `{"endpoint":"https://fcm.googleapis.com/fcm/send/abcdefghijklmnop","expirationTime":null,"keys":{"p256dh":"BCVxsr7N_eNgVRqvHtD0zTZsEc6-VV-JvLexhqUzORcxaOzi6-AYWXvTBHm4bjyPjs7Vd8pZGH6SRpkNtoIAiw4","auth":"BTBZMqHH6r4Tts7J_aSIgg"}}`

	var req pushSubscribeRequest
	dec := json.NewDecoder(strings.NewReader(browserBody))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&req); err != nil {
		t.Fatalf("a real browser subscription body was rejected: %v", err)
	}
	if req.Endpoint == "" || req.Keys.P256dh == "" || req.Keys.Auth == "" {
		t.Fatalf("decoded body is missing fields: %+v", req)
	}

	// And a non-null expirationTime, which a push service is permitted to set.
	const withExpiry = `{"endpoint":"https://fcm.googleapis.com/fcm/send/abcdefghijklmnop","expirationTime":1735689600000,"keys":{"p256dh":"x","auth":"y"}}`
	dec2 := json.NewDecoder(strings.NewReader(withExpiry))
	dec2.DisallowUnknownFields()
	if err := dec2.Decode(&pushSubscribeRequest{}); err != nil {
		t.Fatalf("a subscription with a non-null expirationTime was rejected: %v", err)
	}

	// Unknown fields must still be refused: this is an unauthenticated
	// endpoint and the strictness is deliberate.
	const unexpected = `{"endpoint":"https://fcm.googleapis.com/fcm/send/abcdefghijklmnop","surprise":1,"keys":{"p256dh":"x","auth":"y"}}`
	dec3 := json.NewDecoder(strings.NewReader(unexpected))
	dec3.DisallowUnknownFields()
	if err := dec3.Decode(&pushSubscribeRequest{}); err == nil {
		t.Fatalf("an unknown field was accepted; the decoder must stay strict")
	}
}
