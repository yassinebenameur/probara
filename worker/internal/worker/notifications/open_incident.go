package notifications

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"time"

	"github.com/sirupsen/logrus"

	"github.com/yassinebenameur/probara/shared/notifications"
	"github.com/yassinebenameur/probara/shared/notifications/deliverylog"
	"github.com/yassinebenameur/probara/shared/notifications/plugin"
	"github.com/yassinebenameur/probara/shared/secrets"
)

// Paging providers (PagerDuty, Opsgenie) key their incident per paging
// episode of an alert (notifications.AlertDetails.PagedRelapse): a relapse
// opens a new incident and closes the earlier one. What is open at the
// provider for a route is the episode of the last page actually delivered to
// it, alert_notification_states.delivered_relapse, which only a delivered
// send writes (here, and in the alerter's synchronous path). A claim alone
// opens nothing: a relapse notice claimed and then dropped, or never
// delivered, leaves the earlier incident open, so everything that closes or
// acknowledges an incident targets the delivered episode.

// useOpenIncident points envelope at the incident open for its route, read
// inside tx (which holds the alert row): a resolve or an acknowledgement
// targets the last delivered page's episode, and a relapse notice closes it
// before opening its own. A route with nothing recorded keeps what the
// alerter's claim said.
func useOpenIncident(ctx context.Context, tx *sql.Tx, envelope *notifications.DispatchEnvelope) error {
	relapseNotice := envelope.Event.Alert.RelapseNotice
	if envelope.EventType != "resolved" && envelope.EventType != "acknowledged" && !relapseNotice {
		return nil
	}
	alertID, channelID, ok := envelopeIDs(*envelope)
	if !ok {
		return nil
	}
	var delivered sql.NullInt64
	err := tx.QueryRowContext(ctx, `SELECT delivered_relapse FROM alert_notification_states
		WHERE alert_id = $1 AND channel_id = $2`, alertID, channelID).Scan(&delivered)
	if errors.Is(err, sql.ErrNoRows) || (err == nil && !delivered.Valid) {
		return nil
	}
	if err != nil {
		return err
	}
	open := int(delivered.Int64)
	if relapseNotice {
		envelope.Event.Alert.PreviousPagedRelapse = &open
	} else {
		envelope.Event.Alert.PagedRelapse = open
	}
	return nil
}

// recordDeliveredPage notes, through the pool, that a page reached the
// provider: its episode's incident is now the one open for the route. It
// runs after the send, on a context detached from the dispatch deadline; a
// failed write only leaves the earlier episode recorded, so a later resolve
// closes that one (and the relapse notice already closed it), which is
// logged.
func (c *Consumer) recordDeliveredPage(ctx context.Context, entry *logrus.Entry, envelope notifications.DispatchEnvelope) {
	if envelope.EventType != "created" || c.db == nil {
		return
	}
	alertID, channelID, ok := envelopeIDs(envelope)
	if !ok {
		return
	}
	writeCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), markTimeout)
	defer cancel()
	if _, err := c.db.ExecContext(writeCtx, `
		UPDATE alert_notification_states
		SET delivered_relapse = GREATEST(COALESCE(delivered_relapse, 0), $3)
		WHERE alert_id = $1 AND channel_id = $2`, alertID, channelID, envelope.Event.Alert.PagedRelapse); err != nil {
		entry.WithError(err).Warn("Failed to record the delivered page episode")
	}
}

// closePreviousIncident sends a relapse notice's close of the route's
// earlier incident on its own, for a notice that is not going to be
// delivered (dropped as superseded or held, or failed for good): the notice
// would have closed it first, and nothing else will, so a paging provider
// would keep escalating an incident whose outage recovered. A resolve of an
// incident already closed is a no-op at the provider. Best effort; called
// while the caller still holds the alert row, like every send. It returns
// the delivery-history row of that close (recorded as the earlier
// episode's recovery notice) for the caller to write: inside its
// transaction when it commits, else through the pool once the alert row is
// released, never through the pool while it is held.
func (c *Consumer) closePreviousIncident(ctx context.Context, entry *logrus.Entry, p plugin.Plugin, channel *loadedChannel, envelope notifications.DispatchEnvelope, attempt int) *closeRecord {
	alert := envelope.Event.Alert
	if !alert.RelapseNotice || alert.PreviousPagedRelapse == nil || *alert.PreviousPagedRelapse == alert.PagedRelapse {
		return nil
	}
	previous := *alert.PreviousPagedRelapse
	configMap := map[string]any{}
	if len(channel.Config) > 0 {
		if err := json.Unmarshal(channel.Config, &configMap); err != nil {
			return nil
		}
	}
	configMap, err := secrets.DecryptConfig(c.encryptor, p.Manifest(), configMap)
	if err != nil {
		entry.WithError(err).Warn("Failed to decrypt channel config to close an earlier incident")
		return nil
	}
	event := envelope.Event
	event.Type = "resolved"
	event.Alert.Status = "resolved"
	event.Alert.RelapseNotice = false
	event.Alert.PreviousPagedRelapse = nil
	event.Alert.PagedRelapse = previous
	closeEnvelope := envelope
	closeEnvelope.EventType = "resolved"
	closeEnvelope.Event = event
	closeEnvelope.Release = nil
	closeEnvelope.DeliveryKey = notifications.RecipientEventKeyFor("resolved", time.Time{}, previous)
	record := c.deliveryRecord(entry, closeEnvelope, attempt)
	record.channelLoaded(p.Manifest(), configMap)
	sendErr := p.Send(ctx, plugin.DispatchRequest{
		Channel:   plugin.ChannelRef{ID: envelope.ChannelID, Name: channel.Name, Config: configMap},
		Event:     event,
		EventType: "resolved",
		Attempt:   attempt,
	})
	if sendErr != nil {
		entry.WithError(sendErr).Warn("Failed to close an earlier incident of a relapsed alert")
	}
	outcome, reason := deliverylog.SendOutcome(sendErr)
	return &closeRecord{record: record, row: record.row(outcome, reason, sendErr)}
}

// closeRecord is closePreviousIncident's delivery-history row, written by
// the caller once it knows which transaction may carry it.
type closeRecord struct {
	record *deliveryRecord
	row    deliverylog.Attempt
}

// write records the close: inside tx when non-nil (it commits), else
// through the pool. Nil is a no-op.
func (r *closeRecord) write(ctx context.Context, tx *sql.Tx) {
	if r != nil {
		r.record.write(ctx, tx, r.row)
	}
}
