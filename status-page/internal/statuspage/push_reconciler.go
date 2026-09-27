package statuspage

import (
	"context"
	"fmt"
	"time"

	"github.com/yassinebenameur/probara/shared/maintenance"
)

// Push notification timing. These are the knobs that decide what a visitor
// actually receives; each one is a deliberate trade-off, not a tuning default.
const (
	// pushNotificationHorizon bounds how far back the reconciler will look.
	// If the sender has been down longer than this, those transitions are
	// dropped rather than delivered stale -- a notification saying a service
	// is down forty minutes after it recovered is worse than no notification.
	pushNotificationHorizon = 15 * time.Minute

	// pushFlapDebounce is how long a transition must persist before it is
	// eligible. A monitor that bounces back inside this window never creates
	// a ledger row at all.
	//
	// Suppressing at creation rather than cancelling later is what keeps the
	// recovery side symmetric for free: a suppressed down has no row, so its
	// recovery has no row to answer, with no "announced vs claimed" state to
	// track and no race against a recovery arriving mid-send.
	pushFlapDebounce = 60 * time.Second
)

// reconcileNotifications turns recent monitor state transitions into pending
// delivery rows.
//
// The trigger source is monitor_state_intervals, not the statuspage.updates
// NATS subject, because the event stream cannot carry this reliably:
//
//   - push, agent and OTLP monitors discard the Transition from
//     monitorstate.Record and publish "check_result" unconditionally, so the
//     watchdog announces their down but nothing announces their up;
//   - the subject is fire-and-forget core NATS with no redelivery, so a
//     dropped message is a permanently missed notification.
//
// The timeline has neither problem: every transition path writes it inside
// the transition's own transaction, and its UUID primary key is a
// per-transition identity written exactly once. That id is the ledger's
// dedup key, which is what makes this safe to run on every replica
// concurrently -- both run the identical INSERT and the unique index makes
// the loser a no-op. No leader election, no advisory lock.
func (s *pushStore) reconcileNotifications(ctx context.Context) (int64, error) {
	query := `
		WITH candidate AS (
			SELECT
				i.id           AS interval_id,
				i.monitor_id,
				i.state,
				-- The previous state that a visitor was actually told about.
				-- Skipping over suspect/degraded/unknown here is what makes
				-- up->suspect->down one notification, down->suspect->down
				-- none, and down->degraded->up a single recovery.
				(
					SELECT prev.state
					FROM monitor_state_intervals prev
					WHERE prev.monitor_id = i.monitor_id
					  AND prev.state IN ('down', 'up')
					  AND prev.started_at < i.started_at
					ORDER BY prev.started_at DESC
					LIMIT 1
				) AS previous_state
			FROM monitor_state_intervals i
			JOIN monitors m ON m.id = i.monitor_id
			WHERE i.state IN ('down', 'up')
			  -- Only real observations trigger. This also excludes the
			  -- reason='seed' rows migration 000082 wrote for every existing
			  -- monitor, which would otherwise fire a notification storm on
			  -- first deploy.
			  AND i.reason IN ('result', 'watchdog_stale')
			  AND i.created_at > NOW() - make_interval(secs => $1)
			  AND i.started_at < NOW() - make_interval(secs => $2)
			  AND m.deleted_at IS NULL
			  AND m.enabled = TRUE
			  AND NOT ` + maintenance.InMaintenancePredicate("m") + `
		),
		notifiable AS (
			SELECT
				interval_id,
				monitor_id,
				CASE WHEN state = 'down' THEN 'down' ELSE 'recovered' END AS kind
			FROM candidate
			WHERE (state = 'down' AND (previous_state IS NULL OR previous_state <> 'down'))
			   OR (state = 'up'   AND previous_state = 'down')
		),
		-- What the page actually renders. Deliberately NOT the recursive
		-- group walk used for cache invalidation: that resolves upward
		-- through monitor_groups, so it would push a member's name to a page
		-- that only lists the parent group.
		page_monitor AS (
			SELECT sps.status_page_id, spsm.monitor_id,
			       COALESCE(NULLIF(spsm.display_name, ''), m.name) AS label
			FROM status_page_sections sps
			JOIN status_page_section_monitors spsm ON spsm.section_id = sps.id
			JOIN monitors m ON m.id = spsm.monitor_id
			UNION
			SELECT spm.status_page_id, spm.monitor_id,
			       COALESCE(NULLIF(spm.display_name, ''), m.name) AS label
			FROM status_page_monitors spm
			JOIN monitors m ON m.id = spm.monitor_id
		)
		INSERT INTO status_page_push_deliveries
			(status_page_id, monitor_id, interval_id, kind, monitor_name)
		SELECT DISTINCT ON (pm.status_page_id, n.interval_id)
			pm.status_page_id, n.monitor_id, n.interval_id, n.kind, pm.label
		FROM notifiable n
		JOIN page_monitor pm ON pm.monitor_id = n.monitor_id
		JOIN status_pages sp ON sp.id = pm.status_page_id
		WHERE COALESCE(sp.settings->>'enable_push_notifications', 'false') = 'true'
		  -- No subscribers, no work. This also bounds the blast radius when
		  -- an operator first enables the setting on a busy page.
		  AND EXISTS (
			  SELECT 1 FROM status_page_push_subscriptions subs
			  WHERE subs.status_page_id = pm.status_page_id
		  )
		ORDER BY pm.status_page_id, n.interval_id
		ON CONFLICT (status_page_id, interval_id) DO NOTHING
	`

	res, err := s.db.ExecContext(ctx, query,
		pushNotificationHorizon.Seconds(),
		pushFlapDebounce.Seconds(),
	)
	if err != nil {
		return 0, fmt.Errorf("reconcile push notifications: %w", err)
	}
	n, _ := res.RowsAffected()
	return n, nil
}

// pruneDeliveries drops delivery rows past their retention. The ledger only
// needs to remember long enough to deduplicate against the reconciler's own
// lookback window; a week is generous.
func (s *pushStore) pruneDeliveries(ctx context.Context, olderThan time.Duration) (int64, error) {
	res, err := s.db.ExecContext(ctx,
		`DELETE FROM status_page_push_deliveries WHERE created_at < NOW() - make_interval(secs => $1)`,
		olderThan.Seconds())
	if err != nil {
		return 0, fmt.Errorf("prune push deliveries: %w", err)
	}
	n, _ := res.RowsAffected()
	return n, nil
}
