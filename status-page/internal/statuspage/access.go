package statuspage

import (
	"bytes"
	"context"
	_ "embed"
	"errors"
	"html/template"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"

	"github.com/yassinebenameur/probara/shared/pageaccess"
	"github.com/yassinebenameur/probara/shared/securityheaders"
	"github.com/yassinebenameur/probara/shared/statustemplate"
)

// Page access: visibility, visitor passwords and kiosk tokens
// (shared/pageaccess is the contract with the API, which writes them).
//
// authorize runs at the top of HandleStatusPage, before the method guard,
// every sub-route and the render cache (keyed on the slug alone, so a cached
// render must never be what decides). It keys on the first path segment, so
// any route served under /public/status/{slug}/ is gated without being
// listed; the exemptions are explicit: unlockSuffix (the password form's
// target) and, on the draft preview only, a valid preview token.
// TestRouteAccess_EveryRouteIsClassified fails when a route is added
// without a row in its table.
//
// Denials never confirm that a private page exists: a private page without a
// credential, and a slug no page has, get the same notFound answer from the
// same single query.

// unlockSuffix is the password form's target, the one exempt sub-route.
const unlockSuffix = "/unlock"

// previewSuffix is the draft preview. On a gated page it opens with a valid
// preview token (minted by the API for the template editor) as well as with
// a visitor session.
const previewSuffix = "/preview/draft"

// tokenParam carries a kiosk token on the page URL. On a gated page a valid
// one is swapped for a session cookie; on every page the browser is sent back
// to the URL without it.
const tokenParam = "token"

// unlockMaxBody bounds the unlock form. Failed attempts are throttled by
// failureThrottle (unlock_throttle.go).
const unlockMaxBody = 4096

// Session cookie Secure modes (config.StatusPageConfig.SessionCookieSecure).
const (
	cookieSecureDetect = iota // Secure exactly when the request arrived over HTTPS
	cookieSecureAlways
	cookieSecureNever
)

//go:embed unlock.gohtml
var unlockTemplateSource string

var unlockTemplate = template.Must(template.New("unlock").Parse(unlockTemplateSource))

// accessGate is the gate's state.
type accessGate struct {
	store accessQuery
	// sessions signs visitor cookies; nil without STATUS_PAGE_SESSION_SECRET,
	// which leaves every password and private page closed.
	sessions     *sessionSigner
	failures     *failureThrottle
	cookieSecure int
	warnNoSecret sync.Once
	now          func() time.Time
}

func newAccessGate(store accessQuery, secret string, cookieSecure int) *accessGate {
	return &accessGate{
		store:        store,
		sessions:     newSessionSigner(secret),
		failures:     newFailureThrottle(),
		cookieSecure: cookieSecure,
		now:          time.Now,
	}
}

// splitStatusPath splits the path after /public/status/ into the slug (its
// first segment) and the rest ("" for the page itself).
func splitStatusPath(path string) (slug, rest string) {
	if i := strings.IndexByte(path, '/'); i >= 0 {
		return path[:i], path[i:]
	}
	return path, ""
}

// isPageRoute reports whether rest names the page itself.
func isPageRoute(rest string) bool {
	return rest == "" || rest == "/"
}

// authorize decides whether r may see the page at slug. On success it
// returns the writer the route must use, which forces the access headers
// (noindex, and no-store for gated pages) whatever the route sets, and the
// page's access record. On failure it has already answered.
func (h *Handlers) authorize(w http.ResponseWriter, r *http.Request, slug, rest string) (*accessWriter, *pageAccess, bool) {
	if slug == "" || h.access == nil || h.access.store == nil {
		writeNotFound(w)
		return nil, nil, false
	}

	sessionValue := ""
	if c, err := r.Cookie(sessionCookieName); err == nil {
		sessionValue = c.Value
	}
	token, hasToken := pageToken(r, rest)
	presentedHash := ""
	if token != "" {
		presentedHash = pageaccess.HashToken(token)
	}

	acc, err := h.access.store.PageAccess(r.Context(), slug, presentedHash, peekSessionTokenID(sessionValue))
	if err != nil {
		h.writeStatusPageError(w, slug, err, "Failed to load status page access")
		return nil, nil, false
	}

	aw := &accessWriter{ResponseWriter: w, noIndex: pageaccess.NoIndex(acc.Visibility), noStore: pageaccess.Gated(acc.Visibility)}
	if !pageaccess.Gated(acc.Visibility) {
		if hasToken {
			// A token stays out of the address bar on every page: a private
			// page made unlisted for a while must not leave its kiosk tokens
			// in URLs that the page's own scripts (a custom template) can
			// read and its refreshes re-send.
			redirectWithoutToken(aw, r)
			return nil, nil, false
		}
		return aw, acc, true
	}

	// Pages on one origin share it, so another page's script (a custom
	// template runs script) could otherwise request this page with the
	// visitor's cookie and read the answer, or frame it.
	if crossPageRequest(r, slug) {
		h.denyAccess(w, aw, r, acc, slug, rest, false)
		return nil, nil, false
	}

	if h.access.sessions == nil {
		h.access.warnNoSecret.Do(func() {
			h.logger.Warn("A password or private status page was requested but STATUS_PAGE_SESSION_SECRET is not set; such pages cannot be opened until it is")
		})
		h.denyAccess(w, aw, r, acc, slug, rest, true)
		return nil, nil, false
	}

	sessionOK := h.sessionValid(sessionValue, acc)
	if hasToken {
		// A token in the URL is always taken out of it, so it is not re-sent
		// by the page's own refreshes or carried in a Referer. A valid one
		// becomes the session first. Wrong tokens spend the same failure
		// budget as wrong passwords, and a client over it gets no check.
		client := h.clientIPs.BucketKey(r)
		if token != "" && h.access.failures.allowed(client, acc.ID) {
			if acc.PresentedTokenID != uuid.Nil && pageaccess.AcceptsTokens(acc.Visibility) {
				h.setSession(aw, r, slug, acc.ID, sessionCredential{kind: sessionKindToken, tokenID: acc.PresentedTokenID})
				redirectWithoutToken(aw, r)
				return nil, nil, false
			}
			h.access.failures.fail(client, acc.ID)
		}
		if sessionOK {
			redirectWithoutToken(aw, r)
			return nil, nil, false
		}
		h.denyAccess(w, aw, r, acc, slug, rest, false)
		return nil, nil, false
	}
	if sessionOK {
		return aw, acc, true
	}
	if rest == previewSuffix && previewTokenValid(acc.ID, r) {
		return aw, acc, true
	}
	h.denyAccess(w, aw, r, acc, slug, rest, false)
	return nil, nil, false
}

// pageToken returns the kiosk token on a GET of the page itself. hasToken is
// true when the parameter is present at all (even empty), so it is always
// stripped. Sub-routes never take one: the draft preview's own ?token= is a
// preview token, and the rest are reached with the session cookie.
func pageToken(r *http.Request, rest string) (token string, hasToken bool) {
	if !isPageRoute(rest) || r.Method != http.MethodGet {
		return "", false
	}
	q := r.URL.Query()
	if _, ok := q[tokenParam]; !ok {
		return "", false
	}
	return strings.TrimSpace(q.Get(tokenParam)), true
}

// sessionValid checks a session cookie against the page as it is now: a
// password session needs the current password_version on a page that still
// takes passwords, a token session a live token on a page that still takes
// tokens.
func (h *Handlers) sessionValid(value string, acc *pageAccess) bool {
	if value == "" || h.access.sessions == nil {
		return false
	}
	cred, ok := h.access.sessions.verify(acc.ID, value, h.access.now())
	if !ok {
		return false
	}
	switch cred.kind {
	case sessionKindPassword:
		return pageaccess.AcceptsPassword(acc.Visibility) && acc.PasswordHash != "" &&
			cred.passwordVersion == acc.PasswordVersion
	case sessionKindToken:
		// SessionTokenLive was resolved for the id peeked from this same
		// cookie, which verify has now authenticated.
		return pageaccess.AcceptsTokens(acc.Visibility) && acc.SessionTokenLive
	}
	return false
}

// stillAuthorized re-runs the gate for a long-lived response (the SSE
// stream) without answering anything: false once the page is gone, it turned
// password or private under a public stream, or the session expired, the
// password changed, the token was revoked or the page stopped accepting it.
// It runs for every stream, so a lost access-change event cannot leave one
// open.
func (h *Handlers) stillAuthorized(ctx context.Context, r *http.Request, slug string) bool {
	sessionValue := ""
	if c, err := r.Cookie(sessionCookieName); err == nil {
		sessionValue = c.Value
	}
	acc, err := h.access.store.PageAccess(ctx, slug, "", peekSessionTokenID(sessionValue))
	if err != nil {
		// A failed check ends the stream; the browser reconnects through
		// authorize.
		return false
	}
	if !pageaccess.Gated(acc.Visibility) {
		return true
	}
	return h.sessionValid(sessionValue, acc)
}

// previewTokenValid accepts a draft-preview token minted by the API. Only
// when the deployment sets STATUS_PAGE_PREVIEW_SECRET: an open preview must
// not open a gated page.
func previewTokenValid(pageID uuid.UUID, r *http.Request) bool {
	secret := previewSecret()
	if secret == "" {
		return false
	}
	return statustemplate.VerifyPreviewToken(secret, pageID.String(), r.URL.Query().Get("token"), time.Now())
}

func (h *Handlers) setSession(w http.ResponseWriter, r *http.Request, slug string, pageID uuid.UUID, cred sessionCredential) {
	ttl := cred.ttl()
	expires := h.access.now().Add(ttl)
	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookieName,
		Value:    h.access.sessions.sign(pageID, cred, expires),
		Path:     sessionCookiePath(slug),
		Expires:  expires,
		MaxAge:   int(ttl.Seconds()),
		HttpOnly: true,
		// Lax: a kiosk link or a bookmark is a top-level navigation. A gated
		// page framed by another site gets no cookie.
		SameSite: http.SameSiteLaxMode,
		Secure:   h.cookieSecure(r),
	})
}

// cookieSecure is the session cookie's Secure flag: STATUS_PAGE_COOKIE_SECURE
// when set, else on when STATUS_PAGE_BASE_URL is https (the usual setup is
// TLS terminated at an ingress, which request detection cannot see unless
// TRUSTED_PROXY_CIDRS names it), else detected from the request.
func (h *Handlers) cookieSecure(r *http.Request) bool {
	switch h.access.cookieSecure {
	case cookieSecureAlways:
		return true
	case cookieSecureNever:
		return false
	}
	return securityheaders.IsHTTPS(r, h.clientIPs)
}

// crossPageRequest reports whether r for the gated page at slug was made by
// another status page on this origin rather than by the page itself or by a
// top-level navigation. Same-origin pages share cookies' reach, so without
// this a custom template on page A could fetch page B's /data with the
// visitor's session for B and read it, or frame B.
//
//   - Sec-Fetch-Site cross-site: the Lax session cookie is not sent on a
//     cross-site sub-request anyway; none: the visitor typed or bookmarked it.
//   - A top-level navigation (Sec-Fetch-Mode navigate, Dest document) is
//     allowed from anywhere; the navigating page cannot read the result.
//   - Any other same-origin or same-site request must name this page (or a
//     non-status-page path, such as the operator UI's preview) in Referer;
//     the page's own requests always carry one (strict-origin-when-cross-
//     origin), so a missing Referer is refused.
//   - Browsers that send no Sec-Fetch-* headers get the Referer check alone.
//
// What this cannot stop is a same-origin window the other page opened itself
// (a navigation it can then read); the docs state that limit.
func crossPageRequest(r *http.Request, slug string) bool {
	site := r.Header.Get("Sec-Fetch-Site")
	switch site {
	case "cross-site", "none":
		return false
	}
	refSlug, hasReferer := refererPage(r)
	if site == "" {
		return hasReferer && refSlug != "" && refSlug != slug
	}
	if r.Header.Get("Sec-Fetch-Mode") == "navigate" {
		if dest := r.Header.Get("Sec-Fetch-Dest"); dest == "document" || dest == "" {
			return false
		}
	}
	if !hasReferer {
		return true
	}
	return refSlug != "" && refSlug != slug
}

// refererPage returns the status page slug a same-host Referer names ("" for
// any other path), and whether a usable same-host Referer was sent at all.
func refererPage(r *http.Request) (slug string, ok bool) {
	raw := r.Header.Get("Referer")
	if raw == "" {
		return "", false
	}
	u, err := url.Parse(raw)
	if err != nil || !strings.EqualFold(u.Host, r.Host) {
		// Another host's page is not a status page of this origin.
		return "", err == nil
	}
	rest, isPage := strings.CutPrefix(u.Path, "/public/status/")
	if !isPage {
		return "", true
	}
	slug, _ = splitStatusPath(rest)
	return slug, true
}

// gatedFrameAncestors is a password or private page's frame-ancestors list:
// only the operator UI origin, for the template editor, and never 'self'. A
// CSP source cannot name a path, so 'self' would let every other page on
// this origin frame it (crossPageRequest refuses those loads too).
func gatedFrameAncestors(appBaseURL string) []string {
	var out []string
	for _, a := range securityheaders.FrameAncestors(appBaseURL) {
		if a != "'self'" {
			out = append(out, a)
		}
	}
	return out
}

// redirectWithoutToken sends the browser back to the URL it asked for minus
// the token parameter. no-referrer on this one response keeps the URL that
// still carries the token out of any Referer the redirect could produce.
func redirectWithoutToken(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	q.Del(tokenParam)
	clean := url.URL{Path: r.URL.Path, RawQuery: q.Encode()}
	w.Header().Set(securityheaders.HeaderReferrerPolicy, "no-referrer")
	http.Redirect(w, r, clean.String(), http.StatusSeeOther)
}

// denyAccess answers a request the gate refused. A private page answers on
// raw (no access headers beyond writeNotFound's own) exactly like a slug no
// page has. A password page's own URL gets the unlock form; its sub-routes a
// bare 401.
func (h *Handlers) denyAccess(raw http.ResponseWriter, w *accessWriter, r *http.Request, acc *pageAccess, slug, rest string, unavailable bool) {
	if !pageaccess.AcceptsPassword(acc.Visibility) {
		writeNotFound(raw)
		return
	}
	if unavailable {
		http.Error(w, "This status page is temporarily unavailable", http.StatusServiceUnavailable)
		return
	}
	if isPageRoute(rest) && r.Method == http.MethodGet {
		h.writeUnlockPage(w, acc, slug, http.StatusUnauthorized, "")
		return
	}
	http.Error(w, "Password required", http.StatusUnauthorized)
}

// writeNotFound is the one answer for a missing page, a private page without
// a credential and an unknown sub-route, so none of them can be told apart
// (status, body and headers).
func writeNotFound(w http.ResponseWriter) {
	w.Header().Set("Cache-Control", "private, no-store")
	w.Header().Set("X-Robots-Tag", "noindex")
	http.Error(w, "Status page not found", http.StatusNotFound)
}

// hexColor is the only accent the unlock page accepts from the page's
// branding; anything else falls back to the default.
var hexColor = regexp.MustCompile(`^#(?:[0-9a-fA-F]{3}|[0-9a-fA-F]{6})$`)

type unlockView struct {
	Title   string
	LogoURL string
	Accent  template.CSS
	Action  string
	Error   string
}

func (h *Handlers) writeUnlockPage(w http.ResponseWriter, acc *pageAccess, slug string, status int, message string) {
	view := unlockView{
		Title:  acc.Title,
		Action: sessionCookiePath(slug) + unlockSuffix,
		Error:  message,
		Accent: template.CSS("#ff5a24"),
	}
	if hexColor.MatchString(acc.PrimaryColor) {
		// Validated above, so it cannot break out of the declaration.
		view.Accent = template.CSS(acc.PrimaryColor)
	}
	if logo := strings.TrimSpace(acc.LogoURL); strings.HasPrefix(logo, "https://") || strings.HasPrefix(logo, "http://") {
		view.LogoURL = logo
	}
	var buf bytes.Buffer
	if err := unlockTemplate.Execute(&buf, view); err != nil {
		h.logger.WithError(err).Error("Failed to render unlock page")
		http.Error(w, "Password required", status)
		return
	}
	csp := pageCSP{
		mode:           securityheaders.CSPModeStrict,
		frameAncestors: gatedFrameAncestors(h.appBaseURL()),
	}
	html := csp.apply(w.Header(), buf.String())
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(status)
	_, _ = w.Write([]byte(html))
}

// HandleUnlock handles /public/status/{slug}/unlock: POST checks the visitor
// password and opens a session; GET goes back to the page (which shows the
// form when needed). Exempt from authorize, which is what sends visitors
// here, so it answers a private page and a missing one identically itself.
func (h *Handlers) HandleUnlock(w http.ResponseWriter, r *http.Request, slug string) {
	if h.access == nil || h.access.store == nil || slug == "" {
		writeNotFound(w)
		return
	}
	gw := &accessWriter{ResponseWriter: w, noIndex: true, noStore: true}
	acc, err := h.access.store.PageAccess(r.Context(), slug, "", uuid.Nil)
	if err != nil {
		h.writeStatusPageError(w, slug, err, "Failed to load status page access")
		return
	}
	if !pageaccess.AcceptsPassword(acc.Visibility) {
		if pageaccess.Gated(acc.Visibility) {
			writeNotFound(w)
			return
		}
		// Public and unlisted pages need no unlocking.
		http.Redirect(gw, r, sessionCookiePath(slug), http.StatusSeeOther)
		return
	}

	switch r.Method {
	case http.MethodGet:
		http.Redirect(gw, r, sessionCookiePath(slug), http.StatusSeeOther)
		return
	case http.MethodPost:
	default:
		gw.Header().Set("Allow", "GET, POST")
		http.Error(gw, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if h.access.sessions == nil {
		http.Error(gw, "This status page is temporarily unavailable", http.StatusServiceUnavailable)
		return
	}
	if !h.pushOriginOK(r) {
		http.Error(gw, "Forbidden", http.StatusForbidden)
		return
	}
	// The page budget keys on the resolved page ID, never the slug: the slug
	// is raw request text, and a fresh slug per request would mint a fresh
	// bucket. Only a failure is charged, after the check.
	client := h.clientIPs.BucketKey(r)
	if !h.access.failures.allowed(client, acc.ID) {
		gw.Header().Set("Retry-After", "60")
		h.writeUnlockPage(gw, acc, slug, http.StatusTooManyRequests, "Too many attempts. Try again in a minute.")
		return
	}

	r.Body = http.MaxBytesReader(gw, r.Body, unlockMaxBody)
	if err := r.ParseForm(); err != nil {
		var maxErr *http.MaxBytesError
		if errors.As(err, &maxErr) {
			http.Error(gw, "Request body too large", http.StatusRequestEntityTooLarge)
			return
		}
		http.Error(gw, "Invalid form", http.StatusBadRequest)
		return
	}
	if !pageaccess.CheckPassword(acc.PasswordHash, r.PostForm.Get("password")) {
		h.access.failures.fail(client, acc.ID)
		h.writeUnlockPage(gw, acc, slug, http.StatusUnauthorized, "That password is not correct.")
		return
	}
	h.setSession(gw, r, slug, acc.ID, sessionCredential{kind: sessionKindPassword, passwordVersion: acc.PasswordVersion})
	http.Redirect(gw, r, sessionCookiePath(slug), http.StatusSeeOther)
}

// authorizeProxy applies the gate to /_sp_api calls that name a status page.
// Visitor sessions are path-scoped to the page, so none reaches /_sp_api: a
// password or private page's proxy calls are refused outright, and a missing
// page answers the same. Calls naming no page carry nothing page-specific.
func (h *Handlers) authorizeProxy(w http.ResponseWriter, r *http.Request, pageID uuid.UUID) bool {
	if pageID == uuid.Nil {
		return true
	}
	if h.access == nil || h.access.store == nil {
		http.NotFound(w, r)
		return false
	}
	visibility, err := h.access.store.PageVisibilityByID(r.Context(), pageID)
	if err != nil {
		if !errors.Is(err, ErrStatusPageNotFound) {
			h.logger.WithError(err).Error("Failed to load status page access for API proxy")
		}
		http.NotFound(w, r)
		return false
	}
	if pageaccess.Gated(visibility) {
		http.NotFound(w, r)
		return false
	}
	return true
}

// accessWriter forces the access headers onto whatever a route writes: a
// route's own Cache-Control or validator (the page's no-cache revalidation, a
// public max-age) must not let a shared cache keep a gated response, and
// every non-public page says noindex on every response.
type accessWriter struct {
	http.ResponseWriter
	noIndex     bool
	noStore     bool
	wroteHeader bool
}

func (a *accessWriter) WriteHeader(code int) {
	if !a.wroteHeader {
		a.wroteHeader = true
		h := a.ResponseWriter.Header()
		if a.noIndex {
			h.Set("X-Robots-Tag", "noindex")
		}
		if a.noStore {
			markGatedResponse(h)
		}
	}
	a.ResponseWriter.WriteHeader(code)
}

// gated reports whether this response is for a password or private page.
// A route behind authorize (the page, /data, add-ons such as a summary or a
// badge) reads it to decide what it may expose; the headers are forced by
// WriteHeader either way.
func (a *accessWriter) gated() bool { return a.noStore }

// markGatedResponse sets what every response of a password or private page
// carries, whatever its route set: no shared or revalidated caching (no
// validator, so never a 304), no cross-origin read (no CORS grant, and a
// same-origin resource policy against no-cors embedding).
func markGatedResponse(h http.Header) {
	h.Set("Cache-Control", "private, no-store")
	h.Del("ETag")
	h.Del("Access-Control-Allow-Origin")
	h.Set("Cross-Origin-Resource-Policy", "same-origin")
}

func (a *accessWriter) Write(b []byte) (int, error) {
	if !a.wroteHeader {
		a.WriteHeader(http.StatusOK)
	}
	return a.ResponseWriter.Write(b)
}

// Flush keeps the SSE stream working through the wrapper.
func (a *accessWriter) Flush() {
	if !a.wroteHeader {
		a.WriteHeader(http.StatusOK)
	}
	if f, ok := a.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

// Unwrap lets http.ResponseController reach the underlying writer.
func (a *accessWriter) Unwrap() http.ResponseWriter { return a.ResponseWriter }

// noIndexMeta adds the robots meta tag right after <head> for a page that is
// not public, so custom templates carry it too. The header already says it;
// the tag covers saved copies.
func noIndexMeta(html string) string {
	const tag = `<meta name="robots" content="noindex" />`
	lower := strings.ToLower(html)
	for from := 0; ; {
		i := strings.Index(lower[from:], "<head")
		if i < 0 {
			return tag + html
		}
		i += from
		// <head> or <head attr…>, never <header>.
		if next := i + len("<head"); next < len(lower) && (lower[next] == '>' || lower[next] == ' ' || lower[next] == '\t' || lower[next] == '\n' || lower[next] == '\r') {
			end := strings.IndexByte(html[i:], '>')
			if end < 0 {
				return tag + html
			}
			at := i + end + 1
			return html[:at] + tag + html[at:]
		}
		from = i + len("<head")
	}
}
