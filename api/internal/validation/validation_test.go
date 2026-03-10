package validation

import (
	"strings"
	"testing"

	"github.com/yassinebenameur/probara/api/internal/models"
)

// ptr returns a pointer to the given value
func ptr[T any](v T) *T {
	return &v
}

// TestValidateAlertPolicy tests alert policy creation validation
func TestValidateAlertPolicy(t *testing.T) {
	tests := []struct {
		name        string
		req         *models.CreateAlertPolicyRequest
		wantErr     bool
		errContains string
	}{
		{
			name: "valid policy",
			req: &models.CreateAlertPolicyRequest{
				Name:                 "Test Policy",
				FailureThreshold:     3,
				FailureWindowSeconds: 300,
			},
			wantErr: false,
		},
		{
			name: "valid policy with description",
			req: &models.CreateAlertPolicyRequest{
				Name:                 "Test Policy",
				Description:          ptr("A test description"),
				FailureThreshold:     5,
				FailureWindowSeconds: 600,
			},
			wantErr: false,
		},
		{
			name: "missing name",
			req: &models.CreateAlertPolicyRequest{
				FailureThreshold:     3,
				FailureWindowSeconds: 300,
			},
			wantErr:     true,
			errContains: "name is required",
		},
		{
			name: "empty name",
			req: &models.CreateAlertPolicyRequest{
				Name:                 "",
				FailureThreshold:     3,
				FailureWindowSeconds: 300,
			},
			wantErr:     true,
			errContains: "name is required",
		},
		{
			name: "zero failure threshold",
			req: &models.CreateAlertPolicyRequest{
				Name:                 "Test",
				FailureThreshold:     0,
				FailureWindowSeconds: 300,
			},
			wantErr:     true,
			errContains: "failure_threshold must be greater than 0",
		},
		{
			name: "negative failure threshold",
			req: &models.CreateAlertPolicyRequest{
				Name:                 "Test",
				FailureThreshold:     -1,
				FailureWindowSeconds: 300,
			},
			wantErr:     true,
			errContains: "failure_threshold must be greater than 0",
		},
		{
			name: "zero failure window",
			req: &models.CreateAlertPolicyRequest{
				Name:                 "Test",
				FailureThreshold:     3,
				FailureWindowSeconds: 0,
			},
			wantErr:     true,
			errContains: "failure_window_seconds must be greater than 0",
		},
		{
			name: "negative failure window",
			req: &models.CreateAlertPolicyRequest{
				Name:                 "Test",
				FailureThreshold:     3,
				FailureWindowSeconds: -100,
			},
			wantErr:     true,
			errContains: "failure_window_seconds must be greater than 0",
		},
		{
			name: "minimum valid values",
			req: &models.CreateAlertPolicyRequest{
				Name:                 "A",
				FailureThreshold:     1,
				FailureWindowSeconds: 1,
			},
			wantErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateAlertPolicy(tt.req)
			if (err != nil) != tt.wantErr {
				t.Errorf("ValidateAlertPolicy() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if err != nil && tt.errContains != "" {
				if !strings.Contains(err.Error(), tt.errContains) {
					t.Errorf("ValidateAlertPolicy() error = %v, want error containing %q", err, tt.errContains)
				}
			}
		})
	}
}

// TestValidateAlertPolicyUpdate tests alert policy update validation
func TestValidateAlertPolicyUpdate(t *testing.T) {
	tests := []struct {
		name        string
		req         *models.UpdateAlertPolicyRequest
		wantErr     bool
		errContains string
	}{
		{
			name:    "empty update is valid",
			req:     &models.UpdateAlertPolicyRequest{},
			wantErr: false,
		},
		{
			name: "valid name update",
			req: &models.UpdateAlertPolicyRequest{
				Name: ptr("New Name"),
			},
			wantErr: false,
		},
		{
			name: "valid threshold update",
			req: &models.UpdateAlertPolicyRequest{
				FailureThreshold: ptr(5),
			},
			wantErr: false,
		},
		{
			name: "valid window update",
			req: &models.UpdateAlertPolicyRequest{
				FailureWindowSeconds: ptr(600),
			},
			wantErr: false,
		},
		{
			name: "zero threshold update",
			req: &models.UpdateAlertPolicyRequest{
				FailureThreshold: ptr(0),
			},
			wantErr:     true,
			errContains: "failure_threshold must be greater than 0",
		},
		{
			name: "negative threshold update",
			req: &models.UpdateAlertPolicyRequest{
				FailureThreshold: ptr(-5),
			},
			wantErr:     true,
			errContains: "failure_threshold must be greater than 0",
		},
		{
			name: "zero window update",
			req: &models.UpdateAlertPolicyRequest{
				FailureWindowSeconds: ptr(0),
			},
			wantErr:     true,
			errContains: "failure_window_seconds must be greater than 0",
		},
		{
			name: "negative window update",
			req: &models.UpdateAlertPolicyRequest{
				FailureWindowSeconds: ptr(-100),
			},
			wantErr:     true,
			errContains: "failure_window_seconds must be greater than 0",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateAlertPolicyUpdate(tt.req)
			if (err != nil) != tt.wantErr {
				t.Errorf("ValidateAlertPolicyUpdate() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if err != nil && tt.errContains != "" {
				if !strings.Contains(err.Error(), tt.errContains) {
					t.Errorf("ValidateAlertPolicyUpdate() error = %v, want error containing %q", err, tt.errContains)
				}
			}
		})
	}
}

// TestValidateStatusPage tests status page creation validation
func TestValidateStatusPage(t *testing.T) {
	tests := []struct {
		name        string
		req         *models.CreateStatusPageRequest
		wantErr     bool
		errContains string
	}{
		{
			name: "valid minimal status page",
			req: &models.CreateStatusPageRequest{
				Slug:  "my-status",
				Title: "My Status Page",
			},
			wantErr: false,
		},
		{
			name: "valid status page with all fields",
			req: &models.CreateStatusPageRequest{
				Slug:           "my-status",
				Title:          "My Status Page",
				Description:    ptr("A description"),
				LogoURL:        ptr("https://example.com/logo.png"),
				PrimaryColor:   ptr("#1e90ff"),
				SecondaryColor: ptr("#ffffff"),
				Settings: &models.StatusPageSettings{
					DefaultTheme:     ptr("dark"),
					AllowThemeToggle: ptr(true),
				},
				MonitorIDs: []string{"id1", "id2"},
			},
			wantErr: false,
		},
		{
			name: "missing slug",
			req: &models.CreateStatusPageRequest{
				Title: "My Status Page",
			},
			wantErr:     true,
			errContains: "slug is required",
		},
		{
			name: "empty slug",
			req: &models.CreateStatusPageRequest{
				Slug:  "",
				Title: "My Status Page",
			},
			wantErr:     true,
			errContains: "slug is required",
		},
		{
			name: "missing title",
			req: &models.CreateStatusPageRequest{
				Slug: "my-status",
			},
			wantErr:     true,
			errContains: "title is required",
		},
		{
			name: "slug with uppercase",
			req: &models.CreateStatusPageRequest{
				Slug:  "My-Status",
				Title: "My Status Page",
			},
			wantErr:     true,
			errContains: "slug must contain only lowercase letters, numbers, and hyphens",
		},
		{
			name: "slug with spaces",
			req: &models.CreateStatusPageRequest{
				Slug:  "my status",
				Title: "My Status Page",
			},
			wantErr:     true,
			errContains: "slug must contain only lowercase letters, numbers, and hyphens",
		},
		{
			name: "slug with special characters",
			req: &models.CreateStatusPageRequest{
				Slug:  "my_status!",
				Title: "My Status Page",
			},
			wantErr:     true,
			errContains: "slug must contain only lowercase letters, numbers, and hyphens",
		},
		{
			name: "valid slug with numbers",
			req: &models.CreateStatusPageRequest{
				Slug:  "status-page-123",
				Title: "Status Page 123",
			},
			wantErr: false,
		},
		{
			name: "valid slug numbers only",
			req: &models.CreateStatusPageRequest{
				Slug:  "123",
				Title: "Page 123",
			},
			wantErr: false,
		},
		{
			name: "invalid primary color - missing hash",
			req: &models.CreateStatusPageRequest{
				Slug:         "my-status",
				Title:        "My Status Page",
				PrimaryColor: ptr("1e90ff"),
			},
			wantErr:     true,
			errContains: "primary_color must be a valid hex color",
		},
		{
			name: "invalid primary color - too short",
			req: &models.CreateStatusPageRequest{
				Slug:         "my-status",
				Title:        "My Status Page",
				PrimaryColor: ptr("#1e90f"),
			},
			wantErr:     true,
			errContains: "primary_color must be a valid hex color",
		},
		{
			name: "invalid primary color - too long",
			req: &models.CreateStatusPageRequest{
				Slug:         "my-status",
				Title:        "My Status Page",
				PrimaryColor: ptr("#1e90fff"),
			},
			wantErr:     true,
			errContains: "primary_color must be a valid hex color",
		},
		{
			name: "invalid primary color - invalid chars",
			req: &models.CreateStatusPageRequest{
				Slug:         "my-status",
				Title:        "My Status Page",
				PrimaryColor: ptr("#gggggg"),
			},
			wantErr:     true,
			errContains: "primary_color must be a valid hex color",
		},
		{
			name: "invalid secondary color",
			req: &models.CreateStatusPageRequest{
				Slug:           "my-status",
				Title:          "My Status Page",
				SecondaryColor: ptr("white"),
			},
			wantErr:     true,
			errContains: "secondary_color must be a valid hex color",
		},
		{
			name: "valid color uppercase",
			req: &models.CreateStatusPageRequest{
				Slug:         "my-status",
				Title:        "My Status Page",
				PrimaryColor: ptr("#AABBCC"),
			},
			wantErr: false,
		},
		{
			name: "valid color mixed case",
			req: &models.CreateStatusPageRequest{
				Slug:         "my-status",
				Title:        "My Status Page",
				PrimaryColor: ptr("#AaBbCc"),
			},
			wantErr: false,
		},
		{
			name: "invalid default theme",
			req: &models.CreateStatusPageRequest{
				Slug:  "my-status",
				Title: "My Status Page",
				Settings: &models.StatusPageSettings{
					DefaultTheme: ptr("blue"),
				},
			},
			wantErr:     true,
			errContains: "default_theme must be either dark or light",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateStatusPage(tt.req)
			if (err != nil) != tt.wantErr {
				t.Errorf("ValidateStatusPage() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if err != nil && tt.errContains != "" {
				if !strings.Contains(err.Error(), tt.errContains) {
					t.Errorf("ValidateStatusPage() error = %v, want error containing %q", err, tt.errContains)
				}
			}
		})
	}
}

// TestValidateStatusPageUpdate tests status page update validation
func TestValidateStatusPageUpdate(t *testing.T) {
	tests := []struct {
		name        string
		req         *models.UpdateStatusPageRequest
		wantErr     bool
		errContains string
	}{
		{
			name:    "empty update is valid",
			req:     &models.UpdateStatusPageRequest{},
			wantErr: false,
		},
		{
			name: "valid slug update",
			req: &models.UpdateStatusPageRequest{
				Slug: ptr("new-slug"),
			},
			wantErr: false,
		},
		{
			name: "invalid slug update - uppercase",
			req: &models.UpdateStatusPageRequest{
				Slug: ptr("New-Slug"),
			},
			wantErr:     true,
			errContains: "slug must contain only lowercase letters, numbers, and hyphens",
		},
		{
			name: "valid color update",
			req: &models.UpdateStatusPageRequest{
				PrimaryColor: ptr("#ff5500"),
			},
			wantErr: false,
		},
		{
			name: "invalid primary color update",
			req: &models.UpdateStatusPageRequest{
				PrimaryColor: ptr("red"),
			},
			wantErr:     true,
			errContains: "primary_color must be a valid hex color",
		},
		{
			name: "invalid secondary color update",
			req: &models.UpdateStatusPageRequest{
				SecondaryColor: ptr("#12345"),
			},
			wantErr:     true,
			errContains: "secondary_color must be a valid hex color",
		},
		{
			name: "valid theme update",
			req: &models.UpdateStatusPageRequest{
				Settings: &models.StatusPageSettings{
					DefaultTheme: ptr("light"),
				},
			},
			wantErr: false,
		},
		{
			name: "invalid theme update",
			req: &models.UpdateStatusPageRequest{
				Settings: &models.StatusPageSettings{
					DefaultTheme: ptr("sepia"),
				},
			},
			wantErr:     true,
			errContains: "default_theme must be either dark or light",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateStatusPageUpdate(tt.req)
			if (err != nil) != tt.wantErr {
				t.Errorf("ValidateStatusPageUpdate() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if err != nil && tt.errContains != "" {
				if !strings.Contains(err.Error(), tt.errContains) {
					t.Errorf("ValidateStatusPageUpdate() error = %v, want error containing %q", err, tt.errContains)
				}
			}
		})
	}
}

// TestValidateURL tests URL validation helper
func TestValidateURL(t *testing.T) {
	tests := []struct {
		name        string
		url         string
		wantErr     bool
		errContains string
	}{
		{
			name:    "valid https URL",
			url:     "https://example.com",
			wantErr: false,
		},
		{
			name:    "valid http URL",
			url:     "http://example.com",
			wantErr: false,
		},
		{
			name:    "valid URL with path",
			url:     "https://example.com/api/v1/health",
			wantErr: false,
		},
		{
			name:    "valid URL with port",
			url:     "https://example.com:8443/api",
			wantErr: false,
		},
		{
			name:    "valid URL with query params",
			url:     "https://example.com/api?key=value",
			wantErr: false,
		},
		{
			name:    "valid URL with IP",
			url:     "http://192.168.1.1:8080",
			wantErr: false,
		},
		{
			name:        "invalid scheme - ftp",
			url:         "ftp://example.com",
			wantErr:     true,
			errContains: "URL scheme must be http or https",
		},
		{
			name:        "invalid scheme - empty",
			url:         "://example.com",
			wantErr:     true,
			errContains: "failed to parse URL",
		},
		{
			name:        "missing host",
			url:         "https://",
			wantErr:     true,
			errContains: "URL must have a host",
		},
		{
			name:        "relative URL",
			url:         "/api/health",
			wantErr:     true,
			errContains: "URL scheme must be http or https",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateURL(tt.url)
			if (err != nil) != tt.wantErr {
				t.Errorf("validateURL(%q) error = %v, wantErr %v", tt.url, err, tt.wantErr)
				return
			}
			if err != nil && tt.errContains != "" {
				if !strings.Contains(err.Error(), tt.errContains) {
					t.Errorf("validateURL(%q) error = %v, want error containing %q", tt.url, err, tt.errContains)
				}
			}
		})
	}
}

// TestIsValidHostname tests hostname validation helper
func TestIsValidHostname(t *testing.T) {
	tests := []struct {
		name     string
		hostname string
		want     bool
	}{
		{"simple hostname", "example", true},
		{"domain", "example.com", true},
		{"subdomain", "api.example.com", true},
		{"deep subdomain", "a.b.c.example.com", true},
		{"with hyphen", "my-server.example.com", true},
		{"with numbers", "server1.example.com", true},
		{"numbers only", "123.example.com", true},
		{"single char labels", "a.b.c", true},
		{"empty string", "", false},
		{"starts with hyphen", "-example.com", false},
		{"ends with hyphen", "example-.com", false},
		{"label starts with hyphen", "example.-invalid.com", false},
		{"underscore", "my_server.example.com", false},
		{"space", "my server.com", false},
		{"special chars", "example!.com", false},
		{"too long label", strings.Repeat("a", 64) + ".com", false},
		{"max length label (63)", strings.Repeat("a", 63) + ".com", true},
		{"too long hostname (254 chars)", strings.Repeat("a.", 127) + "a", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := isValidHostname(tt.hostname)
			if got != tt.want {
				t.Errorf("isValidHostname(%q) = %v, want %v", tt.hostname, got, tt.want)
			}
		})
	}
}

// TestIsValidColor tests color validation helper
func TestIsValidColor(t *testing.T) {
	tests := []struct {
		name  string
		color string
		want  bool
	}{
		{"valid lowercase", "#aabbcc", true},
		{"valid uppercase", "#AABBCC", true},
		{"valid mixed case", "#AaBbCc", true},
		{"valid with numbers", "#123456", true},
		{"valid black", "#000000", true},
		{"valid white", "#ffffff", true},
		{"missing hash", "aabbcc", false},
		{"too short", "#aabbc", false},
		{"too long", "#aabbccd", false},
		{"invalid chars", "#gggggg", false},
		{"3-char shorthand", "#abc", false},
		{"empty", "", false},
		{"just hash", "#", false},
		{"rgb format", "rgb(0,0,0)", false},
		{"color name", "red", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := isValidColor(tt.color)
			if got != tt.want {
				t.Errorf("isValidColor(%q) = %v, want %v", tt.color, got, tt.want)
			}
		})
	}
}
