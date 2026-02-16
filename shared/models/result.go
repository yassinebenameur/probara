package models

import (
	"encoding/json"
	"time"
)

// ResultStatus represents the status of a result
type ResultStatus string

const (
	ResultStatusSuccess ResultStatus = "success"
	ResultStatusFailure ResultStatus = "failure"
	ResultStatusError   ResultStatus = "error"
)

// Result represents a generic result envelope
type Result struct {
	ID          string          `json:"id"`
	JobID       string          `json:"job_id"`
	Status      ResultStatus    `json:"status"`
	Version     string          `json:"version"`
	Data        json.RawMessage `json:"data,omitempty"`
	Error       string          `json:"error,omitempty"`
	CreatedAt   time.Time       `json:"created_at"`
	CompletedAt *time.Time      `json:"completed_at,omitempty"`
}

// NewResult creates a new result with the given parameters
func NewResult(id, jobID string, status ResultStatus, version string) *Result {
	now := time.Now()
	return &Result{
		ID:        id,
		JobID:     jobID,
		Status:    status,
		Version:   version,
		CreatedAt: now,
	}
}

// WithData sets the data field of the result
// Accepts json.RawMessage for type safety, or marshals other types to JSON
func (r *Result) WithData(data json.RawMessage) *Result {
	r.Data = data
	return r
}

// WithDataJSON marshals the provided value to JSON and sets it as the data field
func (r *Result) WithDataJSON(data interface{}) (*Result, error) {
	jsonData, err := json.Marshal(data)
	if err != nil {
		return r, err
	}
	r.Data = jsonData
	return r, nil
}

// WithError sets the error field of the result
func (r *Result) WithError(err string) *Result {
	r.Error = err
	r.Status = ResultStatusError
	return r
}

// Complete marks the result as completed
func (r *Result) Complete() {
	now := time.Now()
	r.CompletedAt = &now
}
