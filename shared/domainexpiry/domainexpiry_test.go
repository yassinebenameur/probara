package domainexpiry

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

// pinFixtures are the metrics_data documents both readers must agree on
// (TestSQLExprMatchesExtract). want is the domain Extract should pick: ""
// means no snapshot.
var pinFixtures = []struct {
	name string
	raw  string
	want string
}{
	{name: "rdap snapshot", raw: `{"domain":{"domain":"example.com","expires_at":"2027-08-13T04:00:00Z","days_until_expiry":281,"registered":true,"source":"rdap"}}`, want: "example.com"},
	{name: "whois snapshot", raw: `{"domain":{"domain":"example.org","expires_at":"2026-12-01T00:00:00Z","days_until_expiry":26,"registered":true,"source":"whois"}}`, want: "example.org"},
	{name: "no expiry published", raw: `{"domain":{"domain":"example.de","registered":true,"source":"rdap","note":"registry publishes no expiration date"}}`, want: "example.de"},
	{name: "negative days", raw: `{"domain":{"domain":"lapsed.com","expires_at":"2026-10-01T00:00:00Z","days_until_expiry":-35,"registered":true,"source":"rdap"}}`, want: "lapsed.com"},
	{name: "json null is absent", raw: `{"domain":null}`},
	{name: "string is absent", raw: `{"domain":"example.com"}`},
	{name: "array is absent", raw: `{"domain":[{"domain":"example.com"}]}`},
	{name: "empty object counts", raw: `{"domain":{}}`, want: ""},
	{name: "unrelated envelope", raw: `{"http":{"tls":{"days_until_expiry":3}}}`},
	{name: "top-level array", raw: `[{"domain":{}}]`},
	{name: "top-level scalar", raw: `42`},
	{name: "top-level null", raw: `null`},
	{name: "empty object", raw: `{}`},
}

func TestExtract(t *testing.T) {
	for _, tt := range pinFixtures {
		t.Run(tt.name, func(t *testing.T) {
			info := Extract(json.RawMessage(tt.raw))
			if tt.want == "" {
				if info != nil && info.Domain != "" {
					t.Fatalf("Extract = %+v, want none", info)
				}
				return
			}
			if info == nil || info.Domain != tt.want {
				t.Fatalf("Extract = %+v, want domain %q", info, tt.want)
			}
		})
	}
	if Extract(nil) != nil || Extract(json.RawMessage("  ")) != nil {
		t.Fatal("empty input must extract nothing")
	}
}

func TestSQLExprShape(t *testing.T) {
	got := SQLExpr("cr.metrics_data")
	want := "COALESCE(CASE WHEN jsonb_typeof(cr.metrics_data #> '{domain}') = 'object' THEN cr.metrics_data #> '{domain}' END)"
	if got != want {
		t.Fatalf("SQLExpr = %s\nwant %s", got, want)
	}
	expires, days := ExpirySQL("d.obj")
	if !strings.Contains(expires, "'expires_at'") || !strings.Contains(days, "'days_until_expiry'") {
		t.Fatalf("ExpirySQL = %s, %s", expires, days)
	}
}

func TestSetExpiry(t *testing.T) {
	now := time.Date(2026, 11, 5, 12, 0, 0, 0, time.UTC)
	var info Info
	info.SetExpiry(now.Add(9*24*time.Hour+23*time.Hour), now)
	if info.ExpiresAt != "2026-11-15T11:00:00Z" || info.DaysUntilExpiry == nil || *info.DaysUntilExpiry != 9 {
		t.Fatalf("SetExpiry = %q %v", info.ExpiresAt, info.DaysUntilExpiry)
	}
	info.SetExpiry(now.Add(-36*time.Hour), now)
	if *info.DaysUntilExpiry != -1 {
		t.Fatalf("expired days = %d, want -1", *info.DaysUntilExpiry)
	}
}

// The stored keys are a contract with the web and the alerter.
func TestInfoJSONKeys(t *testing.T) {
	var info Info
	info.SetExpiry(time.Date(2027, 1, 1, 0, 0, 0, 0, time.UTC), time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC))
	info.Domain, info.Registrar, info.Source, info.Registered = "example.com", "Example Registrar", "rdap", true
	info.Statuses = []string{"active"}
	raw, _ := json.Marshal(info)
	for _, key := range []string{`"domain"`, `"expires_at"`, `"days_until_expiry"`, `"registrar"`, `"statuses"`, `"registered"`, `"source"`} {
		if !strings.Contains(string(raw), key) {
			t.Errorf("marshalled Info lacks %s: %s", key, raw)
		}
	}
}
