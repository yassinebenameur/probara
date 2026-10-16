package worker

import (
	"bufio"
	"context"
	"crypto/md5" //nolint:gosec // RFC 3261 digest auth in a test server
	"crypto/tls"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/yassinebenameur/probara/shared/models"
)

// --- fake UAS -------------------------------------------------------------

// fakeUAS is a scripted SIP callee over UDP, TCP or TLS. It records every
// message it receives (requests and the client's answers to its own
// requests), can ignore the Nth datagram (a lost packet), and hands each
// message it keeps to the test's handler, which answers with raw strings,
// now or later (sendAfter).
type fakeUAS struct {
	t         *testing.T
	transport string
	host      string
	port      int
	handler   func(u *fakeUAS, msg string)
	dropNth   map[int]bool

	mu       sync.Mutex
	received []fakeUASMsg
	accepted int

	pc      net.PacketConn
	peer    net.Addr
	conn    net.Conn
	writeMu sync.Mutex
}

type fakeUASMsg struct {
	raw     string
	at      time.Time
	dropped bool
}

func startFakeUAS(t *testing.T, transport string, handler func(u *fakeUAS, msg string)) *fakeUAS {
	t.Helper()
	u := &fakeUAS{t: t, transport: transport, handler: handler, dropNth: map[int]bool{}}
	switch transport {
	case "udp":
		pc, err := net.ListenPacket("udp", "127.0.0.1:0")
		if err != nil {
			t.Fatalf("ListenPacket: %v", err)
		}
		u.pc = pc
		t.Cleanup(func() { pc.Close() })
		u.host, u.port = splitTestHostPort(t, pc.LocalAddr().String())
		go func() {
			buf := make([]byte, 65535)
			for {
				n, addr, err := pc.ReadFrom(buf)
				if err != nil {
					return
				}
				u.mu.Lock()
				u.peer = addr
				u.mu.Unlock()
				u.receive(string(buf[:n]))
			}
		}()
	case "tcp", "tls":
		var (
			listener net.Listener
			err      error
		)
		if transport == "tls" {
			cert := newSelfSignedTestCert(t, time.Now().Add(30*24*time.Hour))
			listener, err = tls.Listen("tcp", "127.0.0.1:0", &tls.Config{Certificates: []tls.Certificate{cert}})
		} else {
			listener, err = net.Listen("tcp", "127.0.0.1:0")
		}
		if err != nil {
			t.Fatalf("Listen: %v", err)
		}
		t.Cleanup(func() { listener.Close() })
		u.host, u.port = splitTestHostPort(t, listener.Addr().String())
		go func() {
			for {
				conn, err := listener.Accept()
				if err != nil {
					return
				}
				u.mu.Lock()
				u.accepted++
				u.conn = conn
				u.mu.Unlock()
				go func(conn net.Conn) {
					defer conn.Close()
					reader := bufio.NewReader(conn)
					for {
						msg, err := readSIPRequestForTest(reader)
						if err != nil {
							return
						}
						u.receive(msg)
					}
				}(conn)
			}
		}()
	default:
		t.Fatalf("unknown transport %q", transport)
	}
	return u
}

func (u *fakeUAS) receive(raw string) {
	u.mu.Lock()
	n := len(u.received) + 1
	dropped := u.dropNth[n]
	u.received = append(u.received, fakeUASMsg{raw: raw, at: time.Now(), dropped: dropped})
	u.mu.Unlock()
	if !dropped && u.handler != nil {
		u.handler(u, raw)
	}
}

// drop makes the UAS ignore the nth message it receives (1-based).
func (u *fakeUAS) drop(n int) {
	u.mu.Lock()
	defer u.mu.Unlock()
	u.dropNth[n] = true
}

func (u *fakeUAS) send(raw string) {
	u.mu.Lock()
	peer, conn := u.peer, u.conn
	u.mu.Unlock()
	if u.transport == "udp" {
		if peer != nil {
			_, _ = u.pc.WriteTo([]byte(raw), peer)
		}
		return
	}
	if conn != nil {
		u.writeMu.Lock()
		_, _ = conn.Write([]byte(raw))
		u.writeMu.Unlock()
	}
}

// hangUp closes the stream connection (TCP/TLS): a peer that writes its
// last response and goes away at once.
func (u *fakeUAS) hangUp() {
	u.mu.Lock()
	conn := u.conn
	u.mu.Unlock()
	if conn != nil {
		u.writeMu.Lock()
		conn.Close()
		u.writeMu.Unlock()
	}
}

func (u *fakeUAS) sendAfter(d time.Duration, raw string) {
	time.AfterFunc(d, func() { u.send(raw) })
}

// requests returns the received requests of a method, dropped ones included.
func (u *fakeUAS) requests(method string) []fakeUASMsg {
	u.mu.Lock()
	defer u.mu.Unlock()
	var out []fakeUASMsg
	for _, m := range u.received {
		if fMethod(m.raw) == method {
			out = append(out, m)
		}
	}
	return out
}

// responses returns the client's responses (to requests the UAS sent).
func (u *fakeUAS) responses() []fakeUASMsg {
	u.mu.Lock()
	defer u.mu.Unlock()
	var out []fakeUASMsg
	for _, m := range u.received {
		if strings.HasPrefix(m.raw, "SIP/2.0 ") {
			out = append(out, m)
		}
	}
	return out
}

func (u *fakeUAS) total() int {
	u.mu.Lock()
	defer u.mu.Unlock()
	return len(u.received)
}

// waitRequests waits until at least n requests of method arrived.
func (u *fakeUAS) waitRequests(method string, n int, within time.Duration) []fakeUASMsg {
	u.t.Helper()
	deadline := time.Now().Add(within)
	for {
		got := u.requests(method)
		if len(got) >= n {
			return got
		}
		if time.Now().After(deadline) {
			u.t.Fatalf("got %d %s requests within %s, want %d", len(got), method, within, n)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func (u *fakeUAS) contact() string {
	return fmt.Sprintf("Contact: <sip:callee@127.0.0.1:%d>", u.port)
}

// fMethod is a raw request's method ("" for a response).
func fMethod(raw string) string {
	if strings.HasPrefix(raw, "SIP/2.0 ") {
		return ""
	}
	method, _, _ := strings.Cut(raw, " ")
	return method
}

// fRequestURI is a raw request's Request-URI.
func fRequestURI(raw string) string {
	line, _, _ := strings.Cut(raw, "\r\n")
	parts := strings.Split(line, " ")
	if len(parts) != 3 {
		return ""
	}
	return parts[1]
}

func fBranch(raw string) string { return sipViaBranch(sipTestHeader(raw, "Via")) }

// fHeaders returns every value of a header in a raw message.
func fHeaders(raw, name string) []string {
	msg, err := parseSIPMessage([]byte(raw))
	if err != nil {
		return nil
	}
	return msg.Values(name)
}

func fBody(raw string) string {
	_, body, _ := strings.Cut(raw, "\r\n\r\n")
	return body
}

// uasReply answers req: Via, From, Call-ID and CSeq echoed, To echoed with
// toTag added when it has none, then extra headers and an optional SDP body.
func uasReply(req string, code int, reason, toTag, body string, extra ...string) string {
	lines := []string{fmt.Sprintf("SIP/2.0 %d %s", code, reason)}
	for _, line := range strings.Split(req, "\r\n") {
		if line == "" {
			break
		}
		name, _, _ := strings.Cut(line, ":")
		switch strings.ToLower(strings.TrimSpace(name)) {
		case "via", "from", "call-id", "cseq":
			lines = append(lines, line)
		case "to":
			if toTag != "" && !strings.Contains(line, ";tag=") {
				line += ";tag=" + toTag
			}
			lines = append(lines, line)
		}
	}
	lines = append(lines, extra...)
	if body != "" {
		lines = append(lines, "Content-Type: application/sdp")
	}
	lines = append(lines, fmt.Sprintf("Content-Length: %d", len(body)), "")
	return strings.Join(lines, "\r\n") + "\r\n" + body
}

const uasSDPAnswer = "v=0\r\no=- 1 1 IN IP4 198.51.100.7\r\ns=-\r\nc=IN IP4 198.51.100.7\r\nt=0 0\r\n" +
	"m=audio 40012 RTP/AVP 8 101\r\na=rtpmap:8 PCMA/8000\r\na=rtpmap:101 telephone-event/8000\r\na=sendrecv\r\n"

// uasBye is a BYE the callee sends in the dialog of invite (its To tag
// toTag), to the client's Contact.
func uasBye(invite, toTag, fromTagOverride string) string {
	contact := sipTestHeader(invite, "Contact")
	na, _ := parseSIPNameAddr(contact)
	from := sipTestHeader(invite, "To") + ";tag=" + toTag
	to := sipTestHeader(invite, "From")
	if fromTagOverride != "" {
		from = sipTestHeader(invite, "To") + ";tag=" + fromTagOverride
	}
	return strings.Join([]string{
		"BYE " + na.URI + " SIP/2.0",
		"Via: SIP/2.0/UDP 127.0.0.1:5999;branch=z9hG4bKuasbye" + sipRandomToken(3),
		"Max-Forwards: 70",
		"From: " + from,
		"To: " + to,
		"Call-ID: " + sipTestHeader(invite, "Call-ID"),
		"CSeq: 1 BYE",
		"Content-Length: 0",
		"", "",
	}, "\r\n")
}

// firstTimeBranch reports whether a request's branch is new (a retransmitted
// INVITE keeps its branch), recording it.
type branchSet struct {
	mu   sync.Mutex
	seen map[string]bool
}

func (b *branchSet) first(raw string) bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.seen == nil {
		b.seen = map[string]bool{}
	}
	br := fBranch(raw)
	if b.seen[br] {
		return false
	}
	b.seen[br] = true
	return true
}

// --- harness --------------------------------------------------------------

func newInviteTestChecker() *SIPChecker {
	c := NewSIPChecker(false, nil)
	c.ConfigureCalls(true, nil)
	c.callTimers = sipCallTimers{teardown: 1500 * time.Millisecond, linger: 300 * time.Millisecond}
	return c
}

func inviteConfig(u *fakeUAS, call map[string]any, extra map[string]any) map[string]any {
	cfg := map[string]any{
		"host": "127.0.0.1", "port": u.port, "transport": u.transport, "method": "invite", "call": call,
	}
	if u.transport == "tls" {
		cfg["tls_skip_verify"] = true
	}
	for k, v := range extra {
		cfg[k] = v
	}
	return cfg
}

func runInvite(t *testing.T, c *SIPChecker, cfg map[string]any, timeoutSeconds int) (CheckResult, *sipMetrics) {
	t.Helper()
	return runInviteCtx(t, context.Background(), c, cfg, timeoutSeconds)
}

func runInviteCtx(t *testing.T, ctx context.Context, c *SIPChecker, cfg map[string]any, timeoutSeconds int) (CheckResult, *sipMetrics) {
	t.Helper()
	t.Setenv("SIP_LOCALHOST_AS_HOST_GATEWAY", "")
	raw, err := json.Marshal(cfg)
	if err != nil {
		t.Fatal(err)
	}
	result := c.Check(ctx, raw, timeoutSeconds)
	var env sipMetricsEnvelope
	if len(result.MetricsData) > 0 {
		if err := json.Unmarshal(result.MetricsData, &env); err != nil {
			t.Fatalf("metrics_data: %v", err)
		}
	}
	return result, env.SIP
}

func resultMsg(r CheckResult) string {
	if r.ErrorMessage == nil {
		return ""
	}
	return *r.ErrorMessage
}

func requireStatus(t *testing.T, r CheckResult, status string, fragments ...string) {
	t.Helper()
	if r.Status != status {
		t.Fatalf("Status = %q (%s), want %q", r.Status, resultMsg(r), status)
	}
	for _, f := range fragments {
		if !strings.Contains(resultMsg(r), f) {
			t.Fatalf("ErrorMessage = %q, want it to contain %q", resultMsg(r), f)
		}
	}
}

func requireHTTPStatus(t *testing.T, r CheckResult, code int) {
	t.Helper()
	if r.HTTPStatus == nil || *r.HTTPStatus != code {
		got := "nil"
		if r.HTTPStatus != nil {
			got = strconv.Itoa(*r.HTTPStatus)
		}
		t.Fatalf("HTTPStatus = %s, want %d", got, code)
	}
}

// answeringUAS answers INVITE with 100, 180 (+ringDelay; none when
// negative), 200 + SDP (+answerDelay), and every BYE with 200.
func answeringUAS(ringDelay, answerDelay time.Duration, extra200 ...string) func(u *fakeUAS, msg string) {
	var invites branchSet
	return func(u *fakeUAS, msg string) {
		switch fMethod(msg) {
		case "INVITE":
			if !invites.first(msg) {
				return
			}
			u.send(uasReply(msg, 100, "Trying", "", ""))
			if ringDelay >= 0 {
				u.sendAfter(ringDelay, uasReply(msg, 180, "Ringing", "srv1", "", u.contact()))
			}
			u.sendAfter(answerDelay, uasReply(msg, 200, "OK", "srv1", uasSDPAnswer, append([]string{u.contact()}, extra200...)...))
		case "BYE":
			u.send(uasReply(msg, 200, "OK", "", ""))
		}
	}
}

// --- tests ----------------------------------------------------------------

// 1. The basic answered call over each transport: 100 → 180 → 200 + SDP,
// ACK (its own transaction: new branch, CSeq n ACK, the To tag, the Contact
// as Request-URI), hold, then BYE with CSeq n+1.
func TestSIPInvite_AnsweredCall(t *testing.T) {
	for _, transport := range []string{"udp", "tcp", "tls"} {
		t.Run(transport, func(t *testing.T) {
			u := startFakeUAS(t, transport, answeringUAS(50*time.Millisecond, 120*time.Millisecond))
			result, m := runInvite(t, newInviteTestChecker(), inviteConfig(u, map[string]any{"number": "+33123456789", "hold_ms": 200}, nil), 5)
			requireStatus(t, result, "success")
			requireHTTPStatus(t, result, 200)
			if *result.LatencyMs < 120 || *result.LatencyMs > 3000 {
				t.Fatalf("latency_ms = %d, want the 2xx time (≥120ms)", *result.LatencyMs)
			}

			invite := u.requests("INVITE")[0].raw
			ack := u.waitRequests("ACK", 1, 2*time.Second)[0]
			bye := u.waitRequests("BYE", 1, 2*time.Second)[0]
			if got := sipTestHeader(ack.raw, "CSeq"); got != "1 ACK" {
				t.Fatalf("ACK CSeq = %q, want 1 ACK", got)
			}
			if fBranch(ack.raw) == fBranch(invite) {
				t.Fatal("2xx ACK reused the INVITE branch; it is its own transaction")
			}
			if !strings.Contains(sipTestHeader(ack.raw, "To"), "tag=srv1") {
				t.Fatalf("ACK To = %q, want the dialog's To tag", sipTestHeader(ack.raw, "To"))
			}
			wantTarget := fmt.Sprintf("sip:callee@127.0.0.1:%d", u.port)
			if fRequestURI(ack.raw) != wantTarget || fRequestURI(bye.raw) != wantTarget {
				t.Fatalf("ACK/BYE Request-URI = %q/%q, want the Contact %q", fRequestURI(ack.raw), fRequestURI(bye.raw), wantTarget)
			}
			if got := sipTestHeader(bye.raw, "CSeq"); got != "2 BYE" {
				t.Fatalf("BYE CSeq = %q, want 2 BYE", got)
			}
			if held := bye.at.Sub(ack.at); held < 180*time.Millisecond {
				t.Fatalf("BYE came %s after the ACK, want the 200ms hold", held)
			}

			wantScheme := "sip:"
			if transport == "tls" {
				wantScheme = "sips:"
			}
			if got := fRequestURI(invite); got != wantScheme+"+33123456789@127.0.0.1;user=phone" {
				t.Fatalf("INVITE Request-URI = %q", got)
			}
			if c := sipTestHeader(invite, "Contact"); !strings.HasPrefix(c, "<"+wantScheme+"probara@127.0.0.1:") {
				t.Fatalf("INVITE Contact = %q, want a %s Contact with the real local address", c, wantScheme)
			}

			call := m.Call
			if call == nil || !call.Answered || call.Mode != "answer" {
				t.Fatalf("call metrics = %+v", call)
			}
			if call.PDDMs == nil || *call.PDDMs < 50 || *call.PDDMs > 2000 || call.PDDSource != "180" {
				t.Fatalf("pdd = %v (%s), want ≥50ms from the 180", call.PDDMs, call.PDDSource)
			}
			if call.AnswerMs == nil || *call.AnswerMs < 120 || *call.AnswerMs > 3000 || call.TryingMs == nil {
				t.Fatalf("answer_ms = %v, trying_ms = %v", call.AnswerMs, call.TryingMs)
			}
			if call.Teardown != "bye_ok" || call.TeardownStatus != 200 || call.ByeMs == nil {
				t.Fatalf("teardown = %q/%d bye_ms=%v, want bye_ok/200", call.Teardown, call.TeardownStatus, call.ByeMs)
			}
			if call.SDPAnswer == nil || call.SDPAnswer.Codec != "PCMA/8000" || call.SDPAnswer.RemoteMedia != "198.51.100.7:40012" {
				t.Fatalf("sdp_answer = %+v", call.SDPAnswer)
			}
			if call.AnsweredBy == nil || call.AnsweredBy.ContactHost != "127.0.0.1" {
				t.Fatalf("answered_by = %+v", call.AnsweredBy)
			}
			if len(call.Provisional) != 2 || call.Provisional[0].Code != 100 || call.Provisional[1].Code != 180 {
				t.Fatalf("provisional = %+v", call.Provisional)
			}
			if m.Method != "INVITE" || m.StatusCode != 200 || m.CallID == "" || m.Timing.TotalMs < *result.LatencyMs {
				t.Fatalf("envelope = %+v", m)
			}
		})
	}
}

// 2. The offer is valid SDP, every payload type has an rtpmap, and its port
// is really bound for the duration of the call.
func TestSIPInvite_SDPOfferIsValidAndBound(t *testing.T) {
	var portBusy atomic.Int32
	var invites branchSet
	u := startFakeUAS(t, "udp", func(u *fakeUAS, msg string) {
		if fMethod(msg) == "INVITE" && invites.first(msg) {
			m := regexp.MustCompile(`m=audio (\d+) `).FindStringSubmatch(fBody(msg))
			if len(m) == 2 {
				port, _ := strconv.Atoi(m[1])
				if conn, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.ParseIP("127.0.0.1"), Port: port}); err != nil {
					portBusy.Store(int32(port))
				} else {
					conn.Close()
				}
			}
			u.send(uasReply(msg, 486, "Busy Here", "srv1", ""))
		}
	})
	runInvite(t, newInviteTestChecker(), inviteConfig(u, map[string]any{"number": "100"}, nil), 5)

	invite := u.requests("INVITE")[0].raw
	if ct := sipTestHeader(invite, "Content-Type"); ct != "application/sdp" {
		t.Fatalf("Content-Type = %q", ct)
	}
	body := strings.Split(strings.TrimRight(fBody(invite), "\r\n"), "\r\n")
	order := []string{"v=", "o=", "s=", "c=", "t=", "m="}
	for i, prefix := range order {
		if !strings.HasPrefix(body[i], prefix) {
			t.Fatalf("SDP line %d = %q, want %s… (lines %q)", i, body[i], prefix, body)
		}
	}
	m := strings.Fields(body[5])
	if len(m) < 4 || m[2] != "RTP/AVP" || m[1] == "0" {
		t.Fatalf("m= line = %q", body[5])
	}
	port, _ := strconv.Atoi(m[1])
	if port%2 != 0 {
		t.Fatalf("RTP port %d is odd", port)
	}
	for _, pt := range m[3:] {
		if !strings.Contains(fBody(invite), "a=rtpmap:"+pt+" ") {
			t.Fatalf("no rtpmap for payload type %s", pt)
		}
	}
	if int(portBusy.Load()) != port {
		t.Fatalf("the offered RTP port %d was not bound during the call", port)
	}
	if !strings.Contains(body[1], "IN IP4 127.0.0.1") || body[3] != "c=IN IP4 127.0.0.1" {
		t.Fatalf("o=/c= = %q / %q, want the flow's local address", body[1], body[3])
	}
}

// digestFields parses an Authorization value's parameters.
func digestFields(value string) map[string]string {
	return parseSIPAuthParams(strings.TrimPrefix(strings.TrimSpace(value), "Digest "))
}

// verifyDigest checks a credentials value the way a server does (MD5,
// qop=auth): the response over method:uri with the password.
func verifyDigest(value, method, password string) bool {
	f := digestFields(value)
	h := func(s string) string {
		sum := md5.Sum([]byte(s)) //nolint:gosec // RFC 3261 digest auth
		return hex.EncodeToString(sum[:])
	}
	ha1 := h(f["username"] + ":" + f["realm"] + ":" + password)
	ha2 := h(method + ":" + f["uri"])
	return f["response"] == h(strings.Join([]string{ha1, f["nonce"], f["nc"], f["cnonce"], "auth", ha2}, ":"))
}

// 3 + 4. A challenged INVITE: the 4xx is ACKed in its transaction (same
// branch, no credentials), the retry is a new transaction (CSeq+1, new
// branch) whose digest is over INVITE and the Request-URI, and the 2xx ACK
// repeats the INVITE's credentials.
func TestSIPInvite_DigestChallenge(t *testing.T) {
	for _, tc := range []struct {
		code            int
		challengeHeader string
		credHeader      string
	}{
		{401, "WWW-Authenticate", "Authorization"},
		{407, "Proxy-Authenticate", "Proxy-Authorization"},
	} {
		t.Run(strconv.Itoa(tc.code), func(t *testing.T) {
			var invites branchSet
			n := 0
			u := startFakeUAS(t, "udp", func(u *fakeUAS, msg string) {
				switch fMethod(msg) {
				case "INVITE":
					if !invites.first(msg) {
						return
					}
					n++
					if n == 1 {
						u.send(uasReply(msg, tc.code, "Auth", "srv0", "", tc.challengeHeader+`: Digest realm="trunk", nonce="n1", qop="auth"`))
						return
					}
					u.send(uasReply(msg, 200, "OK", "srv1", uasSDPAnswer, u.contact()))
				case "BYE":
					u.send(uasReply(msg, 200, "OK", "", ""))
				}
			})
			result, m := runInvite(t, newInviteTestChecker(), inviteConfig(u, map[string]any{"number": "100"}, map[string]any{"username": "trunk", "password": "s3cret"}), 5)
			requireStatus(t, result, "success")

			invites2 := u.waitRequests("INVITE", 2, 2*time.Second)
			first, second := invites2[0].raw, invites2[1].raw
			acks := u.waitRequests("ACK", 2, 2*time.Second)
			challengeAck, answerAck := acks[0].raw, acks[1].raw
			if fBranch(challengeAck) != fBranch(first) || sipTestHeader(challengeAck, "CSeq") != "1 ACK" {
				t.Fatalf("challenge ACK branch/CSeq = %s/%s, want the first INVITE's", fBranch(challengeAck), sipTestHeader(challengeAck, "CSeq"))
			}
			if sipTestHeader(challengeAck, tc.credHeader) != "" || sipTestHeader(challengeAck, "Authorization") != "" {
				t.Fatal("the in-transaction ACK of the challenge carries credentials")
			}
			if sipTestHeader(second, "CSeq") != "2 INVITE" || fBranch(second) == fBranch(first) {
				t.Fatalf("retry CSeq/branch = %s/%s, want 2 INVITE on a new branch", sipTestHeader(second, "CSeq"), fBranch(second))
			}
			if sipTestHeader(second, "Call-ID") != sipTestHeader(first, "Call-ID") || sipHeaderTag(sipTestHeader(second, "From")) != sipHeaderTag(sipTestHeader(first, "From")) {
				t.Fatal("the retry changed the Call-ID or From tag")
			}
			cred := sipTestHeader(second, tc.credHeader)
			if !verifyDigest(cred, "INVITE", "s3cret") || digestFields(cred)["uri"] != fRequestURI(second) {
				t.Fatalf("retry credentials do not verify over INVITE:%s: %s", fRequestURI(second), cred)
			}
			// D22: the 2xx ACK carries the INVITE's credentials.
			if got := sipTestHeader(answerAck, tc.credHeader); got != cred {
				t.Fatalf("2xx ACK %s = %q, want the INVITE's %q", tc.credHeader, got, cred)
			}
			if m.Auth == nil || m.Auth.Challenge != tc.code || m.Auth.Rounds != 1 || m.Auth.AuthMs == nil || m.Auth.Algorithm != "MD5" {
				t.Fatalf("auth metrics = %+v", m.Auth)
			}
		})
	}
}

// 3. A stale nonce earns a second round; a second plain challenge fails the
// check as an authentication failure. Two realms get two headers.
func TestSIPInvite_DigestStaleAndRealms(t *testing.T) {
	scripted := func(second string) (*fakeUAS, func() []fakeUASMsg) {
		var invites branchSet
		n := 0
		u := startFakeUAS(t, "udp", func(u *fakeUAS, msg string) {
			switch fMethod(msg) {
			case "INVITE":
				if !invites.first(msg) {
					return
				}
				n++
				switch n {
				case 1:
					u.send(uasReply(msg, 407, "Proxy Auth", "p", "",
						`Proxy-Authenticate: Digest realm="edge", nonce="e1", qop="auth"`,
						`Proxy-Authenticate: Digest realm="core", nonce="c1", qop="auth"`))
				case 2:
					u.send(uasReply(msg, 407, "Proxy Auth", "p", "", second))
				default:
					u.send(uasReply(msg, 200, "OK", "srv1", uasSDPAnswer, u.contact()))
				}
			case "BYE":
				u.send(uasReply(msg, 200, "OK", "", ""))
			}
		})
		return u, func() []fakeUASMsg { return u.requests("INVITE") }
	}

	t.Run("stale", func(t *testing.T) {
		u, invites := scripted(`Proxy-Authenticate: Digest realm="edge", nonce="e2", qop="auth", stale=true`)
		result, m := runInvite(t, newInviteTestChecker(), inviteConfig(u, map[string]any{"number": "100"}, map[string]any{"username": "u", "password": "p"}), 5)
		requireStatus(t, result, "success")
		all := invites()
		if len(all) != 3 {
			t.Fatalf("INVITEs = %d, want 3", len(all))
		}
		if creds := fHeaders(all[1].raw, "Proxy-Authorization"); len(creds) != 2 {
			t.Fatalf("Proxy-Authorization headers = %d, want one per realm", len(creds))
		}
		// The stale retry answers edge's new nonce and keeps core's
		// credentials (its next nonce count), in first-seen order; the 2xx
		// ACK repeats both.
		third := fHeaders(all[2].raw, "Proxy-Authorization")
		if len(third) != 2 {
			t.Fatalf("stale retry Proxy-Authorization = %q, want edge and core", third)
		}
		if f := digestFields(third[0]); f["realm"] != "edge" || f["nonce"] != "e2" || f["nc"] != "00000001" {
			t.Fatalf("stale retry edge = %s, want the new nonce at nc=1", third[0])
		}
		if f := digestFields(third[1]); f["realm"] != "core" || f["nonce"] != "c1" || f["nc"] != "00000002" || !verifyDigest(third[1], "INVITE", "p") {
			t.Fatalf("stale retry core = %s, want the cached realm at nc=2", third[1])
		}
		acks := u.waitRequests("ACK", 3, 2*time.Second)
		if got := fHeaders(acks[2].raw, "Proxy-Authorization"); len(got) != 2 || got[0] != third[0] || got[1] != third[1] {
			t.Fatalf("2xx ACK Proxy-Authorization = %q, want the INVITE's %q", got, third)
		}
		if m.Auth.Rounds != 2 {
			t.Fatalf("auth rounds = %d, want 2", m.Auth.Rounds)
		}
	})
	t.Run("further hop", func(t *testing.T) {
		// The UAS's realm answered, a proxy's new realm challenges next: one
		// round is all the probe does, and the wording says so instead of
		// blaming the password.
		var invites branchSet
		n := 0
		u := startFakeUAS(t, "udp", func(u *fakeUAS, msg string) {
			if fMethod(msg) != "INVITE" || !invites.first(msg) {
				return
			}
			n++
			if n == 1 {
				u.send(uasReply(msg, 401, "Unauthorized", "p", "", `WWW-Authenticate: Digest realm="uas", nonce="u1", qop="auth"`))
				return
			}
			u.send(uasReply(msg, 407, "Proxy Auth", "p", "", `Proxy-Authenticate: Digest realm="proxy", nonce="p1", qop="auth"`))
		})
		result, _ := runInvite(t, newInviteTestChecker(), inviteConfig(u, map[string]any{"number": "100"}, map[string]any{"username": "u", "password": "p"}), 5)
		requireStatus(t, result, "failure", `auth: 407 Proxy Auth challenged realm "proxy" after the first round`, "multi-hop authentication")
		if strings.Contains(resultMsg(result), "authentication failed") {
			t.Fatalf("message = %q blames the password", resultMsg(result))
		}
		if len(u.requests("INVITE")) != 2 {
			t.Fatalf("INVITEs = %d, want 2", len(u.requests("INVITE")))
		}
	})
	t.Run("not stale", func(t *testing.T) {
		u, invites := scripted(`Proxy-Authenticate: Digest realm="edge", nonce="e2", qop="auth"`)
		result, _ := runInvite(t, newInviteTestChecker(), inviteConfig(u, map[string]any{"number": "100"}, map[string]any{"username": "u", "password": "p"}), 5)
		requireStatus(t, result, "failure", "auth: authentication failed: 407")
		requireHTTPStatus(t, result, 407)
		if len(invites()) != 2 {
			t.Fatalf("INVITEs = %d, want 2", len(invites()))
		}
	})
	t.Run("no credentials", func(t *testing.T) {
		u, _ := scripted("")
		result, _ := runInvite(t, newInviteTestChecker(), inviteConfig(u, map[string]any{"number": "100"}, nil), 5)
		requireStatus(t, result, "failure", "auth: 407", "no credentials are configured")
	})
}

// 5. A busy line fails the call, with the Q.850 cause; expected_status 486
// makes it the success a busy test line should give.
func TestSIPInvite_BusyAndExpectedStatus(t *testing.T) {
	handler := func(u *fakeUAS, msg string) {
		if fMethod(msg) == "INVITE" {
			u.send(uasReply(msg, 486, "Busy Here", "srv1", "", `Reason: Q.850;cause=17;text="User busy"`))
		}
	}
	u := startFakeUAS(t, "udp", handler)
	result, m := runInvite(t, newInviteTestChecker(), inviteConfig(u, map[string]any{"number": "100"}, nil), 5)
	requireStatus(t, result, "failure", "status: 486 Busy Here (Q.850 17)")
	requireHTTPStatus(t, result, 486)
	if m.ReasonHeader == nil || m.ReasonHeader.Cause == nil || *m.ReasonHeader.Cause != 17 || m.Call.Teardown != "none" {
		t.Fatalf("reason/teardown = %+v / %q", m.ReasonHeader, m.Call.Teardown)
	}
	ack := u.waitRequests("ACK", 1, time.Second)[0].raw
	if fBranch(ack) != fBranch(u.requests("INVITE")[0].raw) {
		t.Fatal("486 not ACKed in its transaction")
	}

	u2 := startFakeUAS(t, "udp", handler)
	result, _ = runInvite(t, newInviteTestChecker(), inviteConfig(u2, map[string]any{"number": "100"}, map[string]any{"expected_status": 486}), 5)
	requireStatus(t, result, "success")
	requireHTTPStatus(t, result, 486)
}

// A stream peer that sends its final (or its answer) and closes at once:
// the response the dispatcher queued before the flow ended decides the call,
// never the closed connection, whichever the call goroutine sees first. A
// slow event handler and a few provisionals ahead of the final leave both
// the final's event and the closed flow ready together, which without the
// drain decided the call at random.
func TestSIPInvite_FinalThenHangUp(t *testing.T) {
	slowChecker := func() *SIPChecker {
		c := newInviteTestChecker()
		c.callHook = func(stage string) {
			if stage == "event" {
				time.Sleep(3 * time.Millisecond)
			}
		}
		return c
	}
	progress := func(u *fakeUAS, invite string) {
		u.send(uasReply(invite, 100, "Trying", "", ""))
		for range 4 {
			u.send(uasReply(invite, 183, "Session Progress", "srv1", ""))
		}
	}
	busy := func(u *fakeUAS, msg string) {
		if fMethod(msg) == "INVITE" {
			progress(u, msg)
			u.send(uasReply(msg, 486, "Busy Here", "srv1", ""))
			u.hangUp()
		}
	}
	t.Run("expected busy", func(t *testing.T) {
		u := startFakeUAS(t, "tcp", busy)
		result, _ := runInvite(t, slowChecker(), inviteConfig(u, map[string]any{"number": "100"}, map[string]any{"expected_status": 486}), 5)
		requireStatus(t, result, "success")
		requireHTTPStatus(t, result, 486)
	})
	t.Run("busy", func(t *testing.T) {
		u := startFakeUAS(t, "tcp", busy)
		result, _ := runInvite(t, slowChecker(), inviteConfig(u, map[string]any{"number": "100"}, nil), 5)
		requireStatus(t, result, "failure", "status: 486 Busy Here")
		requireHTTPStatus(t, result, 486)
	})
	t.Run("answered", func(t *testing.T) {
		u := startFakeUAS(t, "tcp", func(u *fakeUAS, msg string) {
			if fMethod(msg) == "INVITE" {
				progress(u, msg)
				u.send(uasReply(msg, 200, "OK", "srv1", uasSDPAnswer, u.contact()))
				u.hangUp()
			}
		})
		result, m := runInvite(t, slowChecker(), inviteConfig(u, map[string]any{"number": "100"}, nil), 5)
		requireStatus(t, result, "success")
		if !m.Call.Answered {
			t.Fatal("answered = false, want the 200 sent before the close")
		}
	})
	t.Run("cancelled", func(t *testing.T) {
		var invite atomic.Value
		u := startFakeUAS(t, "tcp", func(u *fakeUAS, msg string) {
			switch fMethod(msg) {
			case "INVITE":
				invite.Store(msg)
				u.send(uasReply(msg, 180, "Ringing", "srv1", ""))
			case "CANCEL":
				inv := invite.Load().(string)
				for range 4 {
					u.send(uasReply(inv, 183, "Session Progress", "srv1", ""))
				}
				u.send(uasReply(msg, 200, "OK", "srv1", ""))
				u.send(uasReply(inv, 487, "Request Terminated", "srv1", ""))
				u.hangUp()
			}
		})
		result, m := runInvite(t, slowChecker(), inviteConfig(u, map[string]any{"number": "100", "mode": "ring"}, nil), 5)
		requireStatus(t, result, "success")
		if m.Call.Teardown != "cancelled" {
			t.Fatalf("teardown = %q, want cancelled", m.Call.Teardown)
		}
		for _, w := range m.Warnings {
			if strings.Contains(w, "CANCEL unconfirmed") {
				t.Fatalf("warnings = %q", m.Warnings)
			}
		}
	})
}

// cancellingUAS rings and then waits: a CANCEL gets 200, and the INVITE then
// gets onCancel's final (487, or a 200 that crosses the CANCEL).
func cancellingUAS(answerOnCancel bool) func(u *fakeUAS, msg string) {
	var invites branchSet
	var invite atomic.Value
	return func(u *fakeUAS, msg string) {
		switch fMethod(msg) {
		case "INVITE":
			if !invites.first(msg) {
				return
			}
			invite.Store(msg)
			u.send(uasReply(msg, 100, "Trying", "", ""))
			u.sendAfter(30*time.Millisecond, uasReply(msg, 180, "Ringing", "srv1", "", u.contact()))
		case "CANCEL":
			inv := invite.Load().(string)
			if answerOnCancel {
				u.send(uasReply(inv, 200, "OK", "srv1", uasSDPAnswer, u.contact()))
				u.send(uasReply(msg, 200, "OK", "srv1", ""))
				return
			}
			u.send(uasReply(msg, 200, "OK", "srv1", ""))
			u.send(uasReply(inv, 487, "Request Terminated", "srv1", ""))
		case "BYE":
			u.send(uasReply(msg, 200, "OK", "", ""))
		}
	}
}

// 6. Ringing with no answer: CANCEL (the INVITE's branch and CSeq number, To
// without tag, no credentials), 200 + 487, the 487 ACKed: cancelled.
func TestSIPInvite_NoAnswerIsCancelled(t *testing.T) {
	u := startFakeUAS(t, "udp", cancellingUAS(false))
	result, m := runInvite(t, newInviteTestChecker(), inviteConfig(u, map[string]any{"number": "100"}, map[string]any{"username": "u", "password": "p"}), 2)
	requireStatus(t, result, "failure", "answer: no answer within 2.0s (ringing since")
	if result.HTTPStatus != nil {
		t.Fatalf("HTTPStatus = %d, want none (no final before giving up)", *result.HTTPStatus)
	}
	invite := u.requests("INVITE")[0].raw
	cancel := u.waitRequests("CANCEL", 1, time.Second)[0].raw
	if fBranch(cancel) != fBranch(invite) || sipTestHeader(cancel, "CSeq") != "1 CANCEL" || fRequestURI(cancel) != fRequestURI(invite) {
		t.Fatalf("CANCEL = %q, want the INVITE's branch, CSeq number and Request-URI", cancel)
	}
	if strings.Contains(sipTestHeader(cancel, "To"), "tag=") || sipTestHeader(cancel, "Authorization") != "" || sipTestHeader(cancel, "Proxy-Authorization") != "" {
		t.Fatalf("CANCEL To/credentials = %q", cancel)
	}
	ack := u.waitRequests("ACK", 1, time.Second)[0].raw
	if fBranch(ack) != fBranch(invite) || !strings.Contains(sipTestHeader(ack, "To"), "tag=srv1") {
		t.Fatalf("487 ACK = %q", ack)
	}
	if m.Call.Teardown != "cancelled" || m.Call.TeardownStatus != 487 || m.Call.Answered {
		t.Fatalf("teardown = %q/%d answered=%v", m.Call.Teardown, m.Call.TeardownStatus, m.Call.Answered)
	}
}

// 7. No response at all: no CANCEL (RFC 3261 §9.1), an error; a provisional
// that arrives during teardown gets its CANCEL then, and a late 200 its ACK
// and BYE.
func TestSIPInvite_NoResponse(t *testing.T) {
	t.Run("silence", func(t *testing.T) {
		u := startFakeUAS(t, "udp", nil)
		c := newInviteTestChecker()
		c.callTimers.teardown = 500 * time.Millisecond
		result, m := runInvite(t, c, inviteConfig(u, map[string]any{"number": "100"}, nil), 2)
		requireStatus(t, result, "error", "timeout: no response to INVITE")
		if n := len(u.requests("CANCEL")); n != 0 {
			t.Fatalf("CANCELs = %d, want none without a provisional", n)
		}
		if m.Call.Teardown != "no_provisional" || m.Retransmissions == 0 {
			t.Fatalf("teardown = %q retransmissions = %d", m.Call.Teardown, m.Retransmissions)
		}
	})
	t.Run("late provisional", func(t *testing.T) {
		var invites branchSet
		u := startFakeUAS(t, "udp", func(u *fakeUAS, msg string) {
			switch fMethod(msg) {
			case "INVITE":
				if invites.first(msg) {
					u.sendAfter(2300*time.Millisecond, uasReply(msg, 180, "Ringing", "srv1", ""))
				}
			case "CANCEL":
				u.send(uasReply(msg, 200, "OK", "srv1", ""))
				u.send(uasReply(u.requests("INVITE")[0].raw, 487, "Request Terminated", "srv1", ""))
			}
		})
		result, m := runInvite(t, newInviteTestChecker(), inviteConfig(u, map[string]any{"number": "100"}, nil), 2)
		requireStatus(t, result, "error", "timeout: no response to INVITE")
		u.waitRequests("CANCEL", 1, time.Second)
		if m.Call.Teardown != "cancelled" {
			t.Fatalf("teardown = %q, want cancelled", m.Call.Teardown)
		}
	})
	t.Run("late answer", func(t *testing.T) {
		var invites branchSet
		u := startFakeUAS(t, "udp", func(u *fakeUAS, msg string) {
			switch fMethod(msg) {
			case "INVITE":
				if invites.first(msg) {
					u.sendAfter(2300*time.Millisecond, uasReply(msg, 200, "OK", "srv1", uasSDPAnswer, u.contact()))
				}
			case "BYE":
				u.send(uasReply(msg, 200, "OK", "", ""))
			}
		})
		result, m := runInvite(t, newInviteTestChecker(), inviteConfig(u, map[string]any{"number": "100"}, nil), 2)
		requireStatus(t, result, "error", "timeout: no response to INVITE")
		u.waitRequests("ACK", 1, time.Second)
		u.waitRequests("BYE", 1, time.Second)
		if m.Call.Teardown != "bye_ok" || !m.Call.Answered {
			t.Fatalf("teardown = %q answered=%v, want bye_ok", m.Call.Teardown, m.Call.Answered)
		}
	})
}

// 8. A 200 that crosses the CANCEL is ACKed and BYE'd.
func TestSIPInvite_AnswerRacingCancel(t *testing.T) {
	u := startFakeUAS(t, "udp", cancellingUAS(true))
	result, m := runInvite(t, newInviteTestChecker(), inviteConfig(u, map[string]any{"number": "100"}, nil), 2)
	requireStatus(t, result, "failure", "answer: no answer within")
	u.waitRequests("ACK", 1, time.Second)
	u.waitRequests("BYE", 1, time.Second)
	if m.Call.Teardown != "answered_after_cancel" || m.Call.TeardownStatus != 200 || !m.Call.Answered {
		t.Fatalf("teardown = %q/%d answered=%v", m.Call.Teardown, m.Call.TeardownStatus, m.Call.Answered)
	}
}

// 9. UDP: a lost INVITE is retransmitted after T1 (Timer A), and a
// retransmitted 200 gets the stored ACK again, never a second BYE.
func TestSIPInvite_UDPRetransmissions(t *testing.T) {
	var invites branchSet
	u := startFakeUAS(t, "udp", func(u *fakeUAS, msg string) {
		switch fMethod(msg) {
		case "INVITE":
			if !invites.first(msg) {
				return
			}
			ok := uasReply(msg, 200, "OK", "srv1", uasSDPAnswer, u.contact())
			u.send(ok)
			u.sendAfter(100*time.Millisecond, ok)
		case "BYE":
			u.send(uasReply(msg, 200, "OK", "", ""))
		}
	})
	u.drop(1)
	result, m := runInvite(t, newInviteTestChecker(), inviteConfig(u, map[string]any{"number": "100", "hold_ms": 400}, nil), 5)
	requireStatus(t, result, "success")

	invites2 := u.requests("INVITE")
	if len(invites2) < 2 || fBranch(invites2[0].raw) != fBranch(invites2[1].raw) {
		t.Fatalf("INVITEs = %d, want the dropped one retransmitted on the same branch", len(invites2))
	}
	if gap := invites2[1].at.Sub(invites2[0].at); gap < 400*time.Millisecond || gap > 1500*time.Millisecond {
		t.Fatalf("retransmission after %s, want ≈T1 (500ms)", gap)
	}
	u.waitRequests("BYE", 1, 2*time.Second)
	acks := u.requests("ACK")
	if len(acks) != 2 || acks[0].raw != acks[1].raw {
		t.Fatalf("ACKs = %d, want the same ACK re-sent for the retransmitted 200", len(acks))
	}
	time.Sleep(200 * time.Millisecond)
	if n := len(u.requests("BYE")); n != 1 {
		t.Fatalf("BYEs = %d, want 1", n)
	}
	if m.Retransmissions < 1 {
		t.Fatalf("retransmissions = %d", m.Retransmissions)
	}
}

// 10. A forked INVITE answered twice: each dialog is ACKed and BYE'd.
func TestSIPInvite_ForkedAnswers(t *testing.T) {
	var invites branchSet
	u := startFakeUAS(t, "tcp", func(u *fakeUAS, msg string) {
		switch fMethod(msg) {
		case "INVITE":
			if !invites.first(msg) {
				return
			}
			u.send(uasReply(msg, 200, "OK", "forkA", uasSDPAnswer, u.contact()))
			u.sendAfter(50*time.Millisecond, uasReply(msg, 200, "OK", "forkB", uasSDPAnswer, u.contact()))
		case "BYE":
			u.send(uasReply(msg, 200, "OK", "", ""))
		}
	})
	result, m := runInvite(t, newInviteTestChecker(), inviteConfig(u, map[string]any{"number": "100", "hold_ms": 200}, nil), 5)
	requireStatus(t, result, "success")
	acks := u.waitRequests("ACK", 2, 2*time.Second)
	byes := u.waitRequests("BYE", 2, 2*time.Second)
	tags := map[string]bool{}
	for _, msg := range append(acks, byes...) {
		tags[sipHeaderTag(sipTestHeader(msg.raw, "To"))+fMethod(msg.raw)] = true
	}
	for _, want := range []string{"forkAACK", "forkBACK", "forkABYE", "forkBBYE"} {
		if !tags[want] {
			t.Fatalf("missing %s (got %v)", want, tags)
		}
	}
	if m.Call.ForkedAnswers != 1 {
		t.Fatalf("forked_answers = %d", m.Call.ForkedAnswers)
	}
}

// 11. Record-Route becomes the BYE's Route set (reversed), the Contact is
// the Request-URI, and neither is ever dialed: one connection carries it all.
func TestSIPInvite_RouteSetAndNoSecondDial(t *testing.T) {
	var invites branchSet
	u := startFakeUAS(t, "tcp", func(u *fakeUAS, msg string) {
		switch fMethod(msg) {
		case "INVITE":
			if invites.first(msg) {
				u.send(uasReply(msg, 200, "OK", "srv1", uasSDPAnswer,
					"Contact: <sip:callee@10.0.0.1:5060>",
					"Record-Route: <sip:p1.example.com;lr>, <sip:p2.example.com;lr>"))
			}
		case "BYE":
			u.send(uasReply(msg, 200, "OK", "", ""))
		}
	})
	_, allowed, _ := net.ParseCIDR("127.0.0.0/8")
	c := NewSIPChecker(true, []*net.IPNet{allowed})
	c.ConfigureCalls(true, nil)
	c.callTimers = sipCallTimers{teardown: time.Second, linger: 200 * time.Millisecond}
	result, m := runInvite(t, c, inviteConfig(u, map[string]any{"number": "100"}, nil), 5)
	requireStatus(t, result, "success")

	bye := u.waitRequests("BYE", 1, 2*time.Second)[0].raw
	if fRequestURI(bye) != "sip:callee@10.0.0.1:5060" {
		t.Fatalf("BYE Request-URI = %q, want the Contact", fRequestURI(bye))
	}
	routes := fHeaders(bye, "Route")
	if len(routes) != 2 || routes[0] != "<sip:p2.example.com;lr>" || routes[1] != "<sip:p1.example.com;lr>" {
		t.Fatalf("BYE Route = %q, want the Record-Route reversed", routes)
	}
	u.mu.Lock()
	accepted := u.accepted
	u.mu.Unlock()
	if accepted != 1 {
		t.Fatalf("connections = %d, want 1: nothing but the configured host is dialed", accepted)
	}
	if m.Call.RouteHops != 2 || len(m.Call.AnsweredBy.RecordRoute) != 2 || m.Call.AnsweredBy.ContactHost != "10.0.0.1" {
		t.Fatalf("route metrics = %d %+v", m.Call.RouteHops, m.Call.AnsweredBy)
	}
}

// Strict routing (a first route without ;lr) puts that route in the
// Request-URI and the remote target last in the Route set.
func TestSIPDialog_StrictRouting(t *testing.T) {
	resp, err := parseSIPMessage([]byte("SIP/2.0 200 OK\r\nTo: <sip:a@b>;tag=t\r\nContact: <sip:callee@10.0.0.1>\r\nRecord-Route: <sip:p1.example.com;lr>, <sip:p2.example.com>\r\nContent-Length: 0\r\n\r\n"))
	if err != nil {
		t.Fatal(err)
	}
	d := newSIPDialog(sipRequestParams{RequestURI: "sip:a@b", CallID: "c", FromTag: "f", CSeq: 1}, resp)
	uri, route := d.target()
	if !d.strictRoute || uri != "sip:p2.example.com" || len(route) != 2 || route[0] != "<sip:p1.example.com;lr>" || route[1] != "<sip:callee@10.0.0.1>" {
		t.Fatalf("strict target = %q %q (strict=%v)", uri, route, d.strictRoute)
	}
}

// 12. The callee hangs up during the hold: 200 to its BYE, remote_bye, and
// no BYE of ours; a BYE for a dialog we do not hold gets 481.
func TestSIPInvite_RemoteBye(t *testing.T) {
	var invites branchSet
	u := startFakeUAS(t, "udp", func(u *fakeUAS, msg string) {
		switch fMethod(msg) {
		case "INVITE":
			if invites.first(msg) {
				u.send(uasReply(msg, 200, "OK", "srv1", uasSDPAnswer, u.contact()))
			}
		case "ACK":
			invite := u.requests("INVITE")[0].raw
			u.sendAfter(100*time.Millisecond, uasBye(invite, "nope", "nope"))
			u.sendAfter(200*time.Millisecond, uasBye(invite, "srv1", ""))
		}
	})
	result, m := runInvite(t, newInviteTestChecker(), inviteConfig(u, map[string]any{"number": "100", "hold_ms": 2000}, nil), 5)
	requireStatus(t, result, "success")
	if m.Call.Teardown != "remote_bye" {
		t.Fatalf("teardown = %q, want remote_bye", m.Call.Teardown)
	}
	if n := len(u.requests("BYE")); n != 0 {
		t.Fatalf("the probe sent %d BYEs after the callee hung up", n)
	}
	codes := map[string]bool{}
	for _, r := range u.responses() {
		codes[strings.Fields(r.raw)[1]] = true
	}
	if !codes["481"] || !codes["200"] {
		t.Fatalf("responses to the callee's BYEs = %v, want 481 (unknown dialog) and 200", codes)
	}
}

// The callee's BYE labels the teardown remote_bye outside the hold too: here
// the 200 fails the check (486 expected), so the call is already
// terminating when the callee hangs up, before the probe's own BYE.
func TestSIPInvite_RemoteByeWhileTerminating(t *testing.T) {
	var invites branchSet
	u := startFakeUAS(t, "udp", func(u *fakeUAS, msg string) {
		switch fMethod(msg) {
		case "INVITE":
			if invites.first(msg) {
				u.send(uasReply(msg, 200, "OK", "srv1", uasSDPAnswer, u.contact()))
			}
		case "ACK":
			u.send(uasBye(u.requests("INVITE")[0].raw, "srv1", ""))
		}
	})
	c := newInviteTestChecker()
	c.callHook = func(stage string) {
		if stage != "teardown" {
			return
		}
		// Hold teardown until the probe has answered the callee's BYE, so
		// the BYE is pending as an event when byeAll starts.
		deadline := time.Now().Add(2 * time.Second)
		for time.Now().Before(deadline) {
			for _, r := range u.responses() {
				if strings.Contains(sipTestHeader(r.raw, "CSeq"), "BYE") {
					return
				}
			}
			time.Sleep(10 * time.Millisecond)
		}
	}
	result, m := runInvite(t, c, inviteConfig(u, map[string]any{"number": "100"}, map[string]any{"expected_status": 486}), 5)
	requireStatus(t, result, "failure", "status: 200 OK (expected 486)")
	if m.Call.Teardown != "remote_bye" {
		t.Fatalf("teardown = %q, want remote_bye", m.Call.Teardown)
	}
	if n := len(u.requests("BYE")); n != 0 {
		t.Fatalf("the probe sent %d BYEs after the callee hung up", n)
	}
}

// 13. A challenged BYE is answered once, counting up the nonce (nc=2) with
// the cnonce the INVITE used.
func TestSIPInvite_ChallengedBye(t *testing.T) {
	var invites branchSet
	n := 0
	u := startFakeUAS(t, "udp", func(u *fakeUAS, msg string) {
		challenge := `Proxy-Authenticate: Digest realm="trunk", nonce="n1", qop="auth"`
		switch fMethod(msg) {
		case "INVITE":
			if !invites.first(msg) {
				return
			}
			n++
			if n == 1 {
				u.send(uasReply(msg, 407, "Proxy Auth", "p", "", challenge))
				return
			}
			u.send(uasReply(msg, 200, "OK", "srv1", uasSDPAnswer, u.contact()))
		case "BYE":
			if sipTestHeader(msg, "Proxy-Authorization") == "" {
				u.send(uasReply(msg, 407, "Proxy Auth", "", "", challenge))
				return
			}
			u.send(uasReply(msg, 200, "OK", "", ""))
		}
	})
	result, m := runInvite(t, newInviteTestChecker(), inviteConfig(u, map[string]any{"number": "100"}, map[string]any{"username": "u", "password": "p"}), 5)
	requireStatus(t, result, "success")
	byes := u.waitRequests("BYE", 2, 2*time.Second)
	cred := digestFields(sipTestHeader(byes[1].raw, "Proxy-Authorization"))
	inviteCred := digestFields(sipTestHeader(u.requests("INVITE")[1].raw, "Proxy-Authorization"))
	if cred["nc"] != "00000002" || cred["cnonce"] != inviteCred["cnonce"] || sipTestHeader(byes[1].raw, "CSeq") != "4 BYE" {
		t.Fatalf("retried BYE nc=%s cnonce=%s CSeq=%s, want nc 2, the INVITE's cnonce, CSeq 4", cred["nc"], cred["cnonce"], sipTestHeader(byes[1].raw, "CSeq"))
	}
	if !verifyDigest(sipTestHeader(byes[1].raw, "Proxy-Authorization"), "BYE", "p") {
		t.Fatal("BYE credentials do not verify")
	}
	if m.Call.Teardown != "bye_ok" {
		t.Fatalf("teardown = %q", m.Call.Teardown)
	}
}

// 14. Ring mode: success on the first 180, then CANCEL; a 200 before any
// ringing still succeeds, with a billing warning and an immediate BYE.
func TestSIPInvite_RingMode(t *testing.T) {
	t.Run("ringing", func(t *testing.T) {
		u := startFakeUAS(t, "udp", cancellingUAS(false))
		result, m := runInvite(t, newInviteTestChecker(), inviteConfig(u, map[string]any{"number": "100", "mode": "ring"}, nil), 5)
		requireStatus(t, result, "success")
		requireHTTPStatus(t, result, 180)
		u.waitRequests("CANCEL", 1, time.Second)
		if m.Call.Teardown != "cancelled" || m.Call.Answered || m.Call.PDDSource != "180" {
			t.Fatalf("ring call = %+v", m.Call)
		}
		if *result.LatencyMs > 2000 {
			t.Fatalf("latency_ms = %d, want the time to ringing", *result.LatencyMs)
		}
	})
	t.Run("answered", func(t *testing.T) {
		u := startFakeUAS(t, "udp", answeringUAS(-1, 20*time.Millisecond))
		result, m := runInvite(t, newInviteTestChecker(), inviteConfig(u, map[string]any{"number": "100", "mode": "ring"}, nil), 5)
		requireStatus(t, result, "success")
		requireHTTPStatus(t, result, 200)
		u.waitRequests("BYE", 1, time.Second)
		if len(m.Warnings) == 0 || !strings.Contains(m.Warnings[0], "billable") || m.Call.PDDSource != "200" {
			t.Fatalf("warnings = %q pdd_source = %q", m.Warnings, m.Call.PDDSource)
		}
		if len(u.requests("CANCEL")) != 0 {
			t.Fatal("an answered call was CANCELled instead of BYE'd")
		}
	})
	t.Run("only trying", func(t *testing.T) {
		u := startFakeUAS(t, "udp", func(u *fakeUAS, msg string) {
			switch fMethod(msg) {
			case "INVITE":
				u.send(uasReply(msg, 100, "Trying", "", ""))
			case "CANCEL":
				u.send(uasReply(msg, 200, "OK", "", ""))
				u.send(uasReply(u.requests("INVITE")[0].raw, 487, "Request Terminated", "srv1", ""))
			}
		})
		result, _ := runInvite(t, newInviteTestChecker(), inviteConfig(u, map[string]any{"number": "100", "mode": "ring"}, nil), 2)
		requireStatus(t, result, "failure", "pdd: no ringing within 2000ms")
	})
}

// 15. A 3xx is never followed, 488 names SRTP, 420 the extension.
func TestSIPInvite_FinalClassification(t *testing.T) {
	for _, tc := range []struct {
		code   int
		reason string
		extra  []string
		want   string
	}{
		{302, "Moved Temporarily", []string{"Contact: <sip:other@203.0.113.9>"}, "redirect: 302 Moved Temporarily (not followed; contact 203.0.113.9)"},
		{488, "Not Acceptable Here", nil, "media: offer rejected (488); trunk may require SRTP (RTP/AVP only)"},
		{420, "Bad Extension", nil, "extension: far end requires 100rel/timer (not supported)"},
		{403, "Forbidden", nil, "auth: 403 Forbidden"},
		{503, "Service Unavailable", nil, "status: 503 Service Unavailable"},
	} {
		t.Run(strconv.Itoa(tc.code), func(t *testing.T) {
			u := startFakeUAS(t, "udp", func(u *fakeUAS, msg string) {
				if fMethod(msg) == "INVITE" {
					u.send(uasReply(msg, tc.code, tc.reason, "srv1", "", tc.extra...))
				}
			})
			result, _ := runInvite(t, newInviteTestChecker(), inviteConfig(u, map[string]any{"number": "100"}, nil), 5)
			requireStatus(t, result, "failure", tc.want)
			requireHTTPStatus(t, result, tc.code)
			u.waitRequests("ACK", 1, time.Second)
			time.Sleep(100 * time.Millisecond)
			if n := len(u.requests("INVITE")); n != 1 {
				t.Fatalf("INVITEs = %d, want 1 (nothing followed)", n)
			}
		})
	}
}

// 16. PDD thresholds: warn passes with a warning, max fails.
func TestSIPInvite_Thresholds(t *testing.T) {
	cfg := func(u *fakeUAS, thresholds map[string]any) map[string]any {
		call := map[string]any{"number": "100"}
		for k, v := range thresholds {
			call[k] = v
		}
		return inviteConfig(u, call, nil)
	}
	u := startFakeUAS(t, "udp", answeringUAS(150*time.Millisecond, 200*time.Millisecond))
	result, m := runInvite(t, newInviteTestChecker(), cfg(u, map[string]any{"warn_pdd_ms": 50, "max_pdd_ms": 5000}), 5)
	requireStatus(t, result, "success")
	if m.LatencyWarnMs == nil || *m.LatencyWarnMs != 50 || len(m.Warnings) != 1 || !strings.HasPrefix(m.Warnings[0], "pdd: ") || !strings.HasSuffix(m.Warnings[0], "> 50ms (warn)") {
		t.Fatalf("warn = %v %q", m.LatencyWarnMs, m.Warnings)
	}

	u = startFakeUAS(t, "udp", answeringUAS(150*time.Millisecond, 200*time.Millisecond))
	result, m = runInvite(t, newInviteTestChecker(), cfg(u, map[string]any{"max_answer_ms": 100}), 5)
	requireStatus(t, result, "failure", "answer: ", "> 100ms")
	requireHTTPStatus(t, result, 200)
	if len(m.AssertionsFailed) != 1 {
		t.Fatalf("assertions_failed = %q", m.AssertionsFailed)
	}
	u.waitRequests("BYE", 1, time.Second)
}

// 17. A worker that refuses calls, or this destination, sends nothing.
func TestSIPInvite_WorkerPolicy(t *testing.T) {
	u := startFakeUAS(t, "udp", answeringUAS(0, 0))

	disabled := NewSIPChecker(false, nil) // never configured: calls off
	result, _ := runInvite(t, disabled, inviteConfig(u, map[string]any{"number": "100"}, nil), 5)
	requireStatus(t, result, "error", "sip_call: disabled on this worker")
	if len(result.MetricsData) != 0 {
		t.Fatal("a refused call recorded metrics as if a phase ran")
	}

	restricted := newInviteTestChecker()
	restricted.ConfigureCalls(true, []*regexp.Regexp{regexp.MustCompile(`^(?:\+33.*)$`)})
	result, _ = runInvite(t, restricted, inviteConfig(u, map[string]any{"number": "+4930123"}, nil), 5)
	requireStatus(t, result, "error", "policy: destination not allowed by worker policy")
	result, _ = runInvite(t, restricted, inviteConfig(u, map[string]any{"target_uri": "sip:%2B4930123@carrier.example.com"}, nil), 5)
	requireStatus(t, result, "error", "policy: destination not allowed by worker policy")
	// A user parameter could change what the far end dials (here to a UK
	// number) behind a user part the allow-list accepts, and so could a
	// ":password" part: both are refused before the policy is even asked.
	for _, uri := range []string{"sip:+33123;phone-context=+44@carrier.example.com", "sip:+33123:+449999@carrier.example.com"} {
		result, _ = runInvite(t, restricted, inviteConfig(u, map[string]any{"target_uri": uri}, nil), 5)
		requireStatus(t, result, "error", "call.target_uri: user part must not carry ;parameters or a :password")
	}
	// The allow-list sees the decoded user part, all of it.
	result, _ = runInvite(t, restricted, inviteConfig(u, map[string]any{"target_uri": "sip:%2B4930123@carrier.example.com;user=phone"}, nil), 5)
	requireStatus(t, result, "error", "policy: destination not allowed by worker policy")

	time.Sleep(100 * time.Millisecond)
	if n := u.total(); n != 0 {
		t.Fatalf("the fake received %d packets, want 0", n)
	}
}

// 18. The call is torn down even when the check is cancelled mid-hold, or
// the call's code panics.
func TestSIPInvite_TeardownSurvivesCancelAndPanic(t *testing.T) {
	t.Run("parent cancelled", func(t *testing.T) {
		u := startFakeUAS(t, "udp", answeringUAS(0, 10*time.Millisecond))
		ctx, cancel := context.WithCancel(context.Background())
		go func() {
			u.waitRequests("ACK", 1, 3*time.Second)
			time.Sleep(100 * time.Millisecond)
			cancel()
		}()
		result, m := runInviteCtx(t, ctx, newInviteTestChecker(), inviteConfig(u, map[string]any{"number": "100", "hold_ms": 5000}, nil), 10)
		cancel()
		u.waitRequests("BYE", 1, time.Second)
		if m.Call.Teardown != "bye_ok" {
			t.Fatalf("teardown = %q (result %s %s)", m.Call.Teardown, result.Status, resultMsg(result))
		}
	})
	t.Run("panic", func(t *testing.T) {
		u := startFakeUAS(t, "udp", answeringUAS(0, 10*time.Millisecond))
		c := newInviteTestChecker()
		c.callHook = func(stage string) {
			if stage == "holding" {
				panic("injected")
			}
		}
		result, m := runInvite(t, c, inviteConfig(u, map[string]any{"number": "100", "hold_ms": 5000}, nil), 10)
		requireStatus(t, result, "error", "internal: SIP call aborted: injected")
		u.waitRequests("BYE", 1, time.Second)
		if m.Call.Teardown != "bye_ok" {
			t.Fatalf("teardown = %q", m.Call.Teardown)
		}
	})
}

// 19. After the result, a linger ACKs and BYEs a 200 that comes late; with
// every linger slot taken the flow is closed at once and counted.
func TestSIPInvite_Linger(t *testing.T) {
	lateAnswer := func() *fakeUAS {
		var invites branchSet
		return startFakeUAS(t, "udp", func(u *fakeUAS, msg string) {
			switch fMethod(msg) {
			case "INVITE":
				if invites.first(msg) {
					u.sendAfter(3*time.Second, uasReply(msg, 200, "OK", "srv1", uasSDPAnswer, u.contact()))
				}
			case "BYE":
				u.send(uasReply(msg, 200, "OK", "", ""))
			}
		})
	}

	t.Run("late answer", func(t *testing.T) {
		u := lateAnswer()
		c := newInviteTestChecker()
		c.callTimers = sipCallTimers{teardown: 300 * time.Millisecond, linger: 3 * time.Second}
		outcomes := make(chan string, 1)
		c.ConfigureCallMetrics(func(o string) { outcomes <- o }, func() { t.Error("linger dropped") })
		result, _ := runInvite(t, c, inviteConfig(u, map[string]any{"number": "100"}, nil), 2)
		requireStatus(t, result, "error", "timeout: no response to INVITE")
		if n := len(u.requests("ACK")); n != 0 {
			t.Fatalf("ACKs before the late answer = %d", n)
		}
		u.waitRequests("ACK", 1, 3*time.Second)
		u.waitRequests("BYE", 1, 3*time.Second)
		select {
		case o := <-outcomes:
			if o != "late_answer" {
				t.Fatalf("linger outcome = %q", o)
			}
		case <-time.After(4 * time.Second):
			t.Fatal("linger never ended")
		}
	})
	t.Run("slots full", func(t *testing.T) {
		u := lateAnswer()
		c := newInviteTestChecker()
		c.callTimers = sipCallTimers{teardown: 300 * time.Millisecond, linger: 3 * time.Second}
		c.lingers = newSIPLingerPool(0)
		var dropped atomic.Int32
		c.ConfigureCallMetrics(func(string) {}, func() { dropped.Add(1) })
		runInvite(t, c, inviteConfig(u, map[string]any{"number": "100"}, nil), 2)
		if dropped.Load() != 1 {
			t.Fatalf("dropped = %d, want 1", dropped.Load())
		}
		time.Sleep(1500 * time.Millisecond)
		if n := len(u.requests("ACK")); n != 0 {
			t.Fatalf("ACKs = %d on a flow that was closed", n)
		}
	})
}

// 20. The worker clamps a payload's timeout to 20 s and hold to 10 s.
func TestSIPInvite_WorkerClamps(t *testing.T) {
	u := startFakeUAS(t, "udp", func(u *fakeUAS, msg string) {
		if fMethod(msg) == "INVITE" {
			u.send(uasReply(msg, 486, "Busy Here", "srv1", ""))
		}
	})
	result, m := runInvite(t, newInviteTestChecker(), inviteConfig(u, map[string]any{"number": "100", "hold_ms": 60000}, nil), 120)
	requireStatus(t, result, "failure", "status: 486")
	if m.Call.HoldMs != 10000 {
		t.Fatalf("hold_ms = %d, want the 10 s clamp", m.Call.HoldMs)
	}

	for _, tc := range []struct {
		timeoutSeconds, holdMs int
		timeout, hold          time.Duration
	}{
		{120, 60000, 20 * time.Second, 10 * time.Second},
		{0, 0, 20 * time.Second, 0},
		{10, 9000, 10 * time.Second, 8 * time.Second},
		{1, 5000, time.Second, 0},
		{15, 3000, 15 * time.Second, 3 * time.Second},
	} {
		timeout, hold := sipCallBudgets(tc.timeoutSeconds, tc.holdMs)
		if timeout != tc.timeout || hold != tc.hold {
			t.Errorf("sipCallBudgets(%d, %d) = %s, %s; want %s, %s", tc.timeoutSeconds, tc.holdMs, timeout, hold, tc.timeout, tc.hold)
		}
	}
}

// The SDP answer parser picks the first audio stream, its codec, address and
// direction.
func TestParseSDPAnswer(t *testing.T) {
	got := parseSDPAnswer([]byte("v=0\r\nc=IN IP4 192.0.2.1\r\na=sendonly\r\nm=audio 4000 RTP/AVP 0 8\r\nc=IN IP4 192.0.2.2\r\nm=video 5000 RTP/AVP 96\r\n"))
	if got == nil || got.Codec != "PCMU/8000" || got.PayloadType == nil || *got.PayloadType != 0 || got.RemoteMedia != "192.0.2.2:4000" || got.Direction != "sendonly" {
		t.Fatalf("parseSDPAnswer = %+v", got)
	}
	got = parseSDPAnswer([]byte("v=0\r\nc=IN IP6 2001:db8::1\r\nm=audio 0 RTP/AVP 96\r\na=rtpmap:96 opus/48000/2\r\n"))
	if got == nil || got.Codec != "opus/48000/2" || got.Direction != "inactive" || got.RemoteMedia != "" {
		t.Fatalf("rejected stream = %+v", got)
	}
	if parseSDPAnswer([]byte("v=0\r\nm=video 1 RTP/AVP 96\r\n")) != nil {
		t.Fatal("no audio stream must parse to nil")
	}
	if offer := string(buildSDPOffer("2001:db8::5", 20000, 7)); !strings.Contains(offer, "c=IN IP6 2001:db8::5") || !strings.Contains(offer, "o=probara 7 7 IN IP6") {
		t.Fatalf("IPv6 offer = %q", offer)
	}
}

func TestNewSIPCallTarget(t *testing.T) {
	for _, tc := range []struct {
		name               string
		cfg                string
		uri, to, user, frm string
	}{
		{"phone number", `{"host":"trunk.example.com","call":{"number":"+33123"}}`, "sip:+33123@trunk.example.com;user=phone", "sip:+33123@trunk.example.com", "+33123", "<sip:probara@trunk.example.com>"},
		{"extension over tls with domain", `{"host":"10.0.0.1","transport":"tls","domain":"example.com","username":"agent","call":{"number":"ivr#1"}}`, "sips:ivr%231@example.com", "sips:ivr%231@example.com", "ivr#1", "<sip:agent@example.com>"},
		{"target uri", `{"host":"sbc.example.com","call":{"target_uri":"sip:%2B331@carrier.example.com:5070;user=phone","from_user":"+1555","from_display":"Probe","from_domain":"probes.example.com"}}`, "sip:%2B331@carrier.example.com:5070;user=phone", "sip:%2B331@carrier.example.com:5070", "+331", `"Probe" <sip:+1555@probes.example.com>`},
		{"ipv6 host", `{"host":"2001:db8::1","username":"bad user","call":{"number":"100"}}`, "sip:100@[2001:db8::1];user=phone", "sip:100@[2001:db8::1]", "100", "<sip:probara@[2001:db8::1]>"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var config models.SIPMonitorConfig
			if err := json.Unmarshal([]byte(tc.cfg), &config); err != nil {
				t.Fatal(err)
			}
			target, err := newSIPCallTarget(&config)
			if err != nil {
				t.Fatal(err)
			}
			if target.requestURI != tc.uri || target.toURI != tc.to || target.user != tc.user || target.from != tc.frm {
				t.Fatalf("target = %+v", target)
			}
		})
	}
}

// stallingTLSDial stands in for an address that accepts the TCP connection
// and then never answers the TLS ClientHello (a dead load-balancer backend):
// dials to addr reach a local listener that holds every connection silent.
// Every other address is dialed through dialGuard.
func stallingTLSDial(t *testing.T, addr string) func(context.Context, *dialGuard, string, string) (net.Conn, error) {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("Listen: %v", err)
	}
	t.Cleanup(func() { listener.Close() })
	go func() {
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			t.Cleanup(func() { conn.Close() })
		}
	}()
	return func(ctx context.Context, guard *dialGuard, network, address string) (net.Conn, error) {
		if address == addr {
			var d net.Dialer
			return d.DialContext(ctx, network, listener.Addr().String())
		}
		return guard.DialContext(ctx, network, address)
	}
}

// A TLS handshake that stalls has sent nothing, so a test call gives the
// address its share of the budget and moves on; OPTIONS under "a" keeps the
// whole budget for the handshake, as it always did.
func TestSIPChecker_InviteFailsOverStalledTLSHandshake(t *testing.T) {
	u := startFakeUAS(t, "tls", answeringUAS(10*time.Millisecond, 20*time.Millisecond))
	stalled := fmt.Sprintf("192.0.2.1:%d", u.port)

	c := newInviteTestChecker()
	c.resolver = &fakeSIPResolver{ips: map[string][]string{"sip.example.com": {"192.0.2.1", "127.0.0.1"}}}
	c.testDial = stallingTLSDial(t, stalled)
	cfg := inviteConfig(u, map[string]any{"number": "100"}, map[string]any{"host": "sip.example.com"})
	start := time.Now()
	result, m := runInvite(t, c, cfg, 6)
	requireStatus(t, result, "success")
	if elapsed := time.Since(start); elapsed > 5*time.Second {
		t.Fatalf("call took %s, want the stalled handshake given up on after its share", elapsed)
	}
	requireAttemptOutcomes(t, m, "timeout", "ok")
	if a := m.Resolution.Attempts[0]; a.Address != stalled || a.Ms < 2500 || a.Ms > 4000 {
		t.Fatalf("first attempt = %+v, want %s given up on after half the 6s budget", a, stalled)
	}
	if !m.Target.Failover || m.Target.Address != fmt.Sprintf("127.0.0.1:%d", u.port) {
		t.Fatalf("target = %+v, want the second address", m.Target)
	}

	// OPTIONS: the handshake keeps the whole budget and never fails over.
	t.Setenv("SIP_LOCALHOST_AS_HOST_GATEWAY", "")
	options := &SIPChecker{
		resolver: &fakeSIPResolver{ips: map[string][]string{"sip.example.com": {"192.0.2.1", "127.0.0.1"}}},
		testDial: stallingTLSDial(t, stalled),
	}
	raw, _ := json.Marshal(map[string]any{"host": "sip.example.com", "port": u.port, "transport": "tls", "tls_skip_verify": true})
	result = options.Check(context.Background(), raw, 3)
	requireStatus(t, result, "error", "timeout")
	var env sipMetricsEnvelope
	if err := json.Unmarshal(result.MetricsData, &env); err != nil {
		t.Fatal(err)
	}
	requireAttemptOutcomes(t, env.SIP, "timeout")
}

// A domain that is not a bare host (a payload that predates the rule) never
// becomes part of a Request-URI or From.
func TestNewSIPCallTarget_RefusesBadDomain(t *testing.T) {
	for _, domain := range []string{"carrier.example.com;maddr=203.0.113.9", "carrier.example.com?X=1", "x>, <sip:y@z"} {
		config := models.SIPMonitorConfig{Host: "trunk.example.com", Domain: domain, Method: "invite", Call: &models.SIPCallConfig{Number: "100"}}
		if _, err := newSIPCallTarget(&config); err == nil || !strings.Contains(err.Error(), "domain:") {
			t.Errorf("domain %q: err = %v, want a domain error", domain, err)
		}
	}
}
