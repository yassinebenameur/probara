package worker

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/yassinebenameur/probara/shared/models"
	"github.com/yassinebenameur/probara/shared/netguard"
)

// fakeSIPResolver answers SRV and A/AAAA from maps and records every query.
// A name missing from a map is NXDOMAIN; one in errs fails with that error,
// alongside its srv records when it has some (as LookupSRV does after
// filtering out invalid targets).
type fakeSIPResolver struct {
	srv  map[string][]*net.SRV // "_sip._udp.example.com"
	ips  map[string][]string   // host → addresses
	errs map[string]error      // query name → error

	mu      sync.Mutex
	queries []string // "SRV _sip._udp.example.com", "A sbc1.example.com"
}

func (f *fakeSIPResolver) record(q string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.queries = append(f.queries, q)
}

func (f *fakeSIPResolver) recorded() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.queries...)
}

func (f *fakeSIPResolver) LookupSRV(_ context.Context, service, proto, name string) (string, []*net.SRV, error) {
	q := fmt.Sprintf("_%s._%s.%s", service, proto, name)
	f.record("SRV " + q)
	if err := f.errs[q]; err != nil {
		return "", f.srv[q], err
	}
	records, ok := f.srv[q]
	if !ok {
		return "", nil, &net.DNSError{Err: "no such host", Name: q, IsNotFound: true}
	}
	return q + ".", records, nil
}

func (f *fakeSIPResolver) LookupIPAddr(_ context.Context, host string) ([]net.IPAddr, error) {
	f.record("A " + host)
	if err := f.errs[host]; err != nil {
		return nil, err
	}
	addrs, ok := f.ips[host]
	if !ok {
		return nil, &net.DNSError{Err: "no such host", Name: host, IsNotFound: true}
	}
	out := make([]net.IPAddr, 0, len(addrs))
	for _, a := range addrs {
		out = append(out, net.IPAddr{IP: net.ParseIP(a)})
	}
	return out, nil
}

func srvRecord(target string, port, priority, weight int) *net.SRV {
	return &net.SRV{Target: target + ".", Port: uint16(port), Priority: uint16(priority), Weight: uint16(weight)}
}

func candidateAddrs(candidates []sipCandidate) []string {
	out := make([]string, 0, len(candidates))
	for _, c := range candidates {
		out = append(out, fmt.Sprintf("%s %s %s/%s", c.via, c.host, c.address(), c.transport))
	}
	return out
}

func requireStrings(t *testing.T, what string, got, want []string) {
	t.Helper()
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("%s:\n got  %q\n want %q", what, got, want)
	}
}

func TestResolveSIPTargets(t *testing.T) {
	t.Setenv("SIP_LOCALHOST_AS_HOST_GATEWAY", "")
	resolver := func() *fakeSIPResolver {
		return &fakeSIPResolver{
			srv: map[string][]*net.SRV{
				// Out of order on purpose: priority decides.
				"_sip._tcp.example.com": {
					srvRecord("sbc2.example.com", 5070, 20, 0),
					srvRecord("sbc1.example.com", 5060, 10, 0),
				},
				"_sips._tcp.example.com": {srvRecord("tls.example.com", 5061, 10, 0)},
				"_sip._udp.multi.example": {
					srvRecord("a.multi.example", 5060, 10, 0),
					srvRecord("b.multi.example", 5060, 20, 0),
					srvRecord("c.multi.example", 5060, 30, 0),
				},
			},
			ips: map[string][]string{
				"example.com":      {"192.0.2.1", "2001:db8::1"},
				"other.example":    {"192.0.2.9"},
				"sbc1.example.com": {"192.0.2.11"},
				"sbc2.example.com": {"192.0.2.12"},
				"tls.example.com":  {"192.0.2.13"},
				"a.multi.example":  {"192.0.2.21", "192.0.2.22", "192.0.2.23"},
				"b.multi.example":  {"192.0.2.31", "192.0.2.32"},
				"c.multi.example":  {"192.0.2.41"},
			},
		}
	}

	tests := []struct {
		name        string
		config      models.SIPMonitorConfig
		want        []string
		wantQueries []string
		wantSRV     int
	}{
		{
			name:        "a mode resolves the host at the default port, never SRV",
			config:      models.SIPMonitorConfig{Host: "example.com"},
			want:        []string{"a example.com 192.0.2.1:5060/udp", "a example.com [2001:db8::1]:5060/udp"},
			wantQueries: []string{"A example.com"},
		},
		{
			name:        "a mode tls defaults to 5061",
			config:      models.SIPMonitorConfig{Host: "example.com", Transport: "TLS"},
			want:        []string{"a example.com 192.0.2.1:5061/tls", "a example.com [2001:db8::1]:5061/tls"},
			wantQueries: []string{"A example.com"},
		},
		{
			name:   "a mode ip literal is used as is",
			config: models.SIPMonitorConfig{Host: "198.51.100.7", Port: 5080, Transport: "tcp"},
			want:   []string{"literal 198.51.100.7 198.51.100.7:5080/tcp"},
		},
		{
			name:        "rfc3263 explicit port skips SRV",
			config:      models.SIPMonitorConfig{Host: "example.com", Port: 5080, Resolution: "rfc3263", Transport: "tcp"},
			want:        []string{"a example.com 192.0.2.1:5080/tcp", "a example.com [2001:db8::1]:5080/tcp"},
			wantQueries: []string{"A example.com"},
		},
		{
			name:   "rfc3263 ip literal skips every lookup",
			config: models.SIPMonitorConfig{Host: "2001:db8::7", Resolution: "rfc3263"},
			want:   []string{"literal 2001:db8::7 [2001:db8::7]:5060/udp"},
		},
		{
			name:        "rfc3263 tcp uses _sip._tcp in priority order",
			config:      models.SIPMonitorConfig{Host: "example.com", Resolution: "rfc3263", Transport: "tcp"},
			want:        []string{"srv sbc1.example.com 192.0.2.11:5060/tcp", "srv sbc2.example.com 192.0.2.12:5070/tcp"},
			wantQueries: []string{"SRV _sip._tcp.example.com", "A sbc1.example.com", "A sbc2.example.com"},
			wantSRV:     2,
		},
		{
			name:        "rfc3263 tls uses _sips._tcp",
			config:      models.SIPMonitorConfig{Host: "example.com", Resolution: "rfc3263", Transport: "tls"},
			want:        []string{"srv tls.example.com 192.0.2.13:5061/tls"},
			wantQueries: []string{"SRV _sips._tcp.example.com", "A tls.example.com"},
			wantSRV:     1,
		},
		{
			name:        "rfc3263 auto transport takes the first transport the domain publishes",
			config:      models.SIPMonitorConfig{Host: "example.com", Resolution: "rfc3263"},
			want:        []string{"srv sbc1.example.com 192.0.2.11:5060/tcp", "srv sbc2.example.com 192.0.2.12:5070/tcp"},
			wantQueries: []string{"SRV _sip._udp.example.com", "SRV _sip._tcp.example.com", "A sbc1.example.com", "A sbc2.example.com"},
			wantSRV:     2,
		},
		{
			name:   "rfc3263 without SRV falls back to A at the default port over udp",
			config: models.SIPMonitorConfig{Host: "other.example", Resolution: "rfc3263"},
			want:   []string{"a other.example 192.0.2.9:5060/udp"},
			wantQueries: []string{
				"SRV _sip._udp.other.example", "SRV _sip._tcp.other.example", "SRV _sips._tcp.other.example", "A other.example",
			},
		},
		{
			name:        "rfc3263 explicit tls without SRV falls back to 5061",
			config:      models.SIPMonitorConfig{Host: "other.example", Resolution: "rfc3263", Transport: "tls"},
			want:        []string{"a other.example 192.0.2.9:5061/tls"},
			wantQueries: []string{"SRV _sips._tcp.other.example", "A other.example"},
		},
		{
			name:   "two addresses per SRV target, four in all",
			config: models.SIPMonitorConfig{Host: "multi.example", Resolution: "rfc3263", Transport: "udp"},
			want: []string{
				"srv a.multi.example 192.0.2.21:5060/udp", "srv a.multi.example 192.0.2.22:5060/udp",
				"srv b.multi.example 192.0.2.31:5060/udp", "srv b.multi.example 192.0.2.32:5060/udp",
			},
			wantQueries: []string{"SRV _sip._udp.multi.example", "A a.multi.example", "A b.multi.example"},
			wantSRV:     3,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := resolver()
			candidates, res, err := resolveSIPTargets(context.Background(), &tt.config, r)
			if err != nil {
				t.Fatalf("resolveSIPTargets() error = %v", err)
			}
			requireStrings(t, "candidates", candidateAddrs(candidates), tt.want)
			requireStrings(t, "queries", r.recorded(), tt.wantQueries)
			if len(res.SRV) != tt.wantSRV {
				t.Errorf("srv metrics = %+v, want %d records", res.SRV, tt.wantSRV)
			}
			if res.Mode != tt.config.ResolutionMode() {
				t.Errorf("mode = %q, want %q", res.Mode, tt.config.ResolutionMode())
			}
			if res.lookedUp != (len(tt.wantQueries) > 0) {
				t.Errorf("lookedUp = %v with queries %v", res.lookedUp, tt.wantQueries)
			}
		})
	}
}

func TestResolveSIPTargets_Failures(t *testing.T) {
	t.Setenv("SIP_LOCALHOST_AS_HOST_GATEWAY", "")
	rfc := func(host string) *models.SIPMonitorConfig {
		return &models.SIPMonitorConfig{Host: host, Resolution: "rfc3263", Transport: "udp"}
	}

	t.Run("an SRV server failure is a dns error, not a silent fallback to A", func(t *testing.T) {
		r := &fakeSIPResolver{
			errs: map[string]error{"_sip._udp.example.com": &net.DNSError{Err: "server misbehaving", Name: "example.com", IsTemporary: true}},
			ips:  map[string][]string{"example.com": {"192.0.2.1"}},
		}
		_, _, err := resolveSIPTargets(context.Background(), rfc("example.com"), r)
		if err == nil || classifySIPError(err) != "dns" {
			t.Fatalf("err = %v (%s), want a dns error", err, classifySIPError(err))
		}
	})

	t.Run("records returned alongside an error are still used", func(t *testing.T) {
		// LookupSRV filters out records with invalid target names and
		// returns the rest with an error.
		r := &fakeSIPResolver{
			srv:  map[string][]*net.SRV{"_sip._udp.example.com": {srvRecord("sbc1.example.com", 5070, 10, 0)}},
			errs: map[string]error{"_sip._udp.example.com": &net.DNSError{Err: "SRV header name is invalid", Name: "example.com"}},
			ips:  map[string][]string{"sbc1.example.com": {"192.0.2.11"}},
		}
		candidates, _, err := resolveSIPTargets(context.Background(), rfc("example.com"), r)
		if err != nil {
			t.Fatalf("resolveSIPTargets() error = %v, want the remaining record used", err)
		}
		requireStrings(t, "candidates", candidateAddrs(candidates), []string{"srv sbc1.example.com 192.0.2.11:5070/udp"})
	})

	t.Run("a lone '.' target means the service is not offered", func(t *testing.T) {
		r := &fakeSIPResolver{srv: map[string][]*net.SRV{"_sip._udp.example.com": {{Target: ".", Port: 0}}}}
		_, _, err := resolveSIPTargets(context.Background(), rfc("example.com"), r)
		if err == nil || classifySIPError(err) != "dns" || !strings.Contains(err.Error(), "not offered") {
			t.Fatalf("err = %v, want a dns error saying the service is not offered", err)
		}
	})

	t.Run("an unresolvable SRV target is recorded and skipped", func(t *testing.T) {
		r := &fakeSIPResolver{
			srv: map[string][]*net.SRV{"_sip._udp.example.com": {
				srvRecord("gone.example.com", 5060, 10, 0),
				srvRecord("sbc2.example.com", 5062, 20, 0),
			}},
			ips: map[string][]string{"sbc2.example.com": {"192.0.2.12"}},
		}
		candidates, res, err := resolveSIPTargets(context.Background(), rfc("example.com"), r)
		if err != nil {
			t.Fatalf("resolveSIPTargets() error = %v", err)
		}
		requireStrings(t, "candidates", candidateAddrs(candidates), []string{"srv sbc2.example.com 192.0.2.12:5062/udp"})
		if len(res.Attempts) != 1 || res.Attempts[0].Outcome != "dns" || res.Attempts[0].Address != "gone.example.com:5060" {
			t.Fatalf("attempts = %+v, want one dns attempt for gone.example.com:5060", res.Attempts)
		}
	})

	t.Run("no SRV target resolves", func(t *testing.T) {
		r := &fakeSIPResolver{srv: map[string][]*net.SRV{"_sip._udp.example.com": {srvRecord("gone.example.com", 5060, 10, 0)}}}
		_, res, err := resolveSIPTargets(context.Background(), rfc("example.com"), r)
		if err == nil || classifySIPError(err) != "dns" {
			t.Fatalf("err = %v, want a dns error", err)
		}
		if len(res.SRV) != 1 {
			t.Fatalf("srv = %+v, want the record DNS returned, for the envelope", res.SRV)
		}
	})

	t.Run("a hostname that does not resolve", func(t *testing.T) {
		_, _, err := resolveSIPTargets(context.Background(), &models.SIPMonitorConfig{Host: "nowhere.example"}, &fakeSIPResolver{})
		if err == nil || classifySIPError(err) != "dns" {
			t.Fatalf("err = %v, want a dns error", err)
		}
	})
}

// SIP_LOCALHOST_AS_HOST_GATEWAY rewrites each target host before its A/AAAA
// lookup: an SRV target of localhost becomes host.docker.internal too.
func TestResolveSIPTargets_RewritePerTarget(t *testing.T) {
	t.Setenv("SIP_LOCALHOST_AS_HOST_GATEWAY", "true")
	r := &fakeSIPResolver{
		srv: map[string][]*net.SRV{"_sip._tcp.example.com": {
			srvRecord("localhost", 5060, 10, 0),
			srvRecord("127.0.0.1", 5062, 20, 0),
		}},
		ips: map[string][]string{"host.docker.internal": {"192.168.65.254"}},
	}
	config := &models.SIPMonitorConfig{Host: "example.com", Resolution: "rfc3263", Transport: "tcp"}
	candidates, _, err := resolveSIPTargets(context.Background(), config, r)
	if err != nil {
		t.Fatalf("resolveSIPTargets() error = %v", err)
	}
	requireStrings(t, "candidates", candidateAddrs(candidates), []string{
		"srv host.docker.internal 192.168.65.254:5060/tcp",
		"srv host.docker.internal 192.168.65.254:5062/tcp",
	})
	for _, q := range r.recorded() {
		if q == "A localhost" || q == "A 127.0.0.1" {
			t.Fatalf("queried %q: the rewrite must apply before the lookup", q)
		}
	}
}

func TestOrderSRVRecords(t *testing.T) {
	records := []*net.SRV{
		srvRecord("c", 1, 30, 0),
		srvRecord("a1", 1, 10, 0),
		srvRecord("b", 1, 20, 5),
		srvRecord("a2", 1, 10, 0),
	}
	var got []string
	for _, rec := range orderSRVRecords(records) {
		got = append(got, strings.TrimSuffix(rec.Target, "."))
	}
	requireStrings(t, "order", got, []string{"a1", "a2", "b", "c"})

	// Weighted selection: a weight-0 record shares its priority with a
	// heavy one, so it is almost always tried second; both always appear.
	firstHeavy := 0
	for range 200 {
		ordered := orderSRVRecords([]*net.SRV{srvRecord("light", 1, 10, 0), srvRecord("heavy", 1, 10, 1000)})
		if len(ordered) != 2 {
			t.Fatalf("ordered %d records, want 2", len(ordered))
		}
		if ordered[0].Target == "heavy." {
			firstHeavy++
		}
	}
	if firstHeavy < 180 {
		t.Fatalf("heavy record first %d/200 times, want nearly always", firstHeavy)
	}
}

func TestSIPFailoverTarget(t *testing.T) {
	candidates := []sipCandidate{
		{host: "sbc1.example.com", ip: net.ParseIP("192.0.2.1")},
		{host: "sbc1.example.com", ip: net.ParseIP("192.0.2.2")},
		{host: "sbc2.example.com", ip: net.ParseIP("192.0.2.3")},
	}
	refused := &sipDialError{err: &net.OpError{Op: "dial", Err: syscall.ECONNREFUSED}}
	handshake := &sipDialError{handshake: true, err: errors.New("remote error: tls: handshake failure")}
	noResponse := &sipNoResponseError{method: "OPTIONS"}
	connectTimeout := &sipDialError{err: &net.OpError{Op: "dial", Net: "tcp", Err: os.ErrDeadlineExceeded}}
	handshakeTimeout := &sipDialError{handshake: true, err: os.ErrDeadlineExceeded}
	blocked := &sipDialError{err: fmt.Errorf("%w: ip 10.0.0.1 not allowed", netguard.ErrBlocked)}
	resp := func(code int) *sipMessage { return &sipMessage{StatusCode: code} }

	tests := []struct {
		name    string
		mode    string
		attempt sipAttemptResult
		from    int
		want    int
	}{
		{"refused moves on", "a", sipAttemptResult{err: refused}, 0, 1},
		{"ssrf_blocked moves on", "rfc3263", sipAttemptResult{err: blocked}, 0, 1},
		{"tls skips the rest of the same host", "rfc3263", sipAttemptResult{err: handshake}, 0, 2},
		{"tls with no other host stops", "rfc3263", sipAttemptResult{err: handshake}, 1, 2},
		{"tls at the last host stops", "rfc3263", sipAttemptResult{err: handshake}, 2, -1},
		{"no response moves on under rfc3263", "rfc3263", sipAttemptResult{err: noResponse}, 0, 1},
		{"no response stops under a", "a", sipAttemptResult{err: noResponse}, 0, -1},
		{"a connect timeout moves on under a", "a", sipAttemptResult{err: connectTimeout}, 0, 1},
		{"a handshake timeout stops under a", "a", sipAttemptResult{err: handshakeTimeout}, 0, -1},
		{"a handshake timeout moves on under rfc3263", "rfc3263", sipAttemptResult{err: handshakeTimeout}, 0, 1},
		{"503 moves on under rfc3263", "rfc3263", sipAttemptResult{resp: resp(503)}, 0, 1},
		{"503 stops under a", "a", sipAttemptResult{resp: resp(503)}, 0, -1},
		{"any other final stops", "rfc3263", sipAttemptResult{resp: resp(404)}, 0, -1},
		{"a loop stops", "rfc3263", sipAttemptResult{err: &sipLoopError{method: "OPTIONS"}}, 0, -1},
		{"nothing after the last address", "rfc3263", sipAttemptResult{err: refused}, 2, -1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := sipFailoverTarget(context.Background(), tt.mode, tt.attempt, candidates, tt.from); got != tt.want {
				t.Fatalf("sipFailoverTarget() = %d, want %d", got, tt.want)
			}
		})
	}

	t.Run("a spent check budget stops", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		if got := sipFailoverTarget(ctx, "rfc3263", sipAttemptResult{err: refused}, candidates, 0); got != -1 {
			t.Fatalf("sipFailoverTarget() = %d, want -1", got)
		}
	})
}

// --- checker-level resolution and failover, against in-process servers ---

// runSIPCheckWith runs a check with a fake resolver and the given policy.
func runSIPCheckWith(t *testing.T, checker *SIPChecker, config map[string]interface{}, timeoutSeconds int) CheckResult {
	t.Helper()
	raw, err := json.Marshal(config)
	if err != nil {
		t.Fatalf("marshal config: %v", err)
	}
	return checker.Check(context.Background(), raw, timeoutSeconds)
}

func closedTCPPort(t *testing.T) int {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("Listen() error = %v", err)
	}
	_, port := splitTestHostPort(t, listener.Addr().String())
	listener.Close()
	return port
}

func startOKTCPSIPServer(t *testing.T, seen chan<- string) int {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("Listen() error = %v", err)
	}
	startFakeStreamSIPServer(t, listener, func(request string) []string {
		if seen != nil {
			select {
			case seen <- request:
			default:
			}
		}
		return []string{sipReply(request, 200, "OK")}
	})
	_, port := splitTestHostPort(t, listener.Addr().String())
	return port
}

func requireAttemptOutcomes(t *testing.T, m *sipMetrics, want ...string) {
	t.Helper()
	if m.Resolution == nil {
		t.Fatalf("no resolution metrics")
	}
	var got []string
	for _, a := range m.Resolution.Attempts {
		got = append(got, a.Outcome)
	}
	requireStrings(t, "attempt outcomes", got, want)
}

// A dead SRV target (connection refused) fails over to the next one; the
// envelope names the target that answered, marks the failover and lists
// both attempts. The Request-URI is the domain, not the SRV target.
func TestSIPChecker_SRVFailoverDeadToLive(t *testing.T) {
	t.Setenv("SIP_LOCALHOST_AS_HOST_GATEWAY", "")
	seen := make(chan string, 1)
	dead := closedTCPPort(t)
	live := startOKTCPSIPServer(t, seen)
	checker := &SIPChecker{resolver: &fakeSIPResolver{
		srv: map[string][]*net.SRV{"_sip._tcp.example.com": {
			srvRecord("sbc1.example.com", dead, 10, 0),
			srvRecord("sbc2.example.com", live, 20, 0),
		}},
		ips: map[string][]string{"sbc1.example.com": {"127.0.0.1"}, "sbc2.example.com": {"127.0.0.1"}},
	}}

	result := runSIPCheckWith(t, checker, map[string]interface{}{"host": "example.com", "resolution": "rfc3263", "transport": "tcp"}, 5)
	if result.Status != "success" {
		t.Fatalf("Status = %q (%v), want success", result.Status, result.ErrorMessage)
	}
	m, raw := decodeSIPMetrics(t, result)
	requireAttemptOutcomes(t, m, "refused", "ok")
	if m.Target == nil || m.Target.Host != "sbc2.example.com" || m.Target.ResolvedVia != "srv" || !m.Target.Failover ||
		m.Target.Address != fmt.Sprintf("127.0.0.1:%d", live) {
		t.Errorf("target = %+v, want sbc2.example.com via srv after failover at 127.0.0.1:%d", m.Target, live)
	}
	if m.Resolution.Mode != "rfc3263" || len(m.Resolution.SRV) != 2 || m.Resolution.SRV[0].Target != "sbc1.example.com" {
		t.Errorf("resolution = %+v, want rfc3263 with both SRV records", m.Resolution)
	}
	if m.Timing.DNSMs == nil {
		t.Errorf("timing = %+v, want dns_ms", m.Timing)
	}
	if timing, _ := raw["timing"].(map[string]any); timing["dns_ms"] == nil {
		t.Errorf("timing = %v, want a dns_ms key", raw["timing"])
	}
	if m.RequestURI != "sip:example.com" {
		t.Errorf("request_uri = %q, want sip:example.com", m.RequestURI)
	}
	request := <-seen
	if !strings.HasPrefix(request, "OPTIONS sip:example.com SIP/2.0\r\n") || sipTestHeader(request, "To") != "<sip:probe@example.com>" {
		t.Errorf("request = %q, want OPTIONS to sip:example.com", request)
	}
}

// A 503 from the first target is RFC 3263's signal to try the next.
func TestSIPChecker_SRVFailoverOn503(t *testing.T) {
	t.Setenv("SIP_LOCALHOST_AS_HOST_GATEWAY", "")
	_, busy := startFakeUDPSIPServer(t, func(request string) []string {
		return []string{sipReply(request, 503, "Service Unavailable")}
	})
	_, ok := startFakeUDPSIPServer(t, func(request string) []string {
		return []string{sipReply(request, 200, "OK")}
	})
	checker := &SIPChecker{resolver: &fakeSIPResolver{
		srv: map[string][]*net.SRV{"_sip._udp.example.com": {
			srvRecord("sbc1.example.com", busy, 10, 0),
			srvRecord("sbc2.example.com", ok, 20, 0),
		}},
		ips: map[string][]string{"sbc1.example.com": {"127.0.0.1"}, "sbc2.example.com": {"127.0.0.1"}},
	}}
	result := runSIPCheckWith(t, checker, map[string]interface{}{"host": "example.com", "resolution": "rfc3263"}, 5)
	if result.Status != "success" {
		t.Fatalf("Status = %q (%v), want success", result.Status, result.ErrorMessage)
	}
	m, _ := decodeSIPMetrics(t, result)
	requireAttemptOutcomes(t, m, "503", "ok")
	if m.Transport != "udp" || m.StatusCode != 200 {
		t.Errorf("transport %q status %d, want udp 200 from the second target", m.Transport, m.StatusCode)
	}

	// Under "a" a 503 is the answer: no failover, as before RFC 3263.
	_, only := startFakeUDPSIPServer(t, func(request string) []string {
		return []string{sipReply(request, 503, "Service Unavailable")}
	})
	failed := runSIPCheckWith(t, &SIPChecker{}, map[string]interface{}{"host": "127.0.0.1", "port": only}, 5)
	if failed.Status != "failure" || failed.HTTPStatus == nil || *failed.HTTPStatus != 503 {
		t.Fatalf("a mode: Status = %q (%v), want failure 503", failed.Status, failed.ErrorMessage)
	}
}

// Under rfc3263 a silent target gets its share of the budget, then the next
// target is tried.
func TestSIPChecker_SRVFailoverOnSilence(t *testing.T) {
	t.Setenv("SIP_LOCALHOST_AS_HOST_GATEWAY", "")
	_, silent := startFakeUDPSIPServer(t, func(string) []string { return nil })
	_, ok := startFakeUDPSIPServer(t, func(request string) []string {
		return []string{sipReply(request, 200, "OK")}
	})
	checker := &SIPChecker{resolver: &fakeSIPResolver{
		srv: map[string][]*net.SRV{"_sip._udp.example.com": {
			srvRecord("sbc1.example.com", silent, 10, 0),
			srvRecord("sbc2.example.com", ok, 20, 0),
		}},
		ips: map[string][]string{"sbc1.example.com": {"127.0.0.1"}, "sbc2.example.com": {"127.0.0.1"}},
	}}
	start := time.Now()
	result := runSIPCheckWith(t, checker, map[string]interface{}{"host": "example.com", "resolution": "rfc3263", "transport": "udp"}, 4)
	if result.Status != "success" {
		t.Fatalf("Status = %q (%v), want success", result.Status, result.ErrorMessage)
	}
	if elapsed := time.Since(start); elapsed < 1500*time.Millisecond || elapsed > 3500*time.Millisecond {
		t.Errorf("check took %s, want about half the 4s budget on the silent target", elapsed)
	}
	m, _ := decodeSIPMetrics(t, result)
	requireAttemptOutcomes(t, m, "timeout", "ok")
	if m.Retransmissions < 1 {
		t.Errorf("retransmissions = %d, want the silent target's resends counted", m.Retransmissions)
	}
}

// An address the network policy refuses is skipped like a dead one.
func TestSIPChecker_SRVSkipsBlockedTarget(t *testing.T) {
	t.Setenv("SIP_LOCALHOST_AS_HOST_GATEWAY", "")
	_, ok := startFakeUDPSIPServer(t, func(request string) []string {
		return []string{sipReply(request, 200, "OK")}
	})
	_, loopback, _ := net.ParseCIDR("127.0.0.1/32")
	checker := &SIPChecker{
		blockPrivateIPs: true,
		allowedCIDRs:    []*net.IPNet{loopback},
		resolver: &fakeSIPResolver{
			srv: map[string][]*net.SRV{"_sip._udp.example.com": {
				srvRecord("internal.example.com", ok, 10, 0),
				srvRecord("sbc2.example.com", ok, 20, 0),
			}},
			ips: map[string][]string{"internal.example.com": {"10.0.0.5"}, "sbc2.example.com": {"127.0.0.1"}},
		},
	}
	result := runSIPCheckWith(t, checker, map[string]interface{}{"host": "example.com", "resolution": "rfc3263", "transport": "udp"}, 5)
	if result.Status != "success" {
		t.Fatalf("Status = %q (%v), want success", result.Status, result.ErrorMessage)
	}
	m, _ := decodeSIPMetrics(t, result)
	requireAttemptOutcomes(t, m, "ssrf_blocked", "ok")

	// Every address blocked: the check is an ssrf_blocked error.
	blocked := &SIPChecker{blockPrivateIPs: true, resolver: checker.resolver}
	result = runSIPCheckWith(t, blocked, map[string]interface{}{"host": "example.com", "resolution": "rfc3263", "transport": "udp"}, 5)
	requireSIPError(t, result, "ssrf_blocked")
}

// A TLS failure moves to another host, and the TLS identity stays the
// configured domain at every SRV target (RFC 5922), never the target name.
func TestSIPChecker_SRVTLSFailoverAndIdentity(t *testing.T) {
	t.Setenv("SIP_LOCALHOST_AS_HOST_GATEWAY", "")
	// sbc1 speaks plaintext: the handshake fails.
	plain, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("Listen() error = %v", err)
	}
	t.Cleanup(func() { plain.Close() })
	go func() {
		for {
			conn, err := plain.Accept()
			if err != nil {
				return
			}
			go func(conn net.Conn) {
				defer conn.Close()
				_, _ = conn.Write([]byte(sipResponseMsg(400, "Bad Request")))
				_, _ = io.Copy(io.Discard, conn)
			}(conn)
		}
	}()
	_, plainPort := splitTestHostPort(t, plain.Addr().String())

	// sbc2 records the SNI each client sent.
	cert := newSelfSignedTestCert(t, time.Now().Add(time.Hour))
	sni := make(chan string, 4)
	secure, err := tls.Listen("tcp", "127.0.0.1:0", &tls.Config{
		GetCertificate: func(hello *tls.ClientHelloInfo) (*tls.Certificate, error) {
			sni <- hello.ServerName
			return &cert, nil
		},
	})
	if err != nil {
		t.Fatalf("tls.Listen() error = %v", err)
	}
	startFakeStreamSIPServer(t, secure, func(request string) []string {
		return []string{sipReply(request, 200, "OK")}
	})
	_, securePort := splitTestHostPort(t, secure.Addr().String())

	checker := &SIPChecker{resolver: &fakeSIPResolver{
		srv: map[string][]*net.SRV{"_sips._tcp.example.com": {
			srvRecord("sbc1.example.com", plainPort, 10, 0),
			srvRecord("sbc2.example.com", securePort, 20, 0),
		}},
		ips: map[string][]string{"sbc1.example.com": {"127.0.0.1"}, "sbc2.example.com": {"127.0.0.1"}},
	}}
	result := runSIPCheckWith(t, checker, map[string]interface{}{
		"host": "example.com", "resolution": "rfc3263", "transport": "tls", "tls_skip_verify": true,
	}, 5)
	if result.Status != "success" {
		t.Fatalf("Status = %q (%v), want success", result.Status, result.ErrorMessage)
	}
	m, _ := decodeSIPMetrics(t, result)
	requireAttemptOutcomes(t, m, "tls", "ok")
	if got := <-sni; got != "example.com" {
		t.Errorf("SNI = %q, want the domain example.com, not the SRV target", got)
	}
	if m.TLS == nil || m.TLS.ServerName != "example.com" {
		t.Errorf("tls = %+v, want server_name example.com", m.TLS)
	}
}

// A certificate that names the SRV target but not the domain fails
// verification; the error says which name was checked and how to override it.
func TestWithSIPSRVIdentityHint(t *testing.T) {
	cert := &x509.Certificate{DNSNames: []string{"sbc1.example.com"}}
	mismatch := &sipDialError{handshake: true, err: &tls.CertificateVerificationError{
		Err: x509.HostnameError{Certificate: cert, Host: "example.com"},
	}}
	srv := sipCandidate{host: "sbc1.example.com", via: "srv"}

	err := withSIPSRVIdentityHint(mismatch, srv, "example.com", false)
	if classifySIPError(err) != "tls" {
		t.Fatalf("classified %q, want tls", classifySIPError(err))
	}
	for _, want := range []string{"tls handshake: ", "checked against example.com", "not SRV target sbc1.example.com", "set tls_server_name"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q lacks %q", err, want)
		}
	}
	if strings.Contains(err.Error(), "; ") {
		t.Errorf("error %q contains \"; \", the assertion separator", err)
	}

	// No hint off SRV, with an override, or for another TLS failure.
	if got := withSIPSRVIdentityHint(mismatch, sipCandidate{host: "example.com", via: "a"}, "example.com", false); got != error(mismatch) {
		t.Errorf("a-record target got a hint: %v", got)
	}
	if got := withSIPSRVIdentityHint(mismatch, srv, "example.com", true); got != error(mismatch) {
		t.Errorf("tls_server_name override got a hint: %v", got)
	}
	unknown := &sipDialError{handshake: true, err: x509.UnknownAuthorityError{}}
	if got := withSIPSRVIdentityHint(unknown, srv, "example.com", false); got != error(unknown) {
		t.Errorf("unknown authority got a hint: %v", got)
	}
}

// The check rewrites SRV targets too: a lab SRV record pointing at
// localhost reaches the host gateway when the rewrite is on.
func TestSIPChecker_SRVTargetRewrite(t *testing.T) {
	t.Setenv("SIP_LOCALHOST_AS_HOST_GATEWAY", "true")
	port := startOKTCPSIPServer(t, nil)
	r := &fakeSIPResolver{
		srv: map[string][]*net.SRV{"_sip._tcp.example.com": {srvRecord("localhost", port, 10, 0)}},
		// The fake gateway is the test's own loopback.
		ips: map[string][]string{"host.docker.internal": {"127.0.0.1"}},
	}
	result := runSIPCheckWith(t, &SIPChecker{resolver: r}, map[string]interface{}{"host": "example.com", "resolution": "rfc3263", "transport": "tcp"}, 5)
	if result.Status != "success" {
		t.Fatalf("Status = %q (%v), want success", result.Status, result.ErrorMessage)
	}
	m, _ := decodeSIPMetrics(t, result)
	if m.Target == nil || m.Target.Host != "host.docker.internal" || m.Target.ResolvedVia != "srv" {
		t.Errorf("target = %+v, want the rewritten SRV target", m.Target)
	}
}

// "a" mode is unchanged: the Request-URI keeps host:port, the envelope
// records the mode and one attempt, and an IP literal needs no DNS.
func TestSIPChecker_AModeUnchanged(t *testing.T) {
	t.Setenv("SIP_LOCALHOST_AS_HOST_GATEWAY", "")
	seen := make(chan string, 1)
	port := startOKTCPSIPServer(t, seen)
	result := runSIPCheckWith(t, &SIPChecker{}, map[string]interface{}{"host": "127.0.0.1", "port": port, "transport": "tcp"}, 5)
	if result.Status != "success" {
		t.Fatalf("Status = %q (%v), want success", result.Status, result.ErrorMessage)
	}
	m, _ := decodeSIPMetrics(t, result)
	if m.Resolution == nil || m.Resolution.Mode != "a" || len(m.Resolution.SRV) != 0 {
		t.Errorf("resolution = %+v, want mode a without SRV", m.Resolution)
	}
	requireAttemptOutcomes(t, m, "ok")
	if m.Timing.DNSMs != nil || m.Target.Failover {
		t.Errorf("dns_ms %v failover %v, want neither for an IP literal", m.Timing.DNSMs, m.Target.Failover)
	}
	want := fmt.Sprintf("OPTIONS sip:127.0.0.1:%d SIP/2.0\r\n", port)
	if request := <-seen; !strings.HasPrefix(request, want) {
		t.Errorf("request = %q, want %q", request, want)
	}
}

// blackholeDial stands in for an address that drops SYNs: the dial to
// addr hangs until its context ends, then fails as net.Dialer's does. Every
// other address is dialed through dialGuard. The deadline it saw is sent on
// deadlines.
func blackholeDial(addr string, deadlines chan<- time.Duration) func(context.Context, *dialGuard, string, string) (net.Conn, error) {
	return func(ctx context.Context, guard *dialGuard, network, address string) (net.Conn, error) {
		if address != addr {
			return guard.DialContext(ctx, network, address)
		}
		if deadline, ok := ctx.Deadline(); ok {
			deadlines <- time.Until(deadline)
		}
		<-ctx.Done()
		return nil, &net.OpError{Op: "dial", Net: network, Err: os.ErrDeadlineExceeded}
	}
}

// Under "a" a hostname's address that never completes a TCP connect gets
// its share of the budget (what is left over the addresses left, at least
// 2 s), as net.Dialer gave it when the hostname was dialed directly, then the
// next address is tried: one SBC down behind a SYN-dropping firewall does not
// take the monitor down.
func TestSIPChecker_AModeConnectFailover(t *testing.T) {
	t.Setenv("SIP_LOCALHOST_AS_HOST_GATEWAY", "")
	for _, transport := range []string{"tcp", "tls"} {
		t.Run(transport, func(t *testing.T) {
			var port int
			if transport == "tcp" {
				port = startOKTCPSIPServer(t, nil)
			} else {
				listener, err := tls.Listen("tcp", "127.0.0.1:0", &tls.Config{Certificates: []tls.Certificate{newSelfSignedTestCert(t, time.Now().Add(30*24*time.Hour))}})
				if err != nil {
					t.Fatalf("tls.Listen() error = %v", err)
				}
				startFakeStreamSIPServer(t, listener, func(request string) []string {
					return []string{sipReply(request, 200, "OK")}
				})
				_, port = splitTestHostPort(t, listener.Addr().String())
			}
			deadlines := make(chan time.Duration, 1)
			checker := &SIPChecker{
				resolver: &fakeSIPResolver{ips: map[string][]string{"sip.example.com": {"192.0.2.1", "127.0.0.1"}}},
				testDial: blackholeDial(fmt.Sprintf("192.0.2.1:%d", port), deadlines),
			}
			start := time.Now()
			result := runSIPCheckWith(t, checker, map[string]interface{}{
				"host": "sip.example.com", "port": port, "transport": transport, "tls_skip_verify": true,
			}, 6)
			if result.Status != "success" {
				t.Fatalf("Status = %q (%v), want success via the second address", result.Status, result.ErrorMessage)
			}
			if got := <-deadlines; got < 2500*time.Millisecond || got > 3*time.Second {
				t.Errorf("connect budget = %s, want half the 6s check", got)
			}
			if elapsed := time.Since(start); elapsed > 4500*time.Millisecond {
				t.Errorf("check took %s, want the black hole given up on after its share", elapsed)
			}
			m, _ := decodeSIPMetrics(t, result)
			requireAttemptOutcomes(t, m, "timeout", "ok")
			if !m.Target.Failover || m.Target.Address != fmt.Sprintf("127.0.0.1:%d", port) {
				t.Errorf("target = %+v, want failover to 127.0.0.1:%d", m.Target, port)
			}
		})
	}

	// The last address keeps the whole budget, and UDP is never split: its
	// connect sends nothing, so there is nothing to time out.
	t.Run("budget", func(t *testing.T) {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		tcp := []sipCandidate{{transport: "tcp"}, {transport: "tcp"}, {transport: "tcp"}}
		if got := sipConnectBudget(ctx, "a", tcp, 0); got < 3*time.Second || got > 3400*time.Millisecond {
			t.Errorf("first of three = %s, want a third of 10s", got)
		}
		if got := sipConnectBudget(ctx, "a", tcp, 2); got != 0 {
			t.Errorf("last = %s, want 0 (whole budget)", got)
		}
		if got := sipConnectBudget(ctx, "a", []sipCandidate{{transport: "udp"}, {transport: "udp"}}, 0); got != 0 {
			t.Errorf("udp = %s, want 0", got)
		}
		if got := sipConnectBudget(ctx, "rfc3263", tcp, 0); got != 0 {
			t.Errorf("rfc3263 = %s, want 0 (its attempt slice bounds it)", got)
		}
		short, cancelShort := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancelShort()
		if got := sipConnectBudget(short, "a", tcp, 0); got < 1900*time.Millisecond || got > 2*time.Second {
			t.Errorf("3s over three = %s, want the 2s floor", got)
		}
	})
}

// DNS failures still write the envelope, with the resolution mode.
func TestSIPChecker_ResolutionFailureEnvelope(t *testing.T) {
	t.Setenv("SIP_LOCALHOST_AS_HOST_GATEWAY", "")
	result := runSIPCheckWith(t, &SIPChecker{resolver: &fakeSIPResolver{}}, map[string]interface{}{"host": "example.com", "resolution": "rfc3263"}, 5)
	requireSIPError(t, result, "dns")
	m, raw := decodeSIPMetrics(t, result)
	if m.Resolution == nil || m.Resolution.Mode != "rfc3263" || m.Timing.DNSMs == nil {
		t.Errorf("resolution %+v timing %+v, want rfc3263 with dns_ms", m.Resolution, m.Timing)
	}
	if _, ok := raw["transport"]; ok {
		t.Errorf("transport = %v, want none: DNS never chose one", raw["transport"])
	}
	requireSIPTotalMatchesLatency(t, result, m)
}

// A configured nameserver is an operator-supplied address: its connections
// go through dialGuard like any target.
func TestSIPChecker_NameserverThroughDialGuard(t *testing.T) {
	checker := NewSIPChecker(true, nil)
	config := &models.SIPMonitorConfig{Host: "example.com", Resolution: "rfc3263", Nameserver: "10.0.0.53"}
	r, err := checker.newSIPResolver(config, time.Second)
	if err != nil {
		t.Fatalf("newSIPResolver() error = %v", err)
	}
	resolver, ok := r.(*net.Resolver)
	if !ok || !resolver.PreferGo || resolver.Dial == nil {
		t.Fatalf("resolver = %T, want a pinned Go resolver", r)
	}
	_, err = resolver.Dial(context.Background(), "udp", "ignored:53")
	if !errors.Is(err, netguard.ErrBlocked) {
		t.Fatalf("Dial() error = %v, want ssrf_blocked for a private nameserver", err)
	}

	// Without a nameserver (or outside rfc3263) the worker's resolver.
	for _, c := range []*models.SIPMonitorConfig{
		{Host: "example.com", Resolution: "rfc3263"},
		{Host: "example.com", Nameserver: "10.0.0.53"},
	} {
		if r, _ := checker.newSIPResolver(c, time.Second); r != sipResolver(net.DefaultResolver) {
			t.Errorf("config %+v: resolver %T, want net.DefaultResolver", c, r)
		}
	}
}
