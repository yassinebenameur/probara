//go:build !unit

package locations

import (
	"context"
	"testing"

	"github.com/google/uuid"
	testcontainers "github.com/testcontainers/testcontainers-go"

	"github.com/yassinebenameur/probara/api/internal/models"
	"github.com/yassinebenameur/probara/shared/testutil"
)

// The API's `offline` flag is rule S-F6 (the alerter's predicate): a
// location seen before and silent for 180 seconds. Never-seen, recently seen
// and disabled locations are not offline. `open_alert_id` names the
// location's open location_offline alert.
func TestLocationOfflineFlag_Integration(t *testing.T) {
	testcontainers.SkipIfProviderIsNotHealthy(t)

	ctx := context.Background()
	dbClient, cleanup := testutil.SetupPostgresDB(ctx, t)
	defer cleanup()

	tenantID := testutil.InsertTenant(ctx, t, dbClient, "tenant-loc-offline")
	insert := func(name, lastSeen string, enabled bool) uuid.UUID {
		t.Helper()
		id := uuid.New()
		if _, err := dbClient.ExecContext(ctx, `
			INSERT INTO locations (id, tenant_id, name, slug, enabled, last_seen_at, enabled_at)
			VALUES ($1, $2, $3, $3, $4, NOW() - $5::interval, NOW() - INTERVAL '30 days')`, id, tenantID, name, enabled, lastSeen); err != nil {
			t.Fatalf("insert location: %v", err)
		}
		return id
	}
	dead := insert("dead", "10 minutes", true)
	insert("quiet", "2 minutes", true)
	insert("fresh", "5 seconds", true)
	insert("disabled", "1 hour", false)
	if _, err := dbClient.ExecContext(ctx, `
		INSERT INTO locations (id, tenant_id, name, slug) VALUES ($1, $2, 'never', 'never')`, uuid.New(), tenantID); err != nil {
		t.Fatalf("insert never-seen location: %v", err)
	}
	alertID := uuid.New()
	if _, err := dbClient.ExecContext(ctx, `
		INSERT INTO alerts (id, tenant_id, monitor_id, kind, source_location_id, status, triggered_at, failure_count, created_at, updated_at)
		VALUES ($1, $2, NULL, 'location_offline', $3, 'active', NOW(), 0, NOW(), NOW())`, alertID, tenantID, dead); err != nil {
		t.Fatalf("insert alert: %v", err)
	}

	svc := NewService(dbClient)
	list, err := svc.List(ctx, tenantID, 1, 50)
	if err != nil {
		t.Fatal(err)
	}
	for _, l := range list.Items {
		wantOffline := l.Name == "dead"
		if l.Offline != wantOffline {
			t.Errorf("%s: offline = %v, want %v", l.Name, l.Offline, wantOffline)
		}
		if l.Name == "dead" {
			if l.OpenAlertID == nil || *l.OpenAlertID != alertID {
				t.Errorf("dead: open_alert_id = %v, want %v", l.OpenAlertID, alertID)
			}
			if l.Connected {
				t.Error("dead: connected")
			}
		} else if l.OpenAlertID != nil {
			t.Errorf("%s: open_alert_id = %v, want none", l.Name, *l.OpenAlertID)
		}
	}
	if len(list.Items) != 5 {
		t.Fatalf("listed %d locations, want 5", len(list.Items))
	}

	// Disabling and re-enabling restarts enabled_at: the location is not
	// offline again until one threshold after the re-enable, whatever its
	// last heartbeat. A rename of an enabled location does not restart it.
	off, on := false, true
	if _, err := svc.Update(ctx, tenantID, dead, &models.UpdateLocationRequest{Enabled: &off}); err != nil {
		t.Fatal(err)
	}
	renamed := "dead-renamed"
	if _, err := svc.Update(ctx, tenantID, dead, &models.UpdateLocationRequest{Name: &renamed}); err != nil {
		t.Fatal(err)
	}
	l, err := svc.Update(ctx, tenantID, dead, &models.UpdateLocationRequest{Enabled: &on})
	if err != nil {
		t.Fatal(err)
	}
	if l.Offline {
		t.Fatal("a just re-enabled location reads offline")
	}
	var recent bool
	if err := dbClient.QueryRowContext(ctx, `SELECT enabled_at > NOW() - INTERVAL '1 minute' FROM locations WHERE id = $1`, dead).Scan(&recent); err != nil || !recent {
		t.Fatalf("enabled_at not restarted on re-enable (%v)", err)
	}
	fresh, err := svc.Update(ctx, tenantID, dead, &models.UpdateLocationRequest{Name: &renamed})
	if err != nil || fresh.Offline {
		t.Fatalf("rename: offline=%v err=%v", fresh != nil && fresh.Offline, err)
	}
	if _, err := dbClient.ExecContext(ctx, `UPDATE locations SET enabled_at = NOW() - INTERVAL '1 hour' WHERE id = $1`, dead); err != nil {
		t.Fatal(err)
	}
	again, err := svc.Update(ctx, tenantID, dead, &models.UpdateLocationRequest{Name: &renamed})
	if err != nil || !again.Offline {
		t.Fatalf("an update that does not enable must keep enabled_at (offline=%v, err=%v)", again != nil && again.Offline, err)
	}
}
