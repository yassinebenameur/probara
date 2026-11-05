package models

import (
	"encoding/json"
	"testing"
)

func TestDomainMonitorConfigValidate(t *testing.T) {
	days := func(n int) *int { return &n }
	tests := []struct {
		name    string
		cfg     DomainMonitorConfig
		wantErr bool
	}{
		{"plain", DomainMonitorConfig{Domain: "example.com"}, false},
		{"trailing dot and case", DomainMonitorConfig{Domain: " Example.COM. "}, false},
		{"second-level registry", DomainMonitorConfig{Domain: "example.co.uk", MinDaysValid: days(30)}, false},
		{"unicode", DomainMonitorConfig{Domain: "bücher.de"}, false},
		{"override", DomainMonitorConfig{Domain: "example.com", RDAPServerOverride: "https://rdap.example.net/v1/"}, false},
		{"empty", DomainMonitorConfig{}, true},
		{"single label", DomainMonitorConfig{Domain: "localhost"}, true},
		{"scheme", DomainMonitorConfig{Domain: "https://example.com"}, true},
		{"path", DomainMonitorConfig{Domain: "example.com/x"}, true},
		{"port", DomainMonitorConfig{Domain: "example.com:443"}, true},
		{"ipv4", DomainMonitorConfig{Domain: "192.0.2.1"}, true},
		{"numeric tld", DomainMonitorConfig{Domain: "example.123"}, true},
		{"negative days", DomainMonitorConfig{Domain: "example.com", MinDaysValid: days(-1)}, true},
		{"days over a term", DomainMonitorConfig{Domain: "example.com", MinDaysValid: days(DomainMaxMinDaysValid + 1)}, true},
		{"http override", DomainMonitorConfig{Domain: "example.com", RDAPServerOverride: "http://rdap.example.net/"}, true},
		{"override with query", DomainMonitorConfig{Domain: "example.com", RDAPServerOverride: "https://rdap.example.net/?x=1"}, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if err := tt.cfg.Validate(); (err != nil) != tt.wantErr {
				t.Fatalf("Validate() = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

func TestDomainLookupName(t *testing.T) {
	got, ok := DomainMonitorConfig{Domain: " Bücher.DE. "}.LookupName()
	if !ok || got != "xn--bcher-kva.de" {
		t.Fatalf("LookupName = %q %v", got, ok)
	}
}

func TestDomainWHOISFallbackDefault(t *testing.T) {
	off := false
	if !(DomainMonitorConfig{}).WHOISFallbackEnabled() || (DomainMonitorConfig{WHOISFallback: &off}).WHOISFallbackEnabled() {
		t.Fatal("whois_fallback: absent means on, false means off")
	}
}

func TestValidateDomainSchedule(t *testing.T) {
	raw := json.RawMessage(`{"domain":"example.com"}`)
	if err := ValidateDomainSchedule(raw, DomainMinIntervalSeconds-1, 30); err == nil {
		t.Fatal("an interval under one hour must be refused")
	}
	if err := ValidateDomainSchedule(raw, DomainMinIntervalSeconds, 30); err != nil {
		t.Fatalf("one hour: %v", err)
	}
}
