package worker

import (
	"context"
	"time"
)

// sipLingerPool bounds the lingers a worker keeps (D21). After a test call's
// result is decided and its teardown ran, a 2xx can still arrive: a late
// answer to an INVITE that never got a final (the teardown budget is 4 s,
// RFC 3261 Timer B is 32 s), or another fork of an answered call (a UAC must
// accept 2xx for 64·T1 after the first). Left alone, such an answer is a
// connected call nobody hangs up, billed until the far end times it out. A
// linger keeps the call's flow open for up to 32 s: the dispatcher keeps
// ACKing every 2xx (sipCall.onResponse), and the linger BYEs each new dialog
// and CANCELs an INVITE that only now draws a provisional. It also sends one
// more BYE to a dialog whose teardown BYE drew no response.
//
// At most slots lingers run at once; above that the flow is closed at once
// (onDropped). A worker that shuts down closes them without BYE.
type sipLingerPool struct {
	slots chan struct{}
	// onOutcome and onDropped feed worker metrics
	// (worker_sip_linger_total{outcome}, worker_sip_linger_dropped_total).
	onOutcome func(outcome string)
	onDropped func()
}

func newSIPLingerPool(slots int) *sipLingerPool {
	return &sipLingerPool{slots: make(chan struct{}, slots)}
}

// ConfigureCallMetrics wires the linger counters.
func (c *SIPChecker) ConfigureCallMetrics(onLinger func(outcome string), onLingerDropped func()) {
	c.lingers.onOutcome = onLinger
	c.lingers.onDropped = onLingerDropped
}

// hand gives the call's flow to a linger, or closes it when every slot is
// taken.
func (p *sipLingerPool) hand(s *sipCall) {
	select {
	case p.slots <- struct{}{}:
	default:
		s.ep.Close()
		if p.onDropped != nil {
			p.onDropped()
		}
		return
	}
	lingerFor := s.c.callTimers.linger
	if lingerFor <= 0 {
		lingerFor = sipCallLingerFor
	}
	go func() {
		defer func() { <-p.slots }()
		outcome := s.linger(lingerFor)
		if p.onOutcome != nil {
			p.onOutcome(outcome)
		}
	}()
}

// linger runs detached from the check, which has returned: it touches no
// metrics. It first sends one more BYE to each dialog whose teardown BYE
// drew no response (retryUnansweredByes), then waits for late answers.
// Outcome: "late_bye_unconfirmed" when that second BYE went unconfirmed too
// (the call may still be up, and billed, so nothing else outranks it),
// "late_answer" when it BYE'd a new dialog, "late_bye_ok" when the second
// BYE was confirmed, "late_cancel" when it CANCELled a late provisional,
// "flow_closed" when the peer closed the flow first, else "quiet".
func (s *sipCall) linger(lingerFor time.Duration) (outcome string) {
	defer s.ep.Close()
	defer func() {
		if r := recover(); r != nil {
			outcome = "aborted"
		}
	}()
	outcome = "quiet"
	deadline := time.NewTimer(lingerFor)
	defer deadline.Stop()

	if retried := s.retryUnansweredByes(); retried != "" {
		outcome = retried
	}
	byeNew := func() {
		for d := s.nextUnended(); d != nil; d = s.nextUnended() {
			ctx, cancel := context.WithTimeout(context.Background(), s.c.teardownBudget())
			s.bye(ctx, d)
			cancel()
			if outcome != "late_bye_unconfirmed" {
				outcome = "late_answer"
			}
		}
	}
	byeNew() // anything that answered after teardown's last look
	for {
		select {
		case ev := <-s.events:
			switch ev.kind {
			case sipCallEvAnswered:
				byeNew()
			case sipCallEvProvisional:
				// A provisional to an INVITE that never got a final and was
				// never CANCELled (the no-provisional timeout): CANCEL it now.
				if ev.txn == s.current && s.inviteFinal == nil && !s.cancelSent {
					s.cancelSent = true
					ctx, cancel := context.WithTimeout(context.Background(), s.c.teardownBudget())
					_, _ = s.ep.roundTripNonInvite(ctx, buildSIPCancel(s.current.params), nil)
					cancel()
					if outcome == "quiet" {
						outcome = "late_cancel"
					}
				}
			case sipCallEvFinal:
				if ev.txn == s.current {
					s.inviteFinal = ev.in.msg
				}
			}
		case <-s.ep.done:
			if outcome == "quiet" {
				outcome = "flow_closed"
			}
			return outcome
		case <-deadline.C:
			return outcome
		}
	}
}

// retryUnansweredByes sends one more BYE, as a new request, to each dialog
// whose teardown BYE drew no response at all and that the callee has not
// hung up since. nextUnended marks a dialog done before its BYE leaves, so
// nothing else ever retries it, and a far end that missed the BYE (a lost
// datagram, a proxy that dropped the transaction) keeps the call up and
// billed until it times out. The published result is not touched: it
// already reports bye_unconfirmed. It returns "late_bye_ok" when every one
// was confirmed (a 2xx, or 481: the far end no longer knows the dialog),
// "late_bye_unconfirmed" when any drew no response again or another final
// (the call's end is still unknown), and "" when there was nothing to retry.
func (s *sipCall) retryUnansweredByes() string {
	s.mu.Lock()
	var retry []*sipDialog
	for _, d := range s.dialogs {
		if d.byeUnanswered && !d.remoteBye {
			retry = append(retry, d)
		}
	}
	s.mu.Unlock()
	outcome := ""
	for _, d := range retry {
		d.byeUnanswered = false
		ctx, cancel := context.WithTimeout(context.Background(), s.c.teardownBudget())
		result, _, _ := s.bye(ctx, d)
		cancel()
		switch {
		case result != "bye_ok" && result != "bye_481":
			outcome = "late_bye_unconfirmed"
		case outcome == "":
			outcome = "late_bye_ok"
		}
	}
	return outcome
}
