package statuspage

import (
	"bytes"
	"context"
	_ "embed"
	"encoding/json"
	"errors"
	"html/template"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"

	"github.com/yassinebenameur/probara/shared/pageaccess"
	"github.com/yassinebenameur/probara/shared/securityheaders"
	"github.com/yassinebenameur/probara/shared/statustemplate"
)

// Email subscriptions. The API stores subscribers and sends every mail
// (api/internal/services/pagesubscribers); this service stays read-only and
// only forwards a visitor's request to the API's public subscriber routes.
// The routes are sub-routes of the page, served after authorize like every
// other, so the page's visibility gate applies and the form is same-origin
// on any host the page is served from:
//
//	POST /public/status/{slug}/subscribe                 the page's subscribe form (works without script)
//	GET  /public/status/{slug}/subscription              confirm/unsubscribe page a mail link opens (token in the fragment)
//	POST /public/status/{slug}/subscription/confirm      that page's Confirm button
//	POST /public/status/{slug}/subscription/unsubscribe  its Unsubscribe button
//
// Subscriptions exist on public and unlisted pages only (a mail would carry
// a gated page's text past its password), so on a password page with a
// session the routes answer "not available", and a private page answers
// like a missing one from authorize, as on every route.
//
// The client's address never leaves this service: it throttles each client
// itself (subscribeThrottle) and forwards nothing that identifies the
// visitor, so the API trusts no forwarded address. The API applies its own
// per-page limits to whoever calls it.
const (
	subscribeSuffix         = "/subscribe"
	subscriptionSuffix      = "/subscription"
	confirmSuffix           = "/subscription/confirm"
	unsubscribeSuffix       = "/subscription/unsubscribe"
	subscribeMaxBody        = 4096
	subscribeForwardTimeout = 10 * time.Second
)

// The API's public subscriber routes (handlers/statuspagesubscribers).
const (
	apiSubscribePath   = "/api/v1/public/status-pages/"
	apiConfirmPath     = "/api/v1/public/status-page-subscriptions/confirm"
	apiUnsubscribePath = "/api/v1/public/status-page-subscriptions/unsubscribe"
)

// Per-client and per-page budgets for the subscription routes, every
// request charged (unlike the unlock throttle, which charges failures only:
// here every request may cost a mail). A client that is not attributable
// (clientip) has no bucket of its own and is limited by the page's.
const (
	subscribePerClientPerMinute = 5
	subscribePerClientBurst     = 5
	subscribePerPagePerMinute   = 60
	subscribePerPageBurst       = 20
	subscribeThrottleMaxKeys    = 10000
)

//go:embed subscription.gohtml
var subscriptionTemplateSource string

var subscriptionTemplate = template.Must(template.New("subscription").Parse(subscriptionTemplateSource))

// subscribeThrottle is a pair of LRU token-bucket maps (unlock_throttle.go's
// lruBuckets), charged on every request.
type subscribeThrottle struct {
	mu      sync.Mutex
	clients *lruBuckets
	pages   *lruBuckets
	now     func() time.Time
}

func newSubscribeThrottle() *subscribeThrottle {
	return &subscribeThrottle{
		clients: newLRUBuckets(subscribePerClientPerMinute, subscribePerClientBurst, subscribeThrottleMaxKeys),
		pages:   newLRUBuckets(subscribePerPagePerMinute, subscribePerPageBurst, subscribeThrottleMaxKeys),
		now:     time.Now,
	}
}

// allow charges one request from client on page, reporting whether it may
// proceed. A refused request charges nothing further.
func (t *subscribeThrottle) allow(client string, page uuid.UUID) bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	now := t.now()
	var c *bucketEntry
	if client != "" {
		c = t.clients.get(client, now)
		if c.lim.TokensAt(now) < 1 {
			return false
		}
	}
	p := t.pages.get(page.String(), now)
	if p.lim.TokensAt(now) < 1 {
		return false
	}
	if c != nil {
		c.lim.AllowN(now, 1)
	}
	p.lim.AllowN(now, 1)
	return true
}

// subscriptionView is what subscription.gohtml renders: the page's
// branding (as the unlock page reads it), and one of the states below.
type subscriptionView struct {
	Title   string
	LogoURL string
	Accent  template.CSS
	PageURL string
	State   string
	Heading string
	Message string
	Action  string // the landing page's form target
	Button  string
	// Nonce is the placeholder pageCSP.apply replaces with the response's
	// nonce, for the landing page's one script.
	Nonce string
}

// subscriptionsOffered reports whether the page shows the subscribe form:
// the page opted in, it is public or unlisted, and the service knows where
// the API is. The API decides again on every request.
func (h *Handlers) subscriptionsOffered(visibility string, pageOptIn bool) bool {
	return pageOptIn && !pageaccess.Gated(visibility) && h.internalAPIURL() != ""
}

func (h *Handlers) internalAPIURL() string {
	if h.config == nil {
		return ""
	}
	return strings.TrimRight(h.config.InternalAPIURL, "/")
}

// handleSubscription serves the subscription sub-routes after authorize.
// It reports false for a path that is not one of them.
func (h *Handlers) handleSubscription(w *accessWriter, r *http.Request, slug, rest string, acc *pageAccess) bool {
	switch rest {
	case subscribeSuffix, confirmSuffix, unsubscribeSuffix:
		if r.Method != http.MethodPost {
			w.Header().Set("Allow", http.MethodPost)
			http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
			return true
		}
	case subscriptionSuffix:
		if r.Method != http.MethodGet {
			w.Header().Set("Allow", http.MethodGet)
			http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
			return true
		}
	default:
		return false
	}
	view := h.subscriptionBase(acc, slug)
	if pageaccess.Gated(acc.Visibility) || h.internalAPIURL() == "" {
		h.renderSubscription(w, http.StatusNotFound, view.with("unavailable"))
		return true
	}
	if rest == subscriptionSuffix {
		h.renderLanding(w, r, view)
		return true
	}
	if !h.pushOriginOK(r) {
		http.Error(w, "Forbidden", http.StatusForbidden)
		return true
	}
	if !h.subscribes.allow(h.clientIPs.BucketKey(r), acc.ID) {
		w.Header().Set("Retry-After", "60")
		h.renderSubscription(w, http.StatusTooManyRequests, view.with("throttled"))
		return true
	}
	r.Body = http.MaxBytesReader(w, r.Body, subscribeMaxBody)
	if err := r.ParseForm(); err != nil {
		var maxErr *http.MaxBytesError
		if errors.As(err, &maxErr) {
			http.Error(w, "Request body too large", http.StatusRequestEntityTooLarge)
			return true
		}
		h.renderSubscription(w, http.StatusBadRequest, view.with("invalid_address"))
		return true
	}

	var status int
	var body []byte
	var err error
	switch rest {
	case subscribeSuffix:
		payload, _ := json.Marshal(map[string]string{"email": r.PostForm.Get("email")})
		status, body, err = h.forward(r.Context(), apiSubscribePath+url.PathEscape(slug)+"/subscribe", payload)
	case confirmSuffix, unsubscribeSuffix:
		token := strings.TrimSpace(r.PostForm.Get("token"))
		if token == "" {
			// A mail client's one-click POST names the token in the query.
			token = strings.TrimSpace(r.URL.Query().Get("token"))
		}
		payload, _ := json.Marshal(map[string]string{"token": token})
		path := apiConfirmPath
		if rest == unsubscribeSuffix {
			path = apiUnsubscribePath
		}
		status, body, err = h.forward(r.Context(), path, payload)
	}
	if err != nil {
		h.logger.WithError(err).Warn("Failed to forward a subscription request to the API")
		h.renderSubscription(w, http.StatusServiceUnavailable, view.with("unavailable_now"))
		return true
	}
	h.renderSubscription(w, statusFor(status), view.with(stateFor(rest, status, body)))
	return true
}

// forward POSTs a JSON body to the API's public subscriber route. Nothing
// from the visitor's request goes along but the form value: no cookie, no
// forwarding header, no address.
func (h *Handlers) forward(ctx context.Context, path string, payload []byte) (int, []byte, error) {
	ctx, cancel := context.WithTimeout(ctx, subscribeForwardTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, h.internalAPIURL()+path, bytes.NewReader(payload))
	if err != nil {
		return 0, nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := h.subscribeClient.Do(req)
	if err != nil {
		return 0, nil, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 16<<10))
	return resp.StatusCode, body, err
}

// stateFor maps the API's answer to the page shown.
func stateFor(rest string, status int, body []byte) string {
	var answer struct {
		State string `json:"state"`
	}
	_ = json.Unmarshal(body, &answer)
	switch {
	case status == http.StatusTooManyRequests:
		return "limit"
	case status == http.StatusBadRequest && rest == subscribeSuffix:
		return "invalid_address"
	case status == http.StatusNotFound && rest == subscribeSuffix:
		return "unavailable"
	case status == http.StatusGone:
		return "expired"
	case status == http.StatusNotFound:
		return "invalid_link"
	case status >= 300:
		return "unavailable_now"
	case rest == subscribeSuffix:
		return "check_inbox"
	case answer.State == "confirmed":
		return "confirmed"
	case answer.State == "unsubscribed":
		return "unsubscribed"
	}
	return "invalid_link"
}

func statusFor(apiStatus int) int {
	switch {
	case apiStatus == http.StatusAccepted:
		return http.StatusOK
	case apiStatus >= 500 || apiStatus == 0:
		return http.StatusServiceUnavailable
	}
	return apiStatus
}

func (h *Handlers) subscriptionBase(acc *pageAccess, slug string) subscriptionView {
	v := subscriptionView{Title: acc.Title, Accent: template.CSS("#ff5a24"), PageURL: sessionCookiePath(slug), Nonce: statustemplate.NoncePlaceholder}
	if hexColor.MatchString(acc.PrimaryColor) {
		// Validated above, so it cannot break out of the declaration.
		v.Accent = template.CSS(acc.PrimaryColor)
	}
	if logo := strings.TrimSpace(acc.LogoURL); strings.HasPrefix(logo, "https://") || strings.HasPrefix(logo, "http://") {
		v.LogoURL = logo
	}
	return v
}

// subscriptionMessages is every state's wording. check_inbox is the same
// whatever the address's state: the page never says whether an address is
// subscribed.
var subscriptionMessages = map[string][2]string{
	"check_inbox":     {"Check your inbox", "If this address is not subscribed yet, a confirmation link is on its way. Follow it within 48 hours to start receiving updates. If nothing arrives, check your spam folder."},
	"invalid_address": {"That address does not work", "Enter a plain email address, like name@example.com."},
	"limit":           {"Not accepting subscriptions right now", "This page has reached its subscription limit for now. Try again later."},
	"throttled":       {"Too many attempts", "Wait a minute, then try again."},
	"unavailable":     {"Subscriptions are not available", "This status page does not offer email updates."},
	"unavailable_now": {"Something went wrong", "Your request could not be completed right now. Try again in a few minutes."},
	"confirmed":       {"You are subscribed", "You will receive an email when an incident is published, updated or resolved on this page, and when maintenance is scheduled. Every email has a link to unsubscribe."},
	"unsubscribed":    {"You are unsubscribed", "You will receive no more emails from this status page."},
	"expired":         {"This link has expired", "Confirmation links work for 48 hours. Subscribe again from the status page to get a new one."},
	"invalid_link":    {"This link is not valid", "It may have been used already, or the subscription no longer exists."},
}

func (v subscriptionView) with(state string) subscriptionView {
	v.State = state
	if m, ok := subscriptionMessages[state]; ok {
		v.Heading, v.Message = m[0], m[1]
	}
	return v
}

// renderLanding is the page a confirmation or unsubscribe link opens. The
// token is in the URL fragment, which no server and no log sees; a small
// script moves it into the form, and the visitor presses the button: a GET
// never acts, since mail scanners open every link they find.
func (h *Handlers) renderLanding(w *accessWriter, r *http.Request, v subscriptionView) {
	v.State = "landing"
	if r.URL.Query().Get("action") == "unsubscribe" {
		v.Heading = "Unsubscribe from email updates"
		v.Message = "Press the button to stop receiving emails from this status page."
		v.Action = v.PageURL + unsubscribeSuffix
		v.Button = "Unsubscribe"
	} else {
		v.Heading = "Confirm your subscription"
		v.Message = "Press the button to start receiving email updates from this status page."
		v.Action = v.PageURL + confirmSuffix
		v.Button = "Confirm subscription"
	}
	h.renderSubscription(w, http.StatusOK, v)
}

func (h *Handlers) renderSubscription(w http.ResponseWriter, status int, v subscriptionView) {
	var buf bytes.Buffer
	if err := subscriptionTemplate.Execute(&buf, v); err != nil {
		h.logger.WithError(err).Error("Failed to render subscription page")
		http.Error(w, v.Heading, status)
		return
	}
	csp := pageCSP{mode: securityheaders.CSPModeStrict, frameAncestors: gatedFrameAncestors(h.appBaseURL())}
	html := csp.apply(w.Header(), buf.String())
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Robots-Tag", robotsNoIndex)
	w.Header().Set(securityheaders.HeaderReferrerPolicy, "no-referrer")
	w.WriteHeader(status)
	_, _ = w.Write([]byte(html))
}
