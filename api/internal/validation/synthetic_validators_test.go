package validation

import (
	"encoding/json"
	"testing"
)

func TestSyntheticAPIConfigValidator(t *testing.T) {
	validator := &SyntheticAPIConfigValidator{}

	tests := []struct {
		name    string
		config  json.RawMessage
		wantErr bool
	}{
		{
			name: "valid synthetic api config",
			config: json.RawMessage(`{
				"base_url": "https://example.com",
				"failure_mode": "fail_fast",
				"variables": {"email":"user@example.com"},
				"steps": [
					{
						"id": "login",
						"request": {"method":"POST","url":"/auth/login"},
						"assert": [{"target":"status","op":"in","value":[200,201]}],
						"extract": [{"name":"token","from":"json","path":"token","sensitive":true}]
					},
					{
						"id": "check",
						"request": {
							"method":"GET",
							"url":"/v1/health",
							"headers":{"Authorization":"Bearer {{token}}"}
						},
						"assert": [{"target":"json","path":"ok","op":"bool_is","value":true}]
					}
				]
			}`),
			wantErr: false,
		},
		{
			name: "relative url without base_url",
			config: json.RawMessage(`{
				"steps": [
					{"id":"s1","request":{"method":"GET","url":"/health"}}
				]
			}`),
			wantErr: true,
		},
		{
			name: "invalid status assertion op",
			config: json.RawMessage(`{
				"base_url": "https://example.com",
				"steps": [
					{
						"id":"s1",
						"request":{"method":"GET","url":"/health"},
						"assert":[{"target":"status","op":"contains","value":"200"}]
					}
				]
			}`),
			wantErr: true,
		},
		{
			name: "duplicate extract name",
			config: json.RawMessage(`{
				"base_url": "https://example.com",
				"steps": [
					{
						"id":"s1",
						"request":{"method":"GET","url":"/health"},
						"extract":[
							{"name":"token","from":"header","path":"X-Token"},
							{"name":"token","from":"json","path":"token"}
						]
					}
				]
			}`),
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validator.ValidateConfig(tt.config)
			if (err != nil) != tt.wantErr {
				t.Fatalf("ValidateConfig() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

func TestSyntheticBrowserConfigValidator(t *testing.T) {
	validator := &SyntheticBrowserConfigValidator{}

	tests := []struct {
		name    string
		config  json.RawMessage
		wantErr bool
	}{
		{
			name: "valid synthetic browser config",
			config: json.RawMessage(`{
				"start_url":"https://example.com/login",
				"device":"Desktop Chrome",
				"failure_mode":"continue",
				"steps":[
					{"id":"s1","action":"goto","url":"https://example.com/login"},
					{"id":"s2","action":"fill","selector":"#email","value":"user@example.com"},
					{"id":"s3","action":"click","selector":"button[type=submit]"},
					{"id":"s4","action":"assert_visible","selector":"[data-test=dashboard]"}
				]
			}`),
			wantErr: false,
		},
		{
			name: "missing start_url",
			config: json.RawMessage(`{
				"steps":[{"id":"s1","action":"goto","url":"https://example.com"}]
			}`),
			wantErr: true,
		},
		{
			name: "click requires selector",
			config: json.RawMessage(`{
				"start_url":"https://example.com",
				"steps":[{"id":"s1","action":"click"}]
			}`),
			wantErr: true,
		},
		{
			name: "unknown action",
			config: json.RawMessage(`{
				"start_url":"https://example.com",
				"steps":[{"id":"s1","action":"scroll"}]
			}`),
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validator.ValidateConfig(tt.config)
			if (err != nil) != tt.wantErr {
				t.Fatalf("ValidateConfig() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}
