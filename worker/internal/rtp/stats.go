package rtp

import (
	"cmp"
	"fmt"
	"math"
	"slices"
	"time"
)

// The measurement's fixed timeline and gates. None of them is a knob: a
// figure is only comparable across calls if they never move.
const (
	// Warmup skips the first moments after the answer, when SBCs re-anchor
	// media and the far end's stream settles. Inbound statistics cover
	// [answer + Warmup, stop]; the echo expectation starts there too.
	Warmup = 500 * time.Millisecond
	// EchoTail: packets sent in the last EchoTail before the stop are not
	// expected back, since their echoes may still be in flight at hang-up.
	EchoTail = 300 * time.Millisecond
	// LateFill: an inbound packet arriving up to LateFill after the stop
	// still counts as received (it was on the wire before the hang-up).
	LateFill = 60 * time.Millisecond

	// A basis gets a quality estimate (and quality thresholds apply to it)
	// only over at least MinQualityWindow holding at least
	// MinQualityExpected expected packets: below that one packet is several
	// percent of loss.
	MinQualityWindow   = 2 * time.Second
	MinQualityExpected = 50

	// MaxStreams is how many far-end SSRCs are tracked; packets of any
	// further SSRC are counted, not analysed.
	MaxStreams = 4

	// AfterByeGrace: far-end audio arriving later than this after our BYE
	// left is evidence the call is still up (the far end never saw the BYE).
	AfterByeGrace = time.Second
	// StallWarn is the silence before the stop that reads as a far end
	// that stopped sending, on a stream that never suppresses silence.
	StallWarn = time.Second
	// ResyncStep: a step in a stream's transit time (arrival minus RTP
	// timestamp) of at least ResyncStep, or of more than the JitterBuffer on
	// a marker-bit packet, re-anchors the late-discard reference and the
	// jitter filter, as a receiver's jitter buffer resyncs (timingSegments).
	// An endpoint that splices streams under one SSRC with contiguous
	// sequence numbers (an announcement handing over to the bridge) jumps its
	// timestamps; read against one reference, every packet on one side of
	// the jump would be late.
	ResyncStep = time.Second

	// The A.8 jitter filter starts at 0 and needs about 16 packets to reach
	// its level, so the reported mean and max leave those estimates out.
	jitterSettle = 16
	// Echo RTT gives the delay for the E-model once at least this many
	// echoes came back; fewer is too noisy for a median.
	minEchoDelaySamples = 10
)

// Payload types the probe receives besides G.711, in its offer's numbering
// (RFC 3264 §6.1: each side receives in its own numbering).
const (
	PTComfortNoise   = 13  // RFC 3389
	PTTelephoneEvent = 101 // RFC 4733, as the offer maps it
)

// Record is one accepted inbound RTP packet, as the media session's reader
// keeps it: packets from a source the session does not analyse never
// become records. At is the arrival time as a monotonic offset from the
// session's base, as are every time in Config.
type Record struct {
	At      time.Duration
	SSRC    uint32
	TS      uint32
	Seq     uint16
	EchoSeq uint16 // the sequence number of ours this echoes, when Echo is set
	PT      uint8
	Marker  bool
	Echo    EchoMatch
}

// Sent is one RTP packet the probe sent: its sequence number and when the
// write actually happened, which the echo statistics use in place of the
// RTP timestamp so the probe's own scheduling lateness drops out.
type Sent struct {
	At  time.Duration
	Seq uint16
}

// Config is what Analyze needs to know about the call besides its records.
type Config struct {
	Answer time.Duration // media started (the call entered its hold)
	Stop   time.Duration // the sender stopped (teardown began)
	Bye    time.Duration // our BYE to the answered dialog left; zero: none was sent

	AudioPT uint8 // the negotiated G.711 payload type, in the offer's numbering (0 or 8)
	PtimeMs int   // the packetisation time the probe sent with; zero is 20

	Sent []Sent // the probe's packets in send order; only echo statistics use it

	// RTCPRoundTrip is a round trip measured from RTCP (LSR/DLSR), the
	// E-model's delay when too few echoes came back; zero: none.
	RTCPRoundTrip time.Duration
}

// Report is the outcome of Analyze. Inbound and RoundTrip marshal to the
// "inbound" and "round_trip" blocks of the call's media metrics; the
// counters are for the session to fold into its own.
type Report struct {
	Inbound   *InboundStats // nil: no far-end stream in the window
	RoundTrip *EchoStats    // nil: nothing of ours came back

	// Accepted counts the packets, far-end or echo, that arrived in the
	// inbound window: the evidence that audio reached the probe.
	Accepted int
	// FirstPacket is the first record's arrival after the answer; zero
	// when there were no records.
	FirstPacket time.Duration
	// UnknownPT counts packets of a payload type the offer did not name;
	// they are in no statistic.
	UnknownPT int
	// AfterBye counts packets of the primary far-end stream arriving more
	// than AfterByeGrace after the BYE left.
	AfterBye int
}

// SSRC marshals as eight hex digits, which JSON cannot misread as a number
// beyond its exact range.
type SSRC uint32

func (s SSRC) String() string { return fmt.Sprintf("%08x", uint32(s)) }

func (s SSRC) MarshalText() ([]byte, error) { return []byte(s.String()), nil }

// InboundStats is the probe's receiver report on the far end's stream.
// Loss, duplicates, reordering and late discards are summed over every
// tracked SSRC active in the window; jitter, gaps and the stall describe the
// primary one (the SSRC with the most packets in the window).
type InboundStats struct {
	SSRC         SSRC `json:"ssrc"`
	SSRCChanges  int  `json:"ssrc_changes"`            // tracked SSRCs after the first
	SSRCOverflow int  `json:"ssrc_overflow,omitempty"` // packets of SSRCs beyond MaxStreams

	// Expected spans the first to the highest sequence number that arrived
	// in the window; Lost is how many of them never arrived (an exact set,
	// so duplicates and reordering cannot hide loss). CumulativeLost is
	// RFC 3550's own figure, expected minus arrivals, comparable with what
	// an SBC's RTCP reports (and negative when duplicates outnumber loss).
	Expected       int     `json:"expected"`
	Lost           int     `json:"lost"`
	LossPct        float64 `json:"loss_pct"`
	CumulativeLost int     `json:"cumulative_lost"`
	Duplicates     int     `json:"duplicates"`
	Reordered      int     `json:"reordered"`
	// Late counts G.711 packets the JitterBuffer reference buffer would
	// have discarded, re-anchored where the timestamps jump
	// (timingSegments); EffectiveLossPct is network loss plus those, the
	// E-model's Ppl.
	Late             int     `json:"late"`
	EffectiveLossPct float64 `json:"effective_loss_pct"`
	BurstR           float64 `json:"burst_r"`

	// RFC 3550 A.8 interarrival jitter on the negotiated codec: the mean
	// and max of the estimate over the window once settled, and its last
	// value (what an RTCP receiver report would carry). Absent with too
	// few packets.
	JitterMs     *float64 `json:"jitter_ms,omitempty"`
	JitterMaxMs  *float64 `json:"jitter_max_ms,omitempty"`
	JitterLastMs *float64 `json:"jitter_last_ms,omitempty"`

	// Sequence loss cannot see a far end that stops sending: MaxGapMs is
	// the longest silence in the window, its edges included, and StallMs
	// the silence before the stop.
	MaxGapMs int `json:"max_gap_ms"`
	StallMs  int `json:"stall_ms"`

	ComfortNoise    int `json:"comfort_noise"`         // RFC 3389 packets in the window
	TelephoneEvents int `json:"telephone_events"`      // RFC 4733 packets in the window
	PTMismatch      int `json:"pt_mismatch,omitempty"` // the other G.711 law, outside jitter

	// Quality is set only when the window meets MinQualityWindow and
	// MinQualityExpected; without it no quality threshold applies.
	Quality *Quality `json:"quality,omitempty"`

	// StoppedEarly: a stream that never suppressed silence (no comfort
	// noise, no new talkspurt) went quiet for more than StallWarn before
	// the stop.
	StoppedEarly bool `json:"-"`
}

// EchoStats is the round trip of the probe's own packets on an echo line.
type EchoStats struct {
	MatchedBy string `json:"matched_by"` // "ssrc", or "marker" for a re-originated echo

	// Expected counts the packets sent in [answer + Warmup, stop − EchoTail];
	// Matched how many of them came back, whenever they arrived.
	Expected         int     `json:"expected"`
	Matched          int     `json:"matched"`
	LossPct          float64 `json:"loss_pct"`
	Duplicates       int     `json:"duplicates"`
	Late             int     `json:"late"`
	EffectiveLossPct float64 `json:"effective_loss_pct"`
	BurstR           float64 `json:"burst_r"`
	// A.8 jitter with the real send times in place of RTP timestamps: the
	// round trip's delay variation, without the probe's own pacing.
	JitterMs *float64  `json:"jitter_ms,omitempty"`
	RTTMs    *RTTStats `json:"rtt_ms,omitempty"`
	Quality  *Quality  `json:"quality,omitempty"`
}

// RTTStats summarises the round trips of the matched echoes, in ms.
type RTTStats struct {
	P50 float64 `json:"p50"`
	P95 float64 `json:"p95"`
	Max float64 `json:"max"`
}

// Analyze computes a call's media statistics from its records, which are in
// arrival order (as one reader goroutine appends them; they are sorted by
// arrival here otherwise). It is pure: the same records and config give the
// same report.
func Analyze(records []Record, cfg Config) Report {
	if !slices.IsSortedFunc(records, byArrival) {
		records = slices.Clone(records)
		slices.SortStableFunc(records, byArrival)
	}
	ws, we := cfg.Answer+Warmup, cfg.Stop
	inWindow := func(at time.Duration) bool { return at >= ws && at <= we }

	var rep Report
	if len(records) > 0 {
		rep.FirstPacket = max(records[0].At-cfg.Answer, 0)
	}

	var (
		streams  []*farStream
		echoes   []Record
		farTimes []time.Duration // arrivals in the window, any far SSRC
		in       InboundStats
	)
	for _, r := range records {
		if r.Echo != NotEcho {
			echoes = append(echoes, r)
			if inWindow(r.At) {
				rep.Accepted++
			}
			continue
		}
		if !receivedPT(r.PT) {
			rep.UnknownPT++
			continue
		}
		win := inWindow(r.At)
		if win {
			rep.Accepted++
			farTimes = append(farTimes, r.At)
			switch {
			case r.PT == PTComfortNoise:
				in.ComfortNoise++
			case r.PT == PTTelephoneEvent:
				in.TelephoneEvents++
			case r.PT != cfg.AudioPT:
				in.PTMismatch++
			}
		}
		st := findStream(streams, r.SSRC)
		if st == nil {
			if len(streams) == MaxStreams {
				in.SSRCOverflow++
				continue
			}
			st = &farStream{ssrc: r.SSRC}
			streams = append(streams, st)
		}
		if win {
			// A marker bit after a stream's first packet in the window
			// starts a new talkspurt: the far end suppresses silence.
			if st.inWindow > 0 && r.Marker {
				st.talkspurts++
			}
			st.inWindow++
		}
		ext, epoch, ok := st.seq.Update(r.Seq)
		if !ok {
			continue
		}
		for len(st.runs) <= epoch {
			st.runs = append(st.runs, nil)
		}
		st.runs[epoch] = append(st.runs[epoch], arrival{at: r.At, ext: ext, ts: r.TS, pt: r.PT, marker: r.Marker})
	}

	if primary := primaryStream(streams); primary != nil {
		in.SSRC = SSRC(primary.ssrc)
		in.SSRCChanges = len(streams) - 1
		var tr transitions
		for _, st := range streams {
			for _, run := range st.runs {
				in.add(run.loss(ws, we, &tr))
			}
		}
		if in.Expected > 0 {
			in.LossPct = round(pct(in.Lost, in.Expected), 2)
			in.EffectiveLossPct = round(pct(in.Lost+in.Late, in.Expected), 2)
			in.BurstR = round(tr.burstRatio(), 2)

			var jit jitter
			for _, run := range primary.runs {
				var (
					txs, transits []time.Duration
					ats           []time.Duration
					markers       []bool
				)
				for _, a := range run.firsts(ws, we) {
					if a.pt == cfg.AudioPT {
						tx := tsDuration(a.ts - run[0].ts)
						ats, txs = append(ats, a.at), append(txs, tx)
						transits, markers = append(transits, a.at-tx), append(markers, a.marker)
					}
				}
				segment := timingSegments(transits, markers)
				for k := range ats {
					if k == 0 || segment[k] != segment[k-1] {
						jit.restart()
					}
					jit.add(ats[k], txs[k])
				}
			}
			in.JitterMs, in.JitterMaxMs, in.JitterLastMs = jit.report()

			in.MaxGapMs, in.StallMs = gaps(farTimes, ws, we)
			in.StoppedEarly = in.ComfortNoise == 0 && primary.talkspurts == 0 &&
				time.Duration(in.StallMs)*time.Millisecond > StallWarn
			if cfg.Bye > 0 {
				for _, r := range records {
					if r.Echo == NotEcho && r.SSRC == primary.ssrc && receivedPT(r.PT) && r.At > cfg.Bye+AfterByeGrace {
						rep.AfterBye++
					}
				}
			}
			rep.Inbound = &in
		}
	}

	rep.RoundTrip = analyzeEcho(echoes, cfg, ws)

	// One delay for every basis: the echo's measured round trip if there
	// is enough of it, else RTCP's, else none (the MOS is then an upper
	// bound, and says so). The estimates take the loss figures as
	// reported, so a reader can reproduce them from the metrics.
	owd, source := 0.0, DelayUnmeasured
	if rt := rep.RoundTrip; rt != nil && rt.RTTMs != nil && rt.Matched >= minEchoDelaySamples {
		owd, source = rt.RTTMs.P50/2, DelayEcho
	} else if cfg.RTCPRoundTrip > 0 {
		owd, source = ms(cfg.RTCPRoundTrip)/2, DelayRTCP
	}
	ptime := float64(cfg.PtimeMs)
	if ptime <= 0 {
		ptime = 20
	}
	delay := MouthToEar(owd, ptime)
	if ib := rep.Inbound; ib != nil && we-ws >= MinQualityWindow && ib.Expected >= MinQualityExpected {
		q := Estimate(ib.EffectiveLossPct, ib.BurstR, delay, source)
		ib.Quality = &q
	}
	if rt := rep.RoundTrip; rt != nil && cfg.Stop-EchoTail-ws >= MinQualityWindow && rt.Expected >= MinQualityExpected {
		q := Estimate(rt.EffectiveLossPct, rt.BurstR, delay, source)
		rt.Quality = &q
	}
	return rep
}

// analyzeEcho matches the echo records to the packets the probe sent.
func analyzeEcho(echoes []Record, cfg Config, ws time.Duration) *EchoStats {
	if len(echoes) == 0 || len(cfg.Sent) == 0 {
		return nil
	}
	sentAt := make(map[uint16]int, len(cfg.Sent))
	for i, s := range cfg.Sent {
		sentAt[s.Seq] = i
	}
	type hit struct {
		at, rtt time.Duration
		sent    int
	}
	var (
		hits    []hit                         // first arrival of each echoed packet, in arrival order
		hitSent = make(map[int]time.Duration) // sent index → its round trip
		bySSRC  bool
		stats   EchoStats
	)
	for _, r := range echoes {
		i, ok := sentAt[r.EchoSeq]
		if !ok || r.At < cfg.Sent[i].At {
			continue // not something we sent, or "back" before it left
		}
		if _, dup := hitSent[i]; dup {
			stats.Duplicates++
			continue
		}
		bySSRC = bySSRC || r.Echo == EchoBySSRC
		h := hit{at: r.At, rtt: r.At - cfg.Sent[i].At, sent: i}
		hits = append(hits, h)
		hitSent[i] = h.rtt
	}
	if len(hits) == 0 {
		return nil
	}
	stats.BurstR = 1
	stats.MatchedBy = EchoByMarker.String()
	if bySSRC {
		stats.MatchedBy = EchoBySSRC.String()
	}

	expectedUntil := cfg.Stop - EchoTail
	expected := func(i int) bool { at := cfg.Sent[i].At; return at >= ws && at <= expectedUntil }
	var rtts []time.Duration
	minRTT := time.Duration(math.MaxInt64)
	for i := range cfg.Sent {
		if !expected(i) {
			continue
		}
		stats.Expected++
		if rtt, ok := hitSent[i]; ok {
			stats.Matched++
			rtts = append(rtts, rtt)
			minRTT = min(minRTT, rtt)
		}
	}
	if stats.Expected == 0 {
		return &stats
	}

	// Late discards against the earliest round trip, and the loss
	// indicator in send order for the burst ratio.
	var tr transitions
	first := true
	for i := range cfg.Sent {
		if !expected(i) {
			continue
		}
		rtt, ok := hitSent[i]
		late := ok && rtt-minRTT > JitterBuffer*time.Millisecond
		if late {
			stats.Late++
		}
		tr.step(ok && !late, first)
		first = false
	}
	tr.close()
	stats.LossPct = round(pct(stats.Expected-stats.Matched, stats.Expected), 2)
	stats.EffectiveLossPct = round(pct(stats.Expected-stats.Matched+stats.Late, stats.Expected), 2)
	stats.BurstR = round(tr.burstRatio(), 2)

	var jit jitter
	for _, h := range hits {
		if expected(h.sent) {
			jit.add(h.at, cfg.Sent[h.sent].At)
		}
	}
	stats.JitterMs, _, _ = jit.report()

	if len(rtts) > 0 {
		slices.Sort(rtts)
		stats.RTTMs = &RTTStats{
			P50: round(ms(percentile(rtts, 50)), 2),
			P95: round(ms(percentile(rtts, 95)), 2),
			Max: round(ms(rtts[len(rtts)-1]), 2),
		}
	}
	return &stats
}

// farStream is one far-end SSRC: its sequence machine and its counted
// packets, one run per sequence epoch (a restarted sender starts a new run,
// whose extended numbers do not continue the previous one's).
type farStream struct {
	ssrc       uint32
	seq        Sequence
	runs       []run
	inWindow   int // packets in the window, counted or not
	talkspurts int // marker bits after the first packet in the window
}

type arrival struct {
	at     time.Duration
	ext    int64
	ts     uint32
	pt     uint8
	marker bool
}

// run is one epoch's counted packets in arrival order.
type run []arrival

// runLoss is what one run contributes to the inbound loss figures.
type runLoss struct {
	expected, received, arrivals, reordered, late int
}

// loss counts one run over the window (RFC 3550 §6.4.1 and A.3, exact):
// the expected range runs from the extended number of the first packet
// arriving in the window to the highest arriving in it, and a number counts
// as received if it arrived by the stop plus LateFill. It feeds the run's
// effective-loss indicator to tr.
func (r run) loss(ws, we time.Duration, tr *transitions) runLoss {
	var (
		out        runLoss
		base, high int64
		found      bool
	)
	for _, a := range r {
		if a.at < ws || a.at > we {
			continue
		}
		if !found {
			base, high, found = a.ext, a.ext, true
		}
		high = max(high, a.ext)
	}
	if !found {
		return out
	}
	out.expected = int(high - base + 1)

	// First arrivals of each number in range, in arrival order.
	seen := make(map[int64]bool)
	var firsts []arrival
	runMax := int64(math.MinInt64)
	for _, a := range r {
		if a.at > we+LateFill || a.ext < base || a.ext > high {
			continue
		}
		out.arrivals++
		if seen[a.ext] {
			continue // a duplicate: neither received again nor reordered
		}
		seen[a.ext] = true
		if a.ext < runMax {
			out.reordered++
		}
		runMax = max(runMax, a.ext)
		firsts = append(firsts, a)
	}
	out.received = len(firsts)

	// Late discards under the reference jitter buffer, by transit time
	// against the earliest of its timing segment (timingSegments), for G.711
	// packets only: telephone events repeat their timestamp and would read
	// as late.
	late := make(map[int64]bool)
	var (
		g711     []int // indices into firsts
		transits []time.Duration
		markers  []bool
	)
	for i, a := range firsts {
		if isG711(a.pt) {
			g711 = append(g711, i)
			transits = append(transits, a.at-tsDuration(a.ts-r[0].ts))
			markers = append(markers, a.marker)
		}
	}
	segment := timingSegments(transits, markers)
	minTransit := make(map[int]time.Duration)
	for k, t := range transits {
		if m, ok := minTransit[segment[k]]; !ok || t < m {
			minTransit[segment[k]] = t
		}
	}
	for k, i := range g711 {
		if transits[k]-minTransit[segment[k]] > JitterBuffer*time.Millisecond {
			late[firsts[i].ext] = true
		}
	}
	out.late = len(late)

	// The effective-loss indicator over [base, high], walked through the
	// received numbers in order: the gaps between them are lost runs.
	exts := make([]int64, 0, len(firsts))
	for _, a := range firsts {
		exts = append(exts, a.ext)
	}
	slices.Sort(exts)
	for i, e := range exts {
		if i > 0 {
			if gap := e - exts[i-1] - 1; gap > 0 {
				tr.step(false, false)
				tr.lostToLost += int(gap - 1)
			}
		}
		tr.step(!late[e], i == 0)
	}
	tr.close()
	return out
}

// firsts is the run's first arrival of each number in the window, in
// arrival order.
func (r run) firsts(ws, we time.Duration) []arrival {
	seen := make(map[int64]bool)
	var out []arrival
	for _, a := range r {
		if a.at < ws || a.at > we || seen[a.ext] {
			continue
		}
		seen[a.ext] = true
		out = append(out, a)
	}
	return out
}

func (s *InboundStats) add(l runLoss) {
	s.Expected += l.expected
	s.Lost += l.expected - l.received
	s.CumulativeLost += l.expected - l.arrivals
	s.Duplicates += l.arrivals - l.received
	s.Reordered += l.reordered
	s.Late += l.late
}

// transitions counts a loss indicator's state changes for BurstRatio.
type transitions struct {
	recvToRecv, recvToLost, lostToRecv, lostToLost int

	prevLost bool
}

// step adds one position of the indicator; first starts a new sequence.
func (t *transitions) step(received, first bool) {
	if !first {
		switch {
		case !t.prevLost && received:
			t.recvToRecv++
		case !t.prevLost:
			t.recvToLost++
		case received:
			t.lostToRecv++
		default:
			t.lostToLost++
		}
	}
	t.prevLost = !received
}

// close ends a sequence. A loss run still open at the end is taken to end
// there, so a run cut off by the window is not read as one that never ends.
func (t *transitions) close() {
	if t.prevLost {
		t.lostToRecv++
		t.prevLost = false
	}
}

func (t *transitions) burstRatio() float64 {
	return BurstRatio(t.recvToRecv, t.recvToLost, t.lostToRecv, t.lostToLost)
}

// timingSegments splits a stream's packets, given their transit times in
// arrival order (and marker bits), into timing segments, each read against
// its own reference: a candidate is a step from the previous packet of at
// least ResyncStep, or of more than the JitterBuffer on a marker-bit packet.
// A step down always re-anchors: network delay cannot make a packet early,
// so the timestamps moved. A step up re-anchors only when it holds: the next
// packet stays within the JitterBuffer of the new level, and none after it
// comes back within the JitterBuffer of the old level before the next
// ResyncStep. That keeps network delay as lateness: a lone delayed packet,
// last in the window or not, is followed by none at its level, and after a
// stall the queued packets drain back to the old transit. A stall still
// draining when the window ends does read as a resync.
func timingSegments(transits []time.Duration, markers []bool) []int {
	seg := make([]int, len(transits))
	jb := JitterBuffer * time.Millisecond
	near := func(a, b time.Duration) bool { return max(a-b, b-a) <= jb }
	n := 0
	for i := 1; i < len(transits); i++ {
		d := transits[i] - transits[i-1]
		step := max(d, -d)
		if step >= ResyncStep || (markers[i] && step > jb) {
			resync := d < 0 || (i+1 < len(transits) && near(transits[i+1], transits[i]))
			if d > 0 && resync {
				for j := i + 1; j < len(transits); j++ {
					if max(transits[j]-transits[j-1], transits[j-1]-transits[j]) >= ResyncStep {
						break
					}
					if near(transits[j], transits[i-1]) {
						resync = false
						break
					}
				}
			}
			if resync {
				n++
			}
		}
		seg[i] = n
	}
	return seg
}

// jitter is RFC 3550 A.8's interarrival jitter estimator, in ms:
// J += (|D| − J)/16, where D is the change in transit time between
// consecutive packets (arrival spacing minus send spacing).
type jitter struct {
	j, sum, peak    float64
	settled, update int // estimates past jitterSettle; updates since restart
	prevAt, prevTx  time.Duration
	started, any    bool
}

// restart begins a new sequence (a new epoch), whose first packet has no
// predecessor to compare with.
func (j *jitter) restart() { j.started, j.update = false, 0 }

// add feeds one packet: its arrival and its send time (the RTP timestamp
// as a duration, or the real send time for an echo).
func (j *jitter) add(at, tx time.Duration) {
	if j.started {
		d := ms((at - j.prevAt) - (tx - j.prevTx))
		j.j += (math.Abs(d) - j.j) / 16
		j.update++
		j.any = true
		if j.update >= jitterSettle {
			j.sum += j.j
			j.peak = max(j.peak, j.j)
			j.settled++
		}
	}
	j.prevAt, j.prevTx, j.started = at, tx, true
}

// report gives the mean and max of the settled estimates and the last one;
// nil where there is none.
func (j *jitter) report() (mean, peak, last *float64) {
	if j.settled > 0 {
		mean, peak = ptr(round(j.sum/float64(j.settled), 2)), ptr(round(j.peak, 2))
	}
	if j.any {
		last = ptr(round(j.j, 2))
	}
	return mean, peak, last
}

// gaps gives the longest silence in [ws, we] between the arrivals (sorted)
// and the window's edges, and the silence before we, in whole ms.
func gaps(times []time.Duration, ws, we time.Duration) (maxGap, stall int) {
	if len(times) == 0 {
		w := int(math.Round(ms(we - ws)))
		return w, w
	}
	longest := times[0] - ws
	for i := 1; i < len(times); i++ {
		longest = max(longest, times[i]-times[i-1])
	}
	tail := we - times[len(times)-1]
	longest = max(longest, tail)
	return int(math.Round(ms(longest))), int(math.Round(ms(tail)))
}

func findStream(streams []*farStream, ssrc uint32) *farStream {
	for _, st := range streams {
		if st.ssrc == ssrc {
			return st
		}
	}
	return nil
}

// primaryStream is the stream with the most packets in the window, the
// earliest seen on a tie; nil when none had any.
func primaryStream(streams []*farStream) *farStream {
	var best *farStream
	for _, st := range streams {
		if st.inWindow > 0 && (best == nil || st.inWindow > best.inWindow) {
			best = st
		}
	}
	return best
}

// receivedPT reports whether pt is one the offer named (0, 8, 101) or
// comfort noise, which arrives unoffered often enough to be expected.
func receivedPT(pt uint8) bool {
	return isG711(pt) || pt == PTComfortNoise || pt == PTTelephoneEvent
}

func isG711(pt uint8) bool { return pt == uint8(PCMU) || pt == uint8(PCMA) }

// tsDuration converts an RTP timestamp difference, read as signed so a
// wrap or a slightly older timestamp stays small, to a duration.
func tsDuration(d uint32) time.Duration {
	return time.Duration(int64(int32(d)) * int64(time.Second) / ClockRate)
}

// percentile is the nearest-rank percentile of sorted, which is non-empty.
func percentile(sorted []time.Duration, p int) time.Duration {
	i := (p*len(sorted)+99)/100 - 1
	return sorted[min(max(i, 0), len(sorted)-1)]
}

func pct(part, whole int) float64 {
	if whole <= 0 {
		return 0
	}
	return 100 * float64(part) / float64(whole)
}

func ms(d time.Duration) float64 { return float64(d) / float64(time.Millisecond) }

func ptr(v float64) *float64 { return &v }

func byArrival(a, b Record) int { return cmp.Compare(a.At, b.At) }
