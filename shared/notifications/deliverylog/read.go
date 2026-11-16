package deliverylog

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/lib/pq"

	"github.com/yassinebenameur/probara/shared/db/pgerr"
)

// FailingWindow is how far back a failure keeps a channel flagged failing
// (see ChannelHealth).
const FailingWindow = 24 * time.Hour

// Delivery is one recorded row as the API returns it.
type Delivery struct {
	ID uuid.UUID `json:"id"`
	// AlertID is null for a test send.
	AlertID     *uuid.UUID `json:"alert_id"`
	ChannelID   uuid.UUID  `json:"channel_id"`
	ChannelName string     `json:"channel_name"`
	ChannelType string     `json:"channel_type"`
	EventType   string     `json:"event_type"`
	// Recipient is the masked recipient on a channel that fans out (SMS),
	// one row per recipient; null otherwise.
	Recipient      *string `json:"recipient"`
	Attempt        int     `json:"attempt"`
	Path           Path    `json:"path"`
	Test           bool    `json:"test"`
	Outcome        Outcome `json:"outcome"`
	Reason         *Reason `json:"reason"`
	Error          *string `json:"error"`
	ProviderStatus *int    `json:"provider_status"`
	DurationMs     *int    `json:"duration_ms"`
	// AlertKind and MonitorName describe the alert for the per-channel view;
	// MonitorName is null for alerts without a monitor (mesh edges) and
	// both are null for a test send.
	AlertKind   *string `json:"alert_kind"`
	MonitorName *string `json:"monitor_name"`
	// SourceLocationName and TargetLocationName name a monitor-less alert's
	// subject: the location of a location_offline alert, the two ends of a
	// mesh_edge alert.
	SourceLocationName *string   `json:"source_location_name"`
	TargetLocationName *string   `json:"target_location_name"`
	CreatedAt          time.Time `json:"created_at"`
}

// Page is one page of deliveries.
type Page struct {
	Items    []Delivery `json:"items"`
	Page     int        `json:"page"`
	PageSize int        `json:"page_size"`
	Total    int        `json:"total"`
}

// ErrNotFound is returned when the alert or channel is not the tenant's.
var ErrNotFound = errors.New("not found")

// Querier is what the readers need.
type Querier interface {
	QueryContext(ctx context.Context, query string, args ...interface{}) (*sql.Rows, error)
	QueryRowContext(ctx context.Context, query string, args ...interface{}) *sql.Row
}

const selectColumns = `
	d.id, d.alert_id, d.channel_id, c.name, c.type, d.event_type, d.recipient,
	d.attempt, d.path, d.is_test, d.outcome, d.reason, d.error, d.provider_status,
	d.duration_ms, a.kind, m.name, ls.name, lt.name, d.created_at`

const fromJoins = `
	FROM alert_notification_deliveries d
	JOIN alert_channels c ON c.id = d.channel_id
	LEFT JOIN alerts a ON a.id = d.alert_id
	LEFT JOIN monitors m ON m.id = a.monitor_id
	LEFT JOIN locations ls ON ls.id = a.source_location_id
	LEFT JOIN locations lt ON lt.id = a.target_location_id`

// ListForAlert returns one alert's attempts across its channels in the order
// they happened. Test sends have no alert, so they never appear here.
// ErrNotFound when the alert is not tenantID's.
func ListForAlert(ctx context.Context, q Querier, tenantID, alertID uuid.UUID, page, pageSize int) (*Page, error) {
	if err := exists(ctx, q, "alerts", tenantID, alertID); err != nil {
		return nil, err
	}
	return list(ctx, q, "d.alert_id", tenantID, alertID, nil, "ASC", page, pageSize)
}

// ListForChannel returns one channel's attempts, test sends included, newest
// first, optionally only those with one of outcomes. ErrNotFound when the
// channel is not tenantID's.
func ListForChannel(ctx context.Context, q Querier, tenantID, channelID uuid.UUID, outcomes []Outcome, page, pageSize int) (*Page, error) {
	if err := exists(ctx, q, "alert_channels", tenantID, channelID); err != nil {
		return nil, err
	}
	return list(ctx, q, "d.channel_id", tenantID, channelID, outcomes, "DESC", page, pageSize)
}

// ParseOutcomes reads a comma-separated outcome filter. Empty means all.
func ParseOutcomes(raw string) ([]Outcome, error) {
	var out []Outcome
	for _, part := range strings.Split(raw, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		known := false
		for _, o := range Outcomes {
			if string(o) == part {
				known = true
				break
			}
		}
		if !known {
			return nil, fmt.Errorf("unknown outcome %q", part)
		}
		out = append(out, Outcome(part))
	}
	return out, nil
}

// Health is a channel's recent delivery record, for the channel list and
// detail.
type Health struct {
	LastDeliveredAt *time.Time
	LastFailedAt    *time.Time
	// Failing: the channel's latest attempt that reached a provider outcome
	// (delivered, failed or permanently failed; drops do not count, test
	// sends do) within FailingWindow failed. Equivalently: a failure in the
	// last 24 hours with no success since. A partly failed fan-out (one SMS
	// number rejected) counts as failing.
	Failing bool
}

// ChannelHealth returns the Health of each of channelIDs that is tenantID's.
// Before migration 000107 it returns an empty map.
func ChannelHealth(ctx context.Context, q Querier, tenantID uuid.UUID, channelIDs []uuid.UUID) (map[uuid.UUID]Health, error) {
	out := make(map[uuid.UUID]Health, len(channelIDs))
	if len(channelIDs) == 0 {
		return out, nil
	}
	// Each MAX is one backward step of the (channel_id, outcome, created_at)
	// index. The failure wins a tie: the rows of one fan-out attempt share a
	// timestamp, and a partial failure is a failure.
	rows, err := q.QueryContext(ctx, `
		SELECT c.id, h.delivered, h.failed,
			h.failed IS NOT NULL AND h.failed > NOW() - ($3::double precision * INTERVAL '1 second')
				AND (h.delivered IS NULL OR h.failed >= h.delivered)
		FROM alert_channels c
		CROSS JOIN LATERAL (
			SELECT
				(SELECT MAX(created_at) FROM alert_notification_deliveries
					WHERE channel_id = c.id AND outcome = 'delivered') AS delivered,
				GREATEST(
					(SELECT MAX(created_at) FROM alert_notification_deliveries
						WHERE channel_id = c.id AND outcome = 'failed'),
					(SELECT MAX(created_at) FROM alert_notification_deliveries
						WHERE channel_id = c.id AND outcome = 'permanent_failure')) AS failed
		) h
		WHERE c.tenant_id = $1 AND c.id = ANY($2::uuid[])`,
		tenantID, pq.Array(channelIDs), FailingWindow.Seconds())
	if err != nil {
		if pgerr.IsUndefinedTable(err) {
			return out, nil
		}
		return nil, fmt.Errorf("load channel delivery health: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var id uuid.UUID
		var delivered, failed sql.NullTime
		var h Health
		if err := rows.Scan(&id, &delivered, &failed, &h.Failing); err != nil {
			return nil, fmt.Errorf("scan channel delivery health: %w", err)
		}
		if delivered.Valid {
			h.LastDeliveredAt = &delivered.Time
		}
		if failed.Valid {
			h.LastFailedAt = &failed.Time
		}
		out[id] = h
	}
	return out, rows.Err()
}

// exists checks that the row of table with id is tenantID's. table is one of
// two constants in this file, never input.
func exists(ctx context.Context, q Querier, table string, tenantID, id uuid.UUID) error {
	var one int
	err := q.QueryRowContext(ctx, `SELECT 1 FROM `+table+` WHERE id = $1 AND tenant_id = $2`, id, tenantID).Scan(&one)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return fmt.Errorf("look up %s: %w", table, err)
	}
	return nil
}

// list reads one page filtered on column (d.alert_id or d.channel_id, never
// input) and the tenant, ordered by time in dir (ASC or DESC, never input).
func list(ctx context.Context, q Querier, column string, tenantID, id uuid.UUID, outcomes []Outcome, dir string, page, pageSize int) (*Page, error) {
	page, pageSize = clampPage(page, pageSize)
	result := &Page{Items: []Delivery{}, Page: page, PageSize: pageSize}
	where := ` WHERE d.tenant_id = $1 AND ` + column + ` = $2`
	args := []any{tenantID, id}
	if len(outcomes) > 0 {
		filter := make([]string, len(outcomes))
		for i, o := range outcomes {
			filter[i] = string(o)
		}
		args = append(args, pq.StringArray(filter))
		where += ` AND d.outcome = ANY($` + strconv.Itoa(len(args)) + `::text[])`
	}
	if err := q.QueryRowContext(ctx, `SELECT COUNT(*) FROM alert_notification_deliveries d`+where, args...).Scan(&result.Total); err != nil {
		if pgerr.IsUndefinedTable(err) {
			return result, nil
		}
		return nil, fmt.Errorf("count deliveries: %w", err)
	}
	n := len(args)
	rows, err := q.QueryContext(ctx, `SELECT `+selectColumns+fromJoins+where+`
		ORDER BY d.created_at `+dir+`, d.id `+dir+`
		LIMIT $`+strconv.Itoa(n+1)+` OFFSET $`+strconv.Itoa(n+2),
		append(args, pageSize, (page-1)*pageSize)...)
	if err != nil {
		return nil, fmt.Errorf("list deliveries: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		d, err := scan(rows)
		if err != nil {
			return nil, err
		}
		result.Items = append(result.Items, d)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate deliveries: %w", err)
	}
	return result, nil
}

// DefaultPageSize and MaxPageSize bound a page. An alert's whole history is
// usually one page.
const (
	DefaultPageSize = 50
	MaxPageSize     = 200
)

func clampPage(page, pageSize int) (int, int) {
	if page < 1 {
		page = 1
	}
	if pageSize <= 0 {
		pageSize = DefaultPageSize
	}
	if pageSize > MaxPageSize {
		pageSize = MaxPageSize
	}
	return page, pageSize
}

func scan(rows *sql.Rows) (Delivery, error) {
	var d Delivery
	var alertID uuid.NullUUID
	var recipient, reason, errText, alertKind, monitorName, sourceName, targetName sql.NullString
	var status, duration sql.NullInt64
	var path, outcome string
	if err := rows.Scan(&d.ID, &alertID, &d.ChannelID, &d.ChannelName, &d.ChannelType, &d.EventType, &recipient,
		&d.Attempt, &path, &d.Test, &outcome, &reason, &errText, &status,
		&duration, &alertKind, &monitorName, &sourceName, &targetName, &d.CreatedAt); err != nil {
		return Delivery{}, fmt.Errorf("scan delivery: %w", err)
	}
	d.Path, d.Outcome = Path(path), Outcome(outcome)
	if alertID.Valid {
		d.AlertID = &alertID.UUID
	}
	d.Recipient = nullString(recipient)
	if reason.Valid {
		r := Reason(reason.String)
		d.Reason = &r
	}
	d.Error = nullString(errText)
	d.AlertKind = nullString(alertKind)
	d.MonitorName = nullString(monitorName)
	d.SourceLocationName = nullString(sourceName)
	d.TargetLocationName = nullString(targetName)
	if status.Valid {
		n := int(status.Int64)
		d.ProviderStatus = &n
	}
	if duration.Valid {
		n := int(duration.Int64)
		d.DurationMs = &n
	}
	return d, nil
}

func nullString(s sql.NullString) *string {
	if !s.Valid {
		return nil
	}
	return &s.String
}
