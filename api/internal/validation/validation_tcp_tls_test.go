package validation

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/yassinebenameur/probara/api/internal/models"
)

func TestValidateMonitor_TCPCertificateFields(t *testing.T) {
	tests := []struct {
		config      string
		errContains string
	}{
		{config: `{"host":"imap.example.com","port":993,"use_tls":true,"tls_min_days_valid":14,"tls_server_name":"imap.example.com"}`},
		{config: `{"host":"imap.example.com","port":993,"tls_min_days_valid":14}`, errContains: "tls_min_days_valid requires use_tls"},
		{config: `{"host":"imap.example.com","port":993,"use_tls":true,"tls_min_days_valid":-1}`, errContains: "tls_min_days_valid must be 0 or greater"},
		{config: `{"host":"10.0.0.5","port":993,"use_tls":true,"tls_server_name":"bad name"}`, errContains: "tls_server_name must be a valid hostname or IP address"},
	}
	for _, tt := range tests {
		err := ValidateMonitor(&models.CreateMonitorRequest{
			Name: "imap", Type: models.MonitorTypeTCP, Config: json.RawMessage(tt.config),
			IntervalSeconds: 60, TimeoutSeconds: 10,
		})
		if tt.errContains == "" && err != nil || tt.errContains != "" && (err == nil || !strings.Contains(err.Error(), tt.errContains)) {
			t.Errorf("%s: ValidateMonitor() = %v, want %q", tt.config, err, tt.errContains)
		}
	}
}
