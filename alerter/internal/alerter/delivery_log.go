package alerter

import (
	"context"
	"time"

	"github.com/yassinebenameur/probara/shared/notifications/deliverylog"
	"github.com/yassinebenameur/probara/shared/notifications/recipientledger"
)

// channelSender is the shape of sendFunc.
type channelSender = func(ctx context.Context, channel alertChannel, eventType string, binding policyBinding, alert *alertRecord, groupInfo *groupDetail, now time.Time) error

// recordDeliveries wraps send (sendChannelNotification) so each call leaves
// one row in the delivery history (package deliverylog): the synchronous
// send's outcome, or `queued` once the event is handed to the delivery
// workers, whose own rows then follow. deliverNotification calls it while it
// holds the claim transaction; the row goes through the pool, so a transient
// failure that rolls the claim back keeps its row, and the re-claim on a
// later cycle records the next attempt.
func (a *Alerter) recordDeliveries(send channelSender) channelSender {
	return func(ctx context.Context, channel alertChannel, eventType string, binding policyBinding, alert *alertRecord, groupInfo *groupDetail, now time.Time) error {
		ctx, tally := recipientledger.WithTally(ctx)
		err := send(ctx, channel, eventType, binding, alert, groupInfo, now)
		if a.db == nil || alert == nil {
			return err
		}
		entry := deliverylog.Entry{
			AlertID:     alert.ID,
			ChannelID:   channel.ID,
			ChannelName: channel.Name,
			EventType:   eventType,
			DeliveryKey: recipientKey(ctx, eventType, now, alert.RelapseCount),
			Path:        deliverylog.PathSync,
			Outcome:     deliverylog.SendOutcome(err),
			Err:         err,
			Recipients:  tally,
		}
		// The same switch sendChannelNotification takes: publish to the
		// delivery workers instead of sending here.
		if a.config != nil && a.config.AsyncDispatch && a.nats != nil {
			entry.Path = deliverylog.PathAsync
			if err == nil {
				entry.Outcome = deliverylog.OutcomeQueued
			} else {
				entry.Outcome = deliverylog.OutcomeFailed
			}
		}
		if rerr := deliverylog.Record(ctx, a.db, entry); rerr != nil {
			a.logger.WithError(rerr).WithFields(map[string]interface{}{
				"alert_id":   alert.ID,
				"channel_id": channel.ID,
				"event_type": eventType,
			}).Warn("Failed to record notification delivery")
		}
		return err
	}
}
