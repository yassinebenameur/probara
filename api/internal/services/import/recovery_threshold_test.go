package importservice

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/google/uuid"
	"gopkg.in/yaml.v3"

	"github.com/yassinebenameur/probara/api/internal/models"
	"github.com/yassinebenameur/probara/shared/db"
)

// recovery_threshold (S-T7) round-trips through the portable bundle: the
// export writes it only above the default of 1, and the import maps it,
// clamps it into the column's 1-10 range, and leaves it unset (the default)
// when a record omits it.
func TestPortableBundleRoundTripsRecoveryThreshold(t *testing.T) {
	tenantID := uuid.New()
	monitorSvc := &portableMonitorServiceMock{
		listMonitors: []models.Monitor{
			{
				ID: uuid.New(), TenantID: tenantID, Name: "Confirmed", Type: models.MonitorTypeHTTP,
				Config:          json.RawMessage(`{"url":"https://example.com/a","method":"GET"}`),
				IntervalSeconds: 60, TimeoutSeconds: 30, Enabled: true,
				ConsecutiveFailuresThreshold: 2, RecoveryThreshold: 3,
				CreatedAt: time.Unix(20, 0), UpdatedAt: time.Unix(20, 0),
			},
			{
				ID: uuid.New(), TenantID: tenantID, Name: "Single", Type: models.MonitorTypeHTTP,
				Config:          json.RawMessage(`{"url":"https://example.com/b","method":"GET"}`),
				IntervalSeconds: 60, TimeoutSeconds: 30, Enabled: true,
				ConsecutiveFailuresThreshold: 2, RecoveryThreshold: 1,
				CreatedAt: time.Unix(10, 0), UpdatedAt: time.Unix(10, 0),
			},
		},
	}
	sqlDB, _, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock.New() error = %v", err)
	}
	defer sqlDB.Close()
	svc := NewService(&db.Client{DB: sqlDB}, monitorSvc, &groupMembershipMock{})

	data, err := svc.ExportMonitors(context.Background(), tenantID)
	if err != nil {
		t.Fatalf("ExportMonitors() error = %v", err)
	}
	if got := strings.Count(string(data), "recovery_threshold:"); got != 1 {
		t.Fatalf("export has %d recovery_threshold lines, want 1 (default omitted):\n%s", got, data)
	}
	var bundle models.PortableMonitorExport
	if err := yaml.Unmarshal(data, &bundle); err != nil {
		t.Fatalf("unmarshal export: %v", err)
	}
	byName := map[string]models.PortableExportMonitor{}
	for _, m := range bundle.Monitors {
		byName[m.Name] = m
	}
	if byName["Confirmed"].RecoveryThreshold != 3 || byName["Single"].RecoveryThreshold != 0 {
		t.Fatalf("exported recovery thresholds = %d/%d, want 3/omitted",
			byName["Confirmed"].RecoveryThreshold, byName["Single"].RecoveryThreshold)
	}

	// Re-import the bundle with one record pushed out of range.
	bundle.Monitors = append(bundle.Monitors, models.PortableExportMonitor{
		Name: "Too strict", Type: models.MonitorTypeHTTP, IntervalSeconds: 60, TimeoutSeconds: 30, Enabled: true,
		Config:            map[string]interface{}{"url": "https://example.com/c", "method": "GET"},
		RecoveryThreshold: 25,
	})
	reimport, err := yaml.Marshal(bundle)
	if err != nil {
		t.Fatalf("marshal bundle: %v", err)
	}
	preview, err := svc.ParseFile(reimport, "monitors.yaml")
	if err != nil {
		t.Fatalf("ParseFile() error = %v", err)
	}
	if preview.SuggestedMapping.RecoveryThreshold != "recovery_threshold" {
		t.Fatalf("RecoveryThreshold mapping = %q, want recovery_threshold", preview.SuggestedMapping.RecoveryThreshold)
	}

	monitorSvc.listMonitors = nil // nothing exists yet in the target workspace
	result, err := svc.ExecuteImport(context.Background(), tenantID, &models.ImportExecuteRequest{
		Rows:    preview.Rows,
		Mapping: preview.SuggestedMapping,
	})
	if err != nil {
		t.Fatalf("ExecuteImport() error = %v", err)
	}
	if result.SuccessCount != 3 {
		t.Fatalf("import result = %+v, want 3 successes", result)
	}
	got := map[string]*int{}
	for _, req := range monitorSvc.createRequests {
		got[req.Name] = req.RecoveryThreshold
	}
	if v := got["Confirmed"]; v == nil || *v != 3 {
		t.Fatalf("Confirmed recovery_threshold = %v, want 3", v)
	}
	if v := got["Single"]; v != nil {
		t.Fatalf("Single recovery_threshold = %d, want unset (default 1)", *v)
	}
	if v := got["Too strict"]; v == nil || *v != 10 {
		t.Fatalf("Too strict recovery_threshold = %v, want clamped to 10", v)
	}
}
