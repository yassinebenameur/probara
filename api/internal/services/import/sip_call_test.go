package importservice

import (
	"context"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/yassinebenameur/probara/api/internal/models"
	monitorservice "github.com/yassinebenameur/probara/api/internal/services/monitors"
	ctxpkg "github.com/yassinebenameur/probara/shared/context"
)

// gatedMonitorServiceMock applies the monitor service's test-call gate
// (monitorservice.RequireCallPermission, which the real CreateMonitor calls
// first) before recording the create.
type gatedMonitorServiceMock struct{ portableMonitorServiceMock }

func (m *gatedMonitorServiceMock) CreateMonitor(ctx context.Context, tenantID uuid.UUID, req *models.CreateMonitorRequest) (*models.Monitor, error) {
	if err := monitorservice.RequireCallPermission(ctx, req.Type, req.Config); err != nil {
		return nil, err
	}
	return m.portableMonitorServiceMock.CreateMonitor(ctx, tenantID, req)
}

func sipCallImportMapping() models.FieldMapping {
	return models.FieldMapping{
		Name: "name", Type: "type", Config: "config",
		IntervalSeconds: "interval_seconds", TimeoutSeconds: "timeout_seconds", Enabled: "enabled",
	}
}

// The importer creates monitors through the monitor service, so it inherits
// the tenant-admin gate: an editor's import refuses the test-call row and
// keeps the rest.
func TestExecuteImport_SIPCallRowRefusedForNonAdmin(t *testing.T) {
	call := map[string]interface{}{"host": "trunk.example.com", "method": "invite", "call": map[string]interface{}{"number": "+33123456789"}}
	probe := map[string]interface{}{"host": "trunk.example.com", "method": "options"}
	rows := []models.ImportRow{
		rowWithConfig(0, "Call", "sip", call, nil, nil, 60, 15),
		rowWithConfig(1, "Probe", "sip", probe, nil, nil, 60, 15),
	}

	user := ctxpkg.WithAdminID(context.Background(), uuid.NewString())
	editor := ctxpkg.WithRole(user, "editor")
	monitorSvc := &gatedMonitorServiceMock{}
	result, err := NewService(nil, monitorSvc, &groupMembershipMock{}).ExecuteImport(editor, uuid.New(), &models.ImportExecuteRequest{Rows: rows, Mapping: sipCallImportMapping()})
	if err != nil {
		t.Fatalf("ExecuteImport() error = %v", err)
	}
	if result.SuccessCount != 1 || result.FailedCount != 1 {
		t.Fatalf("counts = %+v, want one success and one failure", result)
	}
	if result.Results[0].Status != "failed" || !strings.Contains(result.Results[0].Error, "tenant admin") {
		t.Fatalf("call row = %+v, want refused for a non-admin", result.Results[0])
	}

	admin := ctxpkg.WithRole(user, "admin")
	monitorSvc = &gatedMonitorServiceMock{}
	result, err = NewService(nil, monitorSvc, &groupMembershipMock{}).ExecuteImport(admin, uuid.New(), &models.ImportExecuteRequest{Rows: rows, Mapping: sipCallImportMapping()})
	if err != nil || result.SuccessCount != 2 {
		t.Fatalf("admin import = %+v, %v; want both rows created", result, err)
	}
}

// A test call's schedule bounds hold on import too.
func TestExecuteImport_SIPCallScheduleValidated(t *testing.T) {
	call := map[string]interface{}{"host": "trunk.example.com", "method": "invite", "call": map[string]interface{}{"number": "100"}}
	admin := ctxpkg.WithRole(ctxpkg.WithAdminID(context.Background(), uuid.NewString()), "admin")
	result, err := NewService(nil, &gatedMonitorServiceMock{}, &groupMembershipMock{}).ExecuteImport(admin, uuid.New(), &models.ImportExecuteRequest{
		Rows:    []models.ImportRow{rowWithConfig(0, "Call", "sip", call, nil, nil, 30, 10)},
		Mapping: sipCallImportMapping(),
	})
	if err != nil {
		t.Fatalf("ExecuteImport() error = %v", err)
	}
	if result.FailedCount != 1 || !strings.Contains(result.Results[0].Error, "interval_seconds must be at least 60") {
		t.Fatalf("result = %+v, want the interval floor", result.Results[0])
	}
}
