package notifications

import (
	"context"

	"github.com/google/uuid"

	"github.com/yassinebenameur/probara/shared/notifications"
	"github.com/yassinebenameur/probara/shared/notifications/deliverylog"
	"github.com/yassinebenameur/probara/shared/notifications/recipientledger"
	"github.com/yassinebenameur/probara/shared/queue"
)

// recordAttempt appends one delivery attempt of envelope to the history
// (package deliverylog): delivered, a transient failure that JetStream will
// redeliver, or a permanent one. It writes through the pool, never the
// alert-lock transaction, which always rolls back. sendErr is the attempt's
// result, including a failed follow-up acknowledgement.
func (c *Consumer) recordAttempt(ctx context.Context, msg *queue.Message, envelope notifications.DispatchEnvelope, channelName string, tally *recipientledger.Tally, sendErr error) {
	c.recordDelivery(ctx, msg, envelope, channelName, deliverylog.SendOutcome(sendErr), sendErr, tally)
}

// recordDrop appends an event the worker dropped without sending:
// dropped_superseded (resolved, unacknowledged, or reopened since publish)
// or dropped_ack (held by an ack or snooze since publish).
func (c *Consumer) recordDrop(ctx context.Context, msg *queue.Message, envelope notifications.DispatchEnvelope, channelName string, outcome deliverylog.Outcome) {
	c.recordDelivery(ctx, msg, envelope, channelName, outcome, nil, nil)
}

func (c *Consumer) recordDelivery(ctx context.Context, msg *queue.Message, envelope notifications.DispatchEnvelope, channelName string, outcome deliverylog.Outcome, sendErr error, tally *recipientledger.Tally) {
	if c.db == nil {
		return
	}
	alertID, aerr := uuid.Parse(envelope.AlertID)
	channelID, cerr := uuid.Parse(envelope.ChannelID)
	if aerr != nil || (cerr != nil && envelope.ContactID == "") {
		return
	}
	person := c.personEntry(ctx, envelope) // person.go; zero for channel sends
	err := deliverylog.Record(ctx, c.db, deliverylog.Entry{
		UserID:      person.UserID,
		ContactID:   person.ContactID,
		Recipient:   person.Recipient,
		AlertID:     alertID,
		ChannelID:   channelID,
		ChannelName: channelName,
		EventType:   envelope.EventType,
		DeliveryKey: envelope.RecipientKey(),
		Attempt:     deliveryAttempt(msg),
		Path:        deliverylog.PathAsync,
		Outcome:     outcome,
		Err:         sendErr,
		Recipients:  tally,
	})
	if err != nil && c.logger != nil {
		c.logger.WithError(err).WithField("alert_id", envelope.AlertID).
			WithField("channel_id", envelope.ChannelID).
			Warn("Failed to record notification delivery")
	}
}
