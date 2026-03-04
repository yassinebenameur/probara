package validation

import (
	"encoding/json"
	"testing"
)

func TestGRPCConfigValidator(t *testing.T) {
	validator := &GRPCConfigValidator{}

	tests := []struct {
		name    string
		config  json.RawMessage
		wantErr bool
	}{
		{
			name:    "valid minimal config",
			config:  json.RawMessage(`{"host":"grpc.example.com"}`),
			wantErr: false,
		},
		{
			name:    "valid config with full fields",
			config:  json.RawMessage(`{"host":"127.0.0.1","port":50051,"service":"my.service.Health","use_tls":false}`),
			wantErr: false,
		},
		{
			name:    "missing host",
			config:  json.RawMessage(`{"port":443}`),
			wantErr: true,
		},
		{
			name:    "invalid host",
			config:  json.RawMessage(`{"host":"bad host!"}`),
			wantErr: true,
		},
		{
			name:    "invalid port",
			config:  json.RawMessage(`{"host":"grpc.example.com","port":70000}`),
			wantErr: true,
		},
		{
			name:    "service cannot be blank",
			config:  json.RawMessage(`{"host":"grpc.example.com","service":"   "}`),
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
