package pagesubscribers

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/lib/pq"

	"github.com/yassinebenameur/probara/api/internal/services/pagenotify"
	"github.com/yassinebenameur/probara/shared/maintenance"
	"github.com/yassinebenameur/probara/shared/statuspagesql"
)

// maintenanceStartWindow: a one-off occurrence that started longer ago than
// this when the sweep first sees it (the API was down) is not announced as
// starting.
const maintenanceStartWindow = 15 * time.Minute

// queryer is the read side of a transaction.
type queryer interface {
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}

func jsonOf(n *notice) ([]byte, error) {
	raw, err := json.Marshal(n)
	if err != nil {
		return nil, fmt.Errorf("encode notice: %w", err)
	}
	return raw, nil
}

func jsonInto(raw []byte, n *notice) error {
	if len(raw) == 0 {
		return errors.New("empty notice")
	}
	return json.Unmarshal(raw, n)
}

// timeText is how a mail prints an instant: UTC, spelled out, since the
// reader's zone is unknown (the page shows local time).
func timeText(t time.Time) string {
	return t.UTC().Format("Mon 2 Jan 2006 15:04 UTC")
}

var incidentStates = map[string]string{
	"investigating": "Investigating", "identified": "Identified", "monitoring": "Monitoring", "resolved": "Resolved",
}

// buildNotice renders the outbox row's event as the page shows it. A
// non-empty skip says why the row is not mailed (the event is no longer on
// the page).
func (s *Service) buildNotice(ctx context.Context, q queryer, o outboxRow) (*notice, string, error) {
	n := &notice{Kind: o.kind, PageTitle: o.title, PageURL: s.publicPageURL(pageRef{id: o.pageID, tenantID: o.tenantID, slug: o.slug}), Accent: accent(o.primary)}
	switch o.kind {
	case pagenotify.KindIncidentPublished, pagenotify.KindIncidentUpdated, pagenotify.KindIncidentResolved:
		return s.incidentNotice(ctx, q, o, n)
	case pagenotify.KindMaintenanceScheduled, pagenotify.KindMaintenanceStarted:
		return s.maintenanceNotice(ctx, q, o, n)
	case pagenotify.KindMaintenanceCancelled:
		return s.cancellationNotice(ctx, q, o, n)
	}
	return nil, "unknown event kind " + o.kind, nil
}

func (s *Service) incidentNotice(ctx context.Context, q queryer, o outboxRow, n *notice) (*notice, string, error) {
	// What the page shows: the publication's public title and summary when
	// it has them (always, for an automatically created incident, whose own
	// title names the internal monitor), else the incident's own.
	var title, summary, state string
	var resolvedAt sql.NullTime
	err := q.QueryRowContext(ctx, `
		SELECT COALESCE(isp.public_title, i.title), COALESCE(isp.public_summary, i.summary, ''), i.state::text, i.resolved_at
		FROM incidents i
		JOIN incident_status_page_publications isp
			ON isp.incident_id = i.id AND isp.status_page_id = $2 AND isp.unpublished_at IS NULL
		WHERE i.id = $1 AND i.tenant_id = $3
	`, o.incidentID.UUID, o.pageID, o.tenantID).Scan(&title, &summary, &state, &resolvedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, "the incident is no longer on the page", nil
	}
	if err != nil {
		return nil, "", fmt.Errorf("load incident: %w", err)
	}
	affected, err := names(ctx, q, `
		SELECT DISTINCT `+statuspagesql.PublicNameSQL("$2", "m")+`
		FROM incident_status_page_monitors ispm
		JOIN monitors m ON m.id = ispm.monitor_id AND m.deleted_at IS NULL
		WHERE ispm.incident_id = $1 AND ispm.status_page_id = $2
	`, o.incidentID.UUID, o.pageID)
	if err != nil {
		return nil, "", err
	}
	n.Headline = oneLine(title)
	stateLabel := incidentStates[state]
	if stateLabel == "" {
		stateLabel = state
	}
	switch o.kind {
	case pagenotify.KindIncidentPublished:
		n.Label = "Incident"
		n.Body = strings.TrimSpace(summary)
		n.Facts = append(n.Facts, fact{"Status", stateLabel})
	case pagenotify.KindIncidentUpdated:
		n.Label = "Update"
		var message string
		if o.entryID.Valid {
			err := q.QueryRowContext(ctx, `SELECT message FROM incident_timeline_entries WHERE id = $1 AND incident_id = $2`,
				o.entryID.UUID, o.incidentID.UUID).Scan(&message)
			if errors.Is(err, sql.ErrNoRows) {
				return nil, "the update was deleted", nil
			}
			if err != nil {
				return nil, "", fmt.Errorf("load incident update: %w", err)
			}
		}
		n.Body = strings.TrimSpace(message)
		n.Facts = append(n.Facts, fact{"Status", stateLabel})
	case pagenotify.KindIncidentResolved:
		n.Label = "Resolved"
		n.Body = "This incident has been resolved."
		if resolvedAt.Valid {
			n.Facts = append(n.Facts, fact{"Resolved", timeText(resolvedAt.Time)})
		}
	}
	if len(affected) > 0 {
		n.Facts = append(n.Facts, fact{"Affected", strings.Join(affected, ", ")})
	}
	return n, "", nil
}

// maintenanceNotice describes the window at the occurrence it announces:
// for a scheduled row the next one ahead that covers a monitor the page
// shows, for a started row the occurrence that started. Coverage comes only
// from the shared occurrence fragment.
func (s *Service) maintenanceNotice(ctx context.Context, q queryer, o outboxRow, n *notice) (*notice, string, error) {
	var title, description string
	var deleted bool
	var sched maintenance.ScheduleScanner
	err := q.QueryRowContext(ctx, `
		SELECT mw.title, COALESCE(mw.description, ''), mw.deleted_at IS NOT NULL, `+maintenance.ScheduleColumns("mw")+`
		FROM maintenance_windows mw WHERE mw.id = $1 AND mw.tenant_id = $2
	`, o.windowID.UUID, o.tenantID).Scan(append([]any{&title, &description, &deleted}, sched.Dest()...)...)
	if errors.Is(err, sql.ErrNoRows) || (err == nil && deleted) {
		return nil, "the maintenance window was deleted", nil
	}
	if err != nil {
		return nil, "", fmt.Errorf("load maintenance window: %w", err)
	}
	schedule, err := sched.Schedule()
	if err != nil {
		return nil, "", err
	}

	// The occurrence: its start, its latest covered end, and the page's
	// components it covers.
	occurrenceCond := `mo.occurrence_starts_at > NOW()`
	args := []any{o.pageID, o.tenantID, o.windowID.UUID}
	if o.kind == pagenotify.KindMaintenanceStarted {
		occurrenceCond = `mo.occurrence_starts_at = $4`
		args = append(args, o.occurrence.Time)
	}
	var start, end sql.NullTime
	var affected []string
	err = q.QueryRowContext(ctx, `
		WITH covered AS (
			SELECT mo.occurrence_starts_at, mo.ends_at, `+statuspagesql.PublicNameSQL("$1", "m")+` AS name
			FROM `+statuspagesql.RenderedMonitorsSQL+` rm
			JOIN monitors m ON m.id = rm.monitor_id AND m.tenant_id = $2 AND m.deleted_at IS NULL
			CROSS JOIN LATERAL `+maintenance.OccurrenceRangesSQL("$2::uuid", "m.id", "NOW() - INTERVAL '1 day'", "'infinity'::timestamptz")+` mo
			WHERE rm.status_page_id = $1 AND mo.window_id = $3 AND `+occurrenceCond+`
		), first AS (
			SELECT MIN(occurrence_starts_at) AS start FROM covered
		)
		SELECT f.start, MAX(c.ends_at), COALESCE(array_agg(DISTINCT c.name) FILTER (WHERE c.name IS NOT NULL), '{}')
		FROM first f LEFT JOIN covered c ON c.occurrence_starts_at = f.start
		GROUP BY f.start
	`, args...).Scan(&start, &end, pq.Array(&affected))
	if err != nil {
		return nil, "", fmt.Errorf("load maintenance occurrence: %w", err)
	}
	if !start.Valid {
		return nil, "the window no longer covers a component of the page", nil
	}

	n.Headline = oneLine(title)
	n.Body = strings.TrimSpace(description)
	if o.kind == pagenotify.KindMaintenanceStarted {
		n.Label = "Maintenance started"
	} else {
		n.Label = "Scheduled maintenance"
		var earlier bool
		if err := q.QueryRowContext(ctx, `
			SELECT EXISTS (SELECT 1 FROM status_page_notification_outbox
				WHERE status_page_id = $1 AND maintenance_window_id = $2 AND event_kind = $3 AND created_at < $4)
		`, o.pageID, o.windowID.UUID, pagenotify.KindMaintenanceScheduled, o.createdAt).Scan(&earlier); err != nil {
			return nil, "", fmt.Errorf("check earlier maintenance notice: %w", err)
		}
		if earlier {
			n.Label = "Maintenance rescheduled"
		}
	}
	n.Facts = append(n.Facts, fact{"Starts", timeText(start.Time)})
	if end.Valid {
		n.Facts = append(n.Facts, fact{"Ends", timeText(end.Time)})
	}
	if schedule.Repeat != nil {
		n.Facts = append(n.Facts, fact{"Repeats", repeatText(*schedule.Repeat)})
	}
	if len(affected) > 0 {
		n.Facts = append(n.Facts, fact{"Affected", strings.Join(affected, ", ")})
	}
	return n, "", nil
}

// cancellationNotice withdraws an announced window: its title, and the
// occurrence that will not run (or was stopped early). Nothing about its
// coverage is read: a deleted window covers nothing any more.
func (s *Service) cancellationNotice(ctx context.Context, q queryer, o outboxRow, n *notice) (*notice, string, error) {
	var title string
	err := q.QueryRowContext(ctx, `SELECT title FROM maintenance_windows WHERE id = $1 AND tenant_id = $2`,
		o.windowID.UUID, o.tenantID).Scan(&title)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, "the maintenance window is gone", nil
	}
	if err != nil {
		return nil, "", fmt.Errorf("load maintenance window: %w", err)
	}
	n.Headline = oneLine(title)
	if o.occurrence.Valid && o.occurrence.Time.Before(o.createdAt) {
		n.Label = "Maintenance ended early"
		n.Body = "This maintenance was stopped before its scheduled end."
		n.Facts = append(n.Facts, fact{"Started", timeText(o.occurrence.Time)})
	} else {
		n.Label = "Maintenance cancelled"
		n.Body = "This maintenance will not take place."
		if o.occurrence.Valid {
			n.Facts = append(n.Facts, fact{"Was scheduled for", timeText(o.occurrence.Time)})
		}
	}
	return n, "", nil
}

// repeatText describes a weekly rule: "Mon, Wed at 02:00 (Europe/Paris),
// 1 h 30 min each".
func repeatText(r maintenance.Rule) string {
	days := make([]string, 0, len(r.Weekdays))
	for _, d := range r.Weekdays {
		days = append(days, d.String()[:3])
	}
	when := strings.Join(days, ", ")
	if len(r.Weekdays) == 7 {
		when = "Every day"
	}
	text := fmt.Sprintf("%s at %s (%s), %s each", when, maintenance.FormatStartTime(r.StartMinute), r.Timezone, durationText(r.Duration))
	if r.Until != "" {
		text += ", until " + r.Until
	}
	return text
}

func durationText(d time.Duration) string {
	h, m := int(d.Hours()), int(d.Minutes())%60
	switch {
	case h > 0 && m > 0:
		return fmt.Sprintf("%d h %d min", h, m)
	case h > 0:
		return fmt.Sprintf("%d h", h)
	default:
		return fmt.Sprintf("%d min", m)
	}
}

func names(ctx context.Context, q queryer, query string, args ...any) ([]string, error) {
	rows, err := q.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("load affected components: %w", err)
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var name sql.NullString
		if err := rows.Scan(&name); err != nil {
			return nil, err
		}
		if v := strings.TrimSpace(name.String); v != "" {
			out = append(out, v)
		}
	}
	sort.Strings(out)
	return out, rows.Err()
}

// sweepMaintenanceStarts announces the start of each one-off maintenance
// occurrence on the pages it was announced on as scheduled (maintenance
// that only starts now, a snooze, was never scheduled ahead and is not
// announced). Recurring windows were announced once, with their rule, and
// never per occurrence. Every replica runs it; the outbox's dedupe key keeps
// each start once.
func (s *Service) sweepMaintenanceStarts(ctx context.Context) error {
	if _, err := s.db.ExecContext(ctx, `
		WITH announced AS (
			SELECT DISTINCT tenant_id, status_page_id, maintenance_window_id
			FROM status_page_notification_outbox
			WHERE event_kind = $1 AND created_at > NOW() - INTERVAL '60 days'
		)
		INSERT INTO status_page_notification_outbox
			(tenant_id, status_page_id, event_kind, maintenance_window_id, occurrence_starts_at, dedupe_key)
		SELECT DISTINCT a.tenant_id, a.status_page_id, $2, a.maintenance_window_id, mo.occurrence_starts_at,
			a.maintenance_window_id::text || '@' || to_char(mo.occurrence_starts_at AT TIME ZONE 'UTC', 'YYYY-MM-DD"T"HH24:MI:SS.US')
		FROM announced a
		JOIN maintenance_windows mw ON mw.id = a.maintenance_window_id AND mw.tenant_id = a.tenant_id
			AND mw.deleted_at IS NULL AND mw.recurrence_weekdays IS NULL
		JOIN `+statuspagesql.RenderedMonitorsSQL+` rm ON rm.status_page_id = a.status_page_id
		JOIN monitors m ON m.id = rm.monitor_id AND m.tenant_id = a.tenant_id AND m.deleted_at IS NULL
		CROSS JOIN LATERAL `+maintenance.OccurrenceRangesSQL("a.tenant_id", "m.id", "NOW()", "NOW() + INTERVAL '1 second'")+` mo
		WHERE mo.window_id = a.maintenance_window_id
		  AND mo.occurrence_starts_at <= NOW()
		  AND mo.occurrence_starts_at > NOW() - make_interval(secs => $3)
		ON CONFLICT DO NOTHING
	`, pagenotify.KindMaintenanceScheduled, pagenotify.KindMaintenanceStarted, maintenanceStartWindow.Seconds()); err != nil {
		return fmt.Errorf("announce starting maintenance: %w", err)
	}
	return nil
}
