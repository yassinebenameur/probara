package worker

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/gobwas/ws"
	"github.com/gobwas/ws/wsutil"

	"github.com/yassinebenameur/probara/shared/models"
)

// WebSocketChecker implements Checker for WebSocket monitors: a full ws:// or
// wss:// upgrade handshake — proving the HTTP upgrade path, auth headers, and
// TLS work end to end — optionally followed by a send/expect round trip on
// the open socket. Connection setup goes through dialGuard so it inherits the
// same SSRF policy as the other checkers.
type WebSocketChecker struct {
	blockPrivateIPs bool
	allowedCIDRs    []*net.IPNet
}

// NewWebSocketChecker creates a new WebSocket checker.
func NewWebSocketChecker(blockPrivateIPs bool, allowedCIDRs []*net.IPNet) *WebSocketChecker {
	return &WebSocketChecker{blockPrivateIPs: blockPrivateIPs, allowedCIDRs: allowedCIDRs}
}

type wsMetricsEnvelope struct {
	WebSocket *wsMetrics `json:"websocket,omitempty"`
}

type wsMetrics struct {
	Subprotocol string `json:"subprotocol,omitempty"`
	TLSVersion  string `json:"tls_version,omitempty"`
	// LatencyWarnMs mirrors the database checkers: set when the check
	// succeeded but exceeded warn_latency_ms.
	LatencyWarnMs *int64 `json:"latency_warn_ms,omitempty"`
}

// Check performs a WebSocket handshake (and optional message exchange).
func (c *WebSocketChecker) Check(ctx context.Context, configRaw json.RawMessage, timeoutSeconds int) CheckResult {
	var config models.WebSocketMonitorConfig
	if err := json.Unmarshal(configRaw, &config); err != nil {
		errMsg := fmt.Sprintf("failed to unmarshal websocket config: %v", err)
		return CheckResult{Status: "error", ErrorMessage: &errMsg}
	}

	rawURL := strings.TrimSpace(config.URL)
	u, err := url.Parse(rawURL)
	if err != nil || (u.Scheme != "ws" && u.Scheme != "wss") || u.Host == "" {
		errMsg := "url must be a valid ws:// or wss:// URL"
		return CheckResult{Status: "error", ErrorMessage: &errMsg}
	}

	timeout := time.Duration(timeoutSeconds) * time.Second
	checkCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	guard := newDialGuard(c.blockPrivateIPs, c.allowedCIDRs, timeout)

	tlsCfg := &tls.Config{} // ServerName is filled in per-dial by the dialer
	if config.TLSSkipVerify != nil && *config.TLSSkipVerify {
		tlsCfg.InsecureSkipVerify = true
	}

	header := http.Header{}
	for name, value := range config.Headers {
		if strings.TrimSpace(name) != "" {
			header.Set(name, value)
		}
	}

	dialer := ws.Dialer{
		NetDial:   guard.DialContext,
		TLSConfig: tlsCfg,
		Timeout:   timeout,
	}
	if len(header) > 0 {
		dialer.Header = ws.HandshakeHeaderHTTP(header)
	}

	startTime := time.Now()
	conn, br, hs, err := dialer.Dial(checkCtx, rawURL)
	if err != nil {
		latencyMs := time.Since(startTime).Milliseconds()
		errMsg := fmt.Sprintf("%s: %v", wsErrorReason(err), err)
		// A blocked connection is a checker-policy problem (error), not a
		// statement about the target's health (failure).
		status := "failure"
		if strings.Contains(err.Error(), "ssrf_blocked") {
			status = "error"
		}
		return CheckResult{Status: status, ErrorMessage: &errMsg, LatencyMs: &latencyMs}
	}
	defer conn.Close()

	metrics := wsMetrics{Subprotocol: hs.Protocol}
	if tlsConn, ok := conn.(*tls.Conn); ok {
		metrics.TLSVersion = tlsVersionName(tlsConn.ConnectionState().Version)
	}

	// The handshake consumed the ctx deadline; the message exchange below
	// works on the raw conn, so carry the timeout over as an I/O deadline.
	_ = conn.SetDeadline(time.Now().Add(time.Until(startTime.Add(timeout))))

	// br holds bytes the server sent right after the 101 (e.g. a greeting
	// frame) — reads must drain it before touching the conn again.
	var reader io.Reader = conn
	if br != nil {
		reader = br
	}
	rw := struct {
		io.Reader
		io.Writer
	}{reader, conn}

	if config.SendMessage != nil && *config.SendMessage != "" {
		if err := wsutil.WriteClientText(conn, []byte(*config.SendMessage)); err != nil {
			latencyMs := time.Since(startTime).Milliseconds()
			errMsg := fmt.Sprintf("send: %v", err)
			return CheckResult{Status: "failure", ErrorMessage: &errMsg, LatencyMs: &latencyMs}
		}
	}

	if config.ExpectedSubstring != nil && *config.ExpectedSubstring != "" {
		data, _, err := wsutil.ReadServerData(rw)
		latencyMs := time.Since(startTime).Milliseconds()
		if err != nil {
			errMsg := fmt.Sprintf("receive: %v", err)
			return CheckResult{Status: "failure", ErrorMessage: &errMsg, LatencyMs: &latencyMs}
		}
		if !strings.Contains(string(data), *config.ExpectedSubstring) {
			errMsg := fmt.Sprintf("expected_substring: reply does not contain %q", *config.ExpectedSubstring)
			metricsJSON, _ := json.Marshal(wsMetricsEnvelope{WebSocket: &metrics})
			return CheckResult{Status: "failure", ErrorMessage: &errMsg, LatencyMs: &latencyMs, MetricsData: metricsJSON}
		}
	}

	latencyMs := time.Since(startTime).Milliseconds()

	if config.WarnLatencyMs != nil && *config.WarnLatencyMs > 0 && latencyMs > *config.WarnLatencyMs {
		metrics.LatencyWarnMs = config.WarnLatencyMs
	}
	metricsJSON, _ := json.Marshal(wsMetricsEnvelope{WebSocket: &metrics})

	if config.MaxLatencyMs != nil && *config.MaxLatencyMs > 0 && latencyMs > *config.MaxLatencyMs {
		errMsg := fmt.Sprintf("latency: %dms > %dms", latencyMs, *config.MaxLatencyMs)
		return CheckResult{Status: "failure", ErrorMessage: &errMsg, LatencyMs: &latencyMs, MetricsData: metricsJSON}
	}

	return CheckResult{Status: "success", LatencyMs: &latencyMs, MetricsData: metricsJSON}
}

func wsErrorReason(err error) string {
	errStr := strings.ToLower(err.Error())
	switch {
	case strings.Contains(errStr, "ssrf_blocked"):
		return "ssrf_blocked"
	case strings.Contains(errStr, "timeout"), strings.Contains(errStr, "deadline"):
		return "timeout"
	case strings.Contains(errStr, "no such host"), strings.Contains(errStr, "dns"):
		return "dns"
	case strings.Contains(errStr, "tls"), strings.Contains(errStr, "certificate"):
		return "tls"
	case strings.Contains(errStr, "unexpected http status"), strings.Contains(errStr, "handshake"):
		return "handshake"
	default:
		return "connect"
	}
}
