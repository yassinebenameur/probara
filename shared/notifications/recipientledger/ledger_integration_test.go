//go:build !unit

package recipientledger_test

import (
	"context"
	"testing"

	"github.com/google/uuid"

	shareddb "github.com/yassinebenameur/probara/shared/db"
	"github.com/yassinebenameur/probara/shared/notifications/recipientledger"
	"github.com/yassinebenameur/probara/shared/testutil"
)

func insertAlertAndChannel(ctx context.Context, t *testing.T, db *shareddb.Client, tenantID uuid.UUID) (alertID, channelID uuid.UUID) {
	t.Helper()
	monitorID := testutil.InsertHTTPMonitor(ctx, t, db, tenantID, "API")
	alertID, channelID = uuid.New(), uuid.New()
	if _, err := db.ExecContext(ctx, `
		INSERT INTO alert_channels (id, tenant_id, name, type, config, is_active, created_at, updated_at)
		VALUES ($1, $2, 'sms', 'twilio_sms', '{}'::jsonb, TRUE, NOW(), NOW())
	`, channelID, tenantID); err != nil {
		t.Fatalf("insert channel: %v", err)
	}
	if _, err := db.ExecContext(ctx, `
		INSERT INTO alerts (id, tenant_id, monitor_id, alert_policy_id, kind, status,
			triggered_at, failure_count, created_at, updated_at)
		VALUES ($1, $2, $3, NULL, 'availability', 'active', NOW(), 3, NOW(), NOW())
	`, alertID, tenantID, monitorID); err != nil {
		t.Fatalf("insert alert: %v", err)
	}
	return alertID, channelID
}

func TestLedgerRecordsPerEventAndTenant(t *testing.T) {
	ctx := context.Background()
	db, cleanup := testutil.SetupPostgresDB(ctx, t)
	defer cleanup()

	tenantA := testutil.InsertTenant(ctx, t, db, "a")
	tenantB := testutil.InsertTenant(ctx, t, db, "b")
	alertID, channelID := insertAlertAndChannel(ctx, t, db, tenantA)
	_, foreignChannel := insertAlertAndChannel(ctx, t, db, tenantB)
	recipients := []string{"+15557654321", "+447700900123"}

	created := recipientledger.For(db, alertID, channelID, "created")
	if done, err := created.Delivered(ctx, recipients); err != nil || len(done) != 0 {
		t.Fatalf("fresh ledger = %v, %v; want empty", done, err)
	}
	if err := created.Record(ctx, recipients[0]); err != nil {
		t.Fatalf("Record: %v", err)
	}
	// Recording twice (a retry racing a slow commit) is harmless.
	if err := created.Record(ctx, recipients[0]); err != nil {
		t.Fatalf("Record again: %v", err)
	}
	done, err := created.Delivered(ctx, recipients)
	if err != nil || !done[recipients[0]] || done[recipients[1]] {
		t.Fatalf("Delivered = %v, %v; want only the first recipient", done, err)
	}

	// Another event of the same pair, or a new handle in another process,
	// sees its own progress only / the same progress respectively.
	if done, _ := recipientledger.For(db, alertID, channelID, "resolved").Delivered(ctx, recipients); len(done) != 0 {
		t.Fatalf("resolved ledger = %v, want empty", done)
	}
	if done, _ := recipientledger.For(db, alertID, channelID, "created").Delivered(ctx, recipients); !done[recipients[0]] {
		t.Fatalf("a second handle must see the recorded delivery, got %v", done)
	}

	// An alert never pairs with another tenant's channel.
	cross := recipientledger.For(db, alertID, foreignChannel, "created")
	if err := cross.Record(ctx, recipients[0]); err != nil {
		t.Fatalf("cross-tenant Record: %v", err)
	}
	var n int
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM alert_notification_recipient_deliveries WHERE channel_id = $1`, foreignChannel).Scan(&n); err != nil || n != 0 {
		t.Fatalf("cross-tenant rows = %d (%v), want 0", n, err)
	}

	// Phone numbers are not stored in clear.
	var stored string
	if err := db.QueryRowContext(ctx, `SELECT recipient_hash FROM alert_notification_recipient_deliveries LIMIT 1`).Scan(&stored); err != nil {
		t.Fatalf("read row: %v", err)
	}
	if stored == recipients[0] || len(stored) != 64 {
		t.Fatalf("recipient_hash = %q, want a SHA-256 hex digest", stored)
	}

	// Rows go with their alert.
	if _, err := db.ExecContext(ctx, `DELETE FROM alerts WHERE id = $1`, alertID); err != nil {
		t.Fatalf("delete alert: %v", err)
	}
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM alert_notification_recipient_deliveries`).Scan(&n); err != nil || n != 0 {
		t.Fatalf("rows after alert delete = %d (%v), want 0", n, err)
	}
}

// Before migration 000097 lands nothing can have been recorded, so the read
// reports none delivered instead of failing every SMS send.
func TestLedgerWithoutTableReportsNothingDelivered(t *testing.T) {
	ctx := context.Background()
	db, cleanup := testutil.SetupPostgresDB(ctx, t)
	defer cleanup()
	tenantID := testutil.InsertTenant(ctx, t, db, "a")
	alertID, channelID := insertAlertAndChannel(ctx, t, db, tenantID)
	if _, err := db.ExecContext(ctx, `DROP TABLE alert_notification_recipient_deliveries`); err != nil {
		t.Fatalf("drop: %v", err)
	}
	l := recipientledger.For(db, alertID, channelID, "created")
	done, err := l.Delivered(ctx, []string{"+15557654321"})
	if err != nil || len(done) != 0 {
		t.Fatalf("Delivered = %v, %v; want empty, nil", done, err)
	}
	if err := l.Record(ctx, "+15557654321"); err == nil {
		t.Fatalf("Record without the table must report the failure")
	}
}
