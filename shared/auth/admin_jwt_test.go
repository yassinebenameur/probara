package auth

import (
	"testing"
	"time"
)

func TestAdminTokenCarriesSessionID(t *testing.T) {
	token, err := CreateAdminToken("admin-1", "session-1", "secret", time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	claims, err := ParseAdminToken(token, "secret")
	if err != nil {
		t.Fatal(err)
	}
	if claims.AdminID != "admin-1" || claims.SessionID != "session-1" {
		t.Fatalf("claims = %q/%q, want admin-1/session-1", claims.AdminID, claims.SessionID)
	}
}

func TestAdminTokenWithoutSessionIDOmitsTheClaim(t *testing.T) {
	token, err := CreateAdminToken("admin-1", "", "secret", time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	claims, err := ParseAdminToken(token, "secret")
	if err != nil {
		t.Fatal(err)
	}
	if claims.SessionID != "" {
		t.Fatalf("SessionID = %q, want empty", claims.SessionID)
	}
}
