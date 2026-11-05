package adminauth

import (
	"encoding/base32"
	"net/url"
	"strings"
	"testing"
	"time"
)

// RFC 6238 appendix B, SHA-1 column (8 digits).
func TestHOTPMatchesRFC6238Vectors(t *testing.T) {
	key := []byte("12345678901234567890")
	for _, tc := range []struct {
		unix int64
		want string
	}{
		{59, "94287082"},
		{1111111109, "07081804"},
		{1111111111, "14050471"},
		{1234567890, "89005924"},
		{2000000000, "69279037"},
		{20000000000, "65353130"},
	} {
		got := hotp(key, uint64(totpStep(time.Unix(tc.unix, 0))), 8)
		if got != tc.want {
			t.Errorf("T=%d: got %s, want %s", tc.unix, got, tc.want)
		}
	}
}

func testSecret() string {
	return base32.StdEncoding.WithPadding(base32.NoPadding).EncodeToString([]byte("12345678901234567890"))
}

func TestMatchTOTPWindowAndReplay(t *testing.T) {
	secret := testSecret()
	key, _ := decodeTOTPSecret(secret)
	now := time.Unix(1234567890, 0)
	step := totpStep(now)
	code := func(s int64) string { return hotp(key, uint64(s), totpDigits) }

	if got, ok := matchTOTP(secret, code(step), now, -1); !ok || got != step {
		t.Fatalf("current step: got %d %v", got, ok)
	}
	// One step of drift either way is accepted.
	if got, ok := matchTOTP(secret, code(step-1), now, -1); !ok || got != step-1 {
		t.Fatalf("previous step: got %d %v", got, ok)
	}
	if got, ok := matchTOTP(secret, code(step+1), now, -1); !ok || got != step+1 {
		t.Fatalf("next step: got %d %v", got, ok)
	}
	if _, ok := matchTOTP(secret, code(step-2), now, -1); ok {
		t.Fatal("two steps old accepted")
	}
	// Replay: a code for a step at or before the last accepted one fails.
	if _, ok := matchTOTP(secret, code(step), now, step); ok {
		t.Fatal("replayed code accepted")
	}
	if _, ok := matchTOTP(secret, code(step-1), now, step); ok {
		t.Fatal("older code accepted after a newer one")
	}
	if got, ok := matchTOTP(secret, code(step+1), now, step); !ok || got != step+1 {
		t.Fatalf("later step after last: got %d %v", got, ok)
	}
	// Spaces inside a code are tolerated; anything else is refused.
	c := code(step)
	if _, ok := matchTOTP(secret, c[:3]+" "+c[3:], now, -1); !ok {
		t.Fatal("spaced code refused")
	}
	for _, bad := range []string{"", "12345", "1234567", "abcdef", c + "0"} {
		if _, ok := matchTOTP(secret, bad, now, -1); ok {
			t.Fatalf("malformed code %q accepted", bad)
		}
	}
	// Secrets are accepted lowercase and padded, as typed from an app.
	if _, ok := matchTOTP(strings.ToLower(secret)+"====", c, now, -1); !ok {
		t.Fatal("lowercase padded secret refused")
	}
}

func TestNewTOTPSecretAndURI(t *testing.T) {
	secret, err := newTOTPSecret()
	if err != nil {
		t.Fatal(err)
	}
	key, err := decodeTOTPSecret(secret)
	if err != nil || len(key) != totpSecretBytes {
		t.Fatalf("secret %q decodes to %d bytes (%v)", secret, len(key), err)
	}
	uri := TOTPURI("ops user", secret)
	u, err := url.Parse(uri)
	if err != nil {
		t.Fatal(err)
	}
	if u.Scheme != "otpauth" || u.Host != "totp" || u.Path != "/Probara:ops user" {
		t.Fatalf("uri = %s", uri)
	}
	q := u.Query()
	if q.Get("secret") != secret || q.Get("issuer") != "Probara" || q.Get("digits") != "6" || q.Get("period") != "30" {
		t.Fatalf("query = %v", q)
	}
}

func TestRecoveryCodes(t *testing.T) {
	seen := map[string]bool{}
	for i := 0; i < 50; i++ {
		code, err := newRecoveryCode()
		if err != nil {
			t.Fatal(err)
		}
		if len(code) != 11 || code[5] != '-' {
			t.Fatalf("code %q has the wrong shape", code)
		}
		if seen[code] {
			t.Fatalf("duplicate code %q", code)
		}
		seen[code] = true
		if hashRecoveryCode(code) != hashRecoveryCode(strings.ToUpper(strings.Replace(code, "-", " ", 1))) {
			t.Fatalf("code %q does not normalize", code)
		}
	}
}
