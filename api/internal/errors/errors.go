package errors

import (
	"encoding/json"
	"net/http"
)

// ErrorResponse represents a standardized error response
type ErrorResponse struct {
	Error   string `json:"error"`
	Message string `json:"message"`
}

// WriteError writes an error response to the HTTP response writer
func WriteError(w http.ResponseWriter, statusCode int, errorType, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(statusCode)

	response := ErrorResponse{
		Error:   errorType,
		Message: message,
	}

	json.NewEncoder(w).Encode(response)
}

// WriteValidationError writes a 400 validation error
func WriteValidationError(w http.ResponseWriter, message string) {
	WriteError(w, http.StatusBadRequest, "validation_error", message)
}

// WriteUnauthorizedError writes a 401 unauthorized error
func WriteUnauthorizedError(w http.ResponseWriter, message string) {
	WriteError(w, http.StatusUnauthorized, "unauthorized", message)
}

// WriteForbiddenError writes a 403 forbidden error
func WriteForbiddenError(w http.ResponseWriter, message string) {
	WriteError(w, http.StatusForbidden, "forbidden", message)
}

// WriteNotFoundError writes a 404 not found error
func WriteNotFoundError(w http.ResponseWriter, message string) {
	WriteError(w, http.StatusNotFound, "not_found", message)
}

// WriteInternalError writes a 500 internal server error
func WriteInternalError(w http.ResponseWriter, message string) {
	WriteError(w, http.StatusInternalServerError, "internal_error", message)
}
