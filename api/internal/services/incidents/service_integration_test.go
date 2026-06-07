package incidents

import (
	"context"
	"database/sql"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/yassinebenameur/probara/api/internal/models"
	"github.com/yassinebenameur/probara/shared/statusupdates"
	"github.com/yassinebenameur/probara/shared/testutil"
)

func TestServiceCreateManualIncident(t *testing.T) {
	ctx := context.Background()
	dbClient, cleanup := testutil.SetupPostgresDB(ctx, t)
	defer cleanup()

	tenantID := testutil.InsertTenant(ctx, t, dbClient, "incidents")
	svc := NewService(dbClient, nil)

	incident, err := svc.CreateIncident(ctx, tenantID, &models.CreateIncidentRequest{
		Title:   "API outage",
		Summary: "Requests are failing for all regions.",
	})
	if err != nil {
		t.Fatalf("CreateIncident() error = %v", err)
	}
	if incident.State != models.IncidentStateInvestigating {
		t.Fatalf("state = %q, want %q", incident.State, models.IncidentStateInvestigating)
	}
	if incident.Severity != models.IncidentSeverityHigh {
		t.Fatalf("severity = %q, want %q", incident.Severity, models.IncidentSeverityHigh)
	}
	if incident.OwnerUserID != nil {
		t.Fatalf("owner_user_id = %v, want nil", incident.OwnerUserID)
	}
}

func TestServiceGetIncidentReturnsEmptyLinkedResourceSlices(t *testing.T) {
	ctx := context.Background()
	dbClient, cleanup := testutil.SetupPostgresDB(ctx, t)
	defer cleanup()

	tenantID := testutil.InsertTenant(ctx, t, dbClient, "incidents")
	svc := NewService(dbClient, nil)

	incident, err := svc.CreateIncident(ctx, tenantID, &models.CreateIncidentRequest{
		Title:   "API outage",
		Summary: "Requests are failing for all regions.",
	})
	if err != nil {
		t.Fatalf("CreateIncident() error = %v", err)
	}

	detail, err := svc.GetIncident(ctx, tenantID, incident.ID)
	if err != nil {
		t.Fatalf("GetIncident() error = %v", err)
	}
	if detail.Alerts == nil {
		t.Fatalf("alerts = nil, want empty slice")
	}
	if detail.Monitors == nil {
		t.Fatalf("monitors = nil, want empty slice")
	}
	if detail.Publications == nil {
		t.Fatalf("publications = nil, want empty slice")
	}
	if detail.Severity != models.IncidentSeverityHigh {
		t.Fatalf("severity = %q, want %q", detail.Severity, models.IncidentSeverityHigh)
	}
}

func TestServiceTransitionIncidentStateRejectsResolvedReopen(t *testing.T) {
	ctx := context.Background()
	dbClient, cleanup := testutil.SetupPostgresDB(ctx, t)
	defer cleanup()

	tenantID := testutil.InsertTenant(ctx, t, dbClient, "incidents")
	svc := NewService(dbClient, nil)

	incident, err := svc.CreateIncident(ctx, tenantID, &models.CreateIncidentRequest{
		Title:   "DNS outage",
		Summary: "Resolvers are timing out.",
	})
	if err != nil {
		t.Fatalf("CreateIncident() error = %v", err)
	}
	if _, err := svc.TransitionIncidentState(ctx, tenantID, incident.ID, &models.TransitionIncidentStateRequest{
		State: models.IncidentStateResolved,
	}); err != nil {
		t.Fatalf("TransitionIncidentState(resolve) error = %v", err)
	}
	if _, err := svc.TransitionIncidentState(ctx, tenantID, incident.ID, &models.TransitionIncidentStateRequest{
		State: models.IncidentStateMonitoring,
	}); err == nil {
		t.Fatalf("expected resolved incident reopen to fail")
	}
}

func TestServiceCreateIncidentTimelineEntryAppendsNonSystemEntry(t *testing.T) {
	ctx := context.Background()
	dbClient, cleanup := testutil.SetupPostgresDB(ctx, t)
	defer cleanup()

	tenantID := testutil.InsertTenant(ctx, t, dbClient, "incidents")
	svc := NewService(dbClient, nil)

	incident, err := svc.CreateIncident(ctx, tenantID, &models.CreateIncidentRequest{
		Title:   "Database latency",
		Summary: "Read queries are slower than expected.",
	})
	if err != nil {
		t.Fatalf("CreateIncident() error = %v", err)
	}
	createdUpdatedAt := incident.UpdatedAt

	if _, err := svc.CreateIncidentTimelineEntry(ctx, tenantID, incident.ID, &models.CreateIncidentTimelineEntryRequest{
		EntryType: models.IncidentTimelineEntryTypeInternalNote,
		Message:   "Investigating replica lag.",
	}); err != nil {
		t.Fatalf("CreateIncidentTimelineEntry() error = %v", err)
	}
	if _, err := svc.CreateIncidentTimelineEntry(ctx, tenantID, incident.ID, &models.CreateIncidentTimelineEntryRequest{
		EntryType: models.IncidentTimelineEntryTypePublicUpdate,
		Message:   "Customer impact is limited to one region.",
	}); err != nil {
		t.Fatalf("CreateIncidentTimelineEntry(public update) error = %v", err)
	}

	detail, err := svc.GetIncident(ctx, tenantID, incident.ID)
	if err != nil {
		t.Fatalf("GetIncident() error = %v", err)
	}
	if !detail.UpdatedAt.After(createdUpdatedAt) {
		t.Fatalf("updated_at = %v, want after %v", detail.UpdatedAt, createdUpdatedAt)
	}
	if len(detail.Timeline) != 3 {
		t.Fatalf("timeline length = %d, want %d", len(detail.Timeline), 3)
	}
	if detail.Timeline[0].EntryType != models.IncidentTimelineEntryTypePublicUpdate {
		t.Fatalf("timeline[0] type = %q, want %q", detail.Timeline[0].EntryType, models.IncidentTimelineEntryTypePublicUpdate)
	}
	if detail.Timeline[1].EntryType != models.IncidentTimelineEntryTypeInternalNote {
		t.Fatalf("timeline[1] type = %q, want %q", detail.Timeline[1].EntryType, models.IncidentTimelineEntryTypeInternalNote)
	}
	if detail.Timeline[2].EntryType != models.IncidentTimelineEntryTypeSystem {
		t.Fatalf("timeline[2] type = %q, want %q", detail.Timeline[2].EntryType, models.IncidentTimelineEntryTypeSystem)
	}
}

func TestServiceCreateIncidentTimelineEntryRejectsSystemEntry(t *testing.T) {
	ctx := context.Background()
	dbClient, cleanup := testutil.SetupPostgresDB(ctx, t)
	defer cleanup()

	tenantID := testutil.InsertTenant(ctx, t, dbClient, "incidents")
	svc := NewService(dbClient, nil)

	incident, err := svc.CreateIncident(ctx, tenantID, &models.CreateIncidentRequest{
		Title:   "Queue backlog",
		Summary: "Jobs are piling up.",
	})
	if err != nil {
		t.Fatalf("CreateIncident() error = %v", err)
	}

	if _, err := svc.CreateIncidentTimelineEntry(ctx, tenantID, incident.ID, &models.CreateIncidentTimelineEntryRequest{
		EntryType: models.IncidentTimelineEntryTypeSystem,
		Message:   "This should be rejected.",
	}); err == nil {
		t.Fatalf("expected system timeline entry to be rejected")
	}
}

func TestServiceListIncidentsReturnsSummaryFields(t *testing.T) {
	ctx := context.Background()
	dbClient, cleanup := testutil.SetupPostgresDB(ctx, t)
	defer cleanup()

	tenantID := testutil.InsertTenant(ctx, t, dbClient, "incidents")
	svc := NewService(dbClient, nil)

	manualIncident, err := svc.CreateIncident(ctx, tenantID, &models.CreateIncidentRequest{
		Title:   "Manual outage",
		Summary: "Operators are coordinating a response.",
	})
	if err != nil {
		t.Fatalf("CreateIncident() error = %v", err)
	}

	monitorID := testutil.InsertHTTPMonitor(ctx, t, dbClient, tenantID, "incident-monitor")
	policyID := uuid.New()
	if _, err := dbClient.ExecContext(ctx, `
		INSERT INTO alert_policies (id, tenant_id, name, failure_threshold, failure_window_seconds, created_at, updated_at)
		VALUES ($1, $2, 'incident-policy', 1, 60, NOW(), NOW())
	`, policyID, tenantID); err != nil {
		t.Fatalf("insert alert policy: %v", err)
	}
	alertID := uuid.New()
	if _, err := dbClient.ExecContext(ctx, `
		INSERT INTO alerts (id, tenant_id, monitor_id, alert_policy_id, status, triggered_at, failure_count, created_at, updated_at)
		VALUES ($1, $2, $3, $4, 'active', NOW(), 1, NOW(), NOW())
	`, alertID, tenantID, monitorID, policyID); err != nil {
		t.Fatalf("insert alert: %v", err)
	}
	statusPageID := testutil.InsertStatusPage(ctx, t, dbClient, tenantID, "incident-page", "Incident Page")
	if _, err := dbClient.ExecContext(ctx, `
		INSERT INTO incident_alerts (incident_id, alert_id, created_at)
		VALUES ($1, $2, NOW())
	`, manualIncident.ID, alertID); err != nil {
		t.Fatalf("insert incident alert: %v", err)
	}
	if _, err := dbClient.ExecContext(ctx, `
		INSERT INTO incident_monitors (incident_id, monitor_id, created_at)
		VALUES ($1, $2, NOW())
	`, manualIncident.ID, monitorID); err != nil {
		t.Fatalf("insert incident monitor: %v", err)
	}
	if _, err := dbClient.ExecContext(ctx, `
		INSERT INTO incident_status_page_publications (
			incident_id, status_page_id, tenant_id, published_at, created_at, updated_at
		) VALUES ($1, $2, $3, NOW(), NOW(), NOW())
	`, manualIncident.ID, statusPageID, tenantID); err != nil {
		t.Fatalf("insert incident publication: %v", err)
	}

	manualUpdatedAt := time.Date(2026, time.March, 26, 21, 0, 0, 0, time.UTC)
	if _, err := dbClient.ExecContext(ctx, `
		UPDATE incidents
		SET updated_at = $1
		WHERE id = $2 AND tenant_id = $3
	`, manualUpdatedAt, manualIncident.ID, tenantID); err != nil {
		t.Fatalf("update manual incident updated_at: %v", err)
	}

	autoIncidentID := uuid.New()
	autoUpdatedAt := time.Date(2026, time.March, 26, 20, 0, 0, 0, time.UTC)
	if _, err := dbClient.ExecContext(ctx, `
		INSERT INTO incidents (
			id, tenant_id, title, summary, state, is_auto_created, auto_monitor_id, auto_alert_policy_id,
			created_at, updated_at
		) VALUES ($1, $2, 'Auto incident', 'Created from alert policy.', 'investigating', TRUE, $3, $4, NOW(), $5)
	`, autoIncidentID, tenantID, monitorID, policyID, autoUpdatedAt); err != nil {
		t.Fatalf("insert auto incident: %v", err)
	}

	list, err := svc.ListIncidents(ctx, tenantID, 1, 20)
	if err != nil {
		t.Fatalf("ListIncidents() error = %v", err)
	}
	if len(list.Items) != 2 {
		t.Fatalf("list length = %d, want %d", len(list.Items), 2)
	}

	if list.Items[0].ID != manualIncident.ID {
		t.Fatalf("list.Items[0].ID = %s, want %s", list.Items[0].ID, manualIncident.ID)
	}
	if list.Items[0].Source != models.IncidentSourceManual {
		t.Fatalf("list.Items[0].Source = %q, want %q", list.Items[0].Source, models.IncidentSourceManual)
	}
	if list.Items[0].Severity != models.IncidentSeverityHigh {
		t.Fatalf("list.Items[0].Severity = %q, want %q", list.Items[0].Severity, models.IncidentSeverityHigh)
	}
	if list.Items[0].OwnerUserID != nil {
		t.Fatalf("list.Items[0].OwnerUserID = %v, want nil", list.Items[0].OwnerUserID)
	}
	if list.Items[0].LinkedAlertCount != 1 || list.Items[0].LinkedMonitorCount != 1 || list.Items[0].PublicationCount != 1 {
		t.Fatalf("list.Items[0] counts = (%d,%d,%d), want (1,1,1)", list.Items[0].LinkedAlertCount, list.Items[0].LinkedMonitorCount, list.Items[0].PublicationCount)
	}
	if list.Items[1].ID != autoIncidentID {
		t.Fatalf("list.Items[1].ID = %s, want %s", list.Items[1].ID, autoIncidentID)
	}
	if list.Items[1].Source != models.IncidentSourceAuto {
		t.Fatalf("list.Items[1].Source = %q, want %q", list.Items[1].Source, models.IncidentSourceAuto)
	}
	if list.Items[1].LinkedAlertCount != 0 || list.Items[1].LinkedMonitorCount != 0 || list.Items[1].PublicationCount != 0 {
		t.Fatalf("list.Items[1] counts = (%d,%d,%d), want (0,0,0)", list.Items[1].LinkedAlertCount, list.Items[1].LinkedMonitorCount, list.Items[1].PublicationCount)
	}
}

func TestServiceTransitionIncidentStateRejectsConcurrentResolvedReopen(t *testing.T) {
	ctx := context.Background()
	dbClient, cleanup := testutil.SetupPostgresDB(ctx, t)
	defer cleanup()

	tenantID := testutil.InsertTenant(ctx, t, dbClient, "incidents")
	svc := NewService(dbClient, nil)

	incident, err := svc.CreateIncident(ctx, tenantID, &models.CreateIncidentRequest{
		Title:   "API outage",
		Summary: "Requests are failing across regions.",
	})
	if err != nil {
		t.Fatalf("CreateIncident() error = %v", err)
	}

	tx, err := dbClient.BeginTx(ctx, nil)
	if err != nil {
		t.Fatalf("BeginTx() error = %v", err)
	}
	defer tx.Rollback()

	if _, err := tx.ExecContext(ctx, `
		UPDATE incidents
		SET state = 'resolved', resolved_at = NOW(), updated_at = NOW()
		WHERE id = $1 AND tenant_id = $2
	`, incident.ID, tenantID); err != nil {
		t.Fatalf("seed resolved incident in transaction: %v", err)
	}

	resultCh := make(chan error, 1)
	go func() {
		_, err := svc.TransitionIncidentState(ctx, tenantID, incident.ID, &models.TransitionIncidentStateRequest{
			State: models.IncidentStateMonitoring,
		})
		resultCh <- err
	}()

	select {
	case err := <-resultCh:
		t.Fatalf("transition returned before concurrent transaction committed: %v", err)
	case <-time.After(250 * time.Millisecond):
	}

	if err := tx.Commit(); err != nil {
		t.Fatalf("Commit() error = %v", err)
	}

	select {
	case err := <-resultCh:
		if err == nil {
			t.Fatalf("expected concurrent resolved reopen to fail")
		}
	case <-time.After(10 * time.Second):
		t.Fatalf("timeout waiting for transition result")
	}
}

func TestServiceUpdateIncidentRejectsBlankNarrativeFields(t *testing.T) {
	ctx := context.Background()
	dbClient, cleanup := testutil.SetupPostgresDB(ctx, t)
	defer cleanup()

	tenantID := testutil.InsertTenant(ctx, t, dbClient, "incidents")
	svc := NewService(dbClient, nil)

	incident, err := svc.CreateIncident(ctx, tenantID, &models.CreateIncidentRequest{
		Title:   "Storage degradation",
		Summary: "Latency is increasing.",
	})
	if err != nil {
		t.Fatalf("CreateIncident() error = %v", err)
	}

	blankTitle := "   "
	if _, err := svc.UpdateIncident(ctx, tenantID, incident.ID, &models.UpdateIncidentRequest{
		Title: &blankTitle,
	}); err == nil {
		t.Fatalf("expected blank title update to fail")
	}

	blankSummary := "\t"
	if _, err := svc.UpdateIncident(ctx, tenantID, incident.ID, &models.UpdateIncidentRequest{
		Summary: &blankSummary,
	}); err == nil {
		t.Fatalf("expected blank summary update to fail")
	}
}

func TestServicePublishIncidentToStatusPageRejectsUnlinkedMonitor(t *testing.T) {
	ctx := context.Background()
	dbClient, cleanup := testutil.SetupPostgresDB(ctx, t)
	defer cleanup()

	tenantID := testutil.InsertTenant(ctx, t, dbClient, "tenant")
	monitorID := testutil.InsertHTTPMonitor(ctx, t, dbClient, tenantID, "API")
	statusPageID := testutil.InsertStatusPage(ctx, t, dbClient, tenantID, "status", "Status")
	testutil.AddMonitorToStatusPage(ctx, t, dbClient, statusPageID, monitorID, 0)

	svc := NewService(dbClient, nil)
	incident, err := svc.CreateIncident(ctx, tenantID, &models.CreateIncidentRequest{
		Title:   "API outage",
		Summary: "Down",
	})
	if err != nil {
		t.Fatalf("CreateIncident() error = %v", err)
	}

	_, err = svc.PublishIncidentToStatusPage(ctx, tenantID, incident.ID, statusPageID, &models.UpsertIncidentPublicationRequest{
		MonitorIDs: []string{monitorID.String()},
	})
	if err == nil {
		t.Fatalf("expected publish to reject unlinked incident monitor")
	}
}

func TestServiceAttachAlertAndDetachFlowSkipsTimelineForNoOps(t *testing.T) {
	ctx := context.Background()
	dbClient, cleanup := testutil.SetupPostgresDB(ctx, t)
	defer cleanup()

	tenantID := testutil.InsertTenant(ctx, t, dbClient, "incidents")
	monitorID := testutil.InsertHTTPMonitor(ctx, t, dbClient, tenantID, "API")
	alertID := insertIncidentTestAlert(ctx, t, dbClient, tenantID, monitorID)
	svc := NewService(dbClient, nil)

	incident, err := svc.CreateIncident(ctx, tenantID, &models.CreateIncidentRequest{
		Title:   "API outage",
		Summary: "Requests are failing.",
	})
	if err != nil {
		t.Fatalf("CreateIncident() error = %v", err)
	}

	detail, err := svc.AttachAlert(ctx, tenantID, incident.ID, alertID)
	if err != nil {
		t.Fatalf("AttachAlert() error = %v", err)
	}
	if len(detail.Timeline) != 2 {
		t.Fatalf("timeline length after first attach = %d, want %d", len(detail.Timeline), 2)
	}
	if count := countIncidentAlertLinks(ctx, t, dbClient, incident.ID); count != 1 {
		t.Fatalf("incident alert count after first attach = %d, want %d", count, 1)
	}

	detail, err = svc.AttachAlert(ctx, tenantID, incident.ID, alertID)
	if err != nil {
		t.Fatalf("AttachAlert() duplicate error = %v", err)
	}
	if len(detail.Timeline) != 2 {
		t.Fatalf("timeline length after duplicate attach = %d, want %d", len(detail.Timeline), 2)
	}
	if count := countIncidentAlertLinks(ctx, t, dbClient, incident.ID); count != 1 {
		t.Fatalf("incident alert count after duplicate attach = %d, want %d", count, 1)
	}

	detail, err = svc.DetachAlert(ctx, tenantID, incident.ID, alertID)
	if err != nil {
		t.Fatalf("DetachAlert() error = %v", err)
	}
	if len(detail.Timeline) != 3 {
		t.Fatalf("timeline length after first detach = %d, want %d", len(detail.Timeline), 3)
	}
	if count := countIncidentAlertLinks(ctx, t, dbClient, incident.ID); count != 0 {
		t.Fatalf("incident alert count after first detach = %d, want %d", count, 0)
	}

	detail, err = svc.DetachAlert(ctx, tenantID, incident.ID, alertID)
	if err != nil {
		t.Fatalf("DetachAlert() duplicate error = %v", err)
	}
	if len(detail.Timeline) != 3 {
		t.Fatalf("timeline length after duplicate detach = %d, want %d", len(detail.Timeline), 3)
	}
	if count := countIncidentAlertLinks(ctx, t, dbClient, incident.ID); count != 0 {
		t.Fatalf("incident alert count after duplicate detach = %d, want %d", count, 0)
	}
}

func TestServiceAttachMonitorSkipsTimelineForDuplicateAttach(t *testing.T) {
	ctx := context.Background()
	dbClient, cleanup := testutil.SetupPostgresDB(ctx, t)
	defer cleanup()

	tenantID := testutil.InsertTenant(ctx, t, dbClient, "incidents")
	monitorID := testutil.InsertHTTPMonitor(ctx, t, dbClient, tenantID, "API")
	svc := NewService(dbClient, nil)

	incident, err := svc.CreateIncident(ctx, tenantID, &models.CreateIncidentRequest{
		Title:   "API outage",
		Summary: "Requests are failing.",
	})
	if err != nil {
		t.Fatalf("CreateIncident() error = %v", err)
	}

	detail, err := svc.AttachMonitor(ctx, tenantID, incident.ID, monitorID)
	if err != nil {
		t.Fatalf("AttachMonitor() error = %v", err)
	}
	if len(detail.Timeline) != 2 {
		t.Fatalf("timeline length after first attach = %d, want %d", len(detail.Timeline), 2)
	}
	if count := countIncidentMonitorLinks(ctx, t, dbClient, incident.ID); count != 1 {
		t.Fatalf("incident monitor count after first attach = %d, want %d", count, 1)
	}

	detail, err = svc.AttachMonitor(ctx, tenantID, incident.ID, monitorID)
	if err != nil {
		t.Fatalf("AttachMonitor() duplicate error = %v", err)
	}
	if len(detail.Timeline) != 2 {
		t.Fatalf("timeline length after duplicate attach = %d, want %d", len(detail.Timeline), 2)
	}
	if count := countIncidentMonitorLinks(ctx, t, dbClient, incident.ID); count != 1 {
		t.Fatalf("incident monitor count after duplicate attach = %d, want %d", count, 1)
	}
}

func TestServicePublishIncidentToStatusPageWithEmptyMonitorList(t *testing.T) {
	ctx := context.Background()
	dbClient, cleanup := testutil.SetupPostgresDB(ctx, t)
	defer cleanup()

	tenantID := testutil.InsertTenant(ctx, t, dbClient, "incidents")
	statusPageID := testutil.InsertStatusPage(ctx, t, dbClient, tenantID, "status", "Status")
	svc := NewService(dbClient, nil)

	incident, err := svc.CreateIncident(ctx, tenantID, &models.CreateIncidentRequest{
		Title:   "API outage",
		Summary: "Requests are failing.",
	})
	if err != nil {
		t.Fatalf("CreateIncident() error = %v", err)
	}

	detail, err := svc.PublishIncidentToStatusPage(ctx, tenantID, incident.ID, statusPageID, &models.UpsertIncidentPublicationRequest{})
	if err != nil {
		t.Fatalf("PublishIncidentToStatusPage() error = %v", err)
	}
	if len(detail.Timeline) != 2 {
		t.Fatalf("timeline length after publish = %d, want %d", len(detail.Timeline), 2)
	}

	publication, err := loadIncidentPublication(ctx, dbClient, incident.ID, statusPageID)
	if err != nil {
		t.Fatalf("loadIncidentPublication() error = %v", err)
	}
	if publication.unpublishedAt.Valid {
		t.Fatalf("publication unexpectedly unpublished at %v", publication.unpublishedAt.Time)
	}
	if count := countIncidentPublicationMonitors(ctx, t, dbClient, incident.ID, statusPageID); count != 0 {
		t.Fatalf("publication monitor count = %d, want %d", count, 0)
	}
}

func TestServicePublishIncidentToStatusPageWithValidMonitorSelection(t *testing.T) {
	ctx := context.Background()
	dbClient, cleanup := testutil.SetupPostgresDB(ctx, t)
	defer cleanup()

	tenantID := testutil.InsertTenant(ctx, t, dbClient, "incidents")
	monitorID := testutil.InsertHTTPMonitor(ctx, t, dbClient, tenantID, "API")
	statusPageID := testutil.InsertStatusPage(ctx, t, dbClient, tenantID, "status", "Status")
	testutil.AddMonitorToStatusPage(ctx, t, dbClient, statusPageID, monitorID, 0)
	svc := NewService(dbClient, nil)

	incident, err := svc.CreateIncident(ctx, tenantID, &models.CreateIncidentRequest{
		Title:   "API outage",
		Summary: "Requests are failing.",
	})
	if err != nil {
		t.Fatalf("CreateIncident() error = %v", err)
	}
	if _, err := svc.AttachMonitor(ctx, tenantID, incident.ID, monitorID); err != nil {
		t.Fatalf("AttachMonitor() error = %v", err)
	}

	detail, err := svc.PublishIncidentToStatusPage(ctx, tenantID, incident.ID, statusPageID, &models.UpsertIncidentPublicationRequest{
		MonitorIDs: []string{monitorID.String()},
	})
	if err != nil {
		t.Fatalf("PublishIncidentToStatusPage() error = %v", err)
	}
	if len(detail.Timeline) != 3 {
		t.Fatalf("timeline length after publish = %d, want %d", len(detail.Timeline), 3)
	}
	if count := countIncidentPublicationMonitors(ctx, t, dbClient, incident.ID, statusPageID); count != 1 {
		t.Fatalf("publication monitor count = %d, want %d", count, 1)
	}
}

func TestServiceGetIncidentIncludesLinkedResources(t *testing.T) {
	ctx := context.Background()
	dbClient, cleanup := testutil.SetupPostgresDB(ctx, t)
	defer cleanup()

	tenantID := testutil.InsertTenant(ctx, t, dbClient, "incidents")
	monitorID := testutil.InsertHTTPMonitor(ctx, t, dbClient, tenantID, "API")
	alertID := insertIncidentTestAlert(ctx, t, dbClient, tenantID, monitorID)
	statusPageID := testutil.InsertStatusPage(ctx, t, dbClient, tenantID, "status", "Status")
	testutil.AddMonitorToStatusPage(ctx, t, dbClient, statusPageID, monitorID, 0)
	svc := NewService(dbClient, nil)

	incident, err := svc.CreateIncident(ctx, tenantID, &models.CreateIncidentRequest{
		Title:   "API outage",
		Summary: "Requests are failing.",
	})
	if err != nil {
		t.Fatalf("CreateIncident() error = %v", err)
	}
	if _, err := svc.AttachAlert(ctx, tenantID, incident.ID, alertID); err != nil {
		t.Fatalf("AttachAlert() error = %v", err)
	}
	if _, err := svc.AttachMonitor(ctx, tenantID, incident.ID, monitorID); err != nil {
		t.Fatalf("AttachMonitor() error = %v", err)
	}
	if _, err := svc.PublishIncidentToStatusPage(ctx, tenantID, incident.ID, statusPageID, &models.UpsertIncidentPublicationRequest{
		MonitorIDs: []string{monitorID.String()},
	}); err != nil {
		t.Fatalf("PublishIncidentToStatusPage() error = %v", err)
	}

	detail, err := svc.GetIncident(ctx, tenantID, incident.ID)
	if err != nil {
		t.Fatalf("GetIncident() error = %v", err)
	}
	if len(detail.Alerts) != 1 {
		t.Fatalf("alerts len = %d, want 1", len(detail.Alerts))
	}
	if detail.Alerts[0].ID != alertID {
		t.Fatalf("alert id = %s, want %s", detail.Alerts[0].ID, alertID)
	}
	if len(detail.Monitors) != 1 {
		t.Fatalf("monitors len = %d, want 1", len(detail.Monitors))
	}
	if detail.Monitors[0].ID != monitorID {
		t.Fatalf("monitor id = %s, want %s", detail.Monitors[0].ID, monitorID)
	}
	if len(detail.Publications) != 1 {
		t.Fatalf("publications len = %d, want 1", len(detail.Publications))
	}
	if detail.Publications[0].StatusPageID != statusPageID {
		t.Fatalf("status page id = %s, want %s", detail.Publications[0].StatusPageID, statusPageID)
	}
	if detail.Publications[0].StatusPageSlug != "status" {
		t.Fatalf("status page slug = %q, want %q", detail.Publications[0].StatusPageSlug, "status")
	}
	if len(detail.Publications[0].MonitorIDs) != 1 || detail.Publications[0].MonitorIDs[0] != monitorID {
		t.Fatalf("publication monitor ids = %v, want [%s]", detail.Publications[0].MonitorIDs, monitorID)
	}
}

func TestServicePublishIncidentToStatusPageEmitsDirectRefreshEvent(t *testing.T) {
	ctx := context.Background()
	dbClient, cleanup := testutil.SetupPostgresDB(ctx, t)
	defer cleanup()

	tenantID := testutil.InsertTenant(ctx, t, dbClient, "incidents")
	statusPageID := testutil.InsertStatusPage(ctx, t, dbClient, tenantID, "status", "Status")
	publisher := &fakeIncidentStatusUpdatePublisher{}
	svc := NewService(dbClient, publisher)

	incident, err := svc.CreateIncident(ctx, tenantID, &models.CreateIncidentRequest{
		Title:   "API outage",
		Summary: "Requests are failing.",
	})
	if err != nil {
		t.Fatalf("CreateIncident() error = %v", err)
	}

	if _, err := svc.PublishIncidentToStatusPage(ctx, tenantID, incident.ID, statusPageID, &models.UpsertIncidentPublicationRequest{}); err != nil {
		t.Fatalf("PublishIncidentToStatusPage() error = %v", err)
	}

	events := publisher.Events()
	if len(events) != 1 {
		t.Fatalf("published events = %d, want %d", len(events), 1)
	}
	if events[0].Type != "incident.publication.updated" {
		t.Fatalf("event type = %q, want %q", events[0].Type, "incident.publication.updated")
	}
	if events[0].TenantID != tenantID.String() {
		t.Fatalf("event tenant_id = %q, want %q", events[0].TenantID, tenantID.String())
	}
	if events[0].StatusPageID != statusPageID.String() {
		t.Fatalf("event status_page_id = %q, want %q", events[0].StatusPageID, statusPageID.String())
	}
}

func TestServicePublishIncidentToStatusPageSkipsNoOpIdenticalPut(t *testing.T) {
	ctx := context.Background()
	dbClient, cleanup := testutil.SetupPostgresDB(ctx, t)
	defer cleanup()

	tenantID := testutil.InsertTenant(ctx, t, dbClient, "incidents")
	monitorID := testutil.InsertHTTPMonitor(ctx, t, dbClient, tenantID, "API")
	statusPageID := testutil.InsertStatusPage(ctx, t, dbClient, tenantID, "status", "Status")
	testutil.AddMonitorToStatusPage(ctx, t, dbClient, statusPageID, monitorID, 0)
	publisher := &fakeIncidentStatusUpdatePublisher{}
	svc := NewService(dbClient, publisher)

	incident, err := svc.CreateIncident(ctx, tenantID, &models.CreateIncidentRequest{
		Title:   "API outage",
		Summary: "Requests are failing.",
	})
	if err != nil {
		t.Fatalf("CreateIncident() error = %v", err)
	}
	if _, err := svc.AttachMonitor(ctx, tenantID, incident.ID, monitorID); err != nil {
		t.Fatalf("AttachMonitor() error = %v", err)
	}

	req := &models.UpsertIncidentPublicationRequest{
		MonitorIDs: []string{monitorID.String()},
	}
	detail, err := svc.PublishIncidentToStatusPage(ctx, tenantID, incident.ID, statusPageID, req)
	if err != nil {
		t.Fatalf("PublishIncidentToStatusPage() first call error = %v", err)
	}
	if len(detail.Timeline) != 3 {
		t.Fatalf("timeline length after first publish = %d, want %d", len(detail.Timeline), 3)
	}
	if got := len(publisher.Events()); got != 1 {
		t.Fatalf("event count after first publish = %d, want %d", got, 1)
	}

	firstPublication, err := loadIncidentPublication(ctx, dbClient, incident.ID, statusPageID)
	if err != nil {
		t.Fatalf("loadIncidentPublication() after first publish error = %v", err)
	}

	time.Sleep(10 * time.Millisecond)

	detail, err = svc.PublishIncidentToStatusPage(ctx, tenantID, incident.ID, statusPageID, req)
	if err != nil {
		t.Fatalf("PublishIncidentToStatusPage() second call error = %v", err)
	}
	if len(detail.Timeline) != 3 {
		t.Fatalf("timeline length after identical publish = %d, want %d", len(detail.Timeline), 3)
	}
	if got := len(publisher.Events()); got != 1 {
		t.Fatalf("event count after no-op publish = %d, want %d", got, 1)
	}

	secondPublication, err := loadIncidentPublication(ctx, dbClient, incident.ID, statusPageID)
	if err != nil {
		t.Fatalf("loadIncidentPublication() after second publish error = %v", err)
	}
	if !secondPublication.publishedAt.Equal(firstPublication.publishedAt) {
		t.Fatalf("published_at changed on no-op publish: got %v want %v", secondPublication.publishedAt, firstPublication.publishedAt)
	}
	if !secondPublication.updatedAt.Equal(firstPublication.updatedAt) {
		t.Fatalf("updated_at changed on no-op publish: got %v want %v", secondPublication.updatedAt, firstPublication.updatedAt)
	}
	if count := countIncidentPublicationMonitors(ctx, t, dbClient, incident.ID, statusPageID); count != 1 {
		t.Fatalf("publication monitor count after identical publish = %d, want %d", count, 1)
	}
}

func TestServiceUnpublishIncidentFromStatusPageSkipsTimelineForNoOp(t *testing.T) {
	ctx := context.Background()
	dbClient, cleanup := testutil.SetupPostgresDB(ctx, t)
	defer cleanup()

	tenantID := testutil.InsertTenant(ctx, t, dbClient, "incidents")
	monitorID := testutil.InsertHTTPMonitor(ctx, t, dbClient, tenantID, "API")
	statusPageID := testutil.InsertStatusPage(ctx, t, dbClient, tenantID, "status", "Status")
	testutil.AddMonitorToStatusPage(ctx, t, dbClient, statusPageID, monitorID, 0)
	publisher := &fakeIncidentStatusUpdatePublisher{}
	svc := NewService(dbClient, publisher)

	incident, err := svc.CreateIncident(ctx, tenantID, &models.CreateIncidentRequest{
		Title:   "API outage",
		Summary: "Requests are failing.",
	})
	if err != nil {
		t.Fatalf("CreateIncident() error = %v", err)
	}
	if _, err := svc.AttachMonitor(ctx, tenantID, incident.ID, monitorID); err != nil {
		t.Fatalf("AttachMonitor() error = %v", err)
	}
	if _, err := svc.PublishIncidentToStatusPage(ctx, tenantID, incident.ID, statusPageID, &models.UpsertIncidentPublicationRequest{
		MonitorIDs: []string{monitorID.String()},
	}); err != nil {
		t.Fatalf("PublishIncidentToStatusPage() error = %v", err)
	}
	publisher.Reset()

	detail, err := svc.UnpublishIncidentFromStatusPage(ctx, tenantID, incident.ID, statusPageID)
	if err != nil {
		t.Fatalf("UnpublishIncidentFromStatusPage() error = %v", err)
	}
	if len(detail.Timeline) != 4 {
		t.Fatalf("timeline length after first unpublish = %d, want %d", len(detail.Timeline), 4)
	}
	if got := len(publisher.Events()); got != 1 {
		t.Fatalf("event count after first unpublish = %d, want %d", got, 1)
	}
	publication, err := loadIncidentPublication(ctx, dbClient, incident.ID, statusPageID)
	if err != nil {
		t.Fatalf("loadIncidentPublication() after first unpublish error = %v", err)
	}
	if !publication.unpublishedAt.Valid {
		t.Fatalf("expected publication to be unpublished")
	}
	if count := countIncidentPublicationMonitors(ctx, t, dbClient, incident.ID, statusPageID); count != 0 {
		t.Fatalf("publication monitor count after first unpublish = %d, want %d", count, 0)
	}

	detail, err = svc.UnpublishIncidentFromStatusPage(ctx, tenantID, incident.ID, statusPageID)
	if err != nil {
		t.Fatalf("UnpublishIncidentFromStatusPage() duplicate error = %v", err)
	}
	if len(detail.Timeline) != 4 {
		t.Fatalf("timeline length after duplicate unpublish = %d, want %d", len(detail.Timeline), 4)
	}
	if got := len(publisher.Events()); got != 1 {
		t.Fatalf("event count after no-op unpublish = %d, want %d", got, 1)
	}
}

func TestServiceUnpublishIncidentFromStatusPageEmitsDirectRefreshEvent(t *testing.T) {
	ctx := context.Background()
	dbClient, cleanup := testutil.SetupPostgresDB(ctx, t)
	defer cleanup()

	tenantID := testutil.InsertTenant(ctx, t, dbClient, "incidents")
	statusPageID := testutil.InsertStatusPage(ctx, t, dbClient, tenantID, "status", "Status")
	publisher := &fakeIncidentStatusUpdatePublisher{}
	svc := NewService(dbClient, publisher)

	incident, err := svc.CreateIncident(ctx, tenantID, &models.CreateIncidentRequest{
		Title:   "API outage",
		Summary: "Requests are failing.",
	})
	if err != nil {
		t.Fatalf("CreateIncident() error = %v", err)
	}
	if _, err := svc.PublishIncidentToStatusPage(ctx, tenantID, incident.ID, statusPageID, &models.UpsertIncidentPublicationRequest{}); err != nil {
		t.Fatalf("PublishIncidentToStatusPage() error = %v", err)
	}
	publisher.Reset()

	if _, err := svc.UnpublishIncidentFromStatusPage(ctx, tenantID, incident.ID, statusPageID); err != nil {
		t.Fatalf("UnpublishIncidentFromStatusPage() error = %v", err)
	}

	events := publisher.Events()
	if len(events) != 1 {
		t.Fatalf("published events = %d, want %d", len(events), 1)
	}
	if events[0].Type != "incident.publication.updated" {
		t.Fatalf("event type = %q, want %q", events[0].Type, "incident.publication.updated")
	}
	if events[0].StatusPageID != statusPageID.String() {
		t.Fatalf("event status_page_id = %q, want %q", events[0].StatusPageID, statusPageID.String())
	}
}

func TestServiceCreateIncidentTimelineEntryPublicUpdateEmitsDirectRefreshEvents(t *testing.T) {
	ctx := context.Background()
	dbClient, cleanup := testutil.SetupPostgresDB(ctx, t)
	defer cleanup()

	tenantID := testutil.InsertTenant(ctx, t, dbClient, "incidents")
	statusPageIDOne := testutil.InsertStatusPage(ctx, t, dbClient, tenantID, "status-one", "Status One")
	statusPageIDTwo := testutil.InsertStatusPage(ctx, t, dbClient, tenantID, "status-two", "Status Two")
	publisher := &fakeIncidentStatusUpdatePublisher{}
	svc := NewService(dbClient, publisher)

	incident, err := svc.CreateIncident(ctx, tenantID, &models.CreateIncidentRequest{
		Title:   "API outage",
		Summary: "Requests are failing.",
	})
	if err != nil {
		t.Fatalf("CreateIncident() error = %v", err)
	}
	if _, err := svc.PublishIncidentToStatusPage(ctx, tenantID, incident.ID, statusPageIDOne, &models.UpsertIncidentPublicationRequest{}); err != nil {
		t.Fatalf("PublishIncidentToStatusPage(statusPageIDOne) error = %v", err)
	}
	if _, err := svc.PublishIncidentToStatusPage(ctx, tenantID, incident.ID, statusPageIDTwo, &models.UpsertIncidentPublicationRequest{}); err != nil {
		t.Fatalf("PublishIncidentToStatusPage(statusPageIDTwo) error = %v", err)
	}
	publisher.Reset()

	if _, err := svc.CreateIncidentTimelineEntry(ctx, tenantID, incident.ID, &models.CreateIncidentTimelineEntryRequest{
		EntryType: models.IncidentTimelineEntryTypePublicUpdate,
		Message:   "Investigating mitigation.",
	}); err != nil {
		t.Fatalf("CreateIncidentTimelineEntry(public update) error = %v", err)
	}

	events := publisher.Events()
	if len(events) != 2 {
		t.Fatalf("published events = %d, want %d", len(events), 2)
	}
	gotStatusPageIDs := map[string]struct{}{}
	for _, event := range events {
		if event.Type != "incident.publication.updated" {
			t.Fatalf("event type = %q, want %q", event.Type, "incident.publication.updated")
		}
		if event.TenantID != tenantID.String() {
			t.Fatalf("event tenant_id = %q, want %q", event.TenantID, tenantID.String())
		}
		gotStatusPageIDs[event.StatusPageID] = struct{}{}
	}
	if _, ok := gotStatusPageIDs[statusPageIDOne.String()]; !ok {
		t.Fatalf("missing status page refresh for %s", statusPageIDOne)
	}
	if _, ok := gotStatusPageIDs[statusPageIDTwo.String()]; !ok {
		t.Fatalf("missing status page refresh for %s", statusPageIDTwo)
	}
}

func TestServiceEnsureIncidentForAlertCreatesAndReusesAutoIncident(t *testing.T) {
	// Verifies that after the first alert resolves, a new alert for the same
	// monitor+policy reuses the still-open auto-incident rather than creating a
	// duplicate. Under the one-open-alert-per-monitor invariant, the first alert
	// must be resolved before the second can be inserted on the same monitor.
	ctx := context.Background()
	dbClient, cleanup := testutil.SetupPostgresDB(ctx, t)
	defer cleanup()

	tenantID := testutil.InsertTenant(ctx, t, dbClient, "incidents")
	monitorID := testutil.InsertHTTPMonitor(ctx, t, dbClient, tenantID, "API")
	policyID := insertIncidentTestPolicy(ctx, t, dbClient, tenantID, "auto-policy", true)
	svc := NewService(dbClient, nil)

	firstAlertID := insertIncidentTestAlertWithPolicy(ctx, t, dbClient, tenantID, monitorID, policyID)
	firstAlert := &models.AlertWithDetails{
		Alert: models.Alert{
			ID:            firstAlertID,
			TenantID:      tenantID,
			MonitorID:     monitorID,
			AlertPolicyID: policyID,
			Status:        models.AlertStatusActive,
		},
		MonitorName: "API",
		PolicyName:  "auto-policy",
	}
	if err := svc.EnsureIncidentForAlert(ctx, tenantID, firstAlert); err != nil {
		t.Fatalf("EnsureIncidentForAlert(first) error = %v", err)
	}

	incidentID := loadAutoIncidentID(ctx, t, dbClient, tenantID, monitorID, policyID)
	detail, err := svc.GetIncident(ctx, tenantID, incidentID)
	if err != nil {
		t.Fatalf("GetIncident() error = %v", err)
	}
	if !detail.IsAutoCreated {
		t.Fatalf("expected auto-created incident")
	}
	if count := countIncidentAlertLinks(ctx, t, dbClient, incidentID); count != 1 {
		t.Fatalf("incident alert count after first ensure = %d, want 1", count)
	}
	if count := countIncidentMonitorLinks(ctx, t, dbClient, incidentID); count != 1 {
		t.Fatalf("incident monitor count after first ensure = %d, want 1", count)
	}
	if len(detail.Timeline) != 1 || !hasTimelineMessage(detail.Timeline, autoIncidentCreatedMessage) {
		t.Fatalf("unexpected first timeline = %#v", detail.Timeline)
	}

	// Resolve the first alert so the monitor has no open alert. The incident
	// remains open (state = investigating) until an operator closes it.
	if _, err := dbClient.ExecContext(ctx, `
		UPDATE alerts SET status = 'resolved', resolved_at = NOW(), updated_at = NOW() WHERE id = $1
	`, firstAlertID); err != nil {
		t.Fatalf("resolve first alert: %v", err)
	}

	// The monitor re-fires; the new alert must link to the existing incident.
	secondAlertID := insertIncidentTestAlertWithPolicy(ctx, t, dbClient, tenantID, monitorID, policyID)
	secondAlert := &models.AlertWithDetails{
		Alert: models.Alert{
			ID:            secondAlertID,
			TenantID:      tenantID,
			MonitorID:     monitorID,
			AlertPolicyID: policyID,
			Status:        models.AlertStatusActive,
		},
		MonitorName: "API",
		PolicyName:  "auto-policy",
	}
	if err := svc.EnsureIncidentForAlert(ctx, tenantID, secondAlert); err != nil {
		t.Fatalf("EnsureIncidentForAlert(second) error = %v", err)
	}

	reusedIncidentID := loadAutoIncidentID(ctx, t, dbClient, tenantID, monitorID, policyID)
	if reusedIncidentID != incidentID {
		t.Fatalf("reused incident id = %s, want %s", reusedIncidentID, incidentID)
	}
	if count := countIncidentAlertLinks(ctx, t, dbClient, incidentID); count != 2 {
		t.Fatalf("incident alert count after second ensure = %d, want 2", count)
	}

	detail, err = svc.GetIncident(ctx, tenantID, incidentID)
	if err != nil {
		t.Fatalf("GetIncident() second error = %v", err)
	}
	if len(detail.Timeline) != 2 || !hasTimelineMessage(detail.Timeline, autoIncidentLinkedMessage) {
		t.Fatalf("unexpected second timeline = %#v", detail.Timeline)
	}
}

func TestServiceEnsureIncidentForAlertRejectsTenantMismatch(t *testing.T) {
	ctx := context.Background()
	dbClient, cleanup := testutil.SetupPostgresDB(ctx, t)
	defer cleanup()

	tenantID := testutil.InsertTenant(ctx, t, dbClient, "incidents")
	otherTenantID := testutil.InsertTenant(ctx, t, dbClient, "other-incidents")
	monitorID := testutil.InsertHTTPMonitor(ctx, t, dbClient, tenantID, "API")
	policyID := insertIncidentTestPolicy(ctx, t, dbClient, tenantID, "auto-policy", true)
	svc := NewService(dbClient, nil)

	alert := &models.AlertWithDetails{
		Alert: models.Alert{
			ID:            uuid.New(),
			TenantID:      otherTenantID,
			MonitorID:     monitorID,
			AlertPolicyID: policyID,
			Status:        models.AlertStatusActive,
		},
		MonitorName: "API",
		PolicyName:  "auto-policy",
	}
	if err := svc.EnsureIncidentForAlert(ctx, tenantID, alert); err == nil {
		t.Fatalf("expected EnsureIncidentForAlert() tenant mismatch error")
	}
}

func TestServiceRecordAlertRecoveryIfNeededAppendsTimelineAfterFinalResolution(t *testing.T) {
	// Two monitors fire alerts that both link to the same auto-incident. Recovery
	// is recorded only when the last linked alert resolves. Under the
	// one-open-alert-per-monitor invariant, each alert must belong to a distinct
	// monitor, so the second alert is inserted directly on monitor2 and linked to
	// the existing incident (bypassing EnsureIncidentForAlert for the second alert).
	ctx := context.Background()
	dbClient, cleanup := testutil.SetupPostgresDB(ctx, t)
	defer cleanup()

	tenantID := testutil.InsertTenant(ctx, t, dbClient, "incidents")
	monitorID := testutil.InsertHTTPMonitor(ctx, t, dbClient, tenantID, "API")
	monitor2ID := testutil.InsertHTTPMonitor(ctx, t, dbClient, tenantID, "API2")
	policyID := insertIncidentTestPolicy(ctx, t, dbClient, tenantID, "auto-policy", true)
	svc := NewService(dbClient, nil)

	firstAlertID := insertIncidentTestAlertWithPolicy(ctx, t, dbClient, tenantID, monitorID, policyID)
	firstAlert := &models.AlertWithDetails{
		Alert: models.Alert{
			ID:            firstAlertID,
			TenantID:      tenantID,
			MonitorID:     monitorID,
			AlertPolicyID: policyID,
			Status:        models.AlertStatusActive,
		},
		MonitorName: "API",
		PolicyName:  "auto-policy",
	}
	if err := svc.EnsureIncidentForAlert(ctx, tenantID, firstAlert); err != nil {
		t.Fatalf("EnsureIncidentForAlert(first) error = %v", err)
	}

	incidentID := loadAutoIncidentID(ctx, t, dbClient, tenantID, monitorID, policyID)

	// Insert a second alert on a different monitor and link it directly to the
	// same incident, simulating a multi-monitor incident under the new schema.
	secondAlertID := insertIncidentSecondAlertAndLink(ctx, t, dbClient, tenantID, monitor2ID, policyID, incidentID)
	if _, err := dbClient.ExecContext(ctx, `UPDATE alerts SET status = 'resolved', resolved_at = NOW(), updated_at = NOW() WHERE id = $1`, firstAlertID); err != nil {
		t.Fatalf("resolve first alert: %v", err)
	}
	if err := svc.RecordAlertRecoveryIfNeeded(ctx, tenantID, firstAlertID); err != nil {
		t.Fatalf("RecordAlertRecoveryIfNeeded(first) error = %v", err)
	}

	detail, err := svc.GetIncident(ctx, tenantID, incidentID)
	if err != nil {
		t.Fatalf("GetIncident() after first resolve error = %v", err)
	}
	if len(detail.Timeline) != 2 {
		t.Fatalf("timeline length after first resolve = %d, want 2", len(detail.Timeline))
	}

	if _, err := dbClient.ExecContext(ctx, `UPDATE alerts SET status = 'resolved', resolved_at = NOW(), updated_at = NOW() WHERE id = $1`, secondAlertID); err != nil {
		t.Fatalf("resolve second alert: %v", err)
	}
	if err := svc.RecordAlertRecoveryIfNeeded(ctx, tenantID, secondAlertID); err != nil {
		t.Fatalf("RecordAlertRecoveryIfNeeded(second) error = %v", err)
	}

	detail, err = svc.GetIncident(ctx, tenantID, incidentID)
	if err != nil {
		t.Fatalf("GetIncident() after second resolve error = %v", err)
	}
	if len(detail.Timeline) != 3 {
		t.Fatalf("timeline length after second resolve = %d, want 3", len(detail.Timeline))
	}
	if !hasTimelineMessage(detail.Timeline, alertsRecoveredMessage) {
		t.Fatalf("timeline missing recovery message: %#v", detail.Timeline)
	}
}

func TestServiceRecordAlertRecoveryIfNeededSerializesConcurrentFinalResolutions(t *testing.T) {
	// Two alerts are linked to the same auto-incident. The first is created via
	// the normal path; the second is inserted directly (bypassing the unique
	// index on the first monitor) and manually linked to the same incident.
	// This exercises the advisory-lock serialization in
	// RecordAlertRecoveryIfNeededTx when both are resolved concurrently.
	ctx := context.Background()
	dbClient, cleanup := testutil.SetupPostgresDB(ctx, t)
	defer cleanup()

	tenantID := testutil.InsertTenant(ctx, t, dbClient, "incidents")
	monitorID := testutil.InsertHTTPMonitor(ctx, t, dbClient, tenantID, "API")
	monitor2ID := testutil.InsertHTTPMonitor(ctx, t, dbClient, tenantID, "API2")
	policyID := insertIncidentTestPolicy(ctx, t, dbClient, tenantID, "auto-policy", true)
	svc := NewService(dbClient, nil)

	firstAlertID := insertIncidentTestAlertWithPolicy(ctx, t, dbClient, tenantID, monitorID, policyID)
	firstAlert := &models.AlertWithDetails{
		Alert: models.Alert{
			ID:            firstAlertID,
			TenantID:      tenantID,
			MonitorID:     monitorID,
			AlertPolicyID: policyID,
			Status:        models.AlertStatusActive,
		},
		MonitorName: "API",
		PolicyName:  "auto-policy",
	}
	if err := svc.EnsureIncidentForAlert(ctx, tenantID, firstAlert); err != nil {
		t.Fatalf("EnsureIncidentForAlert(first) error = %v", err)
	}

	incidentID := loadAutoIncidentID(ctx, t, dbClient, tenantID, monitorID, policyID)

	// Insert a second alert on a different monitor and link it directly to the
	// same incident, simulating a multi-monitor incident under the new schema.
	secondAlertID := insertIncidentSecondAlertAndLink(ctx, t, dbClient, tenantID, monitor2ID, policyID, incidentID)

	tx1, err := dbClient.BeginTx(ctx, nil)
	if err != nil {
		t.Fatalf("BeginTx(tx1) error = %v", err)
	}
	defer func() {
		_ = tx1.Rollback()
	}()

	tx2, err := dbClient.BeginTx(ctx, nil)
	if err != nil {
		t.Fatalf("BeginTx(tx2) error = %v", err)
	}

	if _, err := tx1.ExecContext(ctx, `
		UPDATE alerts
		SET status = 'resolved', resolved_at = NOW(), updated_at = NOW()
		WHERE id = $1
	`, firstAlertID); err != nil {
		t.Fatalf("resolve first alert in tx1: %v", err)
	}
	if _, err := tx2.ExecContext(ctx, `
		UPDATE alerts
		SET status = 'resolved', resolved_at = NOW(), updated_at = NOW()
		WHERE id = $1
	`, secondAlertID); err != nil {
		t.Fatalf("resolve second alert in tx2: %v", err)
	}

	if err := svc.RecordAlertRecoveryIfNeededTx(ctx, tx1, tenantID, firstAlertID); err != nil {
		t.Fatalf("RecordAlertRecoveryIfNeededTx(tx1) error = %v", err)
	}
	assertIncidentRecoveryLockHeld(ctx, t, dbClient, incidentID)

	errCh := make(chan error, 1)
	go func() {
		if err := svc.RecordAlertRecoveryIfNeededTx(ctx, tx2, tenantID, secondAlertID); err != nil {
			_ = tx2.Rollback()
			errCh <- err
			return
		}
		errCh <- tx2.Commit()
	}()

	if err := tx1.Commit(); err != nil {
		t.Fatalf("Commit(tx1) error = %v", err)
	}
	if err := <-errCh; err != nil {
		t.Fatalf("tx2 recovery/commit error = %v", err)
	}

	detail, err := svc.GetIncident(ctx, tenantID, incidentID)
	if err != nil {
		t.Fatalf("GetIncident() after concurrent resolve error = %v", err)
	}
	if got := countTimelineMessage(detail.Timeline, alertsRecoveredMessage); got != 1 {
		t.Fatalf("recovery message count = %d, want 1", got)
	}
}

func TestServiceRecordAlertRecoveryIfNeededSkipsDuplicateAfterLaterNonRecoveryEntry(t *testing.T) {
	ctx := context.Background()
	dbClient, cleanup := testutil.SetupPostgresDB(ctx, t)
	defer cleanup()

	tenantID := testutil.InsertTenant(ctx, t, dbClient, "incidents")
	monitorID := testutil.InsertHTTPMonitor(ctx, t, dbClient, tenantID, "API")
	policyID := insertIncidentTestPolicy(ctx, t, dbClient, tenantID, "auto-policy", true)
	svc := NewService(dbClient, nil)

	alertID := insertIncidentTestAlertWithPolicy(ctx, t, dbClient, tenantID, monitorID, policyID)
	alert := &models.AlertWithDetails{
		Alert: models.Alert{
			ID:            alertID,
			TenantID:      tenantID,
			MonitorID:     monitorID,
			AlertPolicyID: policyID,
			Status:        models.AlertStatusActive,
		},
		MonitorName: "API",
		PolicyName:  "auto-policy",
	}
	if err := svc.EnsureIncidentForAlert(ctx, tenantID, alert); err != nil {
		t.Fatalf("EnsureIncidentForAlert() error = %v", err)
	}

	incidentID := loadAutoIncidentID(ctx, t, dbClient, tenantID, monitorID, policyID)
	if _, err := dbClient.ExecContext(ctx, `
		UPDATE alerts
		SET status = 'resolved', resolved_at = NOW(), updated_at = NOW()
		WHERE id = $1
	`, alertID); err != nil {
		t.Fatalf("resolve alert: %v", err)
	}
	if err := svc.RecordAlertRecoveryIfNeeded(ctx, tenantID, alertID); err != nil {
		t.Fatalf("RecordAlertRecoveryIfNeeded(first) error = %v", err)
	}
	if _, err := svc.CreateIncidentTimelineEntry(ctx, tenantID, incidentID, &models.CreateIncidentTimelineEntryRequest{
		EntryType: models.IncidentTimelineEntryTypeInternalNote,
		Message:   "Operator note after recovery",
	}); err != nil {
		t.Fatalf("CreateIncidentTimelineEntry() error = %v", err)
	}
	if err := svc.RecordAlertRecoveryIfNeeded(ctx, tenantID, alertID); err != nil {
		t.Fatalf("RecordAlertRecoveryIfNeeded(second) error = %v", err)
	}

	detail, err := svc.GetIncident(ctx, tenantID, incidentID)
	if err != nil {
		t.Fatalf("GetIncident() error = %v", err)
	}
	if got := countTimelineMessage(detail.Timeline, alertsRecoveredMessage); got != 1 {
		t.Fatalf("recovery message count = %d, want 1", got)
	}
}

func TestServicePublishIncidentStatusPageRefreshesIgnoresCanceledRequestContext(t *testing.T) {
	ctx := context.Background()
	dbClient, cleanup := testutil.SetupPostgresDB(ctx, t)
	defer cleanup()

	tenantID := testutil.InsertTenant(ctx, t, dbClient, "incidents")
	statusPageID := testutil.InsertStatusPage(ctx, t, dbClient, tenantID, "status", "Status")
	publisher := &fakeIncidentStatusUpdatePublisher{}
	svc := NewService(dbClient, publisher)

	incident, err := svc.CreateIncident(ctx, tenantID, &models.CreateIncidentRequest{
		Title:   "API outage",
		Summary: "Requests are failing.",
	})
	if err != nil {
		t.Fatalf("CreateIncident() error = %v", err)
	}
	if _, err := svc.PublishIncidentToStatusPage(ctx, tenantID, incident.ID, statusPageID, &models.UpsertIncidentPublicationRequest{}); err != nil {
		t.Fatalf("PublishIncidentToStatusPage() error = %v", err)
	}
	publisher.Reset()

	canceledCtx, cancel := context.WithCancel(ctx)
	cancel()

	svc.publishIncidentStatusPageRefreshes(canceledCtx, tenantID, incident.ID)

	events := publisher.Events()
	if len(events) != 1 {
		t.Fatalf("published events = %d, want %d", len(events), 1)
	}
	if events[0].StatusPageID != statusPageID.String() {
		t.Fatalf("event status_page_id = %q, want %q", events[0].StatusPageID, statusPageID.String())
	}
}

func TestServiceDetachMonitorRemovesPublishedMonitorSelection(t *testing.T) {
	ctx := context.Background()
	dbClient, cleanup := testutil.SetupPostgresDB(ctx, t)
	defer cleanup()

	tenantID := testutil.InsertTenant(ctx, t, dbClient, "incidents")
	monitorID := testutil.InsertHTTPMonitor(ctx, t, dbClient, tenantID, "API")
	statusPageID := testutil.InsertStatusPage(ctx, t, dbClient, tenantID, "status", "Status")
	testutil.AddMonitorToStatusPage(ctx, t, dbClient, statusPageID, monitorID, 0)
	svc := NewService(dbClient, nil)

	incident, err := svc.CreateIncident(ctx, tenantID, &models.CreateIncidentRequest{
		Title:   "API outage",
		Summary: "Requests are failing.",
	})
	if err != nil {
		t.Fatalf("CreateIncident() error = %v", err)
	}
	if _, err := svc.AttachMonitor(ctx, tenantID, incident.ID, monitorID); err != nil {
		t.Fatalf("AttachMonitor() error = %v", err)
	}
	if _, err := svc.PublishIncidentToStatusPage(ctx, tenantID, incident.ID, statusPageID, &models.UpsertIncidentPublicationRequest{
		MonitorIDs: []string{monitorID.String()},
	}); err != nil {
		t.Fatalf("PublishIncidentToStatusPage() error = %v", err)
	}

	detail, err := svc.DetachMonitor(ctx, tenantID, incident.ID, monitorID)
	if err != nil {
		t.Fatalf("DetachMonitor() error = %v", err)
	}
	if len(detail.Timeline) != 4 {
		t.Fatalf("timeline length after detach = %d, want %d", len(detail.Timeline), 4)
	}
	if count := countIncidentMonitorLinks(ctx, t, dbClient, incident.ID); count != 0 {
		t.Fatalf("incident monitor count after detach = %d, want %d", count, 0)
	}
	if count := countIncidentPublicationMonitors(ctx, t, dbClient, incident.ID, statusPageID); count != 0 {
		t.Fatalf("publication monitor count after detach = %d, want %d", count, 0)
	}

	publication, err := loadIncidentPublication(ctx, dbClient, incident.ID, statusPageID)
	if err != nil {
		t.Fatalf("loadIncidentPublication() after detach error = %v", err)
	}
	if publication.unpublishedAt.Valid {
		t.Fatalf("publication should remain active, got unpublished_at = %v", publication.unpublishedAt.Time)
	}
}

type incidentPublicationRecord struct {
	publishedAt   time.Time
	unpublishedAt sql.NullTime
	updatedAt     time.Time
}

func insertIncidentTestAlert(ctx context.Context, t *testing.T, dbClient testutilDBClient, tenantID, monitorID uuid.UUID) uuid.UUID {
	t.Helper()

	policyID := insertIncidentTestPolicy(ctx, t, dbClient, tenantID, "incident-policy", false)

	return insertIncidentTestAlertWithPolicy(ctx, t, dbClient, tenantID, monitorID, policyID)
}

func insertIncidentTestPolicy(ctx context.Context, t *testing.T, dbClient testutilDBClient, tenantID uuid.UUID, name string, createIncidentOnFire bool) uuid.UUID {
	t.Helper()

	policyID := uuid.New()
	if _, err := dbClient.ExecContext(ctx, `
		INSERT INTO alert_policies (
			id, tenant_id, name, failure_threshold, failure_window_seconds, create_incident_on_fire, created_at, updated_at
		) VALUES ($1, $2, $3, 1, 60, $4, NOW(), NOW())
	`, policyID, tenantID, name, createIncidentOnFire); err != nil {
		t.Fatalf("insert alert policy: %v", err)
	}

	return policyID
}

func insertIncidentTestAlertWithPolicy(ctx context.Context, t *testing.T, dbClient testutilDBClient, tenantID, monitorID, policyID uuid.UUID) uuid.UUID {
	t.Helper()

	alertID := uuid.New()
	if _, err := dbClient.ExecContext(ctx, `
		INSERT INTO alerts (id, tenant_id, monitor_id, alert_policy_id, status, triggered_at, failure_count, created_at, updated_at)
		VALUES ($1, $2, $3, $4, 'active', NOW(), 1, NOW(), NOW())
	`, alertID, tenantID, monitorID, policyID); err != nil {
		t.Fatalf("insert alert: %v", err)
	}

	return alertID
}

// insertIncidentSecondAlertAndLink inserts an active alert for monitorID and
// links it to an existing incidentID, also appending the "alert auto-linked"
// timeline entry that EnsureIncidentForAlert would normally produce. This
// bypasses EnsureIncidentForAlert (and therefore the auto-incident lookup keyed
// on auto_monitor_id) so that two concurrent active alerts can exist for
// different monitors but share the same incident — needed for tests that verify
// multi-alert recovery logic under the one-open-alert-per-monitor invariant.
func insertIncidentSecondAlertAndLink(ctx context.Context, t *testing.T, dbClient testutilDBClient, tenantID, monitorID, policyID, incidentID uuid.UUID) uuid.UUID {
	t.Helper()

	alertID := uuid.New()
	if _, err := dbClient.ExecContext(ctx, `
		INSERT INTO alerts (id, tenant_id, monitor_id, alert_policy_id, status, triggered_at, failure_count, created_at, updated_at)
		VALUES ($1, $2, $3, $4, 'active', NOW(), 1, NOW(), NOW())
	`, alertID, tenantID, monitorID, policyID); err != nil {
		t.Fatalf("insert second alert: %v", err)
	}
	if _, err := dbClient.ExecContext(ctx, `
		INSERT INTO incident_alerts (incident_id, alert_id, created_at)
		VALUES ($1, $2, NOW())
		ON CONFLICT DO NOTHING
	`, incidentID, alertID); err != nil {
		t.Fatalf("link second alert to incident: %v", err)
	}
	if _, err := dbClient.ExecContext(ctx, `
		INSERT INTO incident_timeline_entries (id, tenant_id, incident_id, entry_type, message, metadata, created_at)
		VALUES ($1, $2, $3, 'system', $4, '{}'::jsonb, clock_timestamp())
	`, uuid.New(), tenantID, incidentID, autoIncidentLinkedMessage); err != nil {
		t.Fatalf("insert linked timeline entry: %v", err)
	}
	return alertID
}

func loadAutoIncidentID(ctx context.Context, t *testing.T, dbClient testutilDBClient, tenantID, monitorID, policyID uuid.UUID) uuid.UUID {
	t.Helper()

	var incidentID uuid.UUID
	if err := dbClient.QueryRowContext(ctx, `
		SELECT id
		FROM incidents
		WHERE tenant_id = $1 AND is_auto_created = TRUE AND auto_monitor_id = $2 AND auto_alert_policy_id = $3
	`, tenantID, monitorID, policyID).Scan(&incidentID); err != nil {
		t.Fatalf("load auto incident: %v", err)
	}

	return incidentID
}

func hasTimelineMessage(timeline []models.IncidentTimelineEntry, message string) bool {
	return countTimelineMessage(timeline, message) > 0
}

func countTimelineMessage(timeline []models.IncidentTimelineEntry, message string) int {
	count := 0
	for _, entry := range timeline {
		if entry.Message == message {
			count++
		}
	}
	return count
}

func assertIncidentRecoveryLockHeld(ctx context.Context, t *testing.T, dbClient testutilDBClient, incidentID uuid.UUID) {
	t.Helper()

	lockKey := "incident-recovery:" + incidentID.String()
	var acquired bool
	if err := dbClient.QueryRowContext(ctx, `
		SELECT pg_try_advisory_xact_lock(hashtextextended($1, 0))
	`, lockKey).Scan(&acquired); err != nil {
		t.Fatalf("probe incident recovery lock: %v", err)
	}
	if acquired {
		t.Fatalf("expected incident recovery lock to be held for %s", incidentID)
	}
}

type testutilDBClient interface {
	ExecContext(ctx context.Context, query string, args ...interface{}) (sql.Result, error)
	QueryRowContext(ctx context.Context, query string, args ...interface{}) *sql.Row
}

func countIncidentAlertLinks(ctx context.Context, t *testing.T, dbClient testutilDBClient, incidentID uuid.UUID) int {
	t.Helper()

	var count int
	if err := dbClient.QueryRowContext(ctx, `
		SELECT COUNT(*)
		FROM incident_alerts
		WHERE incident_id = $1
	`, incidentID).Scan(&count); err != nil {
		t.Fatalf("count incident_alerts: %v", err)
	}

	return count
}

func countIncidentMonitorLinks(ctx context.Context, t *testing.T, dbClient testutilDBClient, incidentID uuid.UUID) int {
	t.Helper()

	var count int
	if err := dbClient.QueryRowContext(ctx, `
		SELECT COUNT(*)
		FROM incident_monitors
		WHERE incident_id = $1
	`, incidentID).Scan(&count); err != nil {
		t.Fatalf("count incident_monitors: %v", err)
	}

	return count
}

func countIncidentPublicationMonitors(ctx context.Context, t *testing.T, dbClient testutilDBClient, incidentID, statusPageID uuid.UUID) int {
	t.Helper()

	var count int
	if err := dbClient.QueryRowContext(ctx, `
		SELECT COUNT(*)
		FROM incident_status_page_monitors
		WHERE incident_id = $1 AND status_page_id = $2
	`, incidentID, statusPageID).Scan(&count); err != nil {
		t.Fatalf("count incident_status_page_monitors: %v", err)
	}

	return count
}

func loadIncidentPublication(ctx context.Context, dbClient testutilDBClient, incidentID, statusPageID uuid.UUID) (incidentPublicationRecord, error) {
	var publication incidentPublicationRecord
	err := dbClient.QueryRowContext(ctx, `
		SELECT published_at, unpublished_at, updated_at
		FROM incident_status_page_publications
		WHERE incident_id = $1 AND status_page_id = $2
	`, incidentID, statusPageID).Scan(&publication.publishedAt, &publication.unpublishedAt, &publication.updatedAt)
	if err != nil {
		return incidentPublicationRecord{}, err
	}

	return publication, nil
}

type fakeIncidentStatusUpdatePublisher struct {
	mu     sync.Mutex
	events []statusupdates.Event
}

func (p *fakeIncidentStatusUpdatePublisher) Publish(event statusupdates.Event) error {
	p.mu.Lock()
	defer p.mu.Unlock()

	p.events = append(p.events, event)
	return nil
}

func (p *fakeIncidentStatusUpdatePublisher) Events() []statusupdates.Event {
	p.mu.Lock()
	defer p.mu.Unlock()

	out := make([]statusupdates.Event, len(p.events))
	copy(out, p.events)
	return out
}

func (p *fakeIncidentStatusUpdatePublisher) Reset() {
	p.mu.Lock()
	defer p.mu.Unlock()

	p.events = nil
}
