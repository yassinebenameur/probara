package plugin

// CapabilityDirectRecipient means the plugin can deliver one send to a
// single person's address (DispatchRequest.Recipient) instead of the
// recipients configured on the channel, so a workspace can designate the
// channel for texting people directly: contact verification codes, contact
// tests and, later, on-call pages. A plugin advertising it must honour
// Recipient.Address and Recipient.Text.
const CapabilityDirectRecipient Capability = "direct_recipient"

// Recipient addresses one send to one person. The address is a user's
// contact, decrypted just before Send; async envelopes carry only the
// contact ID (notifications.DispatchEnvelope.ContactID) and the worker
// resolves it, so an address never rides through NATS.
type Recipient struct {
	// ContactID is the user_contacts row the address came from ("" for a
	// verification code to a contact being created).
	ContactID string
	// Address is the destination in the plugin's own format (E.164 for SMS).
	Address string
	// Text, when set, is sent verbatim instead of the alert rendering: a
	// verification code or a contact test message.
	Text string
}
