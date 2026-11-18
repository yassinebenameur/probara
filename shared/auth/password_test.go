package auth

import (
	"errors"
	"strings"
	"testing"
)

func TestValidatePasswordCountsBytesAgainstBcrypt(t *testing.T) {
	cases := []struct {
		name     string
		password string
		ok       bool
	}{
		{"empty", "", false},
		{"eleven", strings.Repeat("a", 11), false},
		{"twelve", strings.Repeat("a", 12), true},
		{"72 bytes", strings.Repeat("a", 72), true},
		{"73 bytes", strings.Repeat("a", 73), false},
		// 24 three-byte characters are 72 bytes; 25 are 75.
		{"24 CJK", strings.Repeat("密", 24), true},
		{"25 CJK", strings.Repeat("密", 25), false},
		// Four CJK characters are 12 bytes but only 4 characters.
		{"4 CJK", strings.Repeat("密", 4), false},
	}
	for _, tc := range cases {
		err := ValidatePassword(tc.password)
		if (err == nil) != tc.ok {
			t.Errorf("%s: err = %v, want ok=%v", tc.name, err, tc.ok)
		}
	}
	if !errors.Is(ValidatePassword(strings.Repeat("a", 73)), ErrPasswordTooLong) {
		t.Fatal("too long is not ErrPasswordTooLong")
	}
}

func TestHashPasswordRefusesWhatBcryptCannotHash(t *testing.T) {
	if _, err := HashPassword(strings.Repeat("密", 30), 4); !errors.Is(err, ErrPasswordTooLong) {
		t.Fatalf("90-byte password: %v, want ErrPasswordTooLong", err)
	}
	if _, err := HashPassword(strings.Repeat("a", 72), 4); err != nil {
		t.Fatalf("72-byte password: %v", err)
	}
}
