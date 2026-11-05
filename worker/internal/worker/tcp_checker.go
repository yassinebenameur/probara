package worker

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"fmt"
	"net"
	"strings"
	"time"

	"github.com/yassinebenameur/probara/shared/models"
	"github.com/yassinebenameur/probara/shared/tlscert"
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
	// TLS is the server certificate (shared/tlscert owns where readers find
	// it: tcp.tls), recorded under tls_skip_verify too.
	TLS              *tlscert.Info `json:"tls,omitempty"`
	AssertionsFailed []string      `json:"assertions_failed,omitempty"`
}

// Check performs a TCP connect (and optional TLS handshake).
func (c *TCPChecker) Check(ctx context.Context, configRaw json.RawMessage, timeoutSeconds int) CheckResult {
	var config models.TCPMonitorConfig
	if err := json.Unmarshal(configRaw, &config); err != nil {
		errMsg := fmt.Sprintf("failed to unmarshal tcp config: %v", err)
		return CheckResult{Status: "error", ErrorMessage: &errMsg}
	}

	if err := config.Validate(); err != nil {
		errMsg := err.Error()
		return CheckResult{Status: "error", ErrorMessage: &errMsg}
	}
	host := strings.TrimSpace(config.Host)
	useTLS := config.TLSEnabled()

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
		tlsCfg, err := tcpTLSConfig(&config, host)
		if err != nil {
			errMsg := err.Error()
			return CheckResult{Status: "error", ErrorMessage: &errMsg}
		}
		tlsConn := tls.Client(conn, tlsCfg)
		if err := tlsConn.HandshakeContext(checkCtx); err != nil {
			latencyMs := time.Since(startTime).Milliseconds()
			errMsg := fmt.Sprintf("tls handshake: %v", err)
			return CheckResult{Status: "failure", ErrorMessage: &errMsg, LatencyMs: &latencyMs}
		}
		state := tlsConn.ConnectionState()
		metrics.TLSVersion = tlsVersionName(state.Version)
		metrics.TLS = tlscert.FromConnectionState(state, tlsCfg.ServerName, time.Now())
	}

	latencyMs := time.Since(startTime).Milliseconds()
	// A certificate inside the tls_min_days_valid window does NOT fail the
	// check: the alerter reads the recorded expiry (shared/tlscert) and opens
	// a tls_expiry alert. Only a handshake that yielded no certificate to
	// inspect fails, since the threshold could never be evaluated.
	if config.TLSMinDaysValid != nil && (metrics.TLS == nil || metrics.TLS.DaysUntilExpiry == nil) {
		const assertion = "tls: missing certificate info"
		metrics.AssertionsFailed = append(metrics.AssertionsFailed, assertion)
		metricsJSON, _ := json.Marshal(tcpMetricsEnvelope{TCP: &metrics})
		errMsg := assertion
		return CheckResult{Status: "failure", ErrorMessage: &errMsg, LatencyMs: &latencyMs, MetricsData: metricsJSON}
	}
	metricsJSON, _ := json.Marshal(tcpMetricsEnvelope{TCP: &metrics})
	return CheckResult{Status: "success", LatencyMs: &latencyMs, MetricsData: metricsJSON}
}

// tcpTLSConfig is the client TLS config: SNI and verification name from
// tls_server_name (else host), tls_ca_pem added to the system roots, and
// tls_skip_verify.
func tcpTLSConfig(config *models.TCPMonitorConfig, host string) (*tls.Config, error) {
	tlsCfg := &tls.Config{ServerName: host}
	if config.TLSServerName != nil && strings.TrimSpace(*config.TLSServerName) != "" {
		tlsCfg.ServerName = strings.TrimSpace(*config.TLSServerName)
	}
	if config.TLSSkipVerify != nil && *config.TLSSkipVerify {
		tlsCfg.InsecureSkipVerify = true
	}
	if config.TLSCAPem != nil && strings.TrimSpace(*config.TLSCAPem) != "" {
		pool, err := x509.SystemCertPool()
		if err != nil || pool == nil {
			pool = x509.NewCertPool()
		}
		if !pool.AppendCertsFromPEM([]byte(*config.TLSCAPem)) {
			return nil, fmt.Errorf("tls_ca_pem: failed to parse PEM")
		}
		tlsCfg.RootCAs = pool
	}
	return tlsCfg, nil
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
