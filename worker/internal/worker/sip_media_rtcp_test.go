package worker

import (
	"context"
	"net"
	"net/netip"
	"os"
	"slices"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/yassinebenameur/probara/shared/models"
	"github.com/yassinebenameur/probara/worker/internal/rtp"
)

// RTCP goes to the media destination's IP only: at the a=rtcp port or the
// RTP port + 1, vetted like the RTP address, and not at all when a=rtcp
// names another address.
func TestPrepareMedia_RTCP(t *testing.T) {
	const peer = "93.184.216.34"
	env := sipMediaEnv{
		expect: models.SIPMediaExpectAudio,
		peer:   netip.MustParseAddr(peer),
		local:  netip.MustParseAddrPort("10.1.2.3:20000"),
	}
	sdp := func(port string, attrs ...string) string {
		return "v=0\no=- 1 1 IN IP4 " + peer + "\ns=-\nc=IN IP4 " + peer + "\nt=0 0\nm=audio " + port + " RTP/AVP 0\n" + strings.Join(attrs, "\n") + "\n"
	}
	for _, tc := range []struct {
		name, sdp, rtcp string // rtcp "": none
		off             bool
	}{
		{name: "the port above", sdp: sdp("40012"), rtcp: peer + ":40013"},
		{name: "a=rtcp port", sdp: sdp("40012", "a=rtcp:40020"), rtcp: peer + ":40020"},
		{name: "a=rtcp naming the same address", sdp: sdp("40012", "a=rtcp:40020 IN IP4 "+peer), rtcp: peer + ":40020"},
		{name: "a=rtcp naming another address", sdp: sdp("40012", "a=rtcp:40020 IN IP4 198.51.100.9"), off: true},
		{name: "a=rtcp naming a hostname", sdp: sdp("40012", "a=rtcp:40020 IN IP4 rtcp.example.com"), off: true},
		{name: "multiplexed", sdp: sdp("40012", "a=rtcp:40012"), rtcp: peer + ":40012"},
		{name: "a well-known port", sdp: sdp("40012", "a=rtcp:161")},
		{name: "no port above 65535", sdp: sdp("65535")},
	} {
		t.Run(tc.name, func(t *testing.T) {
			plan := prepareMedia(testAnswer(t, tc.sdp), sipAnswerFrom2xx, env)
			if plan.status != "" || !plan.dst.IsValid() {
				t.Fatalf("plan = %q/%q, want the RTP destination usable whatever RTCP gets", plan.status, plan.detail)
			}
			got := ""
			if plan.rtcpDst.IsValid() {
				got = plan.rtcpDst.String()
			}
			if got != tc.rtcp || plan.rtcpOff != tc.off {
				t.Fatalf("rtcp = %q off = %v, want %q %v", got, plan.rtcpOff, tc.rtcp, tc.off)
			}
		})
	}
	// two_way needs the probe's audio to arrive; a sendonly far end refuses it.
	twoWay := env
	twoWay.expect = models.SIPMediaExpectTwoWay
	if plan := prepareMedia(testAnswer(t, sdp("40012", "a=sendonly")), sipAnswerFrom2xx, twoWay); plan.status != sipMediaUnusable || plan.detail != sipMediaSendOnly {
		t.Fatalf("two_way to sendonly: plan = %q/%q", plan.status, plan.detail)
	}
}

// The far end's RTCP is kept only from accepted sources, on either port, with
// only the blocks about the probe; RTCP follows a latch, unless the answer
// turned it off.
func TestSIPMediaSession_RTCPInbound(t *testing.T) {
	at := time.Now()
	sdp := netip.MustParseAddrPort("127.0.0.1:40000")
	plan := &sipMediaPlan{send: true, dst: sdp, sdpIP: sdp.Addr(), law: rtp.PCMU, rtcpDst: netip.MustParseAddrPort("127.0.0.1:40001")}

	t.Run("accepted sources, blocks about the probe", func(t *testing.T) {
		m := newTestMediaSession(models.SIPMediaExpectTwoWay, plan, "127.0.0.1")
		ours := rtp.ReportBlock{SSRC: m.ssrc, HighestSeq: 5}
		m.onRTCP(testRR(1, ours, rtp.ReportBlock{SSRC: 7}), netip.MustParseAddrPort("127.0.0.1:40001"), at)
		m.onRTP(testRR(1, ours), sdp, at) // multiplexed on the RTP port
		m.onRTCP(testRR(1, ours), netip.MustParseAddrPort("[2001:db8::9]:40001"), at)
		m.onRTCP([]byte{0x81, rtp.RTCPSourceDescription, 0, 0}, sdp, at) // not a valid compound
		m.onRTCP(make([]byte, 1501), sdp, at)
		if len(m.rtcpIn.arrivals) != 1 || len(m.rtcpMux.arrivals) != 1 || m.rtcpIn.ignored != 3 {
			t.Fatalf("in = %+v mux = %+v", m.rtcpIn, m.rtcpMux)
		}
		if blocks := m.rtcpIn.arrivals[0].Blocks; len(blocks) != 1 || blocks[0] != ours {
			t.Fatalf("blocks = %+v, want only the one about the probe", blocks)
		}
		if len(m.records) != 0 || m.invalid != 0 {
			t.Fatalf("records = %d invalid = %d: RTCP on the RTP port is not audio", len(m.records), m.invalid)
		}
	})
	t.Run("cap", func(t *testing.T) {
		m := newTestMediaSession(models.SIPMediaExpectTwoWay, plan, "127.0.0.1")
		for range 40 {
			m.onRTCP(testRR(1), sdp, at)
		}
		if len(m.rtcpIn.arrivals) != sipMediaMaxRTCPIn || m.rtcpIn.ignored != 40-sipMediaMaxRTCPIn {
			t.Fatalf("kept %d ignored %d", len(m.rtcpIn.arrivals), m.rtcpIn.ignored)
		}
	})
	t.Run("RTCP follows a latch", func(t *testing.T) {
		m := newTestMediaSession(models.SIPMediaExpectAudio, plan, "127.0.0.1")
		m.rtcpDst.Store(&plan.rtcpDst)
		m.onRTP(testRTP(1, 7), netip.MustParseAddrPort("127.0.0.1:40100"), at)
		if got := m.rtcpDst.Load(); got == nil || got.Port() != 40101 {
			t.Fatalf("rtcp dst = %v, want the latched port + 1", got)
		}
	})
	t.Run("off stays off", func(t *testing.T) {
		off := *plan
		off.rtcpDst, off.rtcpOff = netip.AddrPort{}, true
		m := newTestMediaSession(models.SIPMediaExpectAudio, &off, "127.0.0.1")
		m.onRTP(testRTP(1, 7), netip.MustParseAddrPort("127.0.0.1:40100"), at)
		if m.latched != sipMediaLatchPort || m.rtcpDst.Load() != nil {
			t.Fatalf("latched %q rtcp dst %v, want RTP latched and no RTCP", m.latched, m.rtcpDst.Load())
		}
	})
}

// At most sipMediaMaxRTCPOut packets a call, reports one short of it so the
// BYE fits, and a BYE only from a probe that sent something.
func TestSIPMediaSession_RTCPOutbound(t *testing.T) {
	pair, err := (*sipRTPPorts)(nil).bind(net.IPv4(127, 0, 0, 1))
	if err != nil {
		t.Fatal(err)
	}
	defer pair.Close()
	sdp := netip.MustParseAddrPort("127.0.0.1:40000")
	m := newTestMediaSession(models.SIPMediaExpectAudio, &sipMediaPlan{send: true, dst: sdp, sdpIP: sdp.Addr(), law: rtp.PCMU}, "127.0.0.1")
	m.pair, m.cname = pair, "test@probara"
	var sent [][]byte
	m.sendTo = func(b []byte, _ netip.AddrPort) error { sent = append(sent, slices.Clone(b)); return nil }
	dst := netip.MustParseAddrPort("127.0.0.1:40001")
	m.rtcpDst.Store(&dst)
	t0 := time.Now()

	m.sendRTCP(t0, 160, true)
	if len(sent) != 0 {
		t.Fatal("a BYE from a probe that sent nothing")
	}
	m.sent = []rtp.Sent{{Seq: 1}, {Seq: 2}}
	for range 4 {
		m.sendRTCP(t0, 160, false)
	}
	m.sendRTCP(t0, 160, true)
	m.sendRTCP(t0, 160, true)
	if len(sent) != sipMediaMaxRTCPOut || m.rtcpSent != sipMediaMaxRTCPOut || len(m.srs) != sipMediaMaxRTCPOut || m.rtcpTo != dst {
		t.Fatalf("sent %d (rtcpSent %d, srs %d, to %s), want two reports and a BYE", len(sent), m.rtcpSent, len(m.srs), m.rtcpTo)
	}
	for i, b := range sent {
		if _, err := rtp.ParseRTCP(b); err != nil {
			t.Fatalf("packet %d: %v", i, err)
		}
		if b[1] != rtp.RTCPSenderReport || string(b[28+10:28+10+len(m.cname)]) != m.cname {
			t.Fatalf("packet %d = % x, want an SR with the CNAME", i, b)
		}
		if packets, octets := be32(b[20:]), be32(b[24:]); packets != 2 || octets != 320 {
			t.Fatalf("packet %d: %d packets, %d octets", i, packets, octets)
		}
		if bye := b[len(b)-8:]; (bye[1] == rtp.RTCPGoodbye) != (i == len(sent)-1) {
			t.Fatalf("packet %d: BYE %v", i, bye[1] == rtp.RTCPGoodbye)
		}
	}
}

func be32(b []byte) uint32 {
	return uint32(b[0])<<24 | uint32(b[1])<<16 | uint32(b[2])<<8 | uint32(b[3])
}

// probeRTCPAddr is the probe's RTCP port: the one above its offered RTP port.
func probeRTCPAddr(t *testing.T, invite string) netip.AddrPort {
	rtpAddr := probeMediaAddr(t, invite)
	return netip.AddrPortFrom(rtpAddr.Addr(), rtpAddr.Port()+1)
}

func TestSIPMedia_TwoWay(t *testing.T) {
	// reporting runs a two_way call whose far end streams its own audio and
	// answers the probe's sender reports after a 40 ms round trip.
	reporting := func(t *testing.T, media map[string]any, muxed bool) (CheckResult, *sipMetrics, *sipMediaMetrics, netip.AddrPort) {
		peer := startMediaPeer(t, "127.0.0.1")
		rtcpAt := peer.addr
		if !muxed {
			rtcpAt = peer.listenRTCP()
		}
		peer.answerSRs(40*time.Millisecond, 0, muxed)
		sdp := mediaSDP(peer.addr, "m=audio %d RTP/AVP 0", "a=rtcp:"+strconv.Itoa(int(rtcpAt.Port())))
		u := startFakeUAS(t, "udp", mediaUAS{sdp: sdp, answered: func(u *fakeUAS, invite string) {
			peer.stream(probeMediaAddr(t, invite), mediaStream{count: 300})
		}}.handler())
		result, m, mm := runMediaCall(t, context.Background(), newInviteTestChecker(), mediaCallConfig(u, 6000, media, nil))
		return result, m, mm, rtcpAt
	}
	streaming := func(t *testing.T, extra func(peer *fakeMediaPeer, invite string)) (CheckResult, *sipMetrics, *sipMediaMetrics) {
		peer := startMediaPeer(t, "127.0.0.1")
		u := startFakeUAS(t, "udp", mediaUAS{sdp: pcmuAnswer(peer), answered: func(u *fakeUAS, invite string) {
			peer.stream(probeMediaAddr(t, invite), mediaStream{count: 300})
			if extra != nil {
				extra(peer, invite)
			}
		}}.handler())
		return runMediaCall(t, context.Background(), newInviteTestChecker(), mediaCallConfig(u, 6000, map[string]any{"expect": "two_way"}, nil))
	}
	mediaTests(t, map[string]func(t *testing.T){
		// The far end's receiver report about the probe's SSRC proves the
		// probe's audio arrived, and its LSR/DLSR gives the round trip.
		"confirmed by RTCP": func(t *testing.T) {
			result, m, media, rtcpAt := reporting(t, map[string]any{"expect": "two_way"}, false)
			requireStatus(t, result, "success")
			requireMediaStatus(t, media, sipMediaMeasured, "")
			if media.TwoWay != "verified" || strings.Join(media.TwoWayEvidence, ",") != "rx,rtcp" {
				t.Fatalf("two_way = %q %q, want verified by rx and rtcp", media.TwoWay, media.TwoWayEvidence)
			}
			ob := media.Outbound
			if ob == nil || ob.Reports < 1 || ob.LossPct != 0 || ob.Expected < 100 || ob.JitterMs != 2 || ob.RTTMs == nil || *ob.RTTMs < 40 || *ob.RTTMs > 500 {
				t.Fatalf("outbound = %+v", ob)
			}
			if q := ob.Quality; q == nil || q.BurstRSource != rtp.BurstAssumed || q.Discards != rtp.DiscardsNotReported || q.DelaySource != rtp.DelayRTCP {
				t.Fatalf("outbound quality = %+v", ob.Quality)
			}
			if q := media.Inbound.Quality; q == nil || q.DelaySource != rtp.DelayRTCP {
				t.Fatalf("inbound quality = %+v, want the RTCP round trip as its delay", q)
			}
			// A report at 2.5 s into a 6 s hold, and the BYE.
			if media.TX.RTCP != 2 || media.RTCPDestination != rtcpAt.String() || media.RX.RTCP < 1 {
				t.Fatalf("tx = %+v rx = %+v rtcp_destination = %q", media.TX, media.RX, media.RTCPDestination)
			}
			requireNoAddress(t, result, m)
		},
		"multiplexed RTCP": func(t *testing.T) {
			result, _, media, rtcpAt := reporting(t, map[string]any{"expect": "two_way"}, true)
			requireStatus(t, result, "success")
			if media.RTCPDestination != rtcpAt.String() || !slices.Contains(media.TwoWayEvidence, "rtcp") {
				t.Fatalf("rtcp_destination = %q evidence = %q", media.RTCPDestination, media.TwoWayEvidence)
			}
		},
		"rtt threshold from RTCP": func(t *testing.T) {
			result, m, _ := reliably(t, func() (CheckResult, *sipMetrics, *sipMediaMetrics) {
				result, m, media, _ := reporting(t, map[string]any{"expect": "two_way", "max_rtt_ms": 30}, false)
				return result, m, media
			})
			requireStatus(t, result, "failure", "rtt: ", "ms > 30ms (RTCP)")
			if len(m.AssertionsFailed) != 1 {
				t.Fatalf("assertions = %q", m.AssertionsFailed)
			}
		},
		// The far end reports, but never about the probe: it hears none of it.
		"explicit negative": func(t *testing.T) {
			result, _, media := streaming(t, func(peer *fakeMediaPeer, invite string) {
				peer.reportWithoutUs(probeRTCPAddr(t, invite), 1600*time.Millisecond, 3200*time.Millisecond)
			})
			requireStatus(t, result, "failure", "media: far end reports receiving none of the probe's audio (RTCP)")
			if media.TwoWay != "inbound_only" || media.Outbound != nil || media.RX.RTCP != 2 {
				t.Fatalf("two_way = %q outbound = %+v rx = %+v", media.TwoWay, media.Outbound, media.RX)
			}
		},
		"never confirmed": func(t *testing.T) {
			result, m, _ := streaming(t, nil)
			requireStatus(t, result, "failure", "media: far end never confirmed the probe's audio (no echo, no RTCP report)")
			if len(m.AssertionsFailed) != 1 {
				t.Fatalf("assertions = %q", m.AssertionsFailed)
			}
		},
		"echo line": func(t *testing.T) {
			peer := startMediaPeer(t, "127.0.0.1")
			peer.reflect()
			u := startFakeUAS(t, "udp", mediaUAS{sdp: pcmuAnswer(peer)}.handler())
			result, _, media := runMediaCall(t, context.Background(), newInviteTestChecker(), mediaCallConfig(u, 6000, map[string]any{"expect": "two_way"}, nil))
			requireStatus(t, result, "success")
			if media.TwoWay != "verified" || strings.Join(media.TwoWayEvidence, ",") != "rx,echo" {
				t.Fatalf("two_way = %q %q", media.TwoWay, media.TwoWayEvidence)
			}
		},
		"sendonly": func(t *testing.T) {
			u := startFakeUAS(t, "udp", mediaUAS{sdp: mediaSDP(netip.MustParseAddrPort("127.0.0.1:40012"), "m=audio %d RTP/AVP 0", "a=sendonly")}.handler())
			result, _, media := runMediaCall(t, context.Background(), newInviteTestChecker(), mediaCallConfig(u, 6000, map[string]any{"expect": "two_way"}, nil))
			requireStatus(t, result, "failure", "media: far end answered sendonly (it refuses our audio)")
			requireMediaStatus(t, media, sipMediaUnusable, sipMediaSendOnly)
		},
		// The probe's audio could not leave: nothing about the target was
		// measured.
		"writes refused": func(t *testing.T) {
			peer := startMediaPeer(t, "127.0.0.1")
			u := startFakeUAS(t, "udp", mediaUAS{sdp: pcmuAnswer(peer), answered: func(u *fakeUAS, invite string) {
				peer.stream(probeMediaAddr(t, invite), mediaStream{count: 300})
			}}.handler())
			c := newInviteTestChecker()
			log := sendLog{fail: &net.OpError{Op: "write", Net: "udp", Err: os.NewSyscallError("sendto", syscall.EPERM)}}
			log.attach(c)
			result, m, media := runMediaCall(t, context.Background(), c, mediaCallConfig(u, 6000, map[string]any{"expect": "two_way"}, nil))
			requireStatus(t, result, "error", "media: probe audio could not be sent (eperm)")
			requireNoAddress(t, result, m)
			if media.TX.RTCP != 0 || media.RTCPDestination != "" {
				t.Fatalf("tx = %+v rtcp_destination = %q, want no RTCP counted", media.TX, media.RTCPDestination)
			}
		},
		// A measure-only call reports what two_way would have failed on.
		"measure only": func(t *testing.T) {
			peer := startMediaPeer(t, "127.0.0.1")
			u := startFakeUAS(t, "udp", mediaUAS{sdp: pcmuAnswer(peer), answered: func(u *fakeUAS, invite string) {
				peer.stream(probeMediaAddr(t, invite), mediaStream{count: 150})
			}}.handler())
			result, m, _ := runMediaCall(t, context.Background(), newInviteTestChecker(), mediaCallConfig(u, 3000, map[string]any{"expect": "none"}, nil))
			requireStatus(t, result, "success")
			requireWarning(t, m, "media: far end never confirmed the probe's audio (no echo, no RTCP report)")
		},
	})
}
