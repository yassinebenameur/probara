package worker

import (
	"context"
	"errors"
	"net"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// sipTestPeer is the far end of a flow under test: a UDP socket or a TCP
// connection the test drives by hand.
type sipTestPeer struct {
	pc   net.PacketConn // udp
	conn net.Conn       // tcp

	mu       sync.Mutex
	received []sipTestDatagram
	addr     net.Addr
	notify   chan struct{}
}

type sipTestDatagram struct {
	raw string
	at  time.Time
}

// newTestSIPEndpoint returns an endpoint over a fresh UDP flow to a peer that
// records everything it receives and sends nothing unless told to.
func newTestSIPEndpoint(t *testing.T, t1, t2 time.Duration) (*sipEndpoint, *sipTestPeer) {
	t.Helper()
	pc, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("ListenPacket: %v", err)
	}
	peer := &sipTestPeer{pc: pc, notify: make(chan struct{}, 64)}
	t.Cleanup(func() { pc.Close() })
	go func() {
		buf := make([]byte, 65535)
		for {
			n, addr, err := pc.ReadFrom(buf)
			if err != nil {
				return
			}
			peer.mu.Lock()
			peer.addr = addr
			peer.received = append(peer.received, sipTestDatagram{raw: string(buf[:n]), at: time.Now()})
			peer.mu.Unlock()
			select {
			case peer.notify <- struct{}{}:
			default:
			}
		}
	}()

	conn, err := net.Dial("udp", pc.LocalAddr().String())
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	flow := newSIPFlow("udp", conn)
	flow.start()
	e := newSIPEndpoint(flow)
	e.t1, e.t2 = t1, t2
	t.Cleanup(e.Close)
	return e, peer
}

func (p *sipTestPeer) snapshot() []sipTestDatagram {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]sipTestDatagram(nil), p.received...)
}

// waitFor blocks until the peer has received n messages.
func (p *sipTestPeer) waitFor(t *testing.T, n int) []sipTestDatagram {
	t.Helper()
	deadline := time.After(3 * time.Second)
	for {
		if got := p.snapshot(); len(got) >= n {
			return got
		}
		select {
		case <-p.notify:
		case <-deadline:
			t.Fatalf("peer received %d messages, want %d", len(p.snapshot()), n)
		}
	}
}

func (p *sipTestPeer) send(t *testing.T, raw string) {
	t.Helper()
	p.mu.Lock()
	addr := p.addr
	p.mu.Unlock()
	if p.conn != nil {
		if _, err := p.conn.Write([]byte(raw)); err != nil {
			t.Errorf("peer write: %v", err)
		}
		return
	}
	if _, err := p.pc.WriteTo([]byte(raw), addr); err != nil {
		t.Errorf("peer write: %v", err)
	}
}

func testSIPOptions(e *sipEndpoint) *sipMessage {
	return buildSIPRequest(sipRequestParams{
		Method:     "OPTIONS",
		RequestURI: "sip:127.0.0.1",
		Via:        e.flow.viaValue(newSIPBranch()),
		From:       "<sip:probe@monitor.local>",
		FromTag:    "ft",
		To:         "<sip:probe@127.0.0.1>",
		CallID:     "txn-test",
		CSeq:       1,
	})
}

type txnResult struct {
	in  sipInbound
	err error
}

func runTxn(ctx context.Context, e *sipEndpoint, req *sipMessage, onProvisional func(sipInbound)) <-chan txnResult {
	done := make(chan txnResult, 1)
	go func() {
		in, err := e.roundTripNonInvite(ctx, req, onProvisional)
		done <- txnResult{in, err}
	}()
	return done
}

// Timer E: on UDP an unanswered non-INVITE request is resent with the
// interval doubling from T1 and capped at T2 (RFC 3261 §17.1.2.2).
func TestSIPEndpoint_TimerEBackoff(t *testing.T) {
	const t1, t2 = 40 * time.Millisecond, 160 * time.Millisecond
	e, peer := newTestSIPEndpoint(t, t1, t2)
	ctx, cancel := context.WithTimeout(context.Background(), 900*time.Millisecond)
	defer cancel()

	res := <-runTxn(ctx, e, testSIPOptions(e), nil)
	var noResp *sipNoResponseError
	if !errors.As(res.err, &noResp) || !errors.Is(res.err, context.DeadlineExceeded) {
		t.Fatalf("err = %v, want a sipNoResponseError that is a deadline", res.err)
	}

	got := peer.snapshot()
	// Sends at 0, 40, 120, 280, 440, 600, 760 ms: 7 in 900ms.
	if len(got) < 5 || len(got) > 8 {
		t.Fatalf("%d sends in 900ms, want about 7", len(got))
	}
	for i := 1; i < len(got); i++ {
		if got[i].raw != got[0].raw {
			t.Fatalf("retransmission %d differs from the original request", i)
		}
	}
	if e.retransmissions.Load() != int64(len(got)-1) {
		t.Errorf("retransmissions = %d, want %d", e.retransmissions.Load(), len(got)-1)
	}
	// The first gaps double; later ones sit at T2.
	gap := func(i int) time.Duration { return got[i].at.Sub(got[i-1].at) }
	if gap(2) < gap(1)+t1/2 {
		t.Errorf("second interval %v is not about double the first %v", gap(2), gap(1))
	}
	for i := 4; i < len(got); i++ {
		if gap(i) < t2*3/4 || gap(i) > 2*t2 {
			t.Errorf("interval %d = %v, want about T2 (%v)", i, gap(i), t2)
		}
	}
}

// Once a provisional has arrived, Timer E fires at T2 (the server is alive,
// only slow).
func TestSIPEndpoint_ProvisionalSlowsTimerEToT2(t *testing.T) {
	const t1, t2 = 20 * time.Millisecond, 300 * time.Millisecond
	e, peer := newTestSIPEndpoint(t, t1, t2)
	ctx, cancel := context.WithTimeout(context.Background(), 700*time.Millisecond)
	defer cancel()

	var provisionals []int
	var pmu sync.Mutex
	done := runTxn(ctx, e, testSIPOptions(e), func(in sipInbound) {
		pmu.Lock()
		provisionals = append(provisionals, in.msg.StatusCode)
		pmu.Unlock()
	})
	first := peer.waitFor(t, 1)
	peer.send(t, sipReply(first[0].raw, 100, "Trying"))

	res := <-done
	var noResp *sipNoResponseError
	if !errors.As(res.err, &noResp) {
		t.Fatalf("err = %v, want no final response", res.err)
	}
	if noResp.provisional == nil || noResp.provisional.StatusCode != 100 {
		t.Errorf("error does not carry the last provisional: %v", res.err)
	}
	if !strings.Contains(res.err.Error(), "no final response to OPTIONS (last 100 Trying)") {
		t.Errorf("err = %q", res.err)
	}
	pmu.Lock()
	if len(provisionals) != 1 || provisionals[0] != 100 {
		t.Errorf("onProvisional saw %v, want [100]", provisionals)
	}
	pmu.Unlock()
	// Without the T2 rule a 20ms T1 would resend about 8 times in 700ms;
	// with it, one or two resends (the first may beat the 100 Trying).
	if n := len(peer.snapshot()); n > 4 {
		t.Fatalf("%d sends after a provisional, want at most 4 (T2 pacing)", n)
	}
}

// Responses match on top Via branch plus CSeq method; anything else counts
// as stray, and a retransmitted final after completion is absorbed.
func TestSIPEndpoint_MatchesByBranchAndMethod(t *testing.T) {
	e, peer := newTestSIPEndpoint(t, time.Second, 4*time.Second)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	req := testSIPOptions(e)
	done := runTxn(ctx, e, req, nil)
	raw := peer.waitFor(t, 1)[0].raw

	peer.send(t, sipReply(strings.Replace(raw, ";branch=z9hG4bK", ";branch=z9hG4bKother", 1), 401, "Unauthorized"))
	peer.send(t, sipReply(strings.Replace(raw, " OPTIONS\r\n", " REGISTER\r\n", 1), 403, "Forbidden"))
	peer.send(t, sipResponseMsg(500, "No Via"))
	peer.send(t, sipReply(raw, 202, "Accepted"))

	res := <-done
	if res.err != nil {
		t.Fatalf("err = %v", res.err)
	}
	if res.in.msg.StatusCode != 202 {
		t.Fatalf("matched %d, want 202 (strays must be skipped)", res.in.msg.StatusCode)
	}
	if res.in.at.IsZero() {
		t.Error("response has no arrival timestamp")
	}

	// A retransmitted final for the completed transaction is not stray.
	peer.send(t, sipReply(raw, 202, "Accepted"))
	// …and a request still gets answered, which also proves the dispatcher
	// processed the retransmission before it.
	peer.send(t, strings.Join([]string{
		"OPTIONS sip:probara@127.0.0.1 SIP/2.0",
		"Via: SIP/2.0/UDP 127.0.0.1:9;branch=z9hG4bKka",
		"From: <sip:a@b>;tag=1", "To: <sip:c@d>", "Call-ID: ka", "CSeq: 1 OPTIONS",
		"Content-Length: 0", "", "",
	}, "\r\n"))
	peer.waitFor(t, 2)
	if got := e.stray.Load(); got != 3 {
		t.Fatalf("stray = %d, want 3", got)
	}
}

// When the flow ends, a final that arrived just before is still returned;
// with none, the error says the connection closed.
func TestSIPEndpoint_FlowEnd(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()

	run := func(t *testing.T, serverWrite func(peer *sipTestPeer, raw string)) txnResult {
		accepted := make(chan net.Conn, 1)
		go func() {
			c, err := listener.Accept()
			if err == nil {
				accepted <- c
			}
		}()
		conn, err := net.Dial("tcp", listener.Addr().String())
		if err != nil {
			t.Fatal(err)
		}
		flow := newSIPFlow("tcp", conn)
		flow.start()
		e := newSIPEndpoint(flow)
		defer e.Close()
		server := <-accepted
		peer := &sipTestPeer{conn: server}

		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		done := runTxn(ctx, e, testSIPOptions(e), nil)
		buf := make([]byte, 4096)
		n, err := server.Read(buf)
		if err != nil {
			t.Fatal(err)
		}
		serverWrite(peer, string(buf[:n]))
		server.Close()
		return <-done
	}

	t.Run("final then close", func(t *testing.T) {
		res := run(t, func(peer *sipTestPeer, raw string) { peer.send(t, sipReply(raw, 200, "OK")) })
		if res.err != nil || res.in.msg.StatusCode != 200 {
			t.Fatalf("got %v / %v, want the 200", res.in.msg, res.err)
		}
	})
	t.Run("close without final", func(t *testing.T) {
		res := run(t, func(*sipTestPeer, string) {})
		if res.err == nil || classifySIPError(res.err) != "connect" {
			t.Fatalf("err = %v (class %q), want a connect-class read error", res.err, classifySIPError(res.err))
		}
	})
}

func TestSIPEndpoint_RejectsInviteAndDuplicateKeys(t *testing.T) {
	e, _ := newTestSIPEndpoint(t, time.Second, 4*time.Second)
	invite := testSIPOptions(e)
	invite.Method = "INVITE"
	if _, err := e.roundTripNonInvite(context.Background(), invite, nil); err == nil {
		t.Fatal("INVITE accepted as a non-INVITE transaction")
	}
	if _, err := e.register("b OPTIONS"); err != nil {
		t.Fatal(err)
	}
	if _, err := e.register("b OPTIONS"); err == nil {
		t.Fatal("duplicate transaction key accepted")
	}
}

func TestSIPFlow_ViaAndContact(t *testing.T) {
	for _, tc := range []struct {
		transport, requestURI, wantVia, wantContact string
	}{
		{"udp", "sip:a.example.com", "SIP/2.0/UDP 192.0.2.10:40000;branch=z9hG4bKx;rport", "<sip:probara@192.0.2.10:40000;transport=udp>"},
		{"tcp", "sip:a.example.com:5060", "SIP/2.0/TCP 192.0.2.10:40000;branch=z9hG4bKx;rport", "<sip:probara@192.0.2.10:40000;transport=tcp>"},
		{"tls", "sip:a.example.com", "SIP/2.0/TLS 192.0.2.10:40000;branch=z9hG4bKx;rport", "<sip:probara@192.0.2.10:40000;transport=tls>"},
		// A sips: Request-URI needs a sips: Contact (RFC 3261 §8.1.1.8).
		{"tls", "sips:+331@a.example.com", "SIP/2.0/TLS 192.0.2.10:40000;branch=z9hG4bKx;rport", "<sips:probara@192.0.2.10:40000>"},
		{"tls", "SIPS:a.example.com", "SIP/2.0/TLS 192.0.2.10:40000;branch=z9hG4bKx;rport", "<sips:probara@192.0.2.10:40000>"},
	} {
		f := &sipFlow{transport: tc.transport, localIP: net.ParseIP("192.0.2.10"), localPort: 40000}
		if got := f.viaValue("z9hG4bKx"); got != tc.wantVia {
			t.Errorf("%s via = %q, want %q", tc.transport, got, tc.wantVia)
		}
		if got := f.contactValue(tc.requestURI); got != tc.wantContact {
			t.Errorf("%s %s contact = %q, want %q", tc.transport, tc.requestURI, got, tc.wantContact)
		}
	}

	v6 := &sipFlow{transport: "udp", localIP: net.ParseIP("2001:db8::1"), localPort: 5070}
	if got, want := v6.viaValue("z9hG4bKx"), "SIP/2.0/UDP [2001:db8::1]:5070;branch=z9hG4bKx;rport"; got != want {
		t.Errorf("IPv6 via = %q, want %q", got, want)
	}
}

func TestSIPFlow_SendAfterClose(t *testing.T) {
	e, _ := newTestSIPEndpoint(t, time.Second, 4*time.Second)
	e.Close()
	if err := e.flow.send([]byte("x")); !errors.Is(err, net.ErrClosed) {
		t.Fatalf("send after Close = %v, want net.ErrClosed", err)
	}
	e.Close() // idempotent
}

// A burst of provisionals larger than a transaction's buffer must never cost
// it the final response: on a stream nothing retransmits that final, so a
// dropped one would read as "no final response" from a server that answered.
func TestSIPEndpoint_FinalSurvivesProvisionalFlood(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	accepted := make(chan net.Conn, 1)
	go func() {
		if c, err := listener.Accept(); err == nil {
			accepted <- c
		}
	}()
	conn, err := net.Dial("tcp", listener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	flow := newSIPFlow("tcp", conn)
	flow.start()
	e := newSIPEndpoint(flow)
	defer e.Close()
	server := <-accepted
	defer server.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	var provisionals atomic.Int32
	done := runTxn(ctx, e, testSIPOptions(e), func(sipInbound) {
		provisionals.Add(1)
		time.Sleep(5 * time.Millisecond) // a transaction slower than the burst
	})
	buf := make([]byte, 4096)
	n, err := server.Read(buf)
	if err != nil {
		t.Fatal(err)
	}
	raw := string(buf[:n])
	var burst strings.Builder
	for range 20 {
		burst.WriteString(sipReply(raw, 100, "Trying"))
	}
	burst.WriteString(sipReply(raw, 200, "OK"))
	if _, err := server.Write([]byte(burst.String())); err != nil {
		t.Fatal(err)
	}

	res := <-done
	if res.err != nil {
		t.Fatalf("err = %v, want the 200 after the burst", res.err)
	}
	if res.in.msg.StatusCode != 200 {
		t.Fatalf("final = %d, want 200", res.in.msg.StatusCode)
	}
	if got := provisionals.Load(); got < 1 || got > 20 {
		t.Fatalf("onProvisional saw %d provisionals, want 1..20", got)
	}
}
