package statuspage

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/google/uuid"
)

// pushStore is the status-page service's ONLY write path to Postgres.
//
// The Service deliberately takes a db.Querier (QueryContext/QueryRowContext
// only, shared/db/interfaces.go) and even stubs ExecContext into a refusal so
// a stray write cannot compile -- see metricstoreQuerier in service.go. That
// invariant is worth keeping: everything a public page renders is derived
// state that some other service owns.
//
// Visitor push subscriptions break it, because the subscription is created by
// the visitor's browser talking to this service and to nothing else. Rather
// than widen Querier and lose the guarantee everywhere, the write path is
// confined to this one type, which is the only place in the package holding a
// *sql.DB.
type pushStore struct {
	db *sql.DB
}

func newPushStore(db *sql.DB) *pushStore {
	return &pushStore{db: db}
}

// pushSubscriptionRow is one browser's subscription to one status page.
type pushSubscriptionRow struct {
	ID       uuid.UUID
	Endpoint string
	P256dh   string
	Auth     string
}

// Subscribe stores a browser subscription, or refreshes an existing one.
//
// Re-subscribing is the normal path, not an error: the page re-posts roughly
// once a day so last_seen_at stays fresh for the stale sweep, and the browser
// may hand back the same endpoint with rotated keys. failure_count resets
// because a subscription the browser just vouched for is healthy regardless
// of what happened to it before.
//
// The per-page cap is enforced inside the statement rather than as a
// read-then-write, so two concurrent subscribes cannot both observe a count
// below the cap and both insert.
func (s *pushStore) Subscribe(ctx context.Context, pageID uuid.UUID, sub pushSubscriptionRow, userAgent string, cap int) (bool, error) {
	const query = `
		INSERT INTO status_page_push_subscriptions
			(status_page_id, endpoint, p256dh, auth, user_agent)
		SELECT $1, $2, $3, $4, NULLIF($5, '')
		WHERE (
			SELECT COUNT(*) FROM status_page_push_subscriptions WHERE status_page_id = $1
		) < $6
		ON CONFLICT (status_page_id, endpoint) DO UPDATE
		SET p256dh        = EXCLUDED.p256dh,
			auth          = EXCLUDED.auth,
			user_agent    = COALESCE(EXCLUDED.user_agent, status_page_push_subscriptions.user_agent),
			last_seen_at  = NOW(),
			failure_count = 0
		RETURNING id
	`
	var id uuid.UUID
	err := s.db.QueryRowContext(ctx, query, pageID, sub.Endpoint, sub.P256dh, sub.Auth, userAgent, cap).Scan(&id)
	if err == sql.ErrNoRows {
		// The WHERE suppressed the insert: the page is at its cap. ON CONFLICT
		// never runs for a suppressed row, so an existing subscriber
		// re-posting at a full page also lands here -- acceptable, because the
		// refresh is only a liveness touch.
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("store push subscription: %w", err)
	}
	return true, nil
}

// Unsubscribe removes one browser's subscription. Deleting a row that is not
// there is success: the caller's contract is "this endpoint is not subscribed
// afterwards", and reporting a 404 would tell an unauthenticated caller
// whether an endpoint exists.
func (s *pushStore) Unsubscribe(ctx context.Context, pageID uuid.UUID, endpoint string) error {
	const query = `
		DELETE FROM status_page_push_subscriptions
		WHERE status_page_id = $1 AND endpoint = $2
	`
	if _, err := s.db.ExecContext(ctx, query, pageID, endpoint); err != nil {
		return fmt.Errorf("remove push subscription: %w", err)
	}
	return nil
}

// DeleteSubscription removes a subscription the push service reported as gone.
// Only 404 and 410 may reach here -- see pushSender.
func (s *pushStore) DeleteSubscription(ctx context.Context, id uuid.UUID) error {
	if _, err := s.db.ExecContext(ctx,
		`DELETE FROM status_page_push_subscriptions WHERE id = $1`, id); err != nil {
		return fmt.Errorf("delete dead push subscription: %w", err)
	}
	return nil
}

// PageIDForSlug resolves a public slug to its page id, and reports whether the
// page has opted into browser notifications. Both come from one query so the
// subscribe handler cannot act on a stale or half-read page.
func (s *pushStore) PageIDForSlug(ctx context.Context, slug string) (uuid.UUID, bool, error) {
	const query = `
		SELECT id, COALESCE(settings->>'enable_push_notifications', 'false') = 'true'
		FROM status_pages
		WHERE slug = $1
	`
	var id uuid.UUID
	var enabled bool
	switch err := s.db.QueryRowContext(ctx, query, slug).Scan(&id, &enabled); {
	case err == sql.ErrNoRows:
		return uuid.UUID{}, false, nil
	case err != nil:
		return uuid.UUID{}, false, fmt.Errorf("resolve status page for push: %w", err)
	}
	return id, enabled, nil
}

// RecordSendResult updates a subscription's health after a delivery attempt.
func (s *pushStore) RecordSendResult(ctx context.Context, id uuid.UUID, ok bool) error {
	const query = `
		UPDATE status_page_push_subscriptions
		SET last_success_at = CASE WHEN $2 THEN NOW() ELSE last_success_at END,
			failure_count   = CASE WHEN $2 THEN 0 ELSE failure_count + 1 END
		WHERE id = $1
	`
	if _, err := s.db.ExecContext(ctx, query, id, ok); err != nil {
		return fmt.Errorf("record push send result: %w", err)
	}
	return nil
}

// PruneSubscriptions drops subscriptions that are dead or abandoned.
//
// This is a backstop, not the primary cleanup: the authoritative signal is a
// 404/410 from the push service, handled at send time. failureCap catches
// endpoints that fail some other way forever, and staleAfter catches browsers
// that simply stopped visiting -- the page refreshes last_seen_at about once
// a day while a visitor still has the subscription.
func (s *pushStore) PruneSubscriptions(ctx context.Context, failureCap int, staleAfter time.Duration) (int64, error) {
	const query = `
		DELETE FROM status_page_push_subscriptions
		WHERE failure_count >= $1
		   OR last_seen_at < NOW() - make_interval(secs => $2)
	`
	res, err := s.db.ExecContext(ctx, query, failureCap, staleAfter.Seconds())
	if err != nil {
		return 0, fmt.Errorf("prune push subscriptions: %w", err)
	}
	n, _ := res.RowsAffected()
	return n, nil
}
