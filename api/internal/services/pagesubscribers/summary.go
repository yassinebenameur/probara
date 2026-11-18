package pagesubscribers

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"

	"github.com/google/uuid"

	"github.com/yassinebenameur/probara/api/internal/models"
)

// recentOutcomes is how many notices the summary reports.
const recentOutcomes = 10

// Summary is what any member may read about the page's subscribers: the
// counts, whether mail can go out, and what the latest notices came to, so
// mail lost to an SMTP outage or skipped for lack of configuration shows.
func (s *Service) Summary(ctx context.Context, tenantID, pageID uuid.UUID) (*models.StatusPageSubscriberSummary, error) {
	if err := s.ensurePage(ctx, tenantID, pageID); err != nil {
		return nil, err
	}
	out := &models.StatusPageSubscriberSummary{
		Limit: MaxSubscribersPerPage, MailProblems: s.SubscriptionProblems(), Recent: []models.StatusPageNotificationOutcome{},
	}
	out.MailReady = len(out.MailProblems) == 0
	var err error
	if out.Counts, err = s.counts(ctx, tenantID, pageID); err != nil {
		return nil, err
	}
	rows, err := s.db.QueryContext(ctx, `
		SELECT o.id, o.event_kind, o.created_at, o.status, COALESCE(o.skip_reason, ''), o.recipients, o.content,
			COUNT(d.*) FILTER (WHERE d.status = 'sent'),
			COUNT(d.*) FILTER (WHERE d.status IN ('pending', 'claimed', 'sending')),
			COUNT(d.*) FILTER (WHERE d.status = 'failed'),
			COUNT(d.*) FILTER (WHERE d.status = 'uncertain'),
			COUNT(d.*) FILTER (WHERE d.status = 'skipped'),
			COALESCE(MAX(d.last_error) FILTER (WHERE d.status IN ('failed', 'uncertain')), '')
		FROM (
			SELECT * FROM status_page_notification_outbox
			WHERE tenant_id = $1 AND status_page_id = $2
			ORDER BY created_at DESC, id DESC
			LIMIT $3
		) o
		LEFT JOIN status_page_notification_deliveries d ON d.outbox_id = o.id
		GROUP BY o.id, o.event_kind, o.created_at, o.status, o.skip_reason, o.recipients, o.content
		ORDER BY o.created_at DESC, o.id DESC
	`, tenantID, pageID, recentOutcomes)
	if err != nil {
		return nil, fmt.Errorf("list recent notices: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var o models.StatusPageNotificationOutcome
		var content []byte
		if err := rows.Scan(&o.ID, &o.Kind, &o.CreatedAt, &o.Status, &o.SkipReason, &o.Recipients, &content,
			&o.Sent, &o.Waiting, &o.Failed, &o.Uncertain, &o.Skipped, &o.LastError); err != nil {
			return nil, fmt.Errorf("scan notice: %w", err)
		}
		if len(content) > 0 {
			var n notice
			if json.Unmarshal(content, &n) == nil {
				o.Subject = n.Label + ": " + n.Headline
			}
		}
		out.Recent = append(out.Recent, o)
	}
	return out, rows.Err()
}

// confirmedSubscribers counts a page's confirmed subscribers in q.
func confirmedSubscribers(ctx context.Context, q queryer, pageID uuid.UUID) (int, error) {
	var n int
	err := q.QueryRowContext(ctx, `SELECT COUNT(*) FROM status_page_subscribers WHERE status_page_id = $1 AND state = 'confirmed'`, pageID).Scan(&n)
	if err != nil && err != sql.ErrNoRows {
		return 0, err
	}
	return n, nil
}
