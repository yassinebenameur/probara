//go:build !unit

package deliverylog_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"

	shareddb "github.com/yassinebenameur/probara/shared/db"
	"github.com/yassinebenameur/probara/shared/notifications/deliverylog"
	"github.com/yassinebenameur/probara/shared/notifications/recipientledger"
	"github.com/yassinebenameur/probara/shared/testutil"
)

func insertAlert(ctx context.Context, t *testing.T, db *shareddb.Client, tenantID uuid.UUID, name string) uuid.UUID {
	t.Helper()
	monitorID := testutil.InsertHTTPMonitor(ctx, t, db, tenantID, name)
	id := uuid.New()
	if _, err := db.ExecContext(ctx, `
		INSERT INTO alerts (id, tenant_id, monitor_id, kind, status, triggered_at, failure_count, created_at, updated_at)
		VALUES ($1, $2, $3, 'availability', 'active', NOW(), 3, NOW(), NOW())`, id, tenantID, monitorID); err != nil {
		t.Fatalf("insert alert: %v", err)
	}
	return id
}

func insertChannel(ctx context.Context, t *testing.T, db *shareddb.Client, tenantID uuid.UUID, name string) uuid.UUID {
	t.Helper()
	id := uuid.New()
	if _, err := db.ExecContext(ctx, `
		INSERT INTO alert_channels (id, tenant_id, name, type, config, is_active, created_at, updated_at)
		VALUES ($1, $2, $3, 'webhook', '{}'::jsonb, TRUE, NOW(), NOW())`, id, tenantID, name); err != nil {
		t.Fatalf("insert channel: %v", err)
	}
	return id
}

func countRows(ctx context.Context, t *testing.T, db *shareddb.Client, where string, args ...any) int {
	t.Helper()
	var n int
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM alert_notification_deliveries WHERE `+where, args...).Scan(&n); err != nil {
		t.Fatalf("count deliveries: %v", err)
	}
	return n
}

// fanout is a recipient ledger stub with one recipient already delivered.
type fanout struct{ recorded []string }

func (f *fanout) Delivered(_ context.Context, recipients []string) (map[string]bool, error) {
	return map[string]bool{recipients[0]: true}, nil
}
func (f *fanout) Record(_ context.Context, r string) error {
	f.recorded = append(f.recorded, r)
	return nil
}

func TestRecordWritesAttemptsPerTenantAndRecipient(t *testing.T) {
	ctx := context.Background()
	db, cleanup := testutil.SetupPostgresDB(ctx, t)
	defer cleanup()
	tenantA := testutil.InsertTenant(ctx, t, db, "deliveries-a")
	tenantB := testutil.InsertTenant(ctx, t, db, "deliveries-b")
	alertA := insertAlert(ctx, t, db, tenantA, "a")
	channelA := insertChannel(ctx, t, db, tenantA, "hook-a")
	channelB := insertChannel(ctx, t, db, tenantB, "hook-b")

	// Synchronous retries number themselves after the earlier rows.
	for i := 0; i < 2; i++ {
		if err := deliverylog.Record(ctx, db, deliverylog.Attempt{
			AlertID: alertA, ChannelID: channelA, EventType: "created", DeliveryKey: "created",
			Path: deliverylog.PathSync, Outcome: deliverylog.OutcomeFailed, Err: errors.New("timeout"),
		}); err != nil {
			t.Fatal(err)
		}
	}
	var attempts []int
	rows, err := db.QueryContext(ctx, `SELECT attempt FROM alert_notification_deliveries WHERE alert_id = $1 ORDER BY attempt`, alertA)
	if err != nil {
		t.Fatal(err)
	}
	for rows.Next() {
		var n int
		_ = rows.Scan(&n)
		attempts = append(attempts, n)
	}
	rows.Close()
	if len(attempts) != 2 || attempts[0] != 1 || attempts[1] != 2 {
		t.Fatalf("attempts = %v, want [1 2]", attempts)
	}

	// An alert never pairs with another workspace's channel.
	if err := deliverylog.Record(ctx, db, deliverylog.Attempt{
		AlertID: alertA, ChannelID: channelB, EventType: "created", Path: deliverylog.PathSync,
		Outcome: deliverylog.OutcomeDelivered,
	}); err != nil {
		t.Fatal(err)
	}
	if n := countRows(ctx, t, db, `channel_id = $1`, channelB); n != 0 {
		t.Fatalf("cross-tenant rows = %d, want 0", n)
	}

	// One row per recipient the attempt tried; the one an earlier attempt
	// reached gets none. The failed one carries the error, the delivered one
	// does not.
	ledger := &fanout{}
	tracker := deliverylog.Track(ledger)
	all := []string{"+15550000001", "+15550000002", "+15550000003"}
	if _, err := tracker.Delivered(ctx, all); err != nil {
		t.Fatal(err)
	}
	_ = tracker.Record(ctx, all[1])
	if err := deliverylog.Record(ctx, db, deliverylog.Attempt{
		AlertID: alertA, ChannelID: channelA, EventType: "reminder", DeliveryKey: "reminder:x",
		Path: deliverylog.PathAsync, Number: 2, Outcome: deliverylog.OutcomeFailed,
		Err: errors.New("+15550000003 unreachable"), Recipients: tracker,
	}); err != nil {
		t.Fatal(err)
	}
	type row struct {
		recipient, hash, outcome string
		errText                  *string
		attempt                  int
	}
	var got []row
	rows, err = db.QueryContext(ctx, `SELECT recipient, recipient_hash, outcome, error, attempt FROM alert_notification_deliveries
		WHERE delivery_key = 'reminder:x' ORDER BY recipient`)
	if err != nil {
		t.Fatal(err)
	}
	for rows.Next() {
		var r row
		if err := rows.Scan(&r.recipient, &r.hash, &r.outcome, &r.errText, &r.attempt); err != nil {
			t.Fatal(err)
		}
		got = append(got, r)
	}
	rows.Close()
	if len(got) != 2 {
		t.Fatalf("recipient rows = %+v, want 2 (the already delivered one skipped)", got)
	}
	if got[0].recipient != "********0002" || got[0].outcome != "delivered" || got[0].errText != nil || got[0].hash != recipientledger.Hash(all[1]) {
		t.Errorf("delivered row = %+v", got[0])
	}
	if got[1].recipient != "********0003" || got[1].outcome != "failed" || got[1].errText == nil || got[1].attempt != 2 {
		t.Errorf("failed row = %+v", got[1])
	}

	// A retry that finds every recipient delivered writes nothing.
	empty := deliverylog.Track(&fanout{})
	if _, err := empty.Delivered(ctx, all[:1]); err != nil {
		t.Fatal(err)
	}
	if err := deliverylog.Record(ctx, db, deliverylog.Attempt{
		AlertID: alertA, ChannelID: channelA, EventType: "resolved", Path: deliverylog.PathSync,
		Outcome: deliverylog.OutcomeDelivered, Recipients: empty,
	}); err != nil {
		t.Fatal(err)
	}
	if n := countRows(ctx, t, db, `event_type = 'resolved'`); n != 0 {
		t.Fatalf("rows for an attempt that sent nothing = %d, want 0", n)
	}

	// A test send has no alert.
	if err := deliverylog.Record(ctx, db, deliverylog.Attempt{
		ChannelID: channelB, EventType: "created", Path: deliverylog.PathSync, Number: 1, Test: true,
		Outcome: deliverylog.OutcomeDelivered,
	}); err != nil {
		t.Fatal(err)
	}
	if n := countRows(ctx, t, db, `channel_id = $1 AND is_test AND alert_id IS NULL AND tenant_id = $2`, channelB, tenantB); n != 1 {
		t.Fatalf("test rows = %d, want 1", n)
	}
}

// A write that fails inside the caller's transaction is rolled back to its
// savepoint: the transaction still commits its own work.
func TestRecordTxNeverBreaksTheCallerTransaction(t *testing.T) {
	ctx := context.Background()
	db, cleanup := testutil.SetupPostgresDB(ctx, t)
	defer cleanup()
	tenantID := testutil.InsertTenant(ctx, t, db, "deliveries-tx")
	alertID := insertAlert(ctx, t, db, tenantID, "a")
	channelID := insertChannel(ctx, t, db, tenantID, "hook")

	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := tx.ExecContext(ctx, `UPDATE alerts SET failure_count = 9 WHERE id = $1`, alertID); err != nil {
		t.Fatal(err)
	}
	// An outcome the CHECK constraint refuses.
	if err := deliverylog.RecordTx(ctx, tx, deliverylog.Attempt{
		AlertID: alertID, ChannelID: channelID, EventType: "created", Path: deliverylog.PathSync, Outcome: "bogus",
	}); err == nil {
		t.Fatal("RecordTx accepted an invalid outcome")
	}
	if err := deliverylog.RecordTx(ctx, tx, deliverylog.Attempt{
		AlertID: alertID, ChannelID: channelID, EventType: "created", Path: deliverylog.PathSync,
		Outcome: deliverylog.OutcomeDelivered,
	}); err != nil {
		t.Fatalf("RecordTx after a failed write: %v", err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatalf("commit after a failed history write: %v", err)
	}
	var fc int
	if err := db.QueryRowContext(ctx, `SELECT failure_count FROM alerts WHERE id = $1`, alertID).Scan(&fc); err != nil || fc != 9 {
		t.Fatalf("caller's update = %d (%v), want 9", fc, err)
	}
	if n := countRows(ctx, t, db, `alert_id = $1`, alertID); n != 1 {
		t.Fatalf("rows = %d, want 1", n)
	}
}

func TestReadersAndChannelHealth(t *testing.T) {
	ctx := context.Background()
	db, cleanup := testutil.SetupPostgresDB(ctx, t)
	defer cleanup()
	tenantA := testutil.InsertTenant(ctx, t, db, "health-a")
	tenantB := testutil.InsertTenant(ctx, t, db, "health-b")
	alertA := insertAlert(ctx, t, db, tenantA, "a")
	healthy := insertChannel(ctx, t, db, tenantA, "healthy")
	failing := insertChannel(ctx, t, db, tenantA, "failing")
	recovered := insertChannel(ctx, t, db, tenantA, "recovered")
	stale := insertChannel(ctx, t, db, tenantA, "stale")
	dropsOnly := insertChannel(ctx, t, db, tenantA, "drops")
	foreign := insertChannel(ctx, t, db, tenantB, "foreign")

	insert := func(channel uuid.UUID, outcome string, ago time.Duration) {
		t.Helper()
		if _, err := db.ExecContext(ctx, `
			INSERT INTO alert_notification_deliveries (tenant_id, alert_id, channel_id, event_type, attempt, path, outcome, created_at)
			SELECT tenant_id, $1, $2, 'created', 1, 'sync', $3, NOW() - ($4::double precision * INTERVAL '1 second')
			FROM alert_channels WHERE id = $2`, alertA, channel, outcome, ago.Seconds()); err != nil {
			t.Fatalf("insert: %v", err)
		}
	}
	insert(healthy, "delivered", time.Minute)
	insert(failing, "delivered", 2*time.Hour)
	insert(failing, "permanent_failure", time.Hour)
	insert(recovered, "failed", 2*time.Hour)
	insert(recovered, "delivered", time.Hour)
	insert(stale, "failed", 48*time.Hour)
	insert(dropsOnly, "dropped", time.Minute)

	health, err := deliverylog.ChannelHealth(ctx, db, tenantA, []uuid.UUID{healthy, failing, recovered, stale, dropsOnly, foreign})
	if err != nil {
		t.Fatal(err)
	}
	for ch, want := range map[uuid.UUID]bool{healthy: false, failing: true, recovered: false, stale: false, dropsOnly: false} {
		if health[ch].Failing != want {
			t.Errorf("channel %s failing = %v, want %v (%+v)", ch, health[ch].Failing, want, health[ch])
		}
	}
	if h := health[failing]; h.LastDeliveredAt == nil || h.LastFailedAt == nil {
		t.Errorf("failing channel health = %+v, want both times", h)
	}
	if _, ok := health[foreign]; ok {
		t.Error("ChannelHealth returned another workspace's channel")
	}

	// Per alert: oldest first. Per channel: newest first, filterable.
	page, err := deliverylog.ListForAlert(ctx, db, tenantA, alertA, 1, 50)
	if err != nil {
		t.Fatal(err)
	}
	if page.Total != 7 || !page.Items[0].CreatedAt.Before(page.Items[len(page.Items)-1].CreatedAt) {
		t.Fatalf("alert page = %d items, want 7 oldest first", page.Total)
	}
	page, err = deliverylog.ListForChannel(ctx, db, tenantA, failing, []deliverylog.Outcome{deliverylog.OutcomePermanentFailure}, 1, 50)
	if err != nil {
		t.Fatal(err)
	}
	if page.Total != 1 || page.Items[0].Outcome != deliverylog.OutcomePermanentFailure || page.Items[0].MonitorName == nil {
		t.Fatalf("filtered channel page = %+v", page)
	}

	// Another workspace sees neither the alert nor the channel.
	if _, err := deliverylog.ListForAlert(ctx, db, tenantB, alertA, 1, 50); !errors.Is(err, deliverylog.ErrNotFound) {
		t.Fatalf("cross-tenant alert read: %v, want ErrNotFound", err)
	}
	if _, err := deliverylog.ListForChannel(ctx, db, tenantB, failing, nil, 1, 50); !errors.Is(err, deliverylog.ErrNotFound) {
		t.Fatalf("cross-tenant channel read: %v, want ErrNotFound", err)
	}
}
