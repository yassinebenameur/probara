package rtp

import (
	"encoding/binary"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"
)

func TestNTPTime(t *testing.T) {
	if got := NTPTime(time.Unix(0, 0)); got != 2208988800<<32 {
		t.Fatalf("NTP of the Unix epoch = %#x", got)
	}
	ntp := NTPTime(time.Unix(1, int64(500*time.Millisecond)))
	if ntp != (2208988801<<32 | 1<<31) {
		t.Fatalf("NTP of 1.5 s = %#x", ntp)
	}
	// The middle 32 bits: the low 16 of the seconds, the high 16 of the
	// fraction.
	if got := NTPMiddle(ntp); got != (2208988801&0xffff)<<16|0x8000 {
		t.Fatalf("middle = %#x", got)
	}
}

func TestSenderReportEncoding(t *testing.T) {
	si := SenderInfo{SSRC: 0x50524f42, NTP: 0x0102030405060708, RTPTime: 0xdeadbeef, Packets: 125, Octets: 20000}
	for n := range 9 {
		cname := strings.Repeat("c", n)
		b := AppendBye(AppendSenderReport(nil, si, cname), si.SSRC)
		if len(b)%4 != 0 {
			t.Fatalf("cname %d: %d bytes, not 32-bit aligned", n, len(b))
		}
		c, err := ParseRTCP(b)
		if err != nil || c.Reports != 1 || len(c.Blocks) != 0 {
			t.Fatalf("cname %d: parsed %+v, %v", n, c, err)
		}
		// SR: V=2 RC=0, 28 bytes; then the SDES chunk carries the CNAME.
		if b[0] != 0x80 || b[1] != RTCPSenderReport || binary.BigEndian.Uint16(b[2:]) != 6 {
			t.Fatalf("SR header % x", b[:4])
		}
		if binary.BigEndian.Uint32(b[4:]) != si.SSRC || binary.BigEndian.Uint64(b[8:]) != si.NTP ||
			binary.BigEndian.Uint32(b[16:]) != si.RTPTime || binary.BigEndian.Uint32(b[20:]) != 125 || binary.BigEndian.Uint32(b[24:]) != 20000 {
			t.Fatalf("sender info % x", b[4:28])
		}
		sdes := b[28:]
		if sdes[1] != RTCPSourceDescription || sdes[8] != sdesCNAME || int(sdes[9]) != n || string(sdes[10:10+n]) != cname || sdes[10+n] != 0 {
			t.Fatalf("cname %d: SDES % x", n, sdes)
		}
		bye := b[len(b)-8:]
		if bye[0] != 0x81 || bye[1] != RTCPGoodbye || binary.BigEndian.Uint32(bye[4:]) != si.SSRC {
			t.Fatalf("BYE % x", bye)
		}
	}
	long := AppendSenderReport(nil, si, strings.Repeat("x", 300))
	if long[28+9] != maxCNAME {
		t.Fatalf("a 300-octet CNAME is %d long, want it cut at 255", long[28+9])
	}
	if _, err := ParseRTCP(long); err != nil {
		t.Fatal(err)
	}
}

// rr builds an RR from sender with the given report blocks.
func rr(sender uint32, blocks ...ReportBlock) []byte {
	b := []byte{0x80 | byte(len(blocks)), RTCPReceiverReport}
	b = binary.BigEndian.AppendUint16(b, uint16(1+6*len(blocks)))
	b = binary.BigEndian.AppendUint32(b, sender)
	for _, rb := range blocks {
		b = binary.BigEndian.AppendUint32(b, rb.SSRC)
		b = binary.BigEndian.AppendUint32(b, uint32(rb.FractionLost)<<24|uint32(rb.CumulativeLost)&0xffffff)
		b = binary.BigEndian.AppendUint32(b, rb.HighestSeq)
		b = binary.BigEndian.AppendUint32(b, rb.Jitter)
		b = binary.BigEndian.AppendUint32(b, rb.LSR)
		b = binary.BigEndian.AppendUint32(b, rb.DLSR)
	}
	return b
}

func TestParseRTCPReportBlocks(t *testing.T) {
	ours := ReportBlock{SSRC: 0x50524f42, FractionLost: 13, CumulativeLost: -3, HighestSeq: 1<<16 | 42, Jitter: 80, LSR: 0x12345678, DLSR: 0x8000}
	other := ReportBlock{SSRC: 7, CumulativeLost: 0x7fffff}
	// An SR carrying a block, then an RR carrying two, then an SDES and a
	// padded APP packet the parser skips.
	sr := AppendSenderReport(nil, SenderInfo{SSRC: 9}, "far")
	sr[0] |= 1
	sr = append(sr[:28], append(rr(0, ours)[8:], sr[28:]...)...)
	binary.BigEndian.PutUint16(sr[2:], 12)
	b := append(sr, rr(9, ours, other)...)
	b = append(b, 0xa0, 204, 0, 2, 0, 0, 0, 9, 0, 0, 0, 4) // APP, padded by 4
	c, err := ParseRTCP(b)
	if err != nil {
		t.Fatal(err)
	}
	if c.Reports != 2 || len(c.Blocks) != 3 || c.Blocks[0] != ours || c.Blocks[1] != ours || c.Blocks[2] != other {
		t.Fatalf("parsed %+v", c)
	}
}

func TestParseRTCPRefusesMalformed(t *testing.T) {
	valid := AppendSenderReport(nil, SenderInfo{SSRC: 9}, "far")
	with := func(edit func(b []byte) []byte) []byte { return edit(append([]byte(nil), valid...)) }
	cases := []struct {
		name string
		b    []byte
		want error
	}{
		{"empty", nil, ErrRTCPShort},
		{"version 1", with(func(b []byte) []byte { b[0] = 0x40; return b }), ErrRTCPVersion},
		{"starts with an SDES", valid[28:], ErrRTCPFirst},
		{"starts padded", with(func(b []byte) []byte { b[0] |= 0x20; return b }), ErrRTCPFirst},
		{"RTP", AppendPacket(nil, Header{PayloadType: 0, SSRC: 1}, make([]byte, 160)), ErrRTCPFirst},
		{"length overruns", with(func(b []byte) []byte { b[3] = 40; return b }), ErrRTCPLength},
		{"trailing octets", append(append([]byte(nil), valid...), 0x80, 0), ErrRTCPLength},
		{"second packet of another version", with(func(b []byte) []byte { b[28] = 0x41; return b }), ErrRTCPVersion},
		{"padding before the last", append(rr(1), with(func(b []byte) []byte { b[0] |= 0x20; return b })...), ErrRTCPPadding},
		{"zero padding count", with(func(b []byte) []byte { b[28] |= 0x20; b[len(b)-1] = 0; return b }), ErrRTCPPadding},
		{"blocks overrun", with(func(b []byte) []byte { b[0] |= 1; return b }), ErrRTCPOverflow},
		{"short RR", []byte{0x80, RTCPReceiverReport, 0, 0}, ErrRTCPOverflow},
	}
	for _, tc := range cases {
		if _, err := ParseRTCP(tc.b); !errors.Is(err, tc.want) {
			t.Errorf("%s: err = %v, want %v", tc.name, err, tc.want)
		}
	}
}

func TestRoundTrip(t *testing.T) {
	const lsr, dlsr = 0x10000000, 0x8000 // the SR, and half a second held
	arrival := uint32(lsr + dlsr + 2621) // 40 ms of network on top
	if rtt, ok := RoundTrip(arrival, ReportBlock{LSR: lsr, DLSR: dlsr}); !ok || rtt < 39*time.Millisecond || rtt > 40*time.Millisecond {
		t.Fatalf("rtt = %s %v, want about 40ms", rtt, ok)
	}
	for name, b := range map[string]ReportBlock{
		"no SR yet":       {LSR: 0, DLSR: dlsr},
		"negative":        {LSR: lsr, DLSR: dlsr + 3000},
		"above 5 s":       {LSR: lsr - 6<<16, DLSR: dlsr},
		"wrapped garbage": {LSR: arrival + 1, DLSR: 0},
	} {
		if rtt, ok := RoundTrip(arrival, b); ok {
			t.Errorf("%s: rtt = %s, want none", name, rtt)
		}
	}
	// Across the middle-32 wrap.
	if rtt, ok := RoundTrip(1000, ReportBlock{LSR: 0xffffff00, DLSR: 100}); !ok || rtt != time.Duration(1156*int64(time.Second)/65536) {
		t.Fatalf("rtt across the wrap = %s %v", rtt, ok)
	}
}

func TestAnalyzeRTCP(t *testing.T) {
	const us = 0x50524f42
	cfg := RTCPConfig{SSRC: us, FirstSeq: 65500, Sent: 300, FirstSend: 100 * time.Millisecond, SenderReports: []uint32{0x10000000}}
	block := func(past int, cum int32, fraction uint8) ReportBlock {
		return ReportBlock{SSRC: us, HighestSeq: uint32(int(cfg.FirstSeq) + past), CumulativeLost: cum, FractionLost: fraction, Jitter: 24}
	}
	arrive := func(at time.Duration, blocks ...ReportBlock) RTCPArrival {
		return RTCPArrival{At: at, NTPMiddle: 0x10000000 + 0x8000 + 2621, Compound: Compound{Reports: 1, Blocks: blocks}}
	}

	t.Run("loss between the first and last report", func(t *testing.T) {
		first, last := block(99, 1, 0), block(299, 5, 3)
		last.LSR, last.DLSR = 0x10000000, 0x8000
		rep := AnalyzeRTCP([]RTCPArrival{arrive(5*time.Second, last), arrive(2*time.Second, first, ReportBlock{SSRC: 7, HighestSeq: 1})}, cfg)
		ob := rep.Outbound
		if ob == nil || ob.Reports != 2 || ob.Expected != 200 || ob.LossPct != 2 || ob.CumulativeLost != 5 || ob.JitterMs != 3 {
			t.Fatalf("outbound = %+v", ob)
		}
		if !rep.Confirmed || rep.Negative || !rep.Measured || ob.RTTMs == nil || *ob.RTTMs != 39.99 {
			t.Fatalf("report = %+v rtt = %v", rep, ob.RTTMs)
		}
	})
	t.Run("one report: its cumulative loss over what it covers", func(t *testing.T) {
		// 7 of 141, not the fraction lost (13/256), which covers only the
		// interval since the far end's previous report.
		rep := AnalyzeRTCP([]RTCPArrival{arrive(3*time.Second, block(140, 7, 13))}, cfg)
		if ob := rep.Outbound; ob == nil || ob.Expected != 141 || ob.LossPct != 4.96 || ob.Unreported != 0 || ob.RTTMs != nil || rep.Measured {
			t.Fatalf("report = %+v outbound = %+v", rep, rep.Outbound)
		}
	})
	t.Run("a far end that stopped hearing the probe", func(t *testing.T) {
		// 300 packets every 20 ms from 100 ms on. Both reports stop at the
		// 141st, nothing lost, fraction 0: the tail never shows in RFC
		// 3550's own figures. By 5.9 s less a ReportLag the probe had sent
		// 241, so 100 went unreported.
		sends := make([]Sent, 300)
		for i := range sends {
			sends[i] = Sent{At: 100*time.Millisecond + time.Duration(i)*20*time.Millisecond, Seq: cfg.FirstSeq + uint16(i)}
		}
		c := cfg
		c.Sends = sends
		rep := AnalyzeRTCP([]RTCPArrival{arrive(3*time.Second, block(140, 0, 0)), arrive(5900*time.Millisecond, block(140, 0, 0))}, c)
		ob := rep.Outbound
		if ob == nil || ob.Unreported != 100 || ob.Expected != 241 || ob.LossPct != 41.49 || !rep.Confirmed {
			t.Fatalf("report = %+v outbound = %+v", rep, ob)
		}
		// A far end keeping up: its last report reaches what the probe had
		// sent a ReportLag before it, so nothing is unreported.
		rep = AnalyzeRTCP([]RTCPArrival{arrive(3*time.Second, block(140, 0, 0)), arrive(5900*time.Millisecond, block(244, 2, 0))}, c)
		if ob := rep.Outbound; ob == nil || ob.Unreported != 0 || ob.Expected != 104 || ob.LossPct != 1.92 {
			t.Fatalf("outbound = %+v", ob)
		}
	})
	t.Run("an LSR the probe never sent gives no round trip", func(t *testing.T) {
		b := block(140, 0, 0)
		b.LSR, b.DLSR = 0x20000000, 0
		if rep := AnalyzeRTCP([]RTCPArrival{arrive(3*time.Second, b)}, cfg); rep.Measured {
			t.Fatalf("report = %+v", rep)
		}
	})
	t.Run("too few of ours to confirm", func(t *testing.T) {
		rep := AnalyzeRTCP([]RTCPArrival{arrive(3*time.Second, block(9, 0, 0))}, cfg)
		if rep.Confirmed || rep.Negative || rep.Outbound == nil {
			t.Fatalf("report = %+v", rep)
		}
	})
	t.Run("a block beyond what the probe sent", func(t *testing.T) {
		rep := AnalyzeRTCP([]RTCPArrival{arrive(3*time.Second, block(300, 0, 0)), arrive(4 * time.Second), arrive(5 * time.Second)}, cfg)
		if rep.Outbound != nil || rep.Confirmed || rep.Negative {
			t.Fatalf("report = %+v, want the stale block ignored and no negative (the far end did name us)", rep)
		}
	})
	t.Run("explicit negative", func(t *testing.T) {
		other := ReportBlock{SSRC: 7, HighestSeq: 100}
		early := arrive(cfg.FirstSend+500*time.Millisecond, other)
		if rep := AnalyzeRTCP([]RTCPArrival{early, arrive(2 * time.Second)}, cfg); rep.Negative {
			t.Fatal("one report after the first second is not a negative")
		}
		rep := AnalyzeRTCP([]RTCPArrival{early, arrive(2 * time.Second), arrive(4*time.Second, other)}, cfg)
		if !rep.Negative || rep.Confirmed || rep.Outbound != nil {
			t.Fatalf("report = %+v, want a negative", rep)
		}
		if rep := AnalyzeRTCP([]RTCPArrival{arrive(2 * time.Second), arrive(4 * time.Second)}, RTCPConfig{SSRC: us}); rep.Negative {
			t.Fatal("a negative for a probe that sent nothing")
		}
	})
}

// The outbound basis is estimated on the same delay as the others, with the
// burst ratio assumed and no discards, once it covers enough packets.
func TestAnalyzeOutboundQuality(t *testing.T) {
	cfg := testConfig()
	cfg.RTCPRoundTrip = 40 * time.Millisecond
	cfg.Outbound = &OutboundStats{Reports: 2, Expected: 200, LossPct: 1}
	rep := Analyze(farStreamRecords(), cfg)
	q := rep.Outbound.Quality
	if q == nil || q.DelaySource != DelayRTCP || q.DelayMs != rep.Inbound.Quality.DelayMs || q.MOS != 4.26 ||
		q.BurstR != 1 || q.BurstRSource != BurstAssumed || q.Discards != DiscardsNotReported {
		t.Fatalf("outbound quality = %+v", q)
	}
	if cfg.Outbound.Quality != nil {
		t.Fatal("Analyze changed its input")
	}
	raw, err := json.Marshal(rep.Outbound)
	if err != nil || !strings.Contains(string(raw), `"mos_cqe":4.26`) || !strings.Contains(string(raw), `"burst_r_source":"assumed"`) {
		t.Fatalf("outbound json = %s, %v", raw, err)
	}

	cfg.Outbound = &OutboundStats{Reports: 1, Expected: 90, LossPct: 1} // 1.8 s at 20 ms
	if rep := Analyze(farStreamRecords(), cfg); rep.Outbound == nil || rep.Outbound.Quality != nil {
		t.Fatalf("outbound = %+v, want counts without an estimate", rep.Outbound)
	}
}

func FuzzParseRTCP(f *testing.F) {
	f.Add(AppendBye(AppendSenderReport(nil, SenderInfo{SSRC: 1}, "probe"), 1))
	f.Add(rr(9, ReportBlock{SSRC: 1, HighestSeq: 5}))
	f.Add([]byte{0x80, RTCPReceiverReport, 0, 1, 0, 0, 0, 1})
	f.Fuzz(func(t *testing.T, b []byte) {
		c, err := ParseRTCP(b)
		if err != nil {
			return
		}
		if c.Reports < 1 || len(c.Blocks) > len(b)/reportBlockSize {
			t.Fatalf("parsed %+v from %d bytes", c, len(b))
		}
		for _, rb := range c.Blocks {
			RoundTrip(uint32(len(b)), rb)
		}
	})
}
