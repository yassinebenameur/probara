package models

import (
	"encoding/json"
	"time"
)

// JobType represents the type of job
type JobType string

const (
	JobTypeCheck JobType = "check"
	JobTypeAlert JobType = "alert"
)

// Job represents a generic job envelope
type Job struct {
	ID        string          `json:"id"`
	TenantID  string          `json:"tenant_id"`
	Type      JobType         `json:"type"`
	Version   string          `json:"version"`
	Payload   json.RawMessage `json:"payload"`
	Deadline  *time.Time      `json:"deadline,omitempty"`
	CreatedAt time.Time       `json:"created_at"`
}

// NewJob creates a new job with the given parameters
func NewJob(id, tenantID string, jobType JobType, version string, payload json.RawMessage) *Job {
	now := time.Now()
	return &Job{
		ID:        id,
		TenantID:  tenantID,
		Type:      jobType,
		Version:   version,
		Payload:   payload,
		CreatedAt: now,
	}
}

// WithDeadline sets the deadline for the job
func (j *Job) WithDeadline(deadline time.Time) *Job {
	j.Deadline = &deadline
	return j
}

// IsExpired checks if the job has passed its deadline
func (j *Job) IsExpired() bool {
	if j.Deadline == nil {
		return false
	}
	return time.Now().After(*j.Deadline)
}
