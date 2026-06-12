package worker

import (
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"strings"

	"github.com/yassinebenameur/probara/shared/models"
)

// applyDBTLSMaterial merges pasted PEM material (private CA, mTLS client
// pair) into a TLS config. When material is present and base is nil, a config
// is created — pasting certificates implies the connection should use TLS.
// Returns base unchanged when no material is configured.
func applyDBTLSMaterial(base *tls.Config, material models.DBTLSConfig, serverName string) (*tls.Config, error) {
	caPem := pemValue(material.TLSCAPem)
	certPem := pemValue(material.TLSClientCertPem)
	keyPem := pemValue(material.TLSClientKeyPem)
	if caPem == "" && certPem == "" && keyPem == "" {
		return base, nil
	}

	cfg := base
	if cfg == nil {
		cfg = &tls.Config{ServerName: serverName}
	}

	if caPem != "" {
		pool := x509.NewCertPool()
		if !pool.AppendCertsFromPEM([]byte(caPem)) {
			return nil, errors.New("tls_ca_pem: failed to parse PEM")
		}
		cfg.RootCAs = pool
	}

	if (certPem == "") != (keyPem == "") {
		return nil, errors.New("tls_client_cert_pem and tls_client_key_pem must be provided together")
	}
	if certPem != "" {
		cert, err := tls.X509KeyPair([]byte(certPem), []byte(keyPem))
		if err != nil {
			return nil, fmt.Errorf("tls client pair: %w", err)
		}
		cfg.Certificates = []tls.Certificate{cert}
	}

	return cfg, nil
}

func pemValue(v *string) string {
	if v == nil {
		return ""
	}
	return strings.TrimSpace(*v)
}

// classifyDBError buckets a database client error into the same reason
// prefixes the HTTP/gRPC checkers use, so error messages stay greppable
// across monitor types.
func classifyDBError(err error) string {
	errStr := strings.ToLower(err.Error())
	switch {
	case strings.Contains(errStr, "ssrf_blocked"):
		return "ssrf_blocked"
	case strings.Contains(errStr, "timeout"), strings.Contains(errStr, "deadline"):
		return "timeout"
	case strings.Contains(errStr, "no such host"), strings.Contains(errStr, "dns"):
		return "dns"
	case strings.Contains(errStr, "authentication"), strings.Contains(errStr, "auth"),
		strings.Contains(errStr, "password"), strings.Contains(errStr, "wrongpass"),
		strings.Contains(errStr, "noauth"), strings.Contains(errStr, "permission denied"):
		return "auth"
	case strings.Contains(errStr, "tls"), strings.Contains(errStr, "certificate"):
		return "tls"
	case strings.Contains(errStr, "connection refused"), strings.Contains(errStr, "connect"),
		strings.Contains(errStr, "broken pipe"), strings.Contains(errStr, "reset by peer"):
		return "connect"
	default:
		return "check"
	}
}

func dbErrorResult(reason string, err error, latencyMs *int64) CheckResult {
	errMsg := fmt.Sprintf("%s: %v", reason, err)
	return CheckResult{
		Status:       "error",
		ErrorMessage: &errMsg,
		LatencyMs:    latencyMs,
	}
}

// dbLatencyResult finishes a successful round trip, applying the optional
// max-latency assertion.
func dbLatencyResult(latencyMs int64, maxLatencyMs *int64) CheckResult {
	if maxLatencyMs != nil && *maxLatencyMs > 0 && latencyMs > *maxLatencyMs {
		errMsg := fmt.Sprintf("latency: %dms > %dms", latencyMs, *maxLatencyMs)
		return CheckResult{
			Status:       "failure",
			LatencyMs:    &latencyMs,
			ErrorMessage: &errMsg,
		}
	}
	return CheckResult{
		Status:    "success",
		LatencyMs: &latencyMs,
	}
}
