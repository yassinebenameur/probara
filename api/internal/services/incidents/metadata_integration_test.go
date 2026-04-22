package incidents

import (
	"context"
	"testing"

	"github.com/google/uuid"

	"github.com/yassinebenameur/probara/api/internal/models"
	"github.com/yassinebenameur/probara/shared/db"
	"github.com/yassinebenameur/probara/shared/testutil"
)

func TestServiceCreateIncidentStoresMetadataAndInitialLinks(t *testing.T) {
	ctx := context.Background()
	dbClient, cleanup := testutil.SetupPostgresDB(ctx, t)
	defer cleanup()

	tenantID := testutil.InsertTenant(ctx, t, dbClient, "incidents-metadata")
	ownerID := insertIncidentTestAdminUser(ctx, t, dbClient, "incident-owner")
	monitorID := testutil.InsertHTTPMonitor(ctx, t, dbClient, tenantID, "incident-monitor")
	alertID := insertIncidentTestAlert(ctx, t, dbClient, tenantID, monitorID)

	svc := NewService(dbClient, nil)
	incident, err := svc.CreateIncident(ctx, tenantID, &models.CreateIncidentRequest{
		Title:       "API outage",
		Summary:     "Requests are failing for all regions.",
		Severity:    models.IncidentSeverityCritical,
		OwnerUserID: ownerID.String(),
		AlertID:     alertID.String(),
		MonitorID:   monitorID.String(),
	})
	if err != nil {
		t.Fatalf("CreateIncident() error = %v", err)
	}

	if incident.Severity != models.IncidentSeverityCritical {
		t.Fatalf("severity = %q, want %q", incident.Severity, models.IncidentSeverityCritical)
	}
	if incident.OwnerUserID == nil || *incident.OwnerUserID != ownerID {
		t.Fatalf("owner_user_id = %v, want %s", incident.OwnerUserID, ownerID)
	}
	if incident.OwnerUsername != "incident-owner" {
		t.Fatalf("owner_username = %q, want %q", incident.OwnerUsername, "incident-owner")
	}
	if len(incident.Alerts) != 1 {
		t.Fatalf("alerts len = %d, want 1", len(incident.Alerts))
	}
	if incident.Alerts[0].ID != alertID {
		t.Fatalf("alert id = %s, want %s", incident.Alerts[0].ID, alertID)
	}
	if len(incident.Monitors) != 1 {
		t.Fatalf("monitors len = %d, want 1", len(incident.Monitors))
	}
	if incident.Monitors[0].ID != monitorID {
		t.Fatalf("monitor id = %s, want %s", incident.Monitors[0].ID, monitorID)
	}

	var severity string
	var ownerUserID uuid.NullUUID
	if err := dbClient.QueryRowContext(ctx, `
		SELECT severity, owner_user_id
		FROM incidents
		WHERE id = $1 AND tenant_id = $2
	`, incident.ID, tenantID).Scan(&severity, &ownerUserID); err != nil {
		t.Fatalf("load incident metadata: %v", err)
	}
	if severity != string(models.IncidentSeverityCritical) {
		t.Fatalf("database severity = %q, want %q", severity, models.IncidentSeverityCritical)
	}
	if !ownerUserID.Valid || ownerUserID.UUID != ownerID {
		t.Fatalf("database owner_user_id = %v, want %s", ownerUserID, ownerID)
	}

	var alertCount int
	if err := dbClient.QueryRowContext(ctx, `
		SELECT COUNT(*)
		FROM incident_alerts
		WHERE incident_id = $1
	`, incident.ID).Scan(&alertCount); err != nil {
		t.Fatalf("count incident alerts: %v", err)
	}
	if alertCount != 1 {
		t.Fatalf("incident_alerts count = %d, want 1", alertCount)
	}

	var monitorCount int
	if err := dbClient.QueryRowContext(ctx, `
		SELECT COUNT(*)
		FROM incident_monitors
		WHERE incident_id = $1
	`, incident.ID).Scan(&monitorCount); err != nil {
		t.Fatalf("count incident monitors: %v", err)
	}
	if monitorCount != 1 {
		t.Fatalf("incident_monitors count = %d, want 1", monitorCount)
	}
}

func TestServiceUpdateIncidentCanClearOwnerAndChangeSeverity(t *testing.T) {
	ctx := context.Background()
	dbClient, cleanup := testutil.SetupPostgresDB(ctx, t)
	defer cleanup()

	tenantID := testutil.InsertTenant(ctx, t, dbClient, "incidents-metadata")
	ownerAID := insertIncidentTestAdminUser(ctx, t, dbClient, "owner-a")
	ownerBID := insertIncidentTestAdminUser(ctx, t, dbClient, "owner-b")
	svc := NewService(dbClient, nil)

	incident, err := svc.CreateIncident(ctx, tenantID, &models.CreateIncidentRequest{
		Title:       "Storage degradation",
		Summary:     "Latency is increasing.",
		OwnerUserID: ownerAID.String(),
	})
	if err != nil {
		t.Fatalf("CreateIncident() error = %v", err)
	}

	updatedSeverity := models.IncidentSeverityMedium
	ownerB := ownerBID.String()
	updated, err := svc.UpdateIncident(ctx, tenantID, incident.ID, &models.UpdateIncidentRequest{
		Severity:    &updatedSeverity,
		OwnerUserID: &ownerB,
	})
	if err != nil {
		t.Fatalf("UpdateIncident(change owner) error = %v", err)
	}
	if updated.Severity != models.IncidentSeverityMedium {
		t.Fatalf("severity = %q, want %q", updated.Severity, models.IncidentSeverityMedium)
	}
	if updated.OwnerUserID == nil || *updated.OwnerUserID != ownerBID {
		t.Fatalf("owner_user_id = %v, want %s", updated.OwnerUserID, ownerBID)
	}
	if updated.OwnerUsername != "owner-b" {
		t.Fatalf("owner_username = %q, want %q", updated.OwnerUsername, "owner-b")
	}

	clearedSeverity := models.IncidentSeverityLow
	emptyOwner := ""
	updated, err = svc.UpdateIncident(ctx, tenantID, incident.ID, &models.UpdateIncidentRequest{
		Severity:    &clearedSeverity,
		OwnerUserID: &emptyOwner,
	})
	if err != nil {
		t.Fatalf("UpdateIncident(clear owner) error = %v", err)
	}
	if updated.Severity != models.IncidentSeverityLow {
		t.Fatalf("severity = %q, want %q", updated.Severity, models.IncidentSeverityLow)
	}
	if updated.OwnerUserID != nil {
		t.Fatalf("owner_user_id = %v, want nil", updated.OwnerUserID)
	}
	if updated.OwnerUsername != "" {
		t.Fatalf("owner_username = %q, want empty string", updated.OwnerUsername)
	}

	var ownerUserID uuid.NullUUID
	if err := dbClient.QueryRowContext(ctx, `
		SELECT owner_user_id
		FROM incidents
		WHERE id = $1 AND tenant_id = $2
	`, incident.ID, tenantID).Scan(&ownerUserID); err != nil {
		t.Fatalf("load cleared owner_user_id: %v", err)
	}
	if ownerUserID.Valid {
		t.Fatalf("database owner_user_id = %v, want null", ownerUserID.UUID)
	}
}

func insertIncidentTestAdminUser(ctx context.Context, t *testing.T, dbClient *db.Client, username string) uuid.UUID {
	t.Helper()

	id := uuid.New()
	if username == "" {
		username = "incident-user"
	}
	if _, err := dbClient.ExecContext(ctx, `
		INSERT INTO admin_users (id, username, password_hash, created_at, updated_at)
		VALUES ($1, $2, 'hash', NOW(), NOW())
	`, id, username); err != nil {
		t.Fatalf("insert admin user: %v", err)
	}
	return id
}
