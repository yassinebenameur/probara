package worker

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net"
	"net/netip"
	"os"
	"regexp"
	"runtime/metrics"
	"slices"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/yassinebenameur/probara/shared/models"
	"github.com/yassinebenameur/probara/shared/netguard"
	"github.com/yassinebenameur/probara/worker/internal/rtp"
)

// --- the plan ---------------------------------------------------------------

func testAnswer(t *testing.T, sdp string) *sipSDPAnswer {
	t.Helper()
	return parseSDPAnswer([]byte(strings.ReplaceAll(sdp, "\n", "\r\n")))
}

// prepareMedia reads the answer the way RFC 3264 asks (each side receives in
// its own numbering), refuses what it cannot use, and vets the destination
// before anything is sent.
func TestPrepareMedia(t *testing.T) {
	const peer = "93.184.216.34" // public: the shared table counts the TEST-NETs as reserved
	open := sipMediaEnv{
		expect: models.SIPMediaExpectAudio,
		peer:   netip.MustParseAddr(peer),
		local:  netip.MustParseAddrPort("10.1.2.3:20000"),
	}
	sdp := func(conn, m string, attrs ...string) string {
		return "v=0\no=- 1 1 IN IP4 " + peer + "\ns=-\nc=" + conn + "\nt=0 0\nm=" + m + "\n" + strings.Join(attrs, "\n") + "\n"
	}
	tests := []struct {
		name         string
		sdp          string
		env          func(sipMediaEnv) sipMediaEnv
		status       string // "" usable
		detail       string
		codec        string
		txPT         uint8
		ptime        time.Duration
		send         bool
		dst          string
		acceptedFrom string
	}{
		{name: "pcmu", sdp: sdp("IN IP4 "+peer, "audio 40012 RTP/AVP 0 101", "a=rtpmap:101 telephone-event/8000"),
			codec: "PCMU/8000", txPT: 0, ptime: 20 * time.Millisecond, send: true, dst: peer + ":40012", acceptedFrom: peer},
		{name: "first g711 in the answer's order", sdp: sdp("IN IP4 "+peer, "audio 40012 RTP/AVP 18 8 0", "a=ptime:30"),
			codec: "PCMA/8000", txPT: 8, ptime: 30 * time.Millisecond, send: true, dst: peer + ":40012", acceptedFrom: peer},
		{name: "dynamic numbering", sdp: sdp("IN IP4 "+peer, "audio 40012 RTP/AVP 96 98", "a=rtpmap:96 telephone-event/8000", "a=rtpmap:98 pcmu/8000"),
			codec: "pcmu/8000", txPT: 98, ptime: 20 * time.Millisecond, send: true, dst: peer + ":40012", acceptedFrom: peer},
		{name: "unknown ptime falls back to 20", sdp: sdp("IN IP4 "+peer, "audio 40012 RTP/AVP 0", "a=ptime:25"),
			codec: "PCMU/8000", ptime: 20 * time.Millisecond, send: true, dst: peer + ":40012", acceptedFrom: peer},
		{name: "avpf", sdp: sdp("IN IP4 "+peer, "audio 40012 RTP/AVPF 0"), codec: "PCMU/8000", ptime: 20 * time.Millisecond, send: true, dst: peer + ":40012", acceptedFrom: peer},
		{name: "srtp profile", sdp: sdp("IN IP4 "+peer, "audio 40012 RTP/SAVP 0"), status: sipMediaUnusable, detail: "profile"},
		{name: "inactive", sdp: sdp("IN IP4 "+peer, "audio 40012 RTP/AVP 0", "a=inactive"), status: sipMediaUnusable, detail: "inactive"},
		{name: "port 0", sdp: sdp("IN IP4 "+peer, "audio 0 RTP/AVP 0"), status: sipMediaUnusable, detail: "inactive"},
		{name: "hold address", sdp: sdp("IN IP4 0.0.0.0", "audio 40012 RTP/AVP 0"), status: sipMediaUnusable, detail: "inactive"},
		{name: "g729 only", sdp: sdp("IN IP4 "+peer, "audio 40012 RTP/AVP 18"), status: sipMediaUnusable, detail: "codec_not_offered"},
		{name: "pcmu at another rate", sdp: sdp("IN IP4 "+peer, "audio 40012 RTP/AVP 97", "a=rtpmap:97 PCMU/16000"), status: sipMediaUnusable, detail: "codec_not_offered"},
		{name: "ipv6 on an ipv4 flow", sdp: sdp("IN IP6 2001:db8::7", "audio 40012 RTP/AVP 0"), status: sipMediaUnusable, detail: "family"},
		{name: "address type contradicts the literal", sdp: sdp("IN IP6 "+peer, "audio 40012 RTP/AVP 0"), status: sipMediaUnusable, detail: "family"},
		{name: "recvonly", sdp: sdp("IN IP4 "+peer, "audio 40012 RTP/AVP 0", "a=recvonly"), status: sipMediaUnusable, detail: "recvonly"},
		{name: "recvonly measured only", sdp: sdp("IN IP4 "+peer, "audio 40012 RTP/AVP 0", "a=recvonly"),
			env:   func(e sipMediaEnv) sipMediaEnv { e.expect = models.SIPMediaExpectNone; return e },
			codec: "PCMU/8000", ptime: 20 * time.Millisecond, send: true, dst: peer + ":40012", acceptedFrom: peer},
		{name: "sendonly receives only", sdp: sdp("IN IP4 "+peer, "audio 40012 RTP/AVP 0", "a=sendonly"),
			codec: "PCMU/8000", ptime: 20 * time.Millisecond, send: false, dst: peer + ":40012", acceptedFrom: peer},
		{name: "sendonly cannot echo", sdp: sdp("IN IP4 "+peer, "audio 40012 RTP/AVP 0", "a=sendonly"),
			env: func(e sipMediaEnv) sipMediaEnv { e.expect = models.SIPMediaExpectEcho; return e }, status: sipMediaUnusable, detail: "sendonly"},
		{name: "hostname is never resolved", sdp: sdp("IN IP4 media.example.com", "audio 40012 RTP/AVP 0"),
			status: sipMediaRefused, detail: "hostname", codec: "PCMU/8000", ptime: 20 * time.Millisecond, send: true},
		{name: "private behind a public peer", sdp: sdp("IN IP4 10.0.0.5", "audio 40012 RTP/AVP 0"),
			status: sipMediaRefused, detail: netguard.ReasonPrivateBehindPublic, codec: "PCMU/8000", ptime: 20 * time.Millisecond, send: true},
		{name: "metadata", sdp: sdp("IN IP4 169.254.169.254", "audio 40012 RTP/AVP 0"),
			status: sipMediaRefused, detail: netguard.ReasonLinkLocal, codec: "PCMU/8000", ptime: 20 * time.Millisecond, send: true},
		{name: "well-known port", sdp: sdp("IN IP4 "+peer, "audio 53 RTP/AVP 0"),
			status: sipMediaRefused, detail: netguard.ReasonPort, codec: "PCMU/8000", ptime: 20 * time.Millisecond, send: true},
		{name: "blocked by policy", sdp: sdp("IN IP4 10.1.9.9", "audio 40012 RTP/AVP 0"),
			env: func(e sipMediaEnv) sipMediaEnv {
				e.policy = netguard.Policy{BlockPrivate: true}
				e.peer = netip.MustParseAddr("10.1.0.1")
				return e
			},
			status: sipMediaRefused, detail: netguard.ReasonBlockedByPolicy, codec: "PCMU/8000", ptime: 20 * time.Millisecond, send: true},
		{name: "private behind a private peer", sdp: sdp("IN IP4 100.102.14.7", "audio 40012 RTP/AVP 0"),
			env:   func(e sipMediaEnv) sipMediaEnv { e.peer = netip.MustParseAddr("100.102.14.1"); return e },
			codec: "PCMU/8000", ptime: 20 * time.Millisecond, send: true, dst: "100.102.14.7:40012", acceptedFrom: "100.102.14.7"},
		{name: "a local subnet's broadcast", sdp: sdp("IN IP4 10.20.1.255", "audio 8125 RTP/AVP 0"),
			env: func(e sipMediaEnv) sipMediaEnv {
				e.peer = netip.MustParseAddr("10.20.0.5")
				e.localNets = []netip.Prefix{netip.MustParsePrefix("10.20.1.7/24")}
				return e
			},
			status: sipMediaRefused, detail: "broadcast", codec: "PCMU/8000", ptime: 20 * time.Millisecond, send: true},
		{name: "a local subnet's network address", sdp: sdp("IN IP4 10.20.1.0", "audio 8124 RTP/AVP 0"),
			env: func(e sipMediaEnv) sipMediaEnv {
				e.peer = netip.MustParseAddr("10.20.0.5")
				e.localNets = []netip.Prefix{netip.MustParsePrefix("10.20.1.7/24")}
				return e
			},
			status: sipMediaRefused, detail: "broadcast", codec: "PCMU/8000", ptime: 20 * time.Millisecond, send: true},
		{name: "a public subnet's broadcast, blocking worker", sdp: sdp("IN IP4 93.184.216.255", "audio 40000 RTP/AVP 0"),
			env: func(e sipMediaEnv) sipMediaEnv {
				e.policy = netguard.Policy{BlockPrivate: true}
				e.peer = netip.MustParseAddr("93.184.216.10")
				e.localNets = []netip.Prefix{netip.MustParsePrefix("93.184.216.3/24")}
				return e
			},
			status: sipMediaRefused, detail: "broadcast", codec: "PCMU/8000", ptime: 20 * time.Millisecond, send: true},
		{name: "a neighbour on a local subnet", sdp: sdp("IN IP4 10.20.1.9", "audio 40000 RTP/AVP 0"),
			env: func(e sipMediaEnv) sipMediaEnv {
				e.peer = netip.MustParseAddr("10.20.0.5")
				e.localNets = []netip.Prefix{netip.MustParsePrefix("10.20.1.7/24")}
				return e
			},
			codec: "PCMU/8000", ptime: 20 * time.Millisecond, send: true, dst: "10.20.1.9:40000", acceptedFrom: "10.20.1.9"},
		{name: "the probe's own port", sdp: sdp("IN IP4 10.1.2.3", "audio 20001 RTP/AVP 0"),
			env:    func(e sipMediaEnv) sipMediaEnv { e.peer = netip.MustParseAddr("10.1.0.1"); return e },
			status: sipMediaRefused, detail: "self", codec: "PCMU/8000", ptime: 20 * time.Millisecond, send: true},
		{name: "loopback mirrored to the host gateway", sdp: sdp("IN IP4 127.0.0.1", "audio 40012 RTP/AVP 0"),
			env: func(e sipMediaEnv) sipMediaEnv {
				e.loopbackMirror = true
				e.peer = netip.MustParseAddr("192.168.65.254")
				return e
			},
			codec: "PCMU/8000", ptime: 20 * time.Millisecond, send: true, dst: "192.168.65.254:40012", acceptedFrom: "192.168.65.254"},
		{name: "loopback from a public peer", sdp: sdp("IN IP4 127.0.0.1", "audio 40012 RTP/AVP 0"),
			status: sipMediaRefused, detail: netguard.ReasonLoopback, codec: "PCMU/8000", ptime: 20 * time.Millisecond, send: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			env := open
			if tt.env != nil {
				env = tt.env(env)
			}
			plan := prepareMedia(testAnswer(t, tt.sdp), sipAnswerFrom2xx, env)
			if plan.status != tt.status || plan.detail != tt.detail {
				t.Fatalf("plan = %q/%q, want %q/%q", plan.status, plan.detail, tt.status, tt.detail)
			}
			if tt.status == sipMediaUnusable {
				return
			}
			if plan.codec != tt.codec || plan.txPT != tt.txPT || plan.ptime != tt.ptime || plan.send != tt.send {
				t.Fatalf("plan codec/pt/ptime/send = %q/%d/%s/%v, want %q/%d/%s/%v",
					plan.codec, plan.txPT, plan.ptime, plan.send, tt.codec, tt.txPT, tt.ptime, tt.send)
			}
			gotDst := ""
			if plan.dst.IsValid() {
				gotDst = plan.dst.String()
			}
			gotAccepted := ""
			if plan.sdpIP.IsValid() {
				gotAccepted = plan.sdpIP.String()
			}
			if gotDst != tt.dst || gotAccepted != tt.acceptedFrom {
				t.Fatalf("dst/accepted = %q/%q, want %q/%q", gotDst, gotAccepted, tt.dst, tt.acceptedFrom)
			}
		})
	}
	if plan := prepareMedia(nil, sipAnswerFrom2xx, open); plan.status != sipMediaUnusable || plan.detail != "no_audio" {
		t.Fatalf("an SDP without audio: plan = %q/%q, want unusable_answer/no_audio", plan.status, plan.detail)
	}
}

func TestSchedP99Ms(t *testing.T) {
	buckets := []float64{0, 0.001, 0.002, 0.004, 0.008, math.Inf(1)}
	hist := func(counts ...uint64) *metrics.Float64Histogram {
		return &metrics.Float64Histogram{Counts: counts, Buckets: buckets}
	}
	if got := schedP99Ms(hist(10, 0, 0, 0, 0), hist(1010, 0, 0, 0, 0)); got != 1 {
		t.Fatalf("p99 of fast scheduling = %v ms, want the first bucket's bound, 1", got)
	}
	// 2% of the window's latencies above 4 ms: the p99 lands there.
	if got := schedP99Ms(hist(100, 0, 0, 0, 0), hist(1080, 0, 0, 20, 0)); got != 8 {
		t.Fatalf("p99 = %v ms, want 8", got)
	}
	// The open last bucket reads as its lower bound.
	if got := schedP99Ms(hist(0, 0, 0, 0, 0), hist(0, 0, 0, 0, 5)); got != 8 {
		t.Fatalf("p99 in the open bucket = %v ms, want 8", got)
	}
	for _, tc := range [][2]*metrics.Float64Histogram{
		{nil, hist(1, 0, 0, 0, 0)},
		{hist(3, 0, 0, 0, 0), hist(3, 0, 0, 0, 0)},
		{hist(1, 0), hist(1, 0, 0, 0, 0)},
	} {
		if got := schedP99Ms(tc[0], tc[1]); !math.IsNaN(got) {
			t.Fatalf("schedP99Ms(%v, %v) = %v, want NaN (unknown)", tc[0], tc[1], got)
		}
	}
	if readSchedLatencies() == nil {
		t.Fatalf("the runtime has no %s histogram", schedLatenciesMetric)
	}
}

func TestSIPMediaWriteClass(t *testing.T) {
	wrap := func(errno syscall.Errno) error {
		return &net.OpError{Op: "write", Net: "udp", Err: os.NewSyscallError("sendto", errno)}
	}
	for _, tc := range []struct {
		err  error
		want string
	}{
		{wrap(syscall.EPERM), "eperm"},
		{wrap(syscall.EACCES), "eperm"},
		{wrap(syscall.ENETUNREACH), "unreachable"},
		{wrap(syscall.EHOSTUNREACH), "unreachable"},
		{wrap(syscall.ENOBUFS), "nobufs"},
		{errors.New("boom"), "other"},
	} {
		if got := sipMediaWriteClassNames[sipMediaWriteClass(tc.err)]; got != tc.want {
			t.Errorf("class(%v) = %q, want %q", tc.err, got, tc.want)
		}
	}
}

// --- the session without a call ---------------------------------------------

// newTestMediaSession is a session with a plan and no sockets, for feeding
// its reader sink and rules by hand.
func newTestMediaSession(expect string, plan *sipMediaPlan, peer string) *sipMediaSession {
	m := &sipMediaSession{
		cfg:      &models.SIPMediaConfig{Expect: expect},
		counters: &sipMediaCounters{},
		env: sipMediaEnv{
			expect: expect,
			peer:   netip.MustParseAddr(peer),
			local:  netip.MustParseAddrPort("127.0.0.1:20000"),
		},
		plan:     plan,
		base:     time.Now(),
		ssrc:     0x50524f42,
		schedP99: math.NaN(),
		done:     make(chan struct{}),
		held:     true,
		stopped:  true,
	}
	m.records = make([]rtp.Record, 0, 64)
	if plan.dst.IsValid() && plan.send {
		dst := plan.dst
		m.dst.Store(&dst)
	}
	return m
}

func testRTP(seq uint16, ssrc uint32) []byte {
	return rtp.AppendPacket(nil, rtp.Header{PayloadType: 0, Seq: seq, Timestamp: uint32(seq) * 160, SSRC: ssrc}, make([]byte, 160))
}

// Inbound packets are analysed only from the SDP address's IP or the
// signalling peer's; the latch moves the destination once, at most, and only
// to a vetted address on one of those IPs.
func TestSIPMediaSession_SourcesAndLatching(t *testing.T) {
	at := time.Now()
	sdp := netip.MustParseAddrPort("127.0.0.1:40000")
	usable := &sipMediaPlan{send: true, dst: sdp, sdpIP: sdp.Addr(), law: rtp.PCMU}

	t.Run("port latch, once", func(t *testing.T) {
		m := newTestMediaSession(models.SIPMediaExpectAudio, usable, "127.0.0.1")
		m.onRTP(testRTP(1, 7), netip.MustParseAddrPort("127.0.0.1:40100"), at)
		m.onRTP(testRTP(2, 7), netip.MustParseAddrPort("127.0.0.1:40200"), at)
		if got := *m.dst.Load(); got.Port() != 40100 || m.latched != sipMediaLatchPort {
			t.Fatalf("dst = %s latched = %q, want the first other port, latched once", got, m.latched)
		}
		if len(m.records) != 2 || m.foreign != 0 {
			t.Fatalf("records = %d foreign = %d, want both analysed", len(m.records), m.foreign)
		}
	})
	t.Run("no latch once the SDP address sent", func(t *testing.T) {
		m := newTestMediaSession(models.SIPMediaExpectAudio, usable, "127.0.0.1")
		m.onRTP(testRTP(1, 7), sdp, at)
		m.onRTP(testRTP(2, 7), netip.MustParseAddrPort("127.0.0.1:40100"), at)
		if got := *m.dst.Load(); got != sdp || m.latched != "" {
			t.Fatalf("dst = %s latched = %q, want the SDP address kept", got, m.latched)
		}
	})
	t.Run("a latch target below 1024 is refused", func(t *testing.T) {
		m := newTestMediaSession(models.SIPMediaExpectAudio, usable, "127.0.0.1")
		m.onRTP(testRTP(1, 7), netip.MustParseAddrPort("127.0.0.1:53"), at)
		m.onRTP(testRTP(2, 7), netip.MustParseAddrPort("127.0.0.1:40100"), at)
		if got := *m.dst.Load(); got != sdp || m.latched != "" {
			t.Fatalf("dst = %s latched = %q, want no latch (the one try was refused)", got, m.latched)
		}
	})
	t.Run("peer latch after a refused address", func(t *testing.T) {
		refused := &sipMediaPlan{send: true, status: sipMediaRefused, detail: "hostname", law: rtp.PCMU}
		m := newTestMediaSession(models.SIPMediaExpectAudio, refused, "203.0.113.9")
		m.onRTP(testRTP(1, 7), netip.MustParseAddrPort("198.51.100.1:40000"), at) // not the peer
		if m.dst.Load() != nil || m.foreign != 1 || len(m.records) != 0 {
			t.Fatalf("dst = %v foreign = %d records = %d, want a foreign packet ignored", m.dst.Load(), m.foreign, len(m.records))
		}
		m.onRTP(testRTP(2, 7), netip.MustParseAddrPort("203.0.113.9:41000"), at)
		if got := m.dst.Load(); got == nil || got.String() != "203.0.113.9:41000" || m.latched != sipMediaLatchPeer {
			t.Fatalf("dst = %v latched = %q, want the signalling peer's port", got, m.latched)
		}
	})
	t.Run("foreign, oversized, invalid and multiplexed RTCP", func(t *testing.T) {
		m := newTestMediaSession(models.SIPMediaExpectAudio, usable, "127.0.0.1")
		m.onRTP(testRTP(1, 7), netip.MustParseAddrPort("[::1]:40000"), at)
		m.onRTP(make([]byte, 1501), sdp, at)
		m.onRTP([]byte{0x40, 0, 0, 1, 0, 0, 0, 0, 0, 0, 0, 0, 0}, sdp, at) // version 1
		m.onRTP([]byte{0x80, 200, 0, 6, 0, 0, 0, 1}, sdp, at)              // an SR
		if m.foreign != 1 || m.invalid != 2 || len(m.records) != 0 || m.packets != 4 {
			t.Fatalf("foreign/invalid/records/packets = %d/%d/%d/%d, want 1/2/0/4", m.foreign, m.invalid, len(m.records), m.packets)
		}
		if len(m.sources) != 2 {
			t.Fatalf("sources = %v, want the two senders", m.sources)
		}
	})
	t.Run("record cap", func(t *testing.T) {
		m := newTestMediaSession(models.SIPMediaExpectAudio, usable, "127.0.0.1")
		for i := range 70 {
			m.onRTP(testRTP(uint16(i), 7), sdp, at)
		}
		if len(m.records) != 64 || m.overflow != 6 {
			t.Fatalf("records = %d overflow = %d, want the cap kept and the rest counted", len(m.records), m.overflow)
		}
	})
}

// The self-timing guard trusts a worker only when its sender kept time and
// its goroutines were scheduled promptly.
func TestSIPMediaSession_Reliable(t *testing.T) {
	steady := make([]time.Duration, 100)
	for _, tc := range []struct {
		name     string
		lateness []time.Duration
		skipped  int
		sched    float64
		ok       bool
		why      string
	}{
		{name: "steady", lateness: steady, sched: 0.4, ok: true},
		{name: "unknown scheduling", lateness: steady, sched: math.NaN(), ok: true},
		{name: "receive only", sched: 1, ok: true},
		{name: "slow scheduling", lateness: steady, sched: 10, why: "scheduling p99 10ms"},
		{name: "late sender", lateness: append(slices.Repeat([]time.Duration{0}, 94), slices.Repeat([]time.Duration{9 * time.Millisecond}, 6)...), sched: 1, why: "send p95 9ms"},
		{name: "skipped slots", lateness: steady, skipped: 6, sched: 1, why: "send p95 0ms"},
	} {
		m := &sipMediaSession{lateness: tc.lateness, skipped: tc.skipped, schedP99: tc.sched}
		if ok, why := m.reliable(); ok != tc.ok || why != tc.why {
			t.Errorf("%s: reliable() = %v %q, want %v %q", tc.name, ok, why, tc.ok, tc.why)
		}
	}
}

// testMediaRules runs the media rules of a held, successful call over a
// session's records.
func testMediaRules(t *testing.T, m *sipMediaSession, hold time.Duration) (CheckResult, *sipMetrics) {
	t.Helper()
	m.startAt, m.stopAt = 10*time.Millisecond, 10*time.Millisecond+hold
	metrics := &sipMetrics{Call: &sipCallMetrics{}}
	s := &sipCall{
		config:  &models.SIPMonitorConfig{Call: &models.SIPCallConfig{Media: m.cfg}},
		metrics: metrics, cm: metrics.Call, media: m,
		decided: true, result: CheckResult{Status: "success"},
	}
	s.cm.Media = m.finish(false)
	s.applyMediaRules()
	if raw := metrics.marshal(0); raw == nil {
		t.Fatal("the metrics envelope failed to marshal")
	}
	return s.result, metrics
}

// Audio that only ever came from an address the SDP did not name is called
// asymmetric media, not silence: the operator sees why nothing was measured.
func TestSIPMediaRules_AsymmetricMedia(t *testing.T) {
	sdp := netip.MustParseAddrPort("127.0.0.1:40000")
	plan := &sipMediaPlan{send: true, dst: sdp, sdpIP: sdp.Addr(), law: rtp.PCMU}
	for _, expect := range []string{models.SIPMediaExpectAudio, models.SIPMediaExpectEcho} {
		m := newTestMediaSession(expect, plan, "127.0.0.1")
		for i := range 50 {
			m.onRTP(testRTP(uint16(i), 7), netip.MustParseAddrPort("[2001:db8::9]:40000"), time.Now())
		}
		result, metrics := testMediaRules(t, m, 3*time.Second)
		requireStatus(t, result, "failure", "media: audio arrived only from an address the far end's SDP did not name (asymmetric media); not measured")
		if strings.Contains(resultMsg(result), "2001:db8") || len(metrics.AssertionsFailed) != 1 {
			t.Fatalf("%s: message = %q assertions = %q", expect, resultMsg(result), metrics.AssertionsFailed)
		}
	}
}

// One NaN anywhere in the media block would make the envelope fail to
// marshal and erase every call fact with it; degenerate inputs never do.
func TestSIPMediaMetrics_DegenerateInputsMarshal(t *testing.T) {
	sdp := netip.MustParseAddrPort("127.0.0.1:40000")
	plan := &sipMediaPlan{send: true, dst: sdp, sdpIP: sdp.Addr(), law: rtp.PCMU, ptime: 20 * time.Millisecond, codec: "PCMU/8000"}
	at := 600 * time.Millisecond
	cases := map[string][]rtp.Record{
		"empty":  nil,
		"single": {{At: at, SSRC: 7, Seq: 1}},
		"all lost": {
			{At: at, SSRC: 7, Seq: 1}, {At: at + time.Millisecond, SSRC: 7, Seq: 2}, {At: at + 2*time.Millisecond, SSRC: 7, Seq: 3000},
		},
		"all duplicates": {
			{At: at, SSRC: 7, Seq: 1}, {At: at + time.Millisecond, SSRC: 7, Seq: 2},
			{At: at + 2*time.Millisecond, SSRC: 7, Seq: 2}, {At: at + 3*time.Millisecond, SSRC: 7, Seq: 2},
		},
		"echo only": {{At: at, SSRC: 0x50524f42, Seq: 9, EchoSeq: 9, Echo: rtp.EchoBySSRC}},
	}
	for name, records := range cases {
		t.Run(name, func(t *testing.T) {
			m := newTestMediaSession(models.SIPMediaExpectEcho, plan, "127.0.0.1")
			m.records = records
			m.sent = []rtp.Sent{{At: 590 * time.Millisecond, Seq: 9}}
			m.lateness = []time.Duration{0}
			m.schedP99 = math.Inf(1)
			_, metrics := testMediaRules(t, m, 3*time.Second)
			raw := metrics.marshal(0)
			if raw == nil || strings.Contains(string(raw), "NaN") || strings.Contains(string(raw), "Inf") {
				t.Fatalf("envelope = %s, want finite JSON", raw)
			}
			if !strings.Contains(string(raw), `"media":{`) {
				t.Fatalf("envelope = %s, want the media block", raw)
			}
		})
	}
}

// --- in-process calls -------------------------------------------------------

// fakeMediaPeer is the far end's RTP endpoint in in-process call tests: a UDP
// socket on loopback that the fake UAS's SDP answer names. It records what
// the probe sends and can reflect it, re-originate it as an echo, or stream
// audio of its own (stream).
type fakeMediaPeer struct {
	t    *testing.T
	conn *net.UDPConn
	addr netip.AddrPort

	mu       sync.Mutex
	received []fakeMediaPacket
	reply    func(p rtp.Packet, raw []byte) []byte // nil: no echo
}

type fakeMediaPacket struct {
	at  time.Time
	src netip.AddrPort
	rtp.Header
	payload []byte
}

func startMediaPeer(t *testing.T, ip string) *fakeMediaPeer {
	t.Helper()
	conn, err := net.ListenUDP("udp", net.UDPAddrFromAddrPort(netip.AddrPortFrom(netip.MustParseAddr(ip), 0)))
	if err != nil {
		t.Fatalf("media peer on %s: %v (in-process media tests need IPv4 and IPv6 loopback)", ip, err)
	}
	t.Cleanup(func() { conn.Close() })
	p := &fakeMediaPeer{t: t, conn: conn, addr: conn.LocalAddr().(*net.UDPAddr).AddrPort()}
	go func() {
		buf := make([]byte, 2048)
		for {
			n, src, err := conn.ReadFromUDPAddrPort(buf)
			if err != nil {
				return
			}
			raw := append([]byte(nil), buf[:n]...)
			pkt, err := rtp.Parse(raw)
			if err != nil {
				continue
			}
			p.mu.Lock()
			p.received = append(p.received, fakeMediaPacket{at: time.Now(), src: src, Header: pkt.Header, payload: pkt.Payload})
			reply := p.reply
			p.mu.Unlock()
			if reply != nil {
				if out := reply(pkt, raw); out != nil {
					_, _ = conn.WriteToUDPAddrPort(out, src)
				}
			}
		}
	}()
	return p
}

// reflect sends every packet back unchanged: an echo application that keeps
// the probe's SSRC and sequence numbers.
func (p *fakeMediaPeer) reflect() {
	p.mu.Lock()
	p.reply = func(_ rtp.Packet, raw []byte) []byte { return raw }
	p.mu.Unlock()
}

// reoriginate echoes each payload in a stream of its own (new SSRC and
// sequence numbers), as an echo application that terminates RTP does.
func (p *fakeMediaPeer) reoriginate() {
	var seq uint16 = 5000
	p.mu.Lock()
	p.reply = func(pkt rtp.Packet, _ []byte) []byte {
		seq++
		return rtp.AppendPacket(nil, rtp.Header{PayloadType: pkt.PayloadType, Seq: seq, Timestamp: uint32(seq) * 160, SSRC: 0xec40ec40}, pkt.Payload)
	}
	p.mu.Unlock()
}

func (p *fakeMediaPeer) packets() []fakeMediaPacket {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]fakeMediaPacket(nil), p.received...)
}

// mediaStream scripts a far end's own audio: count frames of ptime from
// delay on (counted from when stream is called), in PCMU unless pt says
// otherwise. Frame i carries sequence number seq0+i; drop, dup and swap (i
// goes out after i+1) bend that, and offset shifts a frame's send time.
type mediaStream struct {
	delay  time.Duration
	count  int
	ptime  time.Duration
	pt     uint8
	ssrc   uint32
	seq0   uint16
	drop   map[int]bool
	dup    map[int]bool
	swap   map[int]bool
	offset func(i int) time.Duration
	burst  bool // as fast as possible, no pacing
	from   *net.UDPConn
}

// stream sends st to the probe at to, from the peer's socket unless st.from
// names another.
func (p *fakeMediaPeer) stream(to netip.AddrPort, st mediaStream) {
	conn := p.conn
	if st.from != nil {
		conn = st.from
	}
	if st.ptime == 0 {
		st.ptime = 20 * time.Millisecond
	}
	if st.ssrc == 0 {
		st.ssrc = 0x9f00aa01
	}
	start := time.Now().Add(st.delay)
	frame := func(i int) []byte {
		return rtp.AppendPacket(nil, rtp.Header{PayloadType: st.pt, Seq: st.seq0 + uint16(i), Timestamp: 1000 + uint32(i)*160, SSRC: st.ssrc},
			rtp.AppendSilence(nil, rtp.Law(st.pt), 160))
	}
	go func() {
		for i := 0; i < st.count; i++ {
			if !st.burst {
				due := start.Add(time.Duration(i) * st.ptime)
				if st.offset != nil {
					due = due.Add(st.offset(i))
				}
				time.Sleep(time.Until(due))
			} else if i == 0 {
				time.Sleep(time.Until(start))
			}
			n := i
			switch {
			case st.drop[i]:
				continue
			case st.swap[i]:
				n = i + 1
			case i > 0 && st.swap[i-1]:
				n = i - 1
			}
			if _, err := conn.WriteToUDPAddrPort(frame(n), to); err != nil && errors.Is(err, net.ErrClosed) {
				return
			}
			if st.dup[i] {
				_, _ = conn.WriteToUDPAddrPort(frame(n), to)
			}
		}
	}()
}

// probeMediaAddr is where the probe's INVITE offer says it receives audio.
func probeMediaAddr(t *testing.T, invite string) netip.AddrPort {
	t.Helper()
	body := fBody(invite)
	c := regexp.MustCompile(`c=IN IP[46] (\S+)`).FindStringSubmatch(body)
	m := regexp.MustCompile(`m=audio (\d+) `).FindStringSubmatch(body)
	if len(c) != 2 || len(m) != 2 {
		t.Errorf("offer without c= or m=: %q", body)
		return netip.AddrPort{}
	}
	port, _ := strconv.Atoi(m[1])
	return netip.AddrPortFrom(netip.MustParseAddr(c[1]), uint16(port))
}

// mediaSDP is an audio answer naming addr: "m=audio <port> RTP/AVP <pts>"
// plus attributes. A port of 0 here means the peer's.
func mediaSDP(addr netip.AddrPort, mline string, attrs ...string) string {
	family := "IP4"
	if addr.Addr().Is6() {
		family = "IP6"
	}
	lines := []string{
		"v=0", "o=- 1 1 IN " + family + " " + addr.Addr().String(), "s=-",
		"c=IN " + family + " " + addr.Addr().String(), "t=0 0",
		fmt.Sprintf(mline, addr.Port()),
	}
	return strings.Join(append(lines, attrs...), "\r\n") + "\r\n"
}

// mediaUAS answers an INVITE at once with 100 and a 200 carrying sdp (none
// when empty), calls answered with the INVITE once the 200 is out, and
// answers BYE with 200 unless silentBye.
type mediaUAS struct {
	sdp       string
	answered  func(u *fakeUAS, invite string)
	code      int
	silentBye bool
	early     string // an SDP sent in a 183 first; the 200 then carries none
}

func (h mediaUAS) handler() func(u *fakeUAS, msg string) {
	var invites branchSet
	return func(u *fakeUAS, msg string) {
		switch fMethod(msg) {
		case "INVITE":
			if !invites.first(msg) {
				return
			}
			u.send(uasReply(msg, 100, "Trying", "", ""))
			code, sdp := h.code, h.sdp
			if code == 0 {
				code = 200
			}
			if h.early != "" {
				u.send(uasReply(msg, 183, "Session Progress", "srv1", h.early, u.contact()))
				sdp = ""
			}
			u.send(uasReply(msg, code, "OK", "srv1", sdp, u.contact()))
			if h.answered != nil {
				h.answered(u, msg)
			}
		case "BYE":
			if !h.silentBye {
				u.send(uasReply(msg, 200, "OK", "", ""))
			}
		}
	}
}

// mediaCallConfig is a test call with call.media.
func mediaCallConfig(u *fakeUAS, holdMs int, media map[string]any, extra map[string]any) map[string]any {
	call := map[string]any{"number": "100", "hold_ms": holdMs, "media": media}
	cfg := inviteConfig(u, call, extra)
	if ip := net.ParseIP(u.host); ip != nil && ip.To4() == nil {
		cfg["host"] = u.host
	}
	return cfg
}

// runMediaCall is runInvite for parallel subtests: its parent clears
// SIP_LOCALHOST_AS_HOST_GATEWAY (mediaTests), which a parallel test cannot.
func runMediaCall(t *testing.T, ctx context.Context, c *SIPChecker, cfg map[string]any) (CheckResult, *sipMetrics, *sipMediaMetrics) {
	t.Helper()
	raw, err := json.Marshal(cfg)
	if err != nil {
		t.Fatal(err)
	}
	hold := cfg["call"].(map[string]any)["hold_ms"].(int)
	result := c.Check(ctx, raw, hold/1000+3)
	var env sipMetricsEnvelope
	if err := json.Unmarshal(result.MetricsData, &env); err != nil || env.SIP == nil || env.SIP.Call == nil {
		t.Fatalf("metrics_data = %s (%v), want a call envelope", result.MetricsData, err)
	}
	return result, env.SIP, env.SIP.Call.Media
}

// mediaTests runs each case as a parallel subtest: a media call holds at
// least 3 s, and most cases only wait.
func mediaTests(t *testing.T, cases map[string]func(t *testing.T)) {
	t.Setenv("SIP_LOCALHOST_AS_HOST_GATEWAY", "")
	for name, run := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			run(t)
		})
	}
}

// reliably reruns a scenario whose assertions need trustworthy timing
// (quality thresholds) while the worker's self-timing guard says the run was
// too loaded to judge, which an oversubscribed -race runner can be.
func reliably(t *testing.T, scenario func() (CheckResult, *sipMetrics, *sipMediaMetrics)) (CheckResult, *sipMetrics, *sipMediaMetrics) {
	t.Helper()
	var (
		result  CheckResult
		metrics *sipMetrics
		media   *sipMediaMetrics
	)
	for range 3 {
		result, metrics, media = scenario()
		if media == nil || media.TX == nil || media.TX.Reliable {
			break
		}
		t.Logf("media timing unreliable (%+v); rerunning", media.TX)
	}
	return result, metrics, media
}

// sendLog records every datagram the probe sends (SIPChecker.testSendTo).
type sendLog struct {
	mu    sync.Mutex
	times []time.Time
	dsts  []netip.AddrPort
	pts   []uint8
	fail  error // returned for every write when set
}

func (l *sendLog) attach(c *SIPChecker) {
	c.testSendTo = func(b []byte, dst netip.AddrPort) error {
		l.mu.Lock()
		defer l.mu.Unlock()
		l.times = append(l.times, time.Now())
		l.dsts = append(l.dsts, dst)
		if len(b) > 1 {
			l.pts = append(l.pts, b[1]&0x7f)
		}
		return l.fail
	}
}

func (l *sendLog) count() int {
	l.mu.Lock()
	defer l.mu.Unlock()
	return len(l.times)
}

func (l *sendLog) last() time.Time {
	l.mu.Lock()
	defer l.mu.Unlock()
	if len(l.times) == 0 {
		return time.Time{}
	}
	return l.times[len(l.times)-1]
}

func hasWarning(m *sipMetrics, fragment string) bool {
	for _, w := range m.Warnings {
		if strings.Contains(w, fragment) {
			return true
		}
	}
	return false
}

func requireWarning(t *testing.T, m *sipMetrics, fragment string) {
	t.Helper()
	if !hasWarning(m, fragment) {
		t.Fatalf("warnings = %q, want one containing %q", m.Warnings, fragment)
	}
}

func requireMediaStatus(t *testing.T, media *sipMediaMetrics, status, detail string) {
	t.Helper()
	if media == nil {
		t.Fatal("no media block")
	}
	if media.Status != status || media.Detail != detail {
		t.Fatalf("media status/detail = %q/%q, want %q/%q", media.Status, media.Detail, status, detail)
	}
}

// requireNoAddress checks a result message names no address: it reaches
// every notification channel.
func requireNoAddress(t *testing.T, r CheckResult, m *sipMetrics) {
	t.Helper()
	texts := append([]string{resultMsg(r)}, m.Warnings...)
	texts = append(texts, m.AssertionsFailed...)
	addr := regexp.MustCompile(`\d+\.\d+\.\d+\.\d+|::1|\[`)
	for _, text := range texts {
		if addr.MatchString(text) {
			t.Fatalf("%q names an address", text)
		}
	}
}

func pcmuAnswer(peer *fakeMediaPeer) string {
	return mediaSDP(peer.addr, "m=audio %d RTP/AVP 0 101", "a=rtpmap:0 PCMU/8000", "a=rtpmap:101 telephone-event/8000", "a=ptime:20", "a=sendrecv")
}

// lossyStream is 101 frames 800 ms into the hold, past the warm-up: the
// first serves the RFC 3550 A.1 probation (not counted), and of the 100
// expected after it five are lost (one every twenty), one duplicated and one
// pair swapped.
func lossyStream() mediaStream {
	return mediaStream{
		delay: 800 * time.Millisecond, count: 101, seq0: 65500, // wraps mid-stream
		drop: map[int]bool{10: true, 30: true, 50: true, 70: true, 90: true},
		dup:  map[int]bool{40: true},
		swap: map[int]bool{60: true},
	}
}

func TestSIPMedia_Echo(t *testing.T) {
	mediaTests(t, map[string]func(t *testing.T){
		// A reflector proves the round trip: the probe's own SSRC comes back.
		"reflector": func(t *testing.T) {
			peer := startMediaPeer(t, "127.0.0.1")
			peer.reflect()
			u := startFakeUAS(t, "udp", mediaUAS{sdp: pcmuAnswer(peer)}.handler())
			result, m, media := runMediaCall(t, context.Background(), newInviteTestChecker(), mediaCallConfig(u, 3000, map[string]any{"expect": "echo"}, nil))
			requireStatus(t, result, "success")
			requireMediaStatus(t, media, sipMediaMeasured, "")
			rt := media.RoundTrip
			if rt == nil || rt.MatchedBy != "ssrc" || rt.Expected < 50 || rt.Matched != rt.Expected || rt.LossPct != 0 {
				t.Fatalf("round_trip = %+v, want every expected echo matched by SSRC", rt)
			}
			if rt.Quality == nil || rt.Quality.MOS < 4.3 || rt.RTTMs == nil || rt.RTTMs.P50 > 200 {
				t.Fatalf("round_trip quality/rtt = %+v / %+v", rt.Quality, rt.RTTMs)
			}
			if media.TwoWay != "verified" || strings.Join(media.TwoWayEvidence, ",") != "rx,echo" {
				t.Fatalf("two_way = %q %q, want verified by rx and echo", media.TwoWay, media.TwoWayEvidence)
			}
			if media.Destination != peer.addr.String() || media.Codec != "PCMU/8000" || *media.TxPT != 0 || media.Model == "" {
				t.Fatalf("media = %+v", media)
			}
			if m.Call.Teardown != "bye_ok" {
				t.Fatalf("teardown = %q, want bye_ok", m.Call.Teardown)
			}
			// Echo mode sends the tone with the marker, from the offered port.
			got := peer.packets()
			if len(got) == 0 || string(got[0].payload[:4]) != "PRB1" || got[0].payload[10] == rtp.PCMU.Silence() {
				t.Fatalf("first packet payload = % x, want the marked tone", got[0].payload[:12])
			}
			if offer := probeMediaAddr(t, u.requests("INVITE")[0].raw); got[0].src.Port() != offer.Port() || !got[0].Marker {
				t.Fatalf("sent from %s marker=%v, want the offered port %d and a marker on the first packet", got[0].src, got[0].Marker, offer.Port())
			}
		},
		// An echo application that re-originates the stream is matched by
		// the payload marker.
		"re-originated": func(t *testing.T) {
			peer := startMediaPeer(t, "127.0.0.1")
			peer.reoriginate()
			u := startFakeUAS(t, "udp", mediaUAS{sdp: pcmuAnswer(peer)}.handler())
			result, _, media := runMediaCall(t, context.Background(), newInviteTestChecker(), mediaCallConfig(u, 3000, map[string]any{"expect": "echo"}, nil))
			requireStatus(t, result, "success")
			if rt := media.RoundTrip; rt == nil || rt.MatchedBy != "marker" || rt.Matched < rt.Expected-1 {
				t.Fatalf("round_trip = %+v, want matched by marker", rt)
			}
		},
		"silent": func(t *testing.T) {
			peer := startMediaPeer(t, "127.0.0.1")
			u := startFakeUAS(t, "udp", mediaUAS{sdp: pcmuAnswer(peer)}.handler())
			result, m, media := runMediaCall(t, context.Background(), newInviteTestChecker(), mediaCallConfig(u, 3000, map[string]any{"expect": "echo"}, nil))
			requireStatus(t, result, "failure", "media: no echo of the probe's audio in 2.")
			requireMediaStatus(t, media, sipMediaMeasured, "")
			if len(peer.packets()) < 100 || m.Call.Teardown != "bye_ok" || len(m.AssertionsFailed) != 1 {
				t.Fatalf("peer got %d packets, teardown %q, assertions %q", len(peer.packets()), m.Call.Teardown, m.AssertionsFailed)
			}
		},
		"own audio instead of an echo": func(t *testing.T) {
			peer := startMediaPeer(t, "127.0.0.1")
			u := startFakeUAS(t, "udp", mediaUAS{sdp: pcmuAnswer(peer), answered: func(u *fakeUAS, invite string) {
				peer.stream(probeMediaAddr(t, invite), mediaStream{count: 140})
			}}.handler())
			result, _, _ := runMediaCall(t, context.Background(), newInviteTestChecker(), mediaCallConfig(u, 3000, map[string]any{"expect": "echo"}, nil))
			requireStatus(t, result, "failure", "media: no echo of the probe's audio in", "packets of its own audio (transcoding, or not an echo line?)")
		},
		"ipv6": func(t *testing.T) {
			peer := startMediaPeer(t, "::1")
			peer.reflect()
			u := startFakeUASOn(t, "udp", "::1", mediaUAS{sdp: pcmuAnswer(peer)}.handler())
			result, _, media := runMediaCall(t, context.Background(), newInviteTestChecker(), mediaCallConfig(u, 3000, map[string]any{"expect": "echo"}, nil))
			requireStatus(t, result, "success")
			requireMediaStatus(t, media, sipMediaMeasured, "")
			if media.Destination != peer.addr.String() || media.RoundTrip == nil || media.RoundTrip.Matched == 0 {
				t.Fatalf("media = %+v", media)
			}
		},
		// Signalling over TCP, media over UDP as always.
		"tcp signalling": func(t *testing.T) {
			peer := startMediaPeer(t, "127.0.0.1")
			peer.reflect()
			u := startFakeUAS(t, "tcp", mediaUAS{sdp: pcmuAnswer(peer)}.handler())
			result, _, media := runMediaCall(t, context.Background(), newInviteTestChecker(), mediaCallConfig(u, 3000, map[string]any{"expect": "echo"}, nil))
			requireStatus(t, result, "success")
			requireMediaStatus(t, media, sipMediaMeasured, "")
		},
	})
}

func TestSIPMedia_Inbound(t *testing.T) {
	lossy := func(t *testing.T, media map[string]any) (CheckResult, *sipMetrics, *sipMediaMetrics) {
		return reliably(t, func() (CheckResult, *sipMetrics, *sipMediaMetrics) {
			peer := startMediaPeer(t, "127.0.0.1")
			u := startFakeUAS(t, "udp", mediaUAS{sdp: pcmuAnswer(peer), answered: func(u *fakeUAS, invite string) {
				peer.stream(probeMediaAddr(t, invite), lossyStream())
			}}.handler())
			return runMediaCall(t, context.Background(), newInviteTestChecker(), mediaCallConfig(u, 3000, media, nil))
		})
	}
	mediaTests(t, map[string]func(t *testing.T){
		// Exact counts, whatever loopback timing does: loss is an exact set
		// over the sequence numbers, across a wrap.
		"loss, duplicate and reorder": func(t *testing.T) {
			result, m, media := lossy(t, map[string]any{"expect": "audio"})
			requireStatus(t, result, "success")
			in := media.Inbound
			if in == nil || in.Expected != 100 || in.Lost != 5 || in.Duplicates != 1 || in.Reordered != 1 || in.LossPct != 5 {
				t.Fatalf("inbound = %+v, want 5 of 100 lost, 1 duplicate, 1 reordered", in)
			}
			if in.SSRC != 0x9f00aa01 || in.Quality == nil || in.JitterMs == nil {
				t.Fatalf("inbound = %+v, want the far SSRC with quality and jitter", in)
			}
			if media.TwoWay != "inbound_only" || media.RX.FirstPacketMs == nil || len(media.RX.Sources) != 1 {
				t.Fatalf("media = %+v rx = %+v", media, media.RX)
			}
			if hasWarning(m, "media:") {
				t.Fatalf("warnings = %q, want none for a healthy stream", m.Warnings)
			}
		},
		"max loss fails": func(t *testing.T) {
			result, m, _ := lossy(t, map[string]any{"expect": "audio", "max_loss_pct": 2})
			requireStatus(t, result, "failure", "% > 2% (inbound)")
			if len(m.AssertionsFailed) != 1 || !regexp.MustCompile(`^loss: \d+\.\d\d% > 2% \(inbound\)$`).MatchString(m.AssertionsFailed[0]) {
				t.Fatalf("assertions = %q", m.AssertionsFailed)
			}
		},
		"warn band": func(t *testing.T) {
			result, m, _ := lossy(t, map[string]any{"expect": "audio", "warn_loss_pct": 1, "max_loss_pct": 50, "warn_mos": 4.2})
			requireStatus(t, result, "success")
			requireWarning(t, m, "% > 1% (warn, inbound)")
			requireWarning(t, m, "(warn, inbound)")
			if m.LatencyWarnMs != nil {
				t.Fatal("a media warning set latency_warn_ms")
			}
		},
		"min mos fails": func(t *testing.T) {
			result, _, media := lossy(t, map[string]any{"expect": "audio", "min_mos": 4.3})
			requireStatus(t, result, "failure", "mos: ", " < 4.30 (inbound)")
			if q := media.Inbound.Quality; q.DelaySource != rtp.DelayUnmeasured || q.MOS >= 4.3 {
				t.Fatalf("quality = %+v, want an upper-bound estimate below 4.3", q)
			}
		},
		"rtt unmeasured is a warning": func(t *testing.T) {
			result, m, _ := lossy(t, map[string]any{"expect": "audio", "max_rtt_ms": 300})
			requireStatus(t, result, "success")
			requireWarning(t, m, "rtt: not measured (no echo)")
		},
		"silent peer": func(t *testing.T) {
			for _, tc := range []struct{ expect, status string }{{"none", "success"}, {"audio", "failure"}} {
				peer := startMediaPeer(t, "127.0.0.1")
				u := startFakeUAS(t, "udp", mediaUAS{sdp: pcmuAnswer(peer)}.handler())
				result, m, media := runMediaCall(t, context.Background(), newInviteTestChecker(), mediaCallConfig(u, 3000, map[string]any{"expect": tc.expect}, nil))
				requireStatus(t, result, tc.status)
				requireMediaStatus(t, media, sipMediaMeasured, "")
				if tc.expect == "none" {
					requireWarning(t, m, "media: no audio received (one-way audio, or the far end does not latch to the probe's NAT)")
				} else {
					requireStatus(t, result, "failure", "media: no audio received in 2.", "s; the probe offered a private address, set sdp_address if it is behind NAT")
				}
				if m.Call.Teardown != "bye_ok" || len(peer.packets()) == 0 {
					t.Fatalf("%s: teardown = %q, sent %d", tc.expect, m.Call.Teardown, len(peer.packets()))
				}
			}
		},
		"jitter": func(t *testing.T) {
			_, _, media := reliably(t, func() (CheckResult, *sipMetrics, *sipMediaMetrics) {
				peer := startMediaPeer(t, "127.0.0.1")
				u := startFakeUAS(t, "udp", mediaUAS{sdp: pcmuAnswer(peer), answered: func(u *fakeUAS, invite string) {
					peer.stream(probeMediaAddr(t, invite), mediaStream{delay: 600 * time.Millisecond, count: 110,
						offset: func(i int) time.Duration { return time.Duration(i%2) * 10 * time.Millisecond }})
				}}.handler())
				return runMediaCall(t, context.Background(), newInviteTestChecker(), mediaCallConfig(u, 3000, map[string]any{"expect": "audio"}, nil))
			})
			// Alternating 0/+10 ms transit makes |D| = 10 ms at every packet:
			// the A.8 estimate climbs towards 10 ms from 0.
			if j := media.Inbound.JitterMs; j == nil || *j < 6 || *j > 14 {
				t.Fatalf("jitter_ms = %v, want about 9 ms", j)
			}
		},
		"flood": func(t *testing.T) {
			peer := startMediaPeer(t, "127.0.0.1")
			u := startFakeUAS(t, "udp", mediaUAS{sdp: pcmuAnswer(peer), answered: func(u *fakeUAS, invite string) {
				peer.stream(probeMediaAddr(t, invite), mediaStream{delay: 700 * time.Millisecond, count: 10000, burst: true})
			}}.handler())
			start := time.Now()
			result, m, media := runMediaCall(t, context.Background(), newInviteTestChecker(), mediaCallConfig(u, 3000, map[string]any{"expect": "audio"}, nil))
			requireStatus(t, result, "success")
			if media.RX.Overflow == 0 {
				t.Fatalf("rx = %+v, want the record cap to hold", media.RX)
			}
			requireWarning(t, m, "media: inbound flood truncated")
			if took := time.Since(start); took > 8*time.Second {
				t.Fatalf("the call took %s, want it within its budget", took)
			}
		},
	})
}

func TestSIPMedia_Answer(t *testing.T) {
	// unusable runs a call whose answer the probe cannot use: it must send
	// nothing.
	unusable := func(t *testing.T, sdp string, expect, status, fragment string) (*sipMetrics, *sipMediaMetrics) {
		t.Helper()
		u := startFakeUAS(t, "udp", mediaUAS{sdp: sdp}.handler())
		c := newInviteTestChecker()
		var log sendLog
		log.attach(c)
		result, m, media := runMediaCall(t, context.Background(), c, mediaCallConfig(u, 3000, map[string]any{"expect": expect}, nil))
		requireStatus(t, result, status, fragment)
		if n := log.count(); n != 0 {
			t.Fatalf("sent %d datagrams for an unusable answer, want none", n)
		}
		return m, media
	}
	peerAddr := netip.MustParseAddrPort("127.0.0.1:40012")
	mediaTests(t, map[string]func(t *testing.T){
		"variations": func(t *testing.T) {
			for _, tc := range []struct {
				name, sdp, detail string
			}{
				{"inactive", mediaSDP(peerAddr, "m=audio %d RTP/AVP 0", "a=inactive"), "inactive"},
				{"port 0", mediaSDP(netip.AddrPortFrom(peerAddr.Addr(), 0), "m=audio %d RTP/AVP 0"), "inactive"},
				{"hold address", strings.Replace(mediaSDP(peerAddr, "m=audio %d RTP/AVP 0"), "c=IN IP4 127.0.0.1", "c=IN IP4 0.0.0.0", 1), "inactive"},
				{"g729 only", mediaSDP(peerAddr, "m=audio %d RTP/AVP 18"), "codec_not_offered"},
				{"ipv6 on an ipv4 flow", mediaSDP(netip.MustParseAddrPort("[::1]:40012"), "m=audio %d RTP/AVP 0"), "family"},
				{"srtp", mediaSDP(peerAddr, "m=audio %d RTP/SAVP 0"), "profile"},
			} {
				t.Run(tc.name, func(t *testing.T) {
					_, media := unusable(t, tc.sdp, "audio", "failure", "media: far end answered without a usable audio stream ("+tc.detail+")")
					requireMediaStatus(t, media, sipMediaUnusable, tc.detail)
					m, _ := unusable(t, tc.sdp, "none", "success", "")
					requireWarning(t, m, "media: far end answered without a usable audio stream ("+tc.detail+")")
				})
			}
		},
		"recvonly": func(t *testing.T) {
			sdp := mediaSDP(peerAddr, "m=audio %d RTP/AVP 0", "a=recvonly")
			_, media := unusable(t, sdp, "audio", "failure", "media: far end answered recvonly (it sends no audio)")
			requireMediaStatus(t, media, sipMediaUnusable, "recvonly")
		},
		"sendonly echo": func(t *testing.T) {
			sdp := mediaSDP(peerAddr, "m=audio %d RTP/AVP 0", "a=sendonly")
			unusable(t, sdp, "echo", "failure", "media: far end answered sendonly (it refuses our audio)")
		},
		// sendonly: RFC 3264 forbids the probe to send, so it only listens.
		"sendonly receives only": func(t *testing.T) {
			peer := startMediaPeer(t, "127.0.0.1")
			u := startFakeUAS(t, "udp", mediaUAS{sdp: mediaSDP(peer.addr, "m=audio %d RTP/AVP 0", "a=sendonly"), answered: func(u *fakeUAS, invite string) {
				peer.stream(probeMediaAddr(t, invite), mediaStream{count: 140})
			}}.handler())
			c := newInviteTestChecker()
			var log sendLog
			log.attach(c)
			result, m, media := runMediaCall(t, context.Background(), c, mediaCallConfig(u, 3000, map[string]any{"expect": "audio"}, nil))
			requireStatus(t, result, "success")
			requireWarning(t, m, "media: far end answered sendonly; its audio measured, none sent")
			if log.count() != 0 || media.Inbound == nil || media.Destination != "" {
				t.Fatalf("sent %d, inbound %+v, destination %q: want receive only", log.count(), media.Inbound, media.Destination)
			}
		},
		"recvonly measured only": func(t *testing.T) {
			peer := startMediaPeer(t, "127.0.0.1")
			u := startFakeUAS(t, "udp", mediaUAS{sdp: mediaSDP(peer.addr, "m=audio %d RTP/AVP 0", "a=recvonly")}.handler())
			result, m, _ := runMediaCall(t, context.Background(), newInviteTestChecker(), mediaCallConfig(u, 3000, map[string]any{"expect": "none"}, nil))
			requireStatus(t, result, "success")
			requireWarning(t, m, "media: far end answered recvonly (it sends no audio)")
			if len(peer.packets()) == 0 {
				t.Fatal("nothing sent to a recvonly far end on a measure-only call")
			}
		},
		// RFC 3264: the probe sends with the answer's number for the codec,
		// a dynamic one included, and receives in its own offer's numbering.
		"dynamic payload numbers": func(t *testing.T) {
			peer := startMediaPeer(t, "127.0.0.1")
			sdp := mediaSDP(peer.addr, "m=audio %d RTP/AVP 98 96", "a=rtpmap:98 PCMU/8000", "a=rtpmap:96 telephone-event/8000")
			u := startFakeUAS(t, "udp", mediaUAS{sdp: sdp, answered: func(u *fakeUAS, invite string) {
				peer.stream(probeMediaAddr(t, invite), mediaStream{count: 140, pt: 0}) // its own numbering is the offer's
			}}.handler())
			result, _, media := runMediaCall(t, context.Background(), newInviteTestChecker(), mediaCallConfig(u, 3000, map[string]any{"expect": "audio"}, nil))
			requireStatus(t, result, "success")
			if *media.TxPT != 98 || media.Inbound == nil || media.Inbound.PTMismatch != 0 {
				t.Fatalf("tx_payload_type = %d inbound = %+v", *media.TxPT, media.Inbound)
			}
			for _, p := range peer.packets() {
				if p.PayloadType != 98 {
					t.Fatalf("sent payload type %d, want the answer's 98", p.PayloadType)
				}
			}
		},
		"no sdp at all": func(t *testing.T) {
			_, media := unusable(t, "", "audio", "failure", "media: far end answered without a usable audio stream (no_sdp)")
			requireMediaStatus(t, media, sipMediaUnusable, "no_sdp")
		},
		// A bare 200 after a 183 with SDP on the same dialog: the 183's
		// answer stands in.
		"answer from the 18x": func(t *testing.T) {
			peer := startMediaPeer(t, "127.0.0.1")
			peer.reflect()
			u := startFakeUAS(t, "udp", mediaUAS{early: pcmuAnswer(peer)}.handler())
			result, m, media := runMediaCall(t, context.Background(), newInviteTestChecker(), mediaCallConfig(u, 3000, map[string]any{"expect": "echo"}, nil))
			requireStatus(t, result, "success")
			requireMediaStatus(t, media, sipMediaMeasured, "")
			if media.AnswerSource != "18x" || m.Call.SDPAnswer != nil {
				t.Fatalf("answer_source = %q sdp_answer = %+v, want the 18x's answer and no 2xx sdp_answer", media.AnswerSource, m.Call.SDPAnswer)
			}
			requireWarning(t, m, "media: answer taken from the 18x (the 200 carried no SDP)")
		},
		// A 2xx that is not the expected one never holds, so never sends.
		"status mismatch": func(t *testing.T) {
			peer := startMediaPeer(t, "127.0.0.1")
			u := startFakeUAS(t, "udp", mediaUAS{sdp: pcmuAnswer(peer), code: 202}.handler())
			c := newInviteTestChecker()
			var log sendLog
			log.attach(c)
			result, _, media := runMediaCall(t, context.Background(), c, mediaCallConfig(u, 3000, map[string]any{"expect": "audio"}, map[string]any{"expected_status": 200}))
			requireStatus(t, result, "failure", "status: 202 OK (expected 200)")
			requireMediaStatus(t, media, sipMediaNotHeld, "")
			if log.count() != 0 {
				t.Fatalf("sent %d datagrams on a call that never held", log.count())
			}
		},
	})
}

func TestSIPMedia_Policy(t *testing.T) {
	refused := func(t *testing.T, c *SIPChecker, sdp, expect string) (CheckResult, *sipMetrics, *sipMediaMetrics, *sendLog) {
		t.Helper()
		u := startFakeUAS(t, "udp", mediaUAS{sdp: sdp}.handler())
		var log sendLog
		log.attach(c)
		result, m, media := runMediaCall(t, context.Background(), c, mediaCallConfig(u, 3000, map[string]any{"expect": expect}, nil))
		requireNoAddress(t, result, m)
		return result, m, media, &log
	}
	blocking := func() *SIPChecker {
		c := newInviteTestChecker()
		c.blockPrivateIPs, c.allowedCIDRs = true, []*net.IPNet{mustCIDR("127.0.0.0/8")}
		return c
	}
	mediaTests(t, map[string]func(t *testing.T){
		"blocked by policy": func(t *testing.T) {
			result, _, media, log := refused(t, blocking(), mediaSDP(netip.MustParseAddrPort("10.0.0.1:40012"), "m=audio %d RTP/AVP 0"), "audio")
			requireStatus(t, result, "error", "media: far end's media address refused by worker policy (blocked_by_policy); nothing measured")
			requireMediaStatus(t, media, sipMediaRefused, "blocked_by_policy")
			if log.count() != 0 || media.Destination != "" {
				t.Fatalf("sent %d to a refused address", log.count())
			}
		},
		"well-known port": func(t *testing.T) {
			result, _, _, log := refused(t, newInviteTestChecker(), mediaSDP(netip.MustParseAddrPort("127.0.0.1:53"), "m=audio %d RTP/AVP 0"), "echo")
			requireStatus(t, result, "error", "media: far end's media address refused by worker policy (port); nothing measured")
			if log.count() != 0 {
				t.Fatalf("sent %d to port 53", log.count())
			}
		},
		"hostname measured only": func(t *testing.T) {
			sdp := strings.Replace(mediaSDP(netip.MustParseAddrPort("127.0.0.1:40012"), "m=audio %d RTP/AVP 0"), "c=IN IP4 127.0.0.1", "c=IN IP4 media.example.invalid", 1)
			result, m, media, log := refused(t, newInviteTestChecker(), sdp, "none")
			requireStatus(t, result, "success")
			requireWarning(t, m, "media: far end's media address refused by worker policy (hostname)")
			requireMediaStatus(t, media, sipMediaRefused, "hostname")
			if log.count() != 0 {
				t.Fatalf("sent %d with a hostname c=", log.count())
			}
		},
		// The answer names a hostname; the far end's audio comes from the
		// signalling peer's IP, so the probe answers there (peer latch).
		"peer latch": func(t *testing.T) {
			peer := startMediaPeer(t, "127.0.0.1")
			sdp := strings.Replace(mediaSDP(peer.addr, "m=audio %d RTP/AVP 0"), "c=IN IP4 127.0.0.1", "c=IN IP4 media.example.invalid", 1)
			u := startFakeUAS(t, "udp", mediaUAS{sdp: sdp, answered: func(u *fakeUAS, invite string) {
				peer.stream(probeMediaAddr(t, invite), mediaStream{count: 140})
			}}.handler())
			c := newInviteTestChecker()
			var log sendLog
			log.attach(c)
			result, m, media := runMediaCall(t, context.Background(), c, mediaCallConfig(u, 3000, map[string]any{"expect": "audio"}, nil))
			requireStatus(t, result, "success")
			requireMediaStatus(t, media, sipMediaMeasured, "hostname")
			requireWarning(t, m, "media: far end offered an unusable media address (hostname); latched to the signalling peer")
			if media.Latched != "peer" || media.Destination != peer.addr.String() || log.count() == 0 || len(peer.packets()) == 0 {
				t.Fatalf("latched %q destination %q sent %d, want audio sent to the peer's port", media.Latched, media.Destination, log.count())
			}
		},
		// The far end sends from another port than its SDP names: the probe
		// moves there once, and a third port later moves nothing.
		"port latch": func(t *testing.T) {
			peer := startMediaPeer(t, "127.0.0.1")
			closed := freeTestRTPPorts(t, 1)[0]
			third, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
			if err != nil {
				t.Fatal(err)
			}
			defer third.Close()
			sdp := mediaSDP(netip.AddrPortFrom(peer.addr.Addr(), uint16(closed)), "m=audio %d RTP/AVP 0")
			u := startFakeUAS(t, "udp", mediaUAS{sdp: sdp, answered: func(u *fakeUAS, invite string) {
				to := probeMediaAddr(t, invite)
				peer.stream(to, mediaStream{count: 140})
				peer.stream(to, mediaStream{delay: 1500 * time.Millisecond, count: 5, ssrc: 0x9f00aa01, seq0: 75, from: third})
			}}.handler())
			result, m, media := runMediaCall(t, context.Background(), newInviteTestChecker(), mediaCallConfig(u, 3000, map[string]any{"expect": "audio"}, nil))
			requireStatus(t, result, "success")
			requireWarning(t, m, "media: far end sends from another port than its SDP names; latched to it")
			if media.Latched != "port" || media.Destination != peer.addr.String() {
				t.Fatalf("latched %q destination %q, want the peer's real port %s", media.Latched, media.Destination, peer.addr)
			}
			if len(peer.packets()) == 0 {
				t.Fatal("nothing reached the latched port")
			}
		},
	})
}

func TestSIPMedia_Timeline(t *testing.T) {
	mediaTests(t, map[string]func(t *testing.T){
		// The callee hangs up 0.6 s in: too short to say anything.
		"callee hangs up early": func(t *testing.T) {
			peer := startMediaPeer(t, "127.0.0.1")
			u := startFakeUAS(t, "udp", mediaUAS{sdp: pcmuAnswer(peer), answered: func(u *fakeUAS, invite string) {
				u.sendAfter(600*time.Millisecond, uasBye(invite, "srv1", ""))
			}}.handler())
			result, m, media := runMediaCall(t, context.Background(), newInviteTestChecker(), mediaCallConfig(u, 3000, map[string]any{"expect": "audio"}, nil))
			requireStatus(t, result, "success")
			requireWarning(t, m, "too short to measure")
			requireMediaStatus(t, media, sipMediaTruncated, "")
		},
		// 2.2 s in: the audio assertion holds, quality thresholds do not.
		"callee hangs up before the quality window": func(t *testing.T) {
			peer := startMediaPeer(t, "127.0.0.1")
			u := startFakeUAS(t, "udp", mediaUAS{sdp: pcmuAnswer(peer), answered: func(u *fakeUAS, invite string) {
				peer.stream(probeMediaAddr(t, invite), lossyStream())
				u.sendAfter(2200*time.Millisecond, uasBye(invite, "srv1", ""))
			}}.handler())
			result, m, media := runMediaCall(t, context.Background(), newInviteTestChecker(), mediaCallConfig(u, 3000, map[string]any{"expect": "audio", "max_loss_pct": 1}, nil))
			requireStatus(t, result, "success")
			requireMediaStatus(t, media, sipMediaTruncated, "")
			requireWarning(t, m, "measured; quality thresholds not applied")
		},
		// No RTP leaves after the BYE, nor during the linger.
		"no audio after the bye": func(t *testing.T) {
			peer := startMediaPeer(t, "127.0.0.1")
			peer.reflect()
			u := startFakeUAS(t, "udp", mediaUAS{sdp: pcmuAnswer(peer)}.handler())
			c := newInviteTestChecker()
			var log sendLog
			log.attach(c)
			result, _, _ := runMediaCall(t, context.Background(), c, mediaCallConfig(u, 3000, map[string]any{"expect": "echo"}, nil))
			requireStatus(t, result, "success")
			bye := u.waitRequests("BYE", 1, time.Second)[0]
			if last := log.last(); last.IsZero() || !last.Before(bye.at) {
				t.Fatalf("last RTP at %s, BYE at %s: want every packet before the BYE", last, bye.at)
			}
			sent := log.count()
			time.Sleep(400 * time.Millisecond) // the linger runs on
			if log.count() != sent {
				t.Fatalf("%d datagrams sent after the result", log.count()-sent)
			}
		},
		// A worker shutting down mid-hold keeps the verdict and applies no
		// media rule.
		"shutdown mid-hold": func(t *testing.T) {
			peer := startMediaPeer(t, "127.0.0.1")
			u := startFakeUAS(t, "udp", mediaUAS{sdp: pcmuAnswer(peer)}.handler())
			ctx, cancel := context.WithCancel(context.Background())
			time.AfterFunc(1500*time.Millisecond, cancel)
			result, m, media := runMediaCall(t, ctx, newInviteTestChecker(), mediaCallConfig(u, 3000, map[string]any{"expect": "echo"}, nil))
			requireStatus(t, result, "success")
			requireMediaStatus(t, media, sipMediaInterrupted, "")
			if result.Interrupted || len(m.AssertionsFailed) != 0 || m.Call.Teardown != "bye_ok" {
				t.Fatalf("interrupted=%v assertions=%q teardown=%q", result.Interrupted, m.AssertionsFailed, m.Call.Teardown)
			}
		},
		// A stalling sender: the numbers are kept, quality is not judged.
		"busy worker": func(t *testing.T) {
			peer := startMediaPeer(t, "127.0.0.1")
			u := startFakeUAS(t, "udp", mediaUAS{sdp: pcmuAnswer(peer), answered: func(u *fakeUAS, invite string) {
				peer.stream(probeMediaAddr(t, invite), lossyStream())
			}}.handler())
			c := newInviteTestChecker()
			c.testMediaSlot = func(slot int) {
				if slot%5 == 4 {
					time.Sleep(30 * time.Millisecond)
				}
			}
			result, m, media := runMediaCall(t, context.Background(), c, mediaCallConfig(u, 3000, map[string]any{"expect": "audio", "max_loss_pct": 2}, nil))
			requireStatus(t, result, "success")
			requireWarning(t, m, "media: worker too busy to time media (send p95 ")
			if media.TX.Reliable || media.TX.SkippedSlots == 0 || media.Inbound.Lost != 5 {
				t.Fatalf("tx = %+v inbound lost = %d, want unreliable with the numbers kept", media.TX, media.Inbound.Lost)
			}
		},
		"slow scheduling": func(t *testing.T) {
			peer := startMediaPeer(t, "127.0.0.1")
			peer.reflect()
			u := startFakeUAS(t, "udp", mediaUAS{sdp: pcmuAnswer(peer)}.handler())
			c := newInviteTestChecker()
			buckets := []float64{0, 0.001, 0.01, math.Inf(1)}
			var samples int
			var mu sync.Mutex
			c.schedLatencies = func() *metrics.Float64Histogram {
				mu.Lock()
				defer mu.Unlock()
				samples++
				return &metrics.Float64Histogram{Buckets: buckets, Counts: []uint64{100, 0, uint64(10 * samples)}}
			}
			result, m, media := runMediaCall(t, context.Background(), c, mediaCallConfig(u, 3000, map[string]any{"expect": "echo", "min_mos": 4.3}, nil))
			requireStatus(t, result, "success")
			// The warning names the first cause found; on a loaded runner
			// the sender's own lateness can come first.
			requireWarning(t, m, "media: worker too busy to time media (")
			if media.TX.Reliable || media.TX.SchedP99Ms == nil || *media.TX.SchedP99Ms != 10 {
				t.Fatalf("tx = %+v", media.TX)
			}
		},
		// Every write refused (an egress firewall): an echo test measured
		// nothing about the target; an audio test still measures inbound.
		"writes refused": func(t *testing.T) {
			for _, tc := range []struct{ expect, status string }{{"echo", "error"}, {"audio", "success"}} {
				peer := startMediaPeer(t, "127.0.0.1")
				u := startFakeUAS(t, "udp", mediaUAS{sdp: pcmuAnswer(peer), answered: func(u *fakeUAS, invite string) {
					peer.stream(probeMediaAddr(t, invite), mediaStream{count: 140})
				}}.handler())
				c := newInviteTestChecker()
				log := sendLog{fail: &net.OpError{Op: "write", Net: "udp", Err: os.NewSyscallError("sendto", syscall.EPERM)}}
				log.attach(c)
				result, m, media := runMediaCall(t, context.Background(), c, mediaCallConfig(u, 3000, map[string]any{"expect": tc.expect}, nil))
				requireNoAddress(t, result, m)
				if tc.expect == "echo" {
					requireStatus(t, result, "error", "media: probe audio could not be sent (eperm)")
				} else {
					requireStatus(t, result, "success")
					requireWarning(t, m, "media: probe audio could not be sent (eperm)")
				}
				if media.TX.Errors == 0 || media.TX.ErrorClass != "eperm" || media.TX.Packets != 0 || len(peer.packets()) != 0 {
					t.Fatalf("%s: tx = %+v, peer got %d", tc.expect, media.TX, len(peer.packets()))
				}
			}
		},
		// A 503 that fails over to the next address is not a media session
		// of its own: the check counts one, the attempt that decided it.
		"failover counts one session": func(t *testing.T) {
			busy := startFakeUAS(t, "udp", func(u *fakeUAS, msg string) {
				if fMethod(msg) == "INVITE" {
					u.send(uasReply(msg, 503, "Service Unavailable", "srv1", ""))
				}
			})
			peer := startMediaPeer(t, "127.0.0.1")
			u := startFakeUAS(t, "udp", mediaUAS{sdp: pcmuAnswer(peer), answered: func(u *fakeUAS, invite string) {
				peer.stream(probeMediaAddr(t, invite), mediaStream{count: 140})
			}}.handler())
			c := newInviteTestChecker()
			c.resolver = &fakeSIPResolver{
				srv: map[string][]*net.SRV{"_sip._udp.example.com": {
					srvRecord("sbc1.example.com", busy.port, 10, 0),
					srvRecord("sbc2.example.com", u.port, 20, 0),
				}},
				ips: map[string][]string{"sbc1.example.com": {"127.0.0.1"}, "sbc2.example.com": {"127.0.0.1"}},
			}
			var sessions []string
			var mu sync.Mutex
			c.ConfigureMediaMetrics(sipMediaCounters{session: func(status string) {
				mu.Lock()
				sessions = append(sessions, status)
				mu.Unlock()
			}})
			result, m, media := runMediaCall(t, context.Background(), c, mediaCallConfig(u, 3000, map[string]any{"expect": "audio"},
				map[string]any{"host": "example.com", "port": 0, "resolution": "rfc3263"}))
			requireStatus(t, result, "success")
			requireAttemptOutcomes(t, m, "503", "ok")
			mu.Lock()
			defer mu.Unlock()
			if media.Status != sipMediaMeasured || !slices.Equal(sessions, []string{sipMediaMeasured}) {
				t.Fatalf("media status %q, sessions counted %q: want one measured session", media.Status, sessions)
			}
		},
		// The BYE goes unanswered and the far end keeps streaming: that is
		// evidence the call is still up, and billed.
		"far end still sending after the bye": func(t *testing.T) {
			peer := startMediaPeer(t, "127.0.0.1")
			u := startFakeUAS(t, "udp", mediaUAS{sdp: pcmuAnswer(peer), silentBye: true, answered: func(u *fakeUAS, invite string) {
				peer.stream(probeMediaAddr(t, invite), mediaStream{count: 300})
			}}.handler())
			c := newInviteTestChecker()
			c.callTimers.teardown = 3 * time.Second // the BYE waits; the far end streams on
			var evidence []string
			var mu sync.Mutex
			c.ConfigureMediaMetrics(sipMediaCounters{teardownUnconfirmed: func(e string) {
				mu.Lock()
				evidence = append(evidence, e)
				mu.Unlock()
			}})
			result, m, media := runMediaCall(t, context.Background(), c, mediaCallConfig(u, 3000, map[string]any{"expect": "audio"}, nil))
			requireStatus(t, result, "success")
			requireWarning(t, m, "teardown: BYE unconfirmed; far end still sending audio after the BYE")
			if slices.Contains(m.Warnings, sipByeUnconfirmedWarning) {
				t.Fatalf("warnings = %q, want the plain warning replaced", m.Warnings)
			}
			mu.Lock()
			defer mu.Unlock()
			if media.RX.AfterByePackets < sipMediaAfterByeEvidence || len(evidence) != 1 || evidence[0] != "audio_after_bye" {
				t.Fatalf("after_bye_packets = %d evidence = %q", media.RX.AfterByePackets, evidence)
			}
		},
		// A call without call.media is what it always was: no RTP, no media
		// block, the same offer.
		"without media": func(t *testing.T) {
			u := startFakeUAS(t, "udp", answeringUAS(-1, 0))
			c := newInviteTestChecker()
			var log sendLog
			log.attach(c)
			cfg := inviteConfig(u, map[string]any{"number": "100", "hold_ms": 1000}, nil)
			raw, _ := json.Marshal(cfg)
			result := c.Check(context.Background(), raw, 5)
			requireStatus(t, result, "success")
			if strings.Contains(string(result.MetricsData), `"media"`) || log.count() != 0 {
				t.Fatalf("metrics = %s sent %d: want no media", result.MetricsData, log.count())
			}
			invite := u.requests("INVITE")[0].raw
			sessID := regexp.MustCompile(`o=probara (\d+) `).FindStringSubmatch(fBody(invite))
			id, _ := strconv.ParseUint(sessID[1], 10, 64)
			offer := probeMediaAddr(t, invite)
			if want := string(buildSDPOffer("127.0.0.1", int(offer.Port()), id)); fBody(invite) != want {
				t.Fatalf("offer = %q, want %q", fBody(invite), want)
			}
		},
	})
}

// The worker refuses a media config it cannot honour before it resolves or
// sends anything, as a worker that predates a field does.
func TestSIPMedia_InvalidConfigPlacesNoCall(t *testing.T) {
	u := startFakeUAS(t, "udp", answeringUAS(-1, 0))
	for media, want := range map[string]string{
		`{"expect":"audio","dtmf":"1"}`: `invalid SIP config: call.media: unknown key "dtmf"`,
		`"on"`:                          "invalid SIP config: call.media: must be an object",
	} {
		raw := fmt.Sprintf(`{"host":"127.0.0.1","port":%d,"transport":"udp","method":"invite","call":{"number":"100","hold_ms":3000,"media":%s}}`, u.port, media)
		result := newInviteTestChecker().Check(context.Background(), json.RawMessage(raw), 5)
		requireStatus(t, result, "error", want)
	}
	if n := u.total(); n != 0 {
		t.Fatalf("the UAS got %d messages, want none", n)
	}
}

// The probe's tone and silence frames are what rtp builds: one ptime of
// samples per packet, timestamps advancing per slot.
func TestSIPMedia_SentFrames(t *testing.T) {
	t.Setenv("SIP_LOCALHOST_AS_HOST_GATEWAY", "")
	peer := startMediaPeer(t, "127.0.0.1")
	u := startFakeUAS(t, "udp", mediaUAS{sdp: mediaSDP(peer.addr, "m=audio %d RTP/AVP 8", "a=ptime:30")}.handler())
	result, _, media := runMediaCall(t, context.Background(), newInviteTestChecker(), mediaCallConfig(u, 3000, map[string]any{"expect": "audio"}, nil))
	requireStatus(t, result, "failure", "media: no audio received")
	got := peer.packets()
	if len(got) < 50 || media.PtimeMs != 30 || media.Codec != "PCMA/8000" {
		t.Fatalf("got %d packets, media = %+v", len(got), media)
	}
	for i, p := range got[:20] {
		if len(p.payload) != 240 || p.payload[0] != rtp.PCMA.Silence() || p.PayloadType != 8 {
			t.Fatalf("packet %d: %d bytes, first %#x, pt %d; want 30 ms of A-law silence", i, len(p.payload), p.payload[0], p.PayloadType)
		}
		if i > 0 {
			if p.Seq != got[i-1].Seq+1 || (p.Timestamp-got[i-1].Timestamp)%240 != 0 {
				t.Fatalf("packet %d: seq %d ts %d after %d/%d", i, p.Seq, p.Timestamp, got[i-1].Seq, got[i-1].Timestamp)
			}
		}
	}
	if got[0].SSRC == 0 {
		t.Fatal("the probe's SSRC is 0")
	}
}

func mustCIDR(s string) *net.IPNet {
	_, n, err := net.ParseCIDR(s)
	if err != nil {
		panic(err)
	}
	return n
}

func TestSIPBroadcastAddr(t *testing.T) {
	for prefix, want := range map[string]string{
		"10.20.1.7/24":   "10.20.1.255",
		"192.168.0.1/16": "192.168.255.255",
		"203.0.113.9/28": "203.0.113.15",
		"10.0.0.1/30":    "10.0.0.3",
	} {
		if got := sipBroadcastAddr(netip.MustParsePrefix(prefix)); got.String() != want {
			t.Errorf("sipBroadcastAddr(%s) = %s, want %s", prefix, got, want)
		}
	}
	// A /31 or /32 has no broadcast address to refuse: both ends are hosts.
	env := sipMediaEnv{peer: netip.MustParseAddr("10.0.0.0"), localNets: []netip.Prefix{netip.MustParsePrefix("10.0.0.0/31")}}
	if r := vetMediaDestination(netip.MustParseAddrPort("10.0.0.1:40000"), env); r != "" {
		t.Errorf("the far side of a /31 refused: %q", r)
	}
	if len(sipLocalIPv4Nets()) == 0 {
		t.Error("no IPv4 interface subnet found (loopback at least)")
	}
}
