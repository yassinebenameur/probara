package auth

import (
	"encoding/base64"
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestAdminSessionTokenCarriesSessionIDNotRoles(t *testing.T) {
	const secret = "test-secret-test-secret-test-secret"
	token, err := CreateAdminSessionToken("admin-1", "family-1", secret, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	claims, err := ParseAdminToken(token, secret)
	if err != nil {
		t.Fatal(err)
	}
	if claims.AdminID != "admin-1" || claims.SessionID != "family-1" {
		t.Fatalf("claims = %+v, want admin-1 / family-1", claims)
	}

	// Roles are resolved per request from the database, never carried in
	// the token: the payload holds identity and the session family only.
	payload, err := base64.RawURLEncoding.DecodeString(strings.Split(token, ".")[1])
	if err != nil {
		t.Fatal(err)
	}
	var raw map[string]any
	if err := json.Unmarshal(payload, &raw); err != nil {
		t.Fatal(err)
	}
	for key := range raw {
		switch key {
		case "admin_id", "sid", "sub", "iat", "exp", "iss", "aud":
		default:
			t.Errorf("unexpected claim %q in admin token", key)
		}
	}
}

func TestAdminTokenWithoutSessionOmitsSid(t *testing.T) {
	const secret = "test-secret-test-secret-test-secret"
	token, err := CreateAdminToken("admin-1", secret, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	claims, err := ParseAdminToken(token, secret)
	if err != nil {
		t.Fatal(err)
	}
	if claims.SessionID != "" {
		t.Fatalf("SessionID = %q, want empty", claims.SessionID)
	}
}
