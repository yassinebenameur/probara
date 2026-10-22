package worker

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/yassinebenameur/probara/shared/models"
	"github.com/yassinebenameur/probara/shared/tlscert"
)

// decodeSIPMetrics unmarshals a result's metrics_data twice: as the typed
// envelope and as a generic map, so tests can pin both values and the
// presence of JSON keys the web UI reads.
func decodeSIPMetrics(t *testing.T, result CheckResult) (*sipMetrics, map[string]any) {
	t.Helper()
	if len(result.MetricsData) == 0 {
		t.Fatalf("MetricsData is empty (status %q, %v), want the sip envelope", result.Status, result.ErrorMessage)
	}
	var envelope sipMetricsEnvelope
	if err := json.Unmarshal(result.MetricsData, &envelope); err != nil {
		t.Fatalf("unmarshal MetricsData: %v\n%s", err, result.MetricsData)
	}
	if envelope.SIP == nil {
		t.Fatalf("MetricsData has no sip key: %s", result.MetricsData)
	}
	var generic map[string]map[string]any
	if err := json.Unmarshal(result.MetricsData, &generic); err != nil {
		t.Fatalf("unmarshal MetricsData: %v", err)
	}
	return envelope.SIP, generic["sip"]
}

func requireSIPMetricsKeys(t *testing.T, sip map[string]any, keys ...string) {
	t.Helper()
	for _, key := range keys {
		if _, ok := sip[key]; !ok {
			t.Errorf("metrics_data.sip has no %q key: %v", key, sip)
		}
	}
}

// total_ms is the check's latency_ms: the two must never disagree.
func requireSIPTotalMatchesLatency(t *testing.T, result CheckResult, m *sipMetrics) {
	t.Helper()
	if result.LatencyMs == nil {
		t.Fatal("LatencyMs = nil")
	}
	if m.Timing.TotalMs != *result.LatencyMs {
		t.Fatalf("timing.total_ms = %d, latency_ms = %d, want equal", m.Timing.TotalMs, *result.LatencyMs)
	}
}

func TestSIPMetrics_Success(t *testing.T) {
	host, port := startFakeUDPSIPServer(t, func(request string) []string {
		return []string{
			sipReply(request, 100, "Trying"),
			sipReply(request, 200, "OK", "Server: Example SBC 4.2", "User-Agent: ignored when Server is set"),
		}
	})

	result := runSIPCheck(t, map[string]interface{}{"host": host, "port": port, "transport": "udp"})
	if result.Status != "success" {
		t.Fatalf("Status = %q (%v), want success", result.Status, result.ErrorMessage)
	}
	m, raw := decodeSIPMetrics(t, result)
	requireSIPMetricsKeys(t, raw, "method", "transport", "request_uri", "call_id", "status_code", "reason",
		"server", "target", "timing", "retransmissions", "stray_messages")
	requireSIPTotalMatchesLatency(t, result, m)

	if m.Method != "OPTIONS" || m.Transport != "udp" {
		t.Errorf("method/transport = %q/%q, want OPTIONS/udp", m.Method, m.Transport)
	}
	if want := fmt.Sprintf("sip:%s:%d", host, port); m.RequestURI != want {
		t.Errorf("request_uri = %q, want %q", m.RequestURI, want)
	}
	if !strings.HasSuffix(m.CallID, "@monitor.local") {
		t.Errorf("call_id = %q, want the request's Call-ID", m.CallID)
	}
	if m.StatusCode != 200 || m.Reason != "OK" {
		t.Errorf("status = %d %q, want 200 OK", m.StatusCode, m.Reason)
	}
	if m.Server != "Example SBC 4.2" {
		t.Errorf("server = %q, want the Server header", m.Server)
	}
	if m.Target == nil || m.Target.Host != host || m.Target.ResolvedVia != "literal" ||
		m.Target.Address != net.JoinHostPort(host, fmt.Sprint(port)) {
		t.Errorf("target = %+v, want %s literal at %s:%d", m.Target, host, host, port)
	}
	if m.Timing.ConnectMs == nil || m.Timing.FirstResponseMs == nil || m.Timing.ResponseMs == nil {
		t.Errorf("timing = %+v, want connect, first response and response", m.Timing)
	}
	if m.Timing.TLSHandshakeMs != nil {
		t.Errorf("tls_handshake_ms = %d over UDP, want absent", *m.Timing.TLSHandshakeMs)
	}
	if m.Timing.FirstResponseMs != nil && m.Timing.ResponseMs != nil && *m.Timing.FirstResponseMs > *m.Timing.ResponseMs {
		t.Errorf("first_response_ms %d > response_ms %d: the 100 came before the 200", *m.Timing.FirstResponseMs, *m.Timing.ResponseMs)
	}
	if m.Retransmissions != 0 || m.StrayMessages != 0 || m.Auth != nil {
		t.Errorf("retransmissions %d, stray %d, auth %+v, want 0, 0, nil", m.Retransmissions, m.StrayMessages, m.Auth)
	}
}

// The User-Agent stands in for a missing Server header.
func TestSIPMetrics_ServerFallsBackToUserAgent(t *testing.T) {
	host, port := startFakeUDPSIPServer(t, func(request string) []string {
		return []string{sipReply(request, 200, "OK", "User-Agent: Asterisk PBX 20.1")}
	})
	result := runSIPCheck(t, map[string]interface{}{"host": host, "port": port})
	m, _ := decodeSIPMetrics(t, result)
	if m.Server != "Asterisk PBX 20.1" {
		t.Fatalf("server = %q, want the User-Agent", m.Server)
	}
}

// A failure carries the server's own explanation: status, Warning and the
// Q.850 cause of the Reason header.
func TestSIPMetrics_FailureCarriesServerExplanation(t *testing.T) {
	host, port := startFakeUDPSIPServer(t, func(request string) []string {
		return []string{sipReply(request, 503, "Service Unavailable",
			`Warning: 399 sbc.example.com "Trunk down"`,
			`Reason: SIP;cause=503;text="Overload", Q.850;cause=41;text="Temporary failure"`)}
	})

	result := runSIPCheck(t, map[string]interface{}{"host": host, "port": port})
	if result.Status != "failure" {
		t.Fatalf("Status = %q (%v), want failure", result.Status, result.ErrorMessage)
	}
	m, raw := decodeSIPMetrics(t, result)
	requireSIPMetricsKeys(t, raw, "status_code", "reason", "warning", "reason_header", "call_id", "timing")
	requireSIPTotalMatchesLatency(t, result, m)
	if m.StatusCode != 503 || m.Reason != "Service Unavailable" {
		t.Errorf("status = %d %q, want 503 Service Unavailable", m.StatusCode, m.Reason)
	}
	if m.Warning != `399 sbc.example.com "Trunk down"` {
		t.Errorf("warning = %q", m.Warning)
	}
	rh := m.ReasonHeader
	if rh == nil || rh.Protocol != "Q.850" || rh.Cause == nil || *rh.Cause != 41 || rh.Text != "Temporary failure" {
		t.Errorf("reason_header = %+v, want the Q.850 entry (cause 41)", rh)
	}
}

// A challenged REGISTER records the challenge, the algorithm and the round,
// and the envelope's status is the authenticated retry's, not the 401.
func TestSIPMetrics_DigestChallenge(t *testing.T) {
	challenge := `WWW-Authenticate: Digest realm="probara.test", nonce="n1", qop="auth", algorithm=SHA-256`
	host, port := startFakeUDPSIPServer(t, func(request string) []string {
		if strings.Contains(request, "\r\nAuthorization:") {
			return []string{sipReply(request, 403, "Forbidden")}
		}
		return []string{sipReply(request, 401, "Unauthorized", challenge)}
	})

	result := runSIPCheck(t, map[string]interface{}{
		"host": host, "port": port, "method": "register", "domain": "probara.test",
		"username": "agent42", "password": "s3cret",
	})
	if result.Status != "failure" {
		t.Fatalf("Status = %q (%v), want failure", result.Status, result.ErrorMessage)
	}
	m, raw := decodeSIPMetrics(t, result)
	requireSIPMetricsKeys(t, raw, "auth")
	if m.Method != "REGISTER" || m.RequestURI != "sip:probara.test" {
		t.Errorf("method %q request_uri %q, want REGISTER sip:probara.test", m.Method, m.RequestURI)
	}
	if m.StatusCode != 403 {
		t.Errorf("status_code = %d, want the retry's 403", m.StatusCode)
	}
	a := m.Auth
	if a == nil || a.Challenge != 401 || a.Algorithm != "SHA-256" || a.Rounds != 1 || a.AuthMs == nil {
		t.Fatalf("auth = %+v, want challenge 401 SHA-256, 1 round, auth_ms set", a)
	}

	// Without credentials the challenge is recorded with no round.
	noCreds := runSIPCheck(t, map[string]interface{}{"host": host, "port": port, "method": "register"})
	m, _ = decodeSIPMetrics(t, noCreds)
	if m.Auth == nil || m.Auth.Challenge != 401 || m.Auth.Rounds != 0 || m.Auth.AuthMs != nil {
		t.Fatalf("auth = %+v, want challenge 401, 0 rounds", m.Auth)
	}
	if m.StatusCode != 401 {
		t.Fatalf("status_code = %d, want 401", m.StatusCode)
	}
}

// A challenged REGISTER whose authenticated retry is never answered times
// out with no final response: the 401 it answered must not read as the
// check's status (clearFinal), while its arrival and the round stay.
func TestSIPMetrics_AuthenticatedRetryUnanswered(t *testing.T) {
	challenge := `WWW-Authenticate: Digest realm="probara.test", nonce="n1", qop="auth", algorithm=SHA-256`
	host, port := startFakeUDPSIPServer(t, func(request string) []string {
		if strings.Contains(request, "\r\nAuthorization:") {
			return nil // silent to the authenticated retry
		}
		return []string{sipReply(request, 401, "Unauthorized", challenge, "Server: Registrar/1.0")}
	})
	t.Setenv("SIP_LOCALHOST_AS_HOST_GATEWAY", "")
	raw, _ := json.Marshal(map[string]interface{}{
		"host": host, "port": port, "transport": "udp", "method": "register",
		"domain": "probara.test", "username": "agent42", "password": "s3cret",
	})
	result := NewSIPChecker(false, nil).Check(context.Background(), raw, 1)
	requireSIPError(t, result, "timeout")

	m, _ := decodeSIPMetrics(t, result)
	requireSIPTotalMatchesLatency(t, result, m)
	if m.StatusCode != 0 || m.Reason != "" || m.Server != "" || m.Timing.ResponseMs != nil {
		t.Errorf("status %d reason %q server %q response_ms %v, want the challenge cleared", m.StatusCode, m.Reason, m.Server, m.Timing.ResponseMs)
	}
	if m.Timing.FirstResponseMs == nil {
		t.Errorf("first_response_ms = nil, want the 401's arrival kept")
	}
	if a := m.Auth; a == nil || a.Challenge != 401 || a.Rounds != 1 || a.AuthMs == nil {
		t.Fatalf("auth = %+v, want challenge 401, 1 round, auth_ms set", a)
	}
}

// A dial that never connects still writes the envelope: where the check
// went, and no Call-ID since nothing was sent.
func TestSIPMetrics_ErrorBeforeAnyRequest(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("Listen() error = %v", err)
	}
	host, port := splitTestHostPort(t, listener.Addr().String())
	listener.Close()

	result := runSIPCheck(t, map[string]interface{}{"host": host, "port": port, "transport": "tcp"})
	requireSIPError(t, result, "refused")
	m, raw := decodeSIPMetrics(t, result)
	requireSIPMetricsKeys(t, raw, "method", "transport", "request_uri", "target", "timing", "retransmissions", "stray_messages")
	requireSIPTotalMatchesLatency(t, result, m)
	if m.CallID != "" || m.StatusCode != 0 {
		t.Errorf("call_id %q status %d, want none: no request was sent", m.CallID, m.StatusCode)
	}
	if m.Target == nil || m.Target.Host != host || m.Target.Address != "" {
		t.Errorf("target = %+v, want host %s and no connected address", m.Target, host)
	}
}

// An unanswered UDP request: the envelope shows the retransmissions and no
// status.
func TestSIPMetrics_ErrorNoResponse(t *testing.T) {
	var requests atomic.Int32
	host, port := startFakeUDPSIPServer(t, func(string) []string {
		requests.Add(1)
		return nil
	})
	t.Setenv("SIP_LOCALHOST_AS_HOST_GATEWAY", "")
	raw, _ := json.Marshal(map[string]interface{}{"host": host, "port": port, "transport": "udp"})
	result := NewSIPChecker(false, nil).Check(context.Background(), raw, 1)
	requireSIPError(t, result, "timeout")

	m, keys := decodeSIPMetrics(t, result)
	requireSIPMetricsKeys(t, keys, "call_id", "retransmissions", "timing")
	requireSIPTotalMatchesLatency(t, result, m)
	if m.StatusCode != 0 || m.Timing.ResponseMs != nil || m.Timing.FirstResponseMs != nil {
		t.Errorf("status %d timing %+v, want no response recorded", m.StatusCode, m.Timing)
	}
	// Sends at 0 and 0.5s within a 1s budget: at least one resend.
	if m.Retransmissions < 1 || m.Retransmissions != int64(requests.Load())-1 {
		t.Errorf("retransmissions = %d with %d sends, want sends-1 ≥ 1", m.Retransmissions, requests.Load())
	}
}

// Stray responses (another branch, another method, no Via) are counted.
func TestSIPMetrics_CountsStrayResponses(t *testing.T) {
	host, port := startFakeUDPSIPServer(t, func(request string) []string {
		oldBranch := strings.Replace(request, ";branch=z9hG4bK", ";branch=z9hG4bKold", 1)
		return []string{
			sipReply(oldBranch, 401, "Unauthorized"),
			sipResponseMsg(503, "No Via At All"),
			sipReply(request, 200, "OK"),
		}
	})
	result := runSIPCheck(t, map[string]interface{}{"host": host, "port": port})
	if result.Status != "success" {
		t.Fatalf("Status = %q (%v), want success", result.Status, result.ErrorMessage)
	}
	m, _ := decodeSIPMetrics(t, result)
	if m.StrayMessages != 2 {
		t.Fatalf("stray_messages = %d, want 2", m.StrayMessages)
	}
}

func TestSIPMetrics_TLSTiming(t *testing.T) {
	host, port := startFakeTLSSIPServer(t, time.Now().Add(time.Hour))

	ok := runSIPCheck(t, map[string]interface{}{"host": host, "port": port, "transport": "tls", "tls_skip_verify": true})
	if ok.Status != "success" {
		t.Fatalf("Status = %q (%v), want success", ok.Status, ok.ErrorMessage)
	}
	m, raw := decodeSIPMetrics(t, ok)
	if timing, _ := raw["timing"].(map[string]any); timing == nil || timing["tls_handshake_ms"] == nil || timing["connect_ms"] == nil {
		t.Errorf("timing = %v, want connect_ms and tls_handshake_ms", raw["timing"])
	}
	if m.Transport != "tls" {
		t.Errorf("transport = %q, want tls", m.Transport)
	}

	// A failed handshake keeps the TCP connect time (the TCP leg worked)
	// and has no handshake time.
	failed := runSIPCheck(t, map[string]interface{}{"host": host, "port": port, "transport": "tls"})
	requireSIPError(t, failed, "tls")
	m, _ = decodeSIPMetrics(t, failed)
	if m.Timing.ConnectMs == nil || m.Timing.TLSHandshakeMs != nil {
		t.Errorf("timing = %+v, want connect_ms only", m.Timing)
	}
	// The TCP leg connected: the address that served the rejected
	// certificate is recorded.
	if m.Target == nil || m.Target.Address != net.JoinHostPort(host, fmt.Sprint(port)) {
		t.Errorf("target = %+v, want the connected address after a failed handshake", m.Target)
	}
}

// A config the worker refuses never ran a phase: no envelope.
func TestSIPMetrics_NoEnvelopeForInvalidConfig(t *testing.T) {
	result := runSIPCheck(t, map[string]interface{}{"host": "sip.example.com", "port": 70000})
	if result.Status != "error" {
		t.Fatalf("Status = %q, want error", result.Status)
	}
	if len(result.MetricsData) != 0 {
		t.Fatalf("MetricsData = %s, want none", result.MetricsData)
	}
}

func TestSIPMetrics_HostnameResolvedViaA(t *testing.T) {
	if got := newSIPTargetMetrics("sip.example.com").ResolvedVia; got != "a" {
		t.Errorf("hostname resolved_via = %q, want a", got)
	}
	for _, literal := range []string{"192.0.2.1", "2001:db8::1", "[2001:db8::1]"} {
		if got := newSIPTargetMetrics(literal).ResolvedVia; got != "literal" {
			t.Errorf("%s resolved_via = %q, want literal", literal, got)
		}
	}
}

func TestParseSIPReasonHeader(t *testing.T) {
	intp := func(n int) *int { return &n }
	tests := []struct {
		name   string
		values []string
		want   *sipReasonHeader
	}{
		{"absent", nil, nil},
		{"q850", []string{`Q.850;cause=17;text="User busy"`}, &sipReasonHeader{Protocol: "Q.850", Cause: intp(17), Text: "User busy"}},
		{"prefers q850 in a list", []string{`SIP;cause=200;text="Call completed elsewhere", Q.850;cause=16`},
			&sipReasonHeader{Protocol: "Q.850", Cause: intp(16)}},
		{"prefers q850 across headers", []string{`SIP;cause=480`, `Q.850 ; cause=18`},
			&sipReasonHeader{Protocol: "Q.850", Cause: intp(18)}},
		{"first entry otherwise", []string{`SIP;cause=487;text="Request Terminated"`},
			&sipReasonHeader{Protocol: "SIP", Cause: intp(487), Text: "Request Terminated"}},
		{"comma inside text", []string{`Q.850;cause=31;text="Normal, unspecified"`},
			&sipReasonHeader{Protocol: "Q.850", Cause: intp(31), Text: "Normal, unspecified"}},
		{"bad cause dropped", []string{`Q.850;cause=abc`}, &sipReasonHeader{Protocol: "Q.850"}},
		{"empty protocol", []string{`;cause=1`}, nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := parseSIPReasonHeader(tt.values)
			gotJSON, _ := json.Marshal(got)
			wantJSON, _ := json.Marshal(tt.want)
			if string(gotJSON) != string(wantJSON) {
				t.Fatalf("parseSIPReasonHeader(%q) = %s, want %s", tt.values, gotJSON, wantJSON)
			}
		})
	}
}

func TestSIPMetricsString(t *testing.T) {
	if got := sipMetricsString("  Example  "); got != "Example" {
		t.Errorf("trim: %q", got)
	}
	long := strings.Repeat("é", 200) // 400 bytes
	got := sipMetricsString(long)
	if !strings.HasSuffix(got, "…") || len(got) > sipMetricsMaxString+len("…") {
		t.Errorf("truncated length %d, want ≤ %d with an ellipsis", len(got), sipMetricsMaxString+len("…"))
	}
	if trimmed := strings.TrimSuffix(got, "…"); !strings.HasPrefix(long, trimmed) || len(trimmed)%2 != 0 {
		t.Errorf("truncation split a rune: %q", trimmed)
	}
}

// A TLS flow records its server certificate under sip.tls, where
// shared/tlscert (and so the alerter and the status page) finds it. It is
// captured under tls_skip_verify too.
func TestSIPMetrics_TLSCertificate(t *testing.T) {
	host, port := startFakeTLSSIPServer(t, time.Now().Add(10*24*time.Hour))

	result := runSIPCheck(t, map[string]interface{}{
		"host": host, "port": port, "transport": "tls", "tls_skip_verify": true,
		"tls_server_name": "sbc.example.com", "tls_min_days_valid": 30,
	})
	// Inside the window the check still passes: the tls_expiry alert fires
	// instead.
	if result.Status != "success" {
		t.Fatalf("Status = %q (%v), want success", result.Status, result.ErrorMessage)
	}
	m, raw := decodeSIPMetrics(t, result)
	if m.TLS == nil || m.TLS.DaysUntilExpiry == nil {
		t.Fatalf("tls = %+v, want certificate info", m.TLS)
	}
	if days := *m.TLS.DaysUntilExpiry; days != 9 && days != 10 {
		t.Errorf("days_until_expiry = %d, want 9 or 10", days)
	}
	if m.TLS.ServerName != "sbc.example.com" || m.TLS.NotAfter == "" || m.TLS.PeerCertificates != 1 {
		t.Errorf("tls = %+v, want the configured server name, not_after and one peer certificate", m.TLS)
	}
	if len(m.AssertionsFailed) != 0 {
		t.Errorf("assertions_failed = %v, want none", m.AssertionsFailed)
	}
	if info := tlscert.Extract(result.MetricsData); info == nil || info.NotAfter != m.TLS.NotAfter {
		t.Errorf("tlscert.Extract = %+v, want the sip.tls certificate", info)
	}
	requireSIPMetricsKeys(t, raw, "tls")

	// Plain transports carry no certificate.
	udpHost, udpPort := startFakeUDPSIPServer(t, func(request string) []string {
		return []string{sipReply(request, 200, "OK")}
	})
	udp := runSIPCheck(t, map[string]interface{}{"host": udpHost, "port": udpPort, "transport": "udp"})
	if m, _ := decodeSIPMetrics(t, udp); m.TLS != nil {
		t.Errorf("udp tls = %+v, want none", m.TLS)
	}
}

// tls_min_days_valid on a check that got an answer but no certificate fails
// it: the threshold could never be evaluated. Validation requires transport
// tls, so a real flow always has one; the rule is pinned directly.
func TestApplySIPCertificateRule(t *testing.T) {
	days := 14
	withRule := &models.SIPMonitorConfig{Host: "sip.example.com", Transport: "tls", TLSMinDaysValid: &days}
	status := 200

	m := &sipMetrics{}
	got := applySIPCertificateRule(withRule, m, CheckResult{Status: "success", HTTPStatus: &status})
	if got.Status != "failure" || got.ErrorMessage == nil || *got.ErrorMessage != "tls: missing certificate info" {
		t.Fatalf("no certificate = %q (%v), want failure tls: missing certificate info", got.Status, got.ErrorMessage)
	}
	if len(m.AssertionsFailed) != 1 || m.AssertionsFailed[0] != "tls: missing certificate info" {
		t.Errorf("assertions_failed = %v", m.AssertionsFailed)
	}

	prior := "unexpected SIP status: got 503, expected 200"
	got = applySIPCertificateRule(withRule, &sipMetrics{}, CheckResult{Status: "failure", ErrorMessage: &prior})
	if got.Status != "failure" || *got.ErrorMessage != prior+"; tls: missing certificate info" {
		t.Errorf("failure without certificate = %q", *got.ErrorMessage)
	}

	// An error result already explains why nothing was captured.
	errMsg := "tls: tls handshake: x509: certificate has expired"
	got = applySIPCertificateRule(withRule, &sipMetrics{}, CheckResult{Status: "error", ErrorMessage: &errMsg})
	if got.Status != "error" || *got.ErrorMessage != errMsg {
		t.Errorf("error result changed to %q (%s)", got.Status, *got.ErrorMessage)
	}

	// A certificate inside the window, or no rule at all, leaves success.
	left := 3
	got = applySIPCertificateRule(withRule, &sipMetrics{TLS: &tlscert.Info{DaysUntilExpiry: &left}}, CheckResult{Status: "success"})
	if got.Status != "success" {
		t.Errorf("certificate inside the window = %q, want success", got.Status)
	}
	got = applySIPCertificateRule(&models.SIPMonitorConfig{Host: "sip.example.com"}, &sipMetrics{}, CheckResult{Status: "success"})
	if got.Status != "success" {
		t.Errorf("no rule = %q, want success", got.Status)
	}
}
