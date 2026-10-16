package worker

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/yassinebenameur/probara/shared/models"
)

// SIPChecker implements Checker for SIP monitors. It supports OPTIONS
// availability pings and contact-less REGISTER checks (which exercise the
// registrar's digest authentication path without mutating any bindings),
// over UDP, TCP, or TLS. Dialing goes through dialGuard so SIP probes
// inherit the worker's private-IP (SSRF) policy, and every message of the
// check rides that one flow (sip_flow.go). Requests run as RFC 3261 client
// transactions (sip_txn.go): retransmitted on UDP, matched to their
// responses by Via branch and CSeq method; requests the server sends us are
// answered by sip_uas.go.
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
	// The API validates on write, but the payload may predate a rule (CR/LF
	// hardening) or come from a private-location queue; nothing reaches the
	// wire unless the shared rules pass here too.
	if err := config.Validate(); err != nil {
		errMsg := fmt.Sprintf("invalid SIP config: %v", err)
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
	// The check budget is the dial, every transaction and every
	// retransmission (Timer F).
	ctx, cancel := context.WithTimeout(ctx, time.Duration(timeoutSeconds)*time.Second)
	defer cancel()
	targetHost := resolveSIPTargetHost(config.Host)

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

	// From here on every result carries the metrics_data envelope
	// (sip_metrics.go): a check that fails to dial still records where it
	// went and how long transport setup took.
	metrics := &sipMetrics{
		Method:     methodUpper,
		Transport:  transport,
		RequestURI: requestURI,
		Target:     newSIPTargetMetrics(targetHost),
	}
	var endpoint *sipEndpoint
	finish := func(result CheckResult) CheckResult {
		latencyMs := time.Since(startTime).Milliseconds()
		if endpoint != nil {
			metrics.recordEndpoint(endpoint)
		}
		result.LatencyMs = &latencyMs
		result.MetricsData = metrics.marshal(latencyMs)
		return result
	}
	errorResult := func(err error) CheckResult {
		errMsg := fmt.Sprintf("%s: %v", classifySIPError(err), err)
		return finish(CheckResult{Status: "error", ErrorMessage: &errMsg})
	}
	failure := func(resp *sipMessage, errMsg string) CheckResult {
		return finish(CheckResult{Status: "failure", HTTPStatus: &resp.StatusCode, ErrorMessage: &errMsg})
	}

	flow, err := c.dialSIP(ctx, transport, targetHost, config.Port, &config)
	if err != nil {
		var dialErr *sipDialError
		if errors.As(err, &dialErr) {
			metrics.recordDialTiming(dialErr.timing)
		}
		return errorResult(err)
	}
	metrics.recordFlow(flow)
	endpoint = newSIPEndpoint(flow)
	defer endpoint.Close()

	// REGISTER uses the configured AOR and sends no Contact header, making
	// it a query-style registration check that never creates, refreshes, or
	// removes bindings. OPTIONS keeps the probe@monitor.local identity.
	params := sipRequestParams{
		Method:     methodUpper,
		RequestURI: requestURI,
		FromTag:    sipRandomToken(4),
		CallID:     uuid.New().String() + "@monitor.local",
		CSeq:       1,
	}
	metrics.CallID = params.CallID
	if method == "register" {
		aor := fmt.Sprintf("<sip:%s@%s>", fromUser, domain)
		params.From, params.To = aor, aor
	} else {
		params.From = "<sip:probe@monitor.local>"
		params.To = fmt.Sprintf("<sip:probe@%s:%d>", config.Host, config.Port)
		params.Contact = flow.contactValue(requestURI)
		params.Extra = []sipHeader{{Name: "Accept", Value: "application/sdp"}}
	}

	// firstSent anchors first_response_ms (and auth_ms); each send
	// re-anchors response_ms on the request it measures.
	var firstSent, firstResponse time.Time
	send := func() (*sipMessage, error) {
		params.Via = flow.viaValue(newSIPBranch())
		sent := time.Now()
		if firstSent.IsZero() {
			firstSent = sent
		}
		seeResponse := func(at time.Time) {
			if firstResponse.IsZero() {
				firstResponse = at
				metrics.Timing.FirstResponseMs = sipDurationMs(at.Sub(firstSent))
			}
		}
		metrics.clearFinal()
		in, err := endpoint.roundTripNonInvite(ctx, buildSIPRequest(params), func(p sipInbound) { seeResponse(p.at) })
		if err != nil {
			return nil, err
		}
		seeResponse(in.at)
		metrics.Timing.ResponseMs = sipDurationMs(in.at.Sub(sent))
		metrics.recordFinal(in.msg)
		return in.msg, nil
	}

	resp, err := send()
	if err != nil {
		return errorResult(err)
	}

	// Answer a digest challenge when credentials are configured. The retry
	// is a new transaction (new branch) that keeps the Call-ID and From tag
	// and increments CSeq, per RFC 3261 §8.1.3.5.
	authAttempted := false
	if resp.StatusCode == 401 || resp.StatusCode == 407 {
		metrics.Auth = &sipAuthMetrics{Challenge: resp.StatusCode}
	}
	if (resp.StatusCode == 401 || resp.StatusCode == 407) && config.Username != "" && config.Password != "" {
		authHeaderName := "Authorization"
		challengeValue := resp.Get("WWW-Authenticate")
		if resp.StatusCode == 407 {
			authHeaderName = "Proxy-Authorization"
			challengeValue = resp.Get("Proxy-Authenticate")
		}
		if challengeValue == "" {
			return failure(resp, fmt.Sprintf("server returned %d without an authentication challenge", resp.StatusCode))
		}

		challenge, parseErr := parseSIPDigestChallenge(challengeValue)
		var credentials string
		if parseErr == nil {
			metrics.Auth.Algorithm = sipDigestAlgorithmName(challenge.Algorithm)
			credentials, parseErr = buildSIPDigestAuthorization(challenge, config.Username, config.Password, methodUpper, requestURI)
		}
		if parseErr != nil {
			return failure(resp, fmt.Sprintf("cannot answer authentication challenge: %v", parseErr))
		}

		params.CSeq = 2
		params.Extra = append(params.Extra, sipHeader{Name: authHeaderName, Value: credentials})
		metrics.Auth.Rounds = 1
		metrics.Auth.AuthMs = sipDurationMs(time.Since(firstSent))
		resp, err = send()
		if err != nil {
			return errorResult(err)
		}
		authAttempted = true
	}

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
		// HTTPStatus carries the SIP status.
		return failure(resp, errMsg)
	}

	return finish(CheckResult{
		Status:     "success",
		HTTPStatus: &resp.StatusCode,
	})
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
