package notifications

import (
	"context"
	"time"

	"github.com/google/uuid"
	"github.com/sirupsen/logrus"

	"github.com/yassinebenameur/probara/shared/alertrouting"
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
// written, keeps it. Two kinds of claim are ever handed back, so two are
// marked: pages (a first page, or a relapse notice: a "created" event
// releaseMutedClaim may hand back) and resolves (dropped while the alert is
// open again after a relapse: releaseSupersededResolveClaim). The marker is
// keyed like the recipient ledger (DispatchEnvelope.RecipientKey), which
// names the event and the alert's episode: "created" for the first page,
// "created:relapse-N" for a route's first page during relapse N,
// "relapsed:relapse-N" for the relapse notice, "resolved[:relapse-N]" for a
// resolve. So a relapse never reads an earlier episode's marker, and a
// resolve's marker never reads as a relapse notice's on the same row (the
// relapsed claim rewrites last_sent_at, the resolve claim's does not
// survive it). A page's marker goes once the episode's resolve reaches the
// channel; a delivered resolve keeps its own (a redelivery after a lost ack,
// met by a relapse, must read as possibly reached, and a held relapse notice
// reads it to tell whether the row it replaced heard that recovery:
// recoveryReached); every marker goes with
// the alert otherwise (the ledger cascades).

// markTimeout bounds marker writes that must not ride the dispatch deadline:
// the clear after a send that may have used it all.
const markTimeout = 5 * time.Second

// pageKeys are the ledger keys a channel's page in episode can have: a page
// ("created", per episode) or a relapse notice.
func pageKeys(episode int) []string {
	keys := []string{notifications.RecipientEventKeyFor("created", time.Time{}, episode)}
	if episode > 0 {
		keys = append(keys, notifications.RecipientEventKeyFor(alertrouting.EventRelapsed, time.Time{}, episode))
	}
	return keys
}

// marked reports whether eventType's claims can be handed back, and so are
// marked: pages (relapse notices included) and resolves.
func marked(eventType string) bool {
	return eventType == "created" || eventType == "resolved"
}

// markPage writes the attempt marker before a page or resolve is sent. It
// reports whether this attempt created it; a marker an earlier attempt left
// stays that attempt's, whatever happens to this one.
func (c *Consumer) markPage(ctx context.Context, envelope notifications.DispatchEnvelope) (bool, error) {
	if !marked(envelope.EventType) {
		return false, nil
	}
	alertID, channelID, ok := envelopeIDs(envelope)
	if !ok {
		return false, nil
	}
	return recipientledger.MarkAttempt(ctx, c.db, alertID, channelID, envelope.RecipientKey())
}

// settlePage updates the marker once a send returned: a page or resolve
// this attempt marked that failed with proof nothing was accepted clears it;
// a delivered resolve clears the channel's page markers of its episode and
// of the episode its page came from (PagedRelapse), whose purpose ended with
// that recovery. A failed clear only leaves the safe side (the claim kept),
// so it is logged, never retried.
func (c *Consumer) settlePage(ctx context.Context, entry *logrus.Entry, envelope notifications.DispatchEnvelope, wasMarked bool, sendErr error) {
	var keys []string
	switch {
	case marked(envelope.EventType) && wasMarked && sendErr != nil && plugin.IsNotDelivered(sendErr):
		keys = []string{envelope.RecipientKey()}
	case envelope.EventType == "resolved" && sendErr == nil:
		alert := envelope.Event.Alert
		keys = pageKeys(alert.RelapseCount)
		if alert.PagedRelapse != alert.RelapseCount {
			keys = append(keys, pageKeys(alert.PagedRelapse)...)
		}
	default:
		return
	}
	alertID, channelID, ok := envelopeIDs(envelope)
	if !ok {
		return
	}
	clearCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), markTimeout)
	defer cancel()
	for _, key := range keys {
		if err := recipientledger.ClearAttempt(clearCtx, c.db, alertID, channelID, key); err != nil {
			entry.WithError(err).Warn("Failed to clear an attempt marker")
		}
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
