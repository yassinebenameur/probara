package incidents

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/yassinebenameur/probara/api/internal/models"
	"github.com/yassinebenameur/probara/shared/testutil"
)

func TestServiceCreateManualIncident(t *testing.T) {
	ctx := context.Background()
	dbClient, cleanup := testutil.SetupPostgresDB(ctx, t)
	defer cleanup()

	tenantID := testutil.InsertTenant(ctx, t, dbClient, "incidents")
	svc := NewService(dbClient)

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
}

func TestServiceTransitionIncidentStateRejectsResolvedReopen(t *testing.T) {
	ctx := context.Background()
	dbClient, cleanup := testutil.SetupPostgresDB(ctx, t)
	defer cleanup()

	tenantID := testutil.InsertTenant(ctx, t, dbClient, "incidents")
	svc := NewService(dbClient)

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
	svc := NewService(dbClient)

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
	svc := NewService(dbClient)

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
	svc := NewService(dbClient)

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
	svc := NewService(dbClient)

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
	svc := NewService(dbClient)

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

	svc := NewService(dbClient)
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
