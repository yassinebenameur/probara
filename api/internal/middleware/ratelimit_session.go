package middleware

import (
	"fmt"
	"net/http"
	"time"

	"github.com/golang-jwt/jwt/v5"

	"github.com/yassinebenameur/probara/shared/auth"
)

// sessionCookieState is what the rate limiter can tell about a request's
// admin_access cookie from its signature alone. It is a bucketing hint, never
// authentication: AuthMiddleware still validates the cookie and loads the
// user for every request it lets through.
type sessionCookieState int

const (
	// sessionNone: no cookie, a value this server did not sign, or one that
	// expired longer ago than the refresh lifetime (it can no longer lead to
	// a refresh, so it identifies nobody the limiter should favour).
	sessionNone sessionCookieState = iota
	// sessionLive: signed and unexpired. AuthMiddleware authenticates such a
	// request on the cookie alone and never reads a bearer credential, so no
	// guessable credential is looked up for it.
	sessionLive
	// sessionExpired: signed, expired less than the refresh lifetime ago.
	// Its 401 is what makes the client call refresh.
	sessionExpired
)

// sessionCookie classifies the request's session cookie (see
// sessionCookieState) and returns the user it names.
func (l *RateLimiter) sessionCookie(r *http.Request) (string, sessionCookieState) {
	if l.sessionSecret == "" {
		return "", sessionNone
	}
	c, err := r.Cookie(sessionCookieName)
	if err != nil || c.Value == "" {
		return "", sessionNone
	}
	return classifySessionToken(c.Value, l.sessionSecret, time.Now(), l.sessionRefreshLifetime)
}

// classifySessionToken checks an admin JWT's signature, issuer and audience
// and sorts it by expiry: live, expired within refreshLifetime, or neither.
// It deliberately skips the claims validation ParseAdminToken does, so it
// must never be used to authenticate anything; it lives here, unexported, so
// it cannot be.
func classifySessionToken(token, secret string, now time.Time, refreshLifetime time.Duration) (string, sessionCookieState) {
	if token == "" || secret == "" {
		return "", sessionNone
	}
	claims := &auth.AdminClaims{}
	_, err := jwt.ParseWithClaims(token, claims, func(t *jwt.Token) (interface{}, error) {
		if _, ok := t.Method.(*jwt.SigningMethodHMAC); !ok {
			return nil, fmt.Errorf("unexpected signing method")
		}
		return []byte(secret), nil
	}, jwt.WithoutClaimsValidation())
	if err != nil || claims.AdminID == "" || claims.Issuer != "probara-api" || claims.ExpiresAt == nil {
		return "", sessionNone
	}
	audienceOK := false
	for _, aud := range claims.Audience {
		if aud == "admin" {
			audienceOK = true
			break
		}
	}
	if !audienceOK {
		return "", sessionNone
	}
	expires := claims.ExpiresAt.Time
	switch {
	case now.Before(expires):
		return claims.AdminID, sessionLive
	case now.Sub(expires) < refreshLifetime:
		return claims.AdminID, sessionExpired
	default:
		return "", sessionNone
	}
}

// carriesCredentialHeader reports whether the request presents a bearer
// credential (an API key or MCP token) besides any cookie. AuthMiddleware
// falls through from an expired cookie to that credential and looks it up, so
// such a request is never treated as a refreshing session.
func carriesCredentialHeader(r *http.Request) bool {
	return r.Header.Get("Authorization") != "" || r.Header.Get("X-API-Key") != ""
}
