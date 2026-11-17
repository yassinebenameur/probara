package pagesubscribers

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/textproto"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/lib/pq"
	"golang.org/x/sync/errgroup"

	"github.com/yassinebenameur/probara/shared/actionlink"
	"github.com/yassinebenameur/probara/shared/mail"
	"github.com/yassinebenameur/probara/shared/statusupdates"
)

const (
	// runInterval is how often the Runner looks for work; a subscribe
	// request wakes it earlier. tickTimeout bounds one pass (what is
	// unfinished is handed back and picked up next time).
	runInterval = 15 * time.Second
	tickTimeout = 5 * time.Minute
	// Per-pass bounds. Deliveries are claimed round-robin across outbox rows
	// (maxOutboxPerBatch rows, deliveriesPerOutbox each), so a page with
	// thousands of subscribers never holds back another page's notice.
	maxPlansPerTick         = 50
	maxConfirmationsPerTick = 100
	confirmationBatch       = 25
	maxDeliveriesPerTick    = 1000
	maxOutboxPerBatch       = 20
	deliveriesPerOutbox     = 25
	// maxPlanAge: a row not planned by then (the API was down, or mail
	// was not configured) is skipped rather than mailed late.
	maxPlanAge = 6 * time.Hour
	// staleClaimAfter hands a reserved delivery or confirmation back: its
	// runner died or is far behind, and nothing was sent.
	// staleSendingAfter closes one whose send started that long ago as
	// uncertain: far above the mailer's 30-second bound per message.
	staleClaimAfter   = 15 * time.Minute
	staleSendingAfter = 15 * time.Minute
	// maxAttempts is the started sends a delivery gets before it fails;
	// maxConfirmAttempts the same for one confirmation request.
	maxAttempts        = 5
	maxConfirmAttempts = 3
	// recordTimeout bounds writing an outcome, which deliberately outlives
	// the pass's own cancellation.
	recordTimeout = 30 * time.Second
	maxErrorLen   = 500
	// outboxRetention keeps finished outbox rows (and their deliveries)
	// this long; pruneEvery paces the prune.
	outboxRetention = 30 * 24 * time.Hour
	pruneEvery      = time.Hour
)

// retryBackoff is the wait after the nth failed send (1-based).
var retryBackoff = []time.Duration{time.Minute, 5 * time.Minute, 30 * time.Minute, 2 * time.Hour}

const interruptedError = "the API stopped while sending; the message may or may not have arrived, so it is not sent again"

// RunOnce does one pass: hands back stale claims, publishes due outages and
// resolves recovered ones, announces starting maintenance, plans pending
// outbox rows, sends owed confirmations and due deliveries, and prunes. It
// stops early, without an error, once ctx is done.
func (s *Service) RunOnce(ctx context.Context) error {
	if err := s.reclaimStale(ctx); err != nil {
		return quietCancel(ctx, err)
	}
	// Automatic publishing needs no mail: it puts incidents on pages.
	if err := s.autoPublish(ctx); err != nil {
		return quietCancel(ctx, err)
	}
	if err := s.autoResolve(ctx); err != nil {
		return quietCancel(ctx, err)
	}
	if err := s.sweepMaintenanceStarts(ctx); err != nil {
		return quietCancel(ctx, err)
	}
	for i := 0; i < maxPlansPerTick && ctx.Err() == nil; i++ {
		planned, err := s.planOne(ctx)
		if err != nil {
			return quietCancel(ctx, err)
		}
		if !planned {
			break
		}
	}
	if s.Ready() {
		for sent := 0; sent < maxConfirmationsPerTick && ctx.Err() == nil; {
			n, err := s.sendConfirmations(ctx)
			if err != nil {
				return quietCancel(ctx, err)
			}
			if n == 0 {
				break
			}
			sent += n
		}
		for sent := 0; sent < maxDeliveriesPerTick && ctx.Err() == nil; {
			n, err := s.deliverBatch(ctx)
			if err != nil {
				return quietCancel(ctx, err)
			}
			if n == 0 {
				break
			}
			sent += n
		}
	}
	if now := s.now(); now.Sub(s.lastPrune) >= pruneEvery {
		if err := s.prune(ctx, now); err != nil {
			return quietCancel(ctx, err)
		}
		s.lastPrune = now
	}
	return nil
}

func quietCancel(ctx context.Context, err error) error {
	if ctx.Err() != nil {
		return nil
	}
	return err
}

// reclaimStale returns deliveries and confirmations reserved by a runner
// that died (or fell far behind) to their queue, since none of them was
// sent, and closes sends that started long ago without an outcome: they may
// have arrived, so they are never sent again.
func (s *Service) reclaimStale(ctx context.Context) error {
	now := s.now()
	stmts := []struct {
		query string
		args  []any
	}{
		{`UPDATE status_page_notification_deliveries SET status = 'pending', claim_id = NULL, claimed_at = NULL, updated_at = NOW()
			WHERE status = 'claimed' AND claimed_at < $1`, []any{now.Add(-staleClaimAfter)}},
		{`UPDATE status_page_notification_deliveries SET status = 'uncertain', claim_id = NULL, last_error = $2, updated_at = NOW()
			WHERE status = 'sending' AND claimed_at < $1`, []any{now.Add(-staleSendingAfter), interruptedError}},
		// A confirmation is reserved while confirm_due_at is still set, and
		// sending once it was cleared under the claim.
		{`UPDATE status_page_subscribers SET confirm_claim_id = NULL, confirm_claimed_at = NULL
			WHERE confirm_claim_id IS NOT NULL AND confirm_due_at IS NOT NULL AND confirm_claimed_at < $1`, []any{now.Add(-staleClaimAfter)}},
		{`UPDATE status_page_subscribers SET confirm_claim_id = NULL, confirm_claimed_at = NULL, last_confirm_error = $2
			WHERE confirm_claim_id IS NOT NULL AND confirm_due_at IS NULL AND confirm_claimed_at < $1`, []any{now.Add(-staleSendingAfter), interruptedError}},
	}
	for _, st := range stmts {
		if _, err := s.db.ExecContext(ctx, st.query, st.args...); err != nil {
			return fmt.Errorf("hand back stale subscriber mail: %w", err)
		}
	}
	return nil
}

// token returns a subscriber's token for action.
func (s *Service) token(action string, tenantID, pageID, subscriberID uuid.UUID) (string, error) {
	if s.links == nil {
		return "", actionlink.ErrNotConfigured
	}
	return s.links.ActionToken(action, tenantID.String(), pageID.String(), subscriberID.String(), s.now())
}

// --- Confirmations ---

type claimedConfirmation struct {
	id, tenantID, pageID, claimID uuid.UUID
	email                         string
	attempts                      int
}

// sendConfirmations reserves up to confirmationBatch owed confirmation mails
// and sends them. A confirmation is reserved (claim set, still due), then
// cleared of its due time, committed, right before its send: a crash after
// that never sends it twice, and the visitor can ask again.
func (s *Service) sendConfirmations(ctx context.Context) (int, error) {
	now := s.now()
	claimID := uuid.New()
	rows, err := s.db.QueryContext(ctx, `
		UPDATE status_page_subscribers sub
		SET confirm_claim_id = $3, confirm_claimed_at = $1
		FROM (
			SELECT id FROM status_page_subscribers
			WHERE state = 'pending' AND confirm_due_at <= $1 AND confirm_claim_id IS NULL
			ORDER BY confirm_due_at
			LIMIT $2
			FOR UPDATE SKIP LOCKED
		) c
		WHERE sub.id = c.id
		RETURNING sub.id, sub.tenant_id, sub.status_page_id, sub.email, sub.confirm_attempts
	`, now, confirmationBatch, claimID)
	if err != nil {
		return 0, fmt.Errorf("claim confirmations: %w", err)
	}
	var claimed []claimedConfirmation
	for rows.Next() {
		c := claimedConfirmation{claimID: claimID}
		if err := rows.Scan(&c.id, &c.tenantID, &c.pageID, &c.email, &c.attempts); err != nil {
			rows.Close()
			return 0, fmt.Errorf("scan confirmation: %w", err)
		}
		claimed = append(claimed, c)
	}
	if err := rows.Close(); err != nil {
		return 0, fmt.Errorf("claim confirmations: %w", err)
	}
	defer s.detached(ctx, `UPDATE status_page_subscribers SET confirm_claim_id = NULL, confirm_claimed_at = NULL
		WHERE confirm_claim_id = $1 AND confirm_due_at IS NOT NULL`, claimID)

	for _, c := range claimed {
		if ctx.Err() != nil {
			break
		}
		s.sendConfirmation(ctx, c)
	}
	return len(claimed), nil
}

func (s *Service) sendConfirmation(ctx context.Context, c claimedConfirmation) {
	var slug, title, primary string
	var open bool
	err := s.db.QueryRowContext(ctx, `SELECT sp.slug, sp.title, COALESCE(sp.primary_color, ''),
			(sp.visibility IN ('public', 'unlisted') AND COALESCE(sp.settings->>'enable_email_subscriptions', 'false') = 'true')
		FROM status_pages sp WHERE sp.id = $1 AND sp.tenant_id = $2`, c.pageID, c.tenantID).Scan(&slug, &title, &primary, &open)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return // handed back
	}
	if !open {
		// The page stopped taking subscriptions: owe nothing.
		s.detached(ctx, `UPDATE status_page_subscribers SET confirm_due_at = NULL, confirm_claim_id = NULL, confirm_claimed_at = NULL,
			last_confirm_error = 'the page no longer takes subscriptions' WHERE id = $1 AND confirm_claim_id = $2`, c.id, c.claimID)
		return
	}
	token, err := s.token(actionlink.ActionConfirmSubscription, c.tenantID, c.pageID, c.id)
	if err != nil {
		return // handed back; MailProblems says why
	}
	msg, err := confirmMessage(confirmView{
		PageTitle: title, PageURL: s.pageURL(slug), Accent: primary, Recipient: c.email,
		Confirm: s.pageURL(slug) + SubscriptionPath + "?action=confirm#" + token,
	})
	if err != nil {
		s.detached(ctx, `UPDATE status_page_subscribers SET confirm_due_at = NULL, confirm_claim_id = NULL, last_confirm_error = $3
			WHERE id = $1 AND confirm_claim_id = $2`, c.id, c.claimID, clip(err.Error()))
		return
	}
	// Sending: the due time is cleared under the claim, committed, now.
	err = s.db.QueryRowContext(ctx, `
		UPDATE status_page_subscribers SET confirm_due_at = NULL, confirm_attempts = confirm_attempts + 1, confirm_claimed_at = $3
		WHERE id = $1 AND confirm_claim_id = $2 AND confirm_due_at IS NOT NULL AND state = 'pending'
		RETURNING confirm_attempts
	`, c.id, c.claimID, s.now()).Scan(&c.attempts)
	if err != nil {
		return // confirmed or reclaimed meanwhile
	}
	sendErr := s.mailer.SendMessage(ctx, msg)
	now := s.now()
	switch {
	case sendErr == nil:
		s.detached(ctx, `UPDATE status_page_subscribers SET confirm_claim_id = NULL, confirm_claimed_at = NULL,
			last_confirm_sent_at = $3, last_confirm_error = NULL WHERE id = $1 AND confirm_claim_id = $2`, c.id, c.claimID, now)
	case errors.Is(sendErr, mail.ErrMaybeDelivered):
		s.detached(ctx, `UPDATE status_page_subscribers SET confirm_claim_id = NULL, confirm_claimed_at = NULL,
			last_confirm_error = $3 WHERE id = $1 AND confirm_claim_id = $2`, c.id, c.claimID,
			clip("the connection was lost after the message was sent; it is not sent again: "+sendErr.Error()))
	case ctx.Err() != nil:
		// Stopped before the message was through: owed again, uncounted.
		s.detached(ctx, `UPDATE status_page_subscribers SET confirm_claim_id = NULL, confirm_claimed_at = NULL,
			confirm_due_at = $3, confirm_attempts = confirm_attempts - 1 WHERE id = $1 AND confirm_claim_id = $2`, c.id, c.claimID, now)
	case permanent(sendErr) || c.attempts >= maxConfirmAttempts:
		s.detached(ctx, `UPDATE status_page_subscribers SET confirm_claim_id = NULL, confirm_claimed_at = NULL,
			last_confirm_error = $3 WHERE id = $1 AND confirm_claim_id = $2`, c.id, c.claimID, clip(sendErr.Error()))
	default:
		wait := retryBackoff[min(c.attempts, len(retryBackoff))-1]
		s.detached(ctx, `UPDATE status_page_subscribers SET confirm_claim_id = NULL, confirm_claimed_at = NULL,
			confirm_due_at = $3, last_confirm_error = $4 WHERE id = $1 AND confirm_claim_id = $2`, c.id, c.claimID, now.Add(wait), clip(sendErr.Error()))
	}
}

// --- Planning ---

// outboxRow is a pending outbox row with its page.
type outboxRow struct {
	id, tenantID, pageID uuid.UUID
	kind                 string
	incidentID           uuid.NullUUID
	entryID              uuid.NullUUID
	windowID             uuid.NullUUID
	occurrence           sql.NullTime
	createdAt            time.Time
	slug, title, primary string
	open                 bool
}

// planOne takes the oldest pending outbox row (FOR UPDATE SKIP LOCKED, so
// any number of replicas plan each row once) and, in the same transaction,
// renders its notice and adds one delivery per confirmed subscriber of the
// page, or marks it skipped with the reason. It reports whether a row was
// handled.
func (s *Service) planOne(ctx context.Context) (bool, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return false, fmt.Errorf("begin transaction: %w", err)
	}
	defer tx.Rollback()
	var o outboxRow
	err = tx.QueryRowContext(ctx, `
		SELECT o.id, o.tenant_id, o.status_page_id, o.event_kind, o.incident_id, o.timeline_entry_id,
			o.maintenance_window_id, o.occurrence_starts_at, o.created_at,
			sp.slug, sp.title, COALESCE(sp.primary_color, ''),
			(sp.visibility IN ('public', 'unlisted') AND COALESCE(sp.settings->>'enable_email_subscriptions', 'false') = 'true')
		FROM status_page_notification_outbox o
		JOIN status_pages sp ON sp.id = o.status_page_id AND sp.tenant_id = o.tenant_id
		WHERE o.status = 'pending'
		ORDER BY o.created_at, o.id
		LIMIT 1
		FOR UPDATE OF o SKIP LOCKED
	`).Scan(&o.id, &o.tenantID, &o.pageID, &o.kind, &o.incidentID, &o.entryID, &o.windowID, &o.occurrence, &o.createdAt,
		&o.slug, &o.title, &o.primary, &o.open)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("claim outbox row: %w", err)
	}

	skip := ""
	switch {
	case !s.Ready():
		skip = "subscriber mail is not configured on the API"
	case !o.open:
		skip = "the page takes no email subscriptions (turned off, or the page is password-protected or private)"
	case s.now().Sub(o.createdAt) > maxPlanAge:
		skip = "too old to send"
	}
	var n *notice
	if skip == "" {
		n, skip, err = s.buildNotice(ctx, tx, o)
		if err != nil {
			return false, err
		}
	}
	if skip != "" {
		if _, err := tx.ExecContext(ctx, `UPDATE status_page_notification_outbox SET status = 'skipped', skip_reason = $2, planned_at = $3 WHERE id = $1`,
			o.id, skip, s.now()); err != nil {
			return false, fmt.Errorf("skip outbox row: %w", err)
		}
		return true, tx.Commit()
	}
	content, err := jsonOf(n)
	if err != nil {
		return false, err
	}
	res, err := tx.ExecContext(ctx, `
		INSERT INTO status_page_notification_deliveries (outbox_id, subscriber_id, tenant_id, next_attempt_at)
		SELECT $1, sub.id, sub.tenant_id, $4
		FROM status_page_subscribers sub
		WHERE sub.status_page_id = $2 AND sub.tenant_id = $3 AND sub.state = 'confirmed'
		ON CONFLICT DO NOTHING
	`, o.id, o.pageID, o.tenantID, s.now())
	if err != nil {
		return false, fmt.Errorf("add deliveries: %w", err)
	}
	recipients, _ := res.RowsAffected()
	if _, err := tx.ExecContext(ctx, `
		UPDATE status_page_notification_outbox SET status = 'planned', content = $2, recipients = $3, planned_at = $4 WHERE id = $1
	`, o.id, content, recipients, s.now()); err != nil {
		return false, fmt.Errorf("plan outbox row: %w", err)
	}
	return true, tx.Commit()
}

// --- Deliveries ---

type claimedDelivery struct {
	outboxID, subscriberID, tenantID, claimID uuid.UUID
	attempts                                  int
	email                                     string
}

// deliverBatch reserves due deliveries round-robin across outbox rows, at
// most deliveriesPerOutbox per row, sends them row by row, and before
// returning hands back every row it reserved but did not get to.
func (s *Service) deliverBatch(ctx context.Context) (int, error) {
	now := s.now()
	rows, err := s.db.QueryContext(ctx, `
		SELECT outbox_id FROM status_page_notification_deliveries
		WHERE status = 'pending' AND next_attempt_at <= $1
		GROUP BY outbox_id
		ORDER BY MIN(next_attempt_at)
		LIMIT $2
	`, now, maxOutboxPerBatch)
	if err != nil {
		return 0, fmt.Errorf("list due outbox rows: %w", err)
	}
	var outboxes []uuid.UUID
	for rows.Next() {
		var id uuid.UUID
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return 0, err
		}
		outboxes = append(outboxes, id)
	}
	if err := rows.Close(); err != nil {
		return 0, err
	}
	claimID := uuid.New()
	defer s.detached(ctx, `UPDATE status_page_notification_deliveries SET status = 'pending', claim_id = NULL, claimed_at = NULL, updated_at = NOW()
		WHERE claim_id = $1 AND status = 'claimed'`, claimID)
	groups := map[uuid.UUID][]claimedDelivery{}
	total := 0
	for _, outboxID := range outboxes {
		rows, err := s.db.QueryContext(ctx, `
			UPDATE status_page_notification_deliveries d
			SET status = 'claimed', claim_id = $3, claimed_at = $1, updated_at = NOW()
			FROM (
				SELECT outbox_id, subscriber_id FROM status_page_notification_deliveries
				WHERE outbox_id = $4 AND status = 'pending' AND next_attempt_at <= $1
				ORDER BY next_attempt_at, subscriber_id
				LIMIT $2
				FOR UPDATE SKIP LOCKED
			) c
			WHERE d.outbox_id = c.outbox_id AND d.subscriber_id = c.subscriber_id
			RETURNING d.outbox_id, d.subscriber_id, d.tenant_id, d.attempts,
				(SELECT email FROM status_page_subscribers WHERE id = d.subscriber_id)
		`, now, deliveriesPerOutbox, claimID, outboxID)
		if err != nil {
			return total, fmt.Errorf("claim deliveries: %w", err)
		}
		for rows.Next() {
			d := claimedDelivery{claimID: claimID}
			var email sql.NullString
			if err := rows.Scan(&d.outboxID, &d.subscriberID, &d.tenantID, &d.attempts, &email); err != nil {
				rows.Close()
				return total, fmt.Errorf("scan delivery: %w", err)
			}
			d.email = email.String
			groups[outboxID] = append(groups[outboxID], d)
			total++
		}
		if err := rows.Close(); err != nil {
			return total, err
		}
	}
	for _, outboxID := range outboxes {
		if ctx.Err() != nil {
			break
		}
		if g := groups[outboxID]; len(g) > 0 {
			s.sendGroup(ctx, outboxID, g)
		}
	}
	return total, nil
}

// sendGroup sends one outbox row's notice to its reserved subscribers.
// Errors end up on the rows, never returned.
func (s *Service) sendGroup(ctx context.Context, outboxID uuid.UUID, group []claimedDelivery) {
	var raw []byte
	var pageID, tenantID uuid.UUID
	var open bool
	err := s.db.QueryRowContext(ctx, `
		SELECT o.content, o.status_page_id, o.tenant_id,
			(sp.visibility IN ('public', 'unlisted') AND COALESCE(sp.settings->>'enable_email_subscriptions', 'false') = 'true')
		FROM status_page_notification_outbox o
		JOIN status_pages sp ON sp.id = o.status_page_id
		WHERE o.id = $1
	`, outboxID).Scan(&raw, &pageID, &tenantID, &open)
	if err != nil {
		return // deleted meanwhile (cascade), or handed back
	}
	var n notice
	if err := jsonInto(raw, &n); err != nil {
		for _, d := range group {
			s.update(ctx, d, "claimed", `status = 'failed', claim_id = NULL, last_error = $4`, clip("unreadable notice: "+err.Error()))
		}
		return
	}
	// A few sends at once: each message opens its own SMTP session (its
	// List-Unsubscribe header is the subscriber's own), so a big page would
	// otherwise take one round trip per subscriber in a row.
	var g errgroup.Group
	g.SetLimit(max(1, s.sendConcurrency))
	for _, d := range group {
		switch {
		case ctx.Err() != nil:
			continue // handed back
		case !open:
			s.update(ctx, d, "claimed", `status = 'skipped', claim_id = NULL, last_error = 'the page takes no email subscriptions now'`)
			continue
		case d.email == "":
			continue // unsubscribed meanwhile; the row went with them
		}
		g.Go(func() error {
			s.sendOne(ctx, n, pageID, tenantID, d)
			return nil
		})
	}
	_ = g.Wait()
}

func (s *Service) sendOne(ctx context.Context, n notice, pageID, tenantID uuid.UUID, d claimedDelivery) {
	token, err := s.token(actionlink.ActionUnsubscribePage, tenantID, pageID, d.subscriberID)
	if err != nil {
		return // handed back; MailProblems says why
	}
	unsubscribePage := n.PageURL + SubscriptionPath + "?action=unsubscribe#" + token
	oneClick := s.appBaseURL + OneClickUnsubscribePath + "?token=" + token
	msg, err := noticeMessage(n, d.email, unsubscribePage, oneClick)
	if err != nil {
		s.update(ctx, d, "claimed", `status = 'failed', claim_id = NULL, last_error = $4`, clip(err.Error()))
		return
	}
	// Sending: committed immediately before the message goes out, and only
	// while this batch still holds the row.
	err = s.db.QueryRowContext(ctx, `
		UPDATE status_page_notification_deliveries SET status = 'sending', attempts = attempts + 1, claimed_at = $4, updated_at = NOW()
		WHERE outbox_id = $1 AND subscriber_id = $2 AND claim_id = $3 AND status = 'claimed'
		RETURNING attempts
	`, d.outboxID, d.subscriberID, d.claimID, s.now()).Scan(&d.attempts)
	if err != nil {
		return
	}
	sendErr := s.mailer.SendMessage(ctx, msg)
	now := s.now()
	switch {
	case sendErr == nil:
		s.update(ctx, d, "sending", `status = 'sent', claim_id = NULL, sent_at = $4, last_error = NULL`, now)
	case errors.Is(sendErr, mail.ErrMaybeDelivered):
		s.update(ctx, d, "sending", `status = 'uncertain', claim_id = NULL, last_error = $4`,
			clip("the connection was lost after the message was sent; it may have arrived, so it is not sent again: "+sendErr.Error()))
	case ctx.Err() != nil:
		// Stopped before the message was through: back, uncounted.
		s.update(ctx, d, "sending", `status = 'pending', claim_id = NULL, claimed_at = NULL, attempts = attempts - 1`)
	case permanent(sendErr) || d.attempts >= maxAttempts:
		s.update(ctx, d, "sending", `status = 'failed', claim_id = NULL, last_error = $4`, clip(sendErr.Error()))
	default:
		wait := retryBackoff[min(d.attempts, len(retryBackoff))-1]
		s.update(ctx, d, "sending", `status = 'pending', claim_id = NULL, next_attempt_at = $4, last_error = $5`, now.Add(wait), clip(sendErr.Error()))
	}
}

// permanent reports failures a retry cannot fix: a message that can never
// be sent, or a relay that refused it outright (5xx).
func permanent(err error) bool {
	if errors.Is(err, mail.ErrInvalidMessage) {
		return true
	}
	var reply *textproto.Error
	return errors.As(err, &reply) && reply.Code >= 500
}

// update writes one delivery's outcome, detached from the pass's context so
// a pass cut short still records what it did, and only while the row is in
// the state this batch left it in (from) under its claim.
func (s *Service) update(ctx context.Context, d claimedDelivery, from, set string, args ...any) {
	all := append([]any{d.outboxID, d.subscriberID, d.claimID}, args...)
	next := len(all) + 1
	all = append(all, from)
	s.detached(ctx, fmt.Sprintf(`UPDATE status_page_notification_deliveries SET %s, updated_at = NOW()
		WHERE outbox_id = $1 AND subscriber_id = $2 AND claim_id = $3 AND status = $%d`, set, next), all...)
}

// detached runs a bookkeeping statement on a context that outlives the
// pass's cancellation.
func (s *Service) detached(ctx context.Context, query string, args ...any) {
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), recordTimeout)
	defer cancel()
	if _, err := s.db.ExecContext(ctx, query, args...); err != nil && s.log != nil {
		s.log.WithError(err).Error("Failed to record subscriber mail progress")
	}
}

func clip(v string) string {
	v = strings.TrimSpace(v)
	if len(v) <= maxErrorLen {
		return v
	}
	return strings.ToValidUTF8(v[:maxErrorLen], "") + "..."
}

// --- Pruning ---

// prune drops unconfirmed addresses a week after their last request, and
// finished outbox rows (with their deliveries) after outboxRetention.
func (s *Service) prune(ctx context.Context, now time.Time) error {
	if _, err := s.db.ExecContext(ctx, `
		DELETE FROM status_page_subscribers WHERE state = 'pending' AND requested_at < $1 AND confirm_claim_id IS NULL
	`, now.Add(-PendingRetention)); err != nil {
		return fmt.Errorf("prune unconfirmed subscribers: %w", err)
	}
	if _, err := s.db.ExecContext(ctx, `
		DELETE FROM status_page_notification_outbox o
		WHERE o.created_at < $1 AND o.status <> 'pending'
		  AND NOT EXISTS (SELECT 1 FROM status_page_notification_deliveries d
			WHERE d.outbox_id = o.id AND d.status IN ('pending', 'claimed', 'sending'))
	`, now.Add(-outboxRetention)); err != nil {
		return fmt.Errorf("prune notification outbox: %w", err)
	}
	return nil
}

// refreshPages tells the status-page service the pages changed (re-render;
// this also wakes its push sender above its early return).
func (s *Service) refreshPages(tenantID uuid.UUID, pageIDs []uuid.UUID) {
	if s.publisher == nil {
		return
	}
	for _, id := range pageIDs {
		_ = s.publisher.Publish(statusupdates.Event{
			Type:         "incident.publication.updated",
			StatusPageID: id.String(),
			TenantID:     tenantID.String(),
			Timestamp:    s.now(),
		})
	}
}

// --- Runner ---

// Runner runs RunOnce on an interval, woken early when a confirmation is
// owed. Every API replica runs one; SKIP LOCKED claims, the deliveries
// committed before sending and the per-delivery states keep them from
// mailing anyone twice.
type Runner struct {
	svc      *Service
	interval time.Duration
	stop     chan struct{}
	wg       sync.WaitGroup
	once     sync.Once
}

// NewRunner wraps the service in a background loop.
func NewRunner(svc *Service) *Runner {
	return &Runner{svc: svc, interval: runInterval, stop: make(chan struct{})}
}

// Start begins the loop. It always runs: automatic outage publishing needs
// no mail. Without mail configured it logs why subscriber mail is off, and
// planning skips what the outbox holds.
func (r *Runner) Start() {
	if problems := r.svc.MailProblems(); len(problems) > 0 && r.svc.log != nil {
		r.svc.log.WithField("reasons", strings.Join(problems, " ")).Info("Status page subscriber mail is off")
	}
	r.wg.Add(1)
	go r.run()
}

// Stop ends the loop and waits for a pass in progress: sends in flight are
// cut short and recorded, and every reserved row is handed back.
func (r *Runner) Stop() {
	r.once.Do(func() { close(r.stop) })
	r.wg.Wait()
}

func (r *Runner) run() {
	defer r.wg.Done()
	ticker := time.NewTicker(r.interval)
	defer ticker.Stop()
	for {
		r.tick()
		select {
		case <-r.stop:
			return
		case <-ticker.C:
		case <-r.svc.wake:
		}
	}
}

func (r *Runner) tick() {
	ctx, cancel := context.WithTimeout(context.Background(), tickTimeout)
	defer cancel()
	go func() {
		select {
		case <-r.stop:
			cancel()
		case <-ctx.Done():
		}
	}()
	if err := r.svc.RunOnce(ctx); err != nil && !isMissingSchema(err) && r.svc.log != nil {
		r.svc.log.WithError(err).Error("Failed to run status page subscriber mail")
	}
}

// isMissingSchema: during a rolling upgrade an API may start before its
// migration ran; the next tick finds the tables.
func isMissingSchema(err error) bool {
	var pqErr *pq.Error
	return errors.As(err, &pqErr) && (pqErr.Code == "42P01" || pqErr.Code == "42703")
}
