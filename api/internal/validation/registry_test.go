package validation

import (
	"encoding/json"
	"testing"

	"github.com/yassinebenameur/probara/api/internal/models"
)

func TestValidatorRegistry_Register(t *testing.T) {
	registry := NewValidatorRegistry()
	validator := &HTTPConfigValidator{}

	registry.Register(models.MonitorTypeHTTP, validator)

	if !registry.Has(models.MonitorTypeHTTP) {
		t.Error("Expected registry to have HTTP validator")
	}
}

func TestValidatorRegistry_Validate(t *testing.T) {
	registry := NewDefaultValidatorRegistry()

	tests := []struct {
		name        string
		monitorType models.MonitorType
		config      json.RawMessage
		wantErr     bool
	}{
		{
			name:        "valid HTTP config",
			monitorType: models.MonitorTypeHTTP,
			config:      json.RawMessage(`{"url":"https://example.com","method":"GET"}`),
			wantErr:     false,
		},
		{
			name:        "invalid HTTP config - missing URL",
			monitorType: models.MonitorTypeHTTP,
			config:      json.RawMessage(`{"method":"GET"}`),
			wantErr:     true,
		},
		{
			name:        "valid ping config",
			monitorType: models.MonitorTypePing,
			config:      json.RawMessage(`{"host":"example.com"}`),
			wantErr:     false,
		},
		{
			name:        "invalid ping config - missing host",
			monitorType: models.MonitorTypePing,
			config:      json.RawMessage(`{}`),
			wantErr:     true,
		},
		{
			name:        "valid dns config",
			monitorType: models.MonitorTypeDNS,
			config:      json.RawMessage(`{"host":"example.com","record_type":"A"}`),
			wantErr:     false,
		},
		{
			name:        "invalid dns config - missing host",
			monitorType: models.MonitorTypeDNS,
			config:      json.RawMessage(`{"record_type":"A"}`),
			wantErr:     true,
		},
		{
			name:        "valid grpc config",
			monitorType: models.MonitorTypeGRPC,
			config:      json.RawMessage(`{"host":"grpc.example.com","port":443,"use_tls":true}`),
			wantErr:     false,
		},
		{
			name:        "invalid grpc config - missing host",
			monitorType: models.MonitorTypeGRPC,
			config:      json.RawMessage(`{"port":443}`),
			wantErr:     true,
		},
		{
			name:        "valid group config",
			monitorType: models.MonitorTypeGroup,
			config:      json.RawMessage(`{"monitor_ids":["id1","id2"]}`),
			wantErr:     false,
		},
		{
			name:        "invalid group config - empty monitors",
			monitorType: models.MonitorTypeGroup,
			config:      json.RawMessage(`{"monitor_ids":[]}`),
			wantErr:     true,
		},
		{
			name:        "valid synthetic api config",
			monitorType: models.MonitorTypeSyntheticAPI,
			config:      json.RawMessage(`{"base_url":"https://example.com","steps":[{"id":"s1","request":{"method":"GET","url":"/health"}}]}`),
			wantErr:     false,
		},
		{
			name:        "valid synthetic browser config",
			monitorType: models.MonitorTypeSyntheticBrowser,
			config:      json.RawMessage(`{"start_url":"https://example.com","steps":[{"id":"s1","action":"goto","url":"https://example.com"}]}`),
			wantErr:     false,
		},
		{
			name:        "unknown monitor type",
			monitorType: models.MonitorType("unknown"),
			config:      json.RawMessage(`{}`),
			wantErr:     true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := registry.Validate(tt.monitorType, tt.config)
			if (err != nil) != tt.wantErr {
				t.Errorf("Validate() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

func TestValidatorRegistry_Has(t *testing.T) {
	registry := NewDefaultValidatorRegistry()

	if !registry.Has(models.MonitorTypeHTTP) {
		t.Error("Expected Has(HTTP) to return true")
	}

	if !registry.Has(models.MonitorTypePing) {
		t.Error("Expected Has(Ping) to return true")
	}

	if !registry.Has(models.MonitorTypeGroup) {
		t.Error("Expected Has(Group) to return true")
	}

	if !registry.Has(models.MonitorTypeDNS) {
		t.Error("Expected Has(DNS) to return true")
	}

	if !registry.Has(models.MonitorTypeGRPC) {
		t.Error("Expected Has(GRPC) to return true")
	}

	if !registry.Has(models.MonitorTypeAgent) {
		t.Error("Expected Has(Agent) to return true")
	}

	if !registry.Has(models.MonitorTypePush) {
		t.Error("Expected Has(Push) to return true")
	}

	if !registry.Has(models.MonitorTypeSIP) {
		t.Error("Expected Has(SIP) to return true")
	}

	if !registry.Has(models.MonitorTypeSyntheticAPI) {
		t.Error("Expected Has(SyntheticAPI) to return true")
	}

	if !registry.Has(models.MonitorTypeSyntheticBrowser) {
		t.Error("Expected Has(SyntheticBrowser) to return true")
	}

	if registry.Has(models.MonitorType("unknown")) {
		t.Error("Expected Has(unknown) to return false")
	}
}

func TestValidatorRegistry_Types(t *testing.T) {
	registry := NewDefaultValidatorRegistry()
	types := registry.Types()

	// HTTP, Ping, DNS, GRPC, Group, Agent, Push, SIP, Synthetic API, Synthetic Browser,
	// Redis, Postgres, MongoDB, RabbitMQ = 14 types
	if len(types) != 14 {
		t.Errorf("Expected 14 types, got %d", len(types))
	}
}

func TestHTTPConfigValidator(t *testing.T) {
	validator := &HTTPConfigValidator{}

	tests := []struct {
		name    string
		config  json.RawMessage
		wantErr bool
	}{
		{
			name:    "valid config",
			config:  json.RawMessage(`{"url":"https://example.com","method":"GET"}`),
			wantErr: false,
		},
		{
			name:    "valid config with advanced checks",
			config:  json.RawMessage(`{"url":"https://example.com","method":"GET","expected_status_classes":["2xx"],"max_latency_ms":500,"body_assertions":[{"op":"contains","value":"OK"}],"response_header_assertions":[{"name":"Content-Type","op":"contains","value":"application/json"}],"json_assertions":[{"path":"status","op":"equals","value":"ok"}],"follow_redirects":false,"max_redirects":0,"tls_skip_verify":true,"tls_min_days_valid":0}`),
			wantErr: false,
		},
		{
			name:    "missing URL",
			config:  json.RawMessage(`{"method":"GET"}`),
			wantErr: true,
		},
		{
			name:    "missing method",
			config:  json.RawMessage(`{"url":"https://example.com"}`),
			wantErr: true,
		},
		{
			name:    "invalid method",
			config:  json.RawMessage(`{"url":"https://example.com","method":"INVALID"}`),
			wantErr: true,
		},
		{
			name:    "invalid URL scheme",
			config:  json.RawMessage(`{"url":"ftp://example.com","method":"GET"}`),
			wantErr: true,
		},
		{
			name:    "invalid expected_statuses",
			config:  json.RawMessage(`{"url":"https://example.com","method":"GET","expected_statuses":[99]}`),
			wantErr: true,
		},
		{
			name:    "invalid expected_status_ranges",
			config:  json.RawMessage(`{"url":"https://example.com","method":"GET","expected_status_ranges":[{"min":300,"max":200}]}`),
			wantErr: true,
		},
		{
			name:    "invalid expected_status_classes",
			config:  json.RawMessage(`{"url":"https://example.com","method":"GET","expected_status_classes":["9xx"]}`),
			wantErr: true,
		},
		{
			name:    "invalid body_assertions op",
			config:  json.RawMessage(`{"url":"https://example.com","method":"GET","body_assertions":[{"op":"bad","value":"x"}]}`),
			wantErr: true,
		},
		{
			name:    "header assertion missing value",
			config:  json.RawMessage(`{"url":"https://example.com","method":"GET","response_header_assertions":[{"name":"Content-Type","op":"equals"}]}`),
			wantErr: true,
		},
		{
			name:    "json number op with non-number value",
			config:  json.RawMessage(`{"url":"https://example.com","method":"GET","json_assertions":[{"path":"latency","op":"number_gt","value":"abc"}]}`),
			wantErr: true,
		},
		{
			name:    "negative max_redirects",
			config:  json.RawMessage(`{"url":"https://example.com","method":"GET","max_redirects":-1}`),
			wantErr: true,
		},
		{
			name:    "tls_min_days_valid requires https",
			config:  json.RawMessage(`{"url":"http://example.com","method":"GET","tls_min_days_valid":14}`),
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validator.ValidateConfig(tt.config)
			if (err != nil) != tt.wantErr {
				t.Errorf("ValidateConfig() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

func TestPingConfigValidator(t *testing.T) {
	validator := &PingConfigValidator{}

	tests := []struct {
		name    string
		config  json.RawMessage
		wantErr bool
	}{
		{
			name:    "valid hostname",
			config:  json.RawMessage(`{"host":"example.com"}`),
			wantErr: false,
		},
		{
			name:    "valid IP",
			config:  json.RawMessage(`{"host":"192.168.1.1"}`),
			wantErr: false,
		},
		{
			name:    "missing host",
			config:  json.RawMessage(`{}`),
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validator.ValidateConfig(tt.config)
			if (err != nil) != tt.wantErr {
				t.Errorf("ValidateConfig() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}
