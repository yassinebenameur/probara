package auth

import (
	"testing"
	"time"
)

func TestAdminIDFromSignedToken(t *testing.T) {
	fresh, err := CreateAdminToken("u1", "secret", time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	expired, err := CreateAdminToken("u2", "secret", -time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if id, ok := AdminIDFromSignedToken(fresh, "secret"); !ok || id != "u1" {
		t.Fatalf("fresh token: %q %v", id, ok)
	}
	if _, err := ParseAdminToken(expired, "secret"); err == nil {
		t.Fatal("expired token must not authenticate")
	}
	if id, ok := AdminIDFromSignedToken(expired, "secret"); !ok || id != "u2" {
		t.Fatalf("expired but authentic token: %q %v", id, ok)
	}
	for _, bad := range []struct{ token, secret string }{{fresh, "other"}, {"junk", "secret"}, {"", "secret"}, {fresh, ""}} {
		if _, ok := AdminIDFromSignedToken(bad.token, bad.secret); ok {
			t.Fatalf("accepted %q with secret %q", bad.token, bad.secret)
		}
	}
}
