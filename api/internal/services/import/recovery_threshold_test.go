package importservice

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/google/uuid"

	"github.com/yassinebenameur/probara/api/internal/models"
	"github.com/yassinebenameur/probara/shared/db"
)

// recovery_threshold (docs/state-semantics.md S-T7) survives a portable
// export → import round trip, and both import paths carry it, clamped into
// the column's range like the failure threshold.
func TestRecoveryThreshold_PortableRoundTrip(t *testing.T) {
	tenantID := uuid.New()
	source := &portableMonitorServiceMock{listMonitors: []models.Monitor{{
		ID: uuid.New(), TenantID: tenantID, Name: "API", Type: models.MonitorTypeHTTP,
		Config:          json.RawMessage(`{"url":"https://example.com/health","method":"GET"}`),
		IntervalSeconds: 60, TimeoutSeconds: 30, Enabled: true,
		ConsecutiveFailuresThreshold: 2, RecoveryThreshold: 3,
		CreatedAt: time.Unix(10, 0), UpdatedAt: time.Unix(10, 0),
	}}}
	sqlDB, sqlMock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock.New() error = %v", err)
	}
	defer sqlDB.Close()
	sqlMock.ExpectQuery("FROM monitor_dependencies").
		WillReturnRows(sqlmock.NewRows([]string{"monitor_id", "depends_on_id"}))

	data, err := NewService(&db.Client{DB: sqlDB}, source, &groupMembershipMock{}).ExportMonitors(context.Background(), tenantID, ExportOptions{})
	if err != nil {
		t.Fatalf("ExportMonitors() error = %v", err)
	}
	if !strings.Contains(string(data), "recovery_threshold: 3") {
		t.Fatalf("export does not carry recovery_threshold:\n%s", data)
	}

	target := &portableMonitorServiceMock{}
	svc := NewService(&db.Client{DB: sqlDB}, target, &groupMembershipMock{})
	preview, err := svc.ParseFile(data, "monitors.yaml")
	if err != nil {
		t.Fatalf("ParseFile() error = %v", err)
	}
	if preview.SuggestedMapping.RecoveryThreshold != "recovery_threshold" {
		t.Fatalf("RecoveryThreshold mapping = %q, want recovery_threshold", preview.SuggestedMapping.RecoveryThreshold)
	}
	result, err := svc.ExecuteImport(context.Background(), tenantID, &models.ImportExecuteRequest{
		Rows: preview.Rows, Mapping: preview.SuggestedMapping,
	})
	if err != nil || result.SuccessCount != 1 {
		t.Fatalf("ExecuteImport() = %+v, %v", result, err)
	}
	if got := target.createRequests[0].RecoveryThreshold; got == nil || *got != 3 {
		t.Fatalf("imported recovery_threshold = %v, want 3", got)
	}
}

func TestRecoveryThreshold_SimplePathClampsAndOmits(t *testing.T) {
	for _, tc := range []struct {
		name string
		csv  string
		want *int
	}{
		{"clamped_high", "name,type,url,recovery_threshold\nA,http,https://example.com,42\n", intPtr(10)},
		{"clamped_low", "name,type,url,recovery_threshold\nA,http,https://example.com,0\n", intPtr(1)},
		{"in_range", "name,type,url,recovery_threshold\nA,http,https://example.com,4\n", intPtr(4)},
		{"absent_keeps_the_default", "name,type,url\nA,http,https://example.com\n", nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			sqlDB, _, err := sqlmock.New()
			if err != nil {
				t.Fatalf("sqlmock.New() error = %v", err)
			}
			defer sqlDB.Close()
			target := &portableMonitorServiceMock{}
			svc := NewService(&db.Client{DB: sqlDB}, target, &groupMembershipMock{})
			preview, err := svc.ParseFile([]byte(tc.csv), "monitors.csv")
			if err != nil {
				t.Fatalf("ParseFile() error = %v", err)
			}
			result, err := svc.ExecuteImport(context.Background(), uuid.New(), &models.ImportExecuteRequest{
				Rows: preview.Rows, Mapping: preview.SuggestedMapping,
			})
			if err != nil || result.SuccessCount != 1 {
				t.Fatalf("ExecuteImport() = %+v, %v", result, err)
			}
			got := target.createRequests[0].RecoveryThreshold
			if (got == nil) != (tc.want == nil) || (got != nil && *got != *tc.want) {
				t.Fatalf("recovery_threshold = %v, want %v", got, tc.want)
			}
		})
	}
}

func intPtr(n int) *int { return &n }
