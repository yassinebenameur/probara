package validation

import (
	"strings"
	"testing"

	"github.com/yassinebenameur/probara/api/internal/models"
	_ "github.com/yassinebenameur/probara/shared/notifications/plugin/builtin"
)

func TestValidateAlertChannel(t *testing.T) {
	tests := []struct {
		name        string
		req         *models.CreateAlertChannelRequest
		wantErr     bool
		errContains string
	}{
		{
			name: "valid teams channel",
			req: &models.CreateAlertChannelRequest{
				Name:   "Teams",
				Type:   models.AlertChannelTypeTeams,
				Config: []byte(`{"webhook_url":"https://example.com"}`),
			},
			wantErr: false,
		},
		{
			name: "valid email channel",
			req: &models.CreateAlertChannelRequest{
				Name:   "Email",
				Type:   models.AlertChannelTypeEmail,
				Config: []byte(`{"to":["alerts@example.com","oncall@example.com"]}`),
			},
			wantErr: false,
		},
		{
			name: "missing name",
			req: &models.CreateAlertChannelRequest{
				Type:   models.AlertChannelTypeTeams,
				Config: []byte(`{"webhook_url":"https://example.com"}`),
			},
			wantErr:     true,
			errContains: "name is required",
		},
		{
			name: "invalid type",
			req: &models.CreateAlertChannelRequest{
				Name:   "Invalid",
				Type:   "definitely-not-a-plugin",
				Config: []byte(`{"webhook_url":"https://example.com"}`),
			},
			wantErr:     true,
			errContains: "invalid alert channel type",
		},
		{
			name: "missing config",
			req: &models.CreateAlertChannelRequest{
				Name: "Teams",
				Type: models.AlertChannelTypeTeams,
			},
			wantErr:     true,
			errContains: "config is required",
		},
		{
			name: "teams missing webhook url",
			req: &models.CreateAlertChannelRequest{
				Name:   "Teams",
				Type:   models.AlertChannelTypeTeams,
				Config: []byte(`{}`),
			},
			wantErr:     true,
			errContains: "webhook_url is required",
		},
		{
			name: "email missing recipients",
			req: &models.CreateAlertChannelRequest{
				Name:   "Email",
				Type:   models.AlertChannelTypeEmail,
				Config: []byte(`{}`),
			},
			wantErr:     true,
			errContains: "at least one recipient",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateAlertChannel(tt.req)
			if (err != nil) != tt.wantErr {
				t.Fatalf("ValidateAlertChannel() error = %v, wantErr %v", err, tt.wantErr)
			}
			if err != nil && tt.errContains != "" && !strings.Contains(err.Error(), tt.errContains) {
				t.Fatalf("ValidateAlertChannel() error = %v, want error containing %q", err, tt.errContains)
			}
		})
	}
}

func TestValidateAlertChannelUpdate(t *testing.T) {
	tests := []struct {
		name        string
		req         *models.UpdateAlertChannelRequest
		wantErr     bool
		errContains string
	}{
		{
			name:    "empty update",
			req:     &models.UpdateAlertChannelRequest{},
			wantErr: false,
		},
		{
			name: "empty name",
			req: &models.UpdateAlertChannelRequest{
				Name: ptr(" "),
			},
			wantErr:     true,
			errContains: "name cannot be empty",
		},
		{
			name: "invalid config json",
			req: &models.UpdateAlertChannelRequest{
				Config: []byte(`{invalid}`),
			},
			wantErr:     true,
			errContains: "invalid config",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateAlertChannelUpdate(tt.req)
			if (err != nil) != tt.wantErr {
				t.Fatalf("ValidateAlertChannelUpdate() error = %v, wantErr %v", err, tt.wantErr)
			}
			if err != nil && tt.errContains != "" && !strings.Contains(err.Error(), tt.errContains) {
				t.Fatalf("ValidateAlertChannelUpdate() error = %v, want error containing %q", err, tt.errContains)
			}
		})
	}
}
