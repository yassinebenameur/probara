package contacts

import (
	"regexp"
	"testing"

	"github.com/google/uuid"
)

func TestNormalizeAddress(t *testing.T) {
	cases := []struct {
		kind    Kind
		in      string
		want    string
		wantErr bool
	}{
		{KindEmail, " Ops@Example.COM ", "ops@example.com", false},
		{KindEmail, "Ops <ops@example.com>", "", true},
		{KindEmail, "ops@example.com\r\nBcc: x@y.z", "", true},
		{KindEmail, "not-an-address", "", true},
		{KindSMS, "+33 6 12-34.56 (78)", "+33612345678", false},
		{KindSMS, "0612345678", "", true},
		{KindSMS, "+0612345678", "", true},
		{Kind("voice"), "+15557654321", "", true},
	}
	for _, tc := range cases {
		got, err := NormalizeAddress(tc.kind, tc.in)
		if (err != nil) != tc.wantErr || got != tc.want {
			t.Errorf("NormalizeAddress(%s, %q) = %q, %v; want %q, err=%v", tc.kind, tc.in, got, err, tc.want, tc.wantErr)
		}
	}
}

func TestMask(t *testing.T) {
	if got := Mask(KindEmail, "ops@example.com"); got != "o***@example.com" {
		t.Errorf("email mask = %q", got)
	}
	if got := Mask(KindSMS, "+15557654321"); got != "********4321" {
		t.Errorf("sms mask = %q", got)
	}
}

func TestCodes(t *testing.T) {
	digits := regexp.MustCompile(`^[0-9]{6}$`)
	id := uuid.New()
	for range 50 {
		code, err := NewCode()
		if err != nil || !digits.MatchString(code) {
			t.Fatalf("NewCode = %q, %v", code, err)
		}
		hash := HashCode(id, code)
		if !CodeMatches(id, code, hash) || !CodeMatches(id, " "+code+" ", hash) {
			t.Fatal("a code must match its own hash")
		}
		if CodeMatches(uuid.New(), code, hash) {
			t.Fatal("a hash is bound to its contact")
		}
	}
}
