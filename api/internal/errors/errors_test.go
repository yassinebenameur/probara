package errors

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestWriteError(t *testing.T) {
	recorder := httptest.NewRecorder()
	WriteError(recorder, http.StatusBadRequest, "validation_error", "bad request")

	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("expected status 400, got %d", recorder.Code)
	}
	if recorder.Header().Get("Content-Type") != "application/json" {
		t.Fatalf("expected application/json content type")
	}

	var response ErrorResponse
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}
	if response.Error != "validation_error" || response.Message != "bad request" {
		t.Fatalf("unexpected response: %#v", response)
	}
}

func TestWriteErrorHelpers(t *testing.T) {
	tests := []struct {
		name       string
		writeFn    func(http.ResponseWriter, string)
		statusCode int
		errorType  string
	}{
		{"validation", WriteValidationError, http.StatusBadRequest, "validation_error"},
		{"unauthorized", WriteUnauthorizedError, http.StatusUnauthorized, "unauthorized"},
		{"not_found", WriteNotFoundError, http.StatusNotFound, "not_found"},
		{"internal", WriteInternalError, http.StatusInternalServerError, "internal_error"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			recorder := httptest.NewRecorder()
			test.writeFn(recorder, "message")

			if recorder.Code != test.statusCode {
				t.Fatalf("expected status %d, got %d", test.statusCode, recorder.Code)
			}

			var response ErrorResponse
			if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
				t.Fatalf("failed to decode response: %v", err)
			}
			if response.Error != test.errorType {
				t.Fatalf("expected error type %s, got %s", test.errorType, response.Error)
			}
		})
	}
}
