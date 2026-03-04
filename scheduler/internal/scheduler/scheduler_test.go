package scheduler

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/yassinebenameur/probara/shared/models"
)

func TestCreateCheckJob(t *testing.T) {
	// createCheckJob is a method on Scheduler but only uses the monitor parameter
	// We create a minimal scheduler to call the method
	s := &Scheduler{}

	monitorID := uuid.New()
	tenantID := uuid.New()
	config := []byte(`{"url":"https://example.com","method":"GET"}`)

	monitor := Monitor{
		ID:              monitorID,
		TenantID:        tenantID,
		Type:            "http",
		Config:          config,
		IntervalSeconds: 60,
		TimeoutSeconds:  30,
	}

	job, err := s.createCheckJob(monitor)
	if err != nil {
		t.Fatalf("createCheckJob() error = %v", err)
	}

	// Verify job ID is set (should be a valid UUID)
	if job.ID == "" {
		t.Error("Job ID should not be empty")
	}
	if _, err := uuid.Parse(job.ID); err != nil {
		t.Errorf("Job ID should be a valid UUID, got %q", job.ID)
	}

	// Verify tenant ID matches
	if job.TenantID != tenantID.String() {
		t.Errorf("Job TenantID = %v, want %v", job.TenantID, tenantID.String())
	}

	// Verify job type
	if job.Type != models.JobTypeCheck {
		t.Errorf("Job Type = %v, want %v", job.Type, models.JobTypeCheck)
	}

	// Verify version
	if job.Version != "v1" {
		t.Errorf("Job Version = %v, want v1", job.Version)
	}

	// Verify payload contains expected fields
	var payload models.CheckJobPayload
	if err := json.Unmarshal(job.Payload, &payload); err != nil {
		t.Fatalf("Failed to unmarshal payload: %v", err)
	}

	if payload.MonitorID != monitorID.String() {
		t.Errorf("Payload MonitorID = %v, want %v", payload.MonitorID, monitorID.String())
	}

	if payload.Type != "http" {
		t.Errorf("Payload Type = %v, want http", payload.Type)
	}

	if payload.TimeoutSeconds != 30 {
		t.Errorf("Payload TimeoutSeconds = %v, want 30", payload.TimeoutSeconds)
	}

	// Verify config is preserved
	if string(payload.Config) != string(config) {
		t.Errorf("Payload Config = %v, want %v", string(payload.Config), string(config))
	}
}

func TestCreateCheckJob_DeadlineCalculation(t *testing.T) {
	s := &Scheduler{}

	tests := []struct {
		name           string
		timeoutSeconds int
		wantDeadline   time.Duration // expected deadline from now
	}{
		{"30s timeout gives 60s deadline", 30, 60 * time.Second},
		{"60s timeout gives 120s deadline", 60, 120 * time.Second},
		{"5s timeout gives 10s deadline", 5, 10 * time.Second},
		{"1s timeout gives 2s deadline", 1, 2 * time.Second},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			monitor := Monitor{
				ID:              uuid.New(),
				TenantID:        uuid.New(),
				Type:            "http",
				Config:          []byte(`{}`),
				IntervalSeconds: 60,
				TimeoutSeconds:  tt.timeoutSeconds,
			}

			beforeCreate := time.Now()
			job, err := s.createCheckJob(monitor)
			afterCreate := time.Now()

			if err != nil {
				t.Fatalf("createCheckJob() error = %v", err)
			}

			if job.Deadline == nil {
				t.Fatal("Job deadline should not be nil")
			}

			// Deadline should be approximately 2 * timeout from creation time
			expectedMinDeadline := beforeCreate.Add(tt.wantDeadline)
			expectedMaxDeadline := afterCreate.Add(tt.wantDeadline)

			if job.Deadline.Before(expectedMinDeadline) {
				t.Errorf("Deadline %v is before expected min %v", job.Deadline, expectedMinDeadline)
			}
			if job.Deadline.After(expectedMaxDeadline.Add(time.Second)) {
				t.Errorf("Deadline %v is after expected max %v", job.Deadline, expectedMaxDeadline)
			}
		})
	}
}

func TestCreateCheckJob_DifferentMonitorTypes(t *testing.T) {
	s := &Scheduler{}

	types := []struct {
		monitorType string
		config      []byte
	}{
		{"http", []byte(`{"url":"https://example.com","method":"GET"}`)},
		{"ping", []byte(`{"host":"example.com"}`)},
		{"grpc", []byte(`{"host":"grpc.example.com","port":443,"use_tls":true}`)},
		{"agent", []byte(`{"agent_id":"abc123"}`)},
		{"push", []byte(`{"push_token":"token123"}`)},
	}

	for _, tt := range types {
		t.Run(tt.monitorType, func(t *testing.T) {
			monitor := Monitor{
				ID:              uuid.New(),
				TenantID:        uuid.New(),
				Type:            tt.monitorType,
				Config:          tt.config,
				IntervalSeconds: 60,
				TimeoutSeconds:  30,
			}

			job, err := s.createCheckJob(monitor)
			if err != nil {
				t.Fatalf("createCheckJob() error = %v", err)
			}

			var payload models.CheckJobPayload
			if err := json.Unmarshal(job.Payload, &payload); err != nil {
				t.Fatalf("Failed to unmarshal payload: %v", err)
			}

			if payload.Type != tt.monitorType {
				t.Errorf("Payload Type = %v, want %v", payload.Type, tt.monitorType)
			}

			if string(payload.Config) != string(tt.config) {
				t.Errorf("Payload Config = %v, want %v", string(payload.Config), string(tt.config))
			}
		})
	}
}

func TestCreateCheckJob_UniqueJobIDs(t *testing.T) {
	s := &Scheduler{}

	monitor := Monitor{
		ID:              uuid.New(),
		TenantID:        uuid.New(),
		Type:            "http",
		Config:          []byte(`{}`),
		IntervalSeconds: 60,
		TimeoutSeconds:  30,
	}

	jobIDs := make(map[string]bool)
	numJobs := 100

	for i := 0; i < numJobs; i++ {
		job, err := s.createCheckJob(monitor)
		if err != nil {
			t.Fatalf("createCheckJob() error = %v", err)
		}

		if jobIDs[job.ID] {
			t.Errorf("Duplicate job ID generated: %s", job.ID)
		}
		jobIDs[job.ID] = true
	}

	if len(jobIDs) != numJobs {
		t.Errorf("Expected %d unique job IDs, got %d", numJobs, len(jobIDs))
	}
}

func TestCreateCheckJob_CreatedAtSet(t *testing.T) {
	s := &Scheduler{}

	monitor := Monitor{
		ID:              uuid.New(),
		TenantID:        uuid.New(),
		Type:            "http",
		Config:          []byte(`{}`),
		IntervalSeconds: 60,
		TimeoutSeconds:  30,
	}

	before := time.Now()
	job, err := s.createCheckJob(monitor)
	after := time.Now()

	if err != nil {
		t.Fatalf("createCheckJob() error = %v", err)
	}

	if job.CreatedAt.Before(before) || job.CreatedAt.After(after) {
		t.Errorf("Job CreatedAt %v should be between %v and %v", job.CreatedAt, before, after)
	}
}

func TestMonitor_Struct(t *testing.T) {
	// Test Monitor struct can be properly constructed
	id := uuid.New()
	tenantID := uuid.New()
	config := []byte(`{"url":"https://example.com"}`)

	m := Monitor{
		ID:              id,
		TenantID:        tenantID,
		Type:            "http",
		Config:          config,
		IntervalSeconds: 60,
		TimeoutSeconds:  30,
	}

	if m.ID != id {
		t.Errorf("Monitor ID = %v, want %v", m.ID, id)
	}
	if m.TenantID != tenantID {
		t.Errorf("Monitor TenantID = %v, want %v", m.TenantID, tenantID)
	}
	if m.Type != "http" {
		t.Errorf("Monitor Type = %v, want http", m.Type)
	}
	if string(m.Config) != string(config) {
		t.Errorf("Monitor Config = %v, want %v", string(m.Config), string(config))
	}
	if m.IntervalSeconds != 60 {
		t.Errorf("Monitor IntervalSeconds = %v, want 60", m.IntervalSeconds)
	}
	if m.TimeoutSeconds != 30 {
		t.Errorf("Monitor TimeoutSeconds = %v, want 30", m.TimeoutSeconds)
	}
}
