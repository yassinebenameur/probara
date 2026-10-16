package worker

import (
	"encoding/json"
	"net"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/yassinebenameur/probara/shared/tlscert"
)

// sipMetricsEnvelope is a SIP check's metrics_data: {"sip": {…}}. It is
// written on success, failure and error alike whenever the check got as far
// as resolving its host, so a failed check still says where it went, how far
// it got and what the server said. The web detail page reads it (SIPMetricsEnvelope in
// web/lib/types.ts) and /metrics-snapshots serves it like any other type.
//
// Durations are integer milliseconds. Strings copied from the server are
// capped at sipMetricsMaxString: the envelope is stored on every raw check
// row, not an archive of the server's headers.
type sipMetricsEnvelope struct {
	SIP *sipMetrics `json:"sip,omitempty"`
}

type sipMetrics struct {
	Method string `json:"method"` // OPTIONS, REGISTER, INVITE
	// Transport is the deciding attempt's: udp, tcp or tls. Empty only when
	// rfc3263 resolution left it to DNS and DNS failed before choosing one.
	Transport  string `json:"transport,omitempty"`
	RequestURI string `json:"request_uri,omitempty"`
	// CallID is set once a request has been sent: it is what to search for
	// in the server's own logs.
	CallID string `json:"call_id,omitempty"`

	// The final response that decided the check (after any digest retry).
	StatusCode int    `json:"status_code,omitempty"`
	Reason     string `json:"reason,omitempty"`
	// Server is the final response's Server header, else its User-Agent.
	Server       string           `json:"server,omitempty"`
	Warning      string           `json:"warning,omitempty"` // Warning header (RFC 3261 §20.43)
	ReasonHeader *sipReasonHeader `json:"reason_header,omitempty"`

	Target     *sipTargetMetrics     `json:"target,omitempty"`
	Resolution *sipResolutionMetrics `json:"resolution,omitempty"`
	Timing     sipTimingMetrics      `json:"timing"`

	// Retransmissions counts UDP resends under Timer E; StrayMessages counts
	// responses that matched no transaction of this check and were ignored.
	Retransmissions int64 `json:"retransmissions"`
	StrayMessages   int64 `json:"stray_messages"`

	Auth *sipAuthMetrics `json:"auth,omitempty"`

	// TLS is the server certificate of a TLS flow, captured whenever the
	// handshake completed (tls_skip_verify included). Its location is
	// registered in shared/tlscert, which the alerter's tls_expiry
	// evaluation and the status page read through.
	TLS *tlscert.Info `json:"tls,omitempty"`

	// Call describes an INVITE test call (sip_invite.go).
	Call *sipCallMetrics `json:"call,omitempty"`

	// LatencyWarnMs is the warn threshold (warn_pdd_ms, else
	// warn_answer_ms) a successful call exceeded; Warnings says which, and
	// carries the other things worth an operator's attention that did not
	// change the status (an unconfirmed teardown, a ring-mode call that was
	// answered and so billed).
	LatencyWarnMs *int64   `json:"latency_warn_ms,omitempty"`
	Warnings      []string `json:"warnings,omitempty"`

	// AssertionsFailed lists the check's own rules that failed it, in the
	// error message's "name: detail" form.
	AssertionsFailed []string `json:"assertions_failed,omitempty"`
}

// sipMaxCallList caps the provisional, warnings and record_route lists.
const sipMaxCallList = 8

func (m *sipMetrics) warn(msg string) {
	for _, w := range m.Warnings {
		if w == msg {
			return
		}
	}
	if len(m.Warnings) < sipMaxCallList {
		m.Warnings = append(m.Warnings, msg)
	}
}

// sipCallMetrics is metrics_data.sip.call. Timing anchors (integer ms):
//   - pdd_ms (RFC 6076 SRD): the INVITE that produced it (the authenticated
//     one when challenged) → its first 18x, or its 2xx when no 18x came
//     (pdd_source then "200");
//   - answer_ms: that INVITE → the first 2xx;
//   - setup_pdd_ms, setup_ms: the first INVITE → the first 18x / 2xx, so
//     they include the digest round trip;
//   - trying_ms: the INVITE → its first 100;
//   - bye_ms (RFC 6076 SDD): the BYE → its final response.
//
// Provisional at_ms is relative to the INVITE that drew it.
type sipCallMetrics struct {
	Mode          string               `json:"mode"`
	TryingMs      *int64               `json:"trying_ms,omitempty"`
	PDDMs         *int64               `json:"pdd_ms,omitempty"`
	PDDSource     string               `json:"pdd_source,omitempty"`
	SetupPDDMs    *int64               `json:"setup_pdd_ms,omitempty"`
	AnswerMs      *int64               `json:"answer_ms,omitempty"`
	SetupMs       *int64               `json:"setup_ms,omitempty"`
	HoldMs        int64                `json:"hold_ms"`
	ByeMs         *int64               `json:"bye_ms,omitempty"`
	Provisional   []sipProvisionalStep `json:"provisional,omitempty"`
	EarlyMedia    bool                 `json:"early_media"`
	Answered      bool                 `json:"answered"`
	ForkedAnswers int                  `json:"forked_answers"`
	RouteHops     int                  `json:"route_hops"`
	Require100rel bool                 `json:"require_100rel"`
	AnsweredBy    *sipAnsweredBy       `json:"answered_by,omitempty"`
	SDPAnswer     *sipSDPAnswer        `json:"sdp_answer,omitempty"`
	RTPRxPackets  int64                `json:"rtp_rx_packets"`
	// Teardown is how the call ended: bye_ok, bye_481, bye_<code>,
	// bye_unconfirmed, cancelled, cancel_unconfirmed, answered_after_cancel,
	// remote_bye, no_provisional or none; TeardownStatus is the final
	// response that settled it (the BYE's, or the INVITE's 487).
	Teardown       string `json:"teardown"`
	TeardownStatus int    `json:"teardown_status,omitempty"`
}

type sipProvisionalStep struct {
	Code int   `json:"code"`
	AtMs int64 `json:"at_ms"`
}

// sipAnsweredBy is who answered: the flow's peer, the 2xx Contact host (never
// contacted) and its Record-Route hosts.
type sipAnsweredBy struct {
	Source      string   `json:"source,omitempty"`
	ContactHost string   `json:"contact_host,omitempty"`
	RecordRoute []string `json:"record_route,omitempty"`
}

// sipReasonHeader is the Reason header (RFC 3326) of the final response,
// preferring its Q.850 entry: the cause code carriers use to say why a call
// or request was refused.
type sipReasonHeader struct {
	Protocol string `json:"protocol"`
	Cause    *int   `json:"cause,omitempty"`
	Text     string `json:"text,omitempty"`
}

// sipTargetMetrics is the peer that decided the check (the last one tried).
// Host is the name it came from — the configured host, or the SRV target —
// after the SIP_LOCALHOST_AS_HOST_GATEWAY rewrite; Address the IP:port the
// flow connected to; ResolvedVia how Host became that address: "literal" for
// an IP, "a" for an A/AAAA lookup, "srv" for an SRV target, "naptr" for an
// SRV target whose SRV name a NAPTR record gave. Failover is set
// when an earlier address was tried first and given up on.
type sipTargetMetrics struct {
	Host        string `json:"host"`
	Address     string `json:"address,omitempty"`
	ResolvedVia string `json:"resolved_via"`
	Failover    bool   `json:"failover,omitempty"`
}

// sipResolutionMetrics is how the check found its addresses (sip_resolve.go)
// and what happened at each one. Mode is "a" or "rfc3263"; NAPTRStatus
// (rfc3263 only) is ok, none, unavailable (no nameserver answered; SRV went
// ahead without it) or skipped (an IP host, or a port or transport was set);
// NAPTR lists the usable NAPTR records in order and preference; SRV lists
// the records of the SRV name that was used, in the order they were tried;
// Attempts lists every address tried (or SRV target that did not resolve),
// in order, with its outcome: ok (a final response decided the check), 503,
// or the error class (refused, timeout, tls, ssrf_blocked, connect, dns, …).
type sipResolutionMetrics struct {
	Mode        string              `json:"mode"`
	NAPTRStatus string              `json:"naptr_status,omitempty"`
	NAPTR       []sipNAPTRMetrics   `json:"naptr,omitempty"`
	SRV         []sipSRVMetrics     `json:"srv,omitempty"`
	Attempts    []sipAttemptMetrics `json:"attempts,omitempty"`

	lookedUp bool // a DNS query was made: dns_ms is meaningful
}

type sipNAPTRMetrics struct {
	Order       int    `json:"order"`
	Pref        int    `json:"pref"`
	Service     string `json:"service"`     // SIP+D2U, SIP+D2T, SIPS+D2T
	Replacement string `json:"replacement"` // the SRV name it leads to
}

type sipSRVMetrics struct {
	Target   string `json:"target"`
	Port     int    `json:"port"`
	Priority int    `json:"priority"`
	Weight   int    `json:"weight"`
}

type sipAttemptMetrics struct {
	Address   string `json:"address"`
	Transport string `json:"transport"`
	Outcome   string `json:"outcome"`
	Ms        int64  `json:"ms"`
}

func (r *sipResolutionMetrics) addAttempt(a sipAttemptMetrics) {
	if len(r.Attempts) < sipMaxResolutionList {
		r.Attempts = append(r.Attempts, a)
	}
}

// sipTimingMetrics reuses the HTTP timing names. dns_ms and total_ms cover
// the whole check; the others describe the attempt that decided it (after a
// failover, the last address tried). Anchors:
//   - dns_ms: every NAPTR, SRV and A/AAAA lookup; absent for an IP literal.
//   - connect_ms: dial (UDP: socket setup; TCP/TLS: TCP connect).
//   - tls_handshake_ms: the TLS handshake on top of it.
//   - first_response_ms: first request sent → the first response to it of
//     any kind, provisional included.
//   - response_ms: the measured request (the authenticated retry when
//     challenged) sent → its final response.
//   - total_ms: check start → end; equals latency_ms.
type sipTimingMetrics struct {
	DNSMs           *int64 `json:"dns_ms,omitempty"`
	ConnectMs       *int64 `json:"connect_ms,omitempty"`
	TLSHandshakeMs  *int64 `json:"tls_handshake_ms,omitempty"`
	FirstResponseMs *int64 `json:"first_response_ms,omitempty"`
	ResponseMs      *int64 `json:"response_ms,omitempty"`
	TotalMs         int64  `json:"total_ms"`
}

// sipAuthMetrics describes a digest challenge. Rounds is how many
// authenticated retries were sent (0 when the challenge could not be
// answered); AuthMs is what the challenge added: first request sent → the
// authenticated retry sent.
type sipAuthMetrics struct {
	Challenge int    `json:"challenge"`           // 401 or 407
	Algorithm string `json:"algorithm,omitempty"` // MD5, SHA-256
	Rounds    int    `json:"rounds"`
	AuthMs    *int64 `json:"auth_ms,omitempty"`
}

const sipMetricsMaxString = 256

func sipMetricsString(s string) string {
	s = strings.TrimSpace(s)
	if len(s) <= sipMetricsMaxString {
		return s
	}
	cut := sipMetricsMaxString
	for cut > 0 && !utf8.RuneStart(s[cut]) {
		cut--
	}
	return s[:cut] + "…"
}

// sipDigestAlgorithmName is the digest algorithm as the envelope reports it;
// a challenge without one means MD5 (RFC 3261 §22.4).
func sipDigestAlgorithmName(algorithm string) string {
	algorithm = strings.ToUpper(strings.TrimSpace(algorithm))
	if algorithm == "" {
		return "MD5"
	}
	return sipMetricsString(algorithm)
}

func sipDurationMs(d time.Duration) *int64 {
	ms := d.Milliseconds()
	return &ms
}

// newSIPTargetMetrics describes the configured host before resolution and
// the dial complete.
func newSIPTargetMetrics(host string) *sipTargetMetrics {
	via := "a"
	if net.ParseIP(strings.Trim(host, "[]")) != nil {
		via = "literal"
	}
	return &sipTargetMetrics{Host: host, ResolvedVia: via}
}

// startAttempt points the envelope at a new candidate and forgets what the
// previous attempt recorded, so the per-attempt fields (timing, final
// response, auth, certificate) all describe the attempt that decides the
// check. Retransmission and stray counts accumulate over the check.
func (m *sipMetrics) startAttempt(c sipCandidate, failover bool) {
	m.Transport = c.transport
	m.Target = &sipTargetMetrics{Host: c.host, ResolvedVia: c.via, Failover: failover}
	m.CallID = ""
	m.clearFinal()
	m.Timing.ConnectMs, m.Timing.TLSHandshakeMs, m.Timing.FirstResponseMs = nil, nil, nil
	m.Auth = nil
	m.TLS = nil
}

// recordFlow copies what the dial learned: the connected address, the
// transport setup timing and, over TLS, the server certificate.
func (m *sipMetrics) recordFlow(flow *sipFlow, serverName string) {
	if m.Target != nil && flow.peer != nil {
		m.Target.Address = flow.peer.String()
	}
	m.recordDialTiming(flow.timing)
	if flow.tlsState != nil {
		m.TLS = tlscert.FromConnectionState(*flow.tlsState, serverName, time.Now())
	}
}

func (m *sipMetrics) recordDialTiming(t sipFlowTiming) {
	if t.Connect > 0 {
		m.Timing.ConnectMs = sipDurationMs(t.Connect)
	}
	if t.TLSHandshake > 0 {
		m.Timing.TLSHandshakeMs = sipDurationMs(t.TLSHandshake)
	}
}

// clearFinal forgets the previous transaction's final response before a new
// request is sent, so a retry that gets no answer does not leave the
// challenge it answered reading as the check's final status.
func (m *sipMetrics) clearFinal() {
	m.StatusCode, m.Reason, m.Server, m.Warning = 0, "", "", ""
	m.ReasonHeader = nil
	m.Timing.ResponseMs = nil
}

// recordFinal copies the deciding final response.
func (m *sipMetrics) recordFinal(resp *sipMessage) {
	m.StatusCode = resp.StatusCode
	m.Reason = sipMetricsString(resp.Reason)
	server := resp.Get("Server")
	if strings.TrimSpace(server) == "" {
		server = resp.Get("User-Agent")
	}
	m.Server = sipMetricsString(server)
	m.Warning = sipMetricsString(resp.Get("Warning"))
	m.ReasonHeader = parseSIPReasonHeader(resp.Values("Reason"))
}

// recordEndpoint adds one attempt's transaction-layer counters; they sum
// over every address a check tried.
func (m *sipMetrics) recordEndpoint(e *sipEndpoint) {
	m.Retransmissions += e.retransmissions.Load()
	m.StrayMessages += e.stray.Load()
}

// marshal finishes the envelope at the check's end. total is the check's
// latency_ms, so the two always agree.
func (m *sipMetrics) marshal(totalMs int64) json.RawMessage {
	m.Timing.TotalMs = totalMs
	raw, err := json.Marshal(sipMetricsEnvelope{SIP: m})
	if err != nil {
		return nil
	}
	return raw
}

// parseSIPReasonHeader picks the Q.850 entry of the Reason header(s), else the
// first entry: `Q.850;cause=17;text="User busy"`. Nil when absent.
func parseSIPReasonHeader(values []string) *sipReasonHeader {
	var entries []string
	for _, value := range values {
		for _, entry := range splitSIPList(value) {
			if strings.TrimSpace(entry) != "" {
				entries = append(entries, entry)
			}
		}
	}
	if len(entries) == 0 {
		return nil
	}
	chosen := entries[0]
	for _, entry := range entries {
		protocol, _, _ := strings.Cut(entry, ";")
		if strings.EqualFold(strings.TrimSpace(protocol), "Q.850") {
			chosen = entry
			break
		}
	}
	protocol, params, _ := strings.Cut(chosen, ";")
	reason := &sipReasonHeader{Protocol: sipMetricsString(protocol)}
	if reason.Protocol == "" {
		return nil
	}
	if cause, ok := sipParam(params, "cause"); ok {
		if n, err := strconv.Atoi(cause); err == nil && n >= 0 {
			reason.Cause = &n
		}
	}
	if text, ok := sipParam(params, "text"); ok {
		reason.Text = sipMetricsString(text)
	}
	return reason
}
