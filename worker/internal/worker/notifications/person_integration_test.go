//go:build !unit

package notifications

import (
	"context"
	"encoding/json"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/yassinebenameur/probara/shared/logger"
	"github.com/yassinebenameur/probara/shared/notifications"
	"github.com/yassinebenameur/probara/shared/notifications/plugin/builtin/email"
	"github.com/yassinebenameur/probara/shared/queue"
	"github.com/yassinebenameur/probara/shared/secrets"
	"github.com/yassinebenameur/probara/shared/testutil"
)

type workerMailer struct {
	mu   sync.Mutex
	sent []string
}

func (m *workerMailer) SendAlert(_ context.Context, _ notifications.AlertEvent, _ email.Templates, to []string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.sent = append(m.sent, to...)
	return nil
}

// An escalation page to an email contact has no channel: the worker sends it
// through the platform mailer, records a person row in the delivery history,
// and drops it (without touching any channel claim) once the alert is
// acknowledged.
func TestHandle_ChannelLessPersonPage(t *testing.T) {
	ctx := context.Background()
	dbClient, cleanup := testutil.SetupPostgresDB(ctx, t)
	defer cleanup()
	mailer := &workerMailer{}
	email.SetMailer(mailer)
	t.Cleanup(func() { email.SetMailer(nil) })

	enc := secrets.NewAESGCMEncryptor(testKeys{})
	tenantID := testutil.InsertTenant(ctx, t, dbClient, "person")
	monitorID := testutil.InsertHTTPMonitor(ctx, t, dbClient, tenantID, "API")
	alertID, userID, contactID := uuid.New(), uuid.New(), uuid.New()
	sealed, err := enc.Encrypt("oncall@example.com")
	if err != nil {
		t.Fatal(err)
	}
	for _, stmt := range []struct {
		sql  string
		args []any
	}{
		{`INSERT INTO alerts (id, tenant_id, monitor_id, alert_policy_id, kind, status, triggered_at, failure_count, created_at, updated_at)
			VALUES ($1, $2, $3, NULL, 'availability', 'active', NOW(), 3, NOW(), NOW())`, []any{alertID, tenantID, monitorID}},
		{`INSERT INTO admin_users (id, username, password_hash) VALUES ($1, 'oncall', 'x')`, []any{userID}},
		{`INSERT INTO user_contacts (id, user_id, kind, address, verified_at) VALUES ($1, $2, 'email', $3, NOW())`, []any{contactID, userID, sealed}},
	} {
		if _, err := dbClient.ExecContext(ctx, stmt.sql, stmt.args...); err != nil {
			t.Fatalf("%s: %v", stmt.sql, err)
		}
	}
	worker := &Consumer{logger: logger.New("test", "error"), db: dbClient, encryptor: enc}
	raw, _ := json.Marshal(notifications.DispatchEnvelope{
		V: 1, ChannelType: "email", AlertID: alertID.String(), EventType: "created",
		Event:       notifications.AlertEvent{Type: "created", Timestamp: time.Now().UTC()},
		DeliveryKey: "escalation:0:0", StopOnAck: true, ContactID: contactID.String(),
	})
	msg := &queue.Message{Data: raw, NumDelivered: 1}

	if err := worker.handle(ctx, msg); err != nil {
		t.Fatalf("handle: %v", err)
	}
	if len(mailer.sent) != 1 || mailer.sent[0] != "oncall@example.com" {
		t.Fatalf("sent = %v, want the contact's address", mailer.sent)
	}
	var outcome, recipient string
	if err := dbClient.QueryRowContext(ctx, `SELECT outcome, recipient FROM alert_notification_deliveries
		WHERE alert_id = $1 AND channel_id IS NULL AND user_id = $2`, alertID, userID).Scan(&outcome, &recipient); err != nil {
		t.Fatalf("person delivery row: %v", err)
	}
	if outcome != "delivered" || recipient != "o***@example.com" {
		t.Fatalf("delivery row = %s %s", outcome, recipient)
	}

	if _, err := dbClient.ExecContext(ctx, `UPDATE alerts SET status = 'acknowledged', acknowledged_at = NOW() WHERE id = $1`, alertID); err != nil {
		t.Fatal(err)
	}
	if err := worker.handle(ctx, msg); err != nil {
		t.Fatalf("handle after ack: %v", err)
	}
	if len(mailer.sent) != 1 {
		t.Fatalf("an acknowledged alert's page went out (%d sends)", len(mailer.sent))
	}
	var dropped int
	if err := dbClient.QueryRowContext(ctx, `SELECT COUNT(*) FROM alert_notification_deliveries
		WHERE alert_id = $1 AND outcome = 'dropped_ack' AND contact_id = $2`, alertID, contactID).Scan(&dropped); err != nil {
		t.Fatal(err)
	}
	if dropped != 1 {
		t.Fatalf("dropped_ack rows = %d, want 1", dropped)
	}
}
