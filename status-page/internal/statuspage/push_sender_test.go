package statuspage

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/yassinebenameur/probara/shared/config"
	"github.com/yassinebenameur/probara/shared/logger"
	"github.com/yassinebenameur/probara/shared/webpush"
)

// A real browser subscription's public key and auth secret, from the RFC 8291
// example. Only their shape matters here.
const (
	testP256dh = "BCVxsr7N_eNgVRqvHtD0zTZsEc6-VV-JvLexhqUzORcxaOzi6-AYWXvTBHm4bjyPjs7Vd8pZGH6SRpkNtoIAiw4"
	testAuth   = "BTBZMqHH6r4Tts7J_aSIgg"
)

func testSender(t *testing.T, base string) *pushSender {
	t.Helper()
	keys, err := webpush.GenerateKeys()
	if err != nil {
		t.Fatalf("GenerateKeys() error = %v", err)
	}
	return newPushSender(nil, &config.StatusPageConfig{
		StatusPageBaseURL: base,
		VAPIDPublicKey:    keys.Public,
		VAPIDPrivateKey:   keys.Private,
		VAPIDSubject:      "mailto:ops@example.com",
	}, logger.New("status-page", "error"))
}

// Pins the request shape a push service requires. Getting any of these wrong
// is accepted with a 2xx by some services and silently dropped by the
// browser, so asserting them here is the only cheap way to catch it.
func TestPushSender_SendsWellFormedRequest(t *testing.T) {
	var got *http.Request
	var body []byte

	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = r.Clone(r.Context())
		buf := make([]byte, 4096)
		n, _ := r.Body.Read(buf)
		body = buf[:n]
		w.WriteHeader(http.StatusCreated)
	}))
	defer srv.Close()

	p := testSender(t, "https://status.example.com")
	// A TLS server, because AuthorizationHeader refuses to sign for a
	// plaintext audience -- real push endpoints are always https. The
	// allowlist is enforced at subscribe time, not here, which is what lets
	// the sender post to a local test server at all.
	p.client = srv.Client()
	sub := pushSubscriptionRow{ID: uuid.New(), Endpoint: srv.URL + "/push/abc", P256dh: testP256dh, Auth: testAuth}

	if outcome := p.sendClassify(t.Context(), sub, []byte(`{"v":1}`)); outcome != pushOutcomeSent {
		t.Fatalf("sendClassify() = %v on a 201 response, want sent", outcome)
	}

	if got.Header.Get("Content-Encoding") != "aes128gcm" {
		t.Errorf("Content-Encoding = %q, want aes128gcm", got.Header.Get("Content-Encoding"))
	}
	if got.Header.Get("Content-Type") != "application/octet-stream" {
		t.Errorf("Content-Type = %q, want application/octet-stream", got.Header.Get("Content-Type"))
	}
	if ttl, err := strconv.Atoi(got.Header.Get("TTL")); err != nil || ttl <= 0 {
		t.Errorf("TTL = %q, want a positive integer", got.Header.Get("TTL"))
	}
	if auth := got.Header.Get("Authorization"); !strings.HasPrefix(auth, "vapid t=") {
		t.Errorf("Authorization = %q, want the vapid credential form", auth)
	}
	if len(body) == 0 {
		t.Errorf("request body is empty; the payload was not encrypted in")
	}
	if strings.Contains(string(body), `"v":1`) {
		t.Errorf("plaintext payload leaked into the request body")
	}
}

// 404 and 410 are the ONLY authoritative "this subscription is gone" signals.
func TestPushSender_TreatsGoneAsDeletable(t *testing.T) {
	for _, status := range []int{http.StatusNotFound, http.StatusGone} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(status)
			}))
			defer srv.Close()

			p := testSender(t, "")
			p.client = srv.Client()
			sub := pushSubscriptionRow{ID: uuid.New(), Endpoint: srv.URL, P256dh: testP256dh, Auth: testAuth}
			if outcome := p.sendClassify(t.Context(), sub, []byte(`{}`)); outcome != pushOutcomeGone {
				t.Fatalf("status %d classified as %v, want gone", status, outcome)
			}
		})
	}
}

// A 403 means the VAPID signature did not match the key the subscription was
// created with -- a misconfiguration or a key rotation, NOT a dead browser.
// Treating it as death would wipe every subscription on the platform the
// moment a key is rotated.
func TestPushSender_DoesNotDeleteOnForbidden(t *testing.T) {
	for _, status := range []int{http.StatusForbidden, http.StatusUnauthorized, http.StatusBadRequest, http.StatusInternalServerError, http.StatusTooManyRequests} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(status)
			}))
			defer srv.Close()

			p := testSender(t, "")
			p.client = srv.Client()
			sub := pushSubscriptionRow{ID: uuid.New(), Endpoint: srv.URL, P256dh: testP256dh, Auth: testAuth}

			if outcome := p.sendClassify(t.Context(), sub, []byte(`{}`)); outcome == pushOutcomeGone {
				t.Fatalf("status %d classified as gone; only 404/410 may delete a subscription", status)
			}
		})
	}
}

func TestPushSender_PayloadNamesTheMonitorAsThePageShowsIt(t *testing.T) {
	p := testSender(t, "https://status.example.com")
	monitorID := uuid.New()

	down := p.payloadFor(pendingDelivery{
		Slug: "acme", PageTitle: "Acme Status",
		MonitorID: monitorID, MonitorName: "API gateway", Kind: string(pushKindDown),
	})
	if down.Title != "Acme Status" {
		t.Errorf("Title = %q, want the page title", down.Title)
	}
	if !strings.Contains(down.Body, "API gateway") || !strings.Contains(down.Body, "down") {
		t.Errorf("Body = %q, want it to name the monitor and say it is down", down.Body)
	}
	// One notification slot per monitor, so a recovery replaces the outage
	// rather than stacking a second banner.
	if down.Tag != "m:"+monitorID.String() {
		t.Errorf("Tag = %q, want a per-monitor tag", down.Tag)
	}
	if down.URL != "https://status.example.com/public/status/acme" {
		t.Errorf("URL = %q, want the page deep link", down.URL)
	}

	up := p.payloadFor(pendingDelivery{
		Slug: "acme", PageTitle: "Acme Status",
		MonitorID: monitorID, MonitorName: "API gateway", Kind: string(pushKindRecovered),
	})
	if !strings.Contains(up.Body, "back up") {
		t.Errorf("recovery Body = %q, want it to say the monitor is back", up.Body)
	}
	if up.Tag != down.Tag {
		t.Errorf("recovery tag %q differs from outage tag %q; they must share a slot", up.Tag, down.Tag)
	}
}

// With no base URL configured the deep link is omitted rather than guessed;
// the service worker then falls back to its own registration scope, which is
// the page URL by construction.
func TestPushSender_OmitsDeepLinkWithoutBaseURL(t *testing.T) {
	p := testSender(t, "")
	if got := p.payloadFor(pendingDelivery{Slug: "acme"}).URL; got != "" {
		t.Fatalf("URL = %q with no STATUS_PAGE_BASE_URL, want empty", got)
	}
}

func TestPushSender_PayloadFitsTheSingleRecordLimit(t *testing.T) {
	p := testSender(t, "https://status.example.com")
	body, err := json.Marshal(p.payloadFor(pendingDelivery{
		Slug:        strings.Repeat("s", 200),
		PageTitle:   strings.Repeat("t", 300),
		MonitorID:   uuid.New(),
		MonitorName: strings.Repeat("m", 300),
		Kind:        string(pushKindDown),
	}))
	if err != nil {
		t.Fatalf("Marshal() error = %v", err)
	}
	if len(body) > webpush.MaxPayloadLength {
		t.Fatalf("payload is %d bytes, over the %d-byte single-record limit", len(body), webpush.MaxPayloadLength)
	}
}
