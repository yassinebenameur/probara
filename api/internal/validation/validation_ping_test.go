package validation

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/yassinebenameur/probara/api/internal/models"
)

// Ping depth: the config rules come from the shared struct, and the echoes a
// check sends must fit its timeout on create and on a timeout-only PATCH.
func TestValidateMonitor_PingDepth(t *testing.T) {
	create := func(config string, timeout int) *models.CreateMonitorRequest {
		return &models.CreateMonitorRequest{
			Name: "edge", Type: models.MonitorTypePing, Config: json.RawMessage(config),
			IntervalSeconds: 60, TimeoutSeconds: timeout,
		}
	}
	tests := []struct {
		name    string
		req     *models.CreateMonitorRequest
		wantErr string
	}{
		{"legacy host only", create(`{"host":"example.com"}`, 5), ""},
		{"three echoes with thresholds", create(`{"host":"example.com","count":3,"interval_ms":500,"max_packet_loss_pct":34,"max_rtt_ms":200,"warn_rtt_ms":100}`, 5), ""},
		{"count out of range", create(`{"host":"example.com","count":20}`, 30), "count must be between 1 and 10"},
		{"interval too short", create(`{"host":"example.com","interval_ms":50}`, 30), "interval_ms"},
		{"echoes outlast timeout", create(`{"host":"example.com","count":10,"interval_ms":1000}`, 5), "timeout_seconds must be at least 10s"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateMonitor(tt.req)
			if tt.wantErr == "" {
				if err != nil {
					t.Fatalf("ValidateMonitor() = %v, want nil", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("ValidateMonitor() = %v, want error containing %q", err, tt.wantErr)
			}
		})
	}

	existing := &models.Monitor{Type: models.MonitorTypePing, Config: json.RawMessage(`{"host":"example.com","count":5}`), IntervalSeconds: 60, TimeoutSeconds: 10}
	short := 3
	if err := ValidateMonitorUpdate(&models.UpdateMonitorRequest{TimeoutSeconds: &short}, existing); err == nil {
		t.Fatal("a timeout-only PATCH below the echoes' duration was accepted")
	}
}
