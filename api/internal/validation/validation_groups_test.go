package validation

import (
	"encoding/json"
	"testing"

	"github.com/yassinebenameur/probara/api/internal/models"
)

func TestValidateMonitor_GroupType(t *testing.T) {
	tests := []struct {
		name    string
		req     *models.CreateMonitorRequest
		wantErr bool
		errMsg  string
	}{
		{
			name: "valid group monitor",
			req: &models.CreateMonitorRequest{
				Name: "Test Group",
				Type: models.MonitorTypeGroup,
				Config: mustMarshalJSON(models.GroupConfig{
					MonitorIDs: []string{"id1", "id2", "id3"},
				}),
				IntervalSeconds: 60,
				TimeoutSeconds:  30,
			},
			wantErr: false,
		},
		{
			name: "group with no monitors",
			req: &models.CreateMonitorRequest{
				Name: "Empty Group",
				Type: models.MonitorTypeGroup,
				Config: mustMarshalJSON(models.GroupConfig{
					MonitorIDs: []string{},
				}),
				IntervalSeconds: 60,
				TimeoutSeconds:  30,
			},
			wantErr: true,
			errMsg:  "monitor_ids is required and cannot be empty",
		},
		{
			name: "group with empty string in monitor_ids",
			req: &models.CreateMonitorRequest{
				Name: "Invalid Group",
				Type: models.MonitorTypeGroup,
				Config: mustMarshalJSON(models.GroupConfig{
					MonitorIDs: []string{"id1", "", "id2"},
				}),
				IntervalSeconds: 60,
				TimeoutSeconds:  30,
			},
			wantErr: true,
			errMsg:  "monitor_ids cannot contain empty strings",
		},
		{
			name: "group without config",
			req: &models.CreateMonitorRequest{
				Name:            "No Config Group",
				Type:            models.MonitorTypeGroup,
				Config:          json.RawMessage{},
				IntervalSeconds: 60,
				TimeoutSeconds:  30,
			},
			wantErr: true,
			errMsg:  "config is required",
		},
		{
			name: "group without name",
			req: &models.CreateMonitorRequest{
				Name: "",
				Type: models.MonitorTypeGroup,
				Config: mustMarshalJSON(models.GroupConfig{
					MonitorIDs: []string{"id1", "id2"},
				}),
				IntervalSeconds: 60,
				TimeoutSeconds:  30,
			},
			wantErr: true,
			errMsg:  "name is required",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateMonitor(tt.req)
			if tt.wantErr {
				if err == nil {
					t.Errorf("ValidateMonitor() expected error containing %q, got nil", tt.errMsg)
					return
				}
				if tt.errMsg != "" && !contains(err.Error(), tt.errMsg) {
					t.Errorf("ValidateMonitor() error = %v, want error containing %q", err, tt.errMsg)
				}
			} else {
				if err != nil {
					t.Errorf("ValidateMonitor() unexpected error = %v", err)
				}
			}
		})
	}
}

func TestValidateMonitorUpdate_GroupType(t *testing.T) {
	existingGroup := &models.Monitor{
		Name:            "Existing Group",
		Type:            models.MonitorTypeGroup,
		IntervalSeconds: 60,
		TimeoutSeconds:  30,
	}

	tests := []struct {
		name    string
		req     *models.UpdateMonitorRequest
		wantErr bool
		errMsg  string
	}{
		{
			name: "update group config",
			req: &models.UpdateMonitorRequest{
				Config: mustMarshalJSON(models.GroupConfig{
					MonitorIDs: []string{"new-id1", "new-id2"},
				}),
			},
			wantErr: false,
		},
		{
			name: "update group name",
			req: &models.UpdateMonitorRequest{
				Name: strPtr("Updated Group Name"),
			},
			wantErr: false,
		},
		{
			name: "update group with empty monitor_ids",
			req: &models.UpdateMonitorRequest{
				Config: mustMarshalJSON(models.GroupConfig{
					MonitorIDs: []string{},
				}),
			},
			wantErr: true,
			errMsg:  "monitor_ids is required and cannot be empty",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateMonitorUpdate(tt.req, existingGroup)
			if tt.wantErr {
				if err == nil {
					t.Errorf("ValidateMonitorUpdate() expected error containing %q, got nil", tt.errMsg)
					return
				}
				if tt.errMsg != "" && !contains(err.Error(), tt.errMsg) {
					t.Errorf("ValidateMonitorUpdate() error = %v, want error containing %q", err, tt.errMsg)
				}
			} else {
				if err != nil {
					t.Errorf("ValidateMonitorUpdate() unexpected error = %v", err)
				}
			}
		})
	}
}

func TestValidateGroupConfig(t *testing.T) {
	validator := &GroupConfigValidator{}

	tests := []struct {
		name    string
		config  *models.GroupConfig
		wantErr bool
		errMsg  string
	}{
		{
			name: "valid config with multiple monitors",
			config: &models.GroupConfig{
				MonitorIDs: []string{"id1", "id2", "id3"},
			},
			wantErr: false,
		},
		{
			name: "valid config with single monitor",
			config: &models.GroupConfig{
				MonitorIDs: []string{"id1"},
			},
			wantErr: false,
		},
		{
			name: "empty monitor_ids",
			config: &models.GroupConfig{
				MonitorIDs: []string{},
			},
			wantErr: true,
			errMsg:  "monitor_ids is required and cannot be empty",
		},
		{
			name: "nil monitor_ids",
			config: &models.GroupConfig{
				MonitorIDs: nil,
			},
			wantErr: true,
			errMsg:  "monitor_ids is required and cannot be empty",
		},
		{
			name: "monitor_ids with empty string",
			config: &models.GroupConfig{
				MonitorIDs: []string{"id1", "", "id2"},
			},
			wantErr: true,
			errMsg:  "monitor_ids cannot contain empty strings",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			configJSON := mustMarshalJSON(tt.config)
			err := validator.ValidateConfig(configJSON)
			if tt.wantErr {
				if err == nil {
					t.Errorf("GroupConfigValidator.ValidateConfig() expected error containing %q, got nil", tt.errMsg)
					return
				}
				if tt.errMsg != "" && !contains(err.Error(), tt.errMsg) {
					t.Errorf("GroupConfigValidator.ValidateConfig() error = %v, want error containing %q", err, tt.errMsg)
				}
			} else {
				if err != nil {
					t.Errorf("GroupConfigValidator.ValidateConfig() unexpected error = %v", err)
				}
			}
		})
	}
}

// Helper functions

func mustMarshalJSON(v interface{}) json.RawMessage {
	data, err := json.Marshal(v)
	if err != nil {
		panic(err)
	}
	return data
}

func contains(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || len(s) > len(substr) &&
		(s[:len(substr)] == substr || s[len(s)-len(substr):] == substr ||
			findSubstring(s, substr)))
}

func findSubstring(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}

func strPtr(s string) *string {
	return &s
}
