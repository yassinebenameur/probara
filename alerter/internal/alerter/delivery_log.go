package alerter

import (
	"context"
	"database/sql"
	"time"

	"github.com/google/uuid"

	"github.com/yassinebenameur/probara/shared/notifications/deliverylog"
	"github.com/yassinebenameur/probara/shared/notifications/plugin"
)

// sendTrace is what one send through deliverNotification tells the delivery
// history (shared/notifications/deliverylog). deliverNotification attaches it
// to the send's context and records it once the send returned;
// sendChannelNotification fills in what only it knows, so sendFunc keeps its
// signature.
type sendTrace struct {
	deliveryKey string
	started     time.Time
	// queued: the event went to the delivery workers (asynchronous
	// dispatch), which record what happens to it; the alerter records
	// nothing.
	queued bool
	// secrets are the channel's decrypted secret values, masked out of the
	// stored error.
	secrets []string
	// recipients is the send's recipient ledger as the plugin used it.
	recipients *deliverylog.Tracker
}

type sendTraceCtx struct{}

// withSendTrace starts the trace of a send deliverNotification claimed, under
// the recipient key it attached (withRecipientKey).
func withSendTrace(ctx context.Context) (context.Context, *sendTrace) {
	key, _ := ctx.Value(recipientKeyCtx{}).(string)
	t := &sendTrace{deliveryKey: key, started: time.Now()}
	return context.WithValue(ctx, sendTraceCtx{}, t), t
}

// traceQueued marks the traced send as handed to the delivery workers.
func traceQueued(ctx context.Context) {
	if t, ok := ctx.Value(sendTraceCtx{}).(*sendTrace); ok {
		t.queued = true
	}
}

// traceSend notes the channel's secrets for the traced send and wraps its
// recipient ledger so the history gets one row per recipient this attempt
// tried. A nil ledger stays nil: plugins read nil as "untracked".
func traceSend(ctx context.Context, recipients plugin.RecipientLedger, manifest plugin.Manifest, config map[string]any) plugin.RecipientLedger {
	t, ok := ctx.Value(sendTraceCtx{}).(*sendTrace)
	if !ok {
		return recipients
	}
	t.secrets = deliverylog.SecretValues(manifest, config)
	if recipients == nil {
		return nil
	}
	t.recipients = deliverylog.Track(recipients)
	return t.recipients
}

// recordDelivery appends a claimed synchronous send's outcome to the
// delivery history: inside tx when the claim commits with it, through the
// pool (tx nil) when the claim rolled back. Best effort: a failed write is
// logged and the delivery goes on.
func (a *Alerter) recordDelivery(ctx context.Context, tx *sql.Tx, t *sendTrace, eventType string, alertID, channelID uuid.UUID, sendErr error) {
	if t == nil || t.queued || a.db == nil {
		return
	}
	outcome, reason := deliverylog.SendOutcome(sendErr)
	attempt := deliverylog.Attempt{
		AlertID:     alertID,
		ChannelID:   channelID,
		EventType:   eventType,
		DeliveryKey: t.deliveryKey,
		Path:        deliverylog.PathSync,
		Outcome:     outcome,
		Reason:      reason,
		Err:         sendErr,
		Secrets:     t.secrets,
		Duration:    time.Since(t.started),
		Recipients:  t.recipients,
	}
	var err error
	if tx != nil {
		err = deliverylog.RecordTx(ctx, tx, attempt)
	} else {
		err = deliverylog.Record(ctx, a.db, attempt)
	}
	if err != nil {
		a.logger.WithError(err).WithFields(map[string]interface{}{
			"alert_id": alertID, "channel_id": channelID, "event_type": eventType,
		}).Warn("Failed to record notification delivery")
	}
}
