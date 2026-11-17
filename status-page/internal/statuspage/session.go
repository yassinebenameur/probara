package statuspage

import (
	"crypto/hmac"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
)

// Visitor sessions on password and private pages (access.go).
//
// A visitor who passes the gate gets a cookie signed with
// STATUS_PAGE_SESSION_SECRET, naming the page and the credential it came
// from. Nothing about a session is stored, which keeps the service read-only:
//   - a password session is bound to the page's password_version, so setting
//     or clearing the password signs every password visitor out;
//   - a token session is bound to the kiosk token's id, and the gate checks on
//     every request that the token is still live, so revoking one token ends
//     exactly the displays that used it (a kiosk cannot re-enter a token that
//     was stripped from its URL, so revoking one must not sign out the rest).
// The cookie is path-scoped to the page (sessionCookiePath), so one browser
// holds an independent session per page under a single cookie name.

// sessionCookieName is the visitor session cookie. Path scoping keeps pages
// apart, so the name is constant.
const sessionCookieName = "probara_sp_session"

// Session lifetimes. A token session is long because a wall display can run
// for months without a reload and cannot type its token again; revocation is
// checked on every request, so the lifetime is not what ends it. Browsers cap
// cookie lifetimes at 400 days.
const (
	passwordSessionTTL = 30 * 24 * time.Hour
	tokenSessionTTL    = 400 * 24 * time.Hour
)

// Credential kinds carried in a session.
const (
	sessionKindPassword = "p"
	sessionKindToken    = "t"
)

// sessionCredential is what a visitor session was opened with.
type sessionCredential struct {
	kind            string
	passwordVersion int       // password sessions
	tokenID         uuid.UUID // token sessions
}

func (c sessionCredential) ttl() time.Duration {
	if c.kind == sessionKindToken {
		return tokenSessionTTL
	}
	return passwordSessionTTL
}

// sessionCookiePath scopes a page's session to the page and its sub-routes.
// Cookie path matching is segment-aware, so /public/status/acme never matches
// /public/status/acme-two.
func sessionCookiePath(slug string) string {
	return "/public/status/" + slug
}

// sessionSigner signs and verifies visitor session cookies.
type sessionSigner struct {
	key []byte
}

// newSessionSigner derives the cookie key from the session secret, domain
// separated so the secret is never used directly. nil for an empty secret.
func newSessionSigner(secret string) *sessionSigner {
	secret = strings.TrimSpace(secret)
	if secret == "" {
		return nil
	}
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte("probara/status-page-session/v1"))
	return &sessionSigner{key: mac.Sum(nil)}
}

const sessionFormat = "v1"

// sign returns a cookie value for pageID and c, valid until expires.
func (s *sessionSigner) sign(pageID uuid.UUID, c sessionCredential, expires time.Time) string {
	payload := strings.Join([]string{sessionFormat, pageID.String(), encodeCredential(c), strconv.FormatInt(expires.Unix(), 10)}, "|")
	return base64.RawURLEncoding.EncodeToString([]byte(payload)) + "." + base64.RawURLEncoding.EncodeToString(s.mac(payload))
}

// verify checks a cookie value for pageID and returns its credential. A
// session for another page, expired, tampered with or malformed is refused.
// Whether the credential still opens the page (password version, live token,
// a visibility that accepts it) is the caller's check.
func (s *sessionSigner) verify(pageID uuid.UUID, value string, now time.Time) (sessionCredential, bool) {
	if s == nil {
		return sessionCredential{}, false
	}
	payload, macBytes, ok := splitSession(value)
	if !ok || subtle.ConstantTimeCompare(macBytes, s.mac(payload)) != 1 {
		return sessionCredential{}, false
	}
	parts := strings.Split(payload, "|")
	if len(parts) != 4 || parts[0] != sessionFormat || parts[1] != pageID.String() {
		return sessionCredential{}, false
	}
	exp, err := strconv.ParseInt(parts[3], 10, 64)
	if err != nil || !now.Before(time.Unix(exp, 0)) {
		return sessionCredential{}, false
	}
	return decodeCredential(parts[2])
}

// peekSessionTokenID reads the token id of a token session WITHOUT checking
// the signature, so the gate can ask whether that token is live in the same
// query that loads the page. The answer is only ever used after verify.
func peekSessionTokenID(value string) uuid.UUID {
	payload, _, ok := splitSession(value)
	if !ok {
		return uuid.Nil
	}
	parts := strings.Split(payload, "|")
	if len(parts) != 4 {
		return uuid.Nil
	}
	c, ok := decodeCredential(parts[2])
	if !ok || c.kind != sessionKindToken {
		return uuid.Nil
	}
	return c.tokenID
}

func splitSession(value string) (payload string, mac []byte, ok bool) {
	encPayload, encMAC, found := strings.Cut(value, ".")
	if !found {
		return "", nil, false
	}
	p, err := base64.RawURLEncoding.DecodeString(encPayload)
	if err != nil {
		return "", nil, false
	}
	m, err := base64.RawURLEncoding.DecodeString(encMAC)
	if err != nil {
		return "", nil, false
	}
	return string(p), m, true
}

func encodeCredential(c sessionCredential) string {
	if c.kind == sessionKindToken {
		return sessionKindToken + ":" + c.tokenID.String()
	}
	return sessionKindPassword + ":" + strconv.Itoa(c.passwordVersion)
}

func decodeCredential(raw string) (sessionCredential, bool) {
	kind, val, ok := strings.Cut(raw, ":")
	if !ok {
		return sessionCredential{}, false
	}
	switch kind {
	case sessionKindPassword:
		v, err := strconv.Atoi(val)
		if err != nil {
			return sessionCredential{}, false
		}
		return sessionCredential{kind: sessionKindPassword, passwordVersion: v}, true
	case sessionKindToken:
		id, err := uuid.Parse(val)
		if err != nil || id == uuid.Nil {
			return sessionCredential{}, false
		}
		return sessionCredential{kind: sessionKindToken, tokenID: id}, true
	}
	return sessionCredential{}, false
}

func (s *sessionSigner) mac(payload string) []byte {
	m := hmac.New(sha256.New, s.key)
	m.Write([]byte(payload))
	return m.Sum(nil)
}
