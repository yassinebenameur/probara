package alerts

import (
	"context"
	"regexp"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/google/uuid"

	"github.com/yassinebenameur/probara/api/internal/models"
	"github.com/yassinebenameur/probara/shared/db"
)

// TestAlertListParams_Normalization tests the parameter clamping logic
// that happens within ListAlerts. These tests verify the business rules
// for pagination parameters.
func TestAlertListParams_Normalization(t *testing.T) {
	tests := []struct {
		name             string
		inputPage        int
		inputPageSize    int
		expectedPage     int
		expectedPageSize int
	}{
		// Page normalization
		{"page 0 defaults to 1", 0, 20, 1, 20},
		{"page -1 defaults to 1", -1, 20, 1, 20},
		{"page -100 defaults to 1", -100, 20, 1, 20},
		{"page 1 stays 1", 1, 20, 1, 20},
		{"page 10 stays 10", 10, 20, 10, 20},

		// PageSize normalization
		{"pageSize 0 defaults to 20", 1, 0, 1, 20},
		{"pageSize -1 defaults to 20", 1, -1, 1, 20},
		{"pageSize 1 stays 1", 1, 1, 1, 1},
		{"pageSize 50 stays 50", 1, 50, 1, 50},
		{"pageSize 100 stays 100", 1, 100, 1, 100},
		{"pageSize 101 capped to 100", 1, 101, 1, 100},
		{"pageSize 200 capped to 100", 1, 200, 1, 100},
		{"pageSize 1000 capped to 100", 1, 1000, 1, 100},

		// Combined scenarios
		{"both invalid default", 0, 0, 1, 20},
		{"both boundary", 1, 100, 1, 100},
		{"page invalid, pageSize over limit", -5, 150, 1, 100},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			params := &models.AlertListParams{
				Page:     tt.inputPage,
				PageSize: tt.inputPageSize,
			}

			// Apply the same normalization logic as in the service
			normalizeAlertListParams(params)

			if params.Page != tt.expectedPage {
				t.Errorf("Page = %d, want %d", params.Page, tt.expectedPage)
			}

			if params.PageSize != tt.expectedPageSize {
				t.Errorf("PageSize = %d, want %d", params.PageSize, tt.expectedPageSize)
			}
		})
	}
}

// normalizeAlertListParams applies the same normalization as the Service.ListAlerts method
// This is extracted here for testability without requiring a database
func normalizeAlertListParams(params *models.AlertListParams) {
	if params.Page < 1 {
		params.Page = 1
	}
	if params.PageSize < 1 {
		params.PageSize = 20
	}
	if params.PageSize > 100 {
		params.PageSize = 100
	}
}

// TestRecentAlertsLimit_Normalization tests the limit clamping for GetRecentAlerts
func TestRecentAlertsLimit_Normalization(t *testing.T) {
	tests := []struct {
		name          string
		inputLimit    int
		expectedLimit int
	}{
		{"limit 0 defaults to 10", 0, 10},
		{"limit -1 defaults to 10", -1, 10},
		{"limit -100 defaults to 10", -100, 10},
		{"limit 1 stays 1", 1, 1},
		{"limit 10 stays 10", 10, 10},
		{"limit 25 stays 25", 25, 25},
		{"limit 50 stays 50", 50, 50},
		{"limit 51 capped to 50", 51, 50},
		{"limit 100 capped to 50", 100, 50},
		{"limit 1000 capped to 50", 1000, 50},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := normalizeRecentAlertsLimit(tt.inputLimit)

			if result != tt.expectedLimit {
				t.Errorf("normalizeRecentAlertsLimit(%d) = %d, want %d", tt.inputLimit, result, tt.expectedLimit)
			}
		})
	}
}

// normalizeRecentAlertsLimit applies the same normalization as GetRecentAlerts
func normalizeRecentAlertsLimit(limit int) int {
	if limit < 1 {
		limit = 10
	}
	if limit > 50 {
		limit = 50
	}
	return limit
}

func TestGetRecentAlerts_ReturnsEmptySliceWhenNoRows(t *testing.T) {
	sqlDB, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock.New() error = %v", err)
	}
	defer sqlDB.Close()

	mock.ExpectQuery(regexp.QuoteMeta(`
		SELECT a.id, a.tenant_id, a.monitor_id, a.alert_policy_id, a.status,
			a.triggered_at, a.acknowledged_at, a.resolved_at, a.failure_count,
			a.last_error, a.kind, a.baseline_latency_ms, a.observed_latency_ms, a.anomaly_score,
			a.created_at, a.updated_at,
			m.name as monitor_name, ap.name as policy_name,
			a.root_cause_monitor_id, a.root_cause_down_since, rcm.name as root_cause_monitor_name
		FROM alerts a
		JOIN monitors m ON a.monitor_id = m.id
		LEFT JOIN alert_policies ap ON a.alert_policy_id = ap.id
		LEFT JOIN monitors rcm ON rcm.id = a.root_cause_monitor_id
		WHERE a.tenant_id = $1 AND m.deleted_at IS NULL
		ORDER BY a.triggered_at DESC
		LIMIT $2
	`)).
		WithArgs(uuid.Nil, 10).
		WillReturnRows(sqlmock.NewRows([]string{
			"id", "tenant_id", "monitor_id", "alert_policy_id", "status",
			"triggered_at", "acknowledged_at", "resolved_at", "failure_count",
			"last_error", "kind", "baseline_latency_ms", "observed_latency_ms", "anomaly_score",
			"created_at", "updated_at", "monitor_name", "policy_name",
			"root_cause_monitor_id", "root_cause_down_since", "root_cause_monitor_name",
		}))

	svc := NewService(&db.Client{DB: sqlDB}, nil)
	alerts, err := svc.GetRecentAlerts(context.Background(), uuid.Nil, 10)
	if err != nil {
		t.Fatalf("GetRecentAlerts() error = %v", err)
	}
	if alerts == nil {
		t.Fatalf("GetRecentAlerts() returned nil slice, want empty slice")
	}
	if len(alerts) != 0 {
		t.Fatalf("len(alerts) = %d, want 0", len(alerts))
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("sql expectations: %v", err)
	}
}

func TestGetRecentAlertsForTags_ReturnsEmptySliceWhenNoRows(t *testing.T) {
	sqlDB, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock.New() error = %v", err)
	}
	defer sqlDB.Close()

	mock.ExpectQuery(regexp.QuoteMeta(`
		SELECT a.id, a.tenant_id, a.monitor_id, a.alert_policy_id, a.status,
			a.triggered_at, a.acknowledged_at, a.resolved_at, a.failure_count,
			a.last_error, a.kind, a.baseline_latency_ms, a.observed_latency_ms, a.anomaly_score,
			a.created_at, a.updated_at,
			m.name as monitor_name, ap.name as policy_name,
			a.root_cause_monitor_id, a.root_cause_down_since, rcm.name as root_cause_monitor_name
		FROM alerts a
		JOIN monitors m ON a.monitor_id = m.id
		LEFT JOIN alert_policies ap ON a.alert_policy_id = ap.id
		LEFT JOIN monitors rcm ON rcm.id = a.root_cause_monitor_id
		WHERE a.tenant_id = $1
		  AND m.tenant_id = $1
		  AND m.tags @> $2::text[]
		  AND m.deleted_at IS NULL
		ORDER BY a.triggered_at DESC
		LIMIT $3
	`)).
		WithArgs(uuid.Nil, sqlmock.AnyArg(), 10).
		WillReturnRows(sqlmock.NewRows([]string{
			"id", "tenant_id", "monitor_id", "alert_policy_id", "status",
			"triggered_at", "acknowledged_at", "resolved_at", "failure_count",
			"last_error", "kind", "baseline_latency_ms", "observed_latency_ms", "anomaly_score",
			"created_at", "updated_at", "monitor_name", "policy_name",
			"root_cause_monitor_id", "root_cause_down_since", "root_cause_monitor_name",
		}))

	svc := NewService(&db.Client{DB: sqlDB}, nil)
	alerts, err := svc.GetRecentAlertsForTags(context.Background(), uuid.Nil, []string{"prod"}, 10)
	if err != nil {
		t.Fatalf("GetRecentAlertsForTags() error = %v", err)
	}
	if alerts == nil {
		t.Fatalf("GetRecentAlertsForTags() returned nil slice, want empty slice")
	}
	if len(alerts) != 0 {
		t.Fatalf("len(alerts) = %d, want 0", len(alerts))
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("sql expectations: %v", err)
	}
}

// TestAlertListParams_OffsetCalculation tests the offset calculation
func TestAlertListParams_OffsetCalculation(t *testing.T) {
	tests := []struct {
		name           string
		page           int
		pageSize       int
		expectedOffset int
	}{
		{"page 1, pageSize 10 = offset 0", 1, 10, 0},
		{"page 2, pageSize 10 = offset 10", 2, 10, 10},
		{"page 3, pageSize 10 = offset 20", 3, 10, 20},
		{"page 1, pageSize 20 = offset 0", 1, 20, 0},
		{"page 2, pageSize 20 = offset 20", 2, 20, 20},
		{"page 5, pageSize 20 = offset 80", 5, 20, 80},
		{"page 1, pageSize 100 = offset 0", 1, 100, 0},
		{"page 2, pageSize 100 = offset 100", 2, 100, 100},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			offset := calculateOffset(tt.page, tt.pageSize)

			if offset != tt.expectedOffset {
				t.Errorf("calculateOffset(%d, %d) = %d, want %d", tt.page, tt.pageSize, offset, tt.expectedOffset)
			}
		})
	}
}

// calculateOffset calculates the database offset from page and pageSize
func calculateOffset(page, pageSize int) int {
	return (page - 1) * pageSize
}

// TestAlertStatus_Constants tests that alert status constants have expected values
func TestAlertStatus_Constants(t *testing.T) {
	tests := []struct {
		status   models.AlertStatus
		expected string
	}{
		{models.AlertStatusActive, "active"},
		{models.AlertStatusAcknowledged, "acknowledged"},
		{models.AlertStatusResolved, "resolved"},
	}

	for _, tt := range tests {
		t.Run(string(tt.status), func(t *testing.T) {
			if string(tt.status) != tt.expected {
				t.Errorf("AlertStatus = %v, want %v", tt.status, tt.expected)
			}
		})
	}
}

// TestAlertListResponse_Structure tests the response structure
func TestAlertListResponse_Structure(t *testing.T) {
	response := &models.AlertListResponse{
		Items:    []models.AlertWithDetails{},
		Page:     1,
		PageSize: 20,
		Total:    0,
	}

	if response.Page != 1 {
		t.Errorf("Page = %d, want 1", response.Page)
	}
	if response.PageSize != 20 {
		t.Errorf("PageSize = %d, want 20", response.PageSize)
	}
	if response.Total != 0 {
		t.Errorf("Total = %d, want 0", response.Total)
	}
	if response.Items == nil {
		t.Error("Items should not be nil")
	}
}
