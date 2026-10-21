package rtp

import (
	"encoding/binary"
	"encoding/json"
	"math"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"
)

const ourSSRC = 0x9f00aa01

// stream is a far-end G.711 stream: packet k has sequence number seq0 + k,
// timestamp ts0 + 160k and arrives at 10 ms + 20k ms, a constant transit.
// With testConfig's window [500 ms, 5 s], packets 25..249 (arriving 510 to
// 4990 ms) are the 225 in the window.
func stream(ssrc uint32, seq0 uint16, ts0 uint32, from, to int) []Record {
	var out []Record
	for k := from; k <= to; k++ {
		out = append(out, Record{
			At:   10*time.Millisecond + time.Duration(k)*20*time.Millisecond,
			SSRC: ssrc,
			Seq:  seq0 + uint16(k),
			TS:   ts0 + 160*uint32(k),
			PT:   uint8(PCMU),
		})
	}
	return out
}

func farStreamRecords() []Record { return stream(0x1234, 100, 5000, 0, 260) }

func testConfig() Config {
	return Config{Answer: 0, Stop: 5 * time.Second, AudioPT: uint8(PCMU), PtimeMs: 20}
}

// without drops the packets whose sequence numbers are listed.
func without(recs []Record, seqs ...uint16) []Record {
	return slices.DeleteFunc(slices.Clone(recs), func(r Record) bool { return slices.Contains(seqs, r.Seq) })
}

func inbound(t *testing.T, recs []Record, cfg Config) *InboundStats {
	t.Helper()
	rep := Analyze(recs, cfg)
	if rep.Inbound == nil {
		t.Fatal("no inbound statistics")
	}
	return rep.Inbound
}

func fp(v float64) *float64 { return &v }

func deref(p *float64) any {
	if p == nil {
		return nil
	}
	return *p
}

func floatIs(p *float64, want float64) bool { return p != nil && math.Abs(*p-want) < 0.005 }

func TestAnalyzeCleanStream(t *testing.T) {
	rep := Analyze(farStreamRecords(), testConfig())
	in := rep.Inbound
	if in == nil {
		t.Fatal("no inbound statistics")
	}
	want := InboundStats{
		SSRC: 0x1234, Expected: 225, BurstR: 1,
		JitterMs: fp(0), JitterMaxMs: fp(0), JitterLastMs: fp(0),
		MaxGapMs: 20, StallMs: 10,
		// d = 0 + 60 + 20 = 80 ms: R 91.28, MOS 4.37, the model's ceiling.
		Quality: &Quality{RFactor: 91.3, MOS: 4.37, DelayMs: 80, DelaySource: DelayUnmeasured},
	}
	if !reflect.DeepEqual(*in, want) {
		got, _ := json.Marshal(in)
		w, _ := json.Marshal(want)
		t.Fatalf("inbound\n got %s\nwant %s", got, w)
	}
	if rep.Accepted != 225 || rep.FirstPacket != 10*time.Millisecond || rep.RoundTrip != nil || rep.UnknownPT != 0 || rep.AfterBye != 0 {
		t.Fatalf("report %+v", rep)
	}
}

func TestAnalyzeLossDuplicatesReordering(t *testing.T) {
	base := farStreamRecords()
	k := func(i int) uint16 { return 100 + uint16(i) } // sequence number of packet i

	t.Run("one lost in 225", func(t *testing.T) {
		in := inbound(t, without(base, k(100)), testConfig())
		if in.Expected != 225 || in.Lost != 1 || in.LossPct != 0.44 || in.CumulativeLost != 1 ||
			in.EffectiveLossPct != 0.44 || in.BurstR != 1 || in.MaxGapMs != 40 {
			t.Fatalf("%+v", in)
		}
		// Ie,eff = 95·0.44/25.54 = 1.6366, R = 91.28 − 1.6366 = 89.6434,
		// MOS 4.3302.
		if q := in.Quality; q == nil || q.RFactor != 89.6 || q.MOS != 4.33 {
			t.Fatalf("quality %+v", in.Quality)
		}
	})
	t.Run("a reordered pair is no loss", func(t *testing.T) {
		recs := slices.Clone(base)
		recs[100].Seq, recs[101].Seq = recs[101].Seq, recs[100].Seq
		recs[100].TS, recs[101].TS = recs[101].TS, recs[100].TS
		in := inbound(t, recs, testConfig())
		if in.Lost != 0 || in.Reordered != 1 || in.Duplicates != 0 || in.Late != 0 || in.CumulativeLost != 0 {
			t.Fatalf("%+v", in)
		}
	})
	t.Run("a duplicate leaves loss unchanged", func(t *testing.T) {
		dup := base[100]
		dup.At += time.Millisecond
		recs := slices.Insert(slices.Clone(base), 101, dup)
		in := inbound(t, recs, testConfig())
		if in.Lost != 0 || in.LossPct != 0 || in.Duplicates != 1 || in.Reordered != 0 || in.CumulativeLost != -1 {
			t.Fatalf("%+v", in)
		}
	})
	t.Run("a packet later than the reference buffer is a late discard", func(t *testing.T) {
		recs := slices.Clone(base)
		recs[100].At += 75 * time.Millisecond // behind 101..103: reordered too
		in := inbound(t, recs, testConfig())
		if in.Lost != 0 || in.LossPct != 0 || in.Late != 1 || in.Reordered != 1 || in.EffectiveLossPct != 0.44 {
			t.Fatalf("%+v", in)
		}
	})
	t.Run("late by less than the buffer is not", func(t *testing.T) {
		recs := slices.Clone(base)
		recs[100].At += 55 * time.Millisecond
		if in := inbound(t, recs, testConfig()); in.Late != 0 || in.EffectiveLossPct != 0 {
			t.Fatalf("%+v", in)
		}
	})
	t.Run("a packet arriving within LateFill of the stop counts", func(t *testing.T) {
		recs := slices.Clone(base)
		recs[200].At = 5*time.Second + 50*time.Millisecond
		in := inbound(t, recs, testConfig())
		if in.Lost != 0 || in.Late != 1 {
			t.Fatalf("%+v", in)
		}
		recs[200].At = 5*time.Second + 70*time.Millisecond
		if in := inbound(t, recs, testConfig()); in.Lost != 1 {
			t.Fatalf("after LateFill: %+v", in)
		}
	})
}

func TestAnalyzeDTXCNAndTelephoneEvents(t *testing.T) {
	t.Run("a DTX gap is not loss", func(t *testing.T) {
		// Packets 100..149 are never sent: the sequence stays contiguous,
		// the timestamp and arrival jump 50 frames, a marker starts the
		// next talkspurt.
		var recs []Record
		for _, r := range farStreamRecords() {
			k := int(r.Seq - 100)
			if k >= 100 && k < 150 {
				continue
			}
			if k >= 150 {
				r.Seq -= 50
				r.Marker = k == 150
			}
			recs = append(recs, r)
		}
		in := inbound(t, recs, testConfig())
		if in.Expected != 175 || in.Lost != 0 || in.MaxGapMs != 1020 || !floatIs(in.JitterMs, 0) || in.StoppedEarly {
			t.Fatalf("%+v", in)
		}
	})
	t.Run("comfort noise and telephone events count in sequence, not in jitter", func(t *testing.T) {
		recs := farStreamRecords()
		for i := 100; i < 110; i++ {
			recs[i].PT = PTComfortNoise
		}
		for i := 150; i < 160; i++ {
			recs[i].PT = PTTelephoneEvent
			recs[i].TS = recs[150].TS // an RFC 4733 event repeats its timestamp
		}
		recs[60].PT = uint8(PCMA)
		in := inbound(t, recs, testConfig())
		if in.Lost != 0 || in.Late != 0 || in.ComfortNoise != 10 || in.TelephoneEvents != 10 || in.PTMismatch != 1 ||
			!floatIs(in.JitterMs, 0) || !floatIs(in.JitterMaxMs, 0) {
			t.Fatalf("%+v", in)
		}
	})
	t.Run("an unoffered payload type is in no statistic", func(t *testing.T) {
		recs := farStreamRecords()
		recs[70].PT = 18
		rep := Analyze(recs, testConfig())
		if rep.UnknownPT != 1 || rep.Accepted != 224 || rep.Inbound.Lost != 1 {
			t.Fatalf("unknown %d accepted %d inbound %+v", rep.UnknownPT, rep.Accepted, rep.Inbound)
		}
	})
}

// A timestamp jump under one SSRC with contiguous sequence numbers (an
// endpoint splicing an announcement into the bridge) re-anchors the
// late-discard reference and the jitter filter, as a jitter buffer resyncs;
// network delay still reads as lateness.
func TestAnalyzeTimestampResync(t *testing.T) {
	shiftTS := func(from int, d int64, marker bool) []Record {
		recs := farStreamRecords()
		for i := range recs {
			if k := int(recs[i].Seq - 100); k >= from {
				recs[i].TS = uint32(int64(recs[i].TS) + d)
				recs[i].Marker = marker && k == from
			}
		}
		return recs
	}
	clean := func(t *testing.T, in *InboundStats) {
		t.Helper()
		if in.Lost != 0 || in.Late != 0 || in.EffectiveLossPct != 0 || in.BurstR != 1 || !floatIs(in.JitterMs, 0) ||
			in.Quality == nil || in.Quality.MOS != 4.37 {
			t.Fatalf("%+v quality %+v", in, in.Quality)
		}
	}
	t.Run("a forward jump of 1 s", func(t *testing.T) { clean(t, inbound(t, shiftTS(150, 8000, false), testConfig())) })
	t.Run("a backward jump of 1 s", func(t *testing.T) { clean(t, inbound(t, shiftTS(150, -8000, false), testConfig())) })
	t.Run("a 300 ms jump on a marker", func(t *testing.T) { clean(t, inbound(t, shiftTS(150, 2400, true), testConfig())) })
	t.Run("a 300 ms jump without a marker is lateness", func(t *testing.T) {
		// Below ResyncStep and on no talkspurt start: read against the
		// later, lower transit, the 125 packets before it are late.
		if in := inbound(t, shiftTS(150, 2400, false), testConfig()); in.Late != 125 {
			t.Fatalf("%+v", in)
		}
	})
	t.Run("a stall that drains is lateness", func(t *testing.T) {
		// Packets 125..199 are held for up to 1.4 s (a step past
		// ResyncStep) and arrive in a burst; the stream drains back to its
		// transit by 200.
		recs := farStreamRecords()
		for i := range recs {
			if k := int(recs[i].Seq - 100); k >= 125 && k < 200 {
				recs[i].At = 10*time.Millisecond + 200*20*time.Millisecond - time.Duration(200-k)*time.Millisecond
			}
		}
		in := inbound(t, recs, testConfig())
		// Packet k is (200-k)·19 ms late; beyond 60 ms for k ≤ 196.
		if in.Lost != 0 || in.Late != 72 {
			t.Fatalf("%+v", in)
		}
	})
	t.Run("a stall at the end is lateness", func(t *testing.T) {
		recs := farStreamRecords()
		recs[200].At = 5*time.Second + 50*time.Millisecond // 1 s late, last to arrive
		if in := inbound(t, recs, testConfig()); in.Late != 1 {
			t.Fatalf("%+v", in)
		}
	})
}

func TestAnalyzeSSRCs(t *testing.T) {
	t.Run("an SSRC change gives no spurious loss", func(t *testing.T) {
		recs := append(stream(0xa, 100, 0, 0, 120), stream(0xb, 40000, 123456, 121, 260)...)
		in := inbound(t, recs, testConfig())
		// B's first packet serves its probation, so 96 + 128 are expected.
		if in.SSRC != 0xb || in.SSRCChanges != 1 || in.Expected != 224 || in.Lost != 0 || in.Late != 0 {
			t.Fatalf("%+v", in)
		}
	})
	t.Run("SSRCs beyond MaxStreams are counted, not analysed", func(t *testing.T) {
		recs := farStreamRecords()
		for s := uint32(1); s <= 4; s++ {
			for i := range 3 {
				recs = append(recs, Record{At: time.Second + time.Duration(s*10+uint32(i))*time.Microsecond, SSRC: s, Seq: uint16(i), PT: 0})
			}
		}
		in := inbound(t, recs, testConfig())
		if in.SSRC != 0x1234 || in.SSRCChanges != 3 || in.SSRCOverflow != 3 {
			t.Fatalf("%+v", in)
		}
	})
	t.Run("a restarted sender starts a new run", func(t *testing.T) {
		recs := farStreamRecords()
		for i := 150; i < len(recs); i++ {
			recs[i].Seq += 20000
		}
		in := inbound(t, recs, testConfig())
		// Packet 150 waits for 151 to confirm the jump, so it is in no
		// run; 25..149 and 151..249 are, without loss between them.
		if in.Expected != 224 || in.Lost != 0 || in.SSRCChanges != 0 {
			t.Fatalf("%+v", in)
		}
	})
}

func TestAnalyzeGapsAndStall(t *testing.T) {
	t.Run("the far end stops sending", func(t *testing.T) {
		in := inbound(t, stream(0x1234, 100, 0, 0, 180), testConfig())
		// Sequence loss cannot see it: the expected range ends at the
		// last packet. The last arrives at 3610 ms.
		if in.Lost != 0 || in.Expected != 156 || in.StallMs != 1390 || in.MaxGapMs != 1390 || !in.StoppedEarly {
			t.Fatalf("%+v", in)
		}
	})
	t.Run("a stream with comfort noise suppresses silence", func(t *testing.T) {
		recs := stream(0x1234, 100, 0, 0, 180)
		recs[60].PT = PTComfortNoise
		if in := inbound(t, recs, testConfig()); in.StoppedEarly {
			t.Fatalf("%+v", in)
		}
	})
	t.Run("the window's edges count as gaps", func(t *testing.T) {
		recs := without(stream(0x1234, 100, 0, 60, 260), 200)
		in := inbound(t, recs, testConfig())
		// First arrival at 1210 ms, 710 ms into the window.
		if in.MaxGapMs != 710 || in.StallMs != 10 || in.Lost != 1 {
			t.Fatalf("%+v", in)
		}
	})
}

func TestAnalyzeBurstiness(t *testing.T) {
	bursty := inbound(t, without(farStreamRecords(), 200, 201, 202, 203, 204), testConfig())
	random := inbound(t, without(farStreamRecords(), 160, 200, 240, 280, 320), testConfig())
	// Both lose 5 of 225 (2.22%). One run of 5: p = 1/219, q = 1/5,
	// BurstR = 4.89. Isolated losses: p = 5/219, q = 1, held at 1.
	if bursty.LossPct != 2.22 || random.LossPct != 2.22 || bursty.BurstR != 4.89 || random.BurstR != 1 {
		t.Fatalf("bursty %+v\nrandom %+v", bursty, random)
	}
	// Ie,eff = 95·2.22/(2.22/4.89 + 25.1) = 8.2531 against
	// 95·2.22/27.32 = 7.7196: R 83.03 against 83.56.
	if bursty.Quality.RFactor != 83.0 || random.Quality.RFactor != 83.6 {
		t.Fatalf("R bursty %v random %v", bursty.Quality.RFactor, random.Quality.RFactor)
	}
}

func TestAnalyzeJitter(t *testing.T) {
	t.Run("alternating ±10 ms converges to the A.8 value", func(t *testing.T) {
		recs := farStreamRecords()
		for i := range recs {
			if i%2 == 1 {
				recs[i].At += 10 * time.Millisecond
			}
		}
		in := inbound(t, recs, testConfig())
		// Every |D| is 10 ms, so after n updates J = 10·(1 − (15/16)^n).
		// The window holds 225 packets (25..249), 224 updates; the mean is
		// over updates 16..224: 10 − (10/209)·Σ (15/16)^n
		// = 10 − (10/209)·16·(15/16)^16·(1 − (15/16)^209).
		r := 15.0 / 16
		mean := 10 - 10.0/209*16*math.Pow(r, 16)*(1-math.Pow(r, 209))
		last := 10 * (1 - math.Pow(r, 224))
		if !floatIs(in.JitterMs, mean) || !floatIs(in.JitterMaxMs, last) || !floatIs(in.JitterLastMs, last) {
			t.Fatalf("jitter %v max %v last %v, want %.4f %.4f %.4f", deref(in.JitterMs), deref(in.JitterMaxMs), deref(in.JitterLastMs), mean, last, last)
		}
		if math.Abs(mean-9.7274) > 5e-4 {
			t.Fatalf("closed form = %.4f", mean) // pins the oracle itself
		}
	})
	t.Run("too few packets to settle", func(t *testing.T) {
		in := inbound(t, stream(0x1234, 100, 0, 0, 40), testConfig())
		// 16 packets in the window: 15 updates, none settled.
		if in.JitterMs != nil || in.JitterMaxMs != nil || in.JitterLastMs == nil {
			t.Fatalf("%+v", in)
		}
	})
}

// echoCall is an echo line returning every packet 40 ms after it left. The
// probe's sends wobble by 5 ms (odd packets late), which the echo
// statistics must not mistake for network jitter. Sends 25..234 fall in the
// echo expectation [500 ms, 4700 ms].
func echoCall(edit func(k int, r *Record) bool) ([]Record, Config) {
	cfg := testConfig()
	var recs []Record
	for k := range 250 {
		sent := Sent{Seq: 1000 + uint16(k), At: time.Duration(k) * 20 * time.Millisecond}
		if k%2 == 1 {
			sent.At += 5 * time.Millisecond
		}
		cfg.Sent = append(cfg.Sent, sent)
		r := Record{At: sent.At + 40*time.Millisecond, SSRC: ourSSRC, Seq: sent.Seq, TS: 160 * uint32(k), PT: 0,
			Echo: EchoBySSRC, EchoSeq: sent.Seq}
		if edit == nil || edit(k, &r) {
			recs = append(recs, r)
		}
	}
	slices.SortStableFunc(recs, byArrival)
	return recs, cfg
}

func TestAnalyzeEcho(t *testing.T) {
	t.Run("a clean reflector", func(t *testing.T) {
		recs, cfg := echoCall(nil)
		rep := Analyze(recs, cfg)
		rt := rep.RoundTrip
		want := EchoStats{
			MatchedBy: "ssrc", Expected: 210, Matched: 210, BurstR: 1, JitterMs: fp(0),
			RTTMs: &RTTStats{P50: 40, P95: 40, Max: 40},
			// owd 20 ms: d = 100, R = 93.2 − 2.4 = 90.8, MOS 4.36.
			Quality: &Quality{RFactor: 90.8, MOS: 4.36, DelayMs: 100, DelaySource: DelayEcho},
		}
		if rt == nil || !reflect.DeepEqual(*rt, want) {
			got, _ := json.Marshal(rt)
			t.Fatalf("round trip %s", got)
		}
		// Echoes of sends 23..248 arrive in [500 ms, 5 s].
		if rep.Inbound != nil || rep.Accepted != 226 {
			t.Fatalf("inbound %+v accepted %d", rep.Inbound, rep.Accepted)
		}
	})
	t.Run("loss, a late echo and a duplicate", func(t *testing.T) {
		recs, cfg := echoCall(func(k int, r *Record) bool {
			if k == 150 {
				r.At += 70 * time.Millisecond
			}
			return k < 100 || k > 102
		})
		dup := recs[slices.IndexFunc(recs, func(r Record) bool { return r.EchoSeq == 1050 })]
		dup.At += time.Millisecond
		recs = append(recs, dup)
		rt := Analyze(recs, cfg).RoundTrip
		// 3 of 210 lost (1.43%), plus one late (1.90%). Indicator: a run of
		// 3 and a run of 1 in 210: p = 2/205, q = 2/4, BurstR = 1.96.
		if rt.Matched != 207 || rt.LossPct != 1.43 || rt.Late != 1 || rt.EffectiveLossPct != 1.9 ||
			rt.Duplicates != 1 || rt.BurstR != 1.96 || rt.RTTMs.Max != 110 || rt.RTTMs.P95 != 40 {
			t.Fatalf("%+v rtt %+v", rt, rt.RTTMs)
		}
	})
	t.Run("a re-originated echo is matched by its marker", func(t *testing.T) {
		recs, cfg := echoCall(func(k int, r *Record) bool {
			r.SSRC, r.Seq, r.Echo = 0x5555, uint16(k), EchoByMarker
			return true
		})
		if rt := Analyze(recs, cfg).RoundTrip; rt.MatchedBy != "marker" || rt.Matched != 210 {
			t.Fatalf("%+v", rt)
		}
	})
	t.Run("echo jitter uses the real send times", func(t *testing.T) {
		recs, cfg := echoCall(func(k int, r *Record) bool {
			if k%2 == 1 {
				r.At += 10 * time.Millisecond
			}
			return true
		})
		rt := Analyze(recs, cfg).RoundTrip
		if rt.JitterMs == nil || *rt.JitterMs < 9 || *rt.JitterMs > 10 {
			t.Fatalf("jitter %v", rt.JitterMs)
		}
	})
	t.Run("strays are ignored", func(t *testing.T) {
		recs, cfg := echoCall(func(k int, r *Record) bool { return k < 3 })
		recs = append(recs,
			Record{At: time.Second, Echo: EchoBySSRC, EchoSeq: 7},                    // never sent
			Record{At: 10 * time.Millisecond, Echo: EchoBySSRC, EchoSeq: 1000 + 200}) // back before it left
		rt := Analyze(recs, cfg).RoundTrip
		if rt.Expected != 210 || rt.Matched != 0 || rt.LossPct != 100 || rt.Duplicates != 0 || rt.RTTMs != nil {
			t.Fatalf("%+v", rt)
		}
	})
}

func TestAnalyzeDelaySource(t *testing.T) {
	cfg := testConfig()
	t.Run("echo round trip, for every basis", func(t *testing.T) {
		recs, ecfg := echoCall(nil)
		recs = append(recs, farStreamRecords()...)
		rep := Analyze(recs, ecfg)
		if q := rep.Inbound.Quality; q.DelaySource != DelayEcho || q.DelayMs != 100 || q.RFactor != 90.8 {
			t.Fatalf("inbound quality %+v", q)
		}
	})
	t.Run("RTCP when too few echoes", func(t *testing.T) {
		recs, ecfg := echoCall(func(k int, _ *Record) bool { return k >= 25 && k < 34 })
		ecfg.RTCPRoundTrip = 60 * time.Millisecond
		recs = append(recs, farStreamRecords()...)
		rep := Analyze(recs, ecfg)
		if q := rep.Inbound.Quality; q.DelaySource != DelayRTCP || q.DelayMs != 110 {
			t.Fatalf("inbound quality %+v", q)
		}
	})
	t.Run("unmeasured", func(t *testing.T) {
		if q := inbound(t, farStreamRecords(), cfg).Quality; q.DelaySource != DelayUnmeasured || q.DelayMs != 80 {
			t.Fatalf("quality %+v", q)
		}
	})
	t.Run("ptime", func(t *testing.T) {
		c := cfg
		c.PtimeMs = 30
		if q := inbound(t, farStreamRecords(), c).Quality; q.DelayMs != 90 {
			t.Fatalf("quality %+v", q)
		}
	})
}

func TestAnalyzeGates(t *testing.T) {
	t.Run("a short window gets statistics but no estimate", func(t *testing.T) {
		recs, cfg := echoCall(nil)
		recs = append(recs, farStreamRecords()...)
		cfg.Stop = 2200 * time.Millisecond
		rep := Analyze(recs, cfg)
		// Window 1.7 s: packets 25..109.
		if rep.Inbound.Expected != 85 || rep.Inbound.Quality != nil || rep.RoundTrip.Quality != nil {
			t.Fatalf("inbound %+v round trip %+v", rep.Inbound, rep.RoundTrip)
		}
	})
	t.Run("too few expected packets", func(t *testing.T) {
		recs := stream(0x1234, 100, 0, 0, 260)
		var sparse []Record
		for i, r := range recs {
			if i%5 == 0 { // 100 ms ptime: 45 packets in the window
				r.Seq = uint16(i / 5)
				sparse = append(sparse, r)
			}
		}
		if in := inbound(t, sparse, testConfig()); in.Expected != 45 || in.Quality != nil {
			t.Fatalf("%+v", in)
		}
	})
}

func TestAnalyzeAfterBye(t *testing.T) {
	cfg := testConfig()
	cfg.Bye = 5 * time.Second
	recs, _ := echoCall(nil)
	recs = append(recs, stream(0x1234, 100, 0, 0, 360)...)
	// Far packets arriving after 6 s: 300..360.
	if rep := Analyze(recs, cfg); rep.AfterBye != 61 {
		t.Fatalf("after BYE %d", rep.AfterBye)
	}
	cfg.Bye = 0
	if rep := Analyze(recs, cfg); rep.AfterBye != 0 {
		t.Fatalf("no BYE, after BYE %d", rep.AfterBye)
	}
}

func TestAnalyzeSortsRecords(t *testing.T) {
	recs, cfg := echoCall(nil)
	recs = append(recs, without(farStreamRecords(), 150)...)
	sorted := slices.Clone(recs)
	slices.SortStableFunc(sorted, byArrival)
	reversed := slices.Clone(sorted)
	slices.Reverse(reversed)
	input := slices.Clone(reversed)
	if !reflect.DeepEqual(Analyze(sorted, cfg), Analyze(input, cfg)) {
		t.Fatal("arrival order of the input changes the report")
	}
	if !slices.Equal(input, reversed) {
		t.Fatal("Analyze reordered its caller's records")
	}
}

func TestSSRCMarshalsAsHex(t *testing.T) {
	b, err := json.Marshal(struct{ S SSRC }{0x00ab0001})
	if err != nil || string(b) != `{"S":"00ab0001"}` {
		t.Fatalf("%s %v", b, err)
	}
}

// Degenerate inputs still give a report every block of which marshals:
// one NaN in the metrics envelope would drop all of it.
func TestAnalyzeDegenerateInputsMarshal(t *testing.T) {
	one := stream(0x1234, 100, 0, 30, 30)
	var dups []Record
	for i := range 100 {
		r := one[0]
		r.At = 600*time.Millisecond + time.Duration(i)*time.Millisecond
		dups = append(dups, r)
	}
	pair := stream(0x1234, 100, 0, 30, 31)
	allLost, allLostCfg := echoCall(func(k int, _ *Record) bool { return k == 5 })
	backwards := testConfig()
	backwards.Answer, backwards.Stop = 3*time.Second, time.Second

	cases := []struct {
		name string
		recs []Record
		cfg  Config
	}{
		{"empty", nil, testConfig()},
		{"one packet", one, testConfig()},
		{"two packets", pair, testConfig()},
		{"all duplicates", append(slices.Clone(pair), dups...), testConfig()},
		{"every echo lost", allLost, allLostCfg},
		{"stop before the answer", farStreamRecords(), backwards},
		{"zero-length window", farStreamRecords(), Config{Answer: 0, Stop: Warmup}},
	}
	for _, c := range cases {
		rep := Analyze(c.recs, c.cfg)
		checkReport(t, c.name, rep)
	}
	if rt := Analyze(allLost, allLostCfg).RoundTrip; rt == nil || rt.Matched != 0 || rt.LossPct != 100 || rt.BurstR != 1 {
		t.Fatalf("every echo lost: %+v", rt)
	}
}

// checkReport asserts what holds for any report: every block marshals, and
// the figures are in range.
func checkReport(t *testing.T, name string, rep Report) {
	t.Helper()
	for _, block := range []any{rep.Inbound, rep.RoundTrip} {
		b, err := json.Marshal(block)
		if err != nil || strings.Contains(string(b), "NaN") || strings.Contains(string(b), "-0,") {
			t.Fatalf("%s: %s %v", name, b, err)
		}
	}
	checkQuality := func(q *Quality) {
		if q != nil && (q.MOS < 1 || q.MOS > 4.5 || q.RFactor < 0 || q.RFactor > 100 || q.DelayMs < 80-10) {
			t.Fatalf("%s: quality %+v", name, q)
		}
	}
	if in := rep.Inbound; in != nil {
		if in.Expected < 1 || in.Lost < 0 || in.Lost > in.Expected || in.Late < 0 || in.Lost+in.Late > in.Expected ||
			in.LossPct < 0 || in.LossPct > in.EffectiveLossPct || in.EffectiveLossPct > 100 || in.BurstR < 1 ||
			in.Duplicates < 0 || in.MaxGapMs < in.StallMs || in.StallMs < 0 {
			t.Fatalf("%s: inbound %+v", name, in)
		}
		checkQuality(in.Quality)
	}
	if rt := rep.RoundTrip; rt != nil {
		if rt.Matched > rt.Expected || rt.LossPct < 0 || rt.LossPct > 100 || rt.EffectiveLossPct > 100 || rt.BurstR < 1 {
			t.Fatalf("%s: round trip %+v", name, rt)
		}
		checkQuality(rt.Quality)
	}
}

// FuzzAnalyze: whatever the far end sends (any SSRCs, sequence numbers,
// timestamps, payload types and echoes, in any order), the report is in
// range and marshals.
func FuzzAnalyze(f *testing.F) {
	seed := make([]byte, 0, 9*300)
	for k := range 300 {
		seed = binary.BigEndian.AppendUint16(seed, uint16(10+20*k))
		seed = binary.BigEndian.AppendUint16(seed, uint16(100+k))
		seed = append(seed, 0, 0)
		seed = binary.BigEndian.AppendUint16(seed, 160)
		seed = append(seed, 0)
	}
	f.Add(uint16(5000), seed)
	f.Add(uint16(3000), []byte{0, 1, 0xff, 0xff, 7, 1, 0, 0, 1, 0, 2, 0, 0, 7, 3, 0, 0, 3})
	f.Fuzz(func(t *testing.T, stopMs uint16, data []byte) {
		cfg := testConfig()
		cfg.Stop = time.Duration(stopMs) * time.Millisecond
		cfg.Bye = cfg.Stop
		for k := range 250 {
			cfg.Sent = append(cfg.Sent, Sent{Seq: uint16(k), At: time.Duration(k) * 20 * time.Millisecond})
		}
		pts := []uint8{0, 8, PTComfortNoise, PTTelephoneEvent, 18}
		var recs []Record
		var ts uint32
		for len(data) >= 9 {
			c := data[:9]
			data = data[9:]
			ts += uint32(binary.BigEndian.Uint16(c[6:]))
			r := Record{
				At:     time.Duration(binary.BigEndian.Uint16(c[0:])) * time.Millisecond,
				Seq:    binary.BigEndian.Uint16(c[2:]),
				SSRC:   uint32(c[4] % 8),
				PT:     pts[int(c[5])%len(pts)],
				TS:     ts,
				Marker: c[8]&1 != 0,
			}
			if c[4]%8 == 7 {
				r.SSRC, r.Echo, r.EchoSeq = ourSSRC, EchoBySSRC, r.Seq
				if c[8]&2 != 0 {
					r.Echo = EchoByMarker
				}
			}
			recs = append(recs, r)
		}
		checkReport(t, "fuzz", Analyze(recs, cfg))
	})
}

func TestSSRCTextRoundTrip(t *testing.T) {
	b, err := json.Marshal(struct{ S SSRC }{0x0000beef})
	if err != nil || string(b) != `{"S":"0000beef"}` {
		t.Fatalf("marshal = %s, %v", b, err)
	}
	var back struct{ S SSRC }
	if err := json.Unmarshal(b, &back); err != nil || back.S != 0xbeef {
		t.Fatalf("unmarshal = %#x, %v", back.S, err)
	}
	for _, bad := range []string{`"beef"`, `"0000beefa"`, `"zzzzzzzz"`} {
		if err := json.Unmarshal([]byte(`{"S":`+bad+`}`), &back); err == nil {
			t.Fatalf("unmarshal %s: want an error", bad)
		}
	}
}
