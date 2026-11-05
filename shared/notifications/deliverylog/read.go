package deliverylog

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/lib/pq"

	"github.com/yassinebenameur/probara/shared/tenancy"
)

// Delivery is one recorded attempt as the API returns it.
type Delivery struct {
	ID      uuid.UUID `json:"id"`
	AlertID uuid.UUID `json:"alert_id"`
	// ChannelID is null for a person send without a channel (an escalation
	// page by email, through the platform mailer).
	ChannelID   *uuid.UUID `json:"channel_id"`
	ChannelName string     `json:"channel_name"`
	// ChannelType is the channel's current plugin type ("email" for a
	// channel-less person send).
	ChannelType string `json:"channel_type"`
	// UserID, Username and Recipient (the masked address) are set on person
	// sends: an escalation policy paging a user's verified contact.
	UserID      *uuid.UUID `json:"user_id,omitempty"`
	Username    *string    `json:"username,omitempty"`
	Recipient   *string    `json:"recipient,omitempty"`
	EventType   string     `json:"event_type"`
	DeliveryKey string     `json:"delivery_key"`
	Attempt     int        `json:"attempt"`
	Path        Path       `json:"path"`
	Outcome     Outcome    `json:"outcome"`
	Error       *string    `json:"error"`
	// RecipientsTotal/RecipientsDelivered are set for fan-out channels
	// (SMS) only.
	RecipientsTotal     *int `json:"recipients_total"`
	RecipientsDelivered *int `json:"recipients_delivered"`
	// AlertKind and MonitorName describe the alert, for the per-channel
	// view; MonitorName is null for alerts without a monitor (mesh edges,
	// locations).
	AlertKind   string    `json:"alert_kind"`
	MonitorName *string   `json:"monitor_name"`
	CreatedAt   time.Time `json:"created_at"`
}

// Page is one page of deliveries, newest first.
type Page struct {
	Items    []Delivery `json:"items"`
	Page     int        `json:"page"`
	PageSize int        `json:"page_size"`
	Total    int        `json:"total"`
}

// ErrNotFound is returned when the alert or channel is not in the workspace.
var ErrNotFound = errors.New("not found")

// Querier is what the readers need.
type Querier interface {
	QueryContext(ctx context.Context, query string, args ...interface{}) (*sql.Rows, error)
	QueryRowContext(ctx context.Context, query string, args ...interface{}) *sql.Row
}

const selectColumns = `
	d.id, d.alert_id, d.channel_id, d.channel_name, COALESCE(c.type, 'email'), d.event_type,
	d.delivery_key, d.attempt, d.path, d.outcome, d.error,
	d.recipients_total, d.recipients_delivered, a.kind, m.name, d.created_at,
	d.user_id, du.username, d.recipient`

const fromJoins = `
	FROM alert_notification_deliveries d
	LEFT JOIN alert_channels c ON c.id = d.channel_id
	JOIN alerts a ON a.id = d.alert_id
	LEFT JOIN monitors m ON m.id = a.monitor_id
	LEFT JOIN admin_users du ON du.id = d.user_id`

// ListForAlert returns one alert's deliveries, newest first. ErrNotFound when
// the alert is not tenantID's.
func ListForAlert(ctx context.Context, q Querier, tenantID, alertID uuid.UUID, page, pageSize int) (*Page, error) {
	if err := exists(ctx, q, "alerts", tenantID, alertID); err != nil {
		return nil, err
	}
	return list(ctx, q, "d.alert_id", tenantID, alertID, page, pageSize)
}

// ListForChannel returns one channel's deliveries, newest first. ErrNotFound
// when the channel is not tenantID's.
func ListForChannel(ctx context.Context, q Querier, tenantID, channelID uuid.UUID, page, pageSize int) (*Page, error) {
	if err := exists(ctx, q, "alert_channels", tenantID, channelID); err != nil {
		return nil, err
	}
	return list(ctx, q, "d.channel_id", tenantID, channelID, page, pageSize)
}

// LastForChannels returns the newest delivery of each of channelIDs that has
// one, for the channel list's last_delivery. Before migration 000110 it
// returns an empty map.
func LastForChannels(ctx context.Context, q Querier, tenantID uuid.UUID, channelIDs []uuid.UUID) (map[uuid.UUID]Delivery, error) {
	out := make(map[uuid.UUID]Delivery, len(channelIDs))
	if len(channelIDs) == 0 {
		return out, nil
	}
	rows, err := q.QueryContext(ctx, `
		SELECT `+selectColumns+`
		FROM unnest($2::uuid[]) AS ids(channel_id)
		JOIN LATERAL (
			SELECT * FROM alert_notification_deliveries x
			WHERE x.channel_id = ids.channel_id AND x.tenant_id = $1
			ORDER BY x.created_at DESC, x.id DESC
			LIMIT 1
		) d ON TRUE
		JOIN alert_channels c ON c.id = d.channel_id
		JOIN alerts a ON a.id = d.alert_id
		LEFT JOIN monitors m ON m.id = a.monitor_id
		LEFT JOIN admin_users du ON du.id = d.user_id`, tenantID, pq.Array(channelIDs))
	if err != nil {
		if isUndefinedTable(err) {
			return out, nil
		}
		return nil, fmt.Errorf("load last deliveries: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		d, err := scan(rows)
		if err != nil {
			return nil, err
		}
		if d.ChannelID != nil {
			out[*d.ChannelID] = d
		}
	}
	return out, rows.Err()
}

func clampPage(page, pageSize int) (int, int) {
	if page < 1 {
		page = 1
	}
	if pageSize <= 0 {
		pageSize = 20
	}
	if pageSize > 100 {
		pageSize = 100
	}
	return page, pageSize
}

// exists checks that the row of table with id belongs to a live tenantID.
// table is one of two constants above, never input.
func exists(ctx context.Context, q Querier, table string, tenantID, id uuid.UUID) error {
	var one int
	err := q.QueryRowContext(ctx, `SELECT 1 FROM `+table+` t
		WHERE t.id = $1 AND t.tenant_id = $2 AND `+tenancy.LiveTenantSQL("t.tenant_id"),
		id, tenantID).Scan(&one)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return fmt.Errorf("look up %s: %w", table, err)
	}
	return nil
}

// list reads a page filtered on column (d.alert_id or d.channel_id, never
// input) and the tenant.
func list(ctx context.Context, q Querier, column string, tenantID, id uuid.UUID, page, pageSize int) (*Page, error) {
	page, pageSize = clampPage(page, pageSize)
	result := &Page{Items: []Delivery{}, Page: page, PageSize: pageSize}
	where := ` WHERE d.tenant_id = $1 AND ` + column + ` = $2`
	if err := q.QueryRowContext(ctx, `SELECT COUNT(*) FROM alert_notification_deliveries d`+where,
		tenantID, id).Scan(&result.Total); err != nil {
		if isUndefinedTable(err) {
			return result, nil
		}
		return nil, fmt.Errorf("count deliveries: %w", err)
	}
	rows, err := q.QueryContext(ctx, `SELECT `+selectColumns+fromJoins+where+`
		ORDER BY d.created_at DESC, d.id DESC
		LIMIT $3 OFFSET $4`, tenantID, id, pageSize, (page-1)*pageSize)
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

func scan(rows *sql.Rows) (Delivery, error) {
	var d Delivery
	var errText, monitorName, username, recipient sql.NullString
	var total, delivered sql.NullInt64
	var channelID, userID uuid.NullUUID
	var path, outcome string
	if err := rows.Scan(&d.ID, &d.AlertID, &channelID, &d.ChannelName, &d.ChannelType, &d.EventType,
		&d.DeliveryKey, &d.Attempt, &path, &outcome, &errText,
		&total, &delivered, &d.AlertKind, &monitorName, &d.CreatedAt,
		&userID, &username, &recipient); err != nil {
		return Delivery{}, fmt.Errorf("scan delivery: %w", err)
	}
	if channelID.Valid {
		d.ChannelID = &channelID.UUID
	}
	if userID.Valid {
		d.UserID = &userID.UUID
	}
	if username.Valid {
		d.Username = &username.String
	}
	if recipient.Valid {
		d.Recipient = &recipient.String
	}
	d.Path, d.Outcome = Path(path), Outcome(outcome)
	if errText.Valid {
		d.Error = &errText.String
	}
	if monitorName.Valid {
		d.MonitorName = &monitorName.String
	}
	if total.Valid {
		n := int(total.Int64)
		d.RecipientsTotal = &n
	}
	if delivered.Valid {
		n := int(delivered.Int64)
		d.RecipientsDelivered = &n
	}
	return d, nil
}
