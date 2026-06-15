package validation

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/yassinebenameur/probara/api/internal/models"
)

// TCP is an active check type, so the timeout rules the migration enforces at
// the DB layer must also be enforced by the API validator (otherwise invalid
// timeouts surface as generic create failures instead of 400s).
func TestValidateMonitor_TCPTimeoutRules(t *testing.T) {
	baseConfig := json.RawMessage(`{"host":"db.example.com","port":5432}`)

	tests := []struct {
		name        string
		req         *models.CreateMonitorRequest
		wantErr     bool
		errContains string
	}{
		{
			name: "valid tcp monitor",
			req: &models.CreateMonitorRequest{
				Name:            "db port",
				Type:            models.MonitorTypeTCP,
				Config:          baseConfig,
				IntervalSeconds: 60,
				TimeoutSeconds:  10,
			},
			wantErr: false,
		},
		{
			name: "timeout must be > 0",
			req: &models.CreateMonitorRequest{
				Name:            "db port",
				Type:            models.MonitorTypeTCP,
				Config:          baseConfig,
				IntervalSeconds: 60,
				TimeoutSeconds:  0,
			},
			wantErr:     true,
			errContains: "timeout_seconds must be greater than 0",
		},
		{
			name: "timeout must be < interval",
			req: &models.CreateMonitorRequest{
				Name:            "db port",
				Type:            models.MonitorTypeTCP,
				Config:          baseConfig,
				IntervalSeconds: 60,
				TimeoutSeconds:  60,
			},
			wantErr:     true,
			errContains: "timeout_seconds must be less than interval_seconds",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateMonitor(tt.req)
			if (err != nil) != tt.wantErr {
				t.Errorf("ValidateMonitor() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if err != nil && tt.errContains != "" && !strings.Contains(err.Error(), tt.errContains) {
				t.Errorf("ValidateMonitor() error = %v, want to contain %q", err, tt.errContains)
			}
		})
	}
}
