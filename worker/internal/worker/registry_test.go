package worker

import (
	"context"
	"encoding/json"
	"testing"
)

// MockChecker is a mock implementation of the Checker interface for testing
type MockChecker struct {
	result CheckResult
}

func (m *MockChecker) Check(ctx context.Context, config json.RawMessage, timeoutSeconds int) CheckResult {
	return m.result
}

func TestCheckerRegistry_Register(t *testing.T) {
	registry := NewCheckerRegistry()
	mockChecker := &MockChecker{result: CheckResult{Status: "success"}}

	registry.Register("test", mockChecker)

	if !registry.Has("test") {
		t.Error("Expected registry to have 'test' checker")
	}
}

func TestCheckerRegistry_Get(t *testing.T) {
	registry := NewCheckerRegistry()
	mockChecker := &MockChecker{result: CheckResult{Status: "success"}}

	registry.Register("test", mockChecker)

	tests := []struct {
		name        string
		monitorType string
		wantErr     bool
	}{
		{
			name:        "existing checker",
			monitorType: "test",
			wantErr:     false,
		},
		{
			name:        "non-existent checker",
			monitorType: "unknown",
			wantErr:     true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			checker, err := registry.Get(tt.monitorType)
			if (err != nil) != tt.wantErr {
				t.Errorf("Get() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if !tt.wantErr && checker == nil {
				t.Error("Get() returned nil checker for existing type")
			}
		})
	}
}

func TestCheckerRegistry_Has(t *testing.T) {
	registry := NewCheckerRegistry()
	mockChecker := &MockChecker{}

	registry.Register("http", mockChecker)

	if !registry.Has("http") {
		t.Error("Expected Has('http') to return true")
	}

	if registry.Has("unknown") {
		t.Error("Expected Has('unknown') to return false")
	}
}

func TestCheckerRegistry_Types(t *testing.T) {
	registry := NewCheckerRegistry()
	registry.Register("http", &MockChecker{})
	registry.Register("ping", &MockChecker{})

	types := registry.Types()

	if len(types) != 2 {
		t.Errorf("Expected 2 types, got %d", len(types))
	}

	// Check both types are present
	typeMap := make(map[string]bool)
	for _, t := range types {
		typeMap[t] = true
	}

	if !typeMap["http"] {
		t.Error("Expected 'http' in types")
	}
	if !typeMap["ping"] {
		t.Error("Expected 'ping' in types")
	}
}

func TestNewDefaultRegistry(t *testing.T) {
	registry := NewDefaultRegistry(1024, false, nil, t.TempDir())

	// Should have HTTP and Ping checkers
	if !registry.Has("http") {
		t.Error("Default registry should have 'http' checker")
	}

	if !registry.Has("ping") {
		t.Error("Default registry should have 'ping' checker")
	}

	if !registry.Has("dns") {
		t.Error("Default registry should have 'dns' checker")
	}

	if !registry.Has("synthetic_api") {
		t.Error("Default registry should have 'synthetic_api' checker")
	}

	if !registry.Has("synthetic_browser") {
		t.Error("Default registry should have 'synthetic_browser' checker")
	}
}
