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

// tokenParam carries a kiosk token on the page URL. It is swapped for a
// session cookie and the browser is sent back to the URL without it.
const tokenParam = "token"

// Unlock throttling, per client per page and per page across all clients so
// a distributed guess still meets a ceiling. Each attempt costs one bcrypt
// comparison, which this bounds as well.
const (
	unlockPerClientPerMinute = 5
	unlockPerClientBurst     = 5
	unlockPerPagePerMinute   = 30
	unlockPerPageBurst       = 10
	unlockMaxBody            = 4096
)

//go:embed unlock.gohtml
var unlockTemplateSource string

var unlockTemplate = template.Must(template.New("unlock").Parse(unlockTemplateSource))

// accessGate is the gate's state.
type accessGate struct {
	store accessQuery
	// sessions signs visitor cookies; nil without STATUS_PAGE_SESSION_SECRET,
	// which leaves every password and private page closed.
	sessions       *sessionSigner
	unlockByClient *pushRateLimiter
	unlockByPage   *pushRateLimiter
	warnNoSecret   sync.Once
	now            func() time.Time
}

func newAccessGate(store accessQuery, secret string) *accessGate {
	return &accessGate{
		store:          store,
		sessions:       newSessionSigner(secret),
		unlockByClient: newKeyedRateLimiter(unlockPerClientPerMinute, unlockPerClientBurst),
		unlockByPage:   newKeyedRateLimiter(unlockPerPagePerMinute, unlockPerPageBurst),
		now:            time.Now,
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
		return aw, acc, true
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
		// A token in the URL is always taken out of it, so it is not kept in
		// history, re-sent by the page's own refreshes, or carried in a
		// Referer. A valid one becomes the session first.
		if acc.PresentedTokenID != uuid.Nil && pageaccess.AcceptsTokens(acc.Visibility) {
			h.setSession(aw, r, slug, acc.ID, sessionCredential{kind: sessionKindToken, tokenID: acc.PresentedTokenID})
			redirectWithoutToken(aw, r)
			return nil, nil, false
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
// stream) without answering anything: false once the session expired, the
// password changed, the token was revoked or the page stopped accepting it.
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
		Secure:   securityheaders.IsHTTPS(r, h.clientIPs),
	})
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
		frameAncestors: securityheaders.FrameAncestors(h.appBaseURL()),
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
	// Buckets key on the resolved page ID, never the slug: the slug is raw
	// request text, and a fresh slug per request would mint a fresh bucket.
	pageKey := acc.ID.String()
	if !h.access.unlockByClient.allow(h.clientIPs.Resolve(r)+"|"+pageKey) || !h.access.unlockByPage.allow(pageKey) {
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
			h.Set("Cache-Control", "private, no-store")
			// No validator: a no-store body must never come back as a 304.
			h.Del("ETag")
		}
	}
	a.ResponseWriter.WriteHeader(code)
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
