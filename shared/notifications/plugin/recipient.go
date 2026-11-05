package plugin

// CapabilityDirectRecipient means the plugin can deliver one send to a
// single person's address (DispatchRequest.Recipient) instead of the
// recipients configured on the channel, so a workspace can designate the
// channel for texting people directly: contact verification codes, contact
// tests and escalation-policy pages (where the email plugin, the platform
// mailer, also takes it). A plugin advertising it must honour
// Recipient.Address and Recipient.Text. A send carrying a ContactID scopes
// its acknowledge link to that contact instead of the channel
// (DispatchRequest.View).
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
