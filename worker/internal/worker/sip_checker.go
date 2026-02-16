package worker

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/yassinebenameur/probara/shared/models"
)

// SIPChecker implements Checker for SIP monitors
type SIPChecker struct{}

// NewSIPChecker creates a new SIP checker
func NewSIPChecker() *SIPChecker {
	return &SIPChecker{}
}

// Check performs a SIP OPTIONS check
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
	if config.Port == 0 {
		config.Port = 5060
	}
	if config.Transport == "" {
		config.Transport = "udp"
	}

	startTime := time.Now()
	timeout := time.Duration(timeoutSeconds) * time.Second

	// Build the SIP OPTIONS request
	request := c.buildOptionsRequest(config.Host, config.Port, config.Transport)
	targetHost := resolveSIPTargetHost(config.Host)

	var statusCode int
	var err error

	switch strings.ToLower(config.Transport) {
	case "tcp":
		statusCode, err = c.sendTCP(ctx, targetHost, config.Port, request, timeout)
	default: // udp
		statusCode, err = c.sendUDP(ctx, targetHost, config.Port, request, timeout)
	}

	latencyMs := time.Since(startTime).Milliseconds()

	if err != nil {
		errorReason := "unknown"
		errStr := err.Error()
		if strings.Contains(errStr, "timeout") || strings.Contains(errStr, "deadline") {
			errorReason = "timeout"
		} else if strings.Contains(errStr, "no such host") || strings.Contains(errStr, "DNS") {
			errorReason = "dns"
		} else if strings.Contains(errStr, "connection refused") || strings.Contains(errStr, "connect") {
			errorReason = "connect"
		}

		errMsg := fmt.Sprintf("%s: %v", errorReason, err)
		return CheckResult{
			Status:       "error",
			ErrorMessage: &errMsg,
			LatencyMs:    &latencyMs,
		}
	}

	// Check expected status code
	expectedStatus := 200
	if config.ExpectedStatus != nil {
		expectedStatus = *config.ExpectedStatus
	}

	if statusCode != expectedStatus {
		errMsg := fmt.Sprintf("unexpected SIP status: got %d, expected %d", statusCode, expectedStatus)
		return CheckResult{
			Status:       "failure",
			HTTPStatus:   &statusCode, // Reuse HTTPStatus for SIP status
			ErrorMessage: &errMsg,
			LatencyMs:    &latencyMs,
		}
	}

	return CheckResult{
		Status:     "success",
		HTTPStatus: &statusCode,
		LatencyMs:  &latencyMs,
	}
}

// buildOptionsRequest constructs a SIP OPTIONS request
func (c *SIPChecker) buildOptionsRequest(host string, port int, transport string) string {
	callID := uuid.New().String()
	branch := "z9hG4bK" + uuid.New().String()[:8]
	tag := uuid.New().String()[:8]

	transportUpper := strings.ToUpper(transport)
	targetURI := fmt.Sprintf("sip:%s:%d", host, port)

	// Build the SIP OPTIONS message
	// Note: We use a simple local address placeholder since the actual local addr
	// will be determined when we create the connection
	lines := []string{
		fmt.Sprintf("OPTIONS %s SIP/2.0", targetURI),
		fmt.Sprintf("Via: SIP/2.0/%s %s;branch=%s;rport", transportUpper, "monitor.local:5060", branch),
		"Max-Forwards: 70",
		fmt.Sprintf("From: <sip:probe@monitor.local>;tag=%s", tag),
		fmt.Sprintf("To: <sip:probe@%s:%d>", host, port),
		fmt.Sprintf("Call-ID: %s@monitor.local", callID),
		"CSeq: 1 OPTIONS",
		"Contact: <sip:probe@monitor.local:5060>",
		"Accept: application/sdp",
		"Content-Length: 0",
		"",
		"",
	}

	return strings.Join(lines, "\r\n")
}

// sendUDP sends the SIP request via UDP and returns the response status code
func (c *SIPChecker) sendUDP(ctx context.Context, host string, port int, request string, timeout time.Duration) (int, error) {
	addr := fmt.Sprintf("%s:%d", host, port)

	// Resolve the address
	udpAddr, err := net.ResolveUDPAddr("udp", addr)
	if err != nil {
		return 0, fmt.Errorf("failed to resolve UDP address: %w", err)
	}

	// Create UDP connection
	conn, err := net.DialUDP("udp", nil, udpAddr)
	if err != nil {
		return 0, fmt.Errorf("failed to connect: %w", err)
	}
	defer conn.Close()

	// Set deadline
	deadline, ok := ctx.Deadline()
	if !ok {
		deadline = time.Now().Add(timeout)
	}
	conn.SetDeadline(deadline)

	// Send the request
	_, err = conn.Write([]byte(request))
	if err != nil {
		return 0, fmt.Errorf("failed to send request: %w", err)
	}

	// Read response
	buffer := make([]byte, 4096)
	n, err := conn.Read(buffer)
	if err != nil {
		return 0, fmt.Errorf("failed to read response: %w", err)
	}

	// Parse the status code from the response
	response := string(buffer[:n])
	return c.parseStatusCode(response)
}

// sendTCP sends the SIP request via TCP and returns the response status code
func (c *SIPChecker) sendTCP(ctx context.Context, host string, port int, request string, timeout time.Duration) (int, error) {
	addr := fmt.Sprintf("%s:%d", host, port)

	// Create TCP connection with timeout
	dialer := net.Dialer{
		Timeout: timeout,
	}

	conn, err := dialer.DialContext(ctx, "tcp", addr)
	if err != nil {
		return 0, fmt.Errorf("failed to connect: %w", err)
	}
	defer conn.Close()

	// Set deadline
	deadline, ok := ctx.Deadline()
	if !ok {
		deadline = time.Now().Add(timeout)
	}
	conn.SetDeadline(deadline)

	// Send the request
	_, err = conn.Write([]byte(request))
	if err != nil {
		return 0, fmt.Errorf("failed to send request: %w", err)
	}

	// Read the first line of the response
	reader := bufio.NewReader(conn)
	line, err := reader.ReadString('\n')
	if err != nil {
		return 0, fmt.Errorf("failed to read response: %w", err)
	}

	return c.parseStatusCode(line)
}

// parseStatusCode extracts the status code from a SIP response line
// Format: SIP/2.0 200 OK
func (c *SIPChecker) parseStatusCode(response string) (int, error) {
	// Get the first line
	lines := strings.SplitN(response, "\r\n", 2)
	if len(lines) == 0 {
		lines = strings.SplitN(response, "\n", 2)
	}

	statusLine := strings.TrimSpace(lines[0])

	// Parse: SIP/2.0 <status_code> <reason>
	parts := strings.SplitN(statusLine, " ", 3)
	if len(parts) < 2 {
		return 0, fmt.Errorf("invalid SIP response format: %s", statusLine)
	}

	if !strings.HasPrefix(parts[0], "SIP/") {
		return 0, fmt.Errorf("invalid SIP response: does not start with SIP/")
	}

	statusCode, err := strconv.Atoi(parts[1])
	if err != nil {
		return 0, fmt.Errorf("invalid status code: %s", parts[1])
	}

	return statusCode, nil
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
