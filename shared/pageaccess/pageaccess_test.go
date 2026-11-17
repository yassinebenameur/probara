package pageaccess

import (
	"strings"
	"testing"
)

func TestVisibilityPredicates(t *testing.T) {
	tests := []struct {
		v                                               string
		valid, gated, noIndex, listed, password, tokens bool
	}{
		{Public, true, false, false, true, false, false},
		{Unlisted, true, false, true, false, false, false},
		{Password, true, true, true, false, true, true},
		{Private, true, true, true, false, false, true},
		// An unknown value (a hand-edited row) fails closed.
		{"secret", false, true, true, false, false, false},
		{"", false, true, true, false, false, false},
	}
	for _, tt := range tests {
		t.Run(tt.v, func(t *testing.T) {
			got := []bool{Valid(tt.v), Gated(tt.v), NoIndex(tt.v), Listed(tt.v), AcceptsPassword(tt.v), AcceptsTokens(tt.v)}
			want := []bool{tt.valid, tt.gated, tt.noIndex, tt.listed, tt.password, tt.tokens}
			for i := range got {
				if got[i] != want[i] {
					t.Fatalf("predicate %d = %v, want %v", i, got[i], want[i])
				}
			}
		})
	}
}

func TestValidatePassword(t *testing.T) {
	if err := ValidatePassword("short"); err == nil {
		t.Fatal("short password accepted")
	}
	// Counted in characters, not bytes.
	if err := ValidatePassword("ééééééé"); err == nil {
		t.Fatal("7-character password accepted")
	}
	if err := ValidatePassword("éééééééé"); err != nil {
		t.Fatalf("8-character password refused: %v", err)
	}
	if err := ValidatePassword(strings.Repeat("a", MaxPasswordBytes+1)); err == nil {
		t.Fatal("password bcrypt would truncate accepted")
	}
}

func TestPasswordHashRoundTrip(t *testing.T) {
	hash, err := HashPassword("correct horse")
	if err != nil {
		t.Fatal(err)
	}
	if !CheckPassword(hash, "correct horse") {
		t.Fatal("right password refused")
	}
	for _, wrong := range []string{"", "correct hors", "Correct horse"} {
		if CheckPassword(hash, wrong) {
			t.Fatalf("wrong password %q accepted", wrong)
		}
	}
	if CheckPassword("", "correct horse") {
		t.Fatal("empty hash matched")
	}
}

func TestNewToken(t *testing.T) {
	a, hashA, err := NewToken()
	if err != nil {
		t.Fatal(err)
	}
	b, _, err := NewToken()
	if err != nil {
		t.Fatal(err)
	}
	if a == b {
		t.Fatal("two tokens are equal")
	}
	if !strings.HasPrefix(a, TokenPrefix) {
		t.Fatalf("token %q lacks prefix", a)
	}
	if hashA != HashToken(a) || len(hashA) != 64 {
		t.Fatalf("hash %q does not match HashToken", hashA)
	}
	if strings.Contains(hashA, a) {
		t.Fatal("hash contains the token")
	}
}
