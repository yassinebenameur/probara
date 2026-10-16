package worker

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// RFC 3261 §17.1.1.1 timer values. T1 is the RTT estimate and the first
// retransmission interval; T2 caps the non-INVITE retransmission interval.
const (
	sipT1 = 500 * time.Millisecond
	sipT2 = 4 * time.Second
)

// sipEndpoint is the transaction layer over one flow. A dispatcher goroutine
// reads the flow and routes each response to its client transaction by top
// Via branch plus CSeq method (RFC 3261 §17.1.3): anything else — a response
// to a transaction this check never sent, or a late response from an earlier
// one — is never mistaken for the answer to the current request. Requests the
// peer sends us are answered by the UAS (sip_uas.go) on the same flow.
type sipEndpoint struct {
	flow *sipFlow
	uas  *sipUAS
	t1   time.Duration
	t2   time.Duration

	mu   sync.Mutex
	txns map[string]*sipClientTxn

	done chan struct{} // closed once the flow has ended and every message is dispatched

	retransmissions atomic.Int64
	stray           atomic.Int64 // responses matching no transaction
}

// sipClientTxn is one client transaction as the dispatcher sees it.
type sipClientTxn struct {
	responses chan sipInbound
	completed bool // final response received; later retransmissions are absorbed
}

func newSIPEndpoint(flow *sipFlow) *sipEndpoint {
	e := &sipEndpoint{
		flow: flow,
		t1:   sipT1,
		t2:   sipT2,
		txns: make(map[string]*sipClientTxn),
		done: make(chan struct{}),
	}
	e.uas = newSIPUAS(flow.send)
	go e.dispatch()
	return e
}

// Close ends the flow; the dispatcher exits once the reader has.
func (e *sipEndpoint) Close() { e.flow.Close() }

func sipTxnKey(branch, method string) string {
	return branch + " " + strings.ToUpper(method)
}

func (e *sipEndpoint) dispatch() {
	defer close(e.done)
	for in := range e.flow.inbound {
		if in.msg.IsRequest {
			e.uas.handle(in.msg)
			continue
		}
		_, method, err := in.msg.cseq()
		branch := in.msg.topViaBranch()
		if err != nil || branch == "" {
			e.stray.Add(1)
			continue
		}
		e.mu.Lock()
		txn := e.txns[sipTxnKey(branch, method)]
		completed := txn != nil && txn.completed
		e.mu.Unlock()
		switch {
		case txn == nil:
			e.stray.Add(1)
		case completed:
			// A retransmitted final for a finished transaction
			// (the Completed state's job): absorbed, not stray.
		default:
			select {
			case txn.responses <- in:
			default:
				// The transaction is not keeping up with a flood of
				// provisionals; dropping one changes nothing.
			}
		}
	}
}

func (e *sipEndpoint) register(key string) (*sipClientTxn, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if _, exists := e.txns[key]; exists {
		return nil, fmt.Errorf("duplicate SIP transaction %q", key)
	}
	txn := &sipClientTxn{responses: make(chan sipInbound, 16)}
	e.txns[key] = txn
	return txn, nil
}

func (e *sipEndpoint) complete(key string) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if txn := e.txns[key]; txn != nil {
		txn.completed = true
	}
}

// sipNoResponseError is a client transaction that timed out (Timer F, here
// the attempt budget). It unwraps to context.DeadlineExceeded, so it
// classifies as a timeout.
type sipNoResponseError struct {
	method      string
	provisional *sipMessage // last 1xx, if any
	stray       int64
	malformed   int64
}

func (e *sipNoResponseError) Error() string {
	var b strings.Builder
	if e.provisional != nil {
		fmt.Fprintf(&b, "no final response to %s (last %d %s)", e.method, e.provisional.StatusCode, e.provisional.Reason)
	} else {
		fmt.Fprintf(&b, "no response to %s", e.method)
	}
	var ignored []string
	if e.stray > 0 {
		ignored = append(ignored, fmt.Sprintf("%d unmatched", e.stray))
	}
	if e.malformed > 0 {
		ignored = append(ignored, fmt.Sprintf("%d unparseable", e.malformed))
	}
	if len(ignored) > 0 {
		// Never "; " — that separates assertion failures in a result.
		fmt.Fprintf(&b, " (ignored %s)", strings.Join(ignored, ", "))
	}
	return b.String()
}

func (e *sipNoResponseError) Unwrap() error { return context.DeadlineExceeded }

// roundTripNonInvite runs a non-INVITE client transaction (RFC 3261
// §17.1.2): send, retransmit on UDP under Timer E (T1 doubling to T2, and T2
// once a provisional has arrived), and return the first final response.
// Timer F is the caller's ctx: the attempt budget. Stream transports never
// retransmit. onProvisional, if set, sees every 1xx.
func (e *sipEndpoint) roundTripNonInvite(ctx context.Context, req *sipMessage, onProvisional func(sipInbound)) (sipInbound, error) {
	if req.Method == "INVITE" || req.Method == "ACK" {
		return sipInbound{}, fmt.Errorf("%s is not a non-INVITE transaction", req.Method)
	}
	key := sipTxnKey(req.topViaBranch(), req.Method)
	txn, err := e.register(key)
	if err != nil {
		return sipInbound{}, err
	}
	defer e.complete(key)

	raw := req.Bytes()
	if err := e.flow.send(raw); err != nil {
		return sipInbound{}, fmt.Errorf("failed to send request: %w", err)
	}

	var (
		timerE      <-chan time.Time
		timer       *time.Timer
		interval    = e.t1
		provisional *sipMessage
	)
	if !e.flow.reliable() {
		timer = time.NewTimer(interval)
		defer timer.Stop()
		timerE = timer.C
	}

	for {
		select {
		case in := <-txn.responses:
			if in.msg.StatusCode < 200 {
				provisional = in.msg
				if onProvisional != nil {
					onProvisional(in)
				}
				continue
			}
			return in, nil

		case <-timerE:
			if err := e.flow.send(raw); err != nil {
				return sipInbound{}, fmt.Errorf("failed to send request: %w", err)
			}
			e.retransmissions.Add(1)
			if provisional != nil {
				interval = e.t2
			} else {
				interval = min(2*interval, e.t2)
			}
			timer.Reset(interval)

		case <-e.done:
			// The dispatcher drained the flow before closing done, so a
			// final that arrived just before the peer hung up is queued.
			for drained := false; !drained; {
				select {
				case in := <-txn.responses:
					if in.msg.StatusCode >= 200 {
						return in, nil
					}
				default:
					drained = true
				}
			}
			return sipInbound{}, sipFlowReadError(e.flow)

		case <-ctx.Done():
			if !errors.Is(ctx.Err(), context.DeadlineExceeded) {
				return sipInbound{}, fmt.Errorf("check cancelled: %w", ctx.Err())
			}
			return sipInbound{}, &sipNoResponseError{
				method:      req.Method,
				provisional: provisional,
				stray:       e.stray.Load(),
				malformed:   e.flow.malformed.Load(),
			}
		}
	}
}
