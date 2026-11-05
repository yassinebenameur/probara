package models

import (
	"strings"
	"testing"
)

func TestTCPMonitorConfigValidate(t *testing.T) {
	yes, no := true, false
	days := func(v int) *int { return &v }
	str := func(v string) *string { return &v }
	tests := []struct {
		name    string
		cfg     TCPMonitorConfig
		wantErr string
	}{
		{"plain connect", TCPMonitorConfig{Host: "db.example.com", Port: 5432}, ""},
		{"tls with expiry and chain", TCPMonitorConfig{Host: "mail.example.com", Port: 465, UseTLS: &yes,
			TLSMinDaysValid: days(14), TLSCheckChain: &yes, TLSServerName: str("smtp.example.com")}, ""},
		{"missing host", TCPMonitorConfig{Port: 1}, "host is required"},
		{"bad port", TCPMonitorConfig{Host: "h", Port: 0}, "port must be between"},
		{"days without tls", TCPMonitorConfig{Host: "h", Port: 1, TLSMinDaysValid: days(14)}, "tls_min_days_valid requires use_tls"},
		{"days with tls off", TCPMonitorConfig{Host: "h", Port: 1, UseTLS: &no, TLSMinDaysValid: days(14)}, "requires use_tls"},
		{"negative days", TCPMonitorConfig{Host: "h", Port: 1, UseTLS: &yes, TLSMinDaysValid: days(-1)}, "0 or greater"},
		{"chain without tls", TCPMonitorConfig{Host: "h", Port: 1, TLSCheckChain: &yes}, "tls_check_chain requires use_tls"},
		{"chain off without tls", TCPMonitorConfig{Host: "h", Port: 1, TLSCheckChain: &no}, ""},
		{"empty server name", TCPMonitorConfig{Host: "h", Port: 1, UseTLS: &yes, TLSServerName: str(" ")}, "cannot be empty"},
		{"bad server name", TCPMonitorConfig{Host: "h", Port: 1, UseTLS: &yes, TLSServerName: str("a b")}, "valid hostname"},
		{"bad ca pem", TCPMonitorConfig{Host: "h", Port: 1, UseTLS: &yes, TLSCAPem: str("nope")}, "valid PEM"},
		{"ca pem without tls", TCPMonitorConfig{Host: "h", Port: 1, TLSCAPem: str("nope")}, "requires use_tls"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.cfg.Validate()
			if tt.wantErr == "" {
				if err != nil {
					t.Fatalf("Validate() = %v, want nil", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("Validate() = %v, want error containing %q", err, tt.wantErr)
			}
		})
	}
}
