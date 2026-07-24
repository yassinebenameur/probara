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
	"math/big"
	"net"
	"strconv"
	"strings"
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

// sipResponseMsg builds a wire-format SIP response.
func sipResponseMsg(status int, reason string, headers ...string) string {
	lines := []string{fmt.Sprintf("SIP/2.0 %d %s", status, reason)}
	lines = append(lines, headers...)
	lines = append(lines, "Content-Length: 0", "", "")
	return strings.Join(lines, "\r\n")
}

// startFakeUDPSIPServer runs an in-process UDP SIP server; the handler maps
// each received request to the responses to send back.
func startFakeUDPSIPServer(t *testing.T, handler func(request string) []string) (host string, port int) {
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
			for _, response := range handler(string(buffer[:n])) {
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

func readSIPRequestForTest(reader *bufio.Reader) (string, error) {
	var b strings.Builder
	for {
		line, err := reader.ReadString('\n')
		if err != nil {
			return "", err
		}
		b.WriteString(line)
		if strings.TrimRight(line, "\r\n") == "" {
			return b.String(), nil
		}
	}
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
		return []string{sipResponseMsg(200, "OK")}
	})

	raw, err := json.Marshal(map[string]interface{}{"host": host, "port": port, "transport": "udp"})
	if err != nil {
		t.Fatalf("marshal config: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	result := NewSIPChecker(true, nil).Check(ctx, raw, 5)
	if result.Status != "error" {
		t.Fatalf("Status = %q, want error for loopback target under SSRF guard", result.Status)
	}
	if result.ErrorMessage == nil || !strings.Contains(*result.ErrorMessage, "ssrf_blocked") {
		t.Fatalf("ErrorMessage = %v, want ssrf_blocked", result.ErrorMessage)
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
			sipResponseMsg(100, "Trying"),
			sipResponseMsg(200, "OK"),
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
		return []string{sipResponseMsg(200, "OK")}
	})
	host, port := splitTestHostPort(t, listener.Addr().String())

	result := runSIPCheck(t, map[string]interface{}{"host": host, "port": port, "transport": "tcp"})
	if result.Status != "success" {
		t.Fatalf("Status = %q (%v), want success", result.Status, result.ErrorMessage)
	}
}

func TestSIPChecker_OptionsTLS(t *testing.T) {
	cert := newSelfSignedTestCert(t)
	listener, err := tls.Listen("tcp", "127.0.0.1:0", &tls.Config{Certificates: []tls.Certificate{cert}})
	if err != nil {
		t.Fatalf("tls.Listen() error = %v", err)
	}
	startFakeStreamSIPServer(t, listener, func(request string) []string {
		return []string{sipResponseMsg(200, "OK")}
	})
	host, port := splitTestHostPort(t, listener.Addr().String())

	result := runSIPCheck(t, map[string]interface{}{
		"host": host, "port": port, "transport": "tls", "tls_skip_verify": true,
	})
	if result.Status != "success" {
		t.Fatalf("Status = %q (%v), want success", result.Status, result.ErrorMessage)
	}

	// Without tls_skip_verify the self-signed certificate must be rejected.
	strict := runSIPCheck(t, map[string]interface{}{"host": host, "port": port, "transport": "tls"})
	if strict.Status != "error" {
		t.Fatalf("strict Status = %q, want error", strict.Status)
	}
}

func TestSIPChecker_RegisterDigestMD5(t *testing.T) {
	const (
		realm    = "probara.test"
		nonce    = "test-nonce-1"
		username = "agent42"
		password = "s3cret"
	)

	var sawCSeq2 bool
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
				sawCSeq2 = true
			}
		}
		if authLine == "" {
			challenge := fmt.Sprintf(`WWW-Authenticate: Digest realm=%q, nonce=%q, qop="auth", algorithm=MD5`, realm, nonce)
			return []string{sipResponseMsg(401, "Unauthorized", challenge)}
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
			return []string{sipResponseMsg(403, "Forbidden")}
		}
		return []string{sipResponseMsg(200, "OK")}
	})

	result := runSIPCheck(t, map[string]interface{}{
		"host": host, "port": port, "transport": "udp",
		"method": "register", "domain": "probara.test",
		"username": username, "password": password,
	})
	if result.Status != "success" {
		t.Fatalf("Status = %q (%v), want success", result.Status, result.ErrorMessage)
	}
	if !sawCSeq2 {
		t.Fatal("authenticated retry must increment CSeq to 2")
	}
}

func TestSIPChecker_RegisterAuthFailures(t *testing.T) {
	challenge := `WWW-Authenticate: Digest realm="probara.test", nonce="n1", qop="auth", algorithm=MD5`
	host, port := startFakeUDPSIPServer(t, func(request string) []string {
		// Always challenge: wrong credentials never succeed.
		return []string{sipResponseMsg(401, "Unauthorized", challenge)}
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

func newSelfSignedTestCert(t *testing.T) tls.Certificate {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("GenerateKey() error = %v", err)
	}
	template := x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: "127.0.0.1"},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(time.Hour),
		IPAddresses:  []net.IP{net.ParseIP("127.0.0.1")},
	}
	der, err := x509.CreateCertificate(rand.Reader, &template, &template, &key.PublicKey, key)
	if err != nil {
		t.Fatalf("CreateCertificate() error = %v", err)
	}
	return tls.Certificate{Certificate: [][]byte{der}, PrivateKey: key}
}
