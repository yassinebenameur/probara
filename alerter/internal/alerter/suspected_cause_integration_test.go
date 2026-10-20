//go:build !unit

package alerter

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/lib/pq"

	"github.com/yassinebenameur/probara/shared/ai"
	"github.com/yassinebenameur/probara/shared/alertrouting"
	shareddb "github.com/yassinebenameur/probara/shared/db"
	"github.com/yassinebenameur/probara/shared/notifications/plugin"
	"github.com/yassinebenameur/probara/shared/testutil"
)

// followUpStubType is a chat-style plugin registered only in this test
// binary: it advertises CapabilityFollowUp, so dispatchSuspectedCauses targets
// it. ackStubType (acknowledge_integration_test.go) stands in for a paging
// tool, which does not.
const followUpStubType = "test_followup_stub"

type followUpStubPlugin struct{}

func (followUpStubPlugin) Manifest() plugin.Manifest {
	return plugin.Manifest{Type: followUpStubType, Capabilities: []plugin.Capability{plugin.CapabilityFollowUp}}
}
func (followUpStubPlugin) Validate(json.RawMessage) error                     { return nil }
func (followUpStubPlugin) Send(context.Context, plugin.DispatchRequest) error { return nil }

func init() { plugin.Register(followUpStubPlugin{}) }

// causeSend is one captured sendFunc call.
type causeSend struct {
	eventType string
	channelID uuid.UUID
	record    alertRecord
}

type causeRecorder struct {
	mu    sync.Mutex
	sends []causeSend
}

func (r *causeRecorder) send(_ context.Context, ch alertChannel, eventType string, _ policyBinding, alert *alertRecord, _ *groupDetail, _ time.Time) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.sends = append(r.sends, causeSend{eventType: eventType, channelID: ch.ID, record: *alert})
	return nil
}

// count returns the sends of eventType for alertID (uuid.Nil = any alert).
func (r *causeRecorder) count(eventType string, alertID uuid.UUID) int {
	r.mu.Lock()
	defer r.mu.Unlock()
	n := 0
	for _, s := range r.sends {
		if s.eventType == eventType && (alertID == uuid.Nil || s.record.ID == alertID) {
			n++
		}
	}
	return n
}

func (r *causeRecorder) last(eventType string) (causeSend, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for i := len(r.sends) - 1; i >= 0; i-- {
		if r.sends[i].eventType == eventType {
			return r.sends[i], true
		}
	}
	return causeSend{}, false
}

// causeCase is one tenant with an open, paged alert whose triage is ready
// with a named, high-confidence cause, follow-ups opted in an hour before the
// triage completed, and the page old enough to have settled: every gate
// passes until a test mutates one.
type causeCase struct {
	tenantID, monitorID, alertID, channelID, triageID uuid.UUID
}

func enableTriageNotifications(ctx context.Context, t *testing.T, db *shareddb.Client, tenantID uuid.UUID, enabledAgo time.Duration) {
	t.Helper()
	mustExec(ctx, t, db, `
		INSERT INTO ai_settings (tenant_id, enabled, auto_triage_enabled, triage_notifications_enabled, triage_notifications_enabled_at)
		VALUES ($1, TRUE, TRUE, TRUE, NOW() - make_interval(secs => $2))
		ON CONFLICT (tenant_id) DO UPDATE SET enabled = TRUE, auto_triage_enabled = TRUE,
			triage_notifications_enabled = TRUE, triage_notifications_enabled_at = EXCLUDED.triage_notifications_enabled_at
	`, tenantID, enabledAgo.Seconds())
}

func insertReadyTriage(ctx context.Context, t *testing.T, db *shareddb.Client, tenantID, alertID, monitorID uuid.UUID, hypothesis, confidence string, completedAgo time.Duration) uuid.UUID {
	t.Helper()
	var id uuid.UUID
	if err := db.QueryRowContext(ctx, `
		INSERT INTO alert_triage (tenant_id, alert_id, monitor_id, status, hypothesis, confidence, summary, created_at, completed_at)
		VALUES ($1, $2, $3, 'ready', $4, $5, 'DNS lookups failed.', NOW() - make_interval(secs => $6) - INTERVAL '1 minute',
			NOW() - make_interval(secs => $6))
		RETURNING id
	`, tenantID, alertID, monitorID, hypothesis, confidence, completedAgo.Seconds()).Scan(&id); err != nil {
		t.Fatalf("insert triage: %v", err)
	}
	return id
}

// backdatePage moves a channel's page (or every page of the alert when
// channelID is uuid.Nil) ago into the past.
func backdatePage(ctx context.Context, t *testing.T, db *shareddb.Client, alertID, channelID uuid.UUID, ago time.Duration) {
	t.Helper()
	mustExec(ctx, t, db, `
		UPDATE alert_notification_states
		SET created_at = NOW() - make_interval(secs => $3), last_sent_at = NOW() - make_interval(secs => $3)
		WHERE alert_id = $1 AND ($2 = '00000000-0000-0000-0000-000000000000'::uuid OR channel_id = $2)
	`, alertID, channelID, ago.Seconds())
}

func newCauseCase(ctx context.Context, t *testing.T, db *shareddb.Client, channelType string) causeCase {
	t.Helper()
	var c causeCase
	c.tenantID = testutil.InsertTenant(ctx, t, db, "")
	c.monitorID = testutil.InsertHTTPMonitor(ctx, t, db, c.tenantID, "API")
	c.channelID = insertTypedChannel(ctx, t, db, c.tenantID, channelType)
	c.alertID = openDownAlert(ctx, t, db, c.tenantID, c.monitorID)
	pager := newIntegrationAlerter(db)
	if err := pager.dispatchOpenAlerts(ctx); err != nil {
		t.Fatalf("page: %v", err)
	}
	if _, _, ok := notificationStateFor(ctx, t, db, c.alertID, c.channelID); !ok {
		t.Fatal("the alert was not paged")
	}
	backdatePage(ctx, t, db, c.alertID, uuid.Nil, 5*time.Minute)
	enableTriageNotifications(ctx, t, db, c.tenantID, time.Hour)
	c.triageID = insertReadyTriage(ctx, t, db, c.tenantID, c.alertID, c.monitorID, "dns_failure", "high", time.Minute)
	return c
}

func suspectedCauseSentAt(ctx context.Context, t *testing.T, db *shareddb.Client, alertID, channelID uuid.UUID) *time.Time {
	t.Helper()
	var at *time.Time
	if err := db.QueryRowContext(ctx, `
		SELECT suspected_cause_sent_at FROM alert_notification_states WHERE alert_id = $1 AND channel_id = $2
	`, alertID, channelID).Scan(&at); err != nil {
		t.Fatalf("load suspected_cause_sent_at: %v", err)
	}
	return at
}

// Each channel paged for the alert gets exactly one follow-up, even with two
// replicas racing; a channel whose escalation tier has not paged yet gets
// none until it is paged, and a paging tool never does. The page's state
// machine (last_event_type, last_sent_at) is untouched.
func TestDispatchSuspectedCausesOncePerPagedChannel(t *testing.T) {
	ctx := context.Background()
	db, cleanup := testutil.SetupPostgresDB(ctx, t)
	defer cleanup()
	c := newCauseCase(ctx, t, db, followUpStubType)
	second := insertTypedChannel(ctx, t, db, c.tenantID, followUpStubType)
	paging := insertTypedChannel(ctx, t, db, c.tenantID, ackStubType)
	late := insertTypedChannel(ctx, t, db, c.tenantID, followUpStubType)
	mustExec(ctx, t, db, `UPDATE tenant_default_channels SET delay_seconds = 600 WHERE channel_id = $1`, late)
	if err := newIntegrationAlerter(db).dispatchOpenAlerts(ctx); err != nil {
		t.Fatal(err)
	}
	backdatePage(ctx, t, db, c.alertID, uuid.Nil, 5*time.Minute)
	beforeEvent, beforeSent, _ := notificationStateFor(ctx, t, db, c.alertID, c.channelID)

	rec := &causeRecorder{}
	replicas := []*Alerter{newIntegrationAlerter(db), newIntegrationAlerter(db)}
	for _, a := range replicas {
		a.sendFunc = rec.send
	}
	for round := 0; round < 3; round++ {
		runConcurrently(t, replicas, func(a *Alerter) error { return a.dispatchSuspectedCauses(ctx) })
	}
	if got := rec.count("suspected_cause", c.alertID); got != 2 {
		t.Fatalf("follow-ups = %d, want one per paged follow-up channel (2)", got)
	}
	for _, ch := range []uuid.UUID{c.channelID, second} {
		if suspectedCauseSentAt(ctx, t, db, c.alertID, ch) == nil {
			t.Fatalf("channel %s: suspected_cause_sent_at not recorded", ch)
		}
	}
	if suspectedCauseSentAt(ctx, t, db, c.alertID, paging) != nil {
		t.Fatal("a paging channel must never be claimed for a follow-up")
	}
	if _, _, ok := notificationStateFor(ctx, t, db, c.alertID, late); ok {
		t.Fatal("the late tier must not be paged yet")
	}
	afterEvent, afterSent, _ := notificationStateFor(ctx, t, db, c.alertID, c.channelID)
	if afterEvent != beforeEvent || !afterSent.Equal(beforeSent) {
		t.Fatalf("follow-up moved the page state: %s@%v -> %s@%v", beforeEvent, beforeSent, afterEvent, afterSent)
	}

	// The late tier pages; once its page settles it gets its own follow-up,
	// even though the triage is older than that page.
	mustExec(ctx, t, db, `UPDATE tenant_default_channels SET delay_seconds = 0 WHERE channel_id = $1`, late)
	if err := newIntegrationAlerter(db).dispatchOpenAlerts(ctx); err != nil {
		t.Fatal(err)
	}
	if err := replicas[0].dispatchSuspectedCauses(ctx); err != nil {
		t.Fatal(err)
	}
	if got := rec.count("suspected_cause", c.alertID); got != 2 {
		t.Fatalf("follow-up sent before the late page settled: %d", got)
	}
	backdatePage(ctx, t, db, c.alertID, late, 4*time.Minute)
	runConcurrently(t, replicas, func(a *Alerter) error { return a.dispatchSuspectedCauses(ctx) })
	if got := rec.count("suspected_cause", c.alertID); got != 3 {
		t.Fatalf("follow-ups after the late tier settled = %d, want 3", got)
	}
}

// Every gate that must keep a follow-up from going out. Each case is its own
// tenant in one database; sends are counted per alert.
func TestDispatchSuspectedCausesSkips(t *testing.T) {
	ctx := context.Background()
	db, cleanup := testutil.SetupPostgresDB(ctx, t)
	defer cleanup()

	cases := []struct {
		name        string
		channelType string
		mutate      func(t *testing.T, c causeCase)
		want        int
	}{
		{name: "baseline sends", want: 1},
		{name: "confirmed label still sends", want: 1, mutate: func(t *testing.T, c causeCase) { insertTriageLabel(ctx, t, db, c, "confirmed") }},
		{name: "flag off", mutate: func(t *testing.T, c causeCase) {
			mustExec(ctx, t, db, `UPDATE ai_settings SET triage_notifications_enabled = FALSE WHERE tenant_id = $1`, c.tenantID)
		}},
		{name: "auto-triage off", mutate: func(t *testing.T, c causeCase) {
			mustExec(ctx, t, db, `UPDATE ai_settings SET auto_triage_enabled = FALSE WHERE tenant_id = $1`, c.tenantID)
		}},
		{name: "workspace AI off", mutate: func(t *testing.T, c causeCase) {
			mustExec(ctx, t, db, `UPDATE ai_settings SET enabled = FALSE WHERE tenant_id = $1`, c.tenantID)
		}},
		{name: "triage completed before opt-in", mutate: func(t *testing.T, c causeCase) {
			mustExec(ctx, t, db, `UPDATE ai_settings SET triage_notifications_enabled_at = NOW() WHERE tenant_id = $1`, c.tenantID)
		}},
		{name: "abstention", mutate: func(t *testing.T, c causeCase) {
			mustExec(ctx, t, db, `UPDATE alert_triage SET hypothesis = $2 WHERE id = $1`, c.triageID, ai.RCAInsufficientEvidence)
		}},
		{name: "key unknown to the catalogue", mutate: func(t *testing.T, c causeCase) {
			mustExec(ctx, t, db, `UPDATE alert_triage SET hypothesis = 'retired_cause' WHERE id = $1`, c.triageID)
		}},
		{name: "low confidence", mutate: func(t *testing.T, c causeCase) {
			mustExec(ctx, t, db, `UPDATE alert_triage SET confidence = 'low' WHERE id = $1`, c.triageID)
		}},
		{name: "triage pending", mutate: func(t *testing.T, c causeCase) {
			mustExec(ctx, t, db, `UPDATE alert_triage SET status = 'pending', completed_at = NULL WHERE id = $1`, c.triageID)
		}},
		{name: "triage failed", mutate: func(t *testing.T, c causeCase) {
			mustExec(ctx, t, db, `UPDATE alert_triage SET status = 'failed' WHERE id = $1`, c.triageID)
		}},
		{name: "triage skipped", mutate: func(t *testing.T, c causeCase) {
			mustExec(ctx, t, db, `UPDATE alert_triage SET status = 'skipped', skip_reason = 'cooldown' WHERE id = $1`, c.triageID)
		}},
		{name: "operator corrected the cause", mutate: func(t *testing.T, c causeCase) { insertTriageLabel(ctx, t, db, c, "corrected") }},
		{name: "operator rejected the cause", mutate: func(t *testing.T, c causeCase) { insertTriageLabel(ctx, t, db, c, "rejected") }},
		{name: "alert resolved", mutate: func(t *testing.T, c causeCase) {
			mustExec(ctx, t, db, `UPDATE alerts SET status = 'resolved', resolved_at = NOW() WHERE id = $1`, c.alertID)
		}},
		{name: "resolve already sent to the channel", mutate: func(t *testing.T, c causeCase) {
			mustExec(ctx, t, db, `UPDATE alert_notification_states SET last_event_type = 'resolved' WHERE alert_id = $1`, c.alertID)
		}},
		{name: "channel inactive", mutate: func(t *testing.T, c causeCase) {
			mustExec(ctx, t, db, `UPDATE alert_channels SET is_active = FALSE WHERE id = $1`, c.channelID)
		}},
		{name: "channel no longer routed", mutate: func(t *testing.T, c causeCase) {
			mustExec(ctx, t, db, `DELETE FROM tenant_default_channels WHERE channel_id = $1`, c.channelID)
		}},
		{name: "paging plugin without the follow-up capability", channelType: ackStubType},
		{name: "page has not settled", mutate: func(t *testing.T, c causeCase) { backdatePage(ctx, t, db, c.alertID, uuid.Nil, time.Minute) }},
		{name: "triage and page both stale", mutate: func(t *testing.T, c causeCase) {
			mustExec(ctx, t, db, `UPDATE ai_settings SET triage_notifications_enabled_at = NOW() - INTERVAL '3 hours' WHERE tenant_id = $1`, c.tenantID)
			mustExec(ctx, t, db, `UPDATE alert_triage SET completed_at = NOW() - INTERVAL '90 minutes' WHERE id = $1`, c.triageID)
			backdatePage(ctx, t, db, c.alertID, uuid.Nil, 2*time.Hour)
		}},
		{name: "monitor in maintenance", mutate: func(t *testing.T, c causeCase) {
			insertMaintenanceWindow(ctx, t, db, c.tenantID, time.Now().Add(-time.Minute), time.Now().Add(time.Hour), c.monitorID)
		}},
		{name: "dependency-suppressed after the page", mutate: func(t *testing.T, c causeCase) {
			upstream := testutil.InsertHTTPMonitor(ctx, t, db, c.tenantID, "Upstream")
			mustExec(ctx, t, db, `UPDATE tenants SET dependency_suppression_enabled = TRUE WHERE id = $1`, c.tenantID)
			mustExec(ctx, t, db, `UPDATE alerts SET root_cause_monitor_id = $2 WHERE id = $1`, c.alertID, upstream)
		}},
		{name: "member of a roll-up group", mutate: func(t *testing.T, c causeCase) {
			group := testutil.InsertGroupMonitor(ctx, t, db, c.tenantID, "Group")
			testutil.AddMonitorToGroup(ctx, t, db, c.monitorID, group)
			mustExec(ctx, t, db, `UPDATE monitors SET member_alert_rollup = 'group' WHERE id = $1`, group)
		}},
		{name: "triage row from another tenant", mutate: func(t *testing.T, c causeCase) {
			other := testutil.InsertTenant(ctx, t, db, "")
			mustExec(ctx, t, db, `UPDATE alert_triage SET tenant_id = $2 WHERE id = $1`, c.triageID, other)
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			channelType := tc.channelType
			if channelType == "" {
				channelType = followUpStubType
			}
			c := newCauseCase(ctx, t, db, channelType)
			if tc.mutate != nil {
				tc.mutate(t, c)
			}
			rec := &causeRecorder{}
			a := newIntegrationAlerter(db)
			a.sendFunc = rec.send
			if err := a.dispatchSuspectedCauses(ctx); err != nil {
				t.Fatalf("dispatchSuspectedCauses: %v", err)
			}
			if got := rec.count("suspected_cause", c.alertID); got != tc.want {
				t.Fatalf("follow-ups = %d, want %d", got, tc.want)
			}
		})
	}

	// The installation kill switch (LOCAL_AI_DISABLED / AI_TRIAGE_ENABLED=false)
	// stops follow-ups from triages that are already ready.
	t.Run("installation kill switch", func(t *testing.T) {
		c := newCauseCase(ctx, t, db, followUpStubType)
		rec := &causeRecorder{}
		a := newIntegrationAlerter(db)
		a.config.SuspectedCausesDisabled = true
		a.sendFunc = rec.send
		if err := a.runSuspectedCauses(ctx); err != nil {
			t.Fatal(err)
		}
		if got := rec.count("suspected_cause", c.alertID); got != 0 {
			t.Fatalf("follow-ups with the kill switch on = %d, want 0", got)
		}
		a.config.SuspectedCausesDisabled = false
		if err := a.runSuspectedCauses(ctx); err != nil {
			t.Fatal(err)
		}
		if got := rec.count("suspected_cause", c.alertID); got != 1 {
			t.Fatalf("follow-ups with the kill switch off = %d, want 1", got)
		}
	})
}

func insertTriageLabel(ctx context.Context, t *testing.T, db *shareddb.Client, c causeCase, verdict string) {
	t.Helper()
	mustExec(ctx, t, db, `
		INSERT INTO ai_labels (tenant_id, subject_type, subject_key, subject_id, verdict, answers, request, catalogue_version)
		VALUES ($1, 'alert_triage', $2, $3, $4, '{}'::jsonb, '{}'::jsonb, $5)
	`, c.tenantID, c.triageID.String(), c.triageID, verdict, ai.CatalogueVersion)
}

// An acknowledged alert still gets the hint (it helps whoever is working
// it), and the event carries the catalogue's wording for the triage's key.
func TestSuspectedCauseOnAcknowledgedAlert(t *testing.T) {
	ctx := context.Background()
	db, cleanup := testutil.SetupPostgresDB(ctx, t)
	defer cleanup()
	c := newCauseCase(ctx, t, db, followUpStubType)
	mustExec(ctx, t, db, `UPDATE alerts SET status = 'acknowledged', acknowledged_at = NOW() WHERE id = $1`, c.alertID)

	rec := &causeRecorder{}
	a := newIntegrationAlerter(db)
	a.sendFunc = rec.send
	if err := a.dispatchSuspectedCauses(ctx); err != nil {
		t.Fatal(err)
	}
	got, ok := rec.last("suspected_cause")
	if !ok {
		t.Fatal("no follow-up for an acknowledged alert")
	}
	title, actions, _ := ai.RCAChoice("dns_failure")
	sc := got.record.SuspectedCause
	if got.record.Status != "acknowledged" || sc == nil || sc.Key != "dns_failure" || sc.Title != title ||
		sc.Confidence != "high" || len(sc.Actions) != len(actions) || sc.TriageID != c.triageID.String() {
		t.Fatalf("record = status %q cause %+v", got.record.Status, sc)
	}
	event := buildAlertEvent("suspected_cause", policyBinding{MonitorID: c.monitorID, TenantID: c.tenantID}, &got.record, nil, time.Now())
	if event.Type != "suspected_cause" || event.Alert.Status != "acknowledged" || event.Alert.SuspectedCause == nil {
		t.Fatalf("event = %+v", event)
	}
}

// deliverNotification's FOR SHARE status check refuses a follow-up for an
// alert resolved after the dispatcher loaded it, and a resolve issued while a
// follow-up is being sent waits for that send. The resolve always runs on a
// separate connection, never inside sendFunc, which runs under the sender's
// own share lock.
func TestSuspectedCauseResolveRace(t *testing.T) {
	ctx := context.Background()
	db, cleanup := testutil.SetupPostgresDB(ctx, t)
	defer cleanup()

	// Loaded open, resolved before the send: refused, nothing claimed.
	c := newCauseCase(ctx, t, db, followUpStubType)
	sends := 0
	a := newIntegrationAlerter(db)
	a.sendFunc = func(context.Context, alertChannel, string, policyBinding, *alertRecord, *groupDetail, time.Time) error {
		sends++
		return nil
	}
	mustExec(ctx, t, db, `UPDATE alerts SET status = 'resolved', resolved_at = NOW() WHERE id = $1`, c.alertID)
	record := alertRecord{ID: c.alertID, TenantID: c.tenantID, MonitorID: c.monitorID, Status: "active"}
	channel := alertChannel{ID: c.channelID, Type: followUpStubType, IsActive: true}
	sent, err := a.deliverNotification(ctx, "suspected_cause", policyBinding{MonitorID: c.monitorID, TenantID: c.tenantID},
		&record, nil, channel, time.Now(), 0)
	if err != nil || sent || sends != 0 {
		t.Fatalf("resolved alert: sent=%v err=%v sends=%d", sent, err, sends)
	}
	if suspectedCauseSentAt(ctx, t, db, c.alertID, c.channelID) != nil {
		t.Fatal("a refused follow-up must not keep its claim")
	}

	// A resolve issued while the follow-up is in flight waits for it.
	c2 := newCauseCase(ctx, t, db, followUpStubType)
	inSend, release := make(chan struct{}), make(chan struct{})
	var inFlight atomic.Int32
	a.sendFunc = func(context.Context, alertChannel, string, policyBinding, *alertRecord, *groupDetail, time.Time) error {
		inFlight.Add(1)
		close(inSend)
		<-release
		return nil
	}
	done := make(chan error, 1)
	go func() { done <- a.dispatchSuspectedCauses(ctx) }()
	select {
	case <-inSend:
	case <-time.After(10 * time.Second):
		t.Fatal("follow-up never reached sendFunc")
	}
	resolveCtx, cancel := context.WithTimeout(ctx, 300*time.Millisecond)
	_, err = db.ExecContext(resolveCtx, `UPDATE alerts SET status = 'resolved', resolved_at = NOW() WHERE id = $1`, c2.alertID)
	cancel()
	if err == nil {
		t.Fatal("resolution committed while a follow-up send held the alert")
	}
	close(release)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	mustExec(ctx, t, db, `UPDATE alerts SET status = 'resolved', resolved_at = NOW() WHERE id = $1`, c2.alertID)
	if inFlight.Load() != 1 || suspectedCauseSentAt(ctx, t, db, c2.alertID, c2.channelID) == nil {
		t.Fatalf("in-flight follow-up: sends=%d", inFlight.Load())
	}
}

// The claim is one-shot, needs an existing page row, and refuses a resolved
// row.
func TestClaimSuspectedCause(t *testing.T) {
	ctx := context.Background()
	db, cleanup := testutil.SetupPostgresDB(ctx, t)
	defer cleanup()
	c := newCauseCase(ctx, t, db, followUpStubType)
	claim := func(channelID uuid.UUID) bool {
		t.Helper()
		tx, err := db.BeginTx(ctx, nil)
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = tx.Rollback() }()
		ok, _, err := claimNotificationTx(ctx, tx, c.alertID, channelID, "suspected_cause", time.Now(), 0)
		if err != nil {
			t.Fatal(err)
		}
		if err := tx.Commit(); err != nil {
			t.Fatal(err)
		}
		return ok
	}
	if claim(uuid.New()) {
		t.Fatal("claimed a channel that was never paged")
	}
	if !claim(c.channelID) || claim(c.channelID) {
		t.Fatal("claim must succeed exactly once")
	}
	c2 := newCauseCase(ctx, t, db, followUpStubType)
	mustExec(ctx, t, db, `UPDATE alert_notification_states SET last_event_type = 'resolved' WHERE alert_id = $1`, c2.alertID)
	c.alertID = c2.alertID
	if claim(c2.channelID) {
		t.Fatal("claimed a resolved row")
	}
}

// A follow-up never moves the reminder clock.
func TestRemindersUnaffectedBySuspectedCause(t *testing.T) {
	ctx := context.Background()
	db, cleanup := testutil.SetupPostgresDB(ctx, t)
	defer cleanup()
	c := newCauseCase(ctx, t, db, followUpStubType)
	mustExec(ctx, t, db, `UPDATE tenants SET alert_reminder_seconds = 600 WHERE id = $1`, c.tenantID)
	backdatePage(ctx, t, db, c.alertID, uuid.Nil, 11*time.Minute)
	_, pagedAt, _ := notificationStateFor(ctx, t, db, c.alertID, c.channelID)

	rec := &causeRecorder{}
	a := newIntegrationAlerter(db)
	a.sendFunc = rec.send
	if err := a.dispatchSuspectedCauses(ctx); err != nil {
		t.Fatal(err)
	}
	if rec.count("suspected_cause", c.alertID) != 1 {
		t.Fatal("follow-up not sent")
	}
	if event, sentAt, _ := notificationStateFor(ctx, t, db, c.alertID, c.channelID); event != "created" || !sentAt.Equal(pagedAt) {
		t.Fatalf("follow-up moved the page state to %s@%v (paged %v)", event, sentAt, pagedAt)
	}
	if err := a.dispatchOpenAlerts(ctx); err != nil {
		t.Fatal(err)
	}
	if rec.count("reminder", c.alertID) != 1 {
		t.Fatal("the reminder due on the page's schedule did not fire")
	}
}

// In synchronous dispatch a hanging provider inside the follow-up phase must
// not starve the deferred recovery-notification sweep in the same tick: the
// phase stops early enough to leave the sweep its budget (or half the tick
// when the tick is shorter than that budget).
func TestSuspectedCausesLeaveRecoveryBudget(t *testing.T) {
	ctx := context.Background()
	db, cleanup := testutil.SetupPostgresDB(ctx, t)
	defer cleanup()
	c := newCauseCase(ctx, t, db, followUpStubType)

	// A resolved alert whose recovery notification is still owed.
	recoveredMonitor := testutil.InsertHTTPMonitor(ctx, t, db, c.tenantID, "Recovered")
	resolvedAlert := uuid.New()
	mustExec(ctx, t, db, `
		INSERT INTO alerts (id, tenant_id, monitor_id, kind, status, triggered_at, resolved_at, failure_count, created_at, updated_at)
		VALUES ($1, $2, $3, 'availability', 'resolved', NOW() - INTERVAL '10 minutes', NOW() - INTERVAL '1 minute', 3, NOW(), NOW())
	`, resolvedAlert, c.tenantID, recoveredMonitor)
	mustExec(ctx, t, db, `
		INSERT INTO alert_notification_states (alert_id, channel_id, last_sent_at, last_event_type, created_at, updated_at)
		VALUES ($1, $2, NOW() - INTERVAL '10 minutes', 'created', NOW() - INTERVAL '10 minutes', NOW())
	`, resolvedAlert, c.channelID)

	var followUps, recoveries atomic.Int32
	a := newIntegrationAlerter(db)
	a.recoveryBudget = 5 * time.Second
	a.suspectedCauseBudget = time.Minute // larger than the tick: only the reserve bounds it
	a.sendFunc = func(ctx context.Context, _ alertChannel, eventType string, _ policyBinding, _ *alertRecord, _ *groupDetail, _ time.Time) error {
		switch eventType {
		case "suspected_cause":
			followUps.Add(1)
			<-ctx.Done() // a provider that never answers
			return ctx.Err()
		case "resolved":
			recoveries.Add(1)
		}
		return nil
	}

	// A tick shorter than the sweep's budget: the phase stops at half of it.
	short, cancelShort := context.WithTimeout(ctx, 2*time.Second)
	start := time.Now()
	if err := a.runSuspectedCauses(short); err != nil {
		t.Fatal(err)
	}
	cancelShort()
	if elapsed := time.Since(start); elapsed > 1500*time.Millisecond || followUps.Load() != 1 {
		t.Fatalf("short tick: follow-up phase ran %v (attempts %d), want it cut at about half", elapsed, followUps.Load())
	}
	followUps.Store(0)

	// A recovery budget of 2s inside a 4s tick: the phase gets what is left.
	a.recoveryBudget = 2 * time.Second
	tick, cancel := context.WithTimeout(ctx, 4*time.Second)
	defer cancel()
	start = time.Now()
	if err := a.runLifecycle(tick); err != nil && !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("runLifecycle: %v", err)
	}
	if followUps.Load() != 1 {
		t.Fatalf("follow-up attempts = %d, want the hanging one (1)", followUps.Load())
	}
	if recoveries.Load() != 1 {
		t.Fatalf("recovery notifications = %d, want 1: the follow-up phase starved the sweep", recoveries.Load())
	}
	if elapsed := time.Since(start); elapsed > 5*time.Second {
		t.Fatalf("tick took %v", elapsed)
	}
	if suspectedCauseSentAt(ctx, t, db, c.alertID, c.channelID) != nil {
		t.Fatal("a send cut off by the budget must release its claim for the next tick")
	}
}

// Unrouted (alert, channel) pairs never claim, so they must be dropped in SQL
// before the batch LIMIT: a full batch of them (here a channel set removed
// from routing on an alert whose triage is older) must not hide a routed
// pair of another workspace.
func TestSuspectedCausesUnroutedPairsDoNotFillTheBatch(t *testing.T) {
	ctx := context.Background()
	db, cleanup := testutil.SetupPostgresDB(ctx, t)
	defer cleanup()
	stale := newCauseCase(ctx, t, db, followUpStubType)
	for i := 0; i < suspectedCauseBatch; i++ {
		insertTypedChannel(ctx, t, db, stale.tenantID, followUpStubType)
	}
	if err := newIntegrationAlerter(db).dispatchOpenAlerts(ctx); err != nil {
		t.Fatal(err)
	}
	backdatePage(ctx, t, db, stale.alertID, uuid.Nil, 5*time.Minute)
	mustExec(ctx, t, db, `DELETE FROM tenant_default_channels WHERE tenant_id = $1`, stale.tenantID)

	fresh := newCauseCase(ctx, t, db, followUpStubType)
	mustExec(ctx, t, db, `UPDATE alert_triage SET completed_at = NOW() - INTERVAL '10 seconds' WHERE id = $1`, fresh.triageID)

	rec := &causeRecorder{}
	a := newIntegrationAlerter(db)
	a.sendFunc = rec.send
	if err := a.dispatchSuspectedCauses(ctx); err != nil {
		t.Fatal(err)
	}
	if got := rec.count("suspected_cause", stale.alertID); got != 0 {
		t.Fatalf("unrouted follow-ups = %d, want 0", got)
	}
	if got := rec.count("suspected_cause", fresh.alertID); got != 1 {
		t.Fatalf("routed follow-up behind a batch of unrouted pairs = %d, want 1", got)
	}
}

// RoutesToChannelPredicate is TargetsSQL correlated per row: for every
// (monitor, channel) pair, across custom and default routing and two
// workspaces, it must agree with TargetsSQL's channel set.
func TestRoutesToChannelPredicateMatchesTargetsSQL(t *testing.T) {
	ctx := context.Background()
	db, cleanup := testutil.SetupPostgresDB(ctx, t)
	defer cleanup()

	var monitors []uuid.UUID
	tenants := map[uuid.UUID]uuid.UUID{} // monitor -> tenant
	for i := 0; i < 2; i++ {
		tenant := testutil.InsertTenant(ctx, t, db, "")
		d1 := insertTypedChannel(ctx, t, db, tenant, followUpStubType)
		insertTypedChannel(ctx, t, db, tenant, followUpStubType)
		def := testutil.InsertHTTPMonitor(ctx, t, db, tenant, "default")
		custom := testutil.InsertHTTPMonitor(ctx, t, db, tenant, "custom")
		emptyCustom := testutil.InsertHTTPMonitor(ctx, t, db, tenant, "custom-empty")
		mustExec(ctx, t, db, `UPDATE monitors SET notification_mode = 'custom' WHERE id = ANY($1)`, pq.Array([]uuid.UUID{custom, emptyCustom}))
		mustExec(ctx, t, db, `INSERT INTO monitor_channels (monitor_id, channel_id) VALUES ($1, $2)`, custom, d1)
		// A custom assignment on a default-mode monitor is ignored.
		mustExec(ctx, t, db, `INSERT INTO monitor_channels (monitor_id, channel_id) VALUES ($1, $2)`, def, d1)
		for _, m := range []uuid.UUID{def, custom, emptyCustom} {
			monitors = append(monitors, m)
			tenants[m] = tenant
		}
	}

	pairs := 0
	for _, m := range monitors {
		want := map[uuid.UUID]bool{}
		rows, err := db.QueryContext(ctx, `SELECT channel_id FROM (`+alertrouting.TargetsSQL()+`) x`, tenants[m], pq.Array([]uuid.UUID{m}))
		if err != nil {
			t.Fatal(err)
		}
		for rows.Next() {
			var id uuid.UUID
			if err := rows.Scan(&id); err != nil {
				t.Fatal(err)
			}
			want[id] = true
		}
		rows.Close()

		rows, err = db.QueryContext(ctx, `
			SELECT ac.id, `+alertrouting.RoutesToChannelPredicate("m", "ac.id")+`
			FROM monitors m CROSS JOIN alert_channels ac WHERE m.id = $1`, m)
		if err != nil {
			t.Fatal(err)
		}
		for rows.Next() {
			var id uuid.UUID
			var routed bool
			if err := rows.Scan(&id, &routed); err != nil {
				t.Fatal(err)
			}
			if routed != want[id] {
				t.Errorf("monitor %s channel %s: predicate %v, TargetsSQL %v", m, id, routed, want[id])
			}
			pairs++
		}
		rows.Close()
	}
	if pairs != len(monitors)*4 {
		t.Fatalf("compared %d pairs, want %d", pairs, len(monitors)*4)
	}
}

// A provider that hangs until the phase's budget runs out is not retried
// first on every tick: the batch rotates past it, so other follow-ups still
// go out, and the hanging pair gets its turn again once the rest are done.
func TestSuspectedCausesRotatePastAHangingProvider(t *testing.T) {
	ctx := context.Background()
	db, cleanup := testutil.SetupPostgresDB(ctx, t)
	defer cleanup()
	first := newCauseCase(ctx, t, db, followUpStubType)
	second := newCauseCase(ctx, t, db, followUpStubType)

	var mu sync.Mutex
	var hanging uuid.UUID
	attempts := map[uuid.UUID]int{}
	delivered := map[uuid.UUID]int{}
	a := newIntegrationAlerter(db)
	a.suspectedCauseBudget = 500 * time.Millisecond
	a.sendFunc = func(ctx context.Context, _ alertChannel, eventType string, _ policyBinding, alert *alertRecord, _ *groupDetail, _ time.Time) error {
		mu.Lock()
		if hanging == uuid.Nil {
			hanging = alert.ID // whichever pair the batch tries first
		}
		attempts[alert.ID]++
		hang := alert.ID == hanging
		if !hang {
			delivered[alert.ID]++
		}
		mu.Unlock()
		if hang {
			<-ctx.Done()
			return ctx.Err()
		}
		return nil
	}
	for tick := 0; tick < 3; tick++ {
		if err := a.runSuspectedCauses(ctx); err != nil {
			t.Fatal(err)
		}
	}
	other := first.alertID
	if hanging == first.alertID {
		other = second.alertID
	}
	mu.Lock()
	defer mu.Unlock()
	if delivered[other] != 1 {
		t.Fatalf("follow-up behind a hanging provider delivered %d times, want 1 (attempts %v)", delivered[other], attempts)
	}
	if attempts[hanging] < 2 {
		t.Fatalf("hanging pair attempted %d times, want it retried after the rotation", attempts[hanging])
	}
}
