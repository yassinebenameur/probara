package results

import (
	"context"
	"regexp"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/google/uuid"

	"github.com/yassinebenameur/probara/api/internal/models"
	sharedanalytics "github.com/yassinebenameur/probara/shared/analytics"
	shareddb "github.com/yassinebenameur/probara/shared/db"
)

type fakeAnalyticsReader struct {
	calls          int
	lastMonitorIDs []uuid.UUID
	result         *sharedanalytics.Result
}

func (f *fakeAnalyticsReader) GetScopeAnalytics(ctx context.Context, tenantID uuid.UUID, monitorIDs []uuid.UUID, rangeValue sharedanalytics.Range, now time.Time) (*sharedanalytics.Result, error) {
	f.calls++
	f.lastMonitorIDs = append([]uuid.UUID(nil), monitorIDs...)
	return f.result, nil
}

type fakeGroupReader struct {
	members []models.Monitor
	status  string
}

func (f fakeGroupReader) GetGroupLeafMembers(ctx context.Context, tenantID, groupID uuid.UUID) ([]models.Monitor, error) {
	return append([]models.Monitor(nil), f.members...), nil
}

func (f fakeGroupReader) GetGroupStatus(ctx context.Context, tenantID, groupID uuid.UUID) (string, error) {
	return f.status, nil
}

func TestService_GetMonitorAnalytics_UsesInjectedAnalyticsReader(t *testing.T) {
	sqlDB, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock.New() error = %v", err)
	}
	defer sqlDB.Close()

	tenantID := uuid.New()
	monitorID := uuid.New()
	now := time.Now().UTC()
	avgLatency := 123.0
	analytics := &fakeAnalyticsReader{
		result: &sharedanalytics.Result{
			GeneratedAt: now,
			Source:      sharedanalytics.SourceRollup,
			Summary:     sharedanalytics.Summary{SLAPct: 99.9, AvgLatencyMS: &avgLatency},
		},
	}

	mock.ExpectQuery(regexp.QuoteMeta(`
		SELECT id, tenant_id, name, type, config,
			interval_seconds, timeout_seconds, alert_policy_id, enabled, tags,
			agent_id, next_run_at, created_at, updated_at
		FROM monitors
		WHERE id = $1 AND tenant_id = $2
	`)).
		WithArgs(monitorID, tenantID).
		WillReturnRows(sqlmock.NewRows([]string{
			"id", "tenant_id", "name", "type", "config", "interval_seconds", "timeout_seconds",
			"alert_policy_id", "enabled", "tags", "agent_id", "next_run_at", "created_at", "updated_at",
		}).AddRow(
			monitorID, tenantID, "monitor-a", "http", []byte(`{"url":"https://example.com","method":"GET"}`),
			60, 30, nil, true, "{}", nil, nil, now, now,
		))

	svc := NewService(&shareddb.Client{DB: sqlDB}, fakeGroupReader{}, analytics)
	resp, err := svc.GetMonitorAnalytics(context.Background(), tenantID, monitorID, models.MonitorAnalyticsRange30d)
	if err != nil {
		t.Fatalf("GetMonitorAnalytics() error = %v", err)
	}

	if analytics.calls != 1 {
		t.Fatalf("analytics calls = %d, want 1", analytics.calls)
	}
	if len(analytics.lastMonitorIDs) != 1 || analytics.lastMonitorIDs[0] != monitorID {
		t.Fatalf("analytics monitor IDs = %v, want [%s]", analytics.lastMonitorIDs, monitorID)
	}
	if resp.Source != models.AnalyticsSourceRollup {
		t.Fatalf("response source = %s, want %s", resp.Source, models.AnalyticsSourceRollup)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("sql expectations: %v", err)
	}
}

func TestService_GetMonitorAnalytics_GroupUsesLeafMembers(t *testing.T) {
	sqlDB, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock.New() error = %v", err)
	}
	defer sqlDB.Close()

	tenantID := uuid.New()
	groupID := uuid.New()
	leafA := uuid.New()
	leafB := uuid.New()
	now := time.Now().UTC()
	analytics := &fakeAnalyticsReader{result: &sharedanalytics.Result{GeneratedAt: now, Source: sharedanalytics.SourceRollup}}

	mock.ExpectQuery(regexp.QuoteMeta(`
		SELECT id, tenant_id, name, type, config,
			interval_seconds, timeout_seconds, alert_policy_id, enabled, tags,
			agent_id, next_run_at, created_at, updated_at
		FROM monitors
		WHERE id = $1 AND tenant_id = $2
	`)).
		WithArgs(groupID, tenantID).
		WillReturnRows(sqlmock.NewRows([]string{
			"id", "tenant_id", "name", "type", "config", "interval_seconds", "timeout_seconds",
			"alert_policy_id", "enabled", "tags", "agent_id", "next_run_at", "created_at", "updated_at",
		}).AddRow(
			groupID, tenantID, "group-a", "group", []byte(`{"monitor_ids":[]}`),
			60, 30, nil, true, "{}", nil, nil, now, now,
		))

	svc := NewService(&shareddb.Client{DB: sqlDB}, fakeGroupReader{
		members: []models.Monitor{{ID: leafA}, {ID: leafB}},
		status:  "success",
	}, analytics)

	if _, err := svc.GetMonitorAnalytics(context.Background(), tenantID, groupID, models.MonitorAnalyticsRange30d); err != nil {
		t.Fatalf("GetMonitorAnalytics(group) error = %v", err)
	}

	if len(analytics.lastMonitorIDs) != 2 || analytics.lastMonitorIDs[0] != leafA || analytics.lastMonitorIDs[1] != leafB {
		t.Fatalf("analytics monitor IDs = %v, want [%s %s]", analytics.lastMonitorIDs, leafA, leafB)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("sql expectations: %v", err)
	}
}
