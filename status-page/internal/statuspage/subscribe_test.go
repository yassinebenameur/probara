package statuspage

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"

	"github.com/google/uuid"

	"github.com/yassinebenameur/probara/shared/securityheaders"
	"github.com/yassinebenameur/probara/shared/statustemplate"
)

// fakeSubscriberAPI records what the status page forwards and answers like
// the API's public subscriber routes.
type fakeSubscriberAPI struct {
	mu       sync.Mutex
	requests []forwarded
	status   int
	body     string
}

type forwarded struct {
	path    string
	body    map[string]string
	headers http.Header
}

func (f *fakeSubscriberAPI) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	raw, _ := io.ReadAll(r.Body)
	var body map[string]string
	_ = json.Unmarshal(raw, &body)
	f.mu.Lock()
	f.requests = append(f.requests, forwarded{path: r.URL.Path, body: body, headers: r.Header.Clone()})
	status, answer := f.status, f.body
	f.mu.Unlock()
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_, _ = io.WriteString(w, answer)
}

func subscribeFixture(t *testing.T, status int, body string) (*accessFixture, *fakeSubscriberAPI) {
	t.Helper()
	f := newAccessFixture(t, testSessionSecret)
	api := &fakeSubscriberAPI{status: status, body: body}
	srv := httptest.NewServer(api)
	t.Cleanup(srv.Close)
	f.h.config.InternalAPIURL = srv.URL
	return f, api
}

func formPost(path string, form url.Values) *http.Request {
	req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	return req
}

// The form posts without script; the address alone is forwarded, nothing
// identifying the visitor (no cookie, no forwarding header), and every
// accepted address gets the same "check your inbox" page.
func TestSubscribe_ForwardsTheAddressOnly(t *testing.T) {
	f, api := subscribeFixture(t, http.StatusAccepted, `{"state":"check_inbox"}`)
	req := formPost("/public/status/open/subscribe", url.Values{"email": {"Ops@Example.com"}})
	req.Header.Set("Cookie", "probara_admin=secret")
	req.Header.Set("X-Forwarded-For", "203.0.113.9")
	req.RemoteAddr = "198.51.100.7:4444"
	rec := f.do(req)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "Check your inbox") {
		t.Fatalf("= %d %s", rec.Code, rec.Body.String())
	}
	if len(api.requests) != 1 {
		t.Fatalf("forwarded %d requests", len(api.requests))
	}
	got := api.requests[0]
	if got.path != "/api/v1/public/status-pages/open/subscribe" || got.body["email"] != "Ops@Example.com" || len(got.body) != 1 {
		t.Fatalf("forwarded %+v", got)
	}
	for _, h := range []string{"Cookie", "X-Forwarded-For", "X-Real-Ip", "Forwarded"} {
		if got.headers.Get(h) != "" {
			t.Fatalf("forwarded %s: %q", h, got.headers.Get(h))
		}
	}
	if rec.Header().Get("Cache-Control") != "no-store" || !strings.Contains(rec.Header().Get(securityheaders.HeaderCSP), "script-src 'nonce-") {
		t.Fatalf("headers = %v", rec.Header())
	}
}

// What the API answers becomes the page the visitor sees.
func TestSubscribe_AnswerPages(t *testing.T) {
	cases := []struct {
		path   string
		status int
		body   string
		want   int
		text   string
	}{
		{"/subscribe", http.StatusBadRequest, `{"error":"validation_error"}`, http.StatusBadRequest, "That address does not work"},
		{"/subscribe", http.StatusTooManyRequests, `{}`, http.StatusTooManyRequests, "Not accepting subscriptions"},
		{"/subscribe", http.StatusNotFound, `{}`, http.StatusNotFound, "Subscriptions are not available"},
		{"/subscribe", http.StatusInternalServerError, `{}`, http.StatusServiceUnavailable, "Something went wrong"},
		{"/subscription/confirm", http.StatusOK, `{"state":"confirmed"}`, http.StatusOK, "You are subscribed"},
		{"/subscription/confirm", http.StatusGone, `{"state":"expired"}`, http.StatusGone, "This link has expired"},
		{"/subscription/confirm", http.StatusNotFound, `{"state":"invalid"}`, http.StatusNotFound, "This link is not valid"},
		{"/subscription/unsubscribe", http.StatusOK, `{"state":"unsubscribed"}`, http.StatusOK, "You are unsubscribed"},
	}
	for _, c := range cases {
		f, api := subscribeFixture(t, c.status, c.body)
		rec := f.do(formPost("/public/status/open"+c.path, url.Values{"email": {"a@example.com"}, "token": {"tok"}}))
		if rec.Code != c.want || !strings.Contains(rec.Body.String(), c.text) {
			t.Errorf("%s with API %d: = %d %q", c.path, c.status, rec.Code, rec.Body.String())
		}
		if c.path != "/subscribe" && (len(api.requests) != 1 || api.requests[0].body["token"] != "tok") {
			t.Errorf("%s forwarded %+v", c.path, api.requests)
		}
	}
}

// A mail client's one-click unsubscribe names the token in the query.
func TestSubscribe_OneClickTokenFromQuery(t *testing.T) {
	f, api := subscribeFixture(t, http.StatusOK, `{"state":"unsubscribed"}`)
	rec := f.do(formPost("/public/status/open/subscription/unsubscribe?token=qtok", url.Values{"List-Unsubscribe": {"One-Click"}}))
	if rec.Code != http.StatusOK || api.requests[0].path != "/api/v1/public/status-page-subscriptions/unsubscribe" || api.requests[0].body["token"] != "qtok" {
		t.Fatalf("= %d, forwarded %+v", rec.Code, api.requests)
	}
}

// Password pages take no subscriptions (a mail would carry the page's text
// past its password), even with a session: nothing is forwarded.
func TestSubscribe_GatedPagesTakeNone(t *testing.T) {
	f, api := subscribeFixture(t, http.StatusAccepted, `{}`)
	req := formPost("/public/status/locked/subscribe", url.Values{"email": {"a@example.com"}})
	req.AddCookie(f.passwordSession(2))
	rec := f.do(req)
	if rec.Code != http.StatusNotFound || !strings.Contains(rec.Body.String(), "not available") {
		t.Fatalf("= %d %s", rec.Code, rec.Body.String())
	}
	if rec.Header().Get("Cache-Control") != "private, no-store" {
		t.Fatalf("gated answer is cacheable: %v", rec.Header())
	}
	// Without a session the gate answers first, as on every route.
	if rec := f.do(formPost("/public/status/locked/subscribe", url.Values{"email": {"a@example.com"}})); rec.Code != http.StatusUnauthorized {
		t.Fatalf("no session = %d", rec.Code)
	}
	if rec := f.do(formPost("/public/status/internal/subscribe", url.Values{"email": {"a@example.com"}})); rec.Code != http.StatusNotFound {
		t.Fatalf("private = %d", rec.Code)
	}
	if len(api.requests) != 0 {
		t.Fatalf("forwarded %d requests for gated pages", len(api.requests))
	}
}

func TestSubscribe_MethodsOriginAndNoAPI(t *testing.T) {
	f, api := subscribeFixture(t, http.StatusAccepted, `{}`)
	if rec := f.get("/public/status/open/subscribe"); rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("GET /subscribe = %d", rec.Code)
	}
	if rec := f.do(formPost("/public/status/open/subscription", nil)); rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("POST /subscription = %d", rec.Code)
	}
	req := formPost("/public/status/open/subscribe", url.Values{"email": {"a@example.com"}})
	req.Header.Set("Origin", "https://evil.example")
	if rec := f.do(req); rec.Code != http.StatusForbidden {
		t.Fatalf("cross-origin = %d", rec.Code)
	}
	if len(api.requests) != 0 {
		t.Fatalf("forwarded %d", len(api.requests))
	}
	f.h.config.InternalAPIURL = ""
	if rec := f.do(formPost("/public/status/open/subscribe", url.Values{"email": {"a@example.com"}})); rec.Code != http.StatusNotFound {
		t.Fatalf("no API configured = %d", rec.Code)
	}
}

// Each client gets a small budget, every request charged; another client
// is unaffected.
func TestSubscribe_ThrottlesPerClient(t *testing.T) {
	f, api := subscribeFixture(t, http.StatusAccepted, `{}`)
	send := func(ip string) int {
		req := formPost("/public/status/open/subscribe", url.Values{"email": {"a@example.com"}})
		req.RemoteAddr = ip + ":1234"
		return f.do(req).Code
	}
	for i := 0; i < subscribePerClientBurst; i++ {
		if code := send("198.51.100.7"); code != http.StatusOK {
			t.Fatalf("request %d = %d", i, code)
		}
	}
	if code := send("198.51.100.7"); code != http.StatusTooManyRequests {
		t.Fatalf("over budget = %d", code)
	}
	if code := send("198.51.100.8"); code != http.StatusOK {
		t.Fatalf("another client = %d", code)
	}
	if len(api.requests) != subscribePerClientBurst+1 {
		t.Fatalf("forwarded %d", len(api.requests))
	}
}

// A page budget holds every client together.
func TestSubscribeThrottle_PageBudget(t *testing.T) {
	th := newSubscribeThrottle()
	page := uuid.New()
	allowed := 0
	for i := 0; i < 200; i++ {
		if th.allow("", page) {
			allowed++
		}
	}
	if allowed != subscribePerPageBurst {
		t.Fatalf("allowed %d unattributable requests, want the page burst %d", allowed, subscribePerPageBurst)
	}
}

// The link landing page acts on nothing: it carries the form, its one
// nonce'd script reads the token from the fragment, and no token is in the
// page.
func TestSubscriptionLanding(t *testing.T) {
	f, api := subscribeFixture(t, http.StatusOK, `{}`)
	rec := f.get("/public/status/open/subscription?action=unsubscribe")
	body := rec.Body.String()
	if rec.Code != http.StatusOK || !strings.Contains(body, `action="/public/status/open/subscription/unsubscribe"`) {
		t.Fatalf("= %d %s", rec.Code, body)
	}
	csp := rec.Header().Get(securityheaders.HeaderCSP)
	i := strings.Index(csp, "'nonce-")
	if i < 0 {
		t.Fatalf("CSP = %q", csp)
	}
	nonce := strings.TrimSuffix(strings.Fields(csp[i+len("'nonce-"):])[0], "';")
	nonce = strings.TrimSuffix(nonce, "'")
	if !strings.Contains(body, `<script nonce="`+nonce+`">`) || strings.Contains(body, statustemplate.NoncePlaceholder) {
		t.Fatalf("script nonce does not match the CSP %q", csp)
	}
	if len(api.requests) != 0 {
		t.Fatal("a GET forwarded something")
	}
	if rec := f.get("/public/status/open/subscription?action=confirm"); !strings.Contains(rec.Body.String(), "/subscription/confirm") {
		t.Fatal("confirm landing has no confirm form")
	}
}

// The form is offered only on public and unlisted pages that opted in, and
// only when the service can forward.
func TestSubscriptionsOffered(t *testing.T) {
	h := &Handlers{config: nil}
	if h.subscriptionsOffered("public", true) {
		t.Fatal("offered without an API to forward to")
	}
	f, _ := subscribeFixture(t, http.StatusOK, `{}`)
	for _, c := range []struct {
		visibility string
		optIn      bool
		want       bool
	}{
		{"public", true, true}, {"unlisted", true, true}, {"password", true, false}, {"private", true, false}, {"public", false, false}, {"", true, false},
	} {
		if got := f.h.subscriptionsOffered(c.visibility, c.optIn); got != c.want {
			t.Errorf("%q opt-in %v = %v", c.visibility, c.optIn, got)
		}
	}
}

func TestDefaultTemplate_SubscribeForm(t *testing.T) {
	data := &StatusPageData{ID: uuid.NewString(), Slug: "acme", Title: "Acme", SubscribeFormEnabled: true}
	html, err := renderPublicStatusPage(data, false)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(html, `action="/public/status/acme/subscribe"`) || !strings.Contains(html, `name="email"`) || !strings.Contains(html, `<label for="subscribeEmail">`) {
		t.Fatal("subscribe form missing")
	}
	data.SubscribeFormEnabled = false
	html, err = renderPublicStatusPage(data, false)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(html, "subscribeSection") {
		t.Fatal("subscribe form rendered while off")
	}
}
