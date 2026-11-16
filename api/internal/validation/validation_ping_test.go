package validation

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/yassinebenameur/probara/api/internal/models"
)

func pingRequest(config string, interval, timeout int) *models.CreateMonitorRequest {
	return &models.CreateMonitorRequest{
		Name:            "edge ping",
		Type:            models.MonitorTypePing,
		Config:          json.RawMessage(config),
		IntervalSeconds: interval,
		TimeoutSeconds:  timeout,
	}
}

func TestValidateMonitor_PingEchoes(t *testing.T) {
	tests := []struct {
		name        string
		req         *models.CreateMonitorRequest
		errContains string
	}{
		{name: "bare host, short timeout", req: pingRequest(`{"host":"192.0.2.1"}`, 60, 1)},
		{name: "count, interval and thresholds", req: pingRequest(`{"host":"edge.example.com","count":5,"interval_ms":500,"max_packet_loss_pct":20,"max_rtt_ms":150}`, 60, 5)},
		{name: "count out of range", req: pingRequest(`{"host":"edge.example.com","count":20}`, 60, 10), errContains: "count must be between 1 and 10"},
		{name: "interval below the floor", req: pingRequest(`{"host":"edge.example.com","interval_ms":50}`, 60, 10), errContains: "interval_ms must be between 200 and 10000"},
		{name: "loss threshold of 100", req: pingRequest(`{"host":"edge.example.com","max_packet_loss_pct":100}`, 60, 10), errContains: "max_packet_loss_pct"},
		{name: "rtt threshold of 0", req: pingRequest(`{"host":"edge.example.com","max_rtt_ms":0}`, 60, 10), errContains: "max_rtt_ms must be greater than 0"},
		{name: "explicit interval that does not fit", req: pingRequest(`{"host":"edge.example.com","count":5,"interval_ms":2000}`, 60, 5), errContains: "timeout_seconds must be at least 9 for 5 echoes 2000 ms apart"},
		{name: "invalid host", req: pingRequest(`{"host":"not a host"}`, 60, 5), errContains: "host must be a valid IP address or hostname"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateMonitor(tt.req)
			if tt.errContains == "" {
				if err != nil {
					t.Fatalf("ValidateMonitor() = %v, want nil", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tt.errContains) {
				t.Fatalf("ValidateMonitor() = %v, want error containing %q", err, tt.errContains)
			}
		})
	}
}

// Lowering the timeout of a monitor with an explicit interval is checked
// against the effective config; a bare-host monitor accepts any timeout.
func TestValidateMonitorUpdate_PingEffectiveSchedule(t *testing.T) {
	explicit := &models.Monitor{
		ID: uuid.New(), Type: models.MonitorTypePing,
		Config:          json.RawMessage(`{"host":"edge.example.com","count":3,"interval_ms":1000}`),
		IntervalSeconds: 60, TimeoutSeconds: 10,
	}
	if err := ValidateMonitorUpdate(&models.UpdateMonitorRequest{TimeoutSeconds: intPtr(2)}, explicit); err == nil ||
		!strings.Contains(err.Error(), "timeout_seconds must be at least 3") {
		t.Fatalf("timeout-only patch = %v, want the echo schedule refused", err)
	}
	bare := &models.Monitor{
		ID: uuid.New(), Type: models.MonitorTypePing,
		Config:          json.RawMessage(`{"host":"edge.example.com"}`),
		IntervalSeconds: 60, TimeoutSeconds: 10,
	}
	if err := ValidateMonitorUpdate(&models.UpdateMonitorRequest{TimeoutSeconds: intPtr(1)}, bare); err != nil {
		t.Fatalf("bare host timeout-only patch = %v, want nil", err)
	}
}
