package worker

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"net"
	"strings"
	"time"

	"github.com/yassinebenameur/probara/shared/models"
)

// TCPChecker implements Checker for raw TCP connect monitors. It validates that
// a host:port accepts a connection — proving route/security-group/listener
// reachability without requiring an application protocol on the target
// (databases, brokers, SSH, internal LBs). It optionally completes a TLS
// handshake. Connection setup goes through dialGuard so it inherits the same
// SSRF policy as the HTTP, gRPC, and database checkers.
type TCPChecker struct {
	blockPrivateIPs bool
	allowedCIDRs    []*net.IPNet
}

// NewTCPChecker creates a new TCP connect checker.
func NewTCPChecker(blockPrivateIPs bool, allowedCIDRs []*net.IPNet) *TCPChecker {
	return &TCPChecker{blockPrivateIPs: blockPrivateIPs, allowedCIDRs: allowedCIDRs}
}

type tcpMetricsEnvelope struct {
	TCP *tcpMetrics `json:"tcp,omitempty"`
}

type tcpMetrics struct {
	Host       string `json:"host,omitempty"`
	Port       int    `json:"port,omitempty"`
	UseTLS     bool   `json:"use_tls"`
	TLSVersion string `json:"tls_version,omitempty"`
}

// Check performs a TCP connect (and optional TLS handshake).
func (c *TCPChecker) Check(ctx context.Context, configRaw json.RawMessage, timeoutSeconds int) CheckResult {
	var config models.TCPMonitorConfig
	if err := json.Unmarshal(configRaw, &config); err != nil {
		errMsg := fmt.Sprintf("failed to unmarshal tcp config: %v", err)
		return CheckResult{Status: "error", ErrorMessage: &errMsg}
	}

	host := strings.TrimSpace(config.Host)
	if host == "" {
		errMsg := "host is required"
		return CheckResult{Status: "error", ErrorMessage: &errMsg}
	}
	if config.Port < 1 || config.Port > 65535 {
		errMsg := "port must be between 1 and 65535"
		return CheckResult{Status: "error", ErrorMessage: &errMsg}
	}

	useTLS := config.UseTLS != nil && *config.UseTLS

	timeout := time.Duration(timeoutSeconds) * time.Second
	checkCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	address := net.JoinHostPort(host, fmt.Sprintf("%d", config.Port))
	guard := newDialGuard(c.blockPrivateIPs, c.allowedCIDRs, timeout)

	startTime := time.Now()
	conn, err := guard.DialContext(checkCtx, "tcp", address)
	if err != nil {
		latencyMs := time.Since(startTime).Milliseconds()
		errMsg := fmt.Sprintf("%s: %v", tcpErrorReason(err), err)
		// A blocked connection is a checker-policy problem (error), not a
		// statement about the target's health (failure).
		status := "failure"
		if strings.HasPrefix(err.Error(), "ssrf_blocked") {
			status = "error"
		}
		return CheckResult{Status: status, ErrorMessage: &errMsg, LatencyMs: &latencyMs}
	}
	defer conn.Close()

	metrics := tcpMetrics{Host: host, Port: config.Port, UseTLS: useTLS}

	if useTLS {
		tlsCfg := &tls.Config{ServerName: host}
		if config.TLSSkipVerify != nil && *config.TLSSkipVerify {
			tlsCfg.InsecureSkipVerify = true
		}
		tlsConn := tls.Client(conn, tlsCfg)
		if err := tlsConn.HandshakeContext(checkCtx); err != nil {
			latencyMs := time.Since(startTime).Milliseconds()
			errMsg := fmt.Sprintf("tls handshake: %v", err)
			return CheckResult{Status: "failure", ErrorMessage: &errMsg, LatencyMs: &latencyMs}
		}
		metrics.TLSVersion = tlsVersionName(tlsConn.ConnectionState().Version)
	}

	latencyMs := time.Since(startTime).Milliseconds()
	metricsJSON, _ := json.Marshal(tcpMetricsEnvelope{TCP: &metrics})
	return CheckResult{Status: "success", LatencyMs: &latencyMs, MetricsData: metricsJSON}
}

func tcpErrorReason(err error) string {
	errStr := strings.ToLower(err.Error())
	switch {
	case strings.Contains(errStr, "timeout"), strings.Contains(errStr, "deadline"):
		return "timeout"
	case strings.Contains(errStr, "no such host"), strings.Contains(errStr, "dns"):
		return "dns"
	default:
		return "connect"
	}
}

func tlsVersionName(v uint16) string {
	switch v {
	case tls.VersionTLS13:
		return "TLS 1.3"
	case tls.VersionTLS12:
		return "TLS 1.2"
	case tls.VersionTLS11:
		return "TLS 1.1"
	case tls.VersionTLS10:
		return "TLS 1.0"
	default:
		return ""
	}
}
