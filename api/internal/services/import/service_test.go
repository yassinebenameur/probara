package importservice

import (
	"encoding/json"
	"testing"

	"github.com/yassinebenameur/probara/api/internal/models"
)

func TestBuildGRPCConfig_DefaultsAndOverrides(t *testing.T) {
	svc := &Service{}

	t.Run("defaults to tls and 443", func(t *testing.T) {
		row := models.ImportRow{Fields: map[string]interface{}{"host": "grpc.example.com"}}
		mapping := models.FieldMapping{Host: "host"}

		raw, err := svc.buildGRPCConfig(row, mapping)
		if err != nil {
			t.Fatalf("buildGRPCConfig() error = %v", err)
		}

		var cfg map[string]interface{}
		if err := json.Unmarshal(raw, &cfg); err != nil {
			t.Fatalf("failed to unmarshal config: %v", err)
		}

		if cfg["host"] != "grpc.example.com" {
			t.Fatalf("host = %v, want grpc.example.com", cfg["host"])
		}
		if cfg["use_tls"] != true {
			t.Fatalf("use_tls = %v, want true", cfg["use_tls"])
		}
		if cfg["port"] != float64(443) {
			t.Fatalf("port = %v, want 443", cfg["port"])
		}
	})

	t.Run("plaintext defaults to port 80", func(t *testing.T) {
		row := models.ImportRow{Fields: map[string]interface{}{"host": "grpc.example.com", "use_tls": false}}
		mapping := models.FieldMapping{Host: "host", UseTLS: "use_tls"}

		raw, err := svc.buildGRPCConfig(row, mapping)
		if err != nil {
			t.Fatalf("buildGRPCConfig() error = %v", err)
		}

		var cfg map[string]interface{}
		if err := json.Unmarshal(raw, &cfg); err != nil {
			t.Fatalf("failed to unmarshal config: %v", err)
		}

		if cfg["use_tls"] != false {
			t.Fatalf("use_tls = %v, want false", cfg["use_tls"])
		}
		if cfg["port"] != float64(80) {
			t.Fatalf("port = %v, want 80", cfg["port"])
		}
	})

	t.Run("extracts host and tls from url", func(t *testing.T) {
		row := models.ImportRow{Fields: map[string]interface{}{"url": "grpcs://grpc.example.com:7443/health"}}
		mapping := models.FieldMapping{URL: "url"}

		raw, err := svc.buildGRPCConfig(row, mapping)
		if err != nil {
			t.Fatalf("buildGRPCConfig() error = %v", err)
		}

		var cfg map[string]interface{}
		if err := json.Unmarshal(raw, &cfg); err != nil {
			t.Fatalf("failed to unmarshal config: %v", err)
		}

		if cfg["host"] != "grpc.example.com" {
			t.Fatalf("host = %v, want grpc.example.com", cfg["host"])
		}
		if cfg["port"] != float64(7443) {
			t.Fatalf("port = %v, want 7443", cfg["port"])
		}
		if cfg["use_tls"] != true {
			t.Fatalf("use_tls = %v, want true", cfg["use_tls"])
		}
	})

	t.Run("invalid use_tls value returns error", func(t *testing.T) {
		row := models.ImportRow{Fields: map[string]interface{}{"host": "grpc.example.com", "use_tls": "sometimes"}}
		mapping := models.FieldMapping{Host: "host", UseTLS: "use_tls"}

		_, err := svc.buildGRPCConfig(row, mapping)
		if err == nil {
			t.Fatal("expected error for invalid use_tls value")
		}
	})
}

func TestDetectAndSuggestTypes_GRPC(t *testing.T) {
	svc := &Service{}
	rows := []models.ImportRow{
		{Fields: map[string]interface{}{"type": "grpc"}},
		{Fields: map[string]interface{}{"type": "grpcs"}},
		{Fields: map[string]interface{}{"type": "grpc_health"}},
	}
	mapping := models.FieldMapping{Type: "type"}

	detected, suggested := svc.detectAndSuggestTypes(rows, mapping)

	if len(detected) != 3 {
		t.Fatalf("detected types len = %d, want 3", len(detected))
	}
	if suggested["grpcs"] != "grpc" {
		t.Fatalf("expected grpcs -> grpc mapping, got %q", suggested["grpcs"])
	}
	if suggested["grpc_health"] != "grpc" {
		t.Fatalf("expected grpc_health -> grpc mapping, got %q", suggested["grpc_health"])
	}
}

func TestSuggestMapping_GRPCFields(t *testing.T) {
	svc := &Service{}
	mapping := svc.suggestMapping([]string{"name", "type", "host", "port", "service", "use_tls"})

	if mapping.Port != "port" {
		t.Fatalf("Port mapping = %q, want port", mapping.Port)
	}
	if mapping.Service != "service" {
		t.Fatalf("Service mapping = %q, want service", mapping.Service)
	}
	if mapping.UseTLS != "use_tls" {
		t.Fatalf("UseTLS mapping = %q, want use_tls", mapping.UseTLS)
	}
}
