package validation

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/yassinebenameur/probara/api/internal/models"
)

func TestValidateMonitor_GRPCTimeoutRules(t *testing.T) {
	baseConfig := json.RawMessage(`{"host":"grpc.example.com","port":443,"use_tls":true}`)

	tests := []struct {
		name        string
		req         *models.CreateMonitorRequest
		wantErr     bool
		errContains string
	}{
		{
			name: "valid grpc monitor",
			req: &models.CreateMonitorRequest{
				Name:            "grpc health",
				Type:            models.MonitorTypeGRPC,
				Config:          baseConfig,
				IntervalSeconds: 60,
				TimeoutSeconds:  10,
			},
			wantErr: false,
		},
		{
			name: "timeout must be > 0",
			req: &models.CreateMonitorRequest{
				Name:            "grpc health",
				Type:            models.MonitorTypeGRPC,
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
				Name:            "grpc health",
				Type:            models.MonitorTypeGRPC,
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

func TestValidateMonitorUpdate_GRPCTimeoutRules(t *testing.T) {
	enabled := true
	existing := &models.Monitor{
		ID:              uuid.New(),
		TenantID:        uuid.New(),
		Name:            "grpc health",
		Type:            models.MonitorTypeGRPC,
		Config:          json.RawMessage(`{"host":"grpc.example.com"}`),
		IntervalSeconds: 60,
		TimeoutSeconds:  10,
		Enabled:         enabled,
		CreatedAt:       time.Now(),
		UpdatedAt:       time.Now(),
	}

	t.Run("invalid timeout update", func(t *testing.T) {
		timeout := 60
		req := &models.UpdateMonitorRequest{TimeoutSeconds: &timeout}
		err := ValidateMonitorUpdate(req, existing)
		if err == nil || !strings.Contains(err.Error(), "timeout_seconds must be less than interval_seconds") {
			t.Fatalf("expected timeout validation error, got %v", err)
		}
	})

	t.Run("valid timeout update", func(t *testing.T) {
		timeout := 5
		req := &models.UpdateMonitorRequest{TimeoutSeconds: &timeout}
		err := ValidateMonitorUpdate(req, existing)
		if err != nil {
			t.Fatalf("expected no error, got %v", err)
		}
	})
}
