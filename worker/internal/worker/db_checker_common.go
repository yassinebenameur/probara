package worker

import (
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/yassinebenameur/probara/shared/models"
)

// evaluateValueAssertion compares an observed value (stringified) against an
// expectation. Shared by the Postgres query-value assertion; ops mirror the
// HTTP JSON assertion subset that makes sense for scalar DB values.
func evaluateValueAssertion(op, observed, expected string) (bool, error) {
	switch op {
	case "equals":
		return observed == expected, nil
	case "not_equals":
		return observed != expected, nil
	case "contains":
		return strings.Contains(observed, expected), nil
	case "number_gt", "number_gte", "number_lt", "number_lte":
		got, err := strconv.ParseFloat(strings.TrimSpace(observed), 64)
		if err != nil {
			return false, fmt.Errorf("observed value %q is not numeric", observed)
		}
		want, err := strconv.ParseFloat(strings.TrimSpace(expected), 64)
		if err != nil {
			return false, fmt.Errorf("expected value %q is not numeric", expected)
		}
		switch op {
		case "number_gt":
			return got > want, nil
		case "number_gte":
			return got >= want, nil
		case "number_lt":
			return got < want, nil
		default:
			return got <= want, nil
		}
	default:
		return false, fmt.Errorf("unknown op %q", op)
	}
}

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

// dbMetrics is the per-check metrics blob shared by the database/broker
// checkers; it lands in check_results.metrics_data keyed by monitor type,
// e.g. {"redis": {"server_version": "7.2", "role": "master"}}.
type dbMetrics struct {
	ServerVersion    string `json:"server_version,omitempty"`
	Product          string `json:"product,omitempty"` // e.g. RabbitMQ
	Role             string `json:"role,omitempty"`    // redis: master/replica; mongo: primary/secondary
	ReplicaSet       string `json:"replica_set,omitempty"`
	ConnectedClients *int64 `json:"connected_clients,omitempty"`
	UsedMemoryBytes  *int64 `json:"used_memory_bytes,omitempty"`
	// LatencyWarnMs is set when the round trip exceeded warn_latency_ms but
	// the check still succeeded — surfaced as a warning in the UI. The
	// check-result status pipeline only knows success/failure/error, so this
	// deliberately does not change the status.
	LatencyWarnMs *int64 `json:"latency_warn_ms,omitempty"`
}

// dbLatencyResult finishes a round trip that reached the server: it applies
// the max-latency (fail) and warn-latency (flag-only) assertions and attaches
// the metrics envelope under the monitor type's key.
func dbLatencyResult(monitorType string, latencyMs int64, maxLatencyMs, warnLatencyMs *int64, metrics *dbMetrics) CheckResult {
	if metrics == nil {
		metrics = &dbMetrics{}
	}
	return dbLatencyResultEnvelope(monitorType, latencyMs, maxLatencyMs, warnLatencyMs, metrics, metrics)
}

// dbLatencyResultEnvelope is dbLatencyResult for checkers whose metrics are a
// type-specific superset of dbMetrics: `base` points at the embedded dbMetrics
// (where the warn flag is set), `payload` is what is marshaled under the
// monitor-type key.
func dbLatencyResultEnvelope(monitorType string, latencyMs int64, maxLatencyMs, warnLatencyMs *int64, base *dbMetrics, payload any) CheckResult {
	if warnLatencyMs != nil && *warnLatencyMs > 0 && latencyMs > *warnLatencyMs {
		base.LatencyWarnMs = warnLatencyMs
	}

	metricsJSON := dbMetricsEnvelope(monitorType, payload)

	if maxLatencyMs != nil && *maxLatencyMs > 0 && latencyMs > *maxLatencyMs {
		errMsg := fmt.Sprintf("latency: %dms > %dms", latencyMs, *maxLatencyMs)
		return CheckResult{
			Status:       "failure",
			LatencyMs:    &latencyMs,
			ErrorMessage: &errMsg,
			MetricsData:  metricsJSON,
		}
	}
	return CheckResult{
		Status:      "success",
		LatencyMs:   &latencyMs,
		MetricsData: metricsJSON,
	}
}

// dbFailureResult finishes a check whose assertion failed after a successful
// round trip, keeping the metrics envelope attached.
func dbFailureResult(monitorType string, latencyMs int64, metrics *dbMetrics, errMsg string) CheckResult {
	if metrics == nil {
		metrics = &dbMetrics{}
	}
	return dbFailureResultEnvelope(monitorType, latencyMs, metrics, errMsg)
}

// dbFailureResultEnvelope is dbFailureResult with an arbitrary metrics
// payload (see dbLatencyResultEnvelope).
func dbFailureResultEnvelope(monitorType string, latencyMs int64, payload any, errMsg string) CheckResult {
	return CheckResult{
		Status:       "failure",
		LatencyMs:    &latencyMs,
		ErrorMessage: &errMsg,
		MetricsData:  dbMetricsEnvelope(monitorType, payload),
	}
}

func dbMetricsEnvelope(monitorType string, payload any) json.RawMessage {
	b, err := json.Marshal(map[string]any{monitorType: payload})
	if err != nil {
		return nil
	}
	return b
}
