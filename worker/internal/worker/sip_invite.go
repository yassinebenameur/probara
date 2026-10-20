package worker

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math/rand/v2"
	"net"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/yassinebenameur/probara/shared/models"
)

// A SIP test call (method "invite") places a real call and measures it:
//
//	INVITE → 100 → 18x → 200 → ACK → hold → BYE        answer mode (default)
//	INVITE → 100 → 18x → CANCEL → 200/487 → ACK         ring mode
//
// Budgets (D12): the check timeout, capped at 20 s, bounds resolution, dial,
// the INVITE until it is answered (less the hold) and the hold; a fixed 4 s
// teardown reserve outside it always runs (CANCEL, or BYE for every
// confirmed dialog); after the result, a linger (sip_linger.go) keeps the
// flow up to 32 s to ACK and BYE a 2xx that arrives late. Worst case before
// the result is 24 s, inside the check-job AckWait, and the check-job
// consumer never runs a call twice (SideEffecting, worker.go).
//
// Signalling rides the one flow dialed through dialGuard: the Contact and
// Record-Route of the answer only fill in-dialog Request-URIs and Route
// headers. The SDP answer's media address is recorded and, only with
// call.media, sent RTP during the hold (sip_media.go): from the bound pair,
// and only to an address netguard.Policy.AllowsServerChosen admits.
const (
	sipCallTeardownBudget = 4 * time.Second
	// sipCallLingerFor is Timer B / the 2xx retransmission window: 64·T1.
	sipCallLingerFor   = 64 * sipT1
	sipCallLingerSlots = 32
	sipCallEventBuffer = 64
)

// sipCallTimers overrides the call's fixed budgets; tests shrink them. Zero
// fields use the defaults above.
type sipCallTimers struct {
	teardown time.Duration
	linger   time.Duration
}

func (c *SIPChecker) teardownBudget() time.Duration {
	if c.callTimers.teardown > 0 {
		return c.callTimers.teardown
	}
	return sipCallTeardownBudget
}

// SideEffecting marks test-call configs: each run places a real call, so the
// check-job consumer delivers them at most once (SideEffectingChecker). It
// reads only the non-secret method and call fields, so it answers the same
// for a payload whose password is still encrypted.
func (c *SIPChecker) SideEffecting(config json.RawMessage) bool {
	return models.IsSIPCallConfig("sip", config)
}

// ConfigureCalls sets whether this worker places test calls and, when
// allowed is non-empty, which called user parts it accepts (each pattern
// anchored to the whole user part). A checker never configured places no
// call: NewWorker configures it from SIP_CALL_ENABLED and
// SIP_CALL_ALLOWED_DESTINATIONS.
func (c *SIPChecker) ConfigureCalls(enabled bool, allowed []*regexp.Regexp) {
	c.callsEnabled = enabled
	c.callAllowed = allowed
}

func (c *SIPChecker) destinationAllowed(user string) bool {
	if len(c.callAllowed) == 0 {
		return true
	}
	for _, re := range c.callAllowed {
		if re.MatchString(user) {
			return true
		}
	}
	return false
}

// sipCallTarget is who a test call calls and as whom.
type sipCallTarget struct {
	requestURI string
	toURI      string // the Request-URI without its parameters
	user       string // the called user part, decoded: what the allow-list matches
	from       string // From name-addr, without tag
	sips       bool
}

// newSIPCallTarget builds the Request-URI (target_uri as configured, else
// sip:<number>@<domain or host>, with ;user=phone for a telephone number;
// sips: over TLS) and the caller ID.
func newSIPCallTarget(config *models.SIPMonitorConfig) (sipCallTarget, error) {
	call := config.Call
	host := strings.TrimSpace(config.Domain)
	if host == "" {
		host = strings.TrimSpace(config.Host)
	}
	var t sipCallTarget
	// Validate refuses such a domain for a test call; a payload that
	// predates the rule must not build a Request-URI or From around it
	// either (maddr, ?headers or a second name-addr would ride along).
	if net.ParseIP(host) == nil && !models.IsValidHostname(host) {
		return t, fmt.Errorf("domain: must be a valid hostname or IP address")
	}
	if call.TargetURI != "" {
		u, err := models.ParseSIPCallURI(call.TargetURI)
		if err != nil {
			return t, fmt.Errorf("call.target_uri: %w", err)
		}
		t.requestURI = call.TargetURI
		t.sips = u.Scheme == "sips"
		hostPort := sipURIHost(u.Host)
		if u.Port != 0 {
			hostPort = sipHostPort(u.Host, u.Port)
		}
		t.toURI = u.Scheme + ":" + u.User + "@" + hostPort
		t.user = u.DecodedUser
	} else {
		scheme := "sip"
		if strings.EqualFold(config.Transport, "tls") {
			scheme = "sips"
		}
		t.sips = scheme == "sips"
		// '#' is not allowed unescaped in a SIP user part (RFC 3261 §25.1).
		user := strings.ReplaceAll(call.Number, "#", "%23")
		t.toURI = scheme + ":" + user + "@" + sipURIHost(host)
		t.requestURI = t.toURI
		if call.IsTelephoneNumber() {
			t.requestURI += ";user=phone"
		}
		t.user = call.Number
	}

	fromUser := call.FromUser
	if fromUser == "" {
		fromUser = strings.TrimSpace(config.Username)
		if !models.IsValidSIPCallUser(fromUser) {
			fromUser = "probara"
		}
	}
	fromDomain := call.FromDomain
	if fromDomain == "" {
		fromDomain = host
	}
	t.from = "<sip:" + fromUser + "@" + sipURIHost(fromDomain) + ">"
	if call.FromDisplay != "" {
		t.from = `"` + call.FromDisplay + `" ` + t.from
	}
	return t, nil
}

// sipCallBudgets is the call's timeout and hold after the worker's own
// clamps (D12), whatever the payload says (the test-check path allows 120 s,
// and a payload may predate a rule): the timeout at most 20 s, the hold at
// most 10 s and never more than leaves 2 s to ring and answer.
func sipCallBudgets(timeoutSeconds, holdMs int) (timeout, hold time.Duration) {
	timeout = time.Duration(timeoutSeconds) * time.Second
	if maxTimeout := time.Duration(models.SIPCallMaxTimeoutSeconds) * time.Second; timeout <= 0 || timeout > maxTimeout {
		timeout = maxTimeout
	}
	hold = time.Duration(min(max(holdMs, 0), models.SIPCallMaxHoldMs)) * time.Millisecond
	if limit := timeout - models.SIPCallMinAnswerBudgetMs*time.Millisecond; hold > limit {
		hold = max(limit, 0)
	}
	return timeout, hold
}

// checkInvite runs a test call. Check has validated the config.
func (c *SIPChecker) checkInvite(ctx context.Context, config *models.SIPMonitorConfig, timeoutSeconds int) CheckResult {
	startTime := time.Now()
	errorBeforeDial := func(err error) CheckResult {
		msg := fmt.Sprintf("%s: %v", classifySIPError(err), err)
		latency := time.Since(startTime).Milliseconds()
		return CheckResult{Status: "error", ErrorMessage: &msg, LatencyMs: &latency}
	}

	target, err := newSIPCallTarget(config)
	if err != nil {
		msg := fmt.Sprintf("invalid SIP config: %v", err)
		return CheckResult{Status: "error", ErrorMessage: &msg}
	}
	// Worker policy is decided before anything is resolved or sent.
	if !c.callsEnabled {
		return errorBeforeDial(errSIPCallsDisabled)
	}
	if !c.destinationAllowed(target.user) {
		return errorBeforeDial(errSIPDestinationNotAllowed)
	}

	timeout, hold := sipCallBudgets(timeoutSeconds, config.Call.HoldMs)
	mode := config.Call.EffectiveMode()

	callCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	transport := strings.ToLower(config.Transport)
	if transport == "" && config.ResolutionMode() == models.SIPResolutionA {
		transport = "udp"
	}
	metrics := &sipMetrics{
		Method:     "INVITE",
		Transport:  transport,
		RequestURI: sipMetricsString(target.requestURI),
		Target:     newSIPTargetMetrics(resolveSIPTargetHost(config.Host)),
		Call:       &sipCallMetrics{Mode: mode, HoldMs: hold.Milliseconds(), Teardown: "none"},
	}
	finish := func(result CheckResult, decisive time.Time) CheckResult {
		total := time.Since(startTime).Milliseconds()
		result = applySIPCertificateRule(config, metrics, result)
		latency := total
		if !decisive.IsZero() {
			latency = decisive.Sub(startTime).Milliseconds()
		}
		result.LatencyMs = &latency
		result.MetricsData = metrics.marshal(total)
		return result
	}
	errorResult := func(err error) CheckResult {
		msg := fmt.Sprintf("%s: %v", classifySIPError(err), err)
		// Before the INVITE is sent (resolution, dial) a cancelled ctx is the
		// worker shutting down, whatever error it surfaced as.
		return finish(CheckResult{Status: "error", ErrorMessage: &msg, Interrupted: ctx.Err() != nil}, time.Time{})
	}

	resolver, err := c.newSIPResolver(config, timeout)
	if err != nil {
		return errorResult(err)
	}
	dnsStart := time.Now()
	candidates, resolution, err := resolveSIPTargets(callCtx, config, resolver)
	metrics.Resolution = resolution
	if resolution.lookedUp {
		metrics.Timing.DNSMs = sipDurationMs(time.Since(dnsStart))
	}
	if err != nil {
		return errorResult(err)
	}

	serverName := sipTLSServerName(config)
	overridden := strings.TrimSpace(config.TLSServerName) != ""
	var lastErr error
	for i := 0; i < len(candidates); i++ {
		candidate := candidates[i]
		metrics.startAttempt(candidate, i > 0)
		metrics.Call = &sipCallMetrics{Mode: mode, HoldMs: hold.Milliseconds(), Teardown: "none"}
		attemptStart := time.Now()

		// A call fails over only when the connection cannot be established
		// (dial, refusal, policy block, TLS handshake) or on a 503 before
		// any provisional (below). Once connected and the INVITE is sent, an
		// address is never given up on, neither for silence nor for an error
		// on the connection (such as a UDP port unreachable): the INVITE may
		// already be ringing someone, and a second address would place a
		// second call. Each address but the last gets a share of the budget
		// to connect and, over TLS, complete the handshake, whatever the
		// resolution mode: a handshake that stalls past it has sent nothing,
		// so the next address is tried (isSIPHandshakeStall).
		connectBudget := sipConnectBudget(callCtx, models.SIPResolutionA, candidates, i)
		flow, err := c.dialSIP(callCtx, candidate, config, connectBudget, true)
		if err != nil {
			metrics.recordDialError(err, candidate)
			if candidate.transport == "tls" {
				err = withSIPSRVIdentityHint(err, candidate, serverName, overridden)
			}
			lastErr = err
			metrics.Resolution.addAttempt(sipAttemptMetrics{
				Address: candidate.address(), Transport: candidate.transport,
				Outcome: classifySIPError(err), Ms: time.Since(attemptStart).Milliseconds(),
			})
			next := sipFailoverTarget(callCtx, models.SIPResolutionA, sipAttemptResult{err: err}, candidates, i)
			if next < 0 && isSIPHandshakeStall(err) && callCtx.Err() == nil && i+1 < len(candidates) {
				next = i + 1
			}
			if next < 0 {
				break
			}
			i = next - 1
			continue
		}
		metrics.recordFlow(flow, serverName)

		out := c.runInviteCall(ctx, callCtx, flow, config, target, startTime, timeout, hold, metrics, i+1 < len(candidates))
		outcome := "ok"
		switch {
		case out.failover:
			outcome = "503"
		case out.result.Status == "error" && out.err != nil:
			outcome = classifySIPError(out.err)
		}
		metrics.Resolution.addAttempt(sipAttemptMetrics{
			Address: candidate.address(), Transport: candidate.transport,
			Outcome: outcome, Ms: time.Since(attemptStart).Milliseconds(),
		})
		if out.failover {
			continue
		}
		return finish(out.result, out.decisive)
	}
	if lastErr == nil {
		lastErr = errors.New("no address left to try")
	}
	return errorResult(lastErr)
}

// sipCallPhase is where the call stands, as the call's own goroutine sees it.
type sipCallPhase int

const (
	sipCallCalling     sipCallPhase = iota // INVITE pending, no provisional yet
	sipCallProceeding                      // INVITE pending, a provisional came
	sipCallHolding                         // answered (answer mode): holding
	sipCallCancelling                      // INVITE pending and given up on: CANCEL it once a provisional comes
	sipCallTerminating                     // answered: BYE every dialog
	sipCallDone                            // the INVITE got a non-2xx final: nothing to tear down
)

type sipCallEventKind int

const (
	sipCallEvProvisional sipCallEventKind = iota
	sipCallEvFinal                        // a non-2xx final (ACKed by the dispatcher)
	sipCallEvAnswered                     // a 2xx with a new To tag (ACKed by the dispatcher)
	sipCallEvRemoteBye                    // the callee hung up (answered 200 by the UAS)
)

// sipCallEvent is what the dispatcher tells the call's goroutine.
type sipCallEvent struct {
	kind   sipCallEventKind
	txn    *sipInviteTxn
	in     sipInbound
	dialog *sipDialog
}

// sipInviteTxn is one INVITE the call sent: the first, or an authenticated
// retry (a new transaction: new branch, CSeq + 1).
type sipInviteTxn struct {
	params sipRequestParams
	raw    []byte
	sentAt time.Time
	txn    *sipClientTxn

	// Under sipCall.mu: the non-2xx ACK, re-sent for each retransmitted
	// final.
	ackRaw []byte
}

// sipCall is one test call on one flow.
type sipCall struct {
	c            *SIPChecker
	config       *models.SIPMonitorConfig
	ring         bool
	hold         time.Duration
	answerBudget time.Duration
	flow         *sipFlow
	ep           *sipEndpoint
	auth         *sipAuthState // nil without credentials
	rtp          *sipRTPPair
	media        *sipMediaSession // nil without call.media
	metrics      *sipMetrics
	cm           *sipCallMetrics
	checkStart   time.Time
	callDeadline time.Time
	canFailover  bool

	base sipRequestParams // the INVITE minus Via, CSeq and credentials
	cseq int

	events chan sipCallEvent

	mu      sync.Mutex
	dialogs []*sipDialog
	byTag   map[string]*sipDialog

	// The call goroutine's state (then teardown's, then the linger's: never
	// two at once).
	current        *sipInviteTxn
	tFirst         time.Time
	phase          sipCallPhase
	proceeding     bool // a provisional to the current INVITE
	anyProvisional bool
	ringingAt      time.Time
	authRounds     int
	authNewRealm   string // a realm first challenged after the last round allowed
	primary        *sipDialog
	inviteFinal    *sipMessage // the current INVITE's non-2xx final
	decided        bool
	result         CheckResult
	resultErr      error
	decisiveAt     time.Time
	failover       bool
	restartTimerA  bool
	cancelSent     bool
	// earlyAnswers is the SDP of the last 18x on each early dialog, by To
	// tag (nil for an SDP without an audio stream), for at most
	// sipCallMaxEarlyAnswers tags: see answerSDP.
	earlyAnswers map[string]*sipSDPAnswer
}

// sipCallMaxEarlyAnswers bounds the early dialogs whose 18x SDP a call keeps.
const sipCallMaxEarlyAnswers = 4

// Where the SDP answer a call's media is planned from came from (answerSDP).
const (
	sipAnswerFrom2xx = "2xx"
	sipAnswerFrom18x = "18x"
)

// sipCallOutcome is how a call on one flow ended.
type sipCallOutcome struct {
	result   CheckResult
	err      error // the transport error behind an "error" result
	decisive time.Time
	failover bool // a 503 before any provisional with addresses left
}

// runInviteCall places the call on flow, tears it down, and hands the flow
// to a linger or closes it.
func (c *SIPChecker) runInviteCall(parent, callCtx context.Context, flow *sipFlow, config *models.SIPMonitorConfig, target sipCallTarget, checkStart time.Time, timeout, hold time.Duration, metrics *sipMetrics, canFailover bool) sipCallOutcome {
	s := &sipCall{
		c:            c,
		config:       config,
		ring:         config.Call.EffectiveMode() == models.SIPCallModeRing,
		hold:         hold,
		answerBudget: timeout - hold,
		flow:         flow,
		metrics:      metrics,
		cm:           metrics.Call,
		checkStart:   checkStart,
		callDeadline: checkStart.Add(timeout),
		canFailover:  canFailover,
		events:       make(chan sipCallEvent, sipCallEventBuffer),
		byTag:        make(map[string]*sipDialog),
		cseq:         1,
	}
	if strings.TrimSpace(config.Username) != "" && config.Password != "" {
		s.auth = newSIPAuthState(config.Username, config.Password)
	}
	s.ep = newSIPEndpointWithDialogs(flow, s.matchDialog)

	localIP := flow.localIP
	if localIP == nil {
		localIP = net.IPv4zero
	}
	rtp, err := c.rtpPorts.bind(localIP)
	if err != nil {
		s.ep.Close()
		msg := fmt.Sprintf("media: %v", err)
		return sipCallOutcome{result: CheckResult{Status: "error", ErrorMessage: &msg}}
	}
	s.rtp = rtp
	s.media = s.newMediaSession(rtp, localIP)
	// Teardown stops the sender first; this covers a call that never gets
	// there, so a sender cannot outlive it.
	defer func() { s.media.stop(time.Now()) }()
	sdpAddr := localIP.String()
	if config.Call.SDPAddress != "" {
		sdpAddr = config.Call.SDPAddress
	}

	callID := sipRandomToken(16) + "@probara"
	metrics.CallID = callID
	s.base = sipRequestParams{
		Method:      "INVITE",
		RequestURI:  target.requestURI,
		From:        target.from,
		FromTag:     sipRandomToken(4),
		To:          "<" + target.toURI + ">",
		CallID:      callID,
		Contact:     flow.contactValue(target.requestURI),
		Body:        buildSDPOffer(sdpAddr, rtp.port, rand.Uint64N(1<<53)),
		ContentType: "application/sdp",
		Extra:       []sipHeader{{Name: "Allow", Value: sipAllow}},
	}
	if config.Call.ProbeHeader {
		s.base.Extra = append(s.base.Extra, sipHeader{Name: "X-Probara-Probe", Value: "1"})
	}

	s.drive(parent, callCtx)

	teardownCtx, cancelTeardown := context.WithTimeout(context.WithoutCancel(parent), c.teardownBudget())
	s.teardown(teardownCtx)
	cancelTeardown()
	rtp.Close()
	s.cm.RTPRxPackets = rtp.packets.Load()
	// A verdict reached before a shutdown stands, but its media is
	// interrupted: a deploy never records a quality failure.
	s.cm.Media = s.media.finish(parent.Err() != nil)
	if !s.failover {
		// A 503 failover attempt's media block is overwritten by the next
		// attempt's, so only the attempt that decides the check counts.
		s.media.count()
	}
	metrics.recordEndpoint(s.ep)
	s.applyThresholds()
	s.applyMediaRules()
	if s.cm.Teardown == "bye_unconfirmed" {
		if count := c.mediaCounters.teardownUnconfirmed; count != nil {
			evidence := "none"
			if s.media.afterByeEvidence() {
				evidence = "audio_after_bye"
			}
			count(evidence)
		}
	}

	out := sipCallOutcome{result: s.result, err: s.resultErr, decisive: s.decisiveAt, failover: s.failover}
	if !s.decided && !s.failover {
		msg := "unknown: the call ended without a result"
		out.result = CheckResult{Status: "error", ErrorMessage: &msg}
	}

	// A 2xx may still come (a late or forked answer), unless the INVITE got
	// a non-2xx final and nothing answered.
	select {
	case <-s.ep.done:
		s.ep.Close()
		return out
	default:
	}
	if s.primary == nil && s.inviteFinal != nil {
		s.ep.Close()
		return out
	}
	c.lingers.hand(s)
	return out
}

// sendInvite sends a new INVITE transaction: the first, or an authenticated
// retry with credentials.
func (s *sipCall) sendInvite(credentials []sipHeader) error {
	branch := newSIPBranch()
	p := s.base
	p.Via = s.flow.viaValue(branch)
	p.CSeq = s.cseq
	p.Extra = append(append([]sipHeader(nil), s.base.Extra...), credentials...)
	inv := &sipInviteTxn{params: p}
	inv.raw = buildSIPRequest(p).Bytes()
	txn, err := s.ep.registerInvite(branch, func(in sipInbound) { s.onResponse(inv, in) })
	if err != nil {
		return err
	}
	inv.txn = txn
	s.current = inv
	s.inviteFinal = nil
	s.proceeding = false
	inv.sentAt = time.Now()
	if s.tFirst.IsZero() {
		s.tFirst = inv.sentAt
	}
	if err := s.flow.send(inv.raw); err != nil {
		return fmt.Errorf("failed to send request: %w", err)
	}
	return nil
}

// emit hands an event to whoever owns the call (its goroutine, teardown, or
// the linger). It never blocks the dispatcher: if the buffer is full the
// event is dropped, which loses nothing that matters — dialogs stay in the
// call's table, where teardown and the linger find them.
func (s *sipCall) emit(ev sipCallEvent) {
	select {
	case s.events <- ev:
	default:
	}
}

// onResponse runs on the dispatcher goroutine for every response to one of
// the call's INVITEs. It sends the ACKs itself, so they go out whatever the
// call's goroutine is doing: the in-transaction ACK of a non-2xx final (and
// again for each retransmission of it), and the ACK of each 2xx (a new
// dialog per To tag; a retransmitted 2xx gets the stored ACK again, never a
// second BYE).
func (s *sipCall) onResponse(inv *sipInviteTxn, in sipInbound) {
	code := in.msg.StatusCode
	switch {
	case code < 200:
		s.emit(sipCallEvent{kind: sipCallEvProvisional, txn: inv, in: in})
	case code < 300:
		tag := sipHeaderTag(in.msg.Get("To"))
		s.mu.Lock()
		if d := s.byTag[tag]; d != nil {
			ack := d.ackRaw
			s.mu.Unlock()
			_ = s.flow.send(ack)
			return
		}
		d := newSIPDialog(inv.params, in.msg)
		d.ackRaw = d.buildAck2xx(s.flow).Bytes()
		s.byTag[tag] = d
		s.dialogs = append(s.dialogs, d)
		s.mu.Unlock()
		_ = s.flow.send(d.ackRaw)
		s.emit(sipCallEvent{kind: sipCallEvAnswered, txn: inv, in: in, dialog: d})
	default:
		s.mu.Lock()
		ack := inv.ackRaw
		first := ack == nil
		if first {
			ack = buildSIPNon2xxAck(inv.params, in.msg).Bytes()
			inv.ackRaw = ack
		}
		s.mu.Unlock()
		_ = s.flow.send(ack)
		if first {
			s.emit(sipCallEvent{kind: sipCallEvFinal, txn: inv, in: in})
		}
	}
}

// matchDialog is the UAS hook for in-dialog requests: a BYE from the callee
// in one of the call's dialogs is answered 200 and reported.
func (s *sipCall) matchDialog(req *sipMessage) bool {
	s.mu.Lock()
	var match *sipDialog
	for _, d := range s.dialogs {
		if d.matches(req) {
			match = d
			break
		}
	}
	if match != nil && strings.EqualFold(req.Method, "BYE") {
		match.remoteBye = true
	}
	s.mu.Unlock()
	if match == nil {
		return false
	}
	s.emit(sipCallEvent{kind: sipCallEvRemoteBye, dialog: match})
	return true
}

// decide records the check's verdict; the first decision stands.
func (s *sipCall) decide(status string, code int, at time.Time, msg string) {
	if s.decided {
		return
	}
	s.decided = true
	s.result = CheckResult{Status: status}
	if code > 0 {
		c := code
		s.result.HTTPStatus = &c
	}
	if msg != "" {
		m := msg
		s.result.ErrorMessage = &m
	}
	s.decisiveAt = at
}

// decideError records an error verdict for err, its class first.
func (s *sipCall) decideError(err error) {
	if s.decided {
		return
	}
	s.resultErr = err
	s.decide("error", 0, time.Time{}, fmt.Sprintf("%s: %v", classifySIPError(err), err))
}

// hook lets tests inject a failure at a stage of the call.
func (s *sipCall) hook(stage string) {
	if s.c.callHook != nil {
		s.c.callHook(stage)
	}
}

// drive runs the call up to the point where it must be torn down. A panic is
// recovered into an error result, with the call left in a state teardown can
// finish, so even then nothing stays up.
func (s *sipCall) drive(parent, callCtx context.Context) {
	defer func() {
		r := recover()
		if r == nil {
			return
		}
		s.decided = false
		s.decide("error", 0, time.Time{}, fmt.Sprintf("internal: SIP call aborted: %v", r))
		switch {
		case s.primary != nil:
			s.phase = sipCallTerminating
		case s.phase == sipCallCalling || s.phase == sipCallProceeding:
			s.phase = sipCallCancelling
		}
	}()

	if err := s.sendInvite(nil); err != nil {
		s.decideError(err)
		s.phase = sipCallDone
		return
	}

	var (
		timerA   *time.Timer
		timerAC  <-chan time.Time
		interval time.Duration
	)
	startTimerA := func() {
		if s.flow.reliable() {
			return
		}
		interval = s.ep.t1
		if timerA == nil {
			timerA = time.NewTimer(interval)
		} else {
			timerA.Reset(interval)
		}
		timerAC = timerA.C
	}
	stopTimerA := func() {
		if timerA != nil {
			timerA.Stop()
		}
		timerAC = nil
	}
	defer stopTimerA()
	startTimerA()

	answerTimer := time.NewTimer(time.Until(s.checkStart.Add(s.answerBudget)))
	defer answerTimer.Stop()
	answerC := answerTimer.C
	var holdC <-chan time.Time

	for {
		switch s.phase {
		case sipCallCancelling, sipCallTerminating, sipCallDone:
			return
		}
		if s.failover {
			return
		}
		select {
		case ev := <-s.events:
			s.handle(ev)
			if s.restartTimerA {
				s.restartTimerA = false
				startTimerA()
			} else if s.phase != sipCallCalling {
				// Timer A stops at the first provisional, or a final.
				stopTimerA()
			}
			if s.phase == sipCallHolding && holdC == nil {
				answerC = nil
				// Media starts here, the one place the call is known to
				// hold: never for a 2xx that crossed a CANCEL, a ring-mode
				// answer or a status mismatch.
				s.media.start(time.Now())
				s.hook("holding")
				holdTimer := time.NewTimer(min(s.hold, time.Until(s.callDeadline)))
				defer holdTimer.Stop()
				holdC = holdTimer.C
			}

		case <-timerAC:
			if err := s.flow.send(s.current.raw); err == nil {
				s.ep.retransmissions.Add(1)
			}
			interval *= 2
			timerA.Reset(interval)

		case <-answerC:
			answerC = nil
			s.giveUp()

		case <-holdC:
			s.phase = sipCallTerminating

		case <-s.current.txn.looped:
			s.decideError(&sipLoopError{method: "INVITE"})
			s.phase = sipCallDone

		case <-s.ep.done:
			s.drainEvents()
			s.decideError(sipFlowReadError(s.flow)) // no-op if a drained final or 2xx decided
			if s.primary != nil {
				s.phase = sipCallTerminating
			} else {
				s.phase = sipCallDone
			}

		case <-callCtx.Done():
			if parent.Err() != nil && !s.decided {
				// The worker is shutting down mid-call: teardown still runs,
				// but the result says nothing about the target, so it is
				// marked and processJob drops it rather than publish a
				// failure. A verdict reached before (a call answered and
				// holding) stands and is published.
				s.decide("error", 0, time.Time{}, "cancelled: check cancelled")
				s.result.Interrupted = true
			}
			if s.phase == sipCallHolding {
				s.phase = sipCallTerminating
			} else {
				s.giveUp()
			}
		}
	}
}

// giveUp ends the wait for an answer (the answer budget or the check budget
// ran out). With a provisional, the INVITE is CANCELled; without one nothing
// is sent (RFC 3261 §9.1: a CANCEL before any provisional may cross the
// INVITE) and teardown keeps listening for a late provisional or answer.
func (s *sipCall) giveUp() {
	switch s.phase {
	case sipCallProceeding:
		if !s.decided {
			budget := sipFormatSeconds(s.answerBudget)
			switch {
			case s.ring:
				s.decide("failure", 0, time.Time{}, fmt.Sprintf("pdd: no ringing within %dms", s.answerBudget.Milliseconds()))
			case !s.ringingAt.IsZero():
				s.decide("failure", 0, time.Time{}, fmt.Sprintf("answer: no answer within %s (ringing since %s)",
					budget, sipFormatSeconds(time.Since(s.ringingAt))))
			default:
				s.decide("failure", 0, time.Time{}, fmt.Sprintf("answer: no answer within %s (no ringing)", budget))
			}
		}
		s.phase = sipCallCancelling
	case sipCallCalling:
		s.decideError(&sipNoResponseError{method: "INVITE", stray: s.ep.stray.Load(), malformed: s.flow.malformed.Load()})
		s.phase = sipCallCancelling
	}
}

func sipFormatSeconds(d time.Duration) string {
	return strconv.FormatFloat(d.Seconds(), 'f', 1, 64) + "s"
}

// drainEvents applies every event already queued. The dispatcher queues a
// response's event before it closes ep.done, so a final or 2xx sent just
// before the peer hung up is waiting here (the same reason
// roundTripNonInvite drains on e.done); without the drain, a select that
// sees both ready picks one at random and the result depends on the race.
func (s *sipCall) drainEvents() {
	for {
		select {
		case ev := <-s.events:
			s.handle(ev)
		default:
			return
		}
	}
}

// handle applies one event to the call's state.
func (s *sipCall) handle(ev sipCallEvent) {
	s.hook("event")
	switch ev.kind {
	case sipCallEvProvisional:
		if ev.txn == s.current {
			s.onProvisional(ev.txn, ev.in)
		}
	case sipCallEvFinal:
		if ev.txn == s.current {
			s.onFinal(ev.txn, ev.in)
		}
	case sipCallEvAnswered:
		s.onAnswered(ev.txn, ev.dialog, ev.in)
	case sipCallEvRemoteBye:
		// The callee hung up the answered call first. Whatever the phase
		// (holding, or already terminating: ring mode answered, a final
		// mismatch, the hold just over), that ends the primary dialog and
		// labels the teardown, unless a label is already set (our own BYE's
		// outcome, or answered_after_cancel). byeAll then skips the dialog.
		if ev.dialog == s.primary {
			if s.cm.Teardown == "none" || s.cm.Teardown == "" {
				s.cm.Teardown = "remote_bye"
				s.cm.TeardownStatus = 0
			}
			if s.phase == sipCallHolding {
				s.phase = sipCallTerminating
			}
		}
	}
}

func (s *sipCall) seeFirstResponse(at time.Time) {
	if s.metrics.Timing.FirstResponseMs == nil {
		s.metrics.Timing.FirstResponseMs = sipDurationMs(at.Sub(s.tFirst))
	}
}

func (s *sipCall) onProvisional(inv *sipInviteTxn, in sipInbound) {
	msg, code := in.msg, in.msg.StatusCode
	since := in.at.Sub(inv.sentAt)
	s.seeFirstResponse(in.at)
	if len(s.cm.Provisional) < sipMaxCallList {
		s.cm.Provisional = append(s.cm.Provisional, sipProvisionalStep{Code: code, AtMs: since.Milliseconds()})
	}
	if code == 100 && s.cm.TryingMs == nil {
		s.cm.TryingMs = sipDurationMs(since)
	}
	if sipRequires(msg, "100rel") {
		// The probe does not do PRACK; the far end may still go on.
		s.cm.Require100rel = true
	}
	if code >= 180 && code <= 189 && sipBodyIsSDP(msg) {
		s.cm.EarlyMedia = true
		s.keepEarlyAnswer(msg)
	}
	s.anyProvisional = true
	s.proceeding = true
	if s.phase == sipCallCalling {
		s.phase = sipCallProceeding
	}
	if code < 180 || code > 189 || !s.ringingAt.IsZero() {
		return
	}
	s.ringingAt = in.at
	s.cm.PDDMs = sipDurationMs(since)
	s.cm.PDDSource = strconv.Itoa(code)
	s.cm.SetupPDDMs = sipDurationMs(in.at.Sub(s.tFirst))
	if hops := len(msg.Values("Record-Route")); hops > s.cm.RouteHops {
		s.cm.RouteHops = hops
	}
	if s.ring && s.phase == sipCallProceeding {
		// Ring mode is decided by ringing: success, then CANCEL.
		s.decide("success", code, in.at, "")
		s.phase = sipCallCancelling
	}
}

func (s *sipCall) onFinal(inv *sipInviteTxn, in sipInbound) {
	msg, code := in.msg, in.msg.StatusCode
	s.seeFirstResponse(in.at)
	s.inviteFinal = msg

	if (code == 401 || code == 407) && s.metrics.Auth == nil {
		s.metrics.Auth = &sipAuthMetrics{Challenge: code}
	}
	if (code == 401 || code == 407) && s.auth != nil && !s.decided &&
		(s.phase == sipCallCalling || s.phase == sipCallProceeding) {
		// Round 1 always; round 2 only when the server says the nonce went
		// stale. The ACK of the challenge went out with it (onResponse).
		// A realm never answered before, challenged after round 1, is a
		// further hop (proxy then UAS): one round is all this probe does, so
		// that fails the call, worded so it does not blame the password.
		newRealm, isNew := s.auth.unansweredRealm(msg)
		answer, err := s.auth.answer(msg, "INVITE", s.base.RequestURI)
		if err == nil && (s.authRounds == 0 || (s.authRounds == 1 && answer.stale)) {
			s.authRounds++
			s.metrics.Auth.Algorithm = answer.algorithm
			s.metrics.Auth.Rounds = s.authRounds
			s.cseq++
			// Every realm answered so far rides the retry (and so the 2xx
			// ACK, D22), not only the one just challenged.
			credentials, err := s.auth.credentials(answer, "INVITE", s.base.RequestURI)
			if err == nil {
				err = s.sendInvite(credentials)
			}
			if err != nil {
				s.decideError(err)
				s.phase = sipCallDone
				return
			}
			s.metrics.Auth.AuthMs = sipDurationMs(s.current.sentAt.Sub(s.tFirst))
			s.phase = sipCallCalling
			s.restartTimerA = true
			return
		}
		if err != nil {
			s.metrics.recordFinal(msg)
			s.phase = sipCallDone
			s.decide("failure", code, in.at, fmt.Sprintf("auth: cannot answer authentication challenge: %v", err))
			return
		}
		if isNew && s.authRounds > 0 {
			s.authNewRealm = newRealm
		}
	}

	s.metrics.recordFinal(msg)
	s.metrics.Timing.ResponseMs = sipDurationMs(in.at.Sub(inv.sentAt))
	if s.phase == sipCallCancelling || s.decided {
		// The INVITE ended after the call was given up on (or, in ring
		// mode, decided): a 487 answering the CANCEL, or a final that
		// crossed it.
		if s.cancelSent {
			s.cm.Teardown = "cancelled"
			s.cm.TeardownStatus = code
		}
		s.phase = sipCallDone
		return
	}
	s.phase = sipCallDone

	status := fmt.Sprintf("%d %s", code, msg.Reason)
	if r := s.metrics.ReasonHeader; r != nil && r.Cause != nil && strings.EqualFold(r.Protocol, "Q.850") {
		status += fmt.Sprintf(" (Q.850 %d)", *r.Cause)
	}
	expected := s.config.ExpectedStatus
	switch {
	case !s.ring && expected != nil && code == *expected:
		s.decide("success", code, in.at, "")
	case code == 503 && !s.anyProvisional && s.canFailover:
		// RFC 3263 §4.3: the next address. Nothing is ringing yet.
		s.failover = true
	case code == 401 || code == 407:
		switch {
		case s.auth == nil:
			s.decide("failure", code, in.at, fmt.Sprintf("auth: %s (server requires authentication but no credentials are configured)", status))
		case s.authNewRealm != "":
			s.decide("failure", code, in.at, fmt.Sprintf("auth: %s challenged realm %q after the first round; only one authentication round is supported (multi-hop authentication)", status, s.authNewRealm))
		default:
			s.decide("failure", code, in.at, fmt.Sprintf("auth: authentication failed: %s", status))
		}
	case code == 403:
		s.decide("failure", code, in.at, "auth: "+status)
	case code >= 300 && code < 400:
		contact := "none"
		if host := sipContactHost(msg); host != "" {
			contact = host
		}
		s.decide("failure", code, in.at, fmt.Sprintf("redirect: %s (not followed; contact %s)", status, contact))
	case code == 420 || code == 421:
		s.decide("failure", code, in.at, "extension: far end requires 100rel/timer (not supported)")
	case code == 488:
		s.decide("failure", code, in.at, "media: offer rejected (488); trunk may require SRTP (RTP/AVP only)")
	default:
		s.decide("failure", code, in.at, "status: "+status)
	}
}

func (s *sipCall) onAnswered(inv *sipInviteTxn, d *sipDialog, in sipInbound) {
	msg, code := in.msg, in.msg.StatusCode
	if s.primary == nil {
		s.primary = d
		since := in.at.Sub(inv.sentAt)
		s.seeFirstResponse(in.at)
		s.cm.Answered = true
		s.cm.AnswerMs = sipDurationMs(since)
		s.cm.SetupMs = sipDurationMs(in.at.Sub(s.tFirst))
		if s.cm.PDDMs == nil {
			s.cm.PDDMs = s.cm.AnswerMs
			s.cm.PDDSource = strconv.Itoa(code)
			s.cm.SetupPDDMs = s.cm.SetupMs
		}
		if answer, source, ok := s.answerSDP(d, msg); ok {
			if source == sipAnswerFrom2xx {
				s.cm.SDPAnswer = answer // sdp_answer is always the 2xx's
			}
			s.onMediaReady(answer, source)
		}
		by := &sipAnsweredBy{ContactHost: sipContactHost(msg)}
		if s.flow.peer != nil {
			by.Source = s.flow.peer.String()
		}
		for _, rr := range msg.Values("Record-Route") {
			if len(by.RecordRoute) >= sipMaxCallList {
				break
			}
			if host := sipNameAddrHost(rr); host != "" {
				by.RecordRoute = append(by.RecordRoute, host)
			}
		}
		s.cm.AnsweredBy = by
		if hops := len(msg.Values("Record-Route")); hops > s.cm.RouteHops {
			s.cm.RouteHops = hops
		}
		s.metrics.recordFinal(msg)
		s.metrics.Timing.ResponseMs = sipDurationMs(since)
	} else {
		s.cm.ForkedAnswers++
	}
	if s.ring {
		s.metrics.warn("answered in ring mode — billable")
	}

	switch s.phase {
	case sipCallCancelling:
		// Answered after the call was given up on or, in ring mode, decided.
		// With a CANCEL out, the 2xx crossed it; without one (no
		// provisional ever came) the BYE's outcome labels the teardown.
		if s.cancelSent {
			s.cm.Teardown = "answered_after_cancel"
		} else {
			s.cm.Teardown = "none"
		}
		s.phase = sipCallTerminating
		return
	case sipCallHolding, sipCallTerminating, sipCallDone:
		return // a forked or late answer: BYE'd at teardown
	}

	expected := s.config.ExpectedStatus
	switch {
	case s.ring:
		s.decide("success", code, in.at, "")
		s.phase = sipCallTerminating
	case expected != nil && code != *expected:
		s.decide("failure", code, in.at, fmt.Sprintf("status: %d %s (expected %d)", code, msg.Reason, *expected))
		s.phase = sipCallTerminating
	default:
		s.decide("success", code, in.at, "")
		s.phase = sipCallHolding
	}
}

// onMediaReady plans the call's media from the answered dialog's SDP answer
// (source: sipAnswerFrom2xx or sipAnswerFrom18x). It sends nothing: media
// starts with the hold (drive), which a CANCEL race, ring mode or a status
// mismatch never reaches. An answer taken from the 18x is worth a warning.
func (s *sipCall) onMediaReady(answer *sipSDPAnswer, source string) {
	if s.media == nil {
		return
	}
	s.media.prepare(answer, source)
	if source == sipAnswerFrom18x {
		s.metrics.warn("media: answer taken from the 18x (the 200 carried no SDP)")
	}
}

// keepEarlyAnswer records an 18x's SDP as the latest answer of its early
// dialog. An 18x without a To tag opens no dialog and is not kept.
func (s *sipCall) keepEarlyAnswer(msg *sipMessage) {
	tag := sipHeaderTag(msg.Get("To"))
	if tag == "" {
		return
	}
	if _, known := s.earlyAnswers[tag]; !known && len(s.earlyAnswers) >= sipCallMaxEarlyAnswers {
		return
	}
	if s.earlyAnswers == nil {
		s.earlyAnswers = make(map[string]*sipSDPAnswer, sipCallMaxEarlyAnswers)
	}
	s.earlyAnswers[tag] = parseSDPAnswer(msg.Body)
}

// answerSDP is the SDP answer of the answered dialog d: the 2xx's own when it
// carries one, else the last 18x SDP of the same dialog (same To tag). RFC
// 3264 puts the answer in the 2xx when there is no reliable provisional, but
// SBCs that treat a 183's SDP as the answer and send a bare 200 are common,
// so the dialog's 18x stands in. An 18x of another dialog (another fork)
// never does. ok is false when neither carried SDP.
func (s *sipCall) answerSDP(d *sipDialog, msg *sipMessage) (answer *sipSDPAnswer, source string, ok bool) {
	if sipBodyIsSDP(msg) {
		return parseSDPAnswer(msg.Body), sipAnswerFrom2xx, true
	}
	if early, found := s.earlyAnswers[d.remoteTag]; found {
		return early, sipAnswerFrom18x, true
	}
	return nil, "", false
}

// teardown always runs after drive, under its own budget: it settles a
// pending INVITE (CANCEL once a provisional came, or listens for a late
// answer), then BYEs every confirmed dialog, forks included.
func (s *sipCall) teardown(ctx context.Context) {
	// First, before any CANCEL or BYE: no RTP ever follows the hang-up.
	s.media.stop(time.Now())
	defer func() {
		if r := recover(); r != nil {
			s.decided = false
			s.decide("error", 0, time.Time{}, fmt.Sprintf("internal: SIP teardown aborted: %v", r))
		}
	}()
	s.hook("teardown")
	if s.phase == sipCallCancelling {
		s.settleInvite(ctx)
	}
	s.byeAll(ctx)
	if s.cm.Teardown == "bye_unconfirmed" {
		s.metrics.warn(sipByeUnconfirmedWarning)
	}
}

// sipByeUnconfirmedWarning is the warning an unanswered BYE adds; media
// evidence that the far end kept sending rewrites it (addMediaNotes).
const sipByeUnconfirmedWarning = "teardown: BYE unconfirmed"

// settleInvite waits out a pending INVITE that was given up on.
func (s *sipCall) settleInvite(ctx context.Context) {
	var cancelDone <-chan error
	sendCancel := func() {
		s.cancelSent = true
		cancel := buildSIPCancel(s.current.params)
		done := make(chan error, 1)
		go func() {
			_, err := s.ep.roundTripNonInvite(ctx, cancel, nil)
			done <- err
		}()
		cancelDone = done
	}
	if s.proceeding {
		sendCancel()
	}
	for s.phase == sipCallCancelling {
		select {
		case ev := <-s.events:
			s.handle(ev)
			if s.phase == sipCallCancelling && s.proceeding && !s.cancelSent {
				sendCancel() // a late provisional to an unanswered INVITE
			}
		case <-cancelDone:
			// The CANCEL's own 200 (or its failure): the INVITE's 487 is
			// what confirms the call ended.
			cancelDone = nil
		case <-s.ep.done:
			s.drainEvents() // a 487 sent just before the close settles the call
			s.settleTeardownLabel()
			return
		case <-ctx.Done():
			s.settleTeardownLabel()
			return
		}
	}
	s.settleTeardownLabel()
}

func (s *sipCall) settleTeardownLabel() {
	if s.phase != sipCallCancelling {
		return // done (cancelled, or a final crossed) or answered
	}
	if s.cancelSent {
		s.cm.Teardown = "cancel_unconfirmed"
		s.metrics.warn("teardown: CANCEL unconfirmed")
	} else {
		s.cm.Teardown = "no_provisional"
	}
}

// byeAll BYEs every dialog not yet ended, including forks that answer while
// it runs. The first answered dialog's BYE labels the teardown.
func (s *sipCall) byeAll(ctx context.Context) {
	for {
		s.drainEvents()
		d := s.nextUnended()
		if d == nil {
			return
		}
		if d == s.primary {
			s.media.markBye(time.Now())
		}
		outcome, status, ms := s.bye(ctx, d)
		if d == s.primary {
			if s.cm.Teardown == "none" || s.cm.Teardown == "" {
				s.cm.Teardown = outcome
			}
			if s.cm.Teardown == outcome || s.cm.Teardown == "answered_after_cancel" {
				s.cm.TeardownStatus = status
			}
			s.cm.ByeMs = ms
		}
		if outcome == "bye_unconfirmed" {
			s.metrics.warn(sipByeUnconfirmedWarning)
		}
	}
}

// nextUnended marks and returns a dialog that still needs a BYE.
func (s *sipCall) nextUnended() *sipDialog {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, d := range s.dialogs {
		if !d.byeDone && !d.remoteBye {
			d.byeDone = true
			return d
		}
	}
	return nil
}

// bye ends one dialog: 2xx is bye_ok, 481 bye_481 (the far end had already
// ended it), a challenge is answered once, no answer within the budget is
// bye_unconfirmed, anything else bye_<code>.
func (s *sipCall) bye(ctx context.Context, d *sipDialog) (outcome string, status int, ms *int64) {
	cseq := d.inviteCSeq + 1
	var credentials []sipHeader
	for round := 0; ; round++ {
		req := d.buildBye(s.flow, cseq, credentials)
		sent := time.Now()
		in, err := s.ep.roundTripNonInvite(ctx, req, nil)
		if err != nil {
			return "bye_unconfirmed", 0, nil
		}
		code := in.msg.StatusCode
		ms = sipDurationMs(in.at.Sub(sent))
		switch {
		case code >= 200 && code < 300:
			return "bye_ok", code, ms
		case code == 481:
			return "bye_481", code, ms
		case (code == 401 || code == 407) && round == 0 && s.auth != nil:
			answer, err := s.auth.answer(in.msg, "BYE", req.RequestURI)
			if err == nil {
				credentials, err = s.auth.credentials(answer, "BYE", req.RequestURI)
			}
			if err != nil {
				return fmt.Sprintf("bye_%d", code), code, ms
			}
			cseq++
		default:
			return fmt.Sprintf("bye_%d", code), code, ms
		}
	}
}

// applyThresholds applies the call's PDD and answer-time thresholds to a
// success or failure (an error measured nothing): max fails the check, warn
// adds a warning and latency_warn_ms.
func (s *sipCall) applyThresholds() {
	if !s.decided || s.result.Status == "error" {
		return
	}
	call := s.config.Call
	check := func(name string, value *int64, warn, max *int) {
		if value == nil {
			return
		}
		switch {
		case max != nil && *value > int64(*max):
			assertion := fmt.Sprintf("%s: %dms > %dms", name, *value, *max)
			s.metrics.AssertionsFailed = append(s.metrics.AssertionsFailed, assertion)
			msg := assertion
			if s.result.ErrorMessage != nil && *s.result.ErrorMessage != "" {
				msg = *s.result.ErrorMessage + "; " + assertion
			}
			s.result.Status = "failure"
			s.result.ErrorMessage = &msg
		case warn != nil && *value > int64(*warn):
			s.metrics.warn(fmt.Sprintf("%s: %dms > %dms (warn)", name, *value, *warn))
			if s.metrics.LatencyWarnMs == nil {
				w := int64(*warn)
				s.metrics.LatencyWarnMs = &w
			}
		}
	}
	check("pdd", s.cm.PDDMs, call.WarnPDDMs, call.MaxPDDMs)
	if !s.ring {
		check("answer", s.cm.AnswerMs, call.WarnAnswerMs, call.MaxAnswerMs)
	}
}

// sipRequires reports an option tag in a message's Require header(s).
func sipRequires(msg *sipMessage, tag string) bool {
	for _, value := range msg.Values("Require") {
		for _, t := range strings.Split(value, ",") {
			if strings.EqualFold(strings.TrimSpace(t), tag) {
				return true
			}
		}
	}
	return false
}

// sipContactHost is the host of a message's first Contact, for the record
// only: it is never dialed.
func sipContactHost(msg *sipMessage) string {
	return sipNameAddrHost(msg.Get("Contact"))
}

// sipNameAddrHost is the host of a name-addr's SIP URI ("" when it has none).
func sipNameAddrHost(value string) string {
	if value == "" {
		return ""
	}
	na, err := parseSIPNameAddr(value)
	if err != nil {
		return ""
	}
	_, rest, ok := strings.Cut(na.URI, ":")
	if !ok {
		return ""
	}
	if _, after, found := strings.Cut(rest, "@"); found {
		rest = after
	}
	rest, _, _ = strings.Cut(rest, ";")
	rest, _, _ = strings.Cut(rest, "?")
	if strings.HasPrefix(rest, "[") {
		if end := strings.IndexByte(rest, ']'); end > 0 {
			return sipMetricsString(rest[1:end])
		}
	}
	host, _, _ := strings.Cut(rest, ":")
	return sipMetricsString(host)
}
