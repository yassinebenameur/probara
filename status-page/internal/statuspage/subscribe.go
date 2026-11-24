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
	"github.com/yassinebenameur/probara/shared/pagedomains"
	"github.com/yassinebenameur/probara/shared/securityheaders"
	"github.com/yassinebenameur/probara/shared/statustemplate"
)

// Email subscriptions. The API stores subscribers and sends every mail
// (api/internal/services/pagesubscribers); this service stays read-only and
// only forwards a visitor's request to the API's public subscriber routes,
// presenting STATUS_PAGE_INTERNAL_TOKEN (pageaccess.ForwardTokenHeader): the
// API answers the subscribe and confirm routes for nobody else, so the gate
// and the throttle here cannot be bypassed.
//
//	POST /public/status/{slug}/subscribe                 the page's subscribe form (works without script)
//	GET  /public/status/{slug}/subscription              confirm/unsubscribe page a mail link opens (token in the fragment)
//	POST /public/status/{slug}/subscription/confirm      that page's Confirm button
//	POST /public/status/{slug}/subscription/unsubscribe  its Unsubscribe button
//
// The subscribe form is a sub-route of the page, served after authorize like
// every other: subscriptions exist on public and unlisted pages only (a mail
// would carry a gated page's text past its password), so a password page
// with a session answers "not available", and a private page answers like a
// missing one.
//
// The three link routes are the exception, served before authorize
// (handleSubscriptionLink): unsubscribing must work from an old mail however
// the page's visibility changed since. They never look the page up: what
// they answer depends only on the token the visitor brings, so a private
// page and a missing slug still answer identically, and nothing about the
// page shows beyond the title the mail already carried.
//
// The client's address never leaves this service: it throttles each client
// itself (subscribeThrottle) and forwards nothing that identifies the
// visitor. The API applies its own per-page and per-inbox limits.
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
// (clientip) has no bucket of its own and is limited by the page's. The
// link routes are charged to the client only: their tokens cannot be
// guessed, and a page bucket would let one client (or every visitor behind
// an untrusted proxy) block everyone's unsubscribe.
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

// allow charges one request from client on page (uuid.Nil: no page bucket),
// reporting whether it may proceed. A refused request charges nothing
// further.
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
	var p *bucketEntry
	if page != uuid.Nil {
		p = t.pages.get(page.String(), now)
		if p.lim.TokensAt(now) < 1 {
			return false
		}
	}
	if c != nil {
		c.lim.AllowN(now, 1)
	}
	if p != nil {
		p.lim.AllowN(now, 1)
	}
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
// the page opted in, it is public or unlisted, and the service can forward
// (it knows where the API is and holds the internal token). The API decides
// again on every request.
func (h *Handlers) subscriptionsOffered(visibility string, pageOptIn bool) bool {
	return pageOptIn && !pageaccess.Gated(visibility) && h.canForward()
}

func (h *Handlers) canForward() bool {
	return h.internalAPIURL() != "" && h.config != nil && h.config.InternalToken != ""
}

func (h *Handlers) internalAPIURL() string {
	if h.config == nil {
		return ""
	}
	return strings.TrimRight(h.config.InternalAPIURL, "/")
}

// isSubscriptionLinkRoute reports the routes a subscriber mail's links
// reach, served before authorize (handleSubscriptionLink).
func isSubscriptionLinkRoute(rest string) bool {
	return rest == subscriptionSuffix || rest == confirmSuffix || rest == unsubscribeSuffix
}

// handleSubscription serves the subscribe form's target after authorize.
// It reports false for any other path.
func (h *Handlers) handleSubscription(w *accessWriter, r *http.Request, slug, rest string, acc *pageAccess) bool {
	if rest != subscribeSuffix {
		return false
	}
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", http.MethodPost)
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return true
	}
	view := h.subscriptionBase(acc, pagePath(r, slug))
	if pageaccess.Gated(acc.Visibility) || !h.canForward() {
		h.renderSubscription(w, http.StatusNotFound, view.with("unavailable"))
		return true
	}
	if !h.subscribeOriginOK(r) {
		http.Error(w, "Forbidden", http.StatusForbidden)
		return true
	}
	if !h.subscribes.allow(h.clientIPs.BucketKey(r), acc.ID) {
		w.Header().Set("Retry-After", "60")
		h.renderSubscription(w, http.StatusTooManyRequests, view.with("throttled"))
		return true
	}
	if !parseSubscriptionForm(w, r, view, h) {
		return true
	}
	payload, _ := json.Marshal(map[string]string{"email": r.PostForm.Get("email")})
	status, body, err := h.forward(r.Context(), apiSubscribePath+url.PathEscape(slug)+"/subscribe", payload)
	if err != nil {
		h.logger.WithError(err).Warn("Failed to forward a subscription request to the API")
		h.renderSubscription(w, http.StatusServiceUnavailable, view.with("unavailable_now"))
		return true
	}
	h.renderSubscription(w, statusFor(status), view.with(stateFor(rest, status, body)))
	return true
}

func parseSubscriptionForm(w http.ResponseWriter, r *http.Request, view subscriptionView, h *Handlers) bool {
	r.Body = http.MaxBytesReader(w, r.Body, subscribeMaxBody)
	if err := r.ParseForm(); err != nil {
		var maxErr *http.MaxBytesError
		if errors.As(err, &maxErr) {
			http.Error(w, "Request body too large", http.StatusRequestEntityTooLarge)
			return false
		}
		h.renderSubscription(w, http.StatusBadRequest, view.with("invalid_link"))
		return false
	}
	return true
}

// handleSubscriptionLink serves the routes a subscriber mail's links reach,
// before and without the visibility gate: the confirm/unsubscribe page and
// its two buttons. It never reads the page: the answer depends on the token
// alone, so it is the same for a private page and a missing slug, and the
// only page detail it can show is the title the API returns for a valid
// token, which the mail already carried.
func (h *Handlers) handleSubscriptionLink(w http.ResponseWriter, r *http.Request, slug, rest string) {
	// No link back and no slug in the page: the answer must not differ
	// between a private page and a missing one. Form targets are relative
	// (…/subscription → …/subscription/confirm).
	_ = slug
	view := subscriptionView{Title: "Email updates", Accent: template.CSS("#ff5a24"), Nonce: statustemplate.NoncePlaceholder}
	if rest == subscriptionSuffix {
		if r.Method != http.MethodGet {
			w.Header().Set("Allow", http.MethodGet)
			http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
			return
		}
		h.renderLanding(w, r, view)
		return
	}
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", http.MethodPost)
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if !h.canForward() {
		h.renderSubscription(w, http.StatusServiceUnavailable, view.with("unavailable_now"))
		return
	}
	if !h.subscribeOriginOK(r) {
		http.Error(w, "Forbidden", http.StatusForbidden)
		return
	}
	if !h.subscribes.allow(h.clientIPs.BucketKey(r), uuid.Nil) {
		w.Header().Set("Retry-After", "60")
		h.renderSubscription(w, http.StatusTooManyRequests, view.with("throttled"))
		return
	}
	if !parseSubscriptionForm(w, r, view, h) {
		return
	}
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
	status, body, err := h.forward(r.Context(), path, payload)
	if err != nil {
		h.logger.WithError(err).Warn("Failed to forward a subscription request to the API")
		h.renderSubscription(w, http.StatusServiceUnavailable, view.with("unavailable_now"))
		return
	}
	var answer struct {
		PageTitle string `json:"page_title"`
	}
	_ = json.Unmarshal(body, &answer)
	if status == http.StatusOK && strings.TrimSpace(answer.PageTitle) != "" {
		view.Title = strings.TrimSpace(answer.PageTitle)
	}
	h.renderSubscription(w, statusFor(status), view.with(stateFor(rest, status, body)))
}

// forward POSTs a JSON body to the API's public subscriber route with the
// internal token. Nothing from the visitor's request goes along but the
// form value: no cookie, no forwarding header, no address.
func (h *Handlers) forward(ctx context.Context, path string, payload []byte) (int, []byte, error) {
	ctx, cancel := context.WithTimeout(ctx, subscribeForwardTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, h.internalAPIURL()+path, bytes.NewReader(payload))
	if err != nil {
		return 0, nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set(pageaccess.ForwardTokenHeader, h.config.InternalToken)
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

// subscribeOriginOK is pushOriginOK, also accepting a post from a platform
// host: the cached page's form targets the page's canonical origin (its
// custom domain when it has one), so a visitor reading the page on the
// platform host posts across to it.
func (h *Handlers) subscribeOriginOK(r *http.Request) bool {
	if h.pushOriginOK(r) {
		return true
	}
	u, err := url.Parse(strings.TrimSpace(r.Header.Get("Origin")))
	return err == nil && h.isPlatformHost(pagedomains.HostFromHeader(u.Host))
}

// subscriptionBase is a page's subscription view, pageURL the page's path
// as the visitor reaches it (pagePath).
func (h *Handlers) subscriptionBase(acc *pageAccess, pageURL string) subscriptionView {
	v := subscriptionView{Title: acc.Title, Accent: template.CSS("#ff5a24"), PageURL: pageURL, Nonce: statustemplate.NoncePlaceholder}
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
func (h *Handlers) renderLanding(w http.ResponseWriter, r *http.Request, v subscriptionView) {
	v.State = "landing"
	if r.URL.Query().Get("action") == "unsubscribe" {
		v.Heading = "Unsubscribe from email updates"
		v.Message = "Press the button to stop receiving emails from this status page."
		v.Action = "subscription/unsubscribe"
		v.Button = "Unsubscribe"
	} else {
		v.Heading = "Confirm your subscription"
		v.Message = "Press the button to start receiving email updates from this status page."
		v.Action = "subscription/confirm"
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
