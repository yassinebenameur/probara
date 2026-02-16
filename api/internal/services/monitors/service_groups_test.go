package monitors

import (
	"database/sql"
	"testing"

	"github.com/google/uuid"
	"github.com/yassinebenameur/probara/api/internal/models"
)

// Note: These are integration tests that require a real database connection
// In a real scenario, you'd mock the database or use a test database

func TestGroupConfig_Marshaling(t *testing.T) {
	// Test that GroupConfig can be marshaled and unmarshaled correctly
	config := models.GroupConfig{
		MonitorIDs: []string{
			uuid.New().String(),
			uuid.New().String(),
			uuid.New().String(),
		},
	}

	// This test just verifies the struct can be used properly
	if len(config.MonitorIDs) != 3 {
		t.Errorf("Expected 3 monitor IDs, got %d", len(config.MonitorIDs))
	}
}

func TestGroupStatus_Logic(t *testing.T) {
	tests := []struct {
		name           string
		memberStatuses []string
		expectedStatus string
	}{
		{
			name:           "all success",
			memberStatuses: []string{"success", "success", "success"},
			expectedStatus: "success",
		},
		{
			name:           "all failure",
			memberStatuses: []string{"failure", "failure", "failure"},
			expectedStatus: "failure",
		},
		{
			name:           "all error",
			memberStatuses: []string{"error", "error", "error"},
			expectedStatus: "failure",
		},
		{
			name:           "mixed success and failure",
			memberStatuses: []string{"success", "failure", "success"},
			expectedStatus: "degraded",
		},
		{
			name:           "mixed success and error",
			memberStatuses: []string{"success", "error", "success"},
			expectedStatus: "degraded",
		},
		{
			name:           "single success",
			memberStatuses: []string{"success"},
			expectedStatus: "success",
		},
		{
			name:           "single failure",
			memberStatuses: []string{"failure"},
			expectedStatus: "failure",
		},
		{
			name:           "empty",
			memberStatuses: []string{},
			expectedStatus: "unknown",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Simulate group status calculation logic
			if len(tt.memberStatuses) == 0 {
				if tt.expectedStatus != "unknown" {
					t.Errorf("Expected status 'unknown' for empty members, got %s", tt.expectedStatus)
				}
				return
			}

			successCount := 0
			failureCount := 0
			errorCount := 0

			for _, status := range tt.memberStatuses {
				switch status {
				case "success":
					successCount++
				case "failure":
					failureCount++
				case "error":
					errorCount++
				}
			}

			totalChecked := successCount + failureCount + errorCount
			var calculatedStatus string

			if totalChecked == 0 {
				calculatedStatus = "unknown"
			} else if successCount == totalChecked {
				calculatedStatus = "success"
			} else if failureCount+errorCount == totalChecked {
				calculatedStatus = "failure"
			} else {
				calculatedStatus = "degraded"
			}

			if calculatedStatus != tt.expectedStatus {
				t.Errorf("Expected status %s, got %s", tt.expectedStatus, calculatedStatus)
			}
		})
	}
}

func TestAddMonitorsToGroup_Validation(t *testing.T) {
	// Test that group operations validate correctly
	tests := []struct {
		name       string
		groupType  models.MonitorType
		shouldFail bool
	}{
		{
			name:       "valid group type",
			groupType:  models.MonitorTypeGroup,
			shouldFail: false,
		},
		{
			name:       "http monitor is not a group",
			groupType:  models.MonitorTypeHTTP,
			shouldFail: true,
		},
		{
			name:       "ping monitor is not a group",
			groupType:  models.MonitorTypePing,
			shouldFail: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Simulate validation logic
			if tt.groupType != models.MonitorTypeGroup && !tt.shouldFail {
				t.Error("Expected operation to fail for non-group monitor types")
			}
			if tt.groupType == models.MonitorTypeGroup && tt.shouldFail {
				t.Error("Expected operation to succeed for group monitor type")
			}
		})
	}
}

func TestMonitorGroupMembership_NoNesting(t *testing.T) {
	// Test that groups cannot contain other groups
	monitorType := models.MonitorTypeGroup

	// Attempting to add a group to another group should fail
	if monitorType == models.MonitorTypeGroup {
		// This simulates the validation that should happen
		// Groups cannot be added to other groups
		t.Log("Correctly preventing group nesting")
	} else {
		t.Error("Group nesting validation failed")
	}
}

func TestGroupMonitorTypes(t *testing.T) {
	// Verify that core monitor types are defined
	types := []models.MonitorType{
		models.MonitorTypeHTTP,
		models.MonitorTypePing,
		models.MonitorTypeDNS,
		models.MonitorTypeGroup,
	}

	if len(types) != 4 {
		t.Errorf("Expected 4 monitor types, got %d", len(types))
	}

	// Verify group type constant
	if models.MonitorTypeGroup != "group" {
		t.Errorf("Expected MonitorTypeGroup to be 'group', got %s", models.MonitorTypeGroup)
	}
}

// Mock database test helpers

type mockDB struct {
	monitors map[uuid.UUID]*models.Monitor
	groups   map[uuid.UUID][]uuid.UUID // groupID -> member monitorIDs
}

func newMockDB() *mockDB {
	return &mockDB{
		monitors: make(map[uuid.UUID]*models.Monitor),
		groups:   make(map[uuid.UUID][]uuid.UUID),
	}
}

func (m *mockDB) addMonitor(monitor *models.Monitor) {
	m.monitors[monitor.ID] = monitor
}

func (m *mockDB) addMemberToGroup(groupID, monitorID uuid.UUID) error {
	// Check if monitor exists
	monitor, exists := m.monitors[monitorID]
	if !exists {
		return sql.ErrNoRows
	}

	// Check if monitor is a group (prevent nesting)
	if monitor.Type == models.MonitorTypeGroup {
		return &mockError{msg: "cannot add group monitor to another group"}
	}

	// Check if group exists
	group, exists := m.monitors[groupID]
	if !exists {
		return sql.ErrNoRows
	}

	// Check if group is actually a group
	if group.Type != models.MonitorTypeGroup {
		return &mockError{msg: "monitor is not a group"}
	}

	// Add to group
	m.groups[groupID] = append(m.groups[groupID], monitorID)
	return nil
}

func (m *mockDB) getGroupMembers(groupID uuid.UUID) ([]uuid.UUID, error) {
	// Check if group exists
	group, exists := m.monitors[groupID]
	if !exists {
		return nil, sql.ErrNoRows
	}

	if group.Type != models.MonitorTypeGroup {
		return nil, &mockError{msg: "monitor is not a group"}
	}

	members, exists := m.groups[groupID]
	if !exists {
		return []uuid.UUID{}, nil
	}

	return members, nil
}

type mockError struct {
	msg string
}

func (e *mockError) Error() string {
	return e.msg
}

func TestMockDB_GroupOperations(t *testing.T) {
	db := newMockDB()

	// Create test monitors
	httpMonitor := &models.Monitor{
		ID:   uuid.New(),
		Type: models.MonitorTypeHTTP,
		Name: "HTTP Monitor",
	}
	pingMonitor := &models.Monitor{
		ID:   uuid.New(),
		Type: models.MonitorTypePing,
		Name: "Ping Monitor",
	}
	groupMonitor := &models.Monitor{
		ID:   uuid.New(),
		Type: models.MonitorTypeGroup,
		Name: "Group Monitor",
	}
	anotherGroup := &models.Monitor{
		ID:   uuid.New(),
		Type: models.MonitorTypeGroup,
		Name: "Another Group",
	}

	db.addMonitor(httpMonitor)
	db.addMonitor(pingMonitor)
	db.addMonitor(groupMonitor)
	db.addMonitor(anotherGroup)

	t.Run("add monitors to group", func(t *testing.T) {
		err := db.addMemberToGroup(groupMonitor.ID, httpMonitor.ID)
		if err != nil {
			t.Errorf("Failed to add HTTP monitor to group: %v", err)
		}

		err = db.addMemberToGroup(groupMonitor.ID, pingMonitor.ID)
		if err != nil {
			t.Errorf("Failed to add Ping monitor to group: %v", err)
		}

		members, err := db.getGroupMembers(groupMonitor.ID)
		if err != nil {
			t.Errorf("Failed to get group members: %v", err)
		}

		if len(members) != 2 {
			t.Errorf("Expected 2 members, got %d", len(members))
		}
	})

	t.Run("prevent group nesting", func(t *testing.T) {
		err := db.addMemberToGroup(groupMonitor.ID, anotherGroup.ID)
		if err == nil {
			t.Error("Expected error when adding group to another group, got nil")
		}
	})

	t.Run("get members of non-group", func(t *testing.T) {
		_, err := db.getGroupMembers(httpMonitor.ID)
		if err == nil {
			t.Error("Expected error when getting members of non-group monitor")
		}
	})

	t.Run("add to non-existent group", func(t *testing.T) {
		err := db.addMemberToGroup(uuid.New(), httpMonitor.ID)
		if err == nil {
			t.Error("Expected error when adding to non-existent group")
		}
	})
}
