package auth

import (
	"net/http"
	"testing"
)

func TestHashAPIKey(t *testing.T) {
	key := "test-api-key-12345"
	hashResult, err := HashAPIKey(key)
	if err != nil {
		t.Fatalf("Failed to hash API key: %v", err)
	}

	if hashResult == nil {
		t.Fatal("Hash result should not be nil")
	}

	if hashResult.BcryptHash == "" {
		t.Error("Bcrypt hash should not be empty")
	}

	if hashResult.KeyPrefix == "" {
		t.Error("Key prefix should not be empty")
	}

	if hashResult.BcryptHash == key {
		t.Error("Hash should not equal the original key")
	}
}

func TestCompareAPIKey(t *testing.T) {
	key := "test-api-key-12345"
	hashResult, err := HashAPIKey(key)
	if err != nil {
		t.Fatalf("Failed to hash API key: %v", err)
	}

	// Test correct key
	match, err := CompareAPIKey(hashResult.BcryptHash, key)
	if err != nil {
		t.Fatalf("Failed to compare API key: %v", err)
	}
	if !match {
		t.Error("Correct key should match")
	}

	// Test incorrect key
	match, err = CompareAPIKey(hashResult.BcryptHash, "wrong-key")
	if err != nil {
		t.Fatalf("Failed to compare API key: %v", err)
	}
	if match {
		t.Error("Incorrect key should not match")
	}
}

func TestExtractAPIKey(t *testing.T) {
	tests := []struct {
		name        string
		header      string
		expectError bool
		expectedKey string
	}{
		{
			name:        "valid bearer token",
			header:      "Bearer test-key-123",
			expectError: false,
			expectedKey: "test-key-123",
		},
		{
			name:        "missing header",
			header:      "",
			expectError: true,
		},
		{
			name:        "invalid format",
			header:      "Invalid test-key",
			expectError: true,
		},
		{
			name:        "empty token",
			header:      "Bearer ",
			expectError: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req, _ := http.NewRequest("GET", "/", nil)
			if tt.header != "" {
				req.Header.Set("Authorization", tt.header)
			}
			key, err := ExtractAPIKey(req)

			if tt.expectError {
				if err == nil {
					t.Error("Expected error but got none")
				}
			} else {
				if err != nil {
					t.Errorf("Unexpected error: %v", err)
				}
				if key != tt.expectedKey {
					t.Errorf("Expected key %q, got %q", tt.expectedKey, key)
				}
			}
		})
	}
}
