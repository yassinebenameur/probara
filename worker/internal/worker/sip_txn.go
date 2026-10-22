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
// peer sends us are answered by the UAS (sip_uas.go) on the same flow, except
// the probe's own requests coming back (see dispatch).
type sipEndpoint struct {
	flow *sipFlow
	uas  *sipUAS
	t1   time.Duration
	t2   time.Duration

	mu       sync.Mutex
	txns     map[string]*sipClientTxn
	branches map[string]struct{} // every branch this endpoint has sent

	done chan struct{} // closed once the flow has ended and every message is dispatched

	retransmissions atomic.Int64
	stray           atomic.Int64 // responses matching no transaction
	looped          atomic.Int64 // own requests that came back
}

// sipClientTxn is one client transaction as the dispatcher sees it.
//
// Provisionals and the final travel on separate channels. Provisionals are
// lossy: a transaction slower than a burst of 1xx loses some, which changes
// nothing. The final must never be lost — on a stream nothing retransmits it,
// so a dropped final reads as "no final response" from a server that
// answered — so it has its own slot, which the dispatcher fills without ever
// waiting behind queued provisionals. A full slot means a final is already
// queued, and a second one is a retransmission.
type sipClientTxn struct {
	provisionals chan sipInbound
	final        chan sipInbound // capacity 1
	looped       chan struct{}   // closed when the request came back to us
	loopOnce     sync.Once
	completed    bool // final response received; later retransmissions are absorbed

	// onResponse, set for INVITE client transactions (registerInvite),
	// receives every response, provisional and final, retransmissions and
	// forked 2xx included, on the dispatcher goroutine; the channels above
	// are unused. An INVITE needs that: a non-2xx final is ACKed by the
	// transaction and its retransmissions re-ACKed (Timer D's job), every
	// 2xx — one per fork, each retransmitted until ACKed — gets its own ACK,
	// and all of it must happen whatever the call's own goroutine is busy
	// with (a CANCEL, a BYE) or after the check has returned (sip_linger.go).
	onResponse func(sipInbound)
}

// sipLoopError is a request of ours that came back to us as a request: the
// target reflects packets (a UDP or TCP echo service, a port forward pointing
// back at the worker) or a proxy routed it in a loop. No SIP server answered,
// so the check is an error, never a status the reflection happened to carry.
type sipLoopError struct{ method string }

func (e *sipLoopError) Error() string {
	return fmt.Sprintf("request looped back: the target returned the probe's own %s instead of answering it (an echo service or a routing loop, not a SIP server)", e.method)
}

func newSIPEndpoint(flow *sipFlow) *sipEndpoint {
	return newSIPEndpointWithDialogs(flow, nil)
}

// newSIPEndpointWithDialogs is newSIPEndpoint for a flow that holds dialogs:
// matchDialog (see sipUAS) decides which in-dialog requests are ours. It is
// set before the dispatcher starts, so no request is answered without it.
func newSIPEndpointWithDialogs(flow *sipFlow, matchDialog func(*sipMessage) bool) *sipEndpoint {
	e := &sipEndpoint{
		flow:     flow,
		t1:       sipT1,
		t2:       sipT2,
		txns:     make(map[string]*sipClientTxn),
		branches: make(map[string]struct{}),
		done:     make(chan struct{}),
	}
	e.uas = newSIPUAS(flow.send)
	e.uas.matchDialog = matchDialog
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
			// One of our own requests, reflected or looped back, carries a
			// branch we generated in one of its Vias (the top one when
			// echoed, a lower one when a proxy forwarded it back). It is
			// never answered: the reply would carry our branch, a reflector
			// would bounce it back, and the transaction would take its own
			// reply for the server's final response.
			if e.ownRequest(in.msg) {
				continue
			}
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
		case txn.onResponse != nil:
			txn.onResponse(in)
		case completed:
			// A retransmitted final for a finished transaction
			// (the Completed state's job): absorbed, not stray.
		case in.msg.StatusCode < 200:
			select {
			case txn.provisionals <- in:
			default:
				// The transaction is not keeping up with a flood of
				// provisionals; dropping one changes nothing.
			}
		default:
			select {
			case txn.final <- in:
			default:
				// A final is already queued: this is its retransmission.
			}
		}
	}
}

// ownRequest reports a request carrying one of this endpoint's branches, and
// fails the transaction it belongs to with a sipLoopError.
func (e *sipEndpoint) ownRequest(req *sipMessage) bool {
	e.mu.Lock()
	defer e.mu.Unlock()
	for _, via := range req.Values("Via") {
		branch := sipViaBranch(via)
		if _, ours := e.branches[branch]; !ours {
			continue
		}
		e.looped.Add(1)
		if txn := e.txns[sipTxnKey(branch, req.Method)]; txn != nil {
			txn.loopOnce.Do(func() { close(txn.looped) })
		}
		return true
	}
	return false
}

func (e *sipEndpoint) register(key string) (*sipClientTxn, error) {
	return e.registerTxn(key, nil)
}

// registerInvite registers an INVITE client transaction whose responses all
// go to onResponse (see sipClientTxn.onResponse). The caller sends the
// INVITE and runs Timer A; its looped channel still reports the INVITE
// coming back to us.
func (e *sipEndpoint) registerInvite(branch string, onResponse func(sipInbound)) (*sipClientTxn, error) {
	return e.registerTxn(sipTxnKey(branch, "INVITE"), onResponse)
}

func (e *sipEndpoint) registerTxn(key string, onResponse func(sipInbound)) (*sipClientTxn, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if _, exists := e.txns[key]; exists {
		return nil, fmt.Errorf("duplicate SIP transaction %q", key)
	}
	txn := &sipClientTxn{
		provisionals: make(chan sipInbound, 16),
		final:        make(chan sipInbound, 1),
		looped:       make(chan struct{}),
		onResponse:   onResponse,
	}
	e.txns[key] = txn
	branch, _, _ := strings.Cut(key, " ")
	e.branches[branch] = struct{}{}
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

	// seeProvisional records a 1xx; drainProvisionals hands the ones still
	// queued to the hook before a final is returned, so it sees every 1xx the
	// dispatcher kept, in arrival order.
	seeProvisional := func(in sipInbound) {
		provisional = in.msg
		if onProvisional != nil {
			onProvisional(in)
		}
	}
	drainProvisionals := func() {
		for {
			select {
			case in := <-txn.provisionals:
				seeProvisional(in)
			default:
				return
			}
		}
	}

	for {
		select {
		case in := <-txn.provisionals:
			seeProvisional(in)

		case in := <-txn.final:
			drainProvisionals()
			return in, nil

		case <-txn.looped:
			return sipInbound{}, &sipLoopError{method: req.Method}

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
			drainProvisionals()
			select {
			case in := <-txn.final:
				return in, nil
			case <-txn.looped:
				return sipInbound{}, &sipLoopError{method: req.Method}
			default:
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
