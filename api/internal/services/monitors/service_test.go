package monitors

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/yassinebenameur/probara/api/internal/models"
)

// MockRepository implements Repository interface for testing
type MockRepository struct {
	monitors map[uuid.UUID]*models.Monitor
	members  map[uuid.UUID][]uuid.UUID
	policies map[uuid.UUID][]uuid.UUID
}

func NewMockRepository() *MockRepository {
	return &MockRepository{
		monitors: make(map[uuid.UUID]*models.Monitor),
		members:  make(map[uuid.UUID][]uuid.UUID),
		policies: make(map[uuid.UUID][]uuid.UUID),
	}
}

func (m *MockRepository) Create(ctx context.Context, monitor *models.Monitor) error {
	m.monitors[monitor.ID] = monitor
	return nil
}

func (m *MockRepository) GetByID(ctx context.Context, tenantID, monitorID uuid.UUID) (*models.Monitor, error) {
	monitor, ok := m.monitors[monitorID]
	if !ok || monitor.TenantID != tenantID {
		return nil, ErrMonitorNotFound
	}
	return monitor, nil
}

func (m *MockRepository) List(ctx context.Context, tenantID uuid.UUID, tag *string, enabled *bool, page, pageSize int) ([]models.Monitor, int, error) {
	var result []models.Monitor
	for _, monitor := range m.monitors {
		if monitor.TenantID == tenantID {
			result = append(result, *monitor)
		}
	}
	return result, len(result), nil
}

func (m *MockRepository) Update(ctx context.Context, monitor *models.Monitor, setParts []string, args []interface{}) error {
	m.monitors[monitor.ID] = monitor
	return nil
}

func (m *MockRepository) Delete(ctx context.Context, tenantID, monitorID uuid.UUID) error {
	if monitor, ok := m.monitors[monitorID]; ok && monitor.TenantID == tenantID {
		delete(m.monitors, monitorID)
		return nil
	}
	return ErrMonitorNotFound
}

func (m *MockRepository) VerifyAlertPolicy(ctx context.Context, tenantID, policyID uuid.UUID) error {
	return nil
}

func (m *MockRepository) SetAlertPolicies(ctx context.Context, monitorID uuid.UUID, policyIDs []uuid.UUID) error {
	m.policies[monitorID] = policyIDs
	return nil
}

func (m *MockRepository) GetAlertPolicyIDs(ctx context.Context, monitorID uuid.UUID) ([]uuid.UUID, error) {
	return m.policies[monitorID], nil
}

func (m *MockRepository) GetAlertPolicyIDsForMonitors(ctx context.Context, monitorIDs []uuid.UUID) (map[uuid.UUID][]uuid.UUID, error) {
	result := make(map[uuid.UUID][]uuid.UUID)
	for _, monitorID := range monitorIDs {
		result[monitorID] = m.policies[monitorID]
	}
	return result, nil
}

func (m *MockRepository) GetMemberIDs(ctx context.Context, groupID uuid.UUID) ([]uuid.UUID, error) {
	return m.members[groupID], nil
}

// ErrMonitorNotFound is returned when a monitor is not found
var ErrMonitorNotFound = &NotFoundError{msg: "monitor not found"}

type NotFoundError struct {
	msg string
}

func (e *NotFoundError) Error() string {
	return e.msg
}

func TestService_CreateMonitor(t *testing.T) {
	repo := NewMockRepository()
	service := &Service{repo: repo}

	tenantID := uuid.New()

	tests := []struct {
		name    string
		req     *models.CreateMonitorRequest
		wantErr bool
	}{
		{
			name: "valid HTTP monitor",
			req: &models.CreateMonitorRequest{
				Name:            "Test Monitor",
				Type:            models.MonitorTypeHTTP,
				Config:          json.RawMessage(`{"url":"https://example.com","method":"GET"}`),
				IntervalSeconds: 60,
				TimeoutSeconds:  30,
			},
			wantErr: false,
		},
		{
			name: "valid ping monitor",
			req: &models.CreateMonitorRequest{
				Name:            "Test Ping",
				Type:            models.MonitorTypePing,
				Config:          json.RawMessage(`{"host":"example.com"}`),
				IntervalSeconds: 60,
				TimeoutSeconds:  30,
			},
			wantErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			monitor, err := service.CreateMonitor(context.Background(), tenantID, tt.req)
			if (err != nil) != tt.wantErr {
				t.Errorf("CreateMonitor() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if !tt.wantErr {
				if monitor.Name != tt.req.Name {
					t.Errorf("CreateMonitor() name = %v, want %v", monitor.Name, tt.req.Name)
				}
				if monitor.Type != tt.req.Type {
					t.Errorf("CreateMonitor() type = %v, want %v", monitor.Type, tt.req.Type)
				}
			}
		})
	}
}

func TestService_GetMonitor(t *testing.T) {
	repo := NewMockRepository()
	service := &Service{repo: repo}

	tenantID := uuid.New()
	monitorID := uuid.New()

	// Create a monitor in the mock
	repo.monitors[monitorID] = &models.Monitor{
		ID:              monitorID,
		TenantID:        tenantID,
		Name:            "Test Monitor",
		Type:            models.MonitorTypeHTTP,
		IntervalSeconds: 60,
		TimeoutSeconds:  30,
		CreatedAt:       time.Now(),
		UpdatedAt:       time.Now(),
	}

	tests := []struct {
		name      string
		tenantID  uuid.UUID
		monitorID uuid.UUID
		wantErr   bool
	}{
		{
			name:      "existing monitor",
			tenantID:  tenantID,
			monitorID: monitorID,
			wantErr:   false,
		},
		{
			name:      "non-existent monitor",
			tenantID:  tenantID,
			monitorID: uuid.New(),
			wantErr:   true,
		},
		{
			name:      "wrong tenant",
			tenantID:  uuid.New(),
			monitorID: monitorID,
			wantErr:   true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			monitor, err := service.GetMonitor(context.Background(), tt.tenantID, tt.monitorID)
			if (err != nil) != tt.wantErr {
				t.Errorf("GetMonitor() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if !tt.wantErr && monitor.ID != tt.monitorID {
				t.Errorf("GetMonitor() id = %v, want %v", monitor.ID, tt.monitorID)
			}
		})
	}
}

func TestService_DeleteMonitor(t *testing.T) {
	repo := NewMockRepository()
	service := &Service{repo: repo}

	tenantID := uuid.New()
	monitorID := uuid.New()

	// Create a monitor in the mock
	repo.monitors[monitorID] = &models.Monitor{
		ID:       monitorID,
		TenantID: tenantID,
	}

	// Delete should succeed
	err := service.DeleteMonitor(context.Background(), tenantID, monitorID)
	if err != nil {
		t.Errorf("DeleteMonitor() error = %v", err)
	}

	// Monitor should be gone
	_, err = service.GetMonitor(context.Background(), tenantID, monitorID)
	if err == nil {
		t.Error("Expected error after deletion, got nil")
	}
}
