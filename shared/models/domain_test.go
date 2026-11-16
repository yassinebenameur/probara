package models

import (
	"strings"
	"testing"
)

func TestDomainMonitorConfigValidate(t *testing.T) {
	days := func(n int) *int { return &n }
	tests := []struct {
		name    string
		cfg     DomainMonitorConfig
		wantErr string
	}{
		{name: "plain", cfg: DomainMonitorConfig{Domain: "example.com"}},
		{name: "subdomain", cfg: DomainMonitorConfig{Domain: "www.example.co.uk"}},
		{name: "idn", cfg: DomainMonitorConfig{Domain: "bücher.de", MinDaysValid: days(14)}},
		{name: "zero window", cfg: DomainMonitorConfig{Domain: "example.com", MinDaysValid: days(0)}},
		{name: "max window", cfg: DomainMonitorConfig{Domain: "example.com", MinDaysValid: days(DomainMaxMinDaysValid)}},
		{name: "empty", cfg: DomainMonitorConfig{}, wantErr: "domain is required"},
		{name: "url", cfg: DomainMonitorConfig{Domain: "https://example.com"}, wantErr: "bare domain name"},
		{name: "ip", cfg: DomainMonitorConfig{Domain: "192.0.2.1"}, wantErr: "not an IP address"},
		{name: "tld", cfg: DomainMonitorConfig{Domain: "co.uk"}, wantErr: "public suffix"},
		{name: "negative window", cfg: DomainMonitorConfig{Domain: "example.com", MinDaysValid: days(-1)}, wantErr: "min_days_valid"},
		{name: "huge window", cfg: DomainMonitorConfig{Domain: "example.com", MinDaysValid: days(DomainMaxMinDaysValid + 1)}, wantErr: "min_days_valid"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.cfg.Validate()
			if tt.wantErr == "" {
				if err != nil {
					t.Fatalf("Validate() = %v", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("Validate() = %v, want %q", err, tt.wantErr)
			}
		})
	}
}

func TestDomainMonitorConfigDefaults(t *testing.T) {
	cfg := DomainMonitorConfig{Domain: "www.example.com"}
	if got := cfg.EffectiveMinDaysValid(); got != DomainDefaultMinDaysValid {
		t.Fatalf("EffectiveMinDaysValid() = %d", got)
	}
	if name, err := cfg.LookupName(); err != nil || name != "example.com" {
		t.Fatalf("LookupName() = %q, %v", name, err)
	}
}

func TestValidateDomainSchedule(t *testing.T) {
	if err := ValidateDomainSchedule(nil, DomainMinIntervalSeconds, 30); err != nil {
		t.Fatalf("1h: %v", err)
	}
	if err := ValidateDomainSchedule(nil, DomainMinIntervalSeconds-1, 30); err == nil {
		t.Fatal("under 1h: want an error")
	}
}
