package monitors

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/yassinebenameur/probara/api/internal/models"
)

// MockRepository implements Repository interface for testing
type MockRepository struct {
	monitors          map[uuid.UUID]*models.Monitor
	members           map[uuid.UUID][]uuid.UUID
	policies          map[uuid.UUID][]uuid.UUID
	deletedHistoryIDs []uuid.UUID

	// Controllable error/return fields for bulk alert policy tests.
	verifyAlertPolicyErr error
	verifyMonitorsErr    error

	// Soft-delete tracking.
	bulkSoftDeleted   []uuid.UUID
	bulkSoftDeleteN   int64 // override return value; if zero, returns len(monitorIDs)
	bulkSoftDeleteErr error
	hardDeletedID     *uuid.UUID
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

func (m *MockRepository) DeleteHistory(ctx context.Context, tenantID uuid.UUID, monitorIDs []uuid.UUID) error {
	m.deletedHistoryIDs = append([]uuid.UUID(nil), monitorIDs...)
	return nil
}

func (m *MockRepository) BulkSoftDelete(ctx context.Context, tenantID uuid.UUID, monitorIDs []uuid.UUID) (int64, error) {
	if m.bulkSoftDeleteErr != nil {
		return 0, m.bulkSoftDeleteErr
	}
	m.bulkSoftDeleted = append([]uuid.UUID(nil), monitorIDs...)
	for _, id := range monitorIDs {
		delete(m.monitors, id)
	}
	if m.bulkSoftDeleteN > 0 {
		return m.bulkSoftDeleteN, nil
	}
	return int64(len(monitorIDs)), nil
}

func (m *MockRepository) HardDelete(ctx context.Context, monitorID uuid.UUID) error {
	id := monitorID
	m.hardDeletedID = &id
	delete(m.monitors, monitorID)
	return nil
}

func (m *MockRepository) VerifyAlertPolicy(ctx context.Context, tenantID, policyID uuid.UUID) error {
	return m.verifyAlertPolicyErr
}

func (m *MockRepository) VerifyMonitorsBelongToTenant(ctx context.Context, tenantID uuid.UUID, monitorIDs []uuid.UUID) error {
	return m.verifyMonitorsErr
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

func (m *MockRepository) GetDependsOnIDs(ctx context.Context, monitorID uuid.UUID) ([]uuid.UUID, error) {
	return nil, nil
}

func (m *MockRepository) ReplaceMonitorChannels(ctx context.Context, tenantID, monitorID uuid.UUID, channels []models.MonitorChannelAssignment) error {
	return nil
}

func (m *MockRepository) DeleteMonitorChannels(ctx context.Context, monitorID uuid.UUID) error {
	return nil
}

func (m *MockRepository) GetChannelsForMonitors(ctx context.Context, monitorIDs []uuid.UUID) (map[uuid.UUID][]models.MonitorChannelAssignment, error) {
	return make(map[uuid.UUID][]models.MonitorChannelAssignment), nil
}

func (m *MockRepository) SetLocations(ctx context.Context, tenantID, monitorID uuid.UUID, locationIDs []uuid.UUID) error {
	return nil
}

func (m *MockRepository) SetEnabled(ctx context.Context, tenantID, monitorID uuid.UUID, enabled bool) error {
	return nil
}

func (m *MockRepository) GetLocationIDsForMonitors(ctx context.Context, monitorIDs []uuid.UUID) (map[uuid.UUID][]uuid.UUID, error) {
	return make(map[uuid.UUID][]uuid.UUID), nil
}

func (m *MockRepository) GetLocationStatuses(ctx context.Context, monitorID uuid.UUID) ([]models.MonitorLocationStatus, error) {
	return nil, nil
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

type mockGroupResolver struct {
	members []models.Monitor
	err     error
}

func (m mockGroupResolver) GetGroupLeafMembers(ctx context.Context, tenantID, groupID uuid.UUID) ([]models.Monitor, error) {
	if m.err != nil {
		return nil, m.err
	}
	return append([]models.Monitor(nil), m.members...), nil
}

type mockStatusNotifier struct {
	published []uuid.UUID
}

func (m *mockStatusNotifier) PublishStatusUpdate(ctx context.Context, monitorID, tenantID uuid.UUID) {
	m.published = append(m.published, monitorID)
}

func TestService_DeleteMonitorHistory_RegularMonitor(t *testing.T) {
	repo := NewMockRepository()
	service := &Service{repo: repo}

	tenantID := uuid.New()
	monitorID := uuid.New()
	repo.monitors[monitorID] = &models.Monitor{ID: monitorID, TenantID: tenantID, Type: models.MonitorTypeHTTP}

	notifier := &mockStatusNotifier{}
	service.ConfigureHistoryDependencies(nil, notifier)

	if err := service.DeleteMonitorHistory(context.Background(), tenantID, monitorID); err != nil {
		t.Fatalf("DeleteMonitorHistory() error = %v", err)
	}

	if len(repo.deletedHistoryIDs) != 1 || repo.deletedHistoryIDs[0] != monitorID {
		t.Fatalf("deletedHistoryIDs = %v, want [%s]", repo.deletedHistoryIDs, monitorID)
	}
	if len(notifier.published) != 1 || notifier.published[0] != monitorID {
		t.Fatalf("published = %v, want [%s]", notifier.published, monitorID)
	}
}

func TestService_DeleteMonitorHistory_GroupMonitorUsesLeafMembers(t *testing.T) {
	repo := NewMockRepository()
	service := &Service{repo: repo}

	tenantID := uuid.New()
	groupID := uuid.New()
	memberA := uuid.New()
	memberB := uuid.New()
	repo.monitors[groupID] = &models.Monitor{ID: groupID, TenantID: tenantID, Type: models.MonitorTypeGroup}

	notifier := &mockStatusNotifier{}
	service.ConfigureHistoryDependencies(mockGroupResolver{
		members: []models.Monitor{
			{ID: memberA, TenantID: tenantID, Type: models.MonitorTypeHTTP},
			{ID: memberB, TenantID: tenantID, Type: models.MonitorTypePing},
		},
	}, notifier)

	if err := service.DeleteMonitorHistory(context.Background(), tenantID, groupID); err != nil {
		t.Fatalf("DeleteMonitorHistory() error = %v", err)
	}

	if len(repo.deletedHistoryIDs) != 3 {
		t.Fatalf("deletedHistoryIDs length = %d, want 3", len(repo.deletedHistoryIDs))
	}
	if repo.deletedHistoryIDs[0] != groupID || repo.deletedHistoryIDs[1] != memberA || repo.deletedHistoryIDs[2] != memberB {
		t.Fatalf("deletedHistoryIDs = %v, want [%s %s %s]", repo.deletedHistoryIDs, groupID, memberA, memberB)
	}
	if len(notifier.published) != 3 {
		t.Fatalf("published length = %d, want 3", len(notifier.published))
	}
}

func TestService_DeleteMonitorHistory_GroupMonitorWithoutMembers(t *testing.T) {
	repo := NewMockRepository()
	service := &Service{repo: repo}

	tenantID := uuid.New()
	groupID := uuid.New()
	repo.monitors[groupID] = &models.Monitor{ID: groupID, TenantID: tenantID, Type: models.MonitorTypeGroup}
	service.ConfigureHistoryDependencies(mockGroupResolver{}, nil)

	if err := service.DeleteMonitorHistory(context.Background(), tenantID, groupID); err != nil {
		t.Fatalf("DeleteMonitorHistory() error = %v", err)
	}
	if len(repo.deletedHistoryIDs) != 1 || repo.deletedHistoryIDs[0] != groupID {
		t.Fatalf("deletedHistoryIDs = %v, want [%s]", repo.deletedHistoryIDs, groupID)
	}
}

func TestService_BulkDeleteMonitors(t *testing.T) {
	ctx := context.Background()
	tenantID := uuid.New()

	repo := NewMockRepository()
	ids := []uuid.UUID{uuid.New(), uuid.New(), uuid.New()}
	for _, id := range ids {
		repo.monitors[id] = &models.Monitor{ID: id, TenantID: tenantID}
	}

	svc := NewService(repo)

	deleted, err := svc.BulkDeleteMonitors(ctx, tenantID, ids)
	if err != nil {
		t.Fatalf("BulkDeleteMonitors: %v", err)
	}
	if deleted != 3 {
		t.Fatalf("deleted = %d, want 3", deleted)
	}
	if got, want := len(repo.bulkSoftDeleted), 3; got != want {
		t.Fatalf("mock saw %d ids, want %d", got, want)
	}
}

func TestService_BulkDeleteMonitors_RejectsEmpty(t *testing.T) {
	svc := NewService(NewMockRepository())
	if _, err := svc.BulkDeleteMonitors(context.Background(), uuid.New(), nil); err == nil {
		t.Fatal("BulkDeleteMonitors(nil) should reject empty input")
	}
}

func TestService_BulkDeleteMonitors_VerifyFailsBlocks(t *testing.T) {
	repo := NewMockRepository()
	repo.verifyMonitorsErr = fmt.Errorf("one or more monitors not found or do not belong to tenant")
	svc := NewService(repo)

	_, err := svc.BulkDeleteMonitors(context.Background(), uuid.New(),
		[]uuid.UUID{uuid.New()})
	if err == nil {
		t.Fatal("BulkDeleteMonitors should propagate verify error")
	}
	if len(repo.bulkSoftDeleted) != 0 {
		t.Fatal("must not call BulkSoftDelete when verify fails")
	}
}
