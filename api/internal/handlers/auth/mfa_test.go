package auth

import (
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/yassinebenameur/probara/shared/auth"
	"github.com/yassinebenameur/probara/shared/config"
)

func mfaTestHandlers(secret string) *Handlers {
	cfg := &config.APIConfig{}
	cfg.AdminJWTSecret = secret
	return &Handlers{cfg: cfg}
}

func TestMFATokenRoundTrip(t *testing.T) {
	h := mfaTestHandlers("test-secret-test-secret-test-secret")
	id := uuid.New()
	now := time.Now()
	token, err := h.mintMFAToken(id, "binding-1", now)
	if err != nil {
		t.Fatal(err)
	}
	gotID, binding, err := h.parseMFAToken(token, now.Add(time.Minute))
	if err != nil || gotID != id || binding != "binding-1" {
		t.Fatalf("parse = %v %q %v", gotID, binding, err)
	}

	if _, _, err := h.parseMFAToken(token, now.Add(mfaTokenTTL+time.Second)); err == nil {
		t.Fatal("expired token accepted")
	}
	parts := strings.Split(token, "~")
	tampered := parts[0] + "~" + parts[1] + "x~" + parts[2]
	if _, _, err := h.parseMFAToken(tampered, now); err == nil {
		t.Fatal("tampered token accepted")
	}
	if _, _, err := mfaTestHandlers("another-secret-another-secret-xx").parseMFAToken(token, now); err == nil {
		t.Fatal("token accepted under another secret")
	}
	for _, bad := range []string{"", "pmfa1", "pmfa1~a~b~c", "jwt.like.token"} {
		if _, _, err := h.parseMFAToken(bad, now); err == nil {
			t.Fatalf("%q accepted", bad)
		}
	}
}

// The step-one token must never work as an access token, and an access
// token must never pass as a step-one token.
func TestMFATokenIsNotAnAccessToken(t *testing.T) {
	secret := "test-secret-test-secret-test-secret"
	h := mfaTestHandlers(secret)
	id := uuid.New()
	token, err := h.mintMFAToken(id, "b", time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := auth.ParseAdminToken(token, secret); err == nil {
		t.Fatal("step-one token parsed as an admin access token")
	}
	access, err := auth.CreateAdminSessionToken(id.String(), uuid.NewString(), secret, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := h.parseMFAToken(access, time.Now()); err == nil {
		t.Fatal("access token parsed as a step-one token")
	}
}
