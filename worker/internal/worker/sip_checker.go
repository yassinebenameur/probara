package worker

import (
	"context"
	"encoding/json"
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

	errorResult := func(err error) CheckResult {
		latencyMs := time.Since(startTime).Milliseconds()
		errMsg := fmt.Sprintf("%s: %v", classifySIPError(err), err)
		return CheckResult{
			Status:       "error",
			ErrorMessage: &errMsg,
			LatencyMs:    &latencyMs,
		}
	}

	flow, err := c.dialSIP(ctx, transport, targetHost, config.Port, &config)
	if err != nil {
		return errorResult(err)
	}
	endpoint := newSIPEndpoint(flow)
	defer endpoint.Close()

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
	if method == "register" {
		aor := fmt.Sprintf("<sip:%s@%s>", fromUser, domain)
		params.From, params.To = aor, aor
	} else {
		params.From = "<sip:probe@monitor.local>"
		params.To = fmt.Sprintf("<sip:probe@%s:%d>", config.Host, config.Port)
		params.Contact = flow.contactValue(requestURI)
		params.Extra = []sipHeader{{Name: "Accept", Value: "application/sdp"}}
	}
	send := func() (*sipMessage, error) {
		params.Via = flow.viaValue(newSIPBranch())
		in, err := endpoint.roundTripNonInvite(ctx, buildSIPRequest(params), nil)
		return in.msg, err
	}

	resp, err := send()
	if err != nil {
		return errorResult(err)
	}

	// Answer a digest challenge when credentials are configured. The retry
	// is a new transaction (new branch) that keeps the Call-ID and From tag
	// and increments CSeq, per RFC 3261 §8.1.3.5.
	authAttempted := false
	if (resp.StatusCode == 401 || resp.StatusCode == 407) && config.Username != "" && config.Password != "" {
		authHeaderName := "Authorization"
		challengeValue := resp.Get("WWW-Authenticate")
		if resp.StatusCode == 407 {
			authHeaderName = "Proxy-Authorization"
			challengeValue = resp.Get("Proxy-Authenticate")
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
		params.Extra = append(params.Extra, sipHeader{Name: authHeaderName, Value: credentials})
		resp, err = send()
		if err != nil {
			return errorResult(err)
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
