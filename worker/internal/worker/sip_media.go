package worker

import (
	"errors"
	"fmt"
	"math"
	"math/rand/v2"
	"net"
	"net/netip"
	"runtime/metrics"
	"slices"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/yassinebenameur/probara/shared/models"
	"github.com/yassinebenameur/probara/shared/netguard"
	"github.com/yassinebenameur/probara/worker/internal/rtp"
)

// A test call's audio measurement (call.media). When the call reaches its
// hold, the probe exchanges G.711 RTP with the far end for the hold and
// measures what came back; the arithmetic is worker/internal/rtp, this file
// is the orchestration:
//
//	2xx (or its dialog's 18x) → prepareMedia: plan from the SDP answer, no I/O
//	HOLDING entry → start: arm the RTP reader, start the sender
//	teardown entry → stop: the sender stops first, so no RTP follows our BYE
//	rtp.Close() → finish: rtp.Analyze over the reader's records
//	applyMediaRules: the outcome, after the call's own thresholds
//
// The far end chooses where the probe sends, so every destination (the SDP
// address, and a latch target) goes through netguard.Policy.AllowsServerChosen
// before a packet leaves, and is never resolved from a hostname. Packets go
// from the bound, unconnected RTP socket (symmetric RTP, RFC 4961): a
// latching SBC finds a NATed probe from them, and an unconnected socket
// surfaces no ICMP errors, so the stream cannot probe ports. Media error and
// warning text names reason codes, never an address: it reaches every
// notification channel (present.Build copies the error).
//
// Inbound packets are analysed only from the SDP address's IP (once vetted)
// or the signalling peer's IP; anything else is counted as foreign, so a
// spoofer who guesses the port cannot score the call.

const (
	// sipMediaMaxDatagram is the largest RTP datagram analysed (an Ethernet
	// MTU); the reader's larger buffer tells a bigger one apart.
	sipMediaMaxDatagram = 1500
	// sipMediaMaxSources is how many source addresses rx.sources keeps.
	sipMediaMaxSources = 4
	// sipMediaEvidence is the packets (inbound) or echoes (outbound) that
	// show audio flowed in a direction.
	sipMediaEvidence = 5
	// sipMediaMinWindow is the measured window the audio and echo
	// assertions need; below it the call was too short to tell.
	sipMediaMinWindow = time.Second
	// sipMediaAfterByeEvidence is the far-end packets, arriving well after
	// our BYE, that say the call is still up when the BYE went unconfirmed.
	sipMediaAfterByeEvidence = 25
	// The self-timing guard: a worker whose sender slots run this late (p95),
	// skips more than this share of them, or whose goroutines wait this long
	// to run (p99) cannot time packets well enough to judge quality.
	sipMediaMaxLateness   = 5 * time.Millisecond
	sipMediaMaxSkippedPct = 5
	sipMediaMaxSchedP99   = 5 * time.Millisecond
	// sipMediaDefaultPtime is the packetisation time when the answer names
	// none the probe sends with.
	sipMediaDefaultPtime = 20 * time.Millisecond
)

// The media block's status (metrics_data.sip.call.media.status).
const (
	sipMediaMeasured    = "measured"
	sipMediaTruncated   = "truncated"   // the window was too short for quality thresholds
	sipMediaInterrupted = "interrupted" // the worker shut down mid-hold
	sipMediaNotHeld     = "not_held"    // the call never reached its hold
	sipMediaUnusable    = "unusable_answer"
	sipMediaRefused     = "refused" // the SDP address was refused and nothing latched
)

// Reasons an answer is unusable or its address refused (media.detail),
// besides the netguard.Reason* codes AllowsServerChosen gives.
const (
	sipMediaNoSDP     = "no_sdp"
	sipMediaNoAudio   = "no_audio"
	sipMediaProfile   = "profile" // the m= proto is not RTP/AVP or RTP/AVPF
	sipMediaInactive  = "inactive"
	sipMediaNoAddress = "no_address"
	sipMediaNoCodec   = "codec_not_offered"
	sipMediaFamily    = "family" // the address is not of the flow's IP family
	sipMediaRecvOnly  = "recvonly"
	sipMediaSendOnly  = "sendonly"
	sipMediaHostname  = "hostname"  // never resolved
	sipMediaSelf      = "self"      // the probe's own RTP or RTCP port
	sipMediaBroadcast = "broadcast" // a local subnet's broadcast or network address
)

// How the destination moved (media.latched).
const (
	sipMediaLatchPort = "port" // the SDP address's IP, another port
	sipMediaLatchPeer = "peer" // the signalling peer, the SDP address refused
)

// sipMediaPtimes are the packetisation times the probe sends with when the
// answer asks for one of them (a=ptime).
var sipMediaPtimes = []int{10, 20, 30, 40, 60}

// sipMediaPlan is what the SDP answer lets the probe do, decided before the
// hold and without I/O.
type sipMediaPlan struct {
	answerSource string
	// status is "" for a usable stream, else sipMediaUnusable or
	// sipMediaRefused, with the reason in detail. A refused address still
	// receives, and the peer latch may give it a destination.
	status, detail string

	codec     string // as the answer names it, e.g. PCMU/8000
	law       rtp.Law
	txPT      uint8 // the answer's number for the codec (RFC 3264 §6.1)
	ptime     time.Duration
	direction string
	send      bool // false for a sendonly answer: RFC 3264 forbids us to send

	dst   netip.AddrPort // the vetted destination; invalid when refused
	sdpIP netip.Addr     // the SDP address's IP once vetted: an accepted source
}

// sipMediaEnv is what prepareMedia needs about the call besides the answer.
type sipMediaEnv struct {
	expect string
	policy netguard.Policy
	peer   netip.Addr     // the signalling peer
	local  netip.AddrPort // the RTP socket's bound address
	// loopbackMirror: SIP_LOCALHOST_AS_HOST_GATEWAY rewrote a loopback
	// signalling host, so a loopback c= means the developer's machine too.
	loopbackMirror bool
	// localNets are the worker's IPv4 interface subnets (sipLocalIPv4Nets),
	// whose broadcast and network addresses are refused.
	localNets []netip.Prefix
}

// prepareMedia plans the call's media from the SDP answer (nil: none had
// an audio stream). It vets the destination and decides the codec, ptime and
// direction; nothing is sent.
func prepareMedia(answer *sipSDPAnswer, source string, env sipMediaEnv) *sipMediaPlan {
	plan := &sipMediaPlan{answerSource: source, ptime: sipMediaDefaultPtime, send: true}
	unusable := func(detail string) *sipMediaPlan {
		plan.status, plan.detail = sipMediaUnusable, detail
		return plan
	}
	if answer == nil {
		return unusable(sipMediaNoAudio)
	}
	plan.direction = answer.Direction
	if !strings.EqualFold(answer.proto, "RTP/AVP") && !strings.EqualFold(answer.proto, "RTP/AVPF") {
		return unusable(sipMediaProfile)
	}
	if answer.port == 0 || answer.Direction == "inactive" {
		return unusable(sipMediaInactive)
	}
	if answer.connAddr == "" {
		return unusable(sipMediaNoAddress)
	}
	if answer.connIP != nil && answer.connIP.IsUnspecified() {
		return unusable(sipMediaInactive) // c=0.0.0.0: the RFC 2543 hold
	}
	for _, pt := range answer.payloads {
		name, rate, _ := strings.Cut(answer.codecFor(pt), "/")
		if rate != "8000" && !strings.HasPrefix(rate, "8000/") {
			continue
		}
		switch {
		case strings.EqualFold(name, "PCMU"):
			plan.law = rtp.PCMU
		case strings.EqualFold(name, "PCMA"):
			plan.law = rtp.PCMA
		default:
			continue
		}
		plan.codec, plan.txPT = sipMetricsString(answer.codecFor(pt)), uint8(pt)
		break
	}
	if plan.codec == "" {
		return unusable(sipMediaNoCodec)
	}
	if slices.Contains(sipMediaPtimes, answer.ptime) {
		plan.ptime = time.Duration(answer.ptime) * time.Millisecond
	}
	switch answer.Direction {
	case "recvonly":
		// The far end will not send: only measure-only calls go on.
		if env.expect != models.SIPMediaExpectNone {
			return unusable(sipMediaRecvOnly)
		}
	case "sendonly":
		plan.send = false
		if env.expect == models.SIPMediaExpectEcho {
			return unusable(sipMediaSendOnly)
		}
	}

	refuse := func(reason string) *sipMediaPlan {
		plan.status, plan.detail = sipMediaRefused, reason
		return plan
	}
	if answer.connIsHostname {
		return refuse(sipMediaHostname)
	}
	ip, ok := netip.AddrFromSlice(answer.connIP)
	if !ok {
		return refuse(sipMediaHostname)
	}
	ip = ip.Unmap()
	addrType := "IP6"
	if ip.Is4() {
		addrType = "IP4"
	}
	// The pair is bound on the flow's local IP, so it can only reach its own
	// family; an address type that contradicts the literal is malformed.
	if local := env.local.Addr().Unmap(); answer.connAddrType != addrType ||
		(!local.IsUnspecified() && ip.Is4() != local.Is4()) {
		return unusable(sipMediaFamily)
	}
	if env.loopbackMirror && ip.IsLoopback() && env.peer.IsValid() {
		ip = env.peer
	}
	dst := netip.AddrPortFrom(ip, uint16(answer.port))
	if reason := vetMediaDestination(dst, env); reason != "" {
		return refuse(reason)
	}
	plan.dst, plan.sdpIP = dst, ip
	return plan
}

// vetMediaDestination is the reason the probe may not send to dst, or "":
// AllowsServerChosen, plus the probe's own RTP and RTCP ports and the
// broadcast and network addresses of the worker's own subnets. A subnet
// broadcast looks like any unicast address to the range table, yet a write
// to it reaches every host on the segment; the media sockets also clear
// SO_BROADCAST (sipRTPSocketControl), so the kernel refuses one this list
// misses, but only as a write error.
func vetMediaDestination(dst netip.AddrPort, env sipMediaEnv) string {
	local := env.local.Addr().Unmap()
	if (dst.Addr() == local || (local.IsUnspecified() && dst.Addr().IsLoopback())) &&
		(dst.Port() == env.local.Port() || dst.Port() == env.local.Port()+1) {
		return sipMediaSelf
	}
	if ip := dst.Addr().Unmap(); ip.Is4() {
		for _, p := range env.localNets {
			if p.Bits() <= 30 && p.Contains(ip) && (ip == p.Masked().Addr() || ip == sipBroadcastAddr(p)) {
				return sipMediaBroadcast
			}
		}
	}
	var peer net.IP
	if env.peer.IsValid() {
		peer = env.peer.AsSlice()
	}
	err := env.policy.AllowsServerChosen(net.UDPAddrFromAddrPort(dst), peer)
	var refusal *netguard.ServerChosenError
	switch {
	case err == nil:
		return ""
	case errors.As(err, &refusal):
		return refusal.Reason
	default:
		return netguard.ReasonBlockedByPolicy
	}
}

// sipLocalIPv4Nets lists the worker's IPv4 interface subnets; nil when the
// interfaces cannot be read (the sockets' cleared SO_BROADCAST still holds).
func sipLocalIPv4Nets() []netip.Prefix {
	addrs, err := net.InterfaceAddrs()
	if err != nil {
		return nil
	}
	var nets []netip.Prefix
	for _, a := range addrs {
		ipNet, ok := a.(*net.IPNet)
		if !ok {
			continue
		}
		ip4 := ipNet.IP.To4()
		ones, bits := ipNet.Mask.Size()
		if ip4 == nil || bits != 32 {
			continue
		}
		nets = append(nets, netip.PrefixFrom(netip.AddrFrom4([4]byte(ip4)), ones))
	}
	return nets
}

// sipBroadcastAddr is p's directed broadcast address (every host bit set).
func sipBroadcastAddr(p netip.Prefix) netip.Addr {
	a := p.Masked().Addr().As4()
	v := uint32(a[0])<<24 | uint32(a[1])<<16 | uint32(a[2])<<8 | uint32(a[3])
	v |= ^uint32(0) >> p.Bits()
	return netip.AddrFrom4([4]byte{byte(v >> 24), byte(v >> 16), byte(v >> 8), byte(v)})
}

// sipMediaCounters feed the worker's media metrics; nil funcs are skipped.
type sipMediaCounters struct {
	session             func(status string) // sip_media_sessions_total{outcome}
	refused             func(reason string) // sip_media_refused_total{reason}
	rxDropped           func(reason string, n int)
	unreliable          func()
	teardownUnconfirmed func(evidence string) // sip_call_teardown_unconfirmed_total{evidence}
}

// ConfigureMediaMetrics wires the media counters.
func (c *SIPChecker) ConfigureMediaMetrics(counters sipMediaCounters) {
	c.mediaCounters = counters
}

// sipMediaSession is one call's media. The call goroutine owns the plan and
// the timeline; the sender goroutine owns what it sent until stop joins it;
// the RTP reader owns what it recorded until the pair's Close joins it. The
// destination is the one thing two goroutines share while both run (a latch
// moves it), so it is atomic.
type sipMediaSession struct {
	cfg      *models.SIPMediaConfig
	pair     *sipRTPPair
	env      sipMediaEnv
	counters *sipMediaCounters
	// sendTo, slotHook and schedRead are SIPChecker's test interposers.
	sendTo    func(b []byte, dst netip.AddrPort) error
	slotHook  func(slot int)
	schedRead func() *metrics.Float64Histogram

	privateOffer  bool // the offer named a private address of the worker's own
	sdpAddressSet bool
	hold          time.Duration
	recordsCap    int

	// base is the monotonic origin of every offset below, so an NTP step
	// during the call cannot show up as jitter or round trip.
	base       time.Time
	ssrc       uint32
	seq0       uint16
	ts0        uint32
	plan       *sipMediaPlan
	held       bool
	stopped    bool
	interrupt  bool
	startAt    time.Duration
	stopAt     time.Duration
	byeAt      time.Duration
	schedStart *metrics.Float64Histogram
	schedP99   float64 // ms, over the window; NaN when unknown

	dst  atomic.Pointer[netip.AddrPort]
	done chan struct{}
	wg   sync.WaitGroup

	// The sender's.
	sent      []rtp.Sent
	lateness  []time.Duration
	skipped   int
	attempts  int
	writeErrs [sipMediaWriteClasses]int

	// The reader's.
	records   []rtp.Record
	packets   int
	invalid   int
	foreign   int
	overflow  int
	sources   []netip.AddrPort
	latched   string
	latchDone bool

	report rtp.Report // finish's
	mm     *sipMediaMetrics
}

// newMediaSession starts a call's media state when call.media is set; nil
// otherwise, and every method of a nil session is a no-op.
func (s *sipCall) newMediaSession(pair *sipRTPPair, localIP net.IP) *sipMediaSession {
	media := s.config.Call.Media
	if media == nil {
		return nil
	}
	c := s.c
	m := &sipMediaSession{
		cfg:           media,
		pair:          pair,
		counters:      &c.mediaCounters,
		sendTo:        c.testSendTo,
		slotHook:      c.testMediaSlot,
		schedRead:     c.schedLatencies,
		sdpAddressSet: s.config.Call.SDPAddress != "",
		privateOffer:  netguard.IsPrivateOrReserved(localIP),
		hold:          s.hold,
		base:          time.Now(),
		ssrc:          rand.Uint32() | 1, // never 0, which some stacks treat as unset
		seq0:          uint16(rand.Uint32()),
		ts0:           rand.Uint32(),
		schedP99:      math.NaN(),
		done:          make(chan struct{}),
	}
	m.env = sipMediaEnv{
		expect: media.Expect,
		policy: netguard.Policy{BlockPrivate: c.blockPrivateIPs, AllowedCIDRs: c.allowedCIDRs},
		peer:   sipAddrIP(s.flow.peer),
		loopbackMirror: shouldRewriteSIPLoopbackToHostGateway() && s.metrics.Target != nil &&
			s.metrics.Target.Host == "host.docker.internal",
		localNets: sipLocalIPv4Nets(),
	}
	if local, ok := pair.rtp.LocalAddr().(*net.UDPAddr); ok {
		m.env.local = local.AddrPort()
	}
	// Room for four times the far end's packets at the shortest ptime, so a
	// far end that duplicates or bursts still fits; a flood beyond it is
	// counted, not kept.
	m.recordsCap = 4*int(s.hold/(10*time.Millisecond)) + 256
	return m
}

// sipAddrIP is a flow address's IP, unmapped.
func sipAddrIP(addr net.Addr) netip.Addr {
	var ip net.IP
	switch a := addr.(type) {
	case *net.UDPAddr:
		ip = a.IP
	case *net.TCPAddr:
		ip = a.IP
	}
	out, _ := netip.AddrFromSlice(ip)
	return out.Unmap()
}

// prepare plans the media from the answered dialog's SDP answer.
func (m *sipMediaSession) prepare(answer *sipSDPAnswer, source string) {
	if m == nil {
		return
	}
	m.plan = prepareMedia(answer, source, m.env)
}

// start runs when the call enters its hold: it arms the reader and starts
// the sender. A plan the answer made unusable starts nothing.
func (m *sipMediaSession) start(at time.Time) {
	if m == nil || m.held {
		return
	}
	m.held = true
	m.startAt = at.Sub(m.base)
	m.schedStart = m.readSched()
	if m.plan == nil || m.plan.status == sipMediaUnusable {
		return
	}
	m.records = make([]rtp.Record, 0, m.recordsCap)
	if m.plan.dst.IsValid() && m.plan.send {
		dst := m.plan.dst
		m.dst.Store(&dst)
	}
	m.pair.arm(m.onRTP, nil)
	if m.plan.send {
		m.wg.Add(1)
		go m.sendLoop(at)
	}
}

// stop ends the measured window: the sender stops and is joined. It runs as
// teardown's first step, before any BYE, and again (a no-op) when the call
// ends, so a sender never outlives its call.
func (m *sipMediaSession) stop(at time.Time) {
	if m == nil || !m.held || m.stopped {
		return
	}
	m.stopped = true
	m.stopAt = at.Sub(m.base)
	close(m.done)
	m.wg.Wait()
	m.schedP99 = schedP99Ms(m.schedStart, m.readSched())
}

// markBye records when our BYE to the answered dialog left: far-end audio
// long after it says the far end never saw it.
func (m *sipMediaSession) markBye(at time.Time) {
	if m == nil || !m.held || m.byeAt != 0 {
		return
	}
	m.byeAt = at.Sub(m.base)
}

// sendLoop sends one frame per ptime on an absolute schedule from t0, never
// by accumulating ticks, so a late wake-up does not shift every later frame.
// A slot more than a frame late is skipped (the sequence number stays
// contiguous; the timestamp moves on with the clock) and its lateness kept
// for the self-timing guard. Nothing is sent while there is no destination
// (a refused address waiting for the peer latch). The content is fixed: the
// 1004 Hz tone with the echo marker in echo mode, else digital silence.
func (m *sipMediaSession) sendLoop(t0 time.Time) {
	defer m.wg.Done()
	plan := m.plan
	samples := int(plan.ptime * rtp.ClockRate / time.Second)
	echo := m.cfg.Expect == models.SIPMediaExpectEcho
	payload := make([]byte, 0, samples)
	if !echo {
		payload = rtp.AppendSilence(payload, plan.law, samples)
	}
	packet := make([]byte, 0, rtp.HeaderSize+samples)
	slots := int(m.hold/plan.ptime) + 16
	m.lateness = make([]time.Duration, 0, slots)
	m.sent = make([]rtp.Sent, 0, slots)

	timer := time.NewTimer(time.Hour)
	defer timer.Stop()
	seq, first := m.seq0, true
	for slot := 0; ; slot++ {
		due := t0.Add(time.Duration(slot) * plan.ptime)
		if wait := time.Until(due); wait > 0 {
			timer.Reset(wait)
			select {
			case <-m.done:
				return
			case <-timer.C:
			}
		} else {
			select {
			case <-m.done:
				return
			default:
			}
		}
		if m.slotHook != nil {
			m.slotHook(slot)
		}
		late := time.Since(due)
		m.lateness = append(m.lateness, late)
		if late > plan.ptime {
			m.skipped++
			continue
		}
		dst := m.dst.Load()
		if dst == nil {
			continue
		}
		if echo {
			payload = rtp.AppendTone(payload[:0], plan.law, slot*samples, samples)
			rtp.PutEchoMarker(payload, seq, m.ssrc)
		}
		packet = rtp.AppendPacket(packet[:0], rtp.Header{
			Marker: first, PayloadType: plan.txPT, Seq: seq,
			Timestamp: m.ts0 + uint32(slot*samples), SSRC: m.ssrc,
		}, payload)
		at := time.Now()
		m.attempts++
		if err := m.write(packet, *dst); err != nil {
			m.writeErrs[sipMediaWriteClass(err)]++
			continue
		}
		m.sent = append(m.sent, rtp.Sent{At: at.Sub(m.base), Seq: seq})
		seq++
		first = false
	}
}

func (m *sipMediaSession) write(b []byte, dst netip.AddrPort) error {
	if m.sendTo != nil {
		if err := m.sendTo(b, dst); err != nil {
			return err
		}
	}
	_, err := m.pair.rtp.WriteToUDPAddrPort(b, dst)
	return err
}

// The classes of a failed write (tx.error_class).
const (
	sipMediaWriteOther = iota
	sipMediaWriteEPERM
	sipMediaWriteUnreachable
	sipMediaWriteNoBufs
	sipMediaWriteClasses
)

var sipMediaWriteClassNames = [sipMediaWriteClasses]string{"other", "eperm", "unreachable", "nobufs"}

// sipMediaWriteClass sorts a write error: an egress firewall (EPERM,
// EACCES), no route (ENETUNREACH, EHOSTUNREACH), a full socket buffer
// (ENOBUFS), or anything else.
func sipMediaWriteClass(err error) int {
	switch {
	case errors.Is(err, syscall.EPERM), errors.Is(err, syscall.EACCES):
		return sipMediaWriteEPERM
	case errors.Is(err, syscall.ENETUNREACH), errors.Is(err, syscall.EHOSTUNREACH):
		return sipMediaWriteUnreachable
	case errors.Is(err, syscall.ENOBUFS):
		return sipMediaWriteNoBufs
	}
	return sipMediaWriteOther
}

// onRTP is the RTP socket's sink, on the reader goroutine. It keeps a record
// of each packet worth analysing and decides the latch.
func (m *sipMediaSession) onRTP(b []byte, src netip.AddrPort, at time.Time) {
	m.packets++
	if len(b) > sipMediaMaxDatagram {
		m.invalid++
		return
	}
	if rtp.IsRTCP(b) {
		return // RTCP multiplexed on the RTP port (RFC 5761): not audio
	}
	if len(m.sources) < sipMediaMaxSources && !slices.Contains(m.sources, src) {
		m.sources = append(m.sources, src)
	}
	if !m.accepts(src.Addr()) {
		m.foreign++
		return
	}
	p, err := rtp.Parse(b)
	if err != nil {
		m.invalid++
		return
	}
	m.latch(src)
	echoSeq, match := rtp.MatchEcho(p, m.ssrc, m.cfg.Expect == models.SIPMediaExpectEcho)
	if len(m.records) == cap(m.records) {
		m.overflow++
		return
	}
	m.records = append(m.records, rtp.Record{
		At: at.Sub(m.base), SSRC: p.SSRC, TS: p.Timestamp, Seq: p.Seq,
		EchoSeq: echoSeq, PT: p.PayloadType, Marker: p.Marker, Echo: match,
	})
}

// accepts reports a source whose packets are analysed: the vetted SDP
// address's IP or the signalling peer's, on any port.
func (m *sipMediaSession) accepts(ip netip.Addr) bool {
	return (m.plan.sdpIP.IsValid() && ip == m.plan.sdpIP) || (m.env.peer.IsValid() && ip == m.env.peer)
}

// latch moves the destination once, at most, to where the far end really
// sends from (restricted latching). Port latch: until a packet came from the
// exact SDP address, the first from the SDP address's IP on another port
// moves the send port there. Peer latch: when the SDP address was refused,
// the first packet from the signalling peer's IP becomes the destination.
// The send IP never moves to any other address, and the target is vetted
// again, so a spoofer can at most move the probe's audio to another port of
// a host it was already allowed to reach.
func (m *sipMediaSession) latch(src netip.AddrPort) {
	if m.latchDone || !m.plan.send {
		return
	}
	var kind string
	switch {
	case m.plan.dst.IsValid():
		if src == m.plan.dst {
			m.latchDone = true // it sends from where it said: nothing to move
			return
		}
		if src.Addr() != m.plan.dst.Addr() {
			return
		}
		kind = sipMediaLatchPort
	case m.plan.status == sipMediaRefused && src.Addr() == m.env.peer:
		kind = sipMediaLatchPeer
	default:
		return
	}
	m.latchDone = true
	if vetMediaDestination(src, m.env) != "" {
		return
	}
	m.dst.Store(&src)
	m.latched = kind
}

// readSched samples the process's goroutine scheduling latencies.
func (m *sipMediaSession) readSched() *metrics.Float64Histogram {
	if m.schedRead != nil {
		return m.schedRead()
	}
	return readSchedLatencies()
}

const schedLatenciesMetric = "/sched/latencies:seconds"

func readSchedLatencies() *metrics.Float64Histogram {
	sample := []metrics.Sample{{Name: schedLatenciesMetric}}
	metrics.Read(sample)
	if sample[0].Value.Kind() != metrics.KindFloat64Histogram {
		return nil
	}
	return sample[0].Value.Float64Histogram()
}

// schedP99Ms is the 99th percentile, in ms, of the scheduling latencies
// between two samples of the cumulative histogram (an upper bucket bound, so
// it errs high); NaN when either is missing or nothing was scheduled. Arrival
// times are taken on the reader goroutine, so a worker whose goroutines wait
// to run reads bunched arrivals as jitter and late loss on the far end's
// side; the sender's own lateness is only a proxy for that.
func schedP99Ms(from, to *metrics.Float64Histogram) float64 {
	if from == nil || to == nil || len(from.Counts) != len(to.Counts) || len(to.Buckets) != len(to.Counts)+1 {
		return math.NaN()
	}
	var total uint64
	delta := make([]uint64, len(to.Counts))
	for i := range to.Counts {
		if to.Counts[i] >= from.Counts[i] {
			delta[i] = to.Counts[i] - from.Counts[i]
		}
		total += delta[i]
	}
	if total == 0 {
		return math.NaN()
	}
	want := uint64(math.Ceil(0.99 * float64(total)))
	var seen uint64
	for i, n := range delta {
		seen += n
		if seen >= want {
			bound := to.Buckets[i+1]
			if math.IsInf(bound, 1) {
				bound = to.Buckets[i]
			}
			return bound * 1000
		}
	}
	return math.NaN()
}

// sipMediaMetrics is metrics_data.sip.call.media.
type sipMediaMetrics struct {
	Expect       string `json:"expect"`
	Status       string `json:"status"`
	Detail       string `json:"detail,omitempty"`
	AnswerSource string `json:"answer_source,omitempty"`
	Codec        string `json:"codec,omitempty"`
	TxPT         *int   `json:"tx_payload_type,omitempty"`
	PtimeMs      int    `json:"ptime_ms,omitempty"`
	Direction    string `json:"direction,omitempty"`
	// Destination is where the probe's audio went (after any latch); absent
	// when nothing was sent. Only metrics_data names addresses.
	Destination string `json:"destination,omitempty"`
	Latched     string `json:"latched,omitempty"`
	WindowMs    int64  `json:"window_ms"`
	WarmupMs    int64  `json:"warmup_ms"`

	TX *sipMediaTx `json:"tx,omitempty"`
	RX *sipMediaRx `json:"rx,omitempty"`

	Inbound   *rtp.InboundStats `json:"inbound,omitempty"`
	RoundTrip *rtp.EchoStats    `json:"round_trip,omitempty"`

	Model          string   `json:"model,omitempty"`
	Assumptions    []string `json:"assumptions,omitempty"`
	TwoWay         string   `json:"two_way,omitempty"`
	TwoWayEvidence []string `json:"two_way_evidence,omitempty"`
}

type sipMediaTx struct {
	Packets      int      `json:"packets"`
	SkippedSlots int      `json:"skipped_slots"`
	TimerP95Ms   float64  `json:"timer_p95_ms"`
	SchedP99Ms   *float64 `json:"sched_p99_ms,omitempty"`
	// Reliable says the worker's own timing was trustworthy (sender and
	// receiver alike); without it no quality threshold applies.
	Reliable   bool   `json:"reliable"`
	Errors     int    `json:"errors,omitempty"`
	ErrorClass string `json:"error_class,omitempty"`
}

type sipMediaRx struct {
	Packets         int      `json:"packets"`
	Invalid         int      `json:"invalid"`
	Foreign         int      `json:"foreign"`
	Overflow        int      `json:"overflow"`
	UnknownPT       int      `json:"unknown_pt"`
	Sources         []string `json:"sources,omitempty"`
	AfterByePackets int      `json:"after_bye_packets"`
	FirstPacketMs   *int64   `json:"first_packet_ms,omitempty"`
}

// window is the measured inbound window, [start + warm-up, stop].
func (m *sipMediaSession) window() time.Duration {
	return max(m.stopAt-m.startAt-rtp.Warmup, 0)
}

// reliable is the self-timing guard over the sender's lateness and the
// process's scheduling latency.
func (m *sipMediaSession) reliable() (ok bool, why string) {
	if len(m.lateness) > 0 {
		p95 := sipLatenessP95(m.lateness)
		if p95 > sipMediaMaxLateness || m.skipped*100 > sipMediaMaxSkippedPct*len(m.lateness) {
			return false, "send p95 " + sipMediaNum(float64(p95)/float64(time.Millisecond), 1) + "ms"
		}
	}
	if !math.IsNaN(m.schedP99) && m.schedP99 > float64(sipMediaMaxSchedP99)/float64(time.Millisecond) {
		return false, "scheduling p99 " + sipMediaNum(m.schedP99, 1) + "ms"
	}
	return true, ""
}

func sipLatenessP95(lateness []time.Duration) time.Duration {
	sorted := slices.Clone(lateness)
	slices.Sort(sorted)
	i := (95*len(sorted)+99)/100 - 1
	return sorted[min(max(i, 0), len(sorted)-1)]
}

// finish analyses the call's media once the pair's Close has joined the
// reader (the sender was joined by stop), and builds the media block. Nil
// without a session.
func (m *sipMediaSession) finish(interrupted bool) *sipMediaMetrics {
	if m == nil {
		return nil
	}
	m.stop(time.Now()) // teardown stopped it already; stop is idempotent
	m.interrupt = interrupted && m.held
	mm := &sipMediaMetrics{Expect: m.cfg.Expect, WarmupMs: rtp.Warmup.Milliseconds()}
	m.mm = mm
	if plan := m.plan; plan != nil {
		mm.AnswerSource = plan.answerSource
		mm.Detail = plan.detail
		mm.Direction = plan.direction
		if plan.codec != "" {
			mm.Codec = plan.codec
			pt := int(plan.txPT)
			mm.TxPT = &pt
			mm.PtimeMs = int(plan.ptime / time.Millisecond)
		}
	}
	switch {
	case !m.held:
		mm.Status = sipMediaNotHeld
		return mm
	case m.plan == nil:
		mm.Status, mm.Detail = sipMediaUnusable, sipMediaNoSDP
		return mm
	case m.plan.status == sipMediaUnusable:
		mm.Status = sipMediaUnusable
		return mm
	}

	mm.WindowMs = m.window().Milliseconds()
	m.report = rtp.Analyze(m.records, rtp.Config{
		Answer:  m.startAt,
		Stop:    m.stopAt,
		Bye:     m.byeAt,
		AudioPT: uint8(m.plan.law),
		PtimeMs: mm.PtimeMs,
		Sent:    m.sent,
	})
	rep := &m.report
	mm.Inbound, mm.RoundTrip = rep.Inbound, rep.RoundTrip
	if (rep.Inbound != nil && rep.Inbound.Quality != nil) || (rep.RoundTrip != nil && rep.RoundTrip.Quality != nil) {
		mm.Model, mm.Assumptions = rtp.Model, []string{rtp.AssumptionPLC}
	}

	reliable, _ := m.reliable()
	tx := &sipMediaTx{Packets: len(m.sent), SkippedSlots: m.skipped, Reliable: reliable}
	if len(m.lateness) > 0 {
		tx.TimerP95Ms = sipRound(float64(sipLatenessP95(m.lateness))/float64(time.Millisecond), 2)
	}
	if !math.IsNaN(m.schedP99) {
		v := sipRound(m.schedP99, 2)
		tx.SchedP99Ms = &v
	}
	if errs := m.writeErrors(); errs > 0 {
		tx.Errors, tx.ErrorClass = errs, m.writeErrorClass()
	}
	mm.TX = tx
	if len(m.sent) > 0 {
		if dst := m.dst.Load(); dst != nil {
			mm.Destination = sipMetricsString(dst.String())
		}
	}
	mm.Latched = m.latched

	rx := &sipMediaRx{
		Packets: m.packets, Invalid: m.invalid, Foreign: m.foreign, Overflow: m.overflow,
		UnknownPT: rep.UnknownPT, AfterByePackets: rep.AfterBye,
	}
	for _, src := range m.sources {
		rx.Sources = append(rx.Sources, sipMetricsString(src.String()))
	}
	if len(m.records) > 0 {
		first := rep.FirstPacket.Milliseconds()
		rx.FirstPacketMs = &first
	}
	mm.RX = rx

	inbound, outbound := m.inboundEvidence(), m.outboundEvidence()
	switch {
	case inbound && outbound:
		mm.TwoWay = "verified"
	case inbound:
		mm.TwoWay = "inbound_only"
	case outbound:
		mm.TwoWay = "outbound_only"
	default:
		mm.TwoWay = "none"
	}
	if inbound {
		mm.TwoWayEvidence = append(mm.TwoWayEvidence, "rx")
	}
	if outbound {
		mm.TwoWayEvidence = append(mm.TwoWayEvidence, "echo")
	}

	switch {
	case m.interrupt:
		mm.Status = sipMediaInterrupted
	case m.plan.status == sipMediaRefused && m.latched != sipMediaLatchPeer:
		mm.Status = sipMediaRefused
	case m.window() < rtp.MinQualityWindow:
		mm.Status = sipMediaTruncated
	default:
		mm.Status = sipMediaMeasured
	}
	return mm
}

// count feeds the worker's media counters, once finish has built the media
// block. The call counts only the attempt that decided the check, so a 503
// failover attempt is not a session of its own (runInviteCall).
func (m *sipMediaSession) count() {
	if m == nil || m.mm == nil {
		return
	}
	c, mm := m.counters, m.mm
	if c.session != nil {
		c.session(mm.Status)
	}
	if mm.Status == sipMediaRefused && c.refused != nil {
		c.refused(mm.Detail)
	}
	if c.rxDropped != nil {
		for _, d := range []struct {
			reason string
			n      int
		}{{"invalid", m.invalid}, {"foreign", m.foreign}, {"overflow", m.overflow}} {
			if d.n > 0 {
				c.rxDropped(d.reason, d.n)
			}
		}
	}
	if mm.TX != nil && !mm.TX.Reliable && c.unreliable != nil {
		c.unreliable()
	}
}

// inboundEvidence: audio reached the probe (far-end packets or echoes) in
// the window.
func (m *sipMediaSession) inboundEvidence() bool { return m.report.Accepted >= sipMediaEvidence }

// outboundEvidence: the probe's audio reached the far end, which echoed it.
func (m *sipMediaSession) outboundEvidence() bool {
	return m.report.RoundTrip != nil && m.report.RoundTrip.Matched >= sipMediaEvidence
}

func (m *sipMediaSession) writeErrors() int {
	n := 0
	for _, e := range m.writeErrs {
		n += e
	}
	return n
}

// writeErrorClass is the most frequent write error's class.
func (m *sipMediaSession) writeErrorClass() string {
	best := 0
	for class, n := range m.writeErrs {
		if n > m.writeErrs[best] {
			best = class
		}
	}
	return sipMediaWriteClassNames[best]
}

// afterByeEvidence: far-end audio kept coming well after our BYE left.
func (m *sipMediaSession) afterByeEvidence() bool {
	return m != nil && m.mm != nil && m.mm.RX != nil && m.mm.RX.AfterByePackets >= sipMediaAfterByeEvidence
}

// applyMediaRules turns the media block into the check's outcome, after
// applyThresholds. It runs only for a call that reached its hold and was not
// cut short by a worker shutdown, and never on an error verdict (which
// measured nothing). A success can become a failure (the far end's audio
// failed an assertion) or an error (the probe measured nothing about the
// target, such as a refused address); a failure stays one, with the media
// messages appended. With expect "none" media only ever adds warnings.
// Messages name reason codes, never addresses.
func (s *sipCall) applyMediaRules() {
	m := s.media
	if m == nil || m.mm == nil {
		return
	}
	mm := m.mm
	defer s.addMediaNotes()
	if !m.held || m.interrupt || !s.decided || s.result.Status == "error" {
		return
	}
	expect := m.cfg.Expect
	measureOnly := expect == models.SIPMediaExpectNone
	echo := expect == models.SIPMediaExpectEcho
	// assert fails the check on the far end's account; refuse makes it an
	// error on the probe's own; note only warns.
	note := func(msg string) { s.metrics.warn(msg) }
	assert := func(msg string) {
		if measureOnly {
			note(msg)
			return
		}
		s.metrics.AssertionsFailed = append(s.metrics.AssertionsFailed, msg)
		s.mediaVerdict("failure", msg)
	}
	refuse := func(msg string) { s.mediaVerdict("error", msg) }

	switch mm.Status {
	case sipMediaUnusable:
		switch mm.Detail {
		case sipMediaRecvOnly:
			assert("media: far end answered recvonly (it sends no audio)")
		case sipMediaSendOnly:
			assert("media: far end answered sendonly (it refuses our audio)")
		default:
			assert(fmt.Sprintf("media: far end answered without a usable audio stream (%s)", mm.Detail))
		}
		return
	}
	switch m.plan.direction {
	case "sendonly":
		note("media: far end answered sendonly; its audio measured, none sent")
	case "recvonly":
		note("media: far end answered recvonly (it sends no audio)")
	}

	if errs := m.writeErrors(); errs > 0 && errs*2 > m.attempts {
		msg := fmt.Sprintf("media: probe audio could not be sent (%s)", m.writeErrorClass())
		if echo {
			refuse(msg)
			return
		}
		note(msg)
	}

	refused := m.plan.status == sipMediaRefused
	switch {
	case refused && m.latched == sipMediaLatchPeer:
		note(fmt.Sprintf("media: far end offered an unusable media address (%s); latched to the signalling peer", m.plan.detail))
	case refused && !m.inboundEvidence():
		msg := fmt.Sprintf("media: far end's media address refused by worker policy (%s)", m.plan.detail)
		if measureOnly {
			note(msg)
		} else {
			refuse(msg + "; nothing measured")
		}
		return
	case refused:
		msg := fmt.Sprintf("media: probe audio not sent (%s)", m.plan.detail)
		if echo {
			refuse(msg)
			return
		}
		note(msg)
	}

	held := m.stopAt - m.startAt
	window := m.window()
	if window < sipMediaMinWindow {
		note(fmt.Sprintf("media: call ended after %s, too short to measure", sipFormatSeconds(held)))
		return
	}

	asymmetric := m.report.Accepted == 0 && m.foreign > 0
	const asymmetricMsg = "media: audio arrived only from an address the far end's SDP did not name (asymmetric media); not measured"
	switch {
	case echo && !m.outboundEvidence():
		if asymmetric {
			assert(asymmetricMsg)
			return
		}
		msg := fmt.Sprintf("media: no echo of the probe's audio in %s", sipFormatSeconds(window))
		if in := m.report.Inbound; in != nil {
			msg += fmt.Sprintf("; the far end sent %d packets of its own audio (transcoding, or not an echo line?)", in.Expected-in.Lost)
		}
		assert(msg)
		return
	case !echo && !m.inboundEvidence():
		if asymmetric {
			assert(asymmetricMsg)
			return
		}
		if measureOnly {
			note("media: no audio received (one-way audio, or the far end does not latch to the probe's NAT)")
			break
		}
		msg := fmt.Sprintf("media: no audio received in %s", sipFormatSeconds(window))
		switch {
		case m.sdpAddressSet:
			msg += "; check that sdp_address reaches this worker"
		case m.privateOffer:
			msg += "; the probe offered a private address, set sdp_address if it is behind NAT"
		}
		assert(msg)
		return
	}

	if window < rtp.MinQualityWindow {
		note(fmt.Sprintf("media: only %s measured; quality thresholds not applied", sipFormatSeconds(window)))
		return
	}
	if ok, why := m.reliable(); !ok {
		note(fmt.Sprintf("media: worker too busy to time media (%s); quality not judged", why))
		return
	}
	if !measureOnly {
		s.applyMediaThresholds()
	}
}

// mediaVerdict applies a media failure or error: a success takes its
// status, a failure keeps its own; the message is appended either way.
func (s *sipCall) mediaVerdict(status, msg string) {
	if s.result.ErrorMessage != nil && *s.result.ErrorMessage != "" {
		msg = *s.result.ErrorMessage + "; " + msg
	}
	if s.result.Status == "success" {
		s.result.Status = status
	}
	s.result.ErrorMessage = &msg
}

// sipMediaBasis is one direction of evidence the thresholds judge.
type sipMediaBasis struct {
	name    string
	lossPct float64
	jitter  *float64
	quality *rtp.Quality
}

// applyMediaThresholds applies call.media's warn, max and min to every
// basis with a quality estimate: max and min fail the check, warn adds a
// warning. RTT is the echo's median round trip.
func (s *sipCall) applyMediaThresholds() {
	m, cfg := s.media, s.media.cfg
	var bases []sipMediaBasis
	if in := m.report.Inbound; in != nil && in.Quality != nil {
		bases = append(bases, sipMediaBasis{"inbound", in.EffectiveLossPct, in.JitterMs, in.Quality})
	}
	rt := m.report.RoundTrip
	if rt != nil && rt.Quality != nil {
		bases = append(bases, sipMediaBasis{"round trip", rt.EffectiveLossPct, rt.JitterMs, rt.Quality})
	}
	qualityThresholds := cfg.WarnLossPct != nil || cfg.MaxLossPct != nil || cfg.WarnJitterMs != nil ||
		cfg.MaxJitterMs != nil || cfg.WarnMOS != nil || cfg.MinMOS != nil
	if len(bases) == 0 && qualityThresholds {
		s.metrics.warn("media: too few packets to estimate quality; quality thresholds not applied")
	}
	fail := func(assertion string) {
		s.metrics.AssertionsFailed = append(s.metrics.AssertionsFailed, assertion)
		s.mediaVerdict("failure", assertion)
	}
	for _, b := range bases {
		switch v := b.lossPct; {
		case cfg.MaxLossPct != nil && v > *cfg.MaxLossPct:
			fail(fmt.Sprintf("loss: %.2f%% > %s%% (%s)", v, sipMediaNum(*cfg.MaxLossPct, 2), b.name))
		case cfg.WarnLossPct != nil && v > *cfg.WarnLossPct:
			s.metrics.warn(fmt.Sprintf("loss: %.2f%% > %s%% (warn, %s)", v, sipMediaNum(*cfg.WarnLossPct, 2), b.name))
		}
		if b.jitter != nil {
			switch v := *b.jitter; {
			case cfg.MaxJitterMs != nil && v > float64(*cfg.MaxJitterMs):
				fail(fmt.Sprintf("jitter: %sms > %dms (%s)", sipMediaNum(v, 1), *cfg.MaxJitterMs, b.name))
			case cfg.WarnJitterMs != nil && v > float64(*cfg.WarnJitterMs):
				s.metrics.warn(fmt.Sprintf("jitter: %sms > %dms (warn, %s)", sipMediaNum(v, 1), *cfg.WarnJitterMs, b.name))
			}
		}
		switch v := b.quality.MOS; {
		case cfg.MinMOS != nil && v < *cfg.MinMOS:
			fail(fmt.Sprintf("mos: %.2f < %.2f (%s)", v, *cfg.MinMOS, b.name))
		case cfg.WarnMOS != nil && v < *cfg.WarnMOS:
			s.metrics.warn(fmt.Sprintf("mos: %.2f < %.2f (warn, %s)", v, *cfg.WarnMOS, b.name))
		}
	}
	if cfg.WarnRTTMs == nil && cfg.MaxRTTMs == nil {
		return
	}
	if rt == nil || rt.RTTMs == nil || rt.Matched < sipMediaEvidence {
		s.metrics.warn("rtt: not measured (no echo)")
		return
	}
	switch v := rt.RTTMs.P50; {
	case cfg.MaxRTTMs != nil && v > float64(*cfg.MaxRTTMs):
		fail(fmt.Sprintf("rtt: %sms > %dms (echo)", sipMediaNum(v, 0), *cfg.MaxRTTMs))
	case cfg.WarnRTTMs != nil && v > float64(*cfg.WarnRTTMs):
		s.metrics.warn(fmt.Sprintf("rtt: %sms > %dms (warn, echo)", sipMediaNum(v, 0), *cfg.WarnRTTMs))
	}
}

// addMediaNotes adds the media facts worth a warning whatever the outcome,
// and folds post-BYE audio into an unconfirmed teardown's warning.
func (s *sipCall) addMediaNotes() {
	m := s.media
	if m.held && !m.interrupt && m.plan != nil && m.plan.status != sipMediaUnusable {
		if m.latched == sipMediaLatchPort {
			s.metrics.warn("media: far end sends from another port than its SDP names; latched to it")
		}
		if in := m.report.Inbound; in != nil {
			if in.SSRCChanges > 0 {
				s.metrics.warn(fmt.Sprintf("media: far end's stream changed SSRC %d times", in.SSRCChanges))
			}
			if in.StoppedEarly {
				s.metrics.warn(fmt.Sprintf("media: far end stopped sending audio %s before hang-up",
					sipFormatSeconds(time.Duration(in.StallMs)*time.Millisecond)))
			}
		}
		if m.overflow > 0 {
			s.metrics.warn("media: inbound flood truncated")
		}
		if m.foreign > 0 && m.report.Accepted > 0 {
			s.metrics.warn(fmt.Sprintf("media: %d packets from addresses the far end's SDP did not name were ignored", m.foreign))
		}
	}
	if s.cm.Teardown == "bye_unconfirmed" && m.afterByeEvidence() {
		s.metrics.replaceWarning(sipByeUnconfirmedWarning, "teardown: BYE unconfirmed; far end still sending audio after the BYE")
	}
}

// sipRound rounds v to places decimals; a non-finite v is 0, so it can
// never make the metrics envelope fail to marshal.
func sipRound(v float64, places int) float64 {
	if math.IsNaN(v) || math.IsInf(v, 0) {
		return 0
	}
	scale := math.Pow(10, float64(places))
	r := math.Round(v*scale) / scale
	if r == 0 {
		return 0 // never -0
	}
	return r
}

// sipMediaNum formats a measured or configured number with at most places
// decimals and no trailing zeros: 2, 2.5, 34.1.
func sipMediaNum(v float64, places int) string {
	return strconv.FormatFloat(sipRound(v, places), 'f', -1, 64)
}
