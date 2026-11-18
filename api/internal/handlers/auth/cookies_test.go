package auth

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/google/uuid"

	sharedauth "github.com/yassinebenameur/probara/shared/auth"
)

// The access cookie stays in the browser for the refresh lifetime while the
// token in it expires after the access lifetime: the browser keeps sending
// the signed, expired token with its refresh, which the rate limiter
// recognises without a lookup.
func TestAccessCookieOutlivesItsToken(t *testing.T) {
	h := testHandlers()
	h.cfg.AdminAccessTTLMinutes = 15
	h.cfg.AdminRefreshTTLDays = 30
	rec := httptest.NewRecorder()
	if err := h.setAccessCookie(rec, uuid.New(), uuid.New()); err != nil {
		t.Fatal(err)
	}
	cookies := (&http.Response{Header: rec.Header()}).Cookies()
	if len(cookies) != 1 || cookies[0].Name != accessCookieName {
		t.Fatalf("cookies = %v", cookies)
	}
	if want := int((30 * 24 * time.Hour).Seconds()); cookies[0].MaxAge != want {
		t.Fatalf("Max-Age = %d, want the refresh lifetime %d", cookies[0].MaxAge, want)
	}
	claims, err := sharedauth.ParseAdminToken(cookies[0].Value, h.cfg.AdminJWTSecret)
	if err != nil {
		t.Fatal(err)
	}
	if left := time.Until(claims.ExpiresAt.Time); left > 15*time.Minute || left < 14*time.Minute {
		t.Fatalf("token expires in %v, want the access lifetime", left)
	}
}
