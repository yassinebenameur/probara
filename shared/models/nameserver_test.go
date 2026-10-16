package models

import (
	"strings"
	"testing"
)

func TestNormalizeNameserverAddr(t *testing.T) {
	cases := []struct {
		name       string
		nameserver string
		want       string
		wantErr    bool
	}{
		{"bare host gets port 53", "10.0.0.2", "10.0.0.2:53", false},
		{"host:port kept", "10.0.0.2:5353", "10.0.0.2:5353", false},
		{"hostname", "resolver.internal", "resolver.internal:53", false},
		{"bare ipv6 bracketed", "fd00::2", "[fd00::2]:53", false},
		{"ipv6 with port kept", "[fd00::2]:5353", "[fd00::2]:5353", false},
		{"surrounding space trimmed", " 10.0.0.2 ", "10.0.0.2:53", false},
		{"unbracketed ipv6ish garbage rejected", "a:b:c", "", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := NormalizeNameserverAddr(tc.nameserver)
			if (err != nil) != tc.wantErr {
				t.Fatalf("NormalizeNameserverAddr(%q) error = %v, wantErr %v", tc.nameserver, err, tc.wantErr)
			}
			if got != tc.want {
				t.Errorf("NormalizeNameserverAddr(%q) = %q, want %q", tc.nameserver, got, tc.want)
			}
		})
	}
}

func TestValidateNameserver(t *testing.T) {
	cases := []struct {
		nameserver string
		wantErr    string
	}{
		{"", ""},
		{"  ", ""},
		{"10.0.0.2", ""},
		{"10.0.0.2:5353", ""},
		{"resolver.internal", ""},
		{"fd00::2", ""},
		{"[fd00::2]:53", ""},
		{"10.0.0.2:99999", "nameserver port must be between 1 and 65535"},
		{"10.0.0.2:0", "nameserver port must be between 1 and 65535"},
		{"not a host", "nameserver must be a valid IP address or hostname"},
	}
	for _, tc := range cases {
		err := ValidateNameserver(tc.nameserver)
		switch {
		case tc.wantErr == "" && err != nil:
			t.Errorf("ValidateNameserver(%q) = %v, want nil", tc.nameserver, err)
		case tc.wantErr != "" && (err == nil || !strings.Contains(err.Error(), tc.wantErr)):
			t.Errorf("ValidateNameserver(%q) = %v, want %q", tc.nameserver, err, tc.wantErr)
		}
	}
}
