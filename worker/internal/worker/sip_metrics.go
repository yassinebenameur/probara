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
// as dialing, so a failed check still says where it went, how far it got and
// what the server said. The web detail page reads it (SIPMetricsEnvelope in
// web/lib/types.ts) and /metrics-snapshots serves it like any other type.
//
// Durations are integer milliseconds. Strings copied from the server are
// capped at sipMetricsMaxString: the envelope is stored on every raw check
// row, not an archive of the server's headers.
type sipMetricsEnvelope struct {
	SIP *sipMetrics `json:"sip,omitempty"`
}

type sipMetrics struct {
	Method     string `json:"method"`    // OPTIONS, REGISTER
	Transport  string `json:"transport"` // udp, tcp, tls
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

	Target *sipTargetMetrics `json:"target,omitempty"`
	Timing sipTimingMetrics  `json:"timing"`

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

	// AssertionsFailed lists the check's own rules that failed it, in the
	// error message's "name: detail" form.
	AssertionsFailed []string `json:"assertions_failed,omitempty"`
}

// sipReasonHeader is the Reason header (RFC 3326) of the final response,
// preferring its Q.850 entry: the cause code carriers use to say why a call
// or request was refused.
type sipReasonHeader struct {
	Protocol string `json:"protocol"`
	Cause    *int   `json:"cause,omitempty"`
	Text     string `json:"text,omitempty"`
}

// sipTargetMetrics is the peer the check dialed. Host is the name dialed
// (after the SIP_LOCALHOST_AS_HOST_GATEWAY rewrite), Address the IP:port the
// flow connected to, ResolvedVia how Host became that address: "literal" for
// an IP, "a" for an A/AAAA lookup.
type sipTargetMetrics struct {
	Host        string `json:"host"`
	Address     string `json:"address,omitempty"`
	ResolvedVia string `json:"resolved_via"`
}

// sipTimingMetrics reuses the HTTP timing names. Anchors:
//   - connect_ms: dial (UDP: socket setup; TCP/TLS: TCP connect).
//   - tls_handshake_ms: the TLS handshake on top of it.
//   - first_response_ms: first request sent → the first response to it of
//     any kind, provisional included.
//   - response_ms: the measured request (the authenticated retry when
//     challenged) sent → its final response.
//   - total_ms: check start → end; equals latency_ms.
type sipTimingMetrics struct {
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

// newSIPTargetMetrics describes the dialed host before the dial completes.
func newSIPTargetMetrics(host string) *sipTargetMetrics {
	via := "a"
	if net.ParseIP(strings.Trim(host, "[]")) != nil {
		via = "literal"
	}
	return &sipTargetMetrics{Host: host, ResolvedVia: via}
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

// recordEndpoint copies the transaction layer's counters.
func (m *sipMetrics) recordEndpoint(e *sipEndpoint) {
	m.Retransmissions = e.retransmissions.Load()
	m.StrayMessages = e.stray.Load()
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
