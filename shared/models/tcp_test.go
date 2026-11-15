package models

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestTCPMonitorConfigValidate(t *testing.T) {
	valid := []string{
		`{"host":"db.internal","port":5432}`,
		`{"host":"db.internal","port":5432,"tls_skip_verify":true}`, // saved before the TLS rules: kept
		`{"host":"imap.example.com","port":993,"use_tls":true,"tls_min_days_valid":14}`,
		`{"host":"10.0.0.5","port":993,"use_tls":true,"tls_server_name":"imap.example.com"}`,
		`{"host":"10.0.0.5","port":993,"use_tls":true,"tls_server_name":"imap.example.com."}`,
		`{"host":"imap.example.com","port":993,"use_tls":true,"tls_server_name":"10.0.0.5","tls_min_days_valid":0}`,
	}
	for _, raw := range valid {
		var c TCPMonitorConfig
		if err := json.Unmarshal([]byte(raw), &c); err != nil {
			t.Fatal(err)
		}
		if err := c.Validate(); err != nil {
			t.Errorf("%s: Validate() = %v, want nil", raw, err)
		}
	}
	invalid := map[string]string{
		`{"host":"","port":1}`:                                                   "host is required",
		`{"host":"a.example","port":0}`:                                          "port must be between",
		`{"host":"a.example","port":993,"tls_min_days_valid":14}`:                "tls_min_days_valid requires use_tls",
		`{"host":"a.example","port":993,"use_tls":true,"tls_min_days_valid":-1}`: "tls_min_days_valid must be 0 or greater",
		`{"host":"a.example","port":993,"tls_server_name":"b.example"}`:          "tls_server_name requires use_tls",
		`{"host":"a.example","port":993,"use_tls":true,"tls_server_name":"  "}`:  "tls_server_name cannot be empty",
		`{"host":"a.example","port":993,"use_tls":true,"tls_server_name":"a b"}`: "tls_server_name must be a valid hostname or IP address",
	}
	for raw, want := range invalid {
		var c TCPMonitorConfig
		if err := json.Unmarshal([]byte(raw), &c); err != nil {
			t.Fatal(err)
		}
		if err := c.Validate(); err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%s: Validate() = %v, want %q", raw, err, want)
		}
	}
}

func TestTCPMonitorConfigServerName(t *testing.T) {
	name := " imap.example.com "
	if got := (TCPMonitorConfig{Host: "10.0.0.5", TLSServerName: &name}).ServerName(); got != "imap.example.com" {
		t.Fatalf("ServerName = %q", got)
	}
	if got := (TCPMonitorConfig{Host: " 10.0.0.5 "}).ServerName(); got != "10.0.0.5" {
		t.Fatalf("ServerName default = %q, want the host", got)
	}
}
