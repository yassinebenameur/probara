package rtp

import (
	"cmp"
	"encoding/binary"
	"errors"
	"slices"
	"time"
)

// The thin slice of RTCP (RFC 3550 §6) a test call speaks. The probe sends a
// compound sender report (an SR with no report blocks, then an SDES with its
// CNAME) a few times per call and a BYE when it stops; it reads the far
// end's sender and receiver reports for a report block about its own SSRC,
// which says how much of the probe's audio arrived (two-way evidence without
// an echo line), the far end's jitter on it, and the round trip from
// LSR/DLSR. Nothing else (XR, APP, the SDES items of others) is read.

// RTCP packet types.
const (
	RTCPSenderReport      = 200
	RTCPReceiverReport    = 201
	RTCPSourceDescription = 202
	RTCPGoodbye           = 203
)

const (
	// ReportEvidence: a report block whose extended highest sequence number
	// is at least this far past the probe's first shows the probe's audio
	// reached the far end.
	ReportEvidence = 10
	// NegativeReports: this many far-end SRs and RRs arriving at least
	// NegativeAfter after the probe's first packet, none with a block about
	// it, say the far end heard none of it (RFC 3550 receivers report on
	// every source they hear).
	NegativeReports = 2
	NegativeAfter   = time.Second
	// MaxRoundTrip bounds a believable LSR/DLSR round trip; anything outside
	// [0, MaxRoundTrip] is a clock or arithmetic artefact and is discarded.
	MaxRoundTrip = 5 * time.Second
	// ReportLag: the last report's highest sequence number may trail what
	// the probe had sent a round trip before it arrived by up to ReportLag
	// of packets (the far end's own buffering and scheduling); a far end
	// that heard nothing new for longer stopped hearing the probe, and the
	// packets it never reported count as lost (OutboundStats.Unreported).
	ReportLag = time.Second

	// The outbound basis's estimate: RTCP carries the far end's network
	// loss only, with no loss pattern and no late discards.
	BurstAssumed        = "assumed"
	DiscardsNotReported = "not_reported"

	rtcpHeaderSize  = 4
	senderInfoSize  = 20
	reportBlockSize = 24
	sdesCNAME       = 1
	maxCNAME        = 255
	// ntpEpochOffset is the seconds from the NTP epoch (1900) to Unix's.
	ntpEpochOffset = 2208988800
)

// NTPTime is t as a 64-bit NTP timestamp: seconds since 1900 in the high
// word, the fraction of a second in the low one.
func NTPTime(t time.Time) uint64 {
	secs := uint64(t.Unix() + ntpEpochOffset)
	frac := uint64(t.Nanosecond()) << 32 / uint64(time.Second)
	return secs<<32 | frac
}

// NTPMiddle is the middle 32 bits of an NTP timestamp, in 1/65536 s: what a
// report's LSR echoes back of the SR it answers, and the unit of DLSR.
func NTPMiddle(ntp uint64) uint32 { return uint32(ntp >> 16) }

// SenderInfo is an SR's sender section.
type SenderInfo struct {
	SSRC    uint32
	NTP     uint64 // when the report was sent
	RTPTime uint32 // the same instant on the RTP timestamp clock
	Packets uint32 // RTP packets sent so far
	Octets  uint32 // payload octets sent so far
}

// AppendSenderReport appends a compound RTCP packet to dst: an SR with no
// report blocks (RFC 3550 §6.4.1), then an SDES with si.SSRC's CNAME
// (§6.5.1), which every compound packet carries. A CNAME over 255 octets is
// cut there.
func AppendSenderReport(dst []byte, si SenderInfo, cname string) []byte {
	dst = append(dst, 0x80, RTCPSenderReport, 0, 6) // V=2, RC=0, 7 words
	dst = binary.BigEndian.AppendUint32(dst, si.SSRC)
	dst = binary.BigEndian.AppendUint64(dst, si.NTP)
	dst = binary.BigEndian.AppendUint32(dst, si.RTPTime)
	dst = binary.BigEndian.AppendUint32(dst, si.Packets)
	dst = binary.BigEndian.AppendUint32(dst, si.Octets)

	if len(cname) > maxCNAME {
		cname = cname[:maxCNAME]
	}
	// The chunk: SSRC, the CNAME item, then at least one null octet to end
	// the item list, padded to a 32-bit boundary.
	items := 2 + len(cname)
	pad := 4 - items%4
	words := (4 + items + pad) / 4
	dst = append(dst, 0x81, RTCPSourceDescription) // V=2, SC=1
	dst = binary.BigEndian.AppendUint16(dst, uint16(words))
	dst = binary.BigEndian.AppendUint32(dst, si.SSRC)
	dst = append(dst, sdesCNAME, byte(len(cname)))
	dst = append(dst, cname...)
	return append(dst, make([]byte, pad)...)
}

// AppendBye appends a BYE for ssrc (RFC 3550 §6.6) with no reason; it ends a
// compound packet that AppendSenderReport began.
func AppendBye(dst []byte, ssrc uint32) []byte {
	dst = append(dst, 0x81, RTCPGoodbye, 0, 1) // V=2, SC=1, 2 words
	return binary.BigEndian.AppendUint32(dst, ssrc)
}

// ReportBlock is one reception report of an SR or RR (RFC 3550 §6.4.1):
// what its sender received of the source SSRC.
type ReportBlock struct {
	SSRC           uint32 // the source reported on
	FractionLost   uint8  // lost since the previous report, in 1/256
	CumulativeLost int32  // expected minus received since the start; 24-bit signed
	HighestSeq     uint32 // extended highest sequence number received
	Jitter         uint32 // interarrival jitter, in RTP timestamp units
	LSR            uint32 // the middle 32 bits of the last SR received from SSRC; 0: none
	DLSR           uint32 // the delay since that SR, in 1/65536 s
}

// Compound is what a compound RTCP packet says, as far as the probe reads it.
type Compound struct {
	Reports int           // SR and RR packets in it
	Blocks  []ReportBlock // their report blocks, about any source
}

// Why ParseRTCP refused a datagram.
var (
	ErrRTCPShort    = errors.New("rtcp: shorter than its header")
	ErrRTCPVersion  = errors.New("rtcp: version is not 2")
	ErrRTCPFirst    = errors.New("rtcp: compound does not start with an SR or RR")
	ErrRTCPLength   = errors.New("rtcp: packet lengths do not add up to the datagram")
	ErrRTCPPadding  = errors.New("rtcp: padding before the last packet, or an invalid count")
	ErrRTCPOverflow = errors.New("rtcp: report blocks overrun their packet")
)

// ParseRTCP decodes a compound RTCP packet with the validity checks of RFC
// 3550 Appendix A.2: every packet of version 2, the first an SR or RR
// without padding, padding only on the last, and the packet lengths adding
// up to the datagram exactly. The report blocks of each SR and RR must fit
// their packet; anything after them (a profile extension) is skipped, as are
// packets of every other type.
func ParseRTCP(b []byte) (Compound, error) {
	var c Compound
	if len(b) < rtcpHeaderSize {
		return c, ErrRTCPShort
	}
	if b[0]>>6 != 2 {
		return c, ErrRTCPVersion
	}
	if b[0]&0x20 != 0 || (b[1] != RTCPSenderReport && b[1] != RTCPReceiverReport) {
		return c, ErrRTCPFirst
	}
	for off := 0; off < len(b); {
		if len(b)-off < rtcpHeaderSize {
			return Compound{}, ErrRTCPLength
		}
		if b[off]>>6 != 2 {
			return Compound{}, ErrRTCPVersion
		}
		size := 4 * (int(binary.BigEndian.Uint16(b[off+2:])) + 1)
		if size > len(b)-off {
			return Compound{}, ErrRTCPLength
		}
		if b[off]&0x20 != 0 && off+size != len(b) {
			return Compound{}, ErrRTCPPadding
		}
		pkt := b[off : off+size]
		if pkt[0]&0x20 != 0 {
			pad := int(pkt[len(pkt)-1])
			if pad == 0 || pad > len(pkt)-rtcpHeaderSize {
				return Compound{}, ErrRTCPPadding
			}
			pkt = pkt[:len(pkt)-pad]
		}
		if pkt[1] == RTCPSenderReport || pkt[1] == RTCPReceiverReport {
			start := rtcpHeaderSize + 4 // the sender's SSRC
			if pkt[1] == RTCPSenderReport {
				start += senderInfoSize
			}
			count := int(pkt[0] & 0x1f)
			if len(pkt) < start+count*reportBlockSize {
				return Compound{}, ErrRTCPOverflow
			}
			c.Reports++
			for i := range count {
				c.Blocks = append(c.Blocks, parseReportBlock(pkt[start+i*reportBlockSize:]))
			}
		}
		off += size
	}
	return c, nil
}

func parseReportBlock(b []byte) ReportBlock {
	lost := binary.BigEndian.Uint32(b[4:])
	return ReportBlock{
		SSRC:           binary.BigEndian.Uint32(b),
		FractionLost:   uint8(lost >> 24),
		CumulativeLost: int32(lost<<8) >> 8,
		HighestSeq:     binary.BigEndian.Uint32(b[8:]),
		Jitter:         binary.BigEndian.Uint32(b[12:]),
		LSR:            binary.BigEndian.Uint32(b[16:]),
		DLSR:           binary.BigEndian.Uint32(b[20:]),
	}
}

// RoundTrip is RFC 3550 §6.4.1's round trip for a report block that arrived
// at arrival (an NTP middle 32 on the clock of the probe's own SRs): A − LSR
// − DLSR. It is valid only for a block that echoes an SR (LSR non-zero) and
// comes to a value in [0, MaxRoundTrip].
func RoundTrip(arrival uint32, b ReportBlock) (time.Duration, bool) {
	if b.LSR == 0 {
		return 0, false
	}
	v := int64(int32(arrival - b.LSR - b.DLSR))
	rtt := time.Duration(v * int64(time.Second) / 65536)
	if v < 0 || rtt > MaxRoundTrip {
		return 0, false
	}
	return rtt, true
}

// RTCPArrival is one far-end compound packet as the media session kept it:
// its arrival as a monotonic offset from the session's base and as an NTP
// middle 32 on the same clock as the probe's SRs (A), and what it said.
type RTCPArrival struct {
	At        time.Duration
	NTPMiddle uint32
	Compound
}

// RTCPConfig is what the probe sent, which the far end's reports are read
// against.
type RTCPConfig struct {
	SSRC      uint32        // the probe's
	FirstSeq  uint16        // its first RTP packet's sequence number
	Sent      int           // RTP packets sent, numbered FirstSeq onwards
	FirstSend time.Duration // when the first one left; meaningful when Sent > 0
	// SenderReports are the NTP middle 32 of the SRs the probe sent: a
	// block's LSR must be one of them for its round trip to count.
	SenderReports []uint32
	// Sends are the probe's RTP packets in send order (Config.Sent), which
	// tell how many it had sent by the time of a report; nil reads every
	// report as up to date.
	Sends []Sent
}

// RTCPReport is what the far end's RTCP said about the probe's stream.
type RTCPReport struct {
	Outbound *OutboundStats // nil: no report block about the probe's packets
	// RoundTrip is the last valid LSR/DLSR round trip, when Measured.
	RoundTrip time.Duration
	Measured  bool
	// Confirmed: a report block covers at least ReportEvidence of the
	// probe's packets past its first. Negative: the far end reported
	// (NegativeReports times, from NegativeAfter on) without ever naming
	// the probe's SSRC.
	Confirmed bool
	Negative  bool
}

// OutboundStats is the far end's view of the probe's stream, from its
// report blocks about the probe's SSRC (the outbound basis).
type OutboundStats struct {
	Reports int `json:"reports"` // report blocks about the probe's packets
	// Expected is how many of the probe's packets LossPct covers: between
	// the first and the last report when their counts grew, else from the
	// first packet to the last report's highest, plus Unreported.
	Expected int `json:"expected"`
	// Unreported: packets the probe had sent more than ReportLag before the
	// last report could have reflected them (a round trip before it
	// arrived) that the report's highest sequence number does not reach. A
	// far end that stops hearing the probe keeps reporting the same highest
	// number with nothing lost, so the tail would otherwise be invisible;
	// they count as lost.
	Unreported     int              `json:"unreported,omitempty"`
	LossPct        float64          `json:"loss_pct"`
	CumulativeLost int              `json:"cumulative_lost"` // the last report's, RFC 3550's own figure
	JitterMs       float64          `json:"jitter_ms"`       // the last report's interarrival jitter
	RTTMs          *float64         `json:"rtt_ms,omitempty"`
	Quality        *OutboundQuality `json:"quality,omitempty"`
}

// OutboundQuality is the outbound basis's estimate. RTCP gives network loss
// only: the burst ratio is taken as 1 (random loss) and there are no late
// discards, and the block says so.
type OutboundQuality struct {
	Quality
	BurstR       float64 `json:"burst_r"`
	BurstRSource string  `json:"burst_r_source"`
	Discards     string  `json:"discards"`
}

// AnalyzeRTCP reads the far end's RTCP, in arrival order (sorted here
// otherwise), against what the probe sent. Only blocks about cfg.SSRC count;
// one whose highest sequence number is not a packet the probe sent (a stale
// stream, another source with the same SSRC) is left out of the loss figures
// but may still give a round trip. Pure, like Analyze; its RoundTrip is
// Config.RTCPRoundTrip, and its Outbound is Config.Outbound, which Analyze
// gives a quality estimate on the same delay as every other basis.
func AnalyzeRTCP(arrivals []RTCPArrival, cfg RTCPConfig) RTCPReport {
	if !slices.IsSortedFunc(arrivals, byRTCPArrival) {
		arrivals = slices.Clone(arrivals)
		slices.SortStableFunc(arrivals, byRTCPArrival)
	}
	var (
		rep          RTCPReport
		aboutUs      bool
		laterReports int
		valid        []coveredBlock
	)
	for _, a := range arrivals {
		if cfg.Sent > 0 && a.At >= cfg.FirstSend+NegativeAfter {
			laterReports += a.Reports
		}
		for _, b := range a.Blocks {
			if b.SSRC != cfg.SSRC {
				continue
			}
			aboutUs = true
			if slices.Contains(cfg.SenderReports, b.LSR) {
				if rtt, ok := RoundTrip(a.NTPMiddle, b); ok {
					rep.RoundTrip, rep.Measured = rtt, true
				}
			}
			// The probe sends fewer than 2^16 packets a call, so the low 16
			// bits place the highest number among them, across a wrap.
			past := int(uint16(b.HighestSeq) - cfg.FirstSeq)
			if past >= cfg.Sent {
				continue
			}
			valid = append(valid, coveredBlock{ReportBlock: b, covered: past + 1, at: a.At})
			rep.Confirmed = rep.Confirmed || past >= ReportEvidence
		}
	}
	rep.Negative = !aboutUs && laterReports >= NegativeReports
	if len(valid) == 0 {
		return rep
	}

	first, last := valid[0], valid[len(valid)-1]
	ob := &OutboundStats{
		Reports:        len(valid),
		CumulativeLost: int(last.CumulativeLost),
		JitterMs:       round(float64(last.Jitter)*1000/ClockRate, 2),
	}
	// What the probe had sent by a round trip before the last report
	// arrived, less ReportLag, that the report does not reach.
	var rtt time.Duration
	if rep.Measured {
		rtt = rep.RoundTrip
	}
	cutoff := last.at - rtt - ReportLag
	due, _ := slices.BinarySearchFunc(cfg.Sends, cutoff, func(s Sent, t time.Duration) int {
		if s.At <= t {
			return -1
		}
		return 1
	})
	ob.Unreported = max(min(due, cfg.Sent)-last.covered, 0)
	// Loss between the first and last report when coverage grew; else the
	// last report's cumulative figure over the packets it covers, the same
	// span (its fraction lost covers only the interval since the far end's
	// previous report).
	expected, lost := last.covered, min(max(int(last.CumulativeLost), 0), last.covered)
	if delta := last.covered - first.covered; delta > 0 {
		expected, lost = delta, min(max(int(last.CumulativeLost-first.CumulativeLost), 0), delta)
	}
	ob.Expected = expected + ob.Unreported
	ob.LossPct = round(pct(lost+ob.Unreported, ob.Expected), 2)
	if rep.Measured {
		ob.RTTMs = ptr(round(ms(rep.RoundTrip), 2))
	}
	rep.Outbound = ob
	return rep
}

// coveredBlock is a report block about the probe and how many of its packets
// the block's highest sequence number covers.
type coveredBlock struct {
	ReportBlock
	covered int
	at      time.Duration // the report's arrival
}

// outboundQuality gives ob its estimate on the shared delay when it covers
// enough of the probe's packets (MinQualityExpected over MinQualityWindow).
func outboundQuality(ob *OutboundStats, ptimeMs, delay float64, source string) *OutboundStats {
	out := *ob
	covered := time.Duration(float64(out.Expected) * ptimeMs * float64(time.Millisecond))
	if out.Expected >= MinQualityExpected && covered >= MinQualityWindow {
		out.Quality = &OutboundQuality{
			Quality:      Estimate(out.LossPct, 1, delay, source),
			BurstR:       1,
			BurstRSource: BurstAssumed,
			Discards:     DiscardsNotReported,
		}
	}
	return &out
}

func byRTCPArrival(a, b RTCPArrival) int { return cmp.Compare(a.At, b.At) }
