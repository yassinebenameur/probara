package deliverylog

import (
	"context"
	"strings"
	"sync"

	"github.com/yassinebenameur/probara/shared/notifications/plugin"
)

// Tracker wraps a send's recipient ledger (plugin.DispatchRequest.Recipients)
// and remembers, for this one attempt, which recipients the plugin found
// pending and which of them the provider accepted. The history then gets one
// row per recipient this attempt tried, and none for a recipient an earlier
// attempt already reached: the ledger is what the plugin skips on, so the
// rows follow exactly what was sent.
//
// A plugin that never asks the ledger (it does not fan out) leaves the
// tracker empty, and the attempt is recorded as one row for the channel.
type Tracker struct {
	inner plugin.RecipientLedger

	mu       sync.Mutex
	asked    bool
	pending  []string
	seen     map[string]bool
	accepted map[string]bool
}

// Track wraps inner, which must not be nil (a nil ledger means "untracked",
// and a non-nil wrapper would change what the plugin does).
func Track(inner plugin.RecipientLedger) *Tracker {
	return &Tracker{inner: inner, seen: map[string]bool{}, accepted: map[string]bool{}}
}

// Delivered implements plugin.RecipientLedger.
func (t *Tracker) Delivered(ctx context.Context, recipients []string) (map[string]bool, error) {
	done, err := t.inner.Delivered(ctx, recipients)
	if err != nil {
		// The plugin gives up before sending; the attempt is recorded as
		// one failed row for the channel.
		return done, err
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	t.asked = true
	for _, r := range recipients {
		if done[r] || t.seen[r] {
			continue
		}
		t.seen[r] = true
		t.pending = append(t.pending, r)
	}
	return done, nil
}

// Record implements plugin.RecipientLedger. The recipient counts as accepted
// whatever the ledger write returns: the provider did accept it.
func (t *Tracker) Record(ctx context.Context, recipient string) error {
	t.mu.Lock()
	t.accepted[recipient] = true
	t.mu.Unlock()
	return t.inner.Record(ctx, recipient)
}

func (t *Tracker) fannedOut() bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.asked
}

type recipientResult struct {
	recipient string
	outcome   Outcome
}

// results is one entry per recipient this attempt tried: delivered when the
// provider accepted it, else the attempt's outcome.
func (t *Tracker) results(attempt Outcome) []recipientResult {
	t.mu.Lock()
	defer t.mu.Unlock()
	out := make([]recipientResult, 0, len(t.pending))
	for _, r := range t.pending {
		o := attempt
		if t.accepted[r] {
			o = OutcomeDelivered
		}
		out = append(out, recipientResult{recipient: r, outcome: o})
	}
	return out
}

// MaskRecipient is how a recipient is stored and shown: an email address
// keeps its first character and its domain, anything else (a phone number)
// its last four characters, the way the SMS plugin masks numbers in its
// errors.
func MaskRecipient(recipient string) string {
	if local, domain, ok := strings.Cut(recipient, "@"); ok && local != "" && domain != "" {
		return string([]rune(local)[:1]) + "***@" + domain
	}
	if len(recipient) <= 4 {
		return "****"
	}
	return strings.Repeat("*", len(recipient)-4) + recipient[len(recipient)-4:]
}
