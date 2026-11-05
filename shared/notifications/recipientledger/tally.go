package recipientledger

import (
	"context"
	"sync"
)

// Tally counts what one send did with its ledger: how many recipients the
// plugin asked about (Delivered) and how many of them ended up accepted,
// whether by an earlier attempt or by this one (Record). The delivery history
// (package deliverylog) stores the counts next to the attempt, so a partial
// SMS fan-out reads "2 of 3" there.
//
// It rides on the context because the plugin, not the delivery layer, calls
// the ledger: WithTally before Send, read Counts after it.
type Tally struct {
	mu        sync.Mutex
	used      bool
	total     int
	delivered int
}

type tallyCtx struct{}

// WithTally returns ctx carrying a fresh Tally that every ledger reached
// through it (or a context derived from it) reports to.
func WithTally(ctx context.Context) (context.Context, *Tally) {
	t := &Tally{}
	return context.WithValue(ctx, tallyCtx{}, t), t
}

func tallyFrom(ctx context.Context) *Tally {
	t, _ := ctx.Value(tallyCtx{}).(*Tally)
	return t
}

func (t *Tally) observe(total, delivered int) {
	if t == nil {
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	t.used = true
	t.total += total
	t.delivered += delivered
}

// Counts returns the recipients asked about and the ones accepted. ok is false
// when the plugin never consulted a ledger (single-target channels), so the
// history leaves both counts empty rather than reading "0 of 0".
func (t *Tally) Counts() (total, delivered int, ok bool) {
	if t == nil {
		return 0, 0, false
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.total, t.delivered, t.used
}
