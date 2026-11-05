//go:build !unit

package messagestore_test

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/google/uuid"

	shareddb "github.com/yassinebenameur/probara/shared/db"
	"github.com/yassinebenameur/probara/shared/notifications"
	"github.com/yassinebenameur/probara/shared/notifications/messagestore"
	"github.com/yassinebenameur/probara/shared/testutil"
)

func insertAlertAndChannel(ctx context.Context, t *testing.T, db *shareddb.Client, tenantID uuid.UUID) (alertID, channelID uuid.UUID) {
	t.Helper()
	monitorID := testutil.InsertHTTPMonitor(ctx, t, db, tenantID, "API")
	alertID, channelID = uuid.New(), uuid.New()
	if _, err := db.ExecContext(ctx, `
		INSERT INTO alert_channels (id, tenant_id, name, type, config, is_active, created_at, updated_at)
		VALUES ($1, $2, 'slack', 'slack_app', '{}'::jsonb, TRUE, NOW(), NOW())`, channelID, tenantID); err != nil {
		t.Fatalf("insert channel: %v", err)
	}
	if _, err := db.ExecContext(ctx, `
		INSERT INTO alerts (id, tenant_id, monitor_id, status, triggered_at, failure_count, created_at, updated_at)
		VALUES ($1, $2, $3, 'active', NOW(), 1, NOW(), NOW())`, alertID, tenantID, monitorID); err != nil {
		t.Fatalf("insert alert: %v", err)
	}
	return alertID, channelID
}

func event(alertID uuid.UUID, eventType string, relapse int) notifications.AlertEvent {
	return notifications.AlertEvent{
		Type:  eventType,
		Alert: notifications.AlertDetails{ID: alertID.String(), Status: "active", MonitorName: "API", RelapseCount: relapse},
	}
}

func cardRow(ctx context.Context, t *testing.T, db *shareddb.Client, alertID uuid.UUID) (ref string, episode int, state string, ok bool) {
	t.Helper()
	err := db.QueryRowContext(ctx, `SELECT provider_ref::text, episode, rendered_state FROM alert_notification_messages WHERE alert_id = $1`,
		alertID).Scan(&ref, &episode, &state)
	if err != nil {
		return "", 0, "", false
	}
	return ref, episode, state, true
}

// A card saved for an episode is loaded for that episode only; a reopen's
// card replaces it, and an earlier episode's late save never wins back.
// The "updated" event never saves.
func TestStoreEpisodes(t *testing.T) {
	ctx := context.Background()
	db, cleanup := testutil.SetupPostgresDB(ctx, t)
	defer cleanup()
	tenantID := testutil.InsertTenant(ctx, t, db, "cards")
	alertID, channelID := insertAlertAndChannel(ctx, t, db, tenantID)

	first := messagestore.For(db, alertID, channelID, "created", event(alertID, "created", 0))
	if _, ok, err := first.Load(ctx); ok || err != nil {
		t.Fatalf("empty Load: ok=%v err=%v", ok, err)
	}
	if err := first.Save(ctx, json.RawMessage(`{"ts":"1"}`)); err != nil {
		t.Fatalf("Save: %v", err)
	}
	if ref, ok, err := first.Load(ctx); !ok || err != nil || string(ref) != `{"ts": "1"}` {
		t.Fatalf("Load = %s %v %v", ref, ok, err)
	}
	if _, ep, state, _ := cardRow(ctx, t, db, alertID); ep != 0 || state != "active" {
		t.Fatalf("row episode=%d state=%q", ep, state)
	}

	// The update does not touch the row.
	upd := messagestore.For(db, alertID, channelID, "updated", event(alertID, "updated", 0))
	if err := upd.Save(ctx, json.RawMessage(`{"ts":"evil"}`)); err != nil {
		t.Fatal(err)
	}

	relapse := messagestore.For(db, alertID, channelID, "created", event(alertID, "created", 1))
	if _, ok, _ := relapse.Load(ctx); ok {
		t.Fatal("a relapse loaded the previous episode's card")
	}
	if err := relapse.Save(ctx, json.RawMessage(`{"ts":"2"}`)); err != nil {
		t.Fatal(err)
	}
	if err := first.Save(ctx, json.RawMessage(`{"ts":"late"}`)); err != nil {
		t.Fatal(err)
	}
	if ref, ep, _, _ := cardRow(ctx, t, db, alertID); ep != 1 || ref != `{"ts": "2"}` {
		t.Fatalf("after relapse: ref=%s episode=%d", ref, ep)
	}
}

// A channel of another tenant never pairs with the alert.
func TestStoreRefusesCrossTenantChannel(t *testing.T) {
	ctx := context.Background()
	db, cleanup := testutil.SetupPostgresDB(ctx, t)
	defer cleanup()
	alertID, _ := insertAlertAndChannel(ctx, t, db, testutil.InsertTenant(ctx, t, db, "a"))
	_, foreign := insertAlertAndChannel(ctx, t, db, testutil.InsertTenant(ctx, t, db, "b"))
	if err := messagestore.For(db, alertID, foreign, "created", event(alertID, "created", 0)).Save(ctx, json.RawMessage(`{"ts":"1"}`)); err != nil {
		t.Fatal(err)
	}
	if _, _, _, ok := cardRow(ctx, t, db, alertID); ok {
		t.Fatal("card saved against another tenant's channel")
	}
}

// CardStateSQL and the Go CardState / RenderedState format the same state:
// the alerter's claim compares the stored rendered_state (Go) with the
// alert's state (SQL), so any drift rewrites cards forever or never.
func TestCardStateSQLMatchesGo(t *testing.T) {
	ctx := context.Background()
	db, cleanup := testutil.SetupPostgresDB(ctx, t)
	defer cleanup()
	tenantID := testutil.InsertTenant(ctx, t, db, "card-state")
	alertID, _ := insertAlertAndChannel(ctx, t, db, tenantID)

	future := time.Now().Add(90 * time.Minute).Truncate(time.Microsecond)
	past := time.Now().Add(-time.Minute)
	for _, status := range []string{"active", "acknowledged", "resolved"} {
		for _, snooze := range []*time.Time{nil, &past, &future} {
			if _, err := db.ExecContext(ctx, `UPDATE alerts SET status = $2, snoozed_until = $3 WHERE id = $1`,
				alertID, status, snooze); err != nil {
				t.Fatal(err)
			}
			var got string
			if err := db.QueryRowContext(ctx, `SELECT `+messagestore.CardStateSQL("al")+` FROM alerts al WHERE al.id = $1`,
				alertID).Scan(&got); err != nil {
				t.Fatal(err)
			}
			var snoozedUntil *time.Time
			if snooze == &future {
				snoozedUntil = snooze
			}
			want := messagestore.CardState(status, snoozedUntil)
			ev := notifications.AlertEvent{Type: "updated", Alert: notifications.AlertDetails{Status: status, SnoozedUntil: snoozedUntil}}
			if got != want || messagestore.RenderedState(ev) != want {
				t.Errorf("status=%s snooze=%v: SQL %q, Go %q, rendered %q", status, snooze, got, want, messagestore.RenderedState(ev))
			}
		}
	}
}
