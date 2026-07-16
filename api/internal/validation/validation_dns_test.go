package validation

import (
	"testing"
)

func TestDNSConfigValidator_Nameserver(t *testing.T) {
	runConfigValidatorCases(t, &DNSConfigValidator{}, []struct {
		name    string
		config  string
		wantErr bool
	}{
		{"no nameserver", `{"host":"example.com"}`, false},
		{"nameserver ip", `{"host":"example.com","nameserver":"10.0.0.2"}`, false},
		{"nameserver ip with port", `{"host":"example.com","nameserver":"10.0.0.2:5353"}`, false},
		{"nameserver hostname", `{"host":"example.com","nameserver":"resolver.internal"}`, false},
		{"nameserver ipv6 with port", `{"host":"example.com","nameserver":"[fd00::2]:53"}`, false},
		{"nameserver bad port", `{"host":"example.com","nameserver":"10.0.0.2:99999"}`, true},
		{"nameserver invalid host", `{"host":"example.com","nameserver":"not a host"}`, true},
	})
}
