package domainexpiry

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// The fixtures in testdata are recorded registry answers, trimmed to the
// fields the parsers read; every name except IANA's reserved example.com is
// replaced with an example name.

var parseNow = time.Date(2026, 11, 15, 12, 0, 0, 0, time.UTC)

func fixture(t *testing.T, name string) []byte {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func TestParseRDAPDomainFixtures(t *testing.T) {
	tests := []struct {
		file, name                                    string
		expiresAt, registeredAt, updatedAt, registrar string
		days                                          int
		statuses                                      int
	}{
		{
			file: "rdap-example-com.json", name: "example.com",
			expiresAt: "2027-08-13T04:00:00Z", registeredAt: "1995-08-14T04:00:00Z", updatedAt: "2026-08-14T08:01:43Z",
			registrar: "RESERVED-Internet Assigned Numbers Authority", days: 270, statuses: 3,
		},
		{
			// Fractional seconds, and a registrar entity that also holds
			// the sponsor role next to registrant and technical entities.
			file: "rdap-example-fr.json", name: "example.fr",
			expiresAt: "2029-07-18T08:26:59Z", registeredAt: "2001-12-10T23:00:00Z", updatedAt: "2026-06-05T06:59:15Z",
			registrar: "Registry Operations", days: 975, statuses: 1,
		},
	}
	for _, tt := range tests {
		t.Run(tt.file, func(t *testing.T) {
			info, err := parseRDAPDomain(fixture(t, tt.file), tt.name, parseNow)
			if err != nil {
				t.Fatal(err)
			}
			if !info.IsRegistered() || info.Source != SourceRDAP || info.UnknownReason != "" {
				t.Fatalf("info = %+v", info)
			}
			if info.ExpiresAt != tt.expiresAt || info.RegisteredAt != tt.registeredAt || info.UpdatedAt != tt.updatedAt {
				t.Errorf("dates = %q %q %q", info.ExpiresAt, info.RegisteredAt, info.UpdatedAt)
			}
			if info.DaysUntilExpiry == nil || *info.DaysUntilExpiry != tt.days {
				t.Errorf("days = %v, want %d", info.DaysUntilExpiry, tt.days)
			}
			if info.Registrar != tt.registrar || len(info.Statuses) != tt.statuses {
				t.Errorf("registrar = %q, statuses = %v", info.Registrar, info.Statuses)
			}
		})
	}
}

func TestParseRDAPDomainWithoutExpiry(t *testing.T) {
	info, err := parseRDAPDomain(fixture(t, "rdap-no-expiry.json"), "example.org.uk", parseNow)
	if err != nil {
		t.Fatal(err)
	}
	if !info.IsRegistered() || info.ExpiresAt != "" || info.DaysUntilExpiry != nil || info.Registrar != "Nominet UK" {
		t.Fatalf("info = %+v", info)
	}
}

func TestParseRDAPDomainEdgeCases(t *testing.T) {
	// The registrar is found by role, nested a level down, whatever order
	// the entities come in.
	nested := `{"objectClassName":"domain","events":[{"eventAction":"expiration","eventDate":"2027-01-01T00:00:00Z"}],
		"entities":[{"roles":["registrant"],"vcardArray":["vcard",[["fn",{},"text","Holder"]]],
			"entities":[{"roles":["registrar"],"vcardArray":["vcard",[["fn",{},"text","Nested Registrar"]]]}]}]}`
	info, err := parseRDAPDomain([]byte(nested), "example.com", parseNow)
	if err != nil || info.Registrar != "Nested Registrar" || info.ExpiresAt != "2027-01-01T00:00:00Z" {
		t.Fatalf("nested: %+v, %v", info, err)
	}

	// An RDAP error object with code 404 is "not registered".
	info, err = parseRDAPDomain([]byte(`{"errorCode":404,"title":"Not Found"}`), "example.com", parseNow)
	if err != nil || !info.IsNotRegistered() {
		t.Fatalf("404 body: %+v, %v", info, err)
	}

	// An unparseable expiry date is ignored, not fatal.
	info, err = parseRDAPDomain([]byte(`{"objectClassName":"domain","events":[{"eventAction":"expiration","eventDate":"soon"}]}`), "example.com", parseNow)
	if err != nil || info.ExpiresAt != "" || !info.IsRegistered() {
		t.Fatalf("bad date: %+v, %v", info, err)
	}

	// Already expired: negative days.
	info, _ = parseRDAPDomain([]byte(`{"objectClassName":"domain","events":[{"eventAction":"expiration","eventDate":"2026-11-12T12:00:00Z"}]}`), "example.com", parseNow)
	if info.DaysUntilExpiry == nil || *info.DaysUntilExpiry != -3 {
		t.Fatalf("expired days = %v", info.DaysUntilExpiry)
	}

	for name, body := range map[string]string{
		"not json":     `<html>`,
		"other object": `{"objectClassName":"entity"}`,
		"other error":  `{"errorCode":500}`,
	} {
		if _, err := parseRDAPDomain([]byte(body), "example.com", parseNow); err == nil {
			t.Errorf("%s: want an error", name)
		}
	}
}

func TestParseWHOISFixtures(t *testing.T) {
	tests := []struct {
		file, name, expiresAt, registrar string
		status                           string
	}{
		{file: "whois-gtld-example-com.txt", name: "example.com", expiresAt: "2027-08-13T04:00:00Z", registrar: "RESERVED-Internet Assigned Numbers Authority", status: "clientDeleteProhibited"},
		{file: "whois-fr.txt", name: "example.fr", expiresAt: "2029-07-18T08:26:59Z", registrar: "Example Registrar SAS", status: "ACTIVE"},
		{file: "whois-jp.txt", name: "example.jp", expiresAt: "2027-02-28T00:00:00Z", status: "Active"},
		{file: "whois-se.txt", name: "example.se", expiresAt: "2031-03-09T00:00:00Z", registrar: "Example Registrar AB", status: "active"},
		{file: "whois-ru.txt", name: "example.ru", expiresAt: "2027-07-31T21:00:00Z", registrar: "EXAMPLE-RU", status: "REGISTERED"},
		{file: "whois-uk.txt", name: "example.co.uk", expiresAt: "2027-06-10T00:00:00Z", registrar: "Example Registrar Ltd t/a Example"},
	}
	for _, tt := range tests {
		t.Run(tt.file, func(t *testing.T) {
			info, err := parseWHOIS(string(fixture(t, tt.file)), tt.name, parseNow)
			if err != nil {
				t.Fatal(err)
			}
			if !info.IsRegistered() || info.Source != SourceWHOIS {
				t.Fatalf("info = %+v", info)
			}
			if info.ExpiresAt != tt.expiresAt || info.Registrar != tt.registrar {
				t.Errorf("expires %q registrar %q", info.ExpiresAt, info.Registrar)
			}
			if tt.status != "" && (len(info.Statuses) == 0 || info.Statuses[0] != tt.status) {
				t.Errorf("statuses = %v", info.Statuses)
			}
		})
	}
}

func TestParseWHOISRegisteredWithoutExpiry(t *testing.T) {
	info, err := parseWHOIS(string(fixture(t, "whois-uk-no-expiry.txt")), "example.uk", parseNow)
	if err != nil {
		t.Fatal(err)
	}
	if !info.IsRegistered() || info.ExpiresAt != "" || info.Registrar != "" || info.RegisteredAt != "2014-06-10T00:00:00Z" {
		t.Fatalf("info = %+v", info)
	}
}

func TestParseWHOISNotFoundAndRefusals(t *testing.T) {
	info, err := parseWHOIS(string(fixture(t, "whois-gtld-not-found.txt")), "no-such-name-example.com", parseNow)
	if err != nil || !info.IsNotRegistered() {
		t.Fatalf("not found: %+v, %v", info, err)
	}
	// A refusal is never read as "not registered", even next to a
	// not-found phrase.
	if _, err := parseWHOIS("Query rate limit exceeded. Not found.", "example.com", parseNow); err == nil {
		t.Fatal("refusal: want an error")
	}
	if _, err := parseWHOIS("Welcome to the server.\nHave a nice day.", "example.com", parseNow); err == nil {
		t.Fatal("unrecognized: want an error")
	}
}

func TestParseWHOISDateFormats(t *testing.T) {
	want := func(s string) time.Time {
		at, err := time.Parse(time.RFC3339, s)
		if err != nil {
			t.Fatal(err)
		}
		return at
	}
	tests := []struct {
		in   string
		want time.Time
	}{
		{"2027-08-13T04:00:00Z", want("2027-08-13T04:00:00Z")},
		{"2027-08-13T04:00:00.123456Z", want("2027-08-13T04:00:00.123456Z")},
		{"2027-08-13T04:00:00+0200", want("2027-08-13T02:00:00Z")},
		{"2027-08-13T04:00:00", want("2027-08-13T04:00:00Z")},
		{"2027-01-01 00:00:00", want("2027-01-01T00:00:00Z")},
		{"2027-01-01 08:00:00+08:00", want("2027-01-01T00:00:00Z")},
		{"2027-01-01 00:00:00 UTC", want("2027-01-01T00:00:00Z")},
		{"2031-03-09", want("2031-03-09T00:00:00Z")},
		{"2027/02/28", want("2027-02-28T00:00:00Z")},
		{"2026/03/01 01:05:03 (JST)", want("2026-03-01T01:05:03Z")},
		{"2027.02.28", want("2027-02-28T00:00:00Z")},
		{"10-Jun-2027", want("2027-06-10T00:00:00Z")},
		{"10-June-2027", want("2027-06-10T00:00:00Z")},
		{"10 June 2027", want("2027-06-10T00:00:00Z")},
		{"28.02.2027", want("2027-02-28T00:00:00Z")},
		{"20270101", want("2027-01-01T00:00:00Z")},
		{"2027-08-13 (YYYY-MM-DD)", want("2027-08-13T00:00:00Z")},
	}
	for _, tt := range tests {
		got, ok := parseWHOISDate(tt.in)
		if !ok || !got.Equal(tt.want) {
			t.Errorf("parseWHOISDate(%q) = %v, %v; want %v", tt.in, got, ok, tt.want)
		}
	}
	// Ambiguous day/month order is refused rather than guessed.
	for _, in := range []string{"03/04/2027", "soon", ""} {
		if _, ok := parseWHOISDate(in); ok {
			t.Errorf("parseWHOISDate(%q) parsed, want refused", in)
		}
	}
}

func TestWHOISReferral(t *testing.T) {
	if got := whoisReferral(string(fixture(t, "whois-iana-com.txt")), "whois", "refer"); got != "whois.verisign-grs.com" {
		t.Fatalf("IANA referral = %q", got)
	}
	tests := map[string]string{
		"refer: whois.nic.example\n":                 "whois.nic.example",
		"Registrar WHOIS Server: WHOIS.Reg.Example.": "whois.reg.example",
		"whois: whois://whois.nic.example\n":         "whois.nic.example",
		// Never a literal address, a port, a single label or junk: the
		// referral comes from a remote server.
		"whois: 127.0.0.1\n":            "",
		"whois: [::1]\n":                "",
		"whois: 10.0.0.1:43\n":          "",
		"whois: whois.nic.example:4343": "",
		"whois: localhost\n":            "",
		"whois: http://evil.example/\n": "",
		"whois: \n":                     "",
	}
	for text, want := range tests {
		if got := whoisReferral(text, "whois", "refer", "registrar whois server"); got != want {
			t.Errorf("whoisReferral(%q) = %q, want %q", strings.TrimSpace(text), got, want)
		}
	}
}
