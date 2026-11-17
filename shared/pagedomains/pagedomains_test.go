package pagedomains

import (
	"errors"
	"strings"
	"testing"
)

func TestNormalizeHostname(t *testing.T) {
	long := strings.Repeat("a", 60) + "."
	ok := []struct{ in, want string }{
		{"status.example.com", "status.example.com"},
		{"  Status.Example.COM. ", "status.example.com"},
		{"status.bücher.de", "status.xn--bcher-kva.de"},
		{"STATUS.BÜCHER.DE", "status.xn--bcher-kva.de"},
		{"example.com", "example.com"},
		{"a-b.status.example.co.uk", "a-b.status.example.co.uk"},
		{"me.github.io", "me.github.io"},
	}
	for _, c := range ok {
		got, err := NormalizeHostname(c.in)
		if err != nil || got != c.want {
			t.Errorf("NormalizeHostname(%q) = %q, %v; want %q", c.in, got, err, c.want)
		}
	}

	bad := []struct {
		in   string
		want error
	}{
		{"", ErrRequired},
		{"   ", ErrRequired},
		{"*.example.com", ErrWildcard},
		{"status.*.example.com", ErrWildcard},
		{"192.0.2.10", ErrIP},
		{"[2001:db8::1]", ErrIP},
		{"2001:db8::1", ErrIP},
		{"10.1", ErrIP},
		{"https://status.example.com", ErrNotBare},
		{"status.example.com:443", ErrNotBare},
		{"status.example.com/page", ErrNotBare},
		{"user@status.example.com", ErrNotBare},
		{"status example.com", ErrNotBare},
		{"-bad.example.com", ErrInvalid},
		{"bad_.example.com", ErrInvalid},
		{"a..example.com", ErrInvalid},
		{strings.Repeat(long, 5) + "example.com", ErrTooLong},
		{"localhost", ErrSuffix},
		{"status.local", ErrTLD},
		{"status.internal", ErrTLD},
		{"com", ErrSuffix},
		{"co.uk", ErrSuffix},
		{"github.io", ErrSuffix},
	}
	for _, c := range bad {
		got, err := NormalizeHostname(c.in)
		if !errors.Is(err, c.want) {
			t.Errorf("NormalizeHostname(%q) = %q, %v; want %v", c.in, got, err, c.want)
		}
	}
}

func TestHostFromHeader(t *testing.T) {
	cases := map[string]string{
		"status.example.com":           "status.example.com",
		"Status.Example.com:443":       "status.example.com",
		"status.example.com.":          "status.example.com",
		"localhost:8082":               "localhost",
		"[::1]:8082":                   "",
		"":                             "",
		"evil.com/x":                   "",
		"a b":                          "",
		strings.Repeat("a.", 130) + "": "",
	}
	for in, want := range cases {
		if got := HostFromHeader(in); got != want {
			t.Errorf("HostFromHeader(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestHostOfURL(t *testing.T) {
	cases := map[string]string{
		"https://probara.example.com":            "probara.example.com",
		"https://Probara.example.com:8443/x?y#z": "probara.example.com",
		"wss://user:pw@nats.example.com/ws":      "nats.example.com",
		"status.example.com":                     "status.example.com",
		"status.example.com.":                    "status.example.com",
		"":                                       "",
	}
	for in, want := range cases {
		if got := HostOfURL(in); got != want {
			t.Errorf("HostOfURL(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestRecordNamesAndURL(t *testing.T) {
	if got := VerificationRecordName("status.example.com"); got != "_probara-verify.status.example.com" {
		t.Errorf("VerificationRecordName = %q", got)
	}
	if got := PageURL("status.example.com"); got != "https://status.example.com/" {
		t.Errorf("PageURL = %q", got)
	}
	if sql := PrimaryHostSQL("sp.id"); !strings.Contains(sql, "primary_domain.status_page_id = sp.id") || !strings.Contains(sql, "'verified'") {
		t.Errorf("PrimaryHostSQL = %q", sql)
	}
}
