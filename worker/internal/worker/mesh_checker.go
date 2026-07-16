package worker

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/yassinebenameur/probara/shared/models"
)

// MeshChecker implements Checker for inter-location mesh probes: an HTTP GET
// against another location's /mesh/echo endpoint. Success requires both a 200
// and that the echoed location ID matches the intended target — a reachable
// but misrouted endpoint (stale DNS, NAT collision, wrong Service) is a
// failure, not a pass. Dialing goes through dialGuard so mesh probes inherit
// the same SSRF policy as every other checker.
type MeshChecker struct {
	blockPrivateIPs bool
	allowedCIDRs    []*net.IPNet
}

// NewMeshChecker creates a new mesh probe checker.
func NewMeshChecker(blockPrivateIPs bool, allowedCIDRs []*net.IPNet) *MeshChecker {
	return &MeshChecker{blockPrivateIPs: blockPrivateIPs, allowedCIDRs: allowedCIDRs}
}

type meshMetricsEnvelope struct {
	Mesh *meshMetrics `json:"mesh,omitempty"`
}

type meshMetrics struct {
	Endpoint         string `json:"endpoint"`
	TargetLocationID string `json:"target_location_id"`
	EchoHostname     string `json:"echo_hostname,omitempty"`
}

// Check probes the target location's echo endpoint.
func (c *MeshChecker) Check(ctx context.Context, configRaw json.RawMessage, timeoutSeconds int) CheckResult {
	var config models.MeshProbeConfig
	if err := json.Unmarshal(configRaw, &config); err != nil {
		errMsg := fmt.Sprintf("failed to unmarshal mesh_probe config: %v", err)
		return CheckResult{Status: "error", ErrorMessage: &errMsg}
	}

	if _, err := uuid.Parse(config.TargetLocationID); err != nil {
		errMsg := "target_location_id must be a UUID"
		return CheckResult{Status: "error", ErrorMessage: &errMsg}
	}
	host, portStr, err := net.SplitHostPort(strings.TrimSpace(config.Endpoint))
	if err != nil || host == "" {
		errMsg := "endpoint must be host:port"
		return CheckResult{Status: "error", ErrorMessage: &errMsg}
	}
	if port, err := strconv.Atoi(portStr); err != nil || port < 1 || port > 65535 {
		errMsg := "endpoint port must be between 1 and 65535"
		return CheckResult{Status: "error", ErrorMessage: &errMsg}
	}

	timeout := time.Duration(timeoutSeconds) * time.Second
	checkCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	guard := newDialGuard(c.blockPrivateIPs, c.allowedCIDRs, timeout)
	client := &http.Client{
		Timeout: timeout,
		Transport: &http.Transport{
			DialContext:       guard.DialContext,
			DisableKeepAlives: true,
		},
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}

	url := "http://" + net.JoinHostPort(host, portStr) + models.MeshEchoPath
	req, err := http.NewRequestWithContext(checkCtx, http.MethodGet, url, nil)
	if err != nil {
		errMsg := fmt.Sprintf("build request: %v", err)
		return CheckResult{Status: "error", ErrorMessage: &errMsg}
	}

	metrics := meshMetrics{Endpoint: config.Endpoint, TargetLocationID: config.TargetLocationID}

	startTime := time.Now()
	resp, err := client.Do(req)
	if err != nil {
		latencyMs := time.Since(startTime).Milliseconds()
		errMsg := fmt.Sprintf("%s: %v", tcpErrorReason(err), err)
		// A blocked connection is a checker-policy problem (error), not a
		// statement about the edge's health (failure).
		status := "failure"
		if strings.Contains(err.Error(), "ssrf_blocked") {
			status = "error"
		}
		return CheckResult{Status: status, ErrorMessage: &errMsg, LatencyMs: &latencyMs}
	}
	defer resp.Body.Close()

	var echo models.MeshEchoResponse
	decodeErr := json.NewDecoder(resp.Body).Decode(&echo)
	latencyMs := time.Since(startTime).Milliseconds()

	if resp.StatusCode != http.StatusOK {
		errMsg := fmt.Sprintf("echo endpoint returned HTTP %d", resp.StatusCode)
		return CheckResult{Status: "failure", ErrorMessage: &errMsg, LatencyMs: &latencyMs}
	}
	if decodeErr != nil {
		errMsg := fmt.Sprintf("invalid echo response: %v", decodeErr)
		return CheckResult{Status: "failure", ErrorMessage: &errMsg, LatencyMs: &latencyMs}
	}
	if echo.LocationID != config.TargetLocationID {
		errMsg := fmt.Sprintf("mesh_endpoint_mismatch: echoed %q, expected %q", echo.LocationID, config.TargetLocationID)
		return CheckResult{Status: "failure", ErrorMessage: &errMsg, LatencyMs: &latencyMs}
	}

	metrics.EchoHostname = echo.Hostname
	metricsJSON, _ := json.Marshal(meshMetricsEnvelope{Mesh: &metrics})
	return CheckResult{Status: "success", LatencyMs: &latencyMs, MetricsData: metricsJSON}
}
