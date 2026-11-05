//go:build !unit

package deliverylog_test

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"

	shareddb "github.com/yassinebenameur/probara/shared/db"
	"github.com/yassinebenameur/probara/shared/notifications/deliverylog"
	"github.com/yassinebenameur/probara/shared/testutil"
)

func insertAlertAndChannel(ctx context.Context, t *testing.T, db *shareddb.Client, tenantID uuid.UUID, name string) (alertID, channelID uuid.UUID) {
	t.Helper()
	monitorID := testutil.InsertHTTPMonitor(ctx, t, db, tenantID, "API "+name)
	alertID, channelID = uuid.New(), uuid.New()
	if _, err := db.ExecContext(ctx, `
		INSERT INTO alert_channels (id, tenant_id, name, type, config, is_active, created_at, updated_at)
		VALUES ($1, $2, $3, 'webhook', '{}'::jsonb, TRUE, NOW(), NOW())
	`, channelID, tenantID, name); err != nil {
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

func record(ctx context.Context, t *testing.T, db *shareddb.Client, e deliverylog.Entry) {
	t.Helper()
	if err := deliverylog.Record(ctx, db, e); err != nil {
		t.Fatalf("Record: %v", err)
	}
}

func TestRecordAndReadDeliveries(t *testing.T) {
	ctx := context.Background()
	db, cleanup := testutil.SetupPostgresDB(ctx, t)
	defer cleanup()

	tenantA := testutil.InsertTenant(ctx, t, db, "a")
	tenantB := testutil.InsertTenant(ctx, t, db, "b")
	alertA, channelA := insertAlertAndChannel(ctx, t, db, tenantA, "ops")
	alertB, channelB := insertAlertAndChannel(ctx, t, db, tenantB, "other")

	entry := func(outcome deliverylog.Outcome, err error) deliverylog.Entry {
		return deliverylog.Entry{
			AlertID: alertA, ChannelID: channelA, EventType: "created", DeliveryKey: "created",
			Path: deliverylog.PathSync, Outcome: outcome, Err: err,
		}
	}
	// Attempts are numbered per event and path when the caller has no counter.
	record(ctx, t, db, entry(deliverylog.OutcomeFailed, errors.New("503 from https://example.test/hook/s3cr3t")))
	record(ctx, t, db, entry(deliverylog.OutcomeDelivered, nil))
	worker := entry(deliverylog.OutcomeDroppedAck, nil)
	worker.Path, worker.Attempt, worker.ChannelName = deliverylog.PathAsync, 4, "renamed since"
	record(ctx, t, db, worker)

	// An alert never pairs with another workspace's channel.
	cross := entry(deliverylog.OutcomeDelivered, nil)
	cross.ChannelID = channelB
	record(ctx, t, db, cross)

	page, err := deliverylog.ListForAlert(ctx, db, tenantA, alertA, 1, 20)
	if err != nil {
		t.Fatalf("ListForAlert: %v", err)
	}
	if page.Total != 3 || len(page.Items) != 3 {
		t.Fatalf("alert deliveries = %d (%d items), want 3", page.Total, len(page.Items))
	}
	newest, oldest := page.Items[0], page.Items[2]
	if newest.Outcome != deliverylog.OutcomeDroppedAck || newest.Attempt != 4 || newest.ChannelName != "renamed since" {
		t.Errorf("newest = %+v, want the worker's dropped_ack attempt 4 under its snapshot name", newest)
	}
	if oldest.Outcome != deliverylog.OutcomeFailed || oldest.Attempt != 1 || oldest.ChannelName != "ops" ||
		oldest.Error == nil || *oldest.Error != "503 from https://example.test/…" {
		t.Errorf("oldest = %+v, want failed attempt 1 with a sanitized error", oldest)
	}
	if page.Items[1].Attempt != 2 || page.Items[1].ChannelType != "webhook" || page.Items[1].AlertKind != "availability" ||
		page.Items[1].MonitorName == nil || *page.Items[1].MonitorName != "API ops" {
		t.Errorf("middle = %+v, want delivered attempt 2 with channel type, alert kind and monitor name", page.Items[1])
	}

	// Paging.
	if p2, err := deliverylog.ListForChannel(ctx, db, tenantA, channelA, 2, 2); err != nil || p2.Total != 3 || len(p2.Items) != 1 {
		t.Fatalf("channel page 2 = %+v, %v; want 1 of 3", p2, err)
	}

	// Tenant isolation: the other workspace sees neither the alert nor the
	// channel, and its own channel has no rows.
	if _, err := deliverylog.ListForAlert(ctx, db, tenantB, alertA, 1, 20); !errors.Is(err, deliverylog.ErrNotFound) {
		t.Errorf("cross-tenant alert read err = %v, want ErrNotFound", err)
	}
	if _, err := deliverylog.ListForChannel(ctx, db, tenantB, channelA, 1, 20); !errors.Is(err, deliverylog.ErrNotFound) {
		t.Errorf("cross-tenant channel read err = %v, want ErrNotFound", err)
	}
	if p, err := deliverylog.ListForChannel(ctx, db, tenantB, channelB, 1, 20); err != nil || p.Total != 0 {
		t.Errorf("tenant B channel = %+v, %v; want empty", p, err)
	}
	if p, err := deliverylog.ListForAlert(ctx, db, tenantB, alertB, 1, 20); err != nil || p.Total != 0 {
		t.Errorf("tenant B alert = %+v, %v; want empty", p, err)
	}

	last, err := deliverylog.LastForChannels(ctx, db, tenantA, []uuid.UUID{channelA, channelB})
	if err != nil {
		t.Fatalf("LastForChannels: %v", err)
	}
	if len(last) != 1 || last[channelA].Outcome != deliverylog.OutcomeDroppedAck {
		t.Fatalf("last deliveries = %+v, want channel A's dropped_ack only", last)
	}

	// A workspace being deleted records nothing more.
	if _, err := db.ExecContext(ctx, `UPDATE tenants SET deleted_at = NOW() WHERE id = $1`, tenantB); err != nil {
		t.Fatalf("soft-delete tenant: %v", err)
	}
	record(ctx, t, db, deliverylog.Entry{
		AlertID: alertB, ChannelID: channelB, EventType: "created", DeliveryKey: "created",
		Path: deliverylog.PathSync, Outcome: deliverylog.OutcomeDelivered,
	})
	var n int
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM alert_notification_deliveries WHERE tenant_id = $1`, tenantB).Scan(&n); err != nil || n != 0 {
		t.Fatalf("deleted workspace rows = %d (%v), want 0", n, err)
	}
}
