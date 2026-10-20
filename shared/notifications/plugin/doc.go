// Package plugin defines the alert channel plugin contract and registry.
//
// Each alert channel integration (Teams, Slack, email, …) ships as a Plugin
// implementation that self-registers in its init() function. The API, alerter,
// and worker import this package to discover and dispatch through plugins
// without knowing about specific channel types.
//
// # Writing a plugin
//
// Plugins live in builtin/<type>/ and are registered in builtin/builtin.go.
// Every plugin must:
//
//   - Build its client with NewHTTPClient and send through PostJSON or Post.
//     The client dials through the egress policy and refuses redirects; Post
//     classifies the response and strips the URL from transport errors,
//     because webhook paths and bot tokens are credentials.
//   - Mark failures that retrying cannot fix with Permanent. The async worker
//     then stops redelivering, and the alerter keeps its claim instead of
//     failing identically every cycle.
//   - Check provider hosts with HostMatches, which matches on label
//     boundaries, never with strings.Contains or strings.HasSuffix. The API
//     re-runs Validate on the merged config of every channel update
//     (alertchannels.Service.validateMerged), so a host check in Validate
//     cannot be bypassed by editing the channel.
//   - Treat DispatchRequest.Test as a real send that must leave no provider
//     state behind: a paging plugin resolves the incident its test opened.
//   - Take its wording from DispatchRequest.View (package present) and own
//     layout only.
//
// Paging plugins (PagerDuty, Opsgenie) also advertise CapabilityAcknowledge,
// so the alerter sends them "acknowledged" once per channel it paged, and
// return nil for reminders: the provider escalates on its own schedule.
//
// Every other plugin advertises CapabilityFollowUp and renders the advisory
// "suspected_cause" follow-up from View like any other event. Paging plugins
// leave it off and are never sent one: the alerter selects follow-up targets
// by capability, so a new paging plugin is excluded without code. A plugin
// that switches on req.Type() must return nil for event types it does not
// handle.
//
// A plugin that fans out to several recipients sends independently to each
// one, so a retry of a partial failure must not reach the recipients the
// provider already accepted. It asks DispatchRequest.Recipients which ones are
// Delivered, sends to the rest, and Records each acceptance as soon as the
// provider call returns; a nil ledger (test sends) means send to everyone.
// When the ledger cannot be read, return a transient error rather than
// sending to everyone. The ledger is durable (package recipientledger), so
// the skip holds whichever alerter replica or worker runs the retry. Each
// send has its own ledger, chosen by the delivery layer: a deliberate second
// send of an event (the acknowledgement re-sent after a trigger it
// overtook) starts empty, so a plugin never special-cases it.
//
// It must also not return a mixed errors.Join. IsPermanent walks the joined
// children, so one permanent child would stop the retry for every other
// recipient. Return Permanent only when every failure is permanent; otherwise
// flatten the join into a plain error and carry the longest RetryAfterDelay
// onto it (see builtin/twilio).
package plugin
