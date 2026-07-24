package worker

import (
	"bufio"
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/yassinebenameur/probara/shared/models"
)

// SIPChecker implements Checker for SIP monitors. It supports OPTIONS
// availability pings and contact-less REGISTER checks (which exercise the
// registrar's digest authentication path without mutating any bindings),
// over UDP, TCP, or TLS. Dialing goes through dialGuard so SIP probes
// inherit the worker's private-IP (SSRF) policy.
type SIPChecker struct {
	blockPrivateIPs bool
	allowedCIDRs    []*net.IPNet
}

// NewSIPChecker creates a new SIP checker
func NewSIPChecker(blockPrivateIPs bool, allowedCIDRs []*net.IPNet) *SIPChecker {
	return &SIPChecker{
		blockPrivateIPs: blockPrivateIPs,
		allowedCIDRs:    allowedCIDRs,
	}
}

// sipResponse is a parsed SIP response: the status line plus headers.
// Header names are lowercased; bodies are consumed but discarded.
type sipResponse struct {
	StatusCode int
	Reason     string
	Headers    map[string][]string
}

func (r *sipResponse) header(name string) string {
	values := r.Headers[strings.ToLower(name)]
	if len(values) == 0 {
		return ""
	}
	return values[0]
}

// Check performs a SIP check according to the configured method.
func (c *SIPChecker) Check(ctx context.Context, configRaw json.RawMessage, timeoutSeconds int) CheckResult {
	var config models.SIPMonitorConfig
	if err := json.Unmarshal(configRaw, &config); err != nil {
		errMsg := fmt.Sprintf("failed to unmarshal SIP config: %v", err)
		return CheckResult{
			Status:       "error",
			ErrorMessage: &errMsg,
		}
	}

	// Set defaults
	transport := strings.ToLower(config.Transport)
	if transport == "" {
		transport = "udp"
	}
	if config.Port == 0 {
		if transport == "tls" {
			config.Port = 5061
		} else {
			config.Port = 5060
		}
	}
	method := strings.ToLower(config.Method)
	if method == "" {
		method = "options"
	}
	methodUpper := strings.ToUpper(method)

	startTime := time.Now()
	timeout := time.Duration(timeoutSeconds) * time.Second
	targetHost := resolveSIPTargetHost(config.Host)

	conn, err := c.dialSIP(ctx, transport, targetHost, config.Port, &config, timeout)
	if err != nil {
		latencyMs := time.Since(startTime).Milliseconds()
		errMsg := fmt.Sprintf("%s: %v", classifySIPError(err), err)
		return CheckResult{
			Status:       "error",
			ErrorMessage: &errMsg,
			LatencyMs:    &latencyMs,
		}
	}
	defer conn.Close()

	domain := strings.TrimSpace(config.Domain)
	if domain == "" {
		domain = config.Host
	}
	fromUser := strings.TrimSpace(config.Username)
	if fromUser == "" {
		fromUser = "probe"
	}

	var requestURI string
	if method == "register" {
		requestURI = "sip:" + domain
	} else {
		requestURI = fmt.Sprintf("sip:%s:%d", config.Host, config.Port)
	}

	params := sipRequestParams{
		Method:     methodUpper,
		RequestURI: requestURI,
		Host:       config.Host,
		Port:       config.Port,
		Transport:  transport,
		FromUser:   fromUser,
		Domain:     domain,
		CallID:     uuid.New().String(),
		FromTag:    uuid.New().String()[:8],
		CSeq:       1,
	}

	resp, err := conn.roundTrip(buildSIPRequest(params))
	if err != nil {
		latencyMs := time.Since(startTime).Milliseconds()
		errMsg := fmt.Sprintf("%s: %v", classifySIPError(err), err)
		return CheckResult{
			Status:       "error",
			ErrorMessage: &errMsg,
			LatencyMs:    &latencyMs,
		}
	}

	// Answer a digest challenge when credentials are configured. The retry
	// keeps the Call-ID and From tag and increments CSeq, per RFC 3261.
	authAttempted := false
	if (resp.StatusCode == 401 || resp.StatusCode == 407) && config.Username != "" && config.Password != "" {
		authHeaderName := "Authorization"
		challengeValue := resp.header("WWW-Authenticate")
		if resp.StatusCode == 407 {
			authHeaderName = "Proxy-Authorization"
			challengeValue = resp.header("Proxy-Authenticate")
		}
		if challengeValue == "" {
			latencyMs := time.Since(startTime).Milliseconds()
			errMsg := fmt.Sprintf("server returned %d without an authentication challenge", resp.StatusCode)
			return CheckResult{
				Status:       "failure",
				HTTPStatus:   &resp.StatusCode,
				ErrorMessage: &errMsg,
				LatencyMs:    &latencyMs,
			}
		}

		challenge, parseErr := parseSIPDigestChallenge(challengeValue)
		var credentials string
		if parseErr == nil {
			credentials, parseErr = buildSIPDigestAuthorization(challenge, config.Username, config.Password, methodUpper, requestURI)
		}
		if parseErr != nil {
			latencyMs := time.Since(startTime).Milliseconds()
			errMsg := fmt.Sprintf("cannot answer authentication challenge: %v", parseErr)
			return CheckResult{
				Status:       "failure",
				HTTPStatus:   &resp.StatusCode,
				ErrorMessage: &errMsg,
				LatencyMs:    &latencyMs,
			}
		}

		params.CSeq = 2
		params.AuthHeader = authHeaderName + ": " + credentials
		resp, err = conn.roundTrip(buildSIPRequest(params))
		if err != nil {
			latencyMs := time.Since(startTime).Milliseconds()
			errMsg := fmt.Sprintf("%s: %v", classifySIPError(err), err)
			return CheckResult{
				Status:       "error",
				ErrorMessage: &errMsg,
				LatencyMs:    &latencyMs,
			}
		}
		authAttempted = true
	}

	latencyMs := time.Since(startTime).Milliseconds()

	expectedStatus := 200
	if config.ExpectedStatus != nil {
		expectedStatus = *config.ExpectedStatus
	}

	if resp.StatusCode != expectedStatus {
		var errMsg string
		switch {
		case authAttempted && (resp.StatusCode == 401 || resp.StatusCode == 407):
			errMsg = fmt.Sprintf("authentication failed: %d %s", resp.StatusCode, resp.Reason)
		case !authAttempted && (resp.StatusCode == 401 || resp.StatusCode == 407):
			errMsg = fmt.Sprintf("server requires authentication (%d %s) but no credentials are configured", resp.StatusCode, resp.Reason)
		default:
			errMsg = fmt.Sprintf("unexpected SIP status: got %d, expected %d", resp.StatusCode, expectedStatus)
		}
		return CheckResult{
			Status:       "failure",
			HTTPStatus:   &resp.StatusCode, // Reuse HTTPStatus for SIP status
			ErrorMessage: &errMsg,
			LatencyMs:    &latencyMs,
		}
	}

	return CheckResult{
		Status:     "success",
		HTTPStatus: &resp.StatusCode,
		LatencyMs:  &latencyMs,
	}
}

// sipRequestParams carries everything needed to build one SIP request.
type sipRequestParams struct {
	Method     string // OPTIONS or REGISTER (uppercase)
	RequestURI string
	Host       string
	Port       int
	Transport  string
	FromUser   string
	Domain     string
	CallID     string
	FromTag    string
	CSeq       int
	AuthHeader string // full header line, e.g. "Authorization: Digest …"
}

// buildSIPRequest constructs a SIP request message. OPTIONS keeps the legacy
// probe@monitor.local identity; REGISTER uses the configured AOR and sends no
// Contact header, making it a query-style registration check that never
// creates, refreshes, or removes bindings.
func buildSIPRequest(p sipRequestParams) string {
	branch := "z9hG4bK" + uuid.New().String()[:8]
	transportUpper := strings.ToUpper(p.Transport)

	lines := []string{
		fmt.Sprintf("%s %s SIP/2.0", p.Method, p.RequestURI),
		fmt.Sprintf("Via: SIP/2.0/%s %s;branch=%s;rport", transportUpper, "monitor.local:5060", branch),
		"Max-Forwards: 70",
	}

	if p.Method == "REGISTER" {
		aor := fmt.Sprintf("sip:%s@%s", p.FromUser, p.Domain)
		lines = append(lines,
			fmt.Sprintf("From: <%s>;tag=%s", aor, p.FromTag),
			fmt.Sprintf("To: <%s>", aor),
		)
	} else {
		lines = append(lines,
			fmt.Sprintf("From: <sip:probe@monitor.local>;tag=%s", p.FromTag),
			fmt.Sprintf("To: <sip:probe@%s:%d>", p.Host, p.Port),
		)
	}

	lines = append(lines,
		fmt.Sprintf("Call-ID: %s@monitor.local", p.CallID),
		fmt.Sprintf("CSeq: %d %s", p.CSeq, p.Method),
	)

	if p.Method != "REGISTER" {
		lines = append(lines,
			"Contact: <sip:probe@monitor.local:5060>",
			"Accept: application/sdp",
		)
	}

	if p.AuthHeader != "" {
		lines = append(lines, p.AuthHeader)
	}

	lines = append(lines, "Content-Length: 0", "", "")
	return strings.Join(lines, "\r\n")
}

// sipConn is a single connected SIP transport. roundTrip may be called more
// than once (e.g. for a digest retry) on the same connection.
type sipConn struct {
	transport string
	conn      net.Conn
	reader    *bufio.Reader
}

func (c *SIPChecker) dialSIP(ctx context.Context, transport, host string, port int, config *models.SIPMonitorConfig, timeout time.Duration) (*sipConn, error) {
	addr := net.JoinHostPort(host, strconv.Itoa(port))

	deadline := time.Now().Add(timeout)
	if ctxDeadline, ok := ctx.Deadline(); ok && ctxDeadline.Before(deadline) {
		deadline = ctxDeadline
	}
	guard := newDialGuard(c.blockPrivateIPs, c.allowedCIDRs, time.Until(deadline))

	var conn net.Conn
	var err error
	switch transport {
	case "tcp":
		conn, err = guard.DialContext(ctx, "tcp", addr)
	case "tls":
		serverName := strings.TrimSpace(config.TLSServerName)
		if serverName == "" {
			serverName = config.Host
		}
		var rawConn net.Conn
		rawConn, err = guard.DialContext(ctx, "tcp", addr)
		if err == nil {
			tlsConn := tls.Client(rawConn, &tls.Config{
				ServerName:         serverName,
				InsecureSkipVerify: config.TLSSkipVerify, //nolint:gosec // explicit user opt-in for lab/self-signed servers
				MinVersion:         tls.VersionTLS12,
			})
			if err = tlsConn.SetDeadline(deadline); err == nil {
				err = tlsConn.HandshakeContext(ctx)
			}
			if err != nil {
				rawConn.Close()
			} else {
				conn = tlsConn
			}
		}
	default: // udp
		conn, err = guard.DialContext(ctx, "udp", addr)
	}
	if err != nil {
		return nil, fmt.Errorf("failed to connect: %w", err)
	}
	if err := conn.SetDeadline(deadline); err != nil {
		conn.Close()
		return nil, fmt.Errorf("failed to set deadline: %w", err)
	}

	sc := &sipConn{transport: transport, conn: conn}
	if transport != "udp" {
		sc.reader = bufio.NewReader(conn)
	}
	return sc, nil
}

func (c *sipConn) Close() {
	c.conn.Close()
}

// roundTrip sends one request and reads until a final (>= 200) response,
// skipping provisional 1xx responses.
func (c *sipConn) roundTrip(request string) (*sipResponse, error) {
	if _, err := c.conn.Write([]byte(request)); err != nil {
		return nil, fmt.Errorf("failed to send request: %w", err)
	}
	for {
		resp, err := c.readResponse()
		if err != nil {
			return nil, err
		}
		if resp.StatusCode >= 200 {
			return resp, nil
		}
	}
}

func (c *sipConn) readResponse() (*sipResponse, error) {
	if c.transport == "udp" {
		// One datagram carries one complete SIP message.
		buffer := make([]byte, 65535)
		n, err := c.conn.Read(buffer)
		if err != nil {
			return nil, fmt.Errorf("failed to read response: %w", err)
		}
		return parseSIPResponseMessage(string(buffer[:n]))
	}

	// Stream transports: status line, headers, then a Content-Length body
	// that must be consumed so the next message parses cleanly.
	statusLine, err := c.reader.ReadString('\n')
	if err != nil {
		return nil, fmt.Errorf("failed to read response: %w", err)
	}
	resp := &sipResponse{Headers: make(map[string][]string)}
	if err := parseSIPStatusLine(statusLine, resp); err != nil {
		return nil, err
	}

	for {
		line, err := c.reader.ReadString('\n')
		if err != nil {
			return nil, fmt.Errorf("failed to read response headers: %w", err)
		}
		line = strings.TrimRight(line, "\r\n")
		if line == "" {
			break
		}
		addSIPHeader(resp, line)
	}

	if length := sipContentLength(resp); length > 0 {
		if _, err := io.CopyN(io.Discard, c.reader, int64(length)); err != nil {
			return nil, fmt.Errorf("failed to read response body: %w", err)
		}
	}
	return resp, nil
}

// parseSIPResponseMessage parses a complete SIP message held in one string
// (the UDP path).
func parseSIPResponseMessage(message string) (*sipResponse, error) {
	normalized := strings.ReplaceAll(message, "\r\n", "\n")
	head, _, _ := strings.Cut(normalized, "\n\n")
	lines := strings.Split(head, "\n")
	if len(lines) == 0 {
		return nil, fmt.Errorf("empty SIP response")
	}

	resp := &sipResponse{Headers: make(map[string][]string)}
	if err := parseSIPStatusLine(lines[0], resp); err != nil {
		return nil, err
	}
	for _, line := range lines[1:] {
		if line = strings.TrimRight(line, "\r"); line != "" {
			addSIPHeader(resp, line)
		}
	}
	return resp, nil
}

// parseSIPStatusLine parses "SIP/2.0 <status> <reason>".
func parseSIPStatusLine(line string, resp *sipResponse) error {
	statusLine := strings.TrimSpace(line)
	parts := strings.SplitN(statusLine, " ", 3)
	if len(parts) < 2 {
		return fmt.Errorf("invalid SIP response format: %s", statusLine)
	}
	if !strings.HasPrefix(parts[0], "SIP/") {
		return fmt.Errorf("invalid SIP response: does not start with SIP/")
	}
	statusCode, err := strconv.Atoi(parts[1])
	if err != nil {
		return fmt.Errorf("invalid status code: %s", parts[1])
	}
	resp.StatusCode = statusCode
	if len(parts) == 3 {
		resp.Reason = strings.TrimSpace(parts[2])
	}
	return nil
}

func addSIPHeader(resp *sipResponse, line string) {
	name, value, found := strings.Cut(line, ":")
	if !found {
		return
	}
	key := strings.ToLower(strings.TrimSpace(name))
	resp.Headers[key] = append(resp.Headers[key], strings.TrimSpace(value))
}

func sipContentLength(resp *sipResponse) int {
	value := resp.header("Content-Length")
	if value == "" {
		value = resp.header("l") // compact form
	}
	if value == "" {
		return 0
	}
	length, err := strconv.Atoi(value)
	if err != nil || length < 0 {
		return 0
	}
	return length
}

func classifySIPError(err error) string {
	errStr := err.Error()
	switch {
	case strings.Contains(errStr, "timeout") || strings.Contains(errStr, "deadline"):
		return "timeout"
	case strings.Contains(errStr, "no such host") || strings.Contains(errStr, "DNS"):
		return "dns"
	case strings.Contains(errStr, "connection refused") || strings.Contains(errStr, "connect"):
		return "connect"
	case strings.Contains(errStr, "certificate") || strings.Contains(errStr, "tls"):
		return "tls"
	default:
		return "unknown"
	}
}

func resolveSIPTargetHost(host string) string {
	if !shouldRewriteSIPLoopbackToHostGateway() {
		return host
	}
	if isLoopbackSIPHost(host) {
		return "host.docker.internal"
	}
	return host
}

func shouldRewriteSIPLoopbackToHostGateway() bool {
	switch strings.ToLower(strings.TrimSpace(os.Getenv("SIP_LOCALHOST_AS_HOST_GATEWAY"))) {
	case "1", "true", "yes", "on":
		return true
	default:
		return false
	}
}

func isLoopbackSIPHost(host string) bool {
	trimmed := strings.TrimSpace(host)
	trimmed = strings.Trim(trimmed, "[]")

	if strings.EqualFold(trimmed, "localhost") {
		return true
	}

	ip := net.ParseIP(trimmed)
	return ip != nil && ip.IsLoopback()
}
