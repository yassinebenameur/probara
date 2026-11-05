//go:build !unit

package alerter

import (
	"context"
	"reflect"
	"sync"
	"testing"

	"github.com/google/uuid"
	"github.com/lib/pq"

	"github.com/yassinebenameur/probara/shared/alertrouting"
	shareddb "github.com/yassinebenameur/probara/shared/db"
	"github.com/yassinebenameur/probara/shared/notifications"
	"github.com/yassinebenameur/probara/shared/notifications/deliverylog"
	"github.com/yassinebenameur/probara/shared/notifications/plugin/builtin/email"
	"github.com/yassinebenameur/probara/shared/testutil"
)

type policyTarget struct {
	kind string // user | schedule | channel | all_admins
	id   uuid.UUID
}

type policyStep struct {
	delay   int
	targets []policyTarget
}

// insertEscalationPolicy stores a policy with steps in order.
func insertEscalationPolicy(ctx context.Context, t *testing.T, db *shareddb.Client, tenantID uuid.UUID, repeatCount, repeatAfter int, steps ...policyStep) uuid.UUID {
	t.Helper()
	id := uuid.New()
	mustExec(ctx, t, db, `INSERT INTO escalation_policies (id, tenant_id, name, repeat_count, repeat_after_seconds)
		VALUES ($1, $2, $3, $4, $5)`, id, tenantID, "policy-"+id.String()[:8], repeatCount, repeatAfter)
	for pos, st := range steps {
		stepID := uuid.New()
		mustExec(ctx, t, db, `INSERT INTO escalation_policy_steps (id, policy_id, position, delay_seconds) VALUES ($1, $2, $3, $4)`,
			stepID, id, pos, st.delay)
		for i, tg := range st.targets {
			var user, schedule, channel *uuid.UUID
			tid := tg.id
			switch tg.kind {
			case "user":
				user = &tid
			case "schedule":
				schedule = &tid
			case "channel":
				channel = &tid
			}
			mustExec(ctx, t, db, `INSERT INTO escalation_policy_targets (step_id, position, kind, user_id, schedule_id, channel_id)
				VALUES ($1, $2, $3, $4, $5, $6)`, stepID, i, tg.kind, user, schedule, channel)
		}
	}
	return id
}

// insertPerson creates a workspace member with one verified email contact
// (stored in the clear: the integration alerter has no encryptor).
func insertPerson(ctx context.Context, t *testing.T, db *shareddb.Client, tenantID uuid.UUID, name, role string) (uuid.UUID, uuid.UUID) {
	t.Helper()
	userID, contactID := uuid.New(), uuid.New()
	mustExec(ctx, t, db, `INSERT INTO admin_users (id, username, password_hash) VALUES ($1, $2, 'x')`, userID, name+"-"+userID.String()[:6])
	mustExec(ctx, t, db, `INSERT INTO tenant_memberships (admin_user_id, tenant_id, role) VALUES ($1, $2, $3)`, userID, tenantID, role)
	mustExec(ctx, t, db, `INSERT INTO user_contacts (id, user_id, kind, address, verified_at) VALUES ($1, $2, 'email', $3, NOW())`,
		contactID, userID, name+"@example.com")
	return userID, contactID
}

// insertScheduleWith creates an on-call schedule whose one daily rotation
// has the given members (none: nobody is ever on call).
func insertScheduleWith(ctx context.Context, t *testing.T, db *shareddb.Client, tenantID uuid.UUID, members ...uuid.UUID) uuid.UUID {
	t.Helper()
	id := uuid.New()
	mustExec(ctx, t, db, `INSERT INTO oncall_schedules (id, tenant_id, name) VALUES ($1, $2, $3)`, id, tenantID, "sched-"+id.String()[:8])
	if len(members) == 0 {
		return id
	}
	rotation := uuid.New()
	mustExec(ctx, t, db, `INSERT INTO oncall_rotations (id, schedule_id, position, kind, handoff_minute, start_at)
		VALUES ($1, $2, 0, 'daily', 540, NOW() - INTERVAL '1 hour')`, rotation, id)
	for i, m := range members {
		mustExec(ctx, t, db, `INSERT INTO oncall_rotation_members (rotation_id, position, user_id) VALUES ($1, $2, $3)`, rotation, i, m)
	}
	return id
}

// pageRecorder is the platform mailer: every person send by email lands here.
type pageRecorder struct {
	mu    sync.Mutex
	sends []recordedPage
}

type recordedPage struct {
	to      string
	event   string
	relapse int
}

func (r *pageRecorder) SendAlert(_ context.Context, event notifications.AlertEvent, _ email.Templates, recipients []string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, to := range recipients {
		r.sends = append(r.sends, recordedPage{to: to, event: event.Type, relapse: event.Alert.RelapseCount})
	}
	return nil
}

func (r *pageRecorder) count(to, event string) int {
	r.mu.Lock()
	defer r.mu.Unlock()
	n := 0
	for _, s := range r.sends {
		if s.to == to && s.event == event {
			n++
		}
	}
	return n
}

func installPageRecorder(t *testing.T) *pageRecorder {
	t.Helper()
	rec := &pageRecorder{}
	email.SetMailer(rec)
	t.Cleanup(func() { email.SetMailer(nil) })
	return rec
}

func runEscalations(ctx context.Context, t *testing.T, a *Alerter) {
	t.Helper()
	if err := a.dispatchEscalations(ctx); err != nil {
		t.Fatalf("dispatchEscalations: %v", err)
	}
	if err := a.dispatchEscalationNotices(ctx); err != nil {
		t.Fatalf("dispatchEscalationNotices: %v", err)
	}
}

// A policy walks its steps on the channel-tier clock: a user now, whoever
// the schedule has on call after 10 minutes, a channel (an ordinary
// TargetsSQL route) after 20. Acknowledging stops the walk and tells the
// people paged (not the acknowledger); resolving tells all of them, once.
// Every person send is in the delivery history.
func TestEscalationPolicyWalksStepsAndNotifiesThePeoplePaged(t *testing.T) {
	ctx := context.Background()
	db, cleanup := testutil.SetupPostgresDB(ctx, t)
	defer cleanup()
	mail := installPageRecorder(t)

	tenantID := testutil.InsertTenant(ctx, t, db, "escalation-walk")
	monitorID := testutil.InsertHTTPMonitor(ctx, t, db, tenantID, "API")
	alice, _ := insertPerson(ctx, t, db, tenantID, "alice", "editor")
	bob, _ := insertPerson(ctx, t, db, tenantID, "bob", "viewer")
	schedule := insertScheduleWith(ctx, t, db, tenantID, bob)
	channel := insertTestChannel(ctx, t, db, tenantID, "ops-room")
	policy := insertEscalationPolicy(ctx, t, db, tenantID, 0, 1800,
		policyStep{delay: 0, targets: []policyTarget{{kind: "user", id: alice}}},
		policyStep{delay: 600, targets: []policyTarget{{kind: "schedule", id: schedule}}},
		policyStep{delay: 1200, targets: []policyTarget{{kind: "channel", id: channel}}},
	)
	alertID := openDownAlert(ctx, t, db, tenantID, monitorID)
	mustExec(ctx, t, db, `UPDATE monitors SET notification_mode = 'policy', escalation_policy_id = $2 WHERE id = $1`, monitorID, policy)

	rec := newChannelRecorder()
	a := newIntegrationAlerter(db)
	a.sendFunc = rec.send

	runEscalations(ctx, t, a)
	if mail.count("alice@example.com", "created") != 1 || mail.count("bob@example.com", "created") != 0 {
		t.Fatalf("step 1: alice=%d bob=%d pages", mail.count("alice@example.com", "created"), mail.count("bob@example.com", "created"))
	}

	// Eleven minutes in: the schedule step pages whoever is on call (bob).
	mustExec(ctx, t, db, `UPDATE alerts SET triggered_at = NOW() - INTERVAL '11 minutes' WHERE id = $1`, alertID)
	runEscalations(ctx, t, a)
	runEscalations(ctx, t, a) // a second tick claims nothing new
	if mail.count("bob@example.com", "created") != 1 || mail.count("alice@example.com", "created") != 1 {
		t.Fatalf("step 2: alice=%d bob=%d pages", mail.count("alice@example.com", "created"), mail.count("bob@example.com", "created"))
	}

	// The channel target is a plain route: dispatchOpenAlerts pages it at 20 min.
	if err := a.dispatchOpenAlerts(ctx); err != nil {
		t.Fatal(err)
	}
	if got := rec.count(channel, "created"); got != 0 {
		t.Fatalf("channel step fired early (%d)", got)
	}
	mustExec(ctx, t, db, `UPDATE alerts SET triggered_at = NOW() - INTERVAL '21 minutes' WHERE id = $1`, alertID)
	if err := a.dispatchOpenAlerts(ctx); err != nil {
		t.Fatal(err)
	}
	if got := rec.count(channel, "created"); got != 1 {
		t.Fatalf("channel step pages = %d, want 1", got)
	}

	// Person sends are in the delivery history, with the person and a masked address.
	var rows int
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM alert_notification_deliveries
		WHERE alert_id = $1 AND channel_id IS NULL AND user_id = $2 AND recipient = 'a***@example.com'
		  AND delivery_key = 'escalation:0:0' AND outcome = 'delivered'`, alertID, alice).Scan(&rows); err != nil {
		t.Fatal(err)
	}
	if rows != 1 {
		t.Fatalf("delivery history rows for alice's page = %d, want 1", rows)
	}

	// Alice acknowledges: bob hears it, alice does not.
	mustExec(ctx, t, db, `UPDATE alerts SET status = 'acknowledged', acknowledged_at = NOW(), acknowledged_by_user_id = $2 WHERE id = $1`, alertID, alice)
	runEscalations(ctx, t, a)
	runEscalations(ctx, t, a)
	if mail.count("bob@example.com", "acknowledged") != 1 || mail.count("alice@example.com", "acknowledged") != 0 {
		t.Fatalf("ack notices: alice=%d bob=%d", mail.count("alice@example.com", "acknowledged"), mail.count("bob@example.com", "acknowledged"))
	}

	// Resolved: everyone paged hears it, once.
	mustExec(ctx, t, db, `UPDATE alerts SET status = 'resolved', resolved_at = NOW() WHERE id = $1`, alertID)
	runEscalations(ctx, t, a)
	runEscalations(ctx, t, a)
	if mail.count("bob@example.com", "resolved") != 1 || mail.count("alice@example.com", "resolved") != 1 {
		t.Fatalf("resolve notices: alice=%d bob=%d", mail.count("alice@example.com", "resolved"), mail.count("bob@example.com", "resolved"))
	}
}

// Ack and snooze stop the walk; an unacknowledge or the end of a snooze
// resumes it on the A1 clock (unfired steps count from the resume point, so
// nothing bursts); repeat cycles walk the person steps again.
func TestEscalationAckSnoozeResumeAndRepeat(t *testing.T) {
	ctx := context.Background()
	db, cleanup := testutil.SetupPostgresDB(ctx, t)
	defer cleanup()
	mail := installPageRecorder(t)

	tenantID := testutil.InsertTenant(ctx, t, db, "escalation-mutes")
	monitorID := testutil.InsertHTTPMonitor(ctx, t, db, tenantID, "API")
	alice, _ := insertPerson(ctx, t, db, tenantID, "alice", "editor")
	// Period = last delay (300) + repeat_after (600) = 15 minutes.
	policy := insertEscalationPolicy(ctx, t, db, tenantID, 1, 600,
		policyStep{delay: 0, targets: []policyTarget{{kind: "user", id: alice}}},
		policyStep{delay: 300, targets: []policyTarget{{kind: "all_admins"}}},
	)
	insertPerson(ctx, t, db, tenantID, "admin", "admin")
	alertID := openDownAlert(ctx, t, db, tenantID, monitorID)
	mustExec(ctx, t, db, `UPDATE monitors SET notification_mode = 'policy', escalation_policy_id = $2 WHERE id = $1`, monitorID, policy)
	a := newIntegrationAlerter(db)
	pages := func(to string) int { return mail.count(to, "created") }

	runEscalations(ctx, t, a)
	if pages("alice@example.com") != 1 || pages("admin@example.com") != 0 {
		t.Fatalf("first step: alice=%d admin=%d", pages("alice@example.com"), pages("admin@example.com"))
	}

	// Acknowledged with every clock overdue: nothing pages.
	mustExec(ctx, t, db, `UPDATE alerts SET status = 'acknowledged', acknowledged_at = NOW(),
		triggered_at = NOW() - INTERVAL '10 minutes' WHERE id = $1`, alertID)
	runEscalations(ctx, t, a)
	if pages("admin@example.com") != 0 {
		t.Fatal("an acknowledged alert paged its next step")
	}

	// Unacknowledged just now: the next step counts from the unack.
	mustExec(ctx, t, db, `UPDATE alerts SET status = 'active', acknowledged_at = NULL, unacknowledged_at = NOW() WHERE id = $1`, alertID)
	runEscalations(ctx, t, a)
	if pages("admin@example.com") != 0 {
		t.Fatal("an unack released the overdue step at once")
	}
	mustExec(ctx, t, db, `UPDATE alerts SET unacknowledged_at = NOW() - INTERVAL '6 minutes' WHERE id = $1`, alertID)
	runEscalations(ctx, t, a)
	if pages("admin@example.com") != 1 {
		t.Fatalf("step 2 five minutes after the unack: admin=%d, want 1", pages("admin@example.com"))
	}

	// Snoozed: nothing, however late it is.
	mustExec(ctx, t, db, `UPDATE alerts SET snoozed_until = NOW() + INTERVAL '1 hour', unacknowledged_at = NOW() - INTERVAL '2 hours' WHERE id = $1`, alertID)
	runEscalations(ctx, t, a)
	if pages("alice@example.com") != 1 {
		t.Fatal("a snoozed alert paged")
	}

	// The snooze ended 16 minutes ago: the repeat cycle's first step (15
	// minutes) is due, its second (20) not yet.
	mustExec(ctx, t, db, `UPDATE alerts SET snoozed_until = NOW() - INTERVAL '16 minutes', triggered_at = NOW() - INTERVAL '3 hours' WHERE id = $1`, alertID)
	runEscalations(ctx, t, a)
	if pages("alice@example.com") != 2 || pages("admin@example.com") != 1 {
		t.Fatalf("repeat cycle step 1: alice=%d (want 2) admin=%d (want 1)", pages("alice@example.com"), pages("admin@example.com"))
	}
	mustExec(ctx, t, db, `UPDATE alerts SET snoozed_until = NOW() - INTERVAL '3 hours' WHERE id = $1`, alertID)
	runEscalations(ctx, t, a)
	runEscalations(ctx, t, a)
	if pages("alice@example.com") != 2 || pages("admin@example.com") != 2 {
		t.Fatalf("after the last repeat: alice=%d admin=%d, want 2 and 2", pages("alice@example.com"), pages("admin@example.com"))
	}
}

// A schedule with nobody on call claims its step and pages nobody; the walk
// moves on. A reopened alert (a relapse) walks its steps again under fresh
// ledger keys.
func TestEscalationEmptyScheduleAndRelapse(t *testing.T) {
	ctx := context.Background()
	db, cleanup := testutil.SetupPostgresDB(ctx, t)
	defer cleanup()
	mail := installPageRecorder(t)

	tenantID := testutil.InsertTenant(ctx, t, db, "escalation-empty")
	monitorID := testutil.InsertHTTPMonitor(ctx, t, db, tenantID, "API")
	alice, _ := insertPerson(ctx, t, db, tenantID, "alice", "editor")
	empty := insertScheduleWith(ctx, t, db, tenantID)
	policy := insertEscalationPolicy(ctx, t, db, tenantID, 0, 1800,
		policyStep{delay: 0, targets: []policyTarget{{kind: "schedule", id: empty}}},
		policyStep{delay: 60, targets: []policyTarget{{kind: "user", id: alice}}},
	)
	alertID := openDownAlert(ctx, t, db, tenantID, monitorID)
	mustExec(ctx, t, db, `UPDATE monitors SET notification_mode = 'policy', escalation_policy_id = $2 WHERE id = $1`, monitorID, policy)
	a := newIntegrationAlerter(db)

	runEscalations(ctx, t, a)
	mustExec(ctx, t, db, `UPDATE alerts SET triggered_at = NOW() - INTERVAL '2 minutes' WHERE id = $1`, alertID)
	runEscalations(ctx, t, a)
	if got := mail.count("alice@example.com", "created"); got != 1 {
		t.Fatalf("step after an empty schedule: alice=%d, want 1", got)
	}
	var claims int
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM alert_escalation_states WHERE alert_id = $1 AND relapse_count = 0`, alertID).Scan(&claims); err != nil {
		t.Fatal(err)
	}
	if claims != 2 {
		t.Fatalf("claimed steps = %d, want 2 (the empty one included)", claims)
	}

	// Relapse: a new episode pages again.
	mustExec(ctx, t, db, `UPDATE alerts SET relapse_count = 1, reopened_at = NOW() - INTERVAL '2 minutes' WHERE id = $1`, alertID)
	runEscalations(ctx, t, a)
	if got := mail.count("alice@example.com", "created"); got != 2 {
		t.Fatalf("relapse page: alice=%d, want 2", got)
	}
	var key string
	if err := db.QueryRowContext(ctx, `SELECT delivery_key FROM alert_escalation_pages WHERE alert_id = $1 AND relapse_count = 1`, alertID).Scan(&key); err != nil {
		t.Fatal(err)
	}
	if key != "escalation:1:0:1" {
		t.Fatalf("relapse ledger key = %q", key)
	}
}

// An SMS send in a workspace whose personal SMS channel is gone fails with
// no channel at all, like an email page; the delivery history types it from
// the contact (sms), not as an email, even after the contact is deleted.
// Pages skip such a contact (DeliverableContactPredicate), so the case is a
// notice to someone paged by SMS before the channel was cleared or paused.
func TestEscalationChannelLessSMSSendIsTypedSMS(t *testing.T) {
	ctx := context.Background()
	db, cleanup := testutil.SetupPostgresDB(ctx, t)
	defer cleanup()
	installPageRecorder(t)

	tenantID := testutil.InsertTenant(ctx, t, db, "escalation-sms-nochannel")
	monitorID := testutil.InsertHTTPMonitor(ctx, t, db, tenantID, "API")
	alice, _ := insertPerson(ctx, t, db, tenantID, "alice", "editor")
	carol, carolContact := uuid.New(), uuid.New()
	mustExec(ctx, t, db, `INSERT INTO admin_users (id, username, password_hash) VALUES ($1, $2, 'x')`, carol, "carol-"+carol.String()[:6])
	mustExec(ctx, t, db, `INSERT INTO tenant_memberships (admin_user_id, tenant_id, role) VALUES ($1, $2, 'editor')`, carol, tenantID)
	mustExec(ctx, t, db, `INSERT INTO user_contacts (id, user_id, kind, address, verified_at) VALUES ($1, $2, 'sms', '+33612345678', NOW())`,
		carolContact, carol)
	policy := insertEscalationPolicy(ctx, t, db, tenantID, 0, 1800,
		policyStep{delay: 0, targets: []policyTarget{{kind: "user", id: alice}, {kind: "user", id: carol}}},
	)
	alertID := openDownAlert(ctx, t, db, tenantID, monitorID)
	mustExec(ctx, t, db, `UPDATE monitors SET notification_mode = 'policy', escalation_policy_id = $2 WHERE id = $1`, monitorID, policy)
	a := newIntegrationAlerter(db)
	runEscalations(ctx, t, a) // pages alice by email; carol's SMS contact is not deliverable
	// Carol was paged by SMS while the workspace still had an SMS channel.
	mustExec(ctx, t, db, `INSERT INTO alert_escalation_pages (alert_id, contact_id, user_id, relapse_count, event, delivery_key)
		VALUES ($1, $2, $3, 0, 'page', 'escalation:0:0')`, alertID, carolContact, carol)
	mustExec(ctx, t, db, `UPDATE alerts SET status = 'resolved', resolved_at = NOW() WHERE id = $1`, alertID)
	runEscalations(ctx, t, a)

	rowsFor := func(user uuid.UUID) []deliverylog.Delivery {
		t.Helper()
		page, err := deliverylog.ListForAlert(ctx, db, tenantID, alertID, 1, 50)
		if err != nil {
			t.Fatalf("ListForAlert: %v", err)
		}
		var out []deliverylog.Delivery
		for _, d := range page.Items {
			if d.UserID != nil && *d.UserID == user {
				out = append(out, d)
			}
		}
		return out
	}
	sms := rowsFor(carol)
	if len(sms) != 1 {
		t.Fatalf("carol's delivery rows = %d, want 1 (the resolved notice)", len(sms))
	}
	if sms[0].ChannelID != nil || sms[0].ChannelType != "sms" || sms[0].Outcome != deliverylog.OutcomePermanentFailure {
		t.Fatalf("SMS notice row: channel_id=%v type=%q outcome=%q, want no channel, sms, permanent_failure",
			sms[0].ChannelID, sms[0].ChannelType, sms[0].Outcome)
	}
	for _, d := range rowsFor(alice) {
		if d.ChannelID != nil || d.ChannelType != "email" {
			t.Fatalf("email row for alice: channel_id=%v type=%q, want a channel-less email row", d.ChannelID, d.ChannelType)
		}
	}

	// The kind is stored on the row, so it outlives the contact.
	mustExec(ctx, t, db, `DELETE FROM user_contacts WHERE id = $1`, carolContact)
	if got := rowsFor(carol); len(got) != 1 || got[0].ChannelType != "sms" {
		t.Fatalf("SMS notice row after the contact is deleted: %+v, want type sms", got)
	}
}

// A default policy on the workspace replaces its default channels for
// monitor-less alerts too: TenantTargetsSQL emits the policy's channel
// targets, exactly as TargetsSQL does for a default-mode monitor.
func TestTenantTargetsSQLFollowsDefaultPolicy(t *testing.T) {
	ctx := context.Background()
	db, cleanup := testutil.SetupPostgresDB(ctx, t)
	defer cleanup()
	tenantID := testutil.InsertTenant(ctx, t, db, "default-policy")
	insertDefaultRoute(ctx, t, db, tenantID, "legacy-default", 0, false)
	pager := insertTestChannel(ctx, t, db, tenantID, "pager")
	policy := insertEscalationPolicy(ctx, t, db, tenantID, 0, 1800,
		policyStep{delay: 0, targets: []policyTarget{{kind: "all_admins"}}},
		policyStep{delay: 300, targets: []policyTarget{{kind: "channel", id: pager}}})
	mustExec(ctx, t, db, `UPDATE tenants SET default_escalation_policy_id = $2 WHERE id = $1`, tenantID, policy)
	def := testutil.InsertHTTPMonitor(ctx, t, db, tenantID, "default")

	collect := func(query string, args ...any) [][3]any {
		rows, err := db.QueryContext(ctx, `SELECT channel_id, delay_seconds, pos FROM (`+query+`) x ORDER BY pos`, args...)
		if err != nil {
			t.Fatal(err)
		}
		defer rows.Close()
		var out [][3]any
		for rows.Next() {
			var id uuid.UUID
			var delay, pos int
			if err := rows.Scan(&id, &delay, &pos); err != nil {
				t.Fatal(err)
			}
			out = append(out, [3]any{id, delay, pos})
		}
		return out
	}
	tenantRoutes := collect(alertrouting.TenantTargetsSQL(), tenantID)
	want := [][3]any{{pager, 300, 1}}
	if !reflect.DeepEqual(tenantRoutes, want) {
		t.Fatalf("TenantTargetsSQL = %v, want the policy's channel target %v", tenantRoutes, want)
	}
	if got := collect(alertrouting.TargetsSQL(), tenantID, pq.Array([]uuid.UUID{def})); !reflect.DeepEqual(got, tenantRoutes) {
		t.Fatalf("TargetsSQL for a default-mode monitor %v, TenantTargetsSQL %v", got, tenantRoutes)
	}
}
