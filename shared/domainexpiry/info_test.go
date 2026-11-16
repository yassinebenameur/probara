package domainexpiry

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

// pinFixtures are metrics_data documents both readers must agree on
// (TestSQLExprMatchesExtract runs them through Postgres).
var pinFixtures = []struct {
	name string
	raw  string
}{
	{"known expiry", `{"domain":{"domain":"example.com","expires_at":"2027-08-13T04:00:00Z","days_until_expiry":270,"registrar":"R","registered":true,"source":"rdap"}}`},
	{"unknown", `{"domain":{"domain":"example.de","registered":true,"source":"rdap","unknown_reason":"the registry publishes no expiration date"}}`},
	{"not registered", `{"domain":{"domain":"example.com","registered":false,"source":"rdap"}}`},
	{"empty expiry", `{"domain":{"domain":"example.com","expires_at":"","unknown_reason":""}}`},
	{"domain is null", `{"domain":null}`},
	{"domain is a string", `{"domain":"example.com"}`},
	{"other envelope", `{"http":{"tls":{"not_after":"2027-01-01T00:00:00Z"}}}`},
	{"empty object", `{}`},
	{"top-level array", `[]`},
}

func TestExtract(t *testing.T) {
	info := Extract(json.RawMessage(pinFixtures[0].raw))
	if info == nil || info.ExpiresAt != "2027-08-13T04:00:00Z" || !info.IsRegistered() || info.Registrar != "R" {
		t.Fatalf("known: %+v", info)
	}
	if info := Extract(json.RawMessage(pinFixtures[2].raw)); info == nil || !info.IsNotRegistered() {
		t.Fatalf("not registered: %+v", info)
	}
	for _, raw := range []string{`{"domain":null}`, `{"domain":"x"}`, `{}`, ``, `[]`, `{"domain":{"registered":"yes"}}`} {
		if info := Extract(json.RawMessage(raw)); info != nil {
			t.Errorf("Extract(%s) = %+v, want nil", raw, info)
		}
	}
}

func TestSQLExprShape(t *testing.T) {
	got := SQLExpr("cr.metrics_data")
	if !strings.Contains(got, "jsonb_typeof(cr.metrics_data #> '{domain}') = 'object'") {
		t.Fatalf("SQLExpr = %s", got)
	}
	if got := ExpiresAtSQL("o"); got != "NULLIF(o ->> 'expires_at', '')" {
		t.Fatalf("ExpiresAtSQL = %s", got)
	}
}

func TestDaysUntilRoundsDown(t *testing.T) {
	now := time.Date(2026, 11, 15, 12, 0, 0, 0, time.UTC)
	for _, tt := range []struct {
		at   time.Time
		want int
	}{
		{now.Add(30*24*time.Hour - time.Minute), 29},
		{now.Add(30 * 24 * time.Hour), 30},
		{now.Add(time.Minute), 0},
		{now.Add(-time.Minute), -1},
		{now.Add(-3 * 24 * time.Hour), -3},
	} {
		if got := DaysUntil(tt.at, now); got != tt.want {
			t.Errorf("DaysUntil(%v) = %d, want %d", tt.at.Sub(now), got, tt.want)
		}
	}
}
