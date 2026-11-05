package notifications

import (
	"context"

	"github.com/google/uuid"

	"github.com/yassinebenameur/probara/shared/contacts"
	"github.com/yassinebenameur/probara/shared/notifications"
	"github.com/yassinebenameur/probara/shared/notifications/deliverylog"
)

// Escalation policies (migration 000113) page people: the alerter publishes
// one envelope per verified contact, carrying the ContactID (never the
// address). An SMS contact goes through the workspace's personal SMS
// channel (ChannelID set); an email contact goes through the platform
// mailer and has no channel at all (ChannelID empty, ChannelType "email").
// The alert-lock and mute checks are the channel path's, with
// StopOnAck set: an ack or snooze since publish drops the page.

// platformEmailName is the channel name recorded for a channel-less send.
const platformEmailName = "Email"

// loadEnvelopeChannel loads the envelope's channel, or stands in for the
// platform mailer on a channel-less person send. Only the email plugin is
// sent channel-less; anything else without a channel is dropped as deleted.
func (c *Consumer) loadEnvelopeChannel(ctx context.Context, envelope notifications.DispatchEnvelope) (*loadedChannel, error) {
	if envelope.ChannelID == "" && envelope.ContactID != "" && envelope.ChannelType == "email" {
		return &loadedChannel{Name: platformEmailName, Type: envelope.ChannelType, Config: []byte("{}"), IsActive: true}, nil
	}
	return c.loadChannel(ctx, envelope.ChannelID)
}

// personEntry is the person half of a person send's delivery-history row:
// the contact's owner and its masked address. Zero for channel sends, and
// for a contact deleted meanwhile (a channel-less row is then not recorded).
func (c *Consumer) personEntry(ctx context.Context, envelope notifications.DispatchEnvelope) deliverylog.Entry {
	if envelope.ContactID == "" || c.db == nil {
		return deliverylog.Entry{}
	}
	id, err := uuid.Parse(envelope.ContactID)
	if err != nil {
		return deliverylog.Entry{}
	}
	var userID uuid.UUID
	var kind, stored string
	if err := c.db.QueryRowContext(ctx, `SELECT user_id, kind, address FROM user_contacts WHERE id = $1`, id).
		Scan(&userID, &kind, &stored); err != nil {
		return deliverylog.Entry{}
	}
	masked := "***"
	if addr, err := c.encryptor.Decrypt(stored); err == nil {
		masked = contacts.Mask(contacts.Kind(kind), addr)
	}
	return deliverylog.Entry{UserID: userID, ContactID: id, Recipient: masked}
}
