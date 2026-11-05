package notifications

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"

	"github.com/yassinebenameur/probara/shared/contacts"
	"github.com/yassinebenameur/probara/shared/notifications"
	"github.com/yassinebenameur/probara/shared/notifications/plugin"
)

// errDropContact marks a direct send that can never be delivered: the
// contact is gone or unverified, or the channel's plugin cannot address one
// person. The message is acked rather than retried.
var errDropContact = errors.New("direct recipient not deliverable")

// directRecipient resolves an envelope's ContactID into the one recipient
// the plugin sends to. The address is read and decrypted here, never carried
// on NATS. An envelope without a ContactID returns nil (the channel's own
// recipients).
func (c *Consumer) directRecipient(ctx context.Context, m plugin.Manifest, envelope notifications.DispatchEnvelope) (*plugin.Recipient, error) {
	if envelope.ContactID == "" {
		return nil, nil
	}
	if !m.HasCapability(plugin.CapabilityDirectRecipient) {
		return nil, fmt.Errorf("%w: plugin %s cannot address one recipient", errDropContact, m.Type)
	}
	id, err := uuid.Parse(envelope.ContactID)
	if err != nil {
		return nil, fmt.Errorf("%w: invalid contact id", errDropContact)
	}
	contact, err := contacts.LoadVerified(ctx, c.db, c.encryptor, id)
	if errors.Is(err, contacts.ErrNotDeliverable) {
		return nil, fmt.Errorf("%w: contact %s", errDropContact, id)
	}
	if err != nil {
		return nil, err
	}
	return &plugin.Recipient{ContactID: contact.ID.String(), Address: contact.Address}, nil
}
