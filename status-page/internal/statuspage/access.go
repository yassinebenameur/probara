package statuspage

import (
	"bytes"
	_ "embed"
	"errors"
	"html/template"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"

	"github.com/yassinebenameur/probara/shared/pageaccess"
	"github.com/yassinebenameur/probara/shared/securityheaders"
	"github.com/yassinebenameur/probara/shared/statustemplate"
)

// Page access control: visibility, visitor passwords and kiosk tokens
// (shared/pageaccess has the contract).
//
// authorize runs at the top of HandleStatusPage, before the method guard, any
// sub-route and the render cache (whose key is the slug alone, so a cached
// render must never be the thing that decides). It keys on the first path
// segment, so every route the dispatcher serves under /public/status/{slug}/
// is gated without being listed; only exemptSuffixes are not, and sw.js is
// served outside the dispatcher. TestAuthorize_GatesEveryDispatchedSuffix
// fails when a new suffix is routed without a row in its table.

// unlockSuffix is the password form's target, the one exempt sub-route.
const unlockSuffix = "/unlock"

// previewSuffix keeps its own token: a valid preview token opens the draft
// preview of a gated page, so the template editor works without a visitor
// session.
const previewSuffix = "/preview/draft"

// Unlock throttling: per client per page, and per page across all clients so
// a distributed guess still meets a ceiling. Each attempt costs a bcrypt
// comparison, which this also bounds.
const (
	unlockPerClientPerMinute = 5
	unlockPerClientBurst     = 5
	unlockPerPagePerMinute   = 60
	unlockPerPageBurst       = 20
	unlockMaxBody            = 4096
)

//go:embed unlock.gohtml
var unlockTemplateSource string

var unlockTemplate = template.Must(template.New("unlock").Parse(unlockTemplateSource))

// accessGate is the gate's state, embedded in Handlers.
type accessGate struct {
	store accessStore
	// sessions signs visitor cookies; nil without STATUS_PAGE_SESSION_SECRET,
	// which makes every password and private page unavailable.
	sessions       *pageaccess.Signer
	unlockByClient *pushRateLimiter
	unlockByPage   *pushRateLimiter
	warnNoSecret   sync.Once
	now            func() time.Time
}

func (h *Handlers) initAccess(service *Service) {
	h.access.unlockByClient = newKeyedRateLimiter(unlockPerClientPerMinute, unlockPerClientBurst)
	h.access.unlockByPage = newKeyedRateLimiter(unlockPerPagePerMinute, unlockPerPageBurst)
	h.access.now = time.Now
	if service != nil {
		h.access.store = service
	}
	if h.config != nil && h.config.SessionSecret != "" {
		// LoadStatusPageConfig already refused a short secret.
		if signer, err := pageaccess.NewSigner(h.config.SessionSecret); err == nil {
			h.access.sessions = signer
		} else if h.logger != nil {
			h.logger.WithError(err).Error("Invalid STATUS_PAGE_SESSION_SECRET; password and private status pages are unavailable")
		}
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

// authorize decides whether this request may see the page at slug. On true it
// returns the writer the route must use: for unlisted and gated pages it
// forces the noindex and no-store headers whatever the route sets. On false
// it has already answered.
func (h *Handlers) authorize(w http.ResponseWriter, r *http.Request, slug, rest string) (http.ResponseWriter, bool) {
	if slug == "" || h.access.store == nil {
		http.Error(w, "Status page not found", http.StatusNotFound)
		return nil, false
	}
	acc, err := h.access.store.PageAccess(r.Context(), slug)
	if err != nil {
		h.writeStatusPageError(w, slug, err, "Failed to load status page access")
		return nil, false
	}
	if !pageaccess.Gated(acc.Visibility) {
		if pageaccess.NoIndex(acc.Visibility) {
			return &accessWriter{ResponseWriter: w, noIndex: true}, true
		}
		return w, true
	}

	gw := &accessWriter{ResponseWriter: w, noIndex: true, noStore: true}
	if h.access.sessions == nil {
		h.access.warnNoSecret.Do(func() {
			if h.logger != nil {
				h.logger.Warn("A password or private status page was requested but STATUS_PAGE_SESSION_SECRET is not set; such pages are unavailable until it is")
			}
		})
		h.denyAccess(gw, r, acc, rest, true)
		return nil, false
	}
	if h.sessionValid(r, acc) {
		return gw, true
	}
	if rest == previewSuffix && previewTokenValid(acc.ID, r) {
		return gw, true
	}
	if token := r.URL.Query().Get("token"); token != "" && pageaccess.AcceptsTokens(acc.Visibility) {
		tokenID, ok, err := h.access.store.ActiveTokenID(r.Context(), acc.ID, pageaccess.HashToken(token))
		if err != nil {
			h.writeStatusPageError(w, slug, err, "Failed to check kiosk token")
			return nil, false
		}
		if ok {
			h.setSession(gw, r, acc.ID, pageaccess.Credential{Kind: pageaccess.KindToken, TokenID: tokenID})
			return gw, true
		}
	}
	h.denyAccess(gw, r, acc, rest, false)
	return nil, false
}

// sessionValid checks the visitor's cookie against the page as it is now: a
// password session needs the current password_version on a page that still
// takes passwords, a token session a live token on a page that still takes
// tokens.
func (h *Handlers) sessionValid(r *http.Request, acc *pageAccess) bool {
	c, err := r.Cookie(pageaccess.CookieName(acc.ID))
	if err != nil {
		return false
	}
	cred, ok := h.access.sessions.Verify(acc.ID, c.Value, h.access.now())
	if !ok {
		return false
	}
	switch cred.Kind {
	case pageaccess.KindPassword:
		return pageaccess.AcceptsPassword(acc.Visibility) && acc.PasswordHash != "" &&
			cred.PasswordVersion == acc.PasswordVersion
	case pageaccess.KindToken:
		if !pageaccess.AcceptsTokens(acc.Visibility) {
			return false
		}
		active, err := h.access.store.TokenActive(r.Context(), acc.ID, cred.TokenID)
		if err != nil {
			if h.logger != nil {
				h.logger.WithError(err).Error("Failed to check kiosk token session")
			}
			return false
		}
		return active
	}
	return false
}

// previewTokenValid accepts a draft-preview token minted by the API. Only when
// the deployment configures STATUS_PAGE_PREVIEW_SECRET: an open preview must
// not open a gated page.
func previewTokenValid(pageID uuid.UUID, r *http.Request) bool {
	secret := previewSecret()
	if secret == "" {
		return false
	}
	return statustemplate.VerifyPreviewToken(secret, pageID.String(), r.URL.Query().Get("token"), time.Now())
}

func (h *Handlers) setSession(w http.ResponseWriter, r *http.Request, pageID uuid.UUID, cred pageaccess.Credential) {
	ttl := cred.TTL()
	expires := h.access.now().Add(ttl)
	http.SetCookie(w, &http.Cookie{
		Name:  pageaccess.CookieName(pageID),
		Value: h.access.sessions.Sign(pageID, cred, expires),
		// "/" rather than the slug path: a page keeps its session across a
		// slug rename, and the per-page cookie name keeps pages apart.
		Path:     "/",
		Expires:  expires,
		MaxAge:   int(ttl.Seconds()),
		HttpOnly: true,
		// Lax: a kiosk link or bookmark is a top-level navigation. A gated
		// page framed by another site gets no cookie (documented).
		SameSite: http.SameSiteLaxMode,
		Secure:   securityheaders.IsHTTPS(r, h.clientIPs),
	})
}

// denyAccess answers a request the gate refused. A private page, and anything
// that is not a page, answers like a missing page, so a private slug is not
// confirmed to exist. A password page's own URL gets the unlock form.
func (h *Handlers) denyAccess(w http.ResponseWriter, r *http.Request, acc *pageAccess, rest string, unavailable bool) {
	if !pageaccess.AcceptsPassword(acc.Visibility) {
		http.Error(w, "Status page not found", http.StatusNotFound)
		return
	}
	if unavailable {
		http.Error(w, "This status page is temporarily unavailable", http.StatusServiceUnavailable)
		return
	}
	if (rest == "" || rest == "/") && r.Method == http.MethodGet {
		h.writeUnlockPage(w, r, acc, http.StatusUnauthorized, "")
		return
	}
	http.Error(w, "Password required", http.StatusUnauthorized)
}

// pagePath is the page URL an unlock (or any request) answers for.
func pagePath(r *http.Request) string {
	p := strings.TrimSuffix(r.URL.Path, unlockSuffix)
	return strings.TrimSuffix(p, "/")
}

func (h *Handlers) writeUnlockPage(w http.ResponseWriter, r *http.Request, acc *pageAccess, status int, message string) {
	var buf bytes.Buffer
	if err := unlockTemplate.Execute(&buf, struct {
		Title, Action, Error string
	}{Title: acc.Title, Action: pagePath(r) + unlockSuffix, Error: message}); err != nil {
		if h.logger != nil {
			h.logger.WithError(err).Error("Failed to render unlock page")
		}
		http.Error(w, "Password required", status)
		return
	}
	csp := pageCSP{
		mode:           securityheaders.CSPModeStrict,
		frameAncestors: securityheaders.FrameAncestors(h.appBaseURL(), nil),
	}
	html := csp.apply(w.Header(), buf.String())
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(status)
	_, _ = w.Write([]byte(html))
}

// HandleUnlock handles /public/status/{slug}/unlock: POST checks the visitor
// password and opens a session; GET returns to the page (which shows the form
// when needed). Exempt from authorize, which is what sends visitors here.
func (h *Handlers) HandleUnlock(w http.ResponseWriter, r *http.Request, slug string) {
	gw := &accessWriter{ResponseWriter: w, noIndex: true, noStore: true}
	switch r.Method {
	case http.MethodGet:
		http.Redirect(gw, r, pagePath(r), http.StatusSeeOther)
		return
	case http.MethodPost:
	default:
		gw.Header().Set("Allow", "GET, POST")
		http.Error(gw, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if slug == "" || h.access.store == nil {
		http.Error(gw, "Status page not found", http.StatusNotFound)
		return
	}
	if !h.pushOriginOK(r) {
		http.Error(gw, "Forbidden", http.StatusForbidden)
		return
	}
	if !h.access.unlockByClient.allow(h.clientIPs.Resolve(r)+"|"+slug) || !h.access.unlockByPage.allow(slug) {
		gw.Header().Set("Retry-After", "60")
		http.Error(gw, "Too many attempts; try again in a minute", http.StatusTooManyRequests)
		return
	}

	acc, err := h.access.store.PageAccess(r.Context(), slug)
	if err != nil {
		h.writeStatusPageError(gw, slug, err, "Failed to load status page access")
		return
	}
	if !pageaccess.AcceptsPassword(acc.Visibility) {
		if pageaccess.Gated(acc.Visibility) {
			http.Error(gw, "Status page not found", http.StatusNotFound)
			return
		}
		http.Redirect(gw, r, pagePath(r), http.StatusSeeOther)
		return
	}
	if h.access.sessions == nil {
		http.Error(gw, "This status page is temporarily unavailable", http.StatusServiceUnavailable)
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
		h.writeUnlockPage(gw, r, acc, http.StatusUnauthorized, "That password is not correct.")
		return
	}
	h.setSession(gw, r, acc.ID, pageaccess.Credential{Kind: pageaccess.KindPassword, PasswordVersion: acc.PasswordVersion})
	http.Redirect(gw, r, pagePath(r), http.StatusSeeOther)
}

// authorizeProxy applies the gate to /_sp_api calls naming a status page: a
// gated page's proxy needs the same visitor session as the page itself.
// Calls that name no page carry nothing page-specific and pass (the API
// authenticates them on its own).
func (h *Handlers) authorizeProxy(w http.ResponseWriter, r *http.Request, pageID uuid.UUID) bool {
	if pageID == uuid.Nil {
		return true
	}
	if h.access.store == nil {
		http.NotFound(w, r)
		return false
	}
	acc, err := h.access.store.PageAccessByID(r.Context(), pageID)
	if err != nil {
		if errors.Is(err, ErrStatusPageNotFound) {
			http.NotFound(w, r)
			return false
		}
		if h.logger != nil {
			h.logger.WithError(err).Error("Failed to load status page access for API proxy")
		}
		http.Error(w, "Internal server error", http.StatusInternalServerError)
		return false
	}
	if !pageaccess.Gated(acc.Visibility) {
		return true
	}
	if h.access.sessions != nil && h.sessionValid(r, acc) {
		w.Header().Set("Cache-Control", "private, no-store")
		return true
	}
	http.NotFound(w, r)
	return false
}

// accessWriter forces the access headers onto whatever a route writes: a
// route's own Cache-Control (a badge's public max-age, the page's no-cache
// revalidation) must not let a shared cache keep a gated response, and
// unlisted pages say noindex on every response.
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
			// No validator: a no-store body must not come back as a 304.
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

// Flush keeps SSE (/stream) working through the wrapper.
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

// noIndexHTML adds the robots meta tag to a page served through an
// accessWriter that says noindex, right after <head>, so custom templates get
// it too. The header already says it; the tag covers saved copies.
func noIndexHTML(w http.ResponseWriter, html string) string {
	aw, ok := w.(*accessWriter)
	if !ok || !aw.noIndex {
		return html
	}
	const tag = `<meta name="robots" content="noindex" />`
	lower := strings.ToLower(html)
	i := strings.Index(lower, "<head")
	if i < 0 {
		return tag + html
	}
	end := strings.IndexByte(html[i:], '>')
	if end < 0 {
		return tag + html
	}
	at := i + end + 1
	return html[:at] + tag + html[at:]
}

// gatedResponse reports whether w was handed out by authorize for a password
// or private page.
func gatedResponse(w http.ResponseWriter) bool {
	aw, ok := w.(*accessWriter)
	return ok && aw.noStore
}

// previewSecret is STATUS_PAGE_PREVIEW_SECRET (see HandleDraftPreview).
func previewSecret() string {
	return strings.TrimSpace(os.Getenv(previewSecretEnvVar))
}
