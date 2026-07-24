package validation

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestSIPConfigValidator(t *testing.T) {
	validator := &SIPConfigValidator{}

	tests := []struct {
		name        string
		config      string
		wantErr     bool
		errContains string
	}{
		{
			name:   "minimal options monitor",
			config: `{"host":"sip.example.com"}`,
		},
		{
			name:   "tls transport",
			config: `{"host":"sip.example.com","transport":"tls","tls_skip_verify":true}`,
		},
		{
			name:   "register with credentials",
			config: `{"host":"sip.example.com","method":"register","username":"agent","password":"secret","domain":"example.com"}`,
		},
		{
			name:   "register without credentials is allowed",
			config: `{"host":"sip.example.com","method":"register"}`,
		},
		{
			name:   "masked password preserved on update",
			config: `{"host":"sip.example.com","method":"register","username":"agent","password":"***"}`,
		},
		{
			name:        "invalid transport",
			config:      `{"host":"sip.example.com","transport":"sctp"}`,
			wantErr:     true,
			errContains: "transport must be",
		},
		{
			name:        "invalid method",
			config:      `{"host":"sip.example.com","method":"invite"}`,
			wantErr:     true,
			errContains: "method must be",
		},
		{
			name:        "password without username",
			config:      `{"host":"sip.example.com","password":"secret"}`,
			wantErr:     true,
			errContains: "username and password must be provided together",
		},
		{
			name:        "username without password",
			config:      `{"host":"sip.example.com","username":"agent"}`,
			wantErr:     true,
			errContains: "username and password must be provided together",
		},
		{
			name:        "missing host",
			config:      `{"transport":"udp"}`,
			wantErr:     true,
			errContains: "host is required",
		},
		{
			name:        "expected status out of range",
			config:      `{"host":"sip.example.com","expected_status":42}`,
			wantErr:     true,
			errContains: "expected_status must be between 100 and 699",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validator.ValidateConfig(json.RawMessage(tt.config))
			if (err != nil) != tt.wantErr {
				t.Fatalf("ValidateConfig() error = %v, wantErr %v", err, tt.wantErr)
			}
			if err != nil && tt.errContains != "" && !strings.Contains(err.Error(), tt.errContains) {
				t.Fatalf("ValidateConfig() error = %v, want to contain %q", err, tt.errContains)
			}
		})
	}
}
