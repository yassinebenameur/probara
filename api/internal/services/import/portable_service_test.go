package importservice

import (
	"context"
	"database/sql/driver"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/google/uuid"
	"gopkg.in/yaml.v3"

	"github.com/yassinebenameur/probara/api/internal/models"
	monitorservice "github.com/yassinebenameur/probara/api/internal/services/monitors"
	"github.com/yassinebenameur/probara/api/internal/validation"
	"github.com/yassinebenameur/probara/shared/db"
)

type portableMonitorServiceMock struct {
	listMonitors    []models.Monitor
	getMonitors     map[uuid.UUID]*models.Monitor
	createRequests  []*models.CreateMonitorRequest
	createdMonitors []*models.Monitor
}

var _ monitorservice.MonitorService = (*portableMonitorServiceMock)(nil)

func (m *portableMonitorServiceMock) CreateMonitor(ctx context.Context, tenantID uuid.UUID, req *models.CreateMonitorRequest) (*models.Monitor, error) {
	monitorID := uuid.New()
	monitor := &models.Monitor{
		ID:              monitorID,
		TenantID:        tenantID,
		Name:            req.Name,
		Type:            req.Type,
		Config:          req.Config,
		IntervalSeconds: req.IntervalSeconds,
		TimeoutSeconds:  req.TimeoutSeconds,
		Enabled:         req.Enabled == nil || *req.Enabled,
		CreatedAt:       time.Now(),
		UpdatedAt:       time.Now(),
	}
	m.createRequests = append(m.createRequests, req)
	m.createdMonitors = append(m.createdMonitors, monitor)
	return monitor, nil
}

func (m *portableMonitorServiceMock) ResolveTestConfig(ctx context.Context, tenantID uuid.UUID, monitorID *uuid.UUID, monitorType models.MonitorType, config json.RawMessage) (json.RawMessage, error) {
	return config, nil
}

func (m *portableMonitorServiceMock) GetMonitor(ctx context.Context, tenantID, monitorID uuid.UUID) (*models.Monitor, error) {
	monitor, ok := m.getMonitors[monitorID]
	if !ok {
		return nil, nil
	}
	return monitor, nil
}

func (m *portableMonitorServiceMock) ListMonitors(ctx context.Context, tenantID uuid.UUID, tag *string, enabled *bool, page, pageSize int) (*models.MonitorListResponse, error) {
	start := (page - 1) * pageSize
	if start >= len(m.listMonitors) {
		return &models.MonitorListResponse{
			Items:    []models.Monitor{},
			Page:     page,
			PageSize: pageSize,
			Total:    len(m.listMonitors),
		}, nil
	}

	end := start + pageSize
	if end > len(m.listMonitors) {
		end = len(m.listMonitors)
	}

	items := make([]models.Monitor, end-start)
	copy(items, m.listMonitors[start:end])
	return &models.MonitorListResponse{
		Items:    items,
		Page:     page,
		PageSize: pageSize,
		Total:    len(m.listMonitors),
	}, nil
}

func (m *portableMonitorServiceMock) UpdateMonitor(ctx context.Context, tenantID, monitorID uuid.UUID, req *models.UpdateMonitorRequest) (*models.Monitor, error) {
	return nil, nil
}

func (m *portableMonitorServiceMock) DeleteMonitor(ctx context.Context, tenantID, monitorID uuid.UUID) error {
	return nil
}

func (m *portableMonitorServiceMock) BulkDeleteMonitors(ctx context.Context, tenantID uuid.UUID, monitorIDs []uuid.UUID) (int64, error) {
	return 0, nil
}

func (m *portableMonitorServiceMock) DeleteMonitorHistory(ctx context.Context, tenantID, monitorID uuid.UUID) error {
	return nil
}

func (m *portableMonitorServiceMock) BulkUpdateAlerting(ctx context.Context, tenantID uuid.UUID, monitorIDs []uuid.UUID, threshold *int, mode *string, channels []models.MonitorChannelAssignment) (int, error) {
	return len(monitorIDs), nil
}

// groupMembershipMock records the monitor_groups writes the importer makes, so
// tests can assert that imported groups actually gain members instead of only
// carrying monitor_ids in their config.
type groupMembershipMock struct {
	added map[uuid.UUID][]uuid.UUID
	err   error
}

var _ GroupMembershipService = (*groupMembershipMock)(nil)

func (m *groupMembershipMock) AddMonitorsToGroup(ctx context.Context, tenantID, groupID uuid.UUID, monitorIDs []uuid.UUID) error {
	if m.err != nil {
		return m.err
	}
	if m.added == nil {
		m.added = make(map[uuid.UUID][]uuid.UUID)
	}
	m.added[groupID] = append(m.added[groupID], monitorIDs...)
	return nil
}

func TestParseFile_PortableMonitorExport(t *testing.T) {
	svc := &Service{}
	data := []byte(`
kind: monitor_export
version: 1
monitors:
  - name: API
    type: http
    interval_seconds: 60
    timeout_seconds: 30
    enabled: true
    tags: [prod, public]
    alert_policy_names: [Primary]
    config:
      url: https://example.com/health
      method: GET
`)

	preview, err := svc.ParseFile(data, "monitors.yaml")
	if err != nil {
		t.Fatalf("ParseFile() error = %v", err)
	}

	if preview.Schema != models.ImportSchemaPortableMonitorExport {
		t.Fatalf("Schema = %q, want %q", preview.Schema, models.ImportSchemaPortableMonitorExport)
	}
	if preview.SuggestedMapping.Config != "config" {
		t.Fatalf("Config mapping = %q, want config", preview.SuggestedMapping.Config)
	}
	if preview.SuggestedMapping.AlertPolicyNames != "alert_policy_names" {
		t.Fatalf("AlertPolicyNames mapping = %q, want alert_policy_names", preview.SuggestedMapping.AlertPolicyNames)
	}
	if preview.SuggestedMapping.GroupMembers != "" {
		t.Fatalf("GroupMembers mapping = %q, want empty for non-group payload", preview.SuggestedMapping.GroupMembers)
	}

	config, ok := preview.Rows[0].Fields["config"].(map[string]interface{})
	if !ok {
		t.Fatalf("config field type = %T, want map[string]interface{}", preview.Rows[0].Fields["config"])
	}
	if _, ok := config["url"]; !ok {
		t.Fatalf("config.url missing from parsed portable export")
	}
}

func TestExportMonitors_PortableBundle(t *testing.T) {
	tenantID := uuid.New()
	policyID := uuid.New()
	httpID := uuid.New()
	pushID := uuid.New()
	groupID := uuid.New()

	monitorSvc := &portableMonitorServiceMock{
		listMonitors: []models.Monitor{
			{
				ID:              groupID,
				TenantID:        tenantID,
				Name:            "Platform",
				Type:            models.MonitorTypeGroup,
				Config:          json.RawMessage(`{"monitor_ids":["legacy-http","legacy-push"]}`),
				IntervalSeconds: 60,
				TimeoutSeconds:  0,
				Enabled:         true,
				CreatedAt:       time.Unix(30, 0),
				UpdatedAt:       time.Unix(30, 0),
			},
			{
				ID:              pushID,
				TenantID:        tenantID,
				Name:            "Heartbeat",
				Type:            models.MonitorTypePush,
				Config:          json.RawMessage(`{"push_token":"legacy-token","expected_interval_seconds":60,"grace_period_seconds":10}`),
				IntervalSeconds: 60,
				TimeoutSeconds:  0,
				Enabled:         true,
				CreatedAt:       time.Unix(20, 0),
				UpdatedAt:       time.Unix(20, 0),
			},
			{
				ID:              httpID,
				TenantID:        tenantID,
				Name:            "API",
				Type:            models.MonitorTypeHTTP,
				Config:          json.RawMessage(`{"url":"https://example.com/health","method":"GET","expected_status":200}`),
				IntervalSeconds: 60,
				TimeoutSeconds:  30,
				AlertPolicyIDs:  []uuid.UUID{policyID},
				Enabled:         true,
				CreatedAt:       time.Unix(10, 0),
				UpdatedAt:       time.Unix(10, 0),
			},
		},
		getMonitors: map[uuid.UUID]*models.Monitor{
			groupID: {
				ID:        groupID,
				TenantID:  tenantID,
				Name:      "Platform",
				Type:      models.MonitorTypeGroup,
				MemberIDs: []uuid.UUID{httpID, pushID},
			},
		},
	}

	sqlDB, sqlMock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock.New() error = %v", err)
	}
	defer sqlDB.Close()

	sqlMock.ExpectQuery(`(?s)SELECT id, name\s+FROM alert_policies`).
		WithArgs(tenantID, anyArrayArg{}).
		WillReturnRows(sqlmock.NewRows([]string{"id", "name"}).AddRow(policyID, "Primary"))

	groupSvc := &groupMembershipMock{}
	svc := NewService(&db.Client{DB: sqlDB}, monitorSvc, groupSvc)
	data, err := svc.ExportMonitors(context.Background(), tenantID)
	if err != nil {
		t.Fatalf("ExportMonitors() error = %v", err)
	}

	var bundle models.PortableMonitorExport
	if err := yaml.Unmarshal(data, &bundle); err != nil {
		t.Fatalf("failed to unmarshal bundle: %v", err)
	}

	if bundle.Kind != "monitor_export" || bundle.Version != 1 {
		t.Fatalf("unexpected bundle header: %+v", bundle)
	}
	if len(bundle.Monitors) != 3 {
		t.Fatalf("len(bundle.Monitors) = %d, want 3", len(bundle.Monitors))
	}
	if bundle.Monitors[2].Type != models.MonitorTypeGroup {
		t.Fatalf("expected group monitor to be exported last, got %s", bundle.Monitors[2].Type)
	}
	if !strings.Contains(string(data), "alert_policy_names") {
		t.Fatalf("export missing alert_policy_names: %s", string(data))
	}
	if strings.Contains(string(data), "tenant_id:") || strings.Contains(string(data), "created_at:") {
		t.Fatalf("export leaked runtime metadata: %s", string(data))
	}
	if len(bundle.Monitors[2].GroupMembers) != 2 {
		t.Fatalf("expected group_members for exported group, got %+v", bundle.Monitors[2].GroupMembers)
	}
	if strings.Contains(string(data), "key:") || strings.Contains(string(data), "group_member_keys") {
		t.Fatalf("export leaked hidden references: %s", string(data))
	}

	if err := sqlMock.ExpectationsWereMet(); err != nil {
		t.Fatalf("unmet SQL expectations: %v", err)
	}
}

func TestExecuteImport_PortableBundleSupportsAllMonitorTypes(t *testing.T) {
	tenantID := uuid.New()
	policyID := uuid.New()
	monitorSvc := &portableMonitorServiceMock{}

	sqlDB, sqlMock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock.New() error = %v", err)
	}
	defer sqlDB.Close()

	sqlMock.ExpectQuery(`(?s)SELECT id\s+FROM alert_policies\s+WHERE tenant_id = \$1 AND name = \$2`).
		WithArgs(tenantID, "Primary").
		WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(policyID))

	groupSvc := &groupMembershipMock{}
	svc := NewService(&db.Client{DB: sqlDB}, monitorSvc, groupSvc)

	req := &models.ImportExecuteRequest{
		Rows: []models.ImportRow{
			rowWithConfig(0, "API", "http", map[string]interface{}{"url": "https://example.com/health", "method": "GET"}, []string{"Primary"}, nil, 60, 30),
			rowWithConfig(1, "Ping", "ping", map[string]interface{}{"host": "example.com"}, nil, nil, 60, 30),
			rowWithConfig(2, "DNS", "dns", map[string]interface{}{"host": "example.com", "record_type": "A"}, nil, nil, 60, 30),
			rowWithConfig(3, "gRPC", "grpc", map[string]interface{}{"host": "grpc.example.com", "port": 443, "use_tls": true}, nil, nil, 60, 30),
			rowWithConfig(4, "SIP", "sip", map[string]interface{}{"host": "sip.example.com", "port": 5060, "transport": "udp"}, nil, nil, 60, 30),
			rowWithConfig(5, "Agent", "agent", map[string]interface{}{"agent_id": "legacy-agent", "expected_interval_seconds": 60}, nil, nil, 60, 0),
			rowWithConfig(6, "Push", "push", map[string]interface{}{"push_token": "legacy-token", "expected_interval_seconds": 60, "grace_period_seconds": 5}, nil, nil, 60, 0),
			rowWithConfig(7, "Synthetic API", "synthetic_api", map[string]interface{}{"steps": []interface{}{map[string]interface{}{"id": "health", "request": map[string]interface{}{"method": "GET", "url": "https://example.com/health"}}}}, nil, nil, 60, 30),
			rowWithConfig(8, "Synthetic Browser", "synthetic_browser", map[string]interface{}{"start_url": "https://example.com", "steps": []interface{}{map[string]interface{}{"id": "open", "action": "goto", "url": "https://example.com"}}}, nil, nil, 60, 30),
			rowWithConfig(9, "TCP", "tcp", map[string]interface{}{"host": "tcp.example.com", "port": 9000}, nil, nil, 60, 30),
			rowWithConfig(10, "Redis", "redis", map[string]interface{}{"host": "redis.example.com", "port": 6379}, nil, nil, 60, 30),
			rowWithConfig(11, "Postgres", "postgres", map[string]interface{}{"host": "pg.example.com", "port": 5432, "username": "probe"}, nil, nil, 60, 30),
			rowWithConfig(12, "MySQL", "mysql", map[string]interface{}{"host": "mysql.example.com", "port": 3306, "username": "probe"}, nil, nil, 60, 30),
			rowWithConfig(13, "MongoDB", "mongodb", map[string]interface{}{"connection_string": "mongodb://mongo.example.com:27017"}, nil, nil, 60, 30),
			rowWithConfig(14, "RabbitMQ", "rabbitmq", map[string]interface{}{"host": "mq.example.com", "port": 5672, "username": "guest"}, nil, nil, 60, 30),
			rowWithConfig(15, "WebSocket", "websocket", map[string]interface{}{"url": "wss://ws.example.com/socket"}, nil, nil, 60, 30),
			rowWithConfig(16, "Platform", "group", map[string]interface{}{"monitor_ids": []interface{}{"legacy-http"}}, nil, []string{"API", "Push"}, 60, 0),
		},
		Mapping: models.FieldMapping{
			Name:             "name",
			Type:             "type",
			Config:           "config",
			IntervalSeconds:  "interval_seconds",
			TimeoutSeconds:   "timeout_seconds",
			Enabled:          "enabled",
			Tags:             "tags",
			AlertPolicyNames: "alert_policy_names",
			GroupMembers:     "group_members",
		},
	}

	result, err := svc.ExecuteImport(context.Background(), tenantID, req)
	if err != nil {
		t.Fatalf("ExecuteImport() error = %v", err)
	}

	if result.SuccessCount != 17 || result.FailedCount != 0 || result.SkippedCount != 0 {
		t.Fatalf("unexpected import counts: %+v", result)
	}
	if len(monitorSvc.createRequests) != 17 {
		t.Fatalf("len(createRequests) = %d, want 17", len(monitorSvc.createRequests))
	}

	// Every request the import path builds must pass the same validation the
	// real monitor service applies — this catches type gates and timeout
	// handling drifting from the validator registry.
	for i, createReq := range monitorSvc.createRequests {
		if err := validation.ValidateMonitor(createReq); err != nil {
			t.Fatalf("createRequests[%d] (%s/%s) failed validation: %v", i, createReq.Type, createReq.Name, err)
		}
	}

	pushConfig := decodeConfig(t, monitorSvc.createRequests[6].Config)
	if _, ok := pushConfig["push_token"]; ok {
		t.Fatalf("push_token should be stripped from imported push config: %+v", pushConfig)
	}

	agentConfig := decodeConfig(t, monitorSvc.createRequests[5].Config)
	if _, ok := agentConfig["agent_id"]; ok {
		t.Fatalf("agent_id should be stripped from imported agent config: %+v", agentConfig)
	}

	if got := monitorSvc.createRequests[0].AlertPolicyIDs; len(got) != 1 || got[0] != policyID.String() {
		t.Fatalf("alert policy IDs = %+v, want [%s]", got, policyID)
	}

	groupConfig := decodeConfig(t, monitorSvc.createRequests[16].Config)
	memberIDs, ok := groupConfig["monitor_ids"].([]interface{})
	if !ok || len(memberIDs) != 2 {
		t.Fatalf("group monitor_ids = %#v, want two imported member IDs", groupConfig["monitor_ids"])
	}

	// config.monitor_ids is not membership: Monitor.MemberIDs, the group alert
	// roll-up and the dashboards all read the monitor_groups junction table, so
	// the importer has to write it too.
	groupID := monitorSvc.createdMonitors[16].ID
	attached := groupSvc.added[groupID]
	if len(attached) != len(memberIDs) {
		t.Fatalf("group %s gained %d membership rows, want %d", groupID, len(attached), len(memberIDs))
	}
	for i, member := range memberIDs {
		if attached[i].String() != member {
			t.Fatalf("membership row %d = %s, want %v", i, attached[i], member)
		}
	}

	for _, idx := range []int{9, 10, 11, 12, 13, 14, 15} {
		if monitorSvc.createRequests[idx].TimeoutSeconds <= 0 {
			t.Fatalf("active check type %s lost its timeout on import: %+v", monitorSvc.createRequests[idx].Type, monitorSvc.createRequests[idx])
		}
	}

	if err := sqlMock.ExpectationsWereMet(); err != nil {
		t.Fatalf("unmet SQL expectations: %v", err)
	}
}

func TestExecuteImport_SkipsAlreadyImportedMonitors(t *testing.T) {
	tenantID := uuid.New()
	existingAPIID := uuid.New()
	monitorSvc := &portableMonitorServiceMock{
		listMonitors: []models.Monitor{
			{
				ID:       existingAPIID,
				TenantID: tenantID,
				Name:     "API",
				Type:     models.MonitorTypeHTTP,
			},
		},
	}

	groupSvc := &groupMembershipMock{}
	svc := NewService(nil, monitorSvc, groupSvc)
	result, err := svc.ExecuteImport(context.Background(), tenantID, &models.ImportExecuteRequest{
		Rows: []models.ImportRow{
			rowWithConfig(0, "API", "http", map[string]interface{}{"url": "https://example.com/health", "method": "GET"}, nil, nil, 60, 30),
			rowWithConfig(1, "Ping", "ping", map[string]interface{}{"host": "example.com"}, nil, nil, 60, 30),
			rowWithConfig(2, "Platform", "group", map[string]interface{}{"monitor_ids": []interface{}{"legacy-http"}}, nil, []string{"API", "Ping"}, 60, 0),
		},
		Mapping: models.FieldMapping{
			Name:            "name",
			Type:            "type",
			Config:          "config",
			IntervalSeconds: "interval_seconds",
			TimeoutSeconds:  "timeout_seconds",
			Enabled:         "enabled",
			GroupMembers:    "group_members",
		},
	})
	if err != nil {
		t.Fatalf("ExecuteImport() error = %v", err)
	}

	if result.SuccessCount != 2 || result.SkippedCount != 1 || result.FailedCount != 0 {
		t.Fatalf("unexpected import counts: %+v", result)
	}
	if result.Results[0].Status != "skipped" || !strings.Contains(result.Results[0].SkipReason, "already exists") {
		t.Fatalf("unexpected duplicate result: %+v", result.Results[0])
	}
	if len(monitorSvc.createRequests) != 2 {
		t.Fatalf("len(createRequests) = %d, want 2", len(monitorSvc.createRequests))
	}

	groupConfig := decodeConfig(t, monitorSvc.createRequests[1].Config)
	memberIDs, ok := groupConfig["monitor_ids"].([]interface{})
	if !ok || len(memberIDs) != 2 {
		t.Fatalf("group monitor_ids = %#v, want two member IDs", groupConfig["monitor_ids"])
	}
	if memberIDs[0] != existingAPIID.String() {
		t.Fatalf("first group member ID = %v, want existing API ID %s", memberIDs[0], existingAPIID)
	}
}

func TestExecuteImport_PortableBundleRowFailures(t *testing.T) {
	t.Run("missing alert policy fails row", func(t *testing.T) {
		tenantID := uuid.New()
		monitorSvc := &portableMonitorServiceMock{}

		sqlDB, sqlMock, err := sqlmock.New()
		if err != nil {
			t.Fatalf("sqlmock.New() error = %v", err)
		}
		defer sqlDB.Close()

		sqlMock.ExpectQuery(`(?s)SELECT id\s+FROM alert_policies\s+WHERE tenant_id = \$1 AND name = \$2`).
			WithArgs(tenantID, "Missing").
			WillReturnRows(sqlmock.NewRows([]string{"id"}))

		groupSvc := &groupMembershipMock{}
		svc := NewService(&db.Client{DB: sqlDB}, monitorSvc, groupSvc)
		result, err := svc.ExecuteImport(context.Background(), tenantID, &models.ImportExecuteRequest{
			Rows: []models.ImportRow{
				rowWithConfig(0, "API", "http", map[string]interface{}{"url": "https://example.com", "method": "GET"}, []string{"Missing"}, nil, 60, 30),
			},
			Mapping: models.FieldMapping{
				Name:             "name",
				Type:             "type",
				Config:           "config",
				IntervalSeconds:  "interval_seconds",
				TimeoutSeconds:   "timeout_seconds",
				AlertPolicyNames: "alert_policy_names",
			},
		})
		if err != nil {
			t.Fatalf("ExecuteImport() error = %v", err)
		}
		if result.FailedCount != 1 || !strings.Contains(result.Results[0].Error, "not found") {
			t.Fatalf("unexpected failure result: %+v", result.Results[0])
		}
		if err := sqlMock.ExpectationsWereMet(); err != nil {
			t.Fatalf("unmet SQL expectations: %v", err)
		}
	})

	t.Run("missing group member name fails row", func(t *testing.T) {
		tenantID := uuid.New()
		monitorSvc := &portableMonitorServiceMock{}
		groupSvc := &groupMembershipMock{}
		svc := NewService(nil, monitorSvc, groupSvc)

		result, err := svc.ExecuteImport(context.Background(), tenantID, &models.ImportExecuteRequest{
			Rows: []models.ImportRow{
				rowWithConfig(0, "Platform", "group", map[string]interface{}{"monitor_ids": []interface{}{"legacy-http"}}, nil, []string{"missing"}, 60, 0),
			},
			Mapping: models.FieldMapping{
				Name:            "name",
				Type:            "type",
				Config:          "config",
				IntervalSeconds: "interval_seconds",
				GroupMembers:    "group_members",
			},
		})
		if err != nil {
			t.Fatalf("ExecuteImport() error = %v", err)
		}
		if result.FailedCount != 1 || !strings.Contains(result.Results[0].Error, "could not be resolved") {
			t.Fatalf("unexpected failure result: %+v", result.Results[0])
		}
	})

	t.Run("duplicate group member name fails row", func(t *testing.T) {
		tenantID := uuid.New()
		monitorSvc := &portableMonitorServiceMock{}
		groupSvc := &groupMembershipMock{}
		svc := NewService(nil, monitorSvc, groupSvc)

		result, err := svc.ExecuteImport(context.Background(), tenantID, &models.ImportExecuteRequest{
			Rows: []models.ImportRow{
				rowWithConfig(0, "API", "http", map[string]interface{}{"url": "https://example.com", "method": "GET"}, nil, nil, 60, 30),
				rowWithConfig(1, "API", "ping", map[string]interface{}{"host": "example.com"}, nil, nil, 60, 30),
				rowWithConfig(2, "Platform", "group", map[string]interface{}{"monitor_ids": []interface{}{"legacy-http"}}, nil, []string{"API"}, 60, 0),
			},
			Mapping: models.FieldMapping{
				Name:            "name",
				Type:            "type",
				Config:          "config",
				IntervalSeconds: "interval_seconds",
				TimeoutSeconds:  "timeout_seconds",
				GroupMembers:    "group_members",
			},
		})
		if err != nil {
			t.Fatalf("ExecuteImport() error = %v", err)
		}
		if result.SuccessCount != 2 || result.FailedCount != 1 {
			t.Fatalf("unexpected import counts: %+v", result)
		}
		if !strings.Contains(result.Results[2].Error, "ambiguous") {
			t.Fatalf("unexpected duplicate-name failure: %+v", result.Results[2])
		}
	})
}

type anyArrayArg struct{}

func (anyArrayArg) Match(v driver.Value) bool {
	_, ok := v.(string)
	return ok
}

func rowWithConfig(index int, name, monitorType string, config map[string]interface{}, alertPolicyNames []string, groupMembers []string, intervalSeconds, timeoutSeconds int) models.ImportRow {
	fields := map[string]interface{}{
		"name":             name,
		"type":             monitorType,
		"config":           config,
		"interval_seconds": intervalSeconds,
		"timeout_seconds":  timeoutSeconds,
		"enabled":          true,
	}
	if len(alertPolicyNames) > 0 {
		fields["alert_policy_names"] = alertPolicyNames
	}
	if len(groupMembers) > 0 {
		fields["group_members"] = groupMembers
	}
	return models.ImportRow{Index: index, Fields: fields}
}

func decodeConfig(t *testing.T, raw json.RawMessage) map[string]interface{} {
	t.Helper()

	var decoded map[string]interface{}
	if err := json.Unmarshal(raw, &decoded); err != nil {
		t.Fatalf("failed to decode config: %v", err)
	}
	return decoded
}
