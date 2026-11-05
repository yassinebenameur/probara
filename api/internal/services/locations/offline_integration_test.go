//go:build !unit

package locations

import (
	"context"
	"testing"

	"github.com/google/uuid"

	"github.com/yassinebenameur/probara/shared/testutil"
)

// Get and List report S-F6 offline and the open location_offline alert.
func TestLocationReportsOfflineAndOpenAlert(t *testing.T) {
	ctx := context.Background()
	dbClient, cleanup := testutil.SetupPostgresDB(ctx, t)
	defer cleanup()

	tenantID := testutil.InsertTenant(ctx, t, dbClient, "loc-offline-api")
	insert := func(name, lastSeen string) uuid.UUID {
		t.Helper()
		id := uuid.New()
		if _, err := dbClient.ExecContext(ctx, `
			INSERT INTO locations (id, tenant_id, name, slug, enabled, last_seen_at, created_at, updated_at)
			VALUES ($1, $2, $3, $3, TRUE, NOW() - $4::interval, NOW(), NOW())`, id, tenantID, name, lastSeen); err != nil {
			t.Fatal(err)
		}
		return id
	}
	dead := insert("dead", "1 hour")
	fresh := insert("fresh", "10 seconds")
	alertID := uuid.New()
	if _, err := dbClient.ExecContext(ctx, `
		INSERT INTO alerts (id, tenant_id, kind, status, triggered_at, failure_count, source_location_id, created_at, updated_at)
		VALUES ($1, $2, 'location_offline', 'active', NOW(), 0, $3, NOW(), NOW())`, alertID, tenantID, dead); err != nil {
		t.Fatal(err)
	}

	svc := NewService(dbClient)
	got, err := svc.Get(ctx, tenantID, dead)
	if err != nil {
		t.Fatal(err)
	}
	if !got.Offline || got.Connected || got.OpenAlertID == nil || *got.OpenAlertID != alertID {
		t.Fatalf("dead location: offline=%v connected=%v open_alert_id=%v", got.Offline, got.Connected, got.OpenAlertID)
	}
	got, err = svc.Get(ctx, tenantID, fresh)
	if err != nil {
		t.Fatal(err)
	}
	if got.Offline || !got.Connected || got.OpenAlertID != nil {
		t.Fatalf("fresh location: offline=%v connected=%v open_alert_id=%v", got.Offline, got.Connected, got.OpenAlertID)
	}
	list, err := svc.List(ctx, tenantID, 1, 50)
	if err != nil || len(list.Items) != 2 {
		t.Fatalf("List: %v items=%d", err, len(list.Items))
	}
}
