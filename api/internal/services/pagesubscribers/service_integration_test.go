//go:build !unit

package pagesubscribers

// Subscribers against Postgres: double opt-in, the same answer for every
// address state, limits, expiry and pruning, unsubscribe and re-subscribe,
// the outbox written with each trigger, and the mail-configuration gate.

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/textproto"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/yassinebenameur/probara/api/internal/models"
	"github.com/yassinebenameur/probara/api/internal/services/audit"
	"github.com/yassinebenameur/probara/api/internal/services/incidents"
	"github.com/yassinebenameur/probara/api/internal/services/maintenancewindows"
	"github.com/yassinebenameur/probara/api/internal/services/pagenotify"
	"github.com/yassinebenameur/probara/api/internal/services/statuspages"
	"github.com/yassinebenameur/probara/shared/actionlink"
	"github.com/yassinebenameur/probara/shared/testutil"
)

// Subscribe, confirm, receive: a confirmed subscriber gets the incident with
// a working unsubscribe link; nothing but the confirmation reaches anyone
// before that.
func TestSubscribeConfirmReceive(t *testing.T) {
	f := setup(t)
	mailer := &fakeMailer{}
	s := f.service(mailer, nil)

	require.NoError(t, s.Subscribe(f.ctx, "acme", "Ops@Example.com"))
	require.NoError(t, s.Subscribe(f.ctx, "acme", "pending@example.com"))
	run(t, s)
	confirmations := mailer.to("ops@example.com", "Confirm your subscription")
	require.Len(t, confirmations, 1)
	require.Contains(t, confirmations[0].Text, "https://status.example.com/public/status/acme/subscription?action=confirm#")
	require.Empty(t, confirmations[0].Headers, "a confirmation carries no List-Unsubscribe")

	resp, err := s.Confirm(f.ctx, linkToken(t, confirmations[0]), audit.Event{})
	require.NoError(t, err)
	require.Equal(t, models.StatusPageSubscriptionResponse{State: "confirmed", PageTitle: "Acme", PageSlug: "acme"}, resp)
	// Following it again is harmless.
	resp, err = s.Confirm(f.ctx, linkToken(t, confirmations[0]), audit.Event{})
	require.NoError(t, err)
	require.Equal(t, "confirmed", resp.State)

	f.publish(t, "Checkout is failing")
	run(t, s)
	got := mailer.to("ops@example.com", "Checkout is failing")
	require.Len(t, got, 1)
	require.Equal(t, "[Acme] Incident: Checkout is failing", got[0].Subject)
	require.Equal(t, "Acme", got[0].FromName, "subscriber mail is sent in the page's name")
	require.Equal(t, "Acme", confirmations[0].FromName)
	require.Contains(t, got[0].Text, "Affected: Checkout", "the page's display name, never the internal monitor name")
	require.NotContains(t, got[0].Text, "checkout-internal")
	require.Contains(t, got[0].Headers["List-Unsubscribe"], "https://app.example.com"+OneClickUnsubscribePath+"?token=")
	require.Equal(t, "List-Unsubscribe=One-Click", got[0].Headers["List-Unsubscribe-Post"])
	require.Empty(t, mailer.to("pending@example.com", "Checkout is failing"), "an unconfirmed address got a notice")
	require.Equal(t, map[string]int{"sent": 1}, f.deliveryStates(t))
}

// Every address state gets the same answer; the mail follows the state, at
// most three confirmations a day, none for a confirmed address.
func TestSubscribe_SameAnswerAndMailBudget(t *testing.T) {
	f := setup(t)
	mailer := &fakeMailer{}
	now := time.Now().UTC()
	s := f.service(mailer, &now)
	f.confirmed(t, s, mailer, "member@example.com")
	confirmMails := func(a string) int { return len(mailer.to(a, "Confirm your subscription")) }

	for i := 0; i < 6; i++ {
		require.NoError(t, s.Subscribe(f.ctx, "acme", "member@example.com"), "confirmed")
		require.NoError(t, s.Subscribe(f.ctx, "acme", "fresh@example.com"), "new then pending")
		run(t, s)
	}
	require.Equal(t, 1, confirmMails("member@example.com"), "a confirmed address is mailed nothing")
	require.Equal(t, MaxConfirmationsPerDay, confirmMails("fresh@example.com"), "confirmations are capped per day")

	// Unsubscribed is the same as new.
	var id uuid.UUID
	require.NoError(t, f.db.QueryRowContext(f.ctx, `SELECT id FROM status_page_subscribers WHERE email = 'member@example.com'`).Scan(&id))
	token, err := s.token("page-unsubscribe", f.tenantID, f.pageID, id)
	require.NoError(t, err)
	resp, err := s.Unsubscribe(f.ctx, token, audit.Event{})
	require.NoError(t, err)
	require.Equal(t, "unsubscribed", resp.State)
	require.NoError(t, s.Subscribe(f.ctx, "acme", "member@example.com"))
	run(t, s)
	require.Equal(t, 2, confirmMails("member@example.com"), "re-subscribing after an unsubscribe asks for confirmation again")

	// A day later the budget is back.
	now = now.Add(25 * time.Hour)
	require.NoError(t, s.Subscribe(f.ctx, "acme", "fresh@example.com"))
	run(t, s)
	require.Equal(t, MaxConfirmationsPerDay+1, confirmMails("fresh@example.com"))
}

// Hostile input is refused before anything is stored; pages that take no
// subscriptions answer like a missing page.
func TestSubscribe_RefusesHostileAndClosed(t *testing.T) {
	f := setup(t)
	s := f.service(&fakeMailer{}, nil)
	for _, a := range []string{`"x@evil.example>"@example.com`, "a@example.com\r\nBcc: b@example.com", "Name <a@example.com>", "nope"} {
		err := s.Subscribe(f.ctx, "acme", a)
		require.True(t, errors.As(err, new(Invalid)), "%q: %v", a, err)
	}
	var n int
	require.NoError(t, f.db.QueryRowContext(f.ctx, `SELECT COUNT(*) FROM status_page_subscribers`).Scan(&n))
	require.Zero(t, n)

	require.ErrorIs(t, s.Subscribe(f.ctx, "no-such-page", "a@example.com"), ErrNotFound)
	for _, vis := range []string{"password", "private"} {
		_, err := f.db.ExecContext(f.ctx, `UPDATE status_pages SET visibility = $2, password_hash = 'x' WHERE id = $1`, f.pageID, vis)
		require.NoError(t, err)
		require.ErrorIs(t, s.Subscribe(f.ctx, "acme", "a@example.com"), ErrNotFound, vis)
	}
	_, err := f.db.ExecContext(f.ctx, `UPDATE status_pages SET visibility = 'unlisted' WHERE id = $1`, f.pageID)
	require.NoError(t, err)
	require.NoError(t, s.Subscribe(f.ctx, "acme", "a@example.com"), "unlisted pages take subscriptions")
	f.setting(t, "enable_email_subscriptions", false)
	require.ErrorIs(t, s.Subscribe(f.ctx, "acme", "b@example.com"), ErrNotFound)
	f.setting(t, "enable_email_subscriptions", true)
	noMail := NewService(f.db, Config{StatusPageBaseURL: "https://s.example.com"})
	require.ErrorIs(t, noMail.Subscribe(f.ctx, "acme", "b@example.com"), ErrNotFound, "no mail, no subscriptions")
}

// The page limits refuse every address alike, confirmed ones included. The
// hourly limit counts every address taken in the hour, whatever became of
// it: confirming addresses as they arrive does not free room.
func TestSubscribe_PageLimits(t *testing.T) {
	f := setup(t)
	mailer := &fakeMailer{}
	s := f.service(mailer, nil)
	f.confirmed(t, s, mailer, "member@example.com")
	_, err := f.db.ExecContext(f.ctx, `
		INSERT INTO status_page_subscribers (tenant_id, status_page_id, email, email_hash, inbox_hash, state, confirmed_at)
		SELECT $1, $2, 'flood' || g || '@catchall.example', 'h' || g, 'i' || g, 'confirmed', NOW() FROM generate_series(1, $3) g
	`, f.tenantID, f.pageID, MaxNewPerPagePerHour-1)
	require.NoError(t, err)
	require.ErrorIs(t, s.Subscribe(f.ctx, "acme", "new@example.com"), ErrLimit, "confirmed within the hour still count")
	require.ErrorIs(t, s.Subscribe(f.ctx, "acme", "member@example.com"), ErrLimit, "same answer for a confirmed address")

	_, err = f.db.ExecContext(f.ctx, `UPDATE status_page_subscribers SET created_at = NOW() - INTERVAL '2 hours'`)
	require.NoError(t, err)
	require.NoError(t, s.Subscribe(f.ctx, "acme", "new@example.com"), "the hourly limit rolls over")

	// Pending addresses are capped on their own.
	_, err = f.db.ExecContext(f.ctx, `
		INSERT INTO status_page_subscribers (tenant_id, status_page_id, email, email_hash, inbox_hash, state, created_at)
		SELECT $1, $2, 'wait' || g || '@example.com', 'w' || g, 'wi' || g, 'pending', NOW() - INTERVAL '2 hours' FROM generate_series(1, $3) g
	`, f.tenantID, f.pageID, MaxPendingPerPage)
	require.NoError(t, err)
	require.ErrorIs(t, s.Subscribe(f.ctx, "acme", "another@example.com"), ErrLimit)
	_, err = f.db.ExecContext(f.ctx, `DELETE FROM status_page_subscribers WHERE email LIKE 'wait%'`)
	require.NoError(t, err)

	_, err = f.db.ExecContext(f.ctx, `
		INSERT INTO status_page_subscribers (tenant_id, status_page_id, email, email_hash, inbox_hash, state, created_at)
		SELECT $1, $2, 'cap' || g || '@example.com', 'c' || g, 'ci' || g, 'confirmed', NOW() - INTERVAL '2 hours' FROM generate_series(1, $3) g
	`, f.tenantID, f.pageID, MaxSubscribersPerPage)
	require.NoError(t, err)
	require.ErrorIs(t, s.Subscribe(f.ctx, "acme", "another@example.com"), ErrLimit)
	require.ErrorIs(t, s.Subscribe(f.ctx, "acme", "member@example.com"), ErrLimit)

	// An operator clears a flood by domain, or everything.
	n, err := s.RemoveMany(f.ctx, f.tenantID, f.pageID, &models.RemoveStatusPageSubscribersRequest{Domain: "@CatchAll.example"})
	require.NoError(t, err)
	require.Equal(t, MaxNewPerPagePerHour-1, n)
	_, err = s.RemoveMany(f.ctx, f.tenantID, f.pageID, &models.RemoveStatusPageSubscribersRequest{})
	require.True(t, errors.As(err, new(Invalid)))
	_, err = s.RemoveMany(f.ctx, uuid.New(), f.pageID, &models.RemoveStatusPageSubscribersRequest{All: true})
	require.ErrorIs(t, err, ErrNotFound, "another tenant's page")
	n, err = s.RemoveMany(f.ctx, f.tenantID, f.pageID, &models.RemoveStatusPageSubscribersRequest{All: true})
	require.NoError(t, err)
	require.Equal(t, MaxSubscribersPerPage+2, n)
}

// One inbox gets a bounded number of confirmations a day, however many
// +tag aliases and pages are used; the installation's confirmation mail is
// capped per hour, and notices are never held back by it.
func TestConfirmationCaps(t *testing.T) {
	f := setup(t)
	mailer := &fakeMailer{}
	s := f.service(mailer, nil)
	for i := 0; i < MaxConfirmationsPerInboxPerDay+3; i++ {
		require.NoError(t, s.Subscribe(f.ctx, "acme", fmt.Sprintf("Victim+%d@example.com", i)))
		run(t, s)
	}
	require.Len(t, mailer.recipients("Confirm your subscription"), MaxConfirmationsPerInboxPerDay)
	require.Equal(t, InboxHash("victim+3@example.com"), InboxHash("victim@example.com"))
	require.NotEqual(t, InboxHash("victim@example.com"), InboxHash("other@example.com"))

	_, err := f.db.ExecContext(f.ctx, `INSERT INTO status_page_confirmation_sends (inbox_hash, sent_at)
		SELECT 'x' || g, NOW() FROM generate_series(1, $1) g`, MaxConfirmationsPerHour)
	require.NoError(t, err)
	f.confirmedViaSQL(t, "ops@example.com")
	require.NoError(t, s.Subscribe(f.ctx, "acme", "fresh@example.com"))
	f.publish(t, "During the cap")
	run(t, s)
	require.Empty(t, mailer.to("fresh@example.com", "Confirm"), "confirmation sent past the hourly cap")
	require.Len(t, mailer.to("ops@example.com", "During the cap"), 1, "a notice was held back by the confirmation cap")
}

func (f *fixture) confirmedViaSQL(t *testing.T, address string) {
	t.Helper()
	_, err := f.db.ExecContext(f.ctx, `
		INSERT INTO status_page_subscribers (tenant_id, status_page_id, email, email_hash, inbox_hash, state, confirmed_at)
		VALUES ($1, $2, $3, $4, $5, 'confirmed', NOW())`, f.tenantID, f.pageID, address, AddressHash(address), InboxHash(address))
	require.NoError(t, err)
}

// Notices go before confirmations in every pass, and confirmations are
// bounded per pass; a subscribe that queues nothing does not wake the
// runner.
func TestRunner_NoticesBeforeConfirmations(t *testing.T) {
	f := setup(t)
	mailer := &fakeMailer{}
	s := f.service(mailer, nil)
	f.confirmedViaSQL(t, "ops@example.com")
	_, err := f.db.ExecContext(f.ctx, `
		INSERT INTO status_page_subscribers (tenant_id, status_page_id, email, email_hash, inbox_hash, state, created_at, confirm_due_at, confirm_window_start, confirm_window_sends)
		SELECT $1, $2, 'p' || g || '@example.com', 'ph' || g, 'pi' || g, 'pending', NOW() - INTERVAL '2 hours', NOW(), NOW(), 1 FROM generate_series(1, 120) g
	`, f.tenantID, f.pageID)
	require.NoError(t, err)
	f.publish(t, "Priority")
	run(t, s)
	require.Equal(t, "[Acme] Incident: Priority", mailer.sent[0].Subject, "the notice went after confirmations")
	require.Len(t, mailer.recipients("Confirm your subscription"), maxConfirmationsPerTick)

	for len(s.wake) > 0 {
		<-s.wake
	}
	s.lastWake = time.Time{}
	require.NoError(t, s.Subscribe(f.ctx, "acme", "ops@example.com"))
	require.Len(t, s.wake, 0, "a no-op subscribe woke the runner")
	require.NoError(t, s.Subscribe(f.ctx, "acme", "fresh@example.com"))
	require.Len(t, s.wake, 1)
	require.NoError(t, s.Subscribe(f.ctx, "acme", "fresh2@example.com"))
	require.Len(t, s.wake, 1, "wakes are rate-limited")
}

// Confirmation links expire after 48 hours; unconfirmed addresses are
// pruned a week after their last request.
func TestConfirmationExpiryAndPruning(t *testing.T) {
	f := setup(t)
	mailer := &fakeMailer{}
	past := time.Now().UTC().Add(-49 * time.Hour)
	s := f.service(mailer, &past)
	require.NoError(t, s.Subscribe(f.ctx, "acme", "late@example.com"))
	run(t, s)
	token := linkToken(t, mailer.to("late@example.com", "Confirm")[0])

	now := time.Now().UTC()
	s.now = func() time.Time { return now }
	resp, err := s.Confirm(f.ctx, token, audit.Event{})
	require.NoError(t, err)
	require.Equal(t, "expired", resp.State)
	var state string
	require.NoError(t, f.db.QueryRowContext(f.ctx, `SELECT state FROM status_page_subscribers WHERE email = 'late@example.com'`).Scan(&state))
	require.Equal(t, "pending", state)

	resp, err = s.Confirm(f.ctx, "garbage", audit.Event{})
	require.NoError(t, err)
	require.Equal(t, "invalid", resp.State)

	now = past.Add(PendingRetention + time.Minute)
	s.lastPrune = time.Time{}
	run(t, s)
	var n int
	require.NoError(t, f.db.QueryRowContext(f.ctx, `SELECT COUNT(*) FROM status_page_subscribers`).Scan(&n))
	require.Zero(t, n, "the unconfirmed address was not pruned")
}

// Unsubscribe from the body link and from the one-click URL both delete
// the address; repeating answers the same; re-subscribing starts over.
func TestUnsubscribe(t *testing.T) {
	f := setup(t)
	mailer := &fakeMailer{}
	s := f.service(mailer, nil)
	f.confirmed(t, s, mailer, "a@example.com")
	f.confirmed(t, s, mailer, "b@example.com")
	f.publish(t, "First")
	run(t, s)

	a := mailer.to("a@example.com", "First")[0]
	b := mailer.to("b@example.com", "First")[0]
	rec := s.audit.(*recorder)
	resp, err := s.Unsubscribe(f.ctx, linkToken(t, a), audit.Event{})
	require.NoError(t, err)
	require.Equal(t, "unsubscribed", resp.State)
	resp, err = s.Unsubscribe(f.ctx, oneClickToken(t, b), audit.Event{})
	require.NoError(t, err)
	require.Equal(t, "unsubscribed", resp.State)
	resp, err = s.Unsubscribe(f.ctx, oneClickToken(t, b), audit.Event{})
	require.NoError(t, err)
	require.Equal(t, "unsubscribed", resp.State, "repeating is a no-op")
	var n int
	require.NoError(t, f.db.QueryRowContext(f.ctx, `SELECT COUNT(*) FROM status_page_subscribers`).Scan(&n))
	require.Zero(t, n, "unsubscribing removes the address")
	unsubscribes := 0
	for _, e := range rec.events {
		if e.Action == "status-page.subscriber-unsubscribe" {
			unsubscribes++
			require.NotContains(t, e.Details, "email")
			require.Len(t, e.Details["address_hash"], 64)
		}
	}
	require.Equal(t, 2, unsubscribes)

	// A confirmation token never unsubscribes, nor the reverse.
	resp, err = s.Unsubscribe(f.ctx, linkToken(t, mailer.to("a@example.com", "Confirm")[0]), audit.Event{})
	require.NoError(t, err)
	require.Equal(t, "invalid", resp.State)

	f.publish(t, "Second")
	run(t, s)
	require.Empty(t, mailer.recipients("Second"))

	f.confirmed(t, s, mailer, "a@example.com")
	f.publish(t, "Third")
	run(t, s)
	require.Equal(t, []string{"a@example.com"}, mailer.recipients("Third"))

	// A browser that opened the one-click URL is sent to the page's
	// confirmation; nothing changes.
	target, err := s.UnsubscribePageURL(f.ctx, oneClickToken(t, mailer.to("a@example.com", "Third")[0]))
	require.NoError(t, err)
	require.True(t, strings.HasPrefix(target, "https://status.example.com/public/status/acme/subscription?action=unsubscribe#"), target)
	require.NoError(t, f.db.QueryRowContext(f.ctx, `SELECT COUNT(*) FROM status_page_subscribers`).Scan(&n))
	require.Equal(t, 1, n)
}

// The operator view: list with counts, remove, CSV export.
func TestOperatorListRemoveExport(t *testing.T) {
	f := setup(t)
	mailer := &fakeMailer{}
	s := f.service(mailer, nil)
	f.confirmed(t, s, mailer, "=formula@example.com")
	require.NoError(t, s.Subscribe(f.ctx, "acme", "pending@example.com"))

	list, err := s.List(f.ctx, f.tenantID, f.pageID, 1, 50)
	require.NoError(t, err)
	require.Equal(t, models.StatusPageSubscriberCounts{Confirmed: 1, Pending: 1}, list.Counts)
	require.True(t, list.MailReady)
	require.Len(t, list.Items, 2)

	var buf strings.Builder
	n, err := s.ExportCSV(f.ctx, f.tenantID, f.pageID, &buf)
	require.NoError(t, err)
	require.Equal(t, 2, n)
	require.True(t, strings.HasPrefix(buf.String(), "email,state,subscribed_at,confirmed_at\n'=formula@example.com,confirmed,"), buf.String())

	other := uuid.New()
	_, err = s.List(f.ctx, other, f.pageID, 1, 50)
	require.ErrorIs(t, err, ErrNotFound, "another tenant's page")
	require.ErrorIs(t, s.Remove(f.ctx, other, f.pageID, list.Items[0].ID), ErrNotFound)
	require.NoError(t, s.Remove(f.ctx, f.tenantID, f.pageID, list.Items[0].ID))
	require.ErrorIs(t, s.Remove(f.ctx, f.tenantID, f.pageID, list.Items[0].ID), ErrNotFound)
}

// Turning subscriptions on is refused while mail is not configured; a page
// already on keeps saving other settings.
func TestEnableRefusedWithoutMail(t *testing.T) {
	f := setup(t)
	f.setting(t, "enable_email_subscriptions", false)
	pages := statuspages.NewService(f.db, nil)
	pages.ConfigureSubscriberMail(NewService(f.db, Config{}).SubscriptionProblems)
	on, footer := true, false
	_, err := pages.UpdateStatusPage(f.ctx, f.tenantID, f.pageID, &models.UpdateStatusPageRequest{Settings: &models.StatusPageSettings{EnableEmailSubscriptions: &on}})
	require.ErrorIs(t, err, statuspages.ErrSubscriptionsUnavailable)
	require.Contains(t, err.Error(), "SMTP")
	_, err = pages.CreateStatusPage(f.ctx, f.tenantID, &models.CreateStatusPageRequest{Slug: "other", Title: "Other", Settings: &models.StatusPageSettings{EnableEmailSubscriptions: &on}})
	require.ErrorIs(t, err, statuspages.ErrSubscriptionsUnavailable)

	f.setting(t, "enable_email_subscriptions", true)
	page, err := pages.UpdateStatusPage(f.ctx, f.tenantID, f.pageID, &models.UpdateStatusPageRequest{Settings: &models.StatusPageSettings{EnableEmailSubscriptions: &on, ShowFooter: &footer}})
	require.NoError(t, err, "a page already on keeps saving")
	require.True(t, *page.Settings.EnableEmailSubscriptions)

	ready := statuspages.NewService(f.db, nil)
	ready.ConfigureSubscriberMail(f.service(&fakeMailer{}, nil).SubscriptionProblems)
	noToken := NewService(f.db, Config{Mailer: &fakeMailer{}, Links: actionlink.New(testKeys{}), AppBaseURL: "https://a.example", StatusPageBaseURL: "https://s.example"})
	require.Empty(t, noToken.MailProblems())
	require.NotEmpty(t, noToken.SubscriptionProblems(), "subscriptions need the internal token")
	off := false
	auto := true
	page, err = ready.UpdateStatusPage(f.ctx, f.tenantID, f.pageID, &models.UpdateStatusPageRequest{Settings: &models.StatusPageSettings{EnableEmailSubscriptions: &off, AutoPublishIncidents: &auto}})
	require.NoError(t, err)
	require.False(t, *page.Settings.EnableEmailSubscriptions)
	require.True(t, *page.Settings.AutoPublishIncidents)
	page, err = ready.UpdateStatusPage(f.ctx, f.tenantID, f.pageID, &models.UpdateStatusPageRequest{Settings: &models.StatusPageSettings{EnableEmailSubscriptions: &on}})
	require.NoError(t, err)
	require.True(t, *page.Settings.EnableEmailSubscriptions)
	require.True(t, *page.Settings.AutoPublishIncidents, "an absent setting is kept")
}

// Every trigger writes its outbox row in the change's own transaction, and
// only the changes subscribers hear about write one.
func TestOutboxWrittenWithEachTrigger(t *testing.T) {
	f := setup(t)
	id := f.publish(t, "Outage")
	require.Equal(t, 1, f.outbox(t, pagenotify.KindIncidentPublished))
	// Publishing it again unchanged, or changing its components, is not news.
	_, err := f.incidents.PublishIncidentToStatusPage(f.ctx, f.tenantID, id, f.pageID, &models.UpsertIncidentPublicationRequest{MonitorIDs: []string{f.monitorID.String()}})
	require.NoError(t, err)
	_, err = f.incidents.PublishIncidentToStatusPage(f.ctx, f.tenantID, id, f.pageID, &models.UpsertIncidentPublicationRequest{})
	require.NoError(t, err)
	require.Equal(t, 1, f.outbox(t, pagenotify.KindIncidentPublished))

	_, err = f.incidents.CreateIncidentTimelineEntry(f.ctx, f.tenantID, id, &models.CreateIncidentTimelineEntryRequest{EntryType: models.IncidentTimelineEntryTypeInternalNote, Message: "internal"})
	require.NoError(t, err)
	require.Zero(t, f.outbox(t, pagenotify.KindIncidentUpdated))
	_, err = f.incidents.CreateIncidentTimelineEntry(f.ctx, f.tenantID, id, &models.CreateIncidentTimelineEntryRequest{EntryType: models.IncidentTimelineEntryTypePublicUpdate, Message: "Fix deployed"})
	require.NoError(t, err)
	require.Equal(t, 1, f.outbox(t, pagenotify.KindIncidentUpdated))

	_, err = f.incidents.TransitionIncidentState(f.ctx, f.tenantID, id, &models.TransitionIncidentStateRequest{State: models.IncidentStateMonitoring})
	require.NoError(t, err)
	require.Zero(t, f.outbox(t, pagenotify.KindIncidentResolved))
	_, err = f.incidents.TransitionIncidentState(f.ctx, f.tenantID, id, &models.TransitionIncidentStateRequest{State: models.IncidentStateResolved})
	require.NoError(t, err)
	require.Equal(t, 1, f.outbox(t, pagenotify.KindIncidentResolved))

	// Taken down and published again: announced again.
	_, err = f.incidents.UnpublishIncidentFromStatusPage(f.ctx, f.tenantID, id, f.pageID)
	require.NoError(t, err)
	_, err = f.incidents.PublishIncidentToStatusPage(f.ctx, f.tenantID, id, f.pageID, &models.UpsertIncidentPublicationRequest{})
	require.NoError(t, err)
	require.Equal(t, 2, f.outbox(t, pagenotify.KindIncidentPublished))

	// A publication rolled back leaves no row.
	other := f.newIncident(t, "Rolled back")
	tx, err := f.db.BeginTx(f.ctx, nil)
	require.NoError(t, err)
	announced, err := incidents.PublishTx(f.ctx, tx, f.tenantID, other, f.pageID, nil, incidents.PublishOptions{})
	require.NoError(t, err)
	require.True(t, announced)
	require.NoError(t, tx.Rollback())
	require.Equal(t, 2, f.outbox(t, pagenotify.KindIncidentPublished))
}

func (f *fixture) newIncident(t *testing.T, title string) uuid.UUID {
	t.Helper()
	inc, err := f.incidents.CreateIncident(f.ctx, f.tenantID, &models.CreateIncidentRequest{Title: title, Summary: "s", MonitorID: f.monitorID.String()})
	require.NoError(t, err)
	return inc.ID
}

// The maintenance rule: a window is announced when it is scheduled (and
// again when its schedule or targets change) to pages that show a monitor
// an occurrence ahead covers; a window that starts now (a snooze) is not;
// a recurring window is announced once with its rule and never per
// occurrence; a one-off announced window's start is announced once.
func TestMaintenanceAnnouncements(t *testing.T) {
	f := setup(t)
	mailer := &fakeMailer{}
	s := f.service(mailer, nil)
	f.confirmed(t, s, mailer, "ops@example.com")
	windows := maintenancewindows.NewService(f.db)
	unrelated := testutil.InsertHTTPMonitor(f.ctx, t, f.db, f.tenantID, "elsewhere")

	start := time.Now().UTC().Add(2 * time.Hour).Truncate(time.Minute)
	oneOff, err := windows.Create(f.ctx, f.tenantID, &models.CreateMaintenanceWindowRequest{
		Title: "Database upgrade", Description: "Expect short interruptions.", StartsAt: start, EndsAt: start.Add(time.Hour),
		MonitorIDs: []string{f.monitorID.String()},
	})
	require.NoError(t, err)
	_, err = windows.Create(f.ctx, f.tenantID, &models.CreateMaintenanceWindowRequest{
		Title: "Elsewhere", StartsAt: start, EndsAt: start.Add(time.Hour), MonitorIDs: []string{unrelated.String()},
	})
	require.NoError(t, err)
	_, err = windows.SnoozeMonitor(f.ctx, f.tenantID, f.monitorID, time.Now().Add(30*time.Minute))
	require.NoError(t, err)
	_, err = windows.Create(f.ctx, f.tenantID, &models.CreateMaintenanceWindowRequest{
		Title: "Nightly backup", MonitorIDs: []string{f.monitorID.String()},
		Recurrence: &models.MaintenanceRecurrence{Frequency: "weekly", Weekdays: []int{0, 1, 2, 3, 4, 5, 6}, StartTime: "03:00", DurationMinutes: 30, Timezone: "Europe/Paris"},
	})
	require.NoError(t, err)
	require.Equal(t, 2, f.outbox(t, pagenotify.KindMaintenanceScheduled), "the one-off and the recurring window, not the snooze or another page's")

	run(t, s)
	scheduled := mailer.to("ops@example.com", "Scheduled maintenance: Database upgrade")
	require.Len(t, scheduled, 1)
	require.Contains(t, scheduled[0].Text, "Starts: "+start.Format("Mon 2 Jan 2006 15:04 UTC"))
	require.Contains(t, scheduled[0].Text, "Affected: Checkout")
	nightly := mailer.to("ops@example.com", "Scheduled maintenance: Nightly backup")
	require.Len(t, nightly, 1)
	require.Contains(t, nightly[0].Text, "Repeats: Every day at 03:00 (Europe/Paris), 30 min each")
	require.Empty(t, mailer.to("ops@example.com", "Snooze"))

	// A title edit is not news; a schedule change is.
	title := "Database upgrade (v16)"
	_, err = windows.Update(f.ctx, f.tenantID, oneOff.ID, &models.UpdateMaintenanceWindowRequest{Title: &title})
	require.NoError(t, err)
	require.Equal(t, 2, f.outbox(t, pagenotify.KindMaintenanceScheduled))
	later := start.Add(time.Hour)
	end := later.Add(time.Hour)
	_, err = windows.Update(f.ctx, f.tenantID, oneOff.ID, &models.UpdateMaintenanceWindowRequest{StartsAt: &later, EndsAt: &end})
	require.NoError(t, err)
	require.Equal(t, 3, f.outbox(t, pagenotify.KindMaintenanceScheduled))
	run(t, s)
	require.Len(t, mailer.to("ops@example.com", "Maintenance rescheduled: Database upgrade (v16)"), 1)

	// Both windows' occurrences start: only the one-off is announced, once.
	_, err = f.db.ExecContext(f.ctx, `UPDATE maintenance_window_occurrences SET starts_at = NOW() - INTERVAL '1 minute', ends_at = NOW() + INTERVAL '1 hour'
		WHERE window_id IN (SELECT id FROM maintenance_windows WHERE title IN ('Database upgrade (v16)', 'Nightly backup'))
		  AND starts_at = (SELECT MIN(o2.starts_at) FROM maintenance_window_occurrences o2 WHERE o2.window_id = maintenance_window_occurrences.window_id)`)
	require.NoError(t, err)
	run(t, s)
	run(t, s)
	require.Equal(t, 1, f.outbox(t, pagenotify.KindMaintenanceStarted))
	require.Len(t, mailer.to("ops@example.com", "Maintenance started: Database upgrade (v16)"), 1)
	require.Empty(t, mailer.to("ops@example.com", "Maintenance started: Nightly backup"))
}

// Planned rows that cannot be mailed are skipped with the reason, never
// kept for later.
func TestPlanningSkips(t *testing.T) {
	f := setup(t)
	mailer := &fakeMailer{}
	s := f.service(mailer, nil)
	f.confirmed(t, s, mailer, "ops@example.com")
	f.setting(t, "enable_email_subscriptions", false)
	f.publish(t, "While off")
	run(t, s)
	f.setting(t, "enable_email_subscriptions", true)
	run(t, s)
	require.Empty(t, mailer.recipients("While off"))
	var reason sql.NullString
	require.NoError(t, f.db.QueryRowContext(f.ctx, `SELECT skip_reason FROM status_page_notification_outbox WHERE status = 'skipped'`).Scan(&reason))
	require.Contains(t, reason.String, "takes no email subscriptions")

	noMail := NewService(f.db, Config{})
	f.publish(t, "No mail")
	require.NoError(t, noMail.RunOnce(context.Background()))
	run(t, s)
	require.Empty(t, mailer.recipients("No mail"), "a row skipped for lack of mail is not sent once mail works")
}

// The operator UI sends the whole form on every save (title, description,
// the unchanged target list, a weekly window's unchanged rule): that is not
// a schedule change and announces nothing.
func TestMaintenance_UISaveIsNotAnnounced(t *testing.T) {
	f := setup(t)
	windows := maintenancewindows.NewService(f.db)
	start := time.Now().UTC().Add(2 * time.Hour).Truncate(time.Minute)
	oneOff, err := windows.Create(f.ctx, f.tenantID, &models.CreateMaintenanceWindowRequest{
		Title: "Upgrade", StartsAt: start, EndsAt: start.Add(time.Hour), MonitorIDs: []string{f.monitorID.String()},
	})
	require.NoError(t, err)
	rule := &models.MaintenanceRecurrence{Frequency: "weekly", Weekdays: []int{1, 3}, StartTime: "02:00", DurationMinutes: 60, Timezone: "Europe/Paris"}
	weekly, err := windows.Create(f.ctx, f.tenantID, &models.CreateMaintenanceWindowRequest{
		Title: "Backups", MonitorIDs: []string{f.monitorID.String()}, Recurrence: rule,
	})
	require.NoError(t, err)
	require.Equal(t, 2, f.outbox(t, pagenotify.KindMaintenanceScheduled))

	title, description := "Upgrade (fixed typo)", "Now with a description"
	ids := []string{f.monitorID.String()}
	_, err = windows.Update(f.ctx, f.tenantID, oneOff.ID, &models.UpdateMaintenanceWindowRequest{Title: &title, Description: &description, MonitorIDs: &ids})
	require.NoError(t, err)
	backups := "Backups (nightly)"
	sameRule := *rule
	_, err = windows.Update(f.ctx, f.tenantID, weekly.ID, &models.UpdateMaintenanceWindowRequest{Title: &backups, Description: &description, MonitorIDs: &ids, Recurrence: &sameRule})
	require.NoError(t, err)
	require.Equal(t, 2, f.outbox(t, pagenotify.KindMaintenanceScheduled), "a save of the unchanged form was announced again")

	other := testutil.InsertHTTPMonitor(f.ctx, t, f.db, f.tenantID, "other")
	testutil.AddMonitorToStatusPage(f.ctx, t, f.db, f.pageID, other, 1)
	ids = append(ids, other.String())
	_, err = windows.Update(f.ctx, f.tenantID, oneOff.ID, &models.UpdateMaintenanceWindowRequest{Title: &title, MonitorIDs: &ids})
	require.NoError(t, err)
	require.Equal(t, 3, f.outbox(t, pagenotify.KindMaintenanceScheduled), "a real target change was not announced")
}

// Subscribers told about a window are told when it is deleted; those who
// subscribed after the announcement are not, and a window nobody was told
// about is deleted quietly (and outright, when it never started).
func TestMaintenance_CancellationNotice(t *testing.T) {
	f := setup(t)
	mailer := &fakeMailer{}
	s := f.service(mailer, nil)
	f.confirmed(t, s, mailer, "early@example.com")
	windows := maintenancewindows.NewService(f.db)
	start := time.Now().UTC().Add(3 * time.Hour).Truncate(time.Minute)
	w, err := windows.Create(f.ctx, f.tenantID, &models.CreateMaintenanceWindowRequest{
		Title: "Network work", StartsAt: start, EndsAt: start.Add(time.Hour), MonitorIDs: []string{f.monitorID.String()},
	})
	require.NoError(t, err)
	run(t, s)
	require.Len(t, mailer.to("early@example.com", "Scheduled maintenance: Network work"), 1)
	f.confirmed(t, s, mailer, "late@example.com")

	require.NoError(t, windows.Delete(f.ctx, f.tenantID, w.ID))
	run(t, s)
	cancelled := mailer.to("early@example.com", "Maintenance cancelled: Network work")
	require.Len(t, cancelled, 1)
	require.Contains(t, cancelled[0].Text, "Was scheduled for: "+start.Format("Mon 2 Jan 2006 15:04 UTC"))
	require.Empty(t, mailer.to("late@example.com", "Maintenance cancelled"), "a subscriber who was never told got the cancellation")
	var deleted bool
	require.NoError(t, f.db.QueryRowContext(f.ctx, `SELECT deleted_at IS NOT NULL FROM maintenance_windows WHERE id = $1`, w.ID).Scan(&deleted))
	require.True(t, deleted, "an announced window is kept, soft-deleted")

	quiet, err := windows.Create(f.ctx, f.tenantID, &models.CreateMaintenanceWindowRequest{
		Title: "Elsewhere", StartsAt: start, EndsAt: start.Add(time.Hour), MonitorIDs: []string{testutil.InsertHTTPMonitor(f.ctx, t, f.db, f.tenantID, "x").String()},
	})
	require.NoError(t, err)
	require.NoError(t, windows.Delete(f.ctx, f.tenantID, quiet.ID))
	require.Equal(t, 1, f.outbox(t, pagenotify.KindMaintenanceCancelled))
	var gone bool
	require.NoError(t, f.db.QueryRowContext(f.ctx, `SELECT NOT EXISTS (SELECT 1 FROM maintenance_windows WHERE id = $1)`, quiet.ID).Scan(&gone))
	require.True(t, gone)
}

// What a page's recent notices came to is readable, failures included, so
// mail lost to a relay outage shows.
func TestSummaryReportsOutcomes(t *testing.T) {
	f := setup(t)
	mailer := &fakeMailer{}
	now := time.Now().UTC()
	s := f.service(mailer, &now)
	f.confirmedViaSQL(t, "ops@example.com")
	f.confirmedViaSQL(t, "bounce@example.com")
	mailer.setFail(func(to string) error {
		if to == "bounce@example.com" {
			return &textproto.Error{Code: 550, Msg: "no such user"}
		}
		return nil
	})
	f.publish(t, "Reported")
	run(t, s)
	sum, err := s.Summary(f.ctx, f.tenantID, f.pageID)
	require.NoError(t, err)
	require.Equal(t, 2, sum.Counts.Confirmed)
	require.Len(t, sum.Recent, 1)
	o := sum.Recent[0]
	require.Equal(t, "planned", o.Status)
	require.Equal(t, "Incident: Reported", o.Subject)
	require.Equal(t, 1, o.Sent)
	require.Equal(t, 1, o.Failed)
	require.Contains(t, o.LastError, "no such user")
}
