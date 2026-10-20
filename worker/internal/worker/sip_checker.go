package worker

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"regexp"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/yassinebenameur/probara/shared/models"
)

// SIPChecker implements Checker for SIP monitors. It supports OPTIONS
// availability pings, contact-less REGISTER checks (which exercise the
// registrar's digest authentication path without mutating any bindings) and
// INVITE test calls (sip_invite.go), over UDP, TCP, or TLS. The host is resolved to IP literals first
// (sip_resolve.go: A/AAAA, or RFC 3263 SRV when the monitor opts in), and
// each is dialed through dialGuard so SIP probes inherit the worker's
// private-IP (SSRF) policy; every message of an attempt rides that one flow
// (sip_flow.go). Requests run as RFC 3261 client transactions (sip_txn.go):
// retransmitted on UDP, matched to their responses by Via branch and CSeq
// method; requests the server sends us are answered by sip_uas.go.
type SIPChecker struct {
	blockPrivateIPs bool
	allowedCIDRs    []*net.IPNet
	// resolver replaces DNS in tests; nil uses the worker's resolver, or
	// one pinned to the monitor's nameserver.
	resolver sipResolver
	// testDial, set only by tests, interposes on the dial dialGuard makes
	// (dialThroughGuard); nil dials through dialGuard.
	testDial func(ctx context.Context, guard *dialGuard, network, addr string) (net.Conn, error)

	// Test calls (sip_invite.go). callsEnabled and callAllowed come from
	// ConfigureCalls; calls are off until it is called.
	callsEnabled bool
	callAllowed  []*regexp.Regexp
	lingers      *sipLingerPool
	// rtpPorts hands out the calls' RTP port pairs; nil binds random pairs
	// with no reuse quarantine.
	rtpPorts *sipRTPPorts
	// callTimers and callHook are for tests only.
	callTimers sipCallTimers
	callHook   func(stage string)
}

// NewSIPChecker creates a new SIP checker. It places no test call until
// ConfigureCalls enables them.
func NewSIPChecker(blockPrivateIPs bool, allowedCIDRs []*net.IPNet) *SIPChecker {
	return &SIPChecker{
		blockPrivateIPs: blockPrivateIPs,
		allowedCIDRs:    allowedCIDRs,
		lingers:         newSIPLingerPool(sipCallLingerSlots),
		rtpPorts:        newSIPRTPPorts(),
	}
}

// sipMinAttemptBudget is the floor of one address's share of the budget
// under rfc3263 failover: below it a UDP request gets too few
// retransmissions to tell a dead server from a lost datagram.
const sipMinAttemptBudget = 1500 * time.Millisecond

// sipMinConnectBudget is the floor of one address's connect budget under "a"
// (sipConnectBudget), net.Dialer's own minimum for the same split.
const sipMinConnectBudget = 2 * time.Second

// sipConnectBudget bounds the TCP connect to candidate i of a hostname under
// resolution "a". Before the checker resolved hostnames itself it handed them
// to net.Dialer, which gives each address but the last what is left divided
// by the addresses left (at least 2 s) to connect, so an address that drops
// SYNs, or an IPv6 path that is black-holed, failed over to the next one
// instead of eating the whole check; this keeps that. Only the connect is
// bounded: once connected, an address keeps the whole budget, as before. Zero
// (no bound) for the last address, for UDP, whose connect sends nothing, and
// under rfc3263, whose attempt slice already bounds everything.
func sipConnectBudget(ctx context.Context, mode string, candidates []sipCandidate, i int) time.Duration {
	if mode != models.SIPResolutionA || candidates[i].transport == "udp" || i >= len(candidates)-1 {
		return 0
	}
	deadline, ok := ctx.Deadline()
	if !ok {
		return 0
	}
	remaining := time.Until(deadline)
	budget := max(remaining/time.Duration(len(candidates)-i), sipMinConnectBudget)
	if budget >= remaining {
		return 0
	}
	return budget
}

// sipAttemptResult is how one address answered.
type sipAttemptResult struct {
	resp          *sipMessage // the final response, when one arrived
	authAttempted bool
	err           error
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
	// D12: a test call holds at most 10 s, whatever the payload says (the
	// API refuses more; the clamp covers a payload that predates the rule).
	// Applied before Validate so such a payload still runs, clamped.
	if config.Call != nil && config.Call.HoldMs > models.SIPCallMaxHoldMs {
		config.Call.HoldMs = models.SIPCallMaxHoldMs
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
	config.Host = strings.TrimSpace(config.Host)
	if config.IsCall() {
		return c.checkInvite(ctx, &config, timeoutSeconds)
	}
	mode := config.ResolutionMode()

	method := strings.ToLower(config.Method)
	if method == "" {
		method = "options"
	}
	methodUpper := strings.ToUpper(method)

	startTime := time.Now()
	// The check budget is resolution, every dial, every transaction and
	// every retransmission (Timer F).
	timeout := time.Duration(timeoutSeconds) * time.Second
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	domain := strings.TrimSpace(config.Domain)
	if domain == "" {
		domain = config.Host
	}

	// The Request-URI names what is being probed. Under "a" it is the host
	// and port dialed, as it always was; under rfc3263 it is the SIP URI
	// being resolved (RFC 3263 §4): the domain, with the port only when one
	// is configured, since an SRV-chosen port belongs to the server, not to
	// the URI.
	var requestURI, toURI string
	switch {
	case method == "register":
		requestURI = "sip:" + domain
	case mode == models.SIPResolutionA:
		port := config.Port
		if port == 0 {
			port = sipDefaultPort(strings.ToLower(config.Transport))
		}
		requestURI = fmt.Sprintf("sip:%s:%d", config.Host, port)
		toURI = fmt.Sprintf("sip:probe@%s:%d", config.Host, port)
	default:
		target := domain
		if strings.TrimSpace(config.Domain) == "" && config.Port != 0 {
			target = sipHostPort(config.Host, config.Port)
		}
		requestURI = "sip:" + sipURIHost(target)
		toURI = "sip:probe@" + sipURIHost(target)
	}

	// From here on every result carries the metrics_data envelope
	// (sip_metrics.go): a check that fails to resolve or dial still records
	// where it went and how far it got.
	transport := strings.ToLower(config.Transport)
	if transport == "" && mode == models.SIPResolutionA {
		transport = "udp"
	}
	metrics := &sipMetrics{
		Method:     methodUpper,
		Transport:  transport,
		RequestURI: requestURI,
		Target:     newSIPTargetMetrics(resolveSIPTargetHost(config.Host)),
	}
	finish := func(result CheckResult) CheckResult {
		latencyMs := time.Since(startTime).Milliseconds()
		result = applySIPCertificateRule(&config, metrics, result)
		result.LatencyMs = &latencyMs
		result.MetricsData = metrics.marshal(latencyMs)
		return result
	}
	errorResult := func(err error) CheckResult {
		errMsg := fmt.Sprintf("%s: %v", classifySIPError(err), err)
		return finish(CheckResult{Status: "error", ErrorMessage: &errMsg})
	}

	resolver, err := c.newSIPResolver(&config, timeout)
	if err != nil {
		return errorResult(err)
	}
	dnsStart := time.Now()
	candidates, resolution, err := resolveSIPTargets(ctx, &config, resolver)
	metrics.Resolution = resolution
	if resolution.lookedUp {
		metrics.Timing.DNSMs = sipDurationMs(time.Since(dnsStart))
	}
	if err != nil {
		return errorResult(err)
	}

	serverName := sipTLSServerName(&config)
	overridden := strings.TrimSpace(config.TLSServerName) != ""
	// The last address tried decides the check, and the envelope describes
	// that attempt; resolution.attempts keeps the earlier ones.
	var last sipAttemptResult
	for i := 0; i < len(candidates); i++ {
		candidate := candidates[i]
		metrics.startAttempt(candidate, i > 0)

		// Under rfc3263 an unanswered address is given up on so the next
		// can be tried (RFC 3263 §4.3), so each one gets a share of what
		// is left. Under "a" every address may use the whole budget: only
		// a transport failure moves on, as dialing a hostname always did,
		// and a connect that outlasts its share (sipConnectBudget).
		attemptCtx, attemptCancel := ctx, context.CancelFunc(func() {})
		if mode == models.SIPResolutionRFC3263 && i < len(candidates)-1 {
			if deadline, ok := ctx.Deadline(); ok {
				slice := max(time.Until(deadline)/time.Duration(len(candidates)-i), sipMinAttemptBudget)
				attemptCtx, attemptCancel = context.WithTimeout(ctx, slice)
			}
		}
		attemptStart := time.Now()
		connectBudget := sipConnectBudget(ctx, mode, candidates, i)
		last = c.runNonInviteAttempt(attemptCtx, candidate, &config, sipAttemptParams{
			method:     methodUpper,
			requestURI: requestURI,
			toURI:      toURI,
			domain:     domain,
		}, connectBudget, metrics)
		attemptCancel()
		if last.err != nil && candidate.transport == "tls" {
			last.err = withSIPSRVIdentityHint(last.err, candidate, serverName, overridden)
		}
		metrics.Resolution.addAttempt(sipAttemptMetrics{
			Address:   candidate.address(),
			Transport: candidate.transport,
			Outcome:   sipAttemptOutcome(last),
			Ms:        time.Since(attemptStart).Milliseconds(),
		})

		next := sipFailoverTarget(ctx, mode, last, candidates, i)
		if next < 0 {
			break
		}
		i = next - 1
	}

	if last.err != nil && last.resp == nil {
		return errorResult(last.err)
	}
	return finish(sipFinalResult(&config, last))
}

// sipAttemptParams is what every attempt of a check sends the same way.
type sipAttemptParams struct {
	method     string
	requestURI string
	toURI      string // OPTIONS only
	domain     string
}

// runNonInviteAttempt runs an OPTIONS or REGISTER, with the digest retry,
// against one address. It records the attempt in metrics as it goes.
// connectBudget bounds the TCP connect alone (dialSIP).
func (c *SIPChecker) runNonInviteAttempt(ctx context.Context, candidate sipCandidate, config *models.SIPMonitorConfig, p sipAttemptParams, connectBudget time.Duration, metrics *sipMetrics) sipAttemptResult {
	flow, err := c.dialSIP(ctx, candidate, config, connectBudget, false)
	if err != nil {
		metrics.recordDialError(err, candidate)
		return sipAttemptResult{err: err}
	}
	metrics.recordFlow(flow, sipTLSServerName(config))
	endpoint := newSIPEndpoint(flow)
	defer func() {
		endpoint.Close()
		metrics.recordEndpoint(endpoint)
	}()

	fromUser := strings.TrimSpace(config.Username)
	if fromUser == "" {
		fromUser = "probe"
	}

	// REGISTER uses the configured AOR and sends no Contact header, making
	// it a query-style registration check that never creates, refreshes, or
	// removes bindings. OPTIONS keeps the probe@monitor.local identity.
	req := sipRequestParams{
		Method:     p.method,
		RequestURI: p.requestURI,
		FromTag:    sipRandomToken(4),
		CallID:     uuid.New().String() + "@monitor.local",
		CSeq:       1,
	}
	metrics.CallID = req.CallID
	if p.method == "REGISTER" {
		aor := fmt.Sprintf("<sip:%s@%s>", fromUser, p.domain)
		req.From, req.To = aor, aor
	} else {
		req.From = "<sip:probe@monitor.local>"
		req.To = "<" + p.toURI + ">"
		req.Contact = flow.contactValue(p.requestURI)
		req.Extra = []sipHeader{{Name: "Accept", Value: "application/sdp"}}
	}

	// firstSent anchors first_response_ms (and auth_ms); each send
	// re-anchors response_ms on the request it measures.
	var firstSent, firstResponse time.Time
	send := func() (*sipMessage, error) {
		req.Via = flow.viaValue(newSIPBranch())
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
		in, err := endpoint.roundTripNonInvite(ctx, buildSIPRequest(req), func(p sipInbound) { seeResponse(p.at) })
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
		return sipAttemptResult{err: err}
	}

	// Answer a digest challenge when credentials are configured. The retry
	// is a new transaction (new branch) that keeps the Call-ID and From tag
	// and increments CSeq, per RFC 3261 §8.1.3.5.
	if resp.StatusCode == 401 || resp.StatusCode == 407 {
		metrics.Auth = &sipAuthMetrics{Challenge: resp.StatusCode}
	}
	if (resp.StatusCode == 401 || resp.StatusCode == 407) && config.Username != "" && config.Password != "" {
		if len(resp.Values(sipChallengeHeader(resp.StatusCode))) == 0 {
			return sipAttemptResult{resp: resp, err: fmt.Errorf("server returned %d without an authentication challenge", resp.StatusCode)}
		}
		answer, authErr := newSIPAuthState(config.Username, config.Password).answer(resp, p.method, p.requestURI)
		if authErr != nil {
			return sipAttemptResult{resp: resp, err: fmt.Errorf("cannot answer authentication challenge: %v", authErr)}
		}
		metrics.Auth.Algorithm = answer.algorithm

		req.CSeq = 2
		req.Extra = append(req.Extra, answer.headers...)
		metrics.Auth.Rounds = 1
		metrics.Auth.AuthMs = sipDurationMs(time.Since(firstSent))
		resp, err = send()
		if err != nil {
			return sipAttemptResult{err: err, authAttempted: true}
		}
		return sipAttemptResult{resp: resp, authAttempted: true}
	}
	return sipAttemptResult{resp: resp}
}

// sipChallengeHeader is the header carrying a 401's or 407's challenges.
func sipChallengeHeader(status int) string {
	if status == 407 {
		return "Proxy-Authenticate"
	}
	return "WWW-Authenticate"
}

// sipAttemptOutcome is an attempt's entry in resolution.attempts.
func sipAttemptOutcome(a sipAttemptResult) string {
	switch {
	case a.resp != nil && a.resp.StatusCode == 503:
		return "503"
	case a.resp != nil:
		return "ok"
	default:
		return classifySIPError(a.err)
	}
}

// sipFailoverTarget is the index of the candidate to try after attempt i, or
// -1 to stop (RFC 3263 §4.3, both modes unless noted):
//   - a transport failure (refused, unreachable, reset, or an address the
//     network policy refuses) moves to the next address;
//   - a TLS handshake failure moves to the next address of a different host
//     only: another address of the same host presents the same certificate;
//   - a connect that times out moves to the next address: under "a" it ran
//     out of its sipConnectBudget share (for OPTIONS and REGISTER, TLS
//     handshake and transaction silence still keep the whole budget there,
//     and never move on; a test call also bounds its TLS handshake by the
//     share and fails over when it stalls, see isSIPHandshakeStall);
//   - rfc3263 only: nothing within the attempt's share of the budget (no
//     response, or a dial or handshake that hangs), or a 503, moves to the
//     next address.
//
// Nothing moves on once the check's own budget is spent or it was cancelled.
func sipFailoverTarget(ctx context.Context, mode string, a sipAttemptResult, candidates []sipCandidate, i int) int {
	if i+1 >= len(candidates) || ctx.Err() != nil {
		return -1
	}
	if a.resp != nil {
		if a.resp.StatusCode == 503 && mode == models.SIPResolutionRFC3263 {
			return i + 1
		}
		return -1
	}
	switch classifySIPError(a.err) {
	case "refused", "connect", "ssrf_blocked":
		return i + 1
	case "tls":
		for j := i + 1; j < len(candidates); j++ {
			if !strings.EqualFold(candidates[j].host, candidates[i].host) {
				return j
			}
		}
		return -1
	case "timeout":
		// The attempt's share ran out (no response, or a dial or handshake
		// into a black hole); the check's own budget did not.
		if mode == models.SIPResolutionRFC3263 || isSIPConnectTimeout(a.err) {
			return i + 1
		}
		return -1
	default:
		return -1
	}
}

// isSIPConnectTimeout reports a timeout in the connect itself, not in the TLS
// handshake or the transaction that follows it. A test call's handshake that
// stalls past its share is isSIPHandshakeStall.
func isSIPConnectTimeout(err error) bool {
	var dialErr *sipDialError
	return errors.As(err, &dialErr) && !dialErr.handshake && classifySIPError(err) == "timeout"
}

// isSIPHandshakeStall reports a TLS handshake that did not complete in time:
// the TCP connection was up, nothing SIP was sent. A test call bounds its
// handshake by the address's connect share (dialSIP's boundHandshake), so
// this is the share running out on an address that accepts and then stalls,
// such as one dead load-balancer backend, and the call moves to the next
// address: unlike a certificate error, a stall belongs to that address, not
// to every address of the host.
func isSIPHandshakeStall(err error) bool {
	var dialErr *sipDialError
	return errors.As(err, &dialErr) && dialErr.handshake && classifySIPError(err) == "timeout"
}

// sipFinalResult judges the deciding attempt's final response.
func sipFinalResult(config *models.SIPMonitorConfig, a sipAttemptResult) CheckResult {
	resp := a.resp
	failure := func(errMsg string) CheckResult {
		// HTTPStatus carries the SIP status.
		return CheckResult{Status: "failure", HTTPStatus: &resp.StatusCode, ErrorMessage: &errMsg}
	}
	if a.err != nil {
		// A challenge the attempt could not answer.
		return failure(a.err.Error())
	}

	expectedStatus := 200
	if config.ExpectedStatus != nil {
		expectedStatus = *config.ExpectedStatus
	}
	if resp.StatusCode != expectedStatus {
		switch {
		case a.authAttempted && (resp.StatusCode == 401 || resp.StatusCode == 407):
			return failure(fmt.Sprintf("authentication failed: %d %s", resp.StatusCode, resp.Reason))
		case !a.authAttempted && (resp.StatusCode == 401 || resp.StatusCode == 407):
			return failure(fmt.Sprintf("server requires authentication (%d %s) but no credentials are configured", resp.StatusCode, resp.Reason))
		default:
			return failure(fmt.Sprintf("unexpected SIP status: got %d, expected %d", resp.StatusCode, expectedStatus))
		}
	}
	return CheckResult{Status: "success", HTTPStatus: &resp.StatusCode}
}

// sipURIHost brackets a bare IPv6 literal for use as a SIP URI host.
func sipURIHost(host string) string {
	if ip := net.ParseIP(host); ip != nil && ip.To4() == nil {
		return "[" + host + "]"
	}
	return host
}

// sipHostPort is host:port for a SIP URI, an IPv6 host bracketed.
func sipHostPort(host string, port int) string {
	return net.JoinHostPort(host, fmt.Sprint(port))
}

// applySIPCertificateRule enforces tls_min_days_valid's one check-side rule.
// A certificate inside the window does NOT fail the check: the alerter reads
// the recorded expiry (shared/tlscert) and opens a tls_expiry alert, so a
// healthy server with an aging certificate stays up. Only a check that got
// an answer without a certificate to report fails, since the threshold could
// never be evaluated. An "error" result (dial, handshake, timeout) already
// says why nothing was captured and is left alone.
func applySIPCertificateRule(config *models.SIPMonitorConfig, metrics *sipMetrics, result CheckResult) CheckResult {
	if config.TLSMinDaysValid == nil || result.Status == "error" {
		return result
	}
	if metrics.TLS != nil && metrics.TLS.DaysUntilExpiry != nil {
		return result
	}
	const assertion = "tls: missing certificate info"
	metrics.AssertionsFailed = append(metrics.AssertionsFailed, assertion)
	msg := assertion
	if result.ErrorMessage != nil && *result.ErrorMessage != "" {
		msg = *result.ErrorMessage + "; " + assertion
	}
	result.Status = "failure"
	result.ErrorMessage = &msg
	return result
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
