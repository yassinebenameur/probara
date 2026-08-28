package statuspage

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/yassinebenameur/probara/shared/testutil"
)

// pushFixture builds a page with one monitor, one subscriber, and push
// enabled -- the minimum for the reconciler to produce anything.
type pushFixture struct {
	store     *pushStore
	tenantID  uuid.UUID
	pageID    uuid.UUID
	monitorID uuid.UUID
}

func setupPushFixture(ctx context.Context, t *testing.T) (*pushFixture, func()) {
	t.Helper()

	dbClient, cleanup := testutil.SetupPostgresDB(ctx, t)

	tenantID := testutil.InsertTenant(ctx, t, dbClient, "status-page-push")
	pageID := testutil.InsertStatusPage(ctx, t, dbClient, tenantID, "status", "Acme Status")
	monitorID := testutil.InsertHTTPMonitor(ctx, t, dbClient, tenantID, "API gateway")
	testutil.AddMonitorToStatusPage(ctx, t, dbClient, pageID, monitorID, 0)

	if _, err := dbClient.ExecContext(ctx,
		`UPDATE status_pages SET settings = '{"enable_push_notifications": true}'::jsonb WHERE id = $1`,
		pageID); err != nil {
		t.Fatalf("enable push on status page: %v", err)
	}

	store := newPushStore(dbClient.DB)
	if _, err := store.Subscribe(ctx, pageID, pushSubscriptionRow{
		Endpoint: "https://fcm.googleapis.com/fcm/send/test-endpoint-value",
		P256dh:   "BCVxsr7N_eNgVRqvHtD0zTZsEc6-VV-JvLexhqUzORcxaOzi6-AYWXvTBHm4bjyPjs7Vd8pZGH6SRpkNtoIAiw4",
		Auth:     "BTBZMqHH6r4Tts7J_aSIgg",
	}, "test", 100); err != nil {
		t.Fatalf("Subscribe() error = %v", err)
	}

	return &pushFixture{store: store, tenantID: tenantID, pageID: pageID, monitorID: monitorID}, cleanup
}

// addInterval appends one state interval, closing whatever was open, the way
// monitorstate.RecordIntervalTx does.
func (f *pushFixture) addInterval(ctx context.Context, t *testing.T, state string, startedAgo time.Duration) {
	t.Helper()
	startedAt := time.Now().Add(-startedAgo)

	if _, err := f.store.db.ExecContext(ctx,
		`UPDATE monitor_state_intervals SET ended_at = $2 WHERE monitor_id = $1 AND ended_at IS NULL`,
		f.monitorID, startedAt); err != nil {
		t.Fatalf("close open interval: %v", err)
	}
	if _, err := f.store.db.ExecContext(ctx,
		`INSERT INTO monitor_state_intervals (tenant_id, monitor_id, state, reason, started_at)
		 VALUES ($1, $2, $3, 'result', $4)`,
		f.tenantID, f.monitorID, state, startedAt); err != nil {
		t.Fatalf("insert %s interval: %v", state, err)
	}
}

func (f *pushFixture) deliveries(ctx context.Context, t *testing.T) []string {
	t.Helper()
	rows, err := f.store.db.QueryContext(ctx,
		`SELECT kind FROM status_page_push_deliveries WHERE status_page_id = $1 ORDER BY created_at, kind`,
		f.pageID)
	if err != nil {
		t.Fatalf("read deliveries: %v", err)
	}
	defer rows.Close()

	var kinds []string
	for rows.Next() {
		var kind string
		if err := rows.Scan(&kind); err != nil {
			t.Fatalf("scan delivery: %v", err)
		}
		kinds = append(kinds, kind)
	}
	return kinds
}

// The core sequences, against real SQL. These mirror the cases pinned in
// TestClassifyPushTransition, which is the point: the Go rule and the SQL
// must agree, so both are exercised over the same scenarios.
func TestReconcileNotifications_NotifyRuleMatchesClassify(t *testing.T) {
	tests := []struct {
		name   string
		states []string
		want   []string
	}{
		{"single outage", []string{"up", "down"}, []string{"down"}},
		{"outage then recovery", []string{"up", "down", "up"}, []string{"down", "recovered"}},
		// The suspect step is invisible: previous-announced is still up.
		{"suspect on the way down", []string{"up", "suspect", "down"}, []string{"down"}},
		// Already announced down, so the re-entry is silent.
		{"suspect between two downs", []string{"up", "down", "suspect", "down"}, []string{"down"}},
		// Degraded is not a recovery, and not an outage.
		{"degraded between down and up", []string{"up", "down", "degraded", "up"}, []string{"down", "recovered"}},
		{"degraded alone", []string{"up", "degraded"}, nil},
		{"suspect alone", []string{"up", "suspect"}, nil},
		// A recovery with no announced outage before it is not news.
		{"up from unknown", []string{"unknown", "up"}, nil},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := context.Background()
			f, cleanup := setupPushFixture(ctx, t)
			defer cleanup()

			// Spaced well past the flap debounce and inside the horizon.
			ago := 10 * time.Minute
			for _, state := range tt.states {
				f.addInterval(ctx, t, state, ago)
				ago -= 2 * time.Minute
			}

			if _, err := f.store.reconcileNotifications(ctx); err != nil {
				t.Fatalf("reconcileNotifications() error = %v", err)
			}

			got := f.deliveries(ctx, t)
			if fmt.Sprint(got) != fmt.Sprint(tt.want) {
				t.Fatalf("deliveries = %v, want %v", got, tt.want)
			}

			// Every sequence must also agree with the pure rule.
			var wantFromGo []string
			previous := ""
			for _, state := range tt.states {
				if kind, ok := classifyPushTransition(previous, state); ok {
					wantFromGo = append(wantFromGo, string(kind))
				}
				if state == "down" || state == "up" {
					previous = state
				}
			}
			if fmt.Sprint(got) != fmt.Sprint(wantFromGo) {
				t.Fatalf("SQL produced %v but classifyPushTransition says %v", got, wantFromGo)
			}
		})
	}
}

// THE regression test for this design. Two replicas run the identical
// reconcile-and-claim concurrently; the unique index on
// (status_page_id, interval_id) and FOR UPDATE SKIP LOCKED must yield exactly
// one delivery, claimed by exactly one of them. This is the bug class the
// alerter already has in production with duplicate DOWN emails.
func TestReconcileAndClaim_IsExactlyOnceAcrossReplicas(t *testing.T) {
	ctx := context.Background()
	f, cleanup := setupPushFixture(ctx, t)
	defer cleanup()

	f.addInterval(ctx, t, "up", 10*time.Minute)
	f.addInterval(ctx, t, "down", 5*time.Minute)

	const replicas = 4
	var wg sync.WaitGroup
	var mu sync.Mutex
	var claimedTotal int

	wg.Add(replicas)
	for i := 0; i < replicas; i++ {
		go func() {
			defer wg.Done()
			if _, err := f.store.reconcileNotifications(ctx); err != nil {
				t.Errorf("reconcileNotifications() error = %v", err)
				return
			}
			claimed, err := f.store.ClaimDeliveries(ctx, 50, pushClaimStaleAfter)
			if err != nil {
				t.Errorf("ClaimDeliveries() error = %v", err)
				return
			}
			mu.Lock()
			claimedTotal += len(claimed)
			mu.Unlock()
		}()
	}
	wg.Wait()

	if got := f.deliveries(ctx, t); len(got) != 1 {
		t.Fatalf("%d replicas produced %d delivery rows (%v), want exactly 1", replicas, len(got), got)
	}
	if claimedTotal != 1 {
		t.Fatalf("%d replicas claimed the delivery %d times, want exactly 1", replicas, claimedTotal)
	}
}

// Repeated passes must not re-notify: the ledger key is the interval id, so a
// transition is announced once no matter how often the loop runs.
func TestReconcileNotifications_IsIdempotentAcrossPasses(t *testing.T) {
	ctx := context.Background()
	f, cleanup := setupPushFixture(ctx, t)
	defer cleanup()

	f.addInterval(ctx, t, "up", 10*time.Minute)
	f.addInterval(ctx, t, "down", 5*time.Minute)

	for i := 0; i < 3; i++ {
		if _, err := f.store.reconcileNotifications(ctx); err != nil {
			t.Fatalf("pass %d: reconcileNotifications() error = %v", i, err)
		}
	}
	if got := f.deliveries(ctx, t); len(got) != 1 {
		t.Fatalf("three passes produced %d deliveries (%v), want 1", len(got), got)
	}
}

// A transition that has not yet outlived the debounce must create no row at
// all -- suppressing at creation is what keeps the recovery side symmetric.
func TestReconcileNotifications_DebouncesFlaps(t *testing.T) {
	ctx := context.Background()
	f, cleanup := setupPushFixture(ctx, t)
	defer cleanup()

	f.addInterval(ctx, t, "up", 10*time.Minute)
	f.addInterval(ctx, t, "down", 5*time.Second) // inside pushFlapDebounce

	if _, err := f.store.reconcileNotifications(ctx); err != nil {
		t.Fatalf("reconcileNotifications() error = %v", err)
	}
	if got := f.deliveries(ctx, t); len(got) != 0 {
		t.Fatalf("a transition inside the debounce produced %v, want none", got)
	}
}

// Migration 000082 seeded one reason='seed' interval per existing monitor. If
// those were notifiable, the first deploy would notify every subscriber about
// every monitor at once.
func TestReconcileNotifications_IgnoresSeedIntervals(t *testing.T) {
	ctx := context.Background()
	f, cleanup := setupPushFixture(ctx, t)
	defer cleanup()

	if _, err := f.store.db.ExecContext(ctx,
		`UPDATE monitor_state_intervals SET ended_at = NOW() WHERE monitor_id = $1 AND ended_at IS NULL`,
		f.monitorID); err != nil {
		t.Fatalf("close open interval: %v", err)
	}
	if _, err := f.store.db.ExecContext(ctx,
		`INSERT INTO monitor_state_intervals (tenant_id, monitor_id, state, reason, started_at)
		 VALUES ($1, $2, 'down', 'seed', NOW() - interval '5 minutes')`,
		f.tenantID, f.monitorID); err != nil {
		t.Fatalf("insert seed interval: %v", err)
	}

	if _, err := f.store.reconcileNotifications(ctx); err != nil {
		t.Fatalf("reconcileNotifications() error = %v", err)
	}
	if got := f.deliveries(ctx, t); len(got) != 0 {
		t.Fatalf("a seed interval produced %v, want none", got)
	}
}

// A page that never opted in must produce nothing even while its monitors
// transition normally.
func TestReconcileNotifications_SkipsPagesWithPushDisabled(t *testing.T) {
	ctx := context.Background()
	f, cleanup := setupPushFixture(ctx, t)
	defer cleanup()

	if _, err := f.store.db.ExecContext(ctx,
		`UPDATE status_pages SET settings = '{}'::jsonb WHERE id = $1`, f.pageID); err != nil {
		t.Fatalf("disable push: %v", err)
	}

	f.addInterval(ctx, t, "up", 10*time.Minute)
	f.addInterval(ctx, t, "down", 5*time.Minute)

	if _, err := f.store.reconcileNotifications(ctx); err != nil {
		t.Fatalf("reconcileNotifications() error = %v", err)
	}
	if got := f.deliveries(ctx, t); len(got) != 0 {
		t.Fatalf("a page with push disabled produced %v, want none", got)
	}
}

// No subscribers, no work -- this is what bounds the blast radius when an
// operator first enables the setting on a busy page.
func TestReconcileNotifications_SkipsPagesWithNoSubscribers(t *testing.T) {
	ctx := context.Background()
	f, cleanup := setupPushFixture(ctx, t)
	defer cleanup()

	if _, err := f.store.db.ExecContext(ctx,
		`DELETE FROM status_page_push_subscriptions WHERE status_page_id = $1`, f.pageID); err != nil {
		t.Fatalf("remove subscriptions: %v", err)
	}

	f.addInterval(ctx, t, "up", 10*time.Minute)
	f.addInterval(ctx, t, "down", 5*time.Minute)

	if _, err := f.store.reconcileNotifications(ctx); err != nil {
		t.Fatalf("reconcileNotifications() error = %v", err)
	}
	if got := f.deliveries(ctx, t); len(got) != 0 {
		t.Fatalf("a page with no subscribers produced %v, want none", got)
	}
}

// Re-subscribing is the normal path: the page re-posts about once a day to
// keep last_seen_at fresh, and the browser may return rotated keys.
func TestSubscribe_IsIdempotentAndRefreshes(t *testing.T) {
	ctx := context.Background()
	f, cleanup := setupPushFixture(ctx, t)
	defer cleanup()

	sub := pushSubscriptionRow{
		Endpoint: "https://fcm.googleapis.com/fcm/send/test-endpoint-value",
		P256dh:   "BCVxsr7N_eNgVRqvHtD0zTZsEc6-VV-JvLexhqUzORcxaOzi6-AYWXvTBHm4bjyPjs7Vd8pZGH6SRpkNtoIAiw4",
		Auth:     "BTBZMqHH6r4Tts7J_aSIgg",
	}
	if ok, err := f.store.Subscribe(ctx, f.pageID, sub, "test", 100); err != nil || !ok {
		t.Fatalf("re-Subscribe() = (%v, %v), want (true, nil)", ok, err)
	}

	subs, err := f.store.SubscriptionsForPage(ctx, f.pageID)
	if err != nil {
		t.Fatalf("SubscriptionsForPage() error = %v", err)
	}
	if len(subs) != 1 {
		t.Fatalf("re-subscribing created %d rows, want 1", len(subs))
	}
}

// The cap must hold as a statement-level guard, not a read-then-write.
func TestSubscribe_EnforcesPerPageCap(t *testing.T) {
	ctx := context.Background()
	f, cleanup := setupPushFixture(ctx, t)
	defer cleanup()

	// The fixture already holds one subscription, so a cap of 1 is full.
	ok, err := f.store.Subscribe(ctx, f.pageID, pushSubscriptionRow{
		Endpoint: "https://fcm.googleapis.com/fcm/send/another-endpoint-value",
		P256dh:   "BCVxsr7N_eNgVRqvHtD0zTZsEc6-VV-JvLexhqUzORcxaOzi6-AYWXvTBHm4bjyPjs7Vd8pZGH6SRpkNtoIAiw4",
		Auth:     "BTBZMqHH6r4Tts7J_aSIgg",
	}, "test", 1)
	if err != nil {
		t.Fatalf("Subscribe() error = %v", err)
	}
	if ok {
		t.Fatalf("Subscribe() accepted a subscription past the per-page cap")
	}
}

// Only 404/410 delete; this pins the delete path itself.
func TestDeleteSubscription_RemovesTheRow(t *testing.T) {
	ctx := context.Background()
	f, cleanup := setupPushFixture(ctx, t)
	defer cleanup()

	subs, err := f.store.SubscriptionsForPage(ctx, f.pageID)
	if err != nil || len(subs) != 1 {
		t.Fatalf("SubscriptionsForPage() = (%v, %v), want one subscription", subs, err)
	}
	if err := f.store.DeleteSubscription(ctx, subs[0].ID); err != nil {
		t.Fatalf("DeleteSubscription() error = %v", err)
	}

	after, err := f.store.SubscriptionsForPage(ctx, f.pageID)
	if err != nil {
		t.Fatalf("SubscriptionsForPage() error = %v", err)
	}
	if len(after) != 0 {
		t.Fatalf("subscription survived deletion: %v", after)
	}
}
