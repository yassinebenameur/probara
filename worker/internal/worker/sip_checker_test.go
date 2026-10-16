package worker

import (
	"bufio"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/md5" //nolint:gosec // digest auth test verification
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"math/big"
	"net"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestResolveSIPTargetHost(t *testing.T) {
	t.Run("does not rewrite by default", func(t *testing.T) {
		t.Setenv("SIP_LOCALHOST_AS_HOST_GATEWAY", "")
		got := resolveSIPTargetHost("localhost")
		if got != "localhost" {
			t.Fatalf("resolveSIPTargetHost(localhost) = %q, want localhost", got)
		}
	})

	t.Run("rewrites localhost when enabled", func(t *testing.T) {
		t.Setenv("SIP_LOCALHOST_AS_HOST_GATEWAY", "true")
		got := resolveSIPTargetHost("localhost")
		if got != "host.docker.internal" {
			t.Fatalf("resolveSIPTargetHost(localhost) = %q, want host.docker.internal", got)
		}
	})

	t.Run("rewrites 127.0.0.1 when enabled", func(t *testing.T) {
		t.Setenv("SIP_LOCALHOST_AS_HOST_GATEWAY", "1")
		got := resolveSIPTargetHost("127.0.0.1")
		if got != "host.docker.internal" {
			t.Fatalf("resolveSIPTargetHost(127.0.0.1) = %q, want host.docker.internal", got)
		}
	})

	t.Run("rewrites ipv6 loopback when enabled", func(t *testing.T) {
		t.Setenv("SIP_LOCALHOST_AS_HOST_GATEWAY", "yes")
		got := resolveSIPTargetHost("::1")
		if got != "host.docker.internal" {
			t.Fatalf("resolveSIPTargetHost(::1) = %q, want host.docker.internal", got)
		}
	})

	t.Run("does not rewrite non-loopback", func(t *testing.T) {
		t.Setenv("SIP_LOCALHOST_AS_HOST_GATEWAY", "true")
		got := resolveSIPTargetHost("sip.example.com")
		if got != "sip.example.com" {
			t.Fatalf("resolveSIPTargetHost(sip.example.com) = %q, want sip.example.com", got)
		}
	})
}

// sipResponseMsg builds a wire-format SIP response with only the given
// headers. A client matches responses to its requests by Via branch and
// CSeq, so a response built this way is stray to it; use sipReply to answer
// a request.
func sipResponseMsg(status int, reason string, headers ...string) string {
	lines := []string{fmt.Sprintf("SIP/2.0 %d %s", status, reason)}
	lines = append(lines, headers...)
	lines = append(lines, "Content-Length: 0", "", "")
	return strings.Join(lines, "\r\n")
}

// sipReply answers a request the way a server does: Via, From, To, Call-ID
// and CSeq echoed (a To tag added), then the given headers.
func sipReply(request string, status int, reason string, headers ...string) string {
	return sipReplyWithBody(request, status, reason, "", headers...)
}

// sipReplyWithBody is sipReply with a body (Content-Length set from it).
func sipReplyWithBody(request string, status int, reason, body string, headers ...string) string {
	lines := []string{fmt.Sprintf("SIP/2.0 %d %s", status, reason)}
	for _, line := range strings.Split(request, "\r\n") {
		name, _, _ := strings.Cut(line, ":")
		switch strings.ToLower(strings.TrimSpace(name)) {
		case "via", "from", "call-id", "cseq":
			lines = append(lines, line)
		case "to":
			if !strings.Contains(line, ";tag=") {
				line += ";tag=srv1"
			}
			lines = append(lines, line)
		}
		if line == "" {
			break
		}
	}
	lines = append(lines, headers...)
	lines = append(lines, fmt.Sprintf("Content-Length: %d", len(body)), "")
	return strings.Join(lines, "\r\n") + "\r\n" + body
}

// sipTestHeader returns the first value of a header in a raw message.
func sipTestHeader(message, name string) string {
	for _, line := range strings.Split(message, "\r\n") {
		if line == "" {
			break
		}
		k, v, found := strings.Cut(line, ":")
		if found && strings.EqualFold(strings.TrimSpace(k), name) {
			return strings.TrimSpace(v)
		}
	}
	return ""
}

// startFakeUDPSIPServer runs an in-process UDP SIP server; the handler maps
// each received request to the responses to send back.
func startFakeUDPSIPServer(t *testing.T, handler func(request string) []string) (host string, port int) {
	t.Helper()
	return startFakeUDPSIPServerFrom(t, func(request string, _ net.Addr) []string { return handler(request) })
}

// startFakeUDPSIPServerFrom is startFakeUDPSIPServer with the sender's
// address passed to the handler.
func startFakeUDPSIPServerFrom(t *testing.T, handler func(request string, from net.Addr) []string) (host string, port int) {
	t.Helper()
	pc, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("ListenPacket() error = %v", err)
	}
	t.Cleanup(func() { pc.Close() })

	go func() {
		buffer := make([]byte, 65535)
		for {
			n, addr, err := pc.ReadFrom(buffer)
			if err != nil {
				return
			}
			for _, response := range handler(string(buffer[:n]), addr) {
				_, _ = pc.WriteTo([]byte(response), addr)
			}
		}
	}()

	return splitTestHostPort(t, pc.LocalAddr().String())
}

// startFakeStreamSIPServer serves SIP over an already-listening TCP or TLS
// listener, one connection at a time, reading full messages (headers +
// Content-Length: 0 bodies) and replying via the handler.
func startFakeStreamSIPServer(t *testing.T, listener net.Listener, handler func(request string) []string) {
	t.Helper()
	t.Cleanup(func() { listener.Close() })
	go func() {
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			go func(conn net.Conn) {
				defer conn.Close()
				reader := bufio.NewReader(conn)
				for {
					request, err := readSIPRequestForTest(reader)
					if err != nil {
						return
					}
					for _, response := range handler(request) {
						if _, err := conn.Write([]byte(response)); err != nil {
							return
						}
					}
				}
			}(conn)
		}
	}()
}

// readSIPRequestForTest reads one message off a stream: the header block,
// then exactly Content-Length body bytes, so a request with a body does not
// leave its body to be misread as the next message.
func readSIPRequestForTest(reader *bufio.Reader) (string, error) {
	var b strings.Builder
	contentLength := 0
	for {
		line, err := reader.ReadString('\n')
		if err != nil {
			return "", err
		}
		trimmed := strings.TrimRight(line, "\r\n")
		if b.Len() == 0 && trimmed == "" {
			continue // keepalive
		}
		b.WriteString(line)
		if trimmed == "" {
			break
		}
		if name, value, found := strings.Cut(trimmed, ":"); found {
			if n := strings.ToLower(strings.TrimSpace(name)); n == "content-length" || n == "l" {
				contentLength, err = strconv.Atoi(strings.TrimSpace(value))
				if err != nil {
					return "", fmt.Errorf("bad Content-Length %q", value)
				}
			}
		}
	}
	if contentLength > 0 {
		body := make([]byte, contentLength)
		if _, err := io.ReadFull(reader, body); err != nil {
			return "", err
		}
		b.Write(body)
	}
	return b.String(), nil
}

func splitTestHostPort(t *testing.T, addr string) (string, int) {
	t.Helper()
	host, portStr, err := net.SplitHostPort(addr)
	if err != nil {
		t.Fatalf("SplitHostPort(%q) error = %v", addr, err)
	}
	port, err := strconv.Atoi(portStr)
	if err != nil {
		t.Fatalf("Atoi(%q) error = %v", portStr, err)
	}
	return host, port
}

func runSIPCheck(t *testing.T, config map[string]interface{}) CheckResult {
	t.Helper()
	t.Setenv("SIP_LOCALHOST_AS_HOST_GATEWAY", "")
	raw, err := json.Marshal(config)
	if err != nil {
		t.Fatalf("marshal config: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	return NewSIPChecker(false, nil).Check(ctx, raw, 5)
}

// The SIP checker dials through dialGuard, so the worker's private-IP (SSRF)
// policy applies to SIP targets like every other dial-path checker.
func TestSIPChecker_PrivateIPGuard(t *testing.T) {
	t.Setenv("SIP_LOCALHOST_AS_HOST_GATEWAY", "")
	host, port := startFakeUDPSIPServer(t, func(request string) []string {
		return []string{sipReply(request, 200, "OK")}
	})

	raw, err := json.Marshal(map[string]interface{}{"host": host, "port": port, "transport": "udp"})
	if err != nil {
		t.Fatalf("marshal config: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	result := NewSIPChecker(true, nil).Check(ctx, raw, 5)
	requireSIPError(t, result, "ssrf_blocked")
}

// The worker applies the shared config rules before dialing, so a stored
// config carrying header-injection characters never reaches the wire, even
// if it predates the API rule.
func TestSIPChecker_RejectsInvalidConfigBeforeDialing(t *testing.T) {
	var requests atomic.Int32
	host, port := startFakeUDPSIPServer(t, func(request string) []string {
		requests.Add(1)
		return []string{sipReply(request, 200, "OK")}
	})

	result := runSIPCheck(t, map[string]interface{}{
		"host": host, "port": port, "transport": "udp",
		"method": "register", "domain": "example.com\r\nX-Injected: 1",
	})
	requireSIPError(t, result, "invalid SIP config", "domain must not contain CR, LF or NUL")
	if result.LatencyMs != nil {
		t.Fatalf("LatencyMs = %d, want nil: nothing was sent", *result.LatencyMs)
	}
	time.Sleep(50 * time.Millisecond)
	if got := requests.Load(); got != 0 {
		t.Fatalf("server received %d requests, want 0", got)
	}
}

func TestSIPChecker_OptionsUDP(t *testing.T) {
	host, port := startFakeUDPSIPServer(t, func(request string) []string {
		if !strings.HasPrefix(request, "OPTIONS sip:") {
			t.Errorf("expected OPTIONS request, got: %s", request)
		}
		// Provisional response first: the checker must skip 1xx and wait
		// for the final response.
		return []string{
			sipReply(request, 100, "Trying"),
			sipReply(request, 200, "OK"),
		}
	})

	result := runSIPCheck(t, map[string]interface{}{"host": host, "port": port, "transport": "udp"})
	if result.Status != "success" {
		t.Fatalf("Status = %q (%v), want success", result.Status, result.ErrorMessage)
	}
	if result.HTTPStatus == nil || *result.HTTPStatus != 200 {
		t.Fatalf("HTTPStatus = %v, want 200", result.HTTPStatus)
	}
}

func TestSIPChecker_OptionsTCP(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("Listen() error = %v", err)
	}
	startFakeStreamSIPServer(t, listener, func(request string) []string {
		return []string{sipReply(request, 200, "OK")}
	})
	host, port := splitTestHostPort(t, listener.Addr().String())

	result := runSIPCheck(t, map[string]interface{}{"host": host, "port": port, "transport": "tcp"})
	if result.Status != "success" {
		t.Fatalf("Status = %q (%v), want success", result.Status, result.ErrorMessage)
	}
}

// startFakeTLSSIPServer serves 200 OK to every request over TLS with a
// self-signed certificate for 127.0.0.1 that expires at notAfter.
func startFakeTLSSIPServer(t *testing.T, notAfter time.Time) (string, int) {
	t.Helper()
	cert := newSelfSignedTestCert(t, notAfter)
	listener, err := tls.Listen("tcp", "127.0.0.1:0", &tls.Config{Certificates: []tls.Certificate{cert}})
	if err != nil {
		t.Fatalf("tls.Listen() error = %v", err)
	}
	startFakeStreamSIPServer(t, listener, func(request string) []string {
		return []string{sipReply(request, 200, "OK")}
	})
	return splitTestHostPort(t, listener.Addr().String())
}

// requireSIPError asserts an error result whose message starts with the
// given category prefix and contains every fragment.
func requireSIPError(t *testing.T, result CheckResult, prefix string, fragments ...string) {
	t.Helper()
	if result.Status != "error" {
		t.Fatalf("Status = %q (%v), want error", result.Status, result.ErrorMessage)
	}
	if result.ErrorMessage == nil {
		t.Fatalf("ErrorMessage = nil, want %q prefix", prefix)
	}
	msg := *result.ErrorMessage
	if !strings.HasPrefix(msg, prefix+": ") {
		t.Fatalf("ErrorMessage = %q, want %q prefix", msg, prefix+": ")
	}
	for _, fragment := range fragments {
		if !strings.Contains(msg, fragment) {
			t.Fatalf("ErrorMessage = %q, want it to contain %q", msg, fragment)
		}
	}
}

func TestSIPChecker_OptionsTLS(t *testing.T) {
	host, port := startFakeTLSSIPServer(t, time.Now().Add(time.Hour))

	result := runSIPCheck(t, map[string]interface{}{
		"host": host, "port": port, "transport": "tls", "tls_skip_verify": true,
	})
	if result.Status != "success" {
		t.Fatalf("Status = %q (%v), want success", result.Status, result.ErrorMessage)
	}

	// Without tls_skip_verify the self-signed certificate must be rejected,
	// and reported as a TLS failure rather than a connect error: the TCP
	// connection succeeded.
	strict := runSIPCheck(t, map[string]interface{}{"host": host, "port": port, "transport": "tls"})
	requireSIPError(t, strict, "tls", "tls handshake: ", "certificate signed by unknown authority")
	if strings.Contains(*strict.ErrorMessage, "failed to connect") {
		t.Fatalf("ErrorMessage = %q, a handshake failure must not read as a connect failure", *strict.ErrorMessage)
	}
}

func TestSIPChecker_TLSExpiredCertificate(t *testing.T) {
	host, port := startFakeTLSSIPServer(t, time.Now().Add(-time.Hour))

	// The verifier may report the untrusted self-signed chain before the
	// expiry (the stdlib and platform verifiers differ), so only the
	// category is pinned here; TestClassifySIPError covers the typed
	// expired-certificate error itself.
	result := runSIPCheck(t, map[string]interface{}{"host": host, "port": port, "transport": "tls"})
	requireSIPError(t, result, "tls", "tls handshake: ", "certificate")

	// tls_skip_verify is an explicit opt-out: an expired lab certificate
	// still passes.
	skipped := runSIPCheck(t, map[string]interface{}{
		"host": host, "port": port, "transport": "tls", "tls_skip_verify": true,
	})
	if skipped.Status != "success" {
		t.Fatalf("skip-verify Status = %q (%v), want success", skipped.Status, skipped.ErrorMessage)
	}
}

// A plaintext SIP server on a TLS monitor fails the handshake with a record
// error, not a certificate error; it is still a TLS failure.
func TestSIPChecker_TLSAgainstPlaintextServer(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("Listen() error = %v", err)
	}
	t.Cleanup(func() { listener.Close() })
	go func() {
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			go func(conn net.Conn) {
				defer conn.Close()
				_, _ = conn.Write([]byte(sipResponseMsg(400, "Bad Request")))
				_, _ = io.Copy(io.Discard, conn)
			}(conn)
		}
	}()
	host, port := splitTestHostPort(t, listener.Addr().String())

	result := runSIPCheck(t, map[string]interface{}{"host": host, "port": port, "transport": "tls"})
	requireSIPError(t, result, "tls", "tls handshake: ")
}

func TestSIPChecker_ConnectionRefused(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("Listen() error = %v", err)
	}
	host, port := splitTestHostPort(t, listener.Addr().String())
	listener.Close()

	for _, transport := range []string{"tcp", "tls"} {
		t.Run(transport, func(t *testing.T) {
			result := runSIPCheck(t, map[string]interface{}{"host": host, "port": port, "transport": transport})
			requireSIPError(t, result, "refused", "failed to connect: ", "connection refused")
		})
	}
}

func TestSIPChecker_RegisterDigestMD5(t *testing.T) {
	const (
		realm    = "probara.test"
		nonce    = "test-nonce-1"
		username = "agent42"
		password = "s3cret"
	)

	var sawCSeq2 atomic.Bool
	host, port := startFakeUDPSIPServer(t, func(request string) []string {
		if !strings.HasPrefix(request, "REGISTER sip:probara.test SIP/2.0") {
			t.Errorf("unexpected request line: %s", strings.SplitN(request, "\r\n", 2)[0])
		}
		if strings.Contains(request, "Contact:") {
			t.Errorf("query REGISTER must not carry a Contact header: %s", request)
		}
		authLine := ""
		for _, line := range strings.Split(request, "\r\n") {
			if strings.HasPrefix(strings.ToLower(line), "authorization:") {
				authLine = strings.TrimSpace(line[len("authorization:"):])
			}
			if strings.HasPrefix(line, "CSeq: 2 REGISTER") {
				sawCSeq2.Store(true)
			}
		}
		if authLine == "" {
			challenge := fmt.Sprintf(`WWW-Authenticate: Digest realm=%q, nonce=%q, qop="auth", algorithm=MD5`, realm, nonce)
			return []string{sipReply(request, 401, "Unauthorized", challenge)}
		}

		// Verify the digest response server-side.
		params := parseSIPAuthParams(strings.TrimSpace(authLine[len("Digest "):]))
		h := func(s string) string {
			sum := md5.Sum([]byte(s)) //nolint:gosec // digest auth test
			return hex.EncodeToString(sum[:])
		}
		ha1 := h(username + ":" + realm + ":" + password)
		ha2 := h("REGISTER:" + params["uri"])
		expected := h(strings.Join([]string{ha1, nonce, params["nc"], params["cnonce"], "auth", ha2}, ":"))
		if params["response"] != expected || params["username"] != username {
			return []string{sipReply(request, 403, "Forbidden")}
		}
		return []string{sipReply(request, 200, "OK")}
	})

	result := runSIPCheck(t, map[string]interface{}{
		"host": host, "port": port, "transport": "udp",
		"method": "register", "domain": "probara.test",
		"username": username, "password": password,
	})
	if result.Status != "success" {
		t.Fatalf("Status = %q (%v), want success", result.Status, result.ErrorMessage)
	}
	if !sawCSeq2.Load() {
		t.Fatal("authenticated retry must increment CSeq to 2")
	}
}

func TestSIPChecker_RegisterAuthFailures(t *testing.T) {
	challenge := `WWW-Authenticate: Digest realm="probara.test", nonce="n1", qop="auth", algorithm=MD5`
	host, port := startFakeUDPSIPServer(t, func(request string) []string {
		// Always challenge: wrong credentials never succeed.
		return []string{sipReply(request, 401, "Unauthorized", challenge)}
	})

	t.Run("wrong credentials", func(t *testing.T) {
		result := runSIPCheck(t, map[string]interface{}{
			"host": host, "port": port, "method": "register",
			"username": "agent42", "password": "wrong",
		})
		if result.Status != "failure" {
			t.Fatalf("Status = %q, want failure", result.Status)
		}
		if result.ErrorMessage == nil || !strings.Contains(*result.ErrorMessage, "authentication failed") {
			t.Fatalf("ErrorMessage = %v, want authentication failed", result.ErrorMessage)
		}
	})

	t.Run("missing credentials", func(t *testing.T) {
		result := runSIPCheck(t, map[string]interface{}{
			"host": host, "port": port, "method": "register",
		})
		if result.Status != "failure" {
			t.Fatalf("Status = %q, want failure", result.Status)
		}
		if result.ErrorMessage == nil || !strings.Contains(*result.ErrorMessage, "no credentials are configured") {
			t.Fatalf("ErrorMessage = %v, want missing-credentials message", result.ErrorMessage)
		}
	})
}

func newSelfSignedTestCert(t *testing.T, notAfter time.Time) tls.Certificate {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("GenerateKey() error = %v", err)
	}
	// Valid from an hour ago (or an hour before notAfter, if that is
	// earlier), so a past notAfter yields an expired certificate.
	notBefore := time.Now().Add(-time.Hour)
	if earlier := notAfter.Add(-time.Hour); earlier.Before(notBefore) {
		notBefore = earlier
	}
	template := x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: "127.0.0.1"},
		NotBefore:    notBefore,
		NotAfter:     notAfter,
		IPAddresses:  []net.IP{net.ParseIP("127.0.0.1")},
	}
	der, err := x509.CreateCertificate(rand.Reader, &template, &template, &key.PublicKey, key)
	if err != nil {
		t.Fatalf("CreateCertificate() error = %v", err)
	}
	return tls.Certificate{Certificate: [][]byte{der}, PrivateKey: key}
}

// A dropped first datagram is recovered by Timer E: the same request (same
// branch) is retransmitted after T1 and its answer completes the check.
func TestSIPChecker_TimerERetransmitsAfterDrop(t *testing.T) {
	var (
		mu       sync.Mutex
		requests []string
		arrivals []time.Time
	)
	host, port := startFakeUDPSIPServer(t, func(request string) []string {
		mu.Lock()
		defer mu.Unlock()
		requests = append(requests, request)
		arrivals = append(arrivals, time.Now())
		if len(requests) == 1 {
			return nil // lost on the way
		}
		return []string{sipReply(request, 200, "OK")}
	})

	result := runSIPCheck(t, map[string]interface{}{"host": host, "port": port, "transport": "udp"})
	if result.Status != "success" {
		t.Fatalf("Status = %q (%v), want success", result.Status, result.ErrorMessage)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(requests) != 2 {
		t.Fatalf("server saw %d requests, want 2", len(requests))
	}
	if a, b := sipTestHeader(requests[0], "Via"), sipTestHeader(requests[1], "Via"); a != b {
		t.Fatalf("retransmission changed the Via (and so the branch):\n%s\n%s", a, b)
	}
	if gap := arrivals[1].Sub(arrivals[0]); gap < 400*time.Millisecond || gap > 2*time.Second {
		t.Fatalf("retransmission after %v, want about T1 (500ms)", gap)
	}
}

// Responses are matched by top Via branch and CSeq method: a 401 for an
// earlier transaction (another branch) or for another method is stray, never
// read as the answer to the request in flight.
func TestSIPChecker_IgnoresStrayResponses(t *testing.T) {
	host, port := startFakeUDPSIPServer(t, func(request string) []string {
		oldBranch := strings.Replace(request, ";branch=z9hG4bK", ";branch=z9hG4bKold", 1)
		otherMethod := strings.Replace(request, " OPTIONS\r\n", " REGISTER\r\n", 1)
		if oldBranch == request || otherMethod == request {
			t.Errorf("request does not have the expected shape:\n%s", request)
		}
		challenge := `WWW-Authenticate: Digest realm="x", nonce="n"`
		return []string{
			sipReply(oldBranch, 401, "Unauthorized", challenge),
			sipReply(otherMethod, 401, "Unauthorized", challenge),
			sipResponseMsg(503, "No Via At All"),
			sipReply(request, 200, "OK"),
		}
	})

	result := runSIPCheck(t, map[string]interface{}{"host": host, "port": port, "transport": "udp"})
	if result.Status != "success" || result.HTTPStatus == nil || *result.HTTPStatus != 200 {
		t.Fatalf("Status = %q, HTTPStatus = %v (%v), want success 200", result.Status, result.HTTPStatus, result.ErrorMessage)
	}
}

// A request the server sends during the check (an SBC keepalive OPTIONS) is
// answered 200 on the same flow, and does not disturb the check.
func TestSIPChecker_AnswersServerSentOptions(t *testing.T) {
	var (
		pending    string
		probeReply = make(chan string, 1)
	)
	serverOptions := strings.Join([]string{
		"OPTIONS sip:probara@127.0.0.1 SIP/2.0",
		"Via: SIP/2.0/UDP 127.0.0.1:5060;branch=z9hG4bKsrvka",
		"From: <sip:sbc@127.0.0.1>;tag=sbc",
		"To: <sip:probara@127.0.0.1>",
		"Call-ID: keepalive-1",
		"CSeq: 7 OPTIONS",
		"Max-Forwards: 70",
		"Content-Length: 0",
		"", "",
	}, "\r\n")
	host, port := startFakeUDPSIPServer(t, func(message string) []string {
		if strings.HasPrefix(message, "SIP/2.0 ") {
			// The probe's answer to our keepalive: now answer its request.
			probeReply <- message
			return []string{sipReply(pending, 200, "OK")}
		}
		if pending == "" {
			pending = message
			return []string{serverOptions}
		}
		return nil // a retransmission while we wait
	})

	result := runSIPCheck(t, map[string]interface{}{"host": host, "port": port, "transport": "udp"})
	if result.Status != "success" {
		t.Fatalf("Status = %q (%v), want success", result.Status, result.ErrorMessage)
	}
	select {
	case reply := <-probeReply:
		if !strings.HasPrefix(reply, "SIP/2.0 200 ") {
			t.Fatalf("probe answered the server's OPTIONS with %q", strings.SplitN(reply, "\r\n", 2)[0])
		}
		for _, h := range []string{"Via", "Call-ID", "CSeq", "From"} {
			if got, want := sipTestHeader(reply, h), sipTestHeader(serverOptions, h); got != want {
				t.Errorf("reply %s = %q, want %q echoed", h, got, want)
			}
		}
		if sipHeaderTag(sipTestHeader(reply, "To")) == "" {
			t.Errorf("reply To has no tag: %q", sipTestHeader(reply, "To"))
		}
	default:
		t.Fatal("probe never answered the server's OPTIONS")
	}
}

// Via and Contact carry the flow's real local address (not a placeholder),
// so a server can route responses and requests back to the probe.
func TestSIPChecker_RealViaAndContact(t *testing.T) {
	type seen struct {
		request string
		from    string
	}
	got := make(chan seen, 4)
	host, port := startFakeUDPSIPServerFrom(t, func(request string, from net.Addr) []string {
		got <- seen{request, from.String()}
		return []string{sipReply(request, 200, "OK")}
	})

	result := runSIPCheck(t, map[string]interface{}{"host": host, "port": port, "transport": "udp"})
	if result.Status != "success" {
		t.Fatalf("Status = %q (%v), want success", result.Status, result.ErrorMessage)
	}
	s := <-got
	via := sipTestHeader(s.request, "Via")
	if !strings.HasPrefix(via, "SIP/2.0/UDP "+s.from+";branch=z9hG4bK") || !strings.HasSuffix(via, ";rport") {
		t.Errorf("Via = %q, want SIP/2.0/UDP %s;branch=z9hG4bK…;rport", via, s.from)
	}
	if contact, want := sipTestHeader(s.request, "Contact"), "<sip:probara@"+s.from+";transport=udp>"; contact != want {
		t.Errorf("Contact = %q, want %q", contact, want)
	}
	if strings.Contains(s.request, "monitor.local:5060") {
		t.Errorf("request still carries the placeholder address:\n%s", s.request)
	}
	if ua := sipTestHeader(s.request, "User-Agent"); ua != "Probara" {
		t.Errorf("User-Agent = %q, want Probara", ua)
	}
}

// Over TCP the Via names the transport and nothing is retransmitted, even
// when the server is slower than T1.
func TestSIPChecker_TCPDoesNotRetransmit(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("Listen() error = %v", err)
	}
	var requests atomic.Int32
	startFakeStreamSIPServer(t, listener, func(request string) []string {
		requests.Add(1)
		if via := sipTestHeader(request, "Via"); !strings.HasPrefix(via, "SIP/2.0/TCP 127.0.0.1:") {
			t.Errorf("Via = %q, want SIP/2.0/TCP with the local address", via)
		}
		time.Sleep(1200 * time.Millisecond)
		return []string{sipReply(request, 200, "OK")}
	})
	host, port := splitTestHostPort(t, listener.Addr().String())

	result := runSIPCheck(t, map[string]interface{}{"host": host, "port": port, "transport": "tcp"})
	if result.Status != "success" {
		t.Fatalf("Status = %q (%v), want success", result.Status, result.ErrorMessage)
	}
	if n := requests.Load(); n != 1 {
		t.Fatalf("server saw %d requests over TCP, want 1", n)
	}
}

// A silent UDP server: the check ends at its budget as a timeout that says
// which request went unanswered.
func TestSIPChecker_NoResponseIsTimeout(t *testing.T) {
	var requests atomic.Int32
	host, port := startFakeUDPSIPServer(t, func(string) []string {
		requests.Add(1)
		return nil
	})
	t.Setenv("SIP_LOCALHOST_AS_HOST_GATEWAY", "")
	raw, _ := json.Marshal(map[string]interface{}{"host": host, "port": port, "transport": "udp"})
	result := NewSIPChecker(false, nil).Check(context.Background(), raw, 2)
	if result.Status != "error" || result.ErrorMessage == nil {
		t.Fatalf("Status = %q (%v), want error", result.Status, result.ErrorMessage)
	}
	if want := "timeout: no response to OPTIONS"; !strings.HasPrefix(*result.ErrorMessage, want) {
		t.Fatalf("ErrorMessage = %q, want prefix %q", *result.ErrorMessage, want)
	}
	// Timer E over 2s: sends at 0, 0.5, 1.5 (and possibly 3.5 is past the
	// budget), so 3 sends, give or take scheduling.
	if n := requests.Load(); n < 2 || n > 4 {
		t.Fatalf("server saw %d sends in 2s, want about 3 (Timer E)", n)
	}
}
