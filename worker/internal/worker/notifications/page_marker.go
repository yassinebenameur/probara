package notifications

import (
	"context"
	"time"

	"github.com/google/uuid"
	"github.com/sirupsen/logrus"

	"github.com/yassinebenameur/probara/shared/notifications"
	"github.com/yassinebenameur/probara/shared/notifications/plugin"
	"github.com/yassinebenameur/probara/shared/notifications/recipientledger"
)

// The page attempt marker. A page held by an acknowledgement or snooze hands
// its claim back only if it provably reached nobody (releaseMutedClaim), and
// the JetStream delivery count cannot tell: it rises just as well for a 429,
// a refused dial or a worker restart, when nothing went out. So the worker
// records "possibly reached" explicitly: a ledger marker written through the
// pool before the provider is called (it survives a rollback or a crash, the
// safe side), removed only when the send failed in a way that proves nothing
// was accepted (plugin.IsNotDelivered: dial or DNS failure, refused egress,
// a non-2xx answer). An ambiguous failure, a timeout after the request was
// written, keeps it. Only pages are ever handed back, so only pages are
// marked; the marker goes once the alert's resolve reaches the channel, and
// with the alert otherwise (the ledger cascades).

// markTimeout bounds marker writes that must not ride the dispatch deadline:
// the clear after a send that may have used it all.
const markTimeout = 5 * time.Second

// pageKey is the ledger key of an alert's page (created happens once per
// alert and channel).
var pageKey = notifications.RecipientEventKey("created", time.Time{})

// markPage writes the attempt marker before a page is sent. It reports
// whether this attempt created it; a marker an earlier attempt left stays
// that attempt's, whatever happens to this one.
func (c *Consumer) markPage(ctx context.Context, envelope notifications.DispatchEnvelope) (bool, error) {
	if envelope.EventType != "created" {
		return false, nil
	}
	alertID, channelID, ok := envelopeIDs(envelope)
	if !ok {
		return false, nil
	}
	return recipientledger.MarkAttempt(ctx, c.db, alertID, channelID, envelope.RecipientKey())
}

// settlePage updates the marker once a send returned: a page this attempt
// marked that failed with proof nothing was accepted clears it; a delivered
// resolve clears the channel's page marker, whose purpose ended with the
// alert. A failed clear only leaves the safe side (the claim kept), so it is
// logged, never retried.
func (c *Consumer) settlePage(ctx context.Context, entry *logrus.Entry, envelope notifications.DispatchEnvelope, marked bool, sendErr error) {
	key := ""
	switch {
	case envelope.EventType == "created" && marked && sendErr != nil && plugin.IsNotDelivered(sendErr):
		key = envelope.RecipientKey()
	case envelope.EventType == "resolved" && sendErr == nil:
		key = pageKey
	default:
		return
	}
	alertID, channelID, ok := envelopeIDs(envelope)
	if !ok {
		return
	}
	clearCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), markTimeout)
	defer cancel()
	if err := recipientledger.ClearAttempt(clearCtx, c.db, alertID, channelID, key); err != nil {
		entry.WithError(err).Warn("Failed to clear a page attempt marker")
	}
}

func envelopeIDs(envelope notifications.DispatchEnvelope) (uuid.UUID, uuid.UUID, bool) {
	alertID, err := uuid.Parse(envelope.AlertID)
	if err != nil {
		return uuid.Nil, uuid.Nil, false
	}
	channelID, err := uuid.Parse(envelope.ChannelID)
	if err != nil {
		return uuid.Nil, uuid.Nil, false
	}
	return alertID, channelID, true
}
