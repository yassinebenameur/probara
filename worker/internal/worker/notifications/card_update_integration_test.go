//go:build !unit

package notifications

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/google/uuid"

	"github.com/yassinebenameur/probara/shared/logger"
	"github.com/yassinebenameur/probara/shared/notifications"
	"github.com/yassinebenameur/probara/shared/queue"
	"github.com/yassinebenameur/probara/shared/secrets"
	"github.com/yassinebenameur/probara/shared/testutil"
)

// A queued chat-card rewrite ("updated") is delivered while it still draws
// the alert's state, a resolved alert included (a card follows its alert
// into every state), and dropped once the alert moved on: the alerter then
// claims a fresh rewrite, and a late delivery must not leave the card
// showing a state the alert has left.
func TestHandle_CardUpdateFollowsCurrentState(t *testing.T) {
	ctx := context.Background()
	dbClient, cleanup := testutil.SetupPostgresDB(ctx, t)
	defer cleanup()

	tenantID := testutil.InsertTenant(ctx, t, dbClient, "card-update")
	monitorID := testutil.InsertHTTPMonitor(ctx, t, dbClient, tenantID, "API")
	channelID, alertID := uuid.New(), uuid.New()
	if _, err := dbClient.ExecContext(ctx, `
		INSERT INTO alert_channels (id, tenant_id, name, type, config, is_active, created_at, updated_at)
		VALUES ($1, $2, 'chat', $3, '{}'::jsonb, TRUE, NOW(), NOW())
	`, channelID, tenantID, raceStubType); err != nil {
		t.Fatalf("insert channel: %v", err)
	}
	if _, err := dbClient.ExecContext(ctx, `
		INSERT INTO alerts (id, tenant_id, monitor_id, alert_policy_id, kind, status,
			triggered_at, acknowledged_at, failure_count, created_at, updated_at)
		VALUES ($1, $2, $3, NULL, 'availability', 'acknowledged', NOW(), NOW(), 3, NOW(), NOW())
	`, alertID, tenantID, monitorID); err != nil {
		t.Fatalf("insert alert: %v", err)
	}

	raceStub.mu.Lock()
	raceStub.order = nil
	raceStub.mu.Unlock()
	worker := &Consumer{logger: logger.New("test", "error"), db: dbClient, encryptor: secrets.NoOpEncryptor{}}
	deliver := func(status string) int {
		t.Helper()
		raw, _ := json.Marshal(notifications.DispatchEnvelope{
			V: 1, ChannelID: channelID.String(), ChannelType: raceStubType,
			AlertID: alertID.String(), EventType: "updated",
			Event: notifications.AlertEvent{Type: "updated", Alert: notifications.AlertDetails{ID: alertID.String(), Status: status}},
		})
		if err := worker.handle(ctx, &queue.Message{Data: raw}); err != nil {
			t.Fatalf("handle updated(%s): %v", status, err)
		}
		raceStub.mu.Lock()
		defer raceStub.mu.Unlock()
		return len(raceStub.order)
	}

	if n := deliver("acknowledged"); n != 1 {
		t.Fatalf("current-state rewrite: %d sends, want 1", n)
	}
	if n := deliver("active"); n != 1 {
		t.Fatalf("a rewrite drawing a state the alert left was delivered (%d sends)", n)
	}
	if _, err := dbClient.ExecContext(ctx,
		`UPDATE alerts SET status = 'resolved', resolved_at = NOW() WHERE id = $1`, alertID); err != nil {
		t.Fatal(err)
	}
	if n := deliver("resolved"); n != 2 {
		t.Fatalf("a resolved card's rewrite was dropped (%d sends)", n)
	}
}
