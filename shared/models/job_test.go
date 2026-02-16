package models

import (
	"encoding/json"
	"testing"
	"time"
)

func TestNewJob(t *testing.T) {
	payload := json.RawMessage(`{"key": "value"}`)

	before := time.Now()
	job := NewJob("job-123", "tenant-456", JobTypeCheck, "v1", payload)
	after := time.Now()

	if job.ID != "job-123" {
		t.Errorf("NewJob() ID = %v, want job-123", job.ID)
	}

	if job.TenantID != "tenant-456" {
		t.Errorf("NewJob() TenantID = %v, want tenant-456", job.TenantID)
	}

	if job.Type != JobTypeCheck {
		t.Errorf("NewJob() Type = %v, want %v", job.Type, JobTypeCheck)
	}

	if job.Version != "v1" {
		t.Errorf("NewJob() Version = %v, want v1", job.Version)
	}

	if string(job.Payload) != string(payload) {
		t.Errorf("NewJob() Payload = %v, want %v", string(job.Payload), string(payload))
	}

	// CreatedAt should be set to current time
	if job.CreatedAt.Before(before) || job.CreatedAt.After(after) {
		t.Errorf("NewJob() CreatedAt = %v, should be between %v and %v", job.CreatedAt, before, after)
	}

	// Deadline should be nil by default
	if job.Deadline != nil {
		t.Errorf("NewJob() Deadline = %v, want nil", job.Deadline)
	}
}

func TestNewJob_DifferentTypes(t *testing.T) {
	tests := []struct {
		name     string
		jobType  JobType
		wantType JobType
	}{
		{"check job", JobTypeCheck, JobTypeCheck},
		{"alert job", JobTypeAlert, JobTypeAlert},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			job := NewJob("id", "tenant", tt.jobType, "v1", nil)
			if job.Type != tt.wantType {
				t.Errorf("NewJob() Type = %v, want %v", job.Type, tt.wantType)
			}
		})
	}
}

func TestJob_WithDeadline(t *testing.T) {
	job := NewJob("job-123", "tenant-456", JobTypeCheck, "v1", nil)

	// Initially no deadline
	if job.Deadline != nil {
		t.Errorf("Initial Deadline should be nil")
	}

	deadline := time.Now().Add(1 * time.Hour)
	updatedJob := job.WithDeadline(deadline)

	// WithDeadline returns the same job (mutates in place)
	if updatedJob != job {
		t.Errorf("WithDeadline() should return the same job instance")
	}

	// Deadline should be set
	if job.Deadline == nil {
		t.Fatal("Deadline should not be nil after WithDeadline()")
	}

	if !job.Deadline.Equal(deadline) {
		t.Errorf("Deadline = %v, want %v", *job.Deadline, deadline)
	}
}

func TestJob_WithDeadline_Chaining(t *testing.T) {
	deadline := time.Now().Add(30 * time.Minute)

	job := NewJob("id", "tenant", JobTypeCheck, "v1", nil).WithDeadline(deadline)

	if job.Deadline == nil {
		t.Fatal("Deadline should not be nil")
	}
	if !job.Deadline.Equal(deadline) {
		t.Errorf("Deadline = %v, want %v", *job.Deadline, deadline)
	}
}

func TestJob_IsExpired_NoDeadline(t *testing.T) {
	job := NewJob("job-123", "tenant-456", JobTypeCheck, "v1", nil)

	// Job without deadline should never be expired
	if job.IsExpired() {
		t.Error("Job without deadline should not be expired")
	}
}

func TestJob_IsExpired_FutureDeadline(t *testing.T) {
	job := NewJob("job-123", "tenant-456", JobTypeCheck, "v1", nil)
	job.WithDeadline(time.Now().Add(1 * time.Hour))

	if job.IsExpired() {
		t.Error("Job with future deadline should not be expired")
	}
}

func TestJob_IsExpired_PastDeadline(t *testing.T) {
	job := NewJob("job-123", "tenant-456", JobTypeCheck, "v1", nil)
	job.WithDeadline(time.Now().Add(-1 * time.Hour))

	if !job.IsExpired() {
		t.Error("Job with past deadline should be expired")
	}
}

func TestJob_IsExpired_JustExpired(t *testing.T) {
	job := NewJob("job-123", "tenant-456", JobTypeCheck, "v1", nil)
	// Set deadline to 1 millisecond ago
	job.WithDeadline(time.Now().Add(-1 * time.Millisecond))

	// Small sleep to ensure we're past the deadline
	time.Sleep(2 * time.Millisecond)

	if !job.IsExpired() {
		t.Error("Job just past deadline should be expired")
	}
}

func TestJob_IsExpired_AtExactDeadline(t *testing.T) {
	// Set deadline to right now
	now := time.Now()
	job := NewJob("job-123", "tenant-456", JobTypeCheck, "v1", nil)
	job.WithDeadline(now)

	// At the exact deadline, time.Now().After(deadline) should be false
	// because After is strictly greater than, not greater than or equal
	// So right at the deadline, the job is NOT expired yet
	// However, in practice by the time IsExpired runs, time has moved forward
	// So we test that after waiting a bit, it is expired
	time.Sleep(1 * time.Millisecond)

	if !job.IsExpired() {
		t.Error("Job should be expired after deadline passes")
	}
}

func TestJob_Payload_NilHandling(t *testing.T) {
	job := NewJob("id", "tenant", JobTypeCheck, "v1", nil)

	if job.Payload != nil {
		t.Errorf("Payload should be nil when created with nil")
	}
}

func TestJob_Payload_EmptyJSON(t *testing.T) {
	payload := json.RawMessage(`{}`)
	job := NewJob("id", "tenant", JobTypeCheck, "v1", payload)

	if string(job.Payload) != "{}" {
		t.Errorf("Payload = %v, want {}", string(job.Payload))
	}
}

func TestJob_Payload_ComplexJSON(t *testing.T) {
	payload := json.RawMessage(`{"monitor_id":"abc","type":"http","config":{"url":"https://example.com"},"timeout_seconds":30}`)
	job := NewJob("id", "tenant", JobTypeCheck, "v1", payload)

	// Verify payload can be unmarshaled
	var p CheckJobPayload
	if err := json.Unmarshal(job.Payload, &p); err != nil {
		t.Fatalf("Failed to unmarshal payload: %v", err)
	}

	if p.MonitorID != "abc" {
		t.Errorf("MonitorID = %v, want abc", p.MonitorID)
	}
	if p.Type != "http" {
		t.Errorf("Type = %v, want http", p.Type)
	}
	if p.TimeoutSeconds != 30 {
		t.Errorf("TimeoutSeconds = %v, want 30", p.TimeoutSeconds)
	}
}

func TestJobType_Constants(t *testing.T) {
	if JobTypeCheck != "check" {
		t.Errorf("JobTypeCheck = %v, want check", JobTypeCheck)
	}

	if JobTypeAlert != "alert" {
		t.Errorf("JobTypeAlert = %v, want alert", JobTypeAlert)
	}
}

func TestJob_JSON_Serialization(t *testing.T) {
	deadline := time.Date(2024, 1, 15, 10, 30, 0, 0, time.UTC)
	job := &Job{
		ID:        "job-123",
		TenantID:  "tenant-456",
		Type:      JobTypeCheck,
		Version:   "v1",
		Payload:   json.RawMessage(`{"test": true}`),
		Deadline:  &deadline,
		CreatedAt: time.Date(2024, 1, 15, 10, 0, 0, 0, time.UTC),
	}

	data, err := json.Marshal(job)
	if err != nil {
		t.Fatalf("Failed to marshal job: %v", err)
	}

	var decoded Job
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("Failed to unmarshal job: %v", err)
	}

	if decoded.ID != job.ID {
		t.Errorf("Decoded ID = %v, want %v", decoded.ID, job.ID)
	}
	if decoded.TenantID != job.TenantID {
		t.Errorf("Decoded TenantID = %v, want %v", decoded.TenantID, job.TenantID)
	}
	if decoded.Type != job.Type {
		t.Errorf("Decoded Type = %v, want %v", decoded.Type, job.Type)
	}
	if decoded.Version != job.Version {
		t.Errorf("Decoded Version = %v, want %v", decoded.Version, job.Version)
	}
	if decoded.Deadline == nil || !decoded.Deadline.Equal(*job.Deadline) {
		t.Errorf("Decoded Deadline = %v, want %v", decoded.Deadline, job.Deadline)
	}
}

func TestJob_JSON_NilDeadline(t *testing.T) {
	job := NewJob("id", "tenant", JobTypeCheck, "v1", nil)

	data, err := json.Marshal(job)
	if err != nil {
		t.Fatalf("Failed to marshal job: %v", err)
	}

	var decoded Job
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("Failed to unmarshal job: %v", err)
	}

	if decoded.Deadline != nil {
		t.Errorf("Decoded Deadline should be nil, got %v", decoded.Deadline)
	}
}
