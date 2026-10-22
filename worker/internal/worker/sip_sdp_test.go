package worker

import (
	"encoding/json"
	"net"
	"net/netip"
	"slices"
	"strings"
	"testing"
	"time"
)

// The parser reads the whole first audio stream: every payload type, the
// dynamic rtpmap entries, ptime, a=rtcp, the transport protocol and the
// connection address with its type; the envelope keeps its four keys.
func TestParseSDPAnswer_FullAudioStream(t *testing.T) {
	got := parseSDPAnswer([]byte("v=0\r\no=- 1 1 IN IP4 198.51.100.7\r\ns=-\r\nc=IN IP4 198.51.100.7\r\nt=0 0\r\na=ptime:30\r\n" +
		"m=audio 40012/2 RTP/AVP 96 8 0 97 13\r\na=rtpmap:96 opus/48000/2\r\na=rtpmap:97 telephone-event/8000\r\n" +
		"a=fmtp:97 0-15\r\na=ptime:20\r\na=rtcp:40021 IN IP4 198.51.100.9\r\na=sendrecv\r\n"))
	if got == nil {
		t.Fatal("no answer")
	}
	if got.proto != "RTP/AVP" || got.port != 40012 || !slices.Equal(got.payloads, []int{96, 8, 0, 97, 13}) {
		t.Fatalf("m= line = %q %d %v", got.proto, got.port, got.payloads)
	}
	if got.codecFor(96) != "opus/48000/2" || got.codecFor(8) != "PCMA/8000" || got.codecFor(0) != "PCMU/8000" || got.codecFor(13) != "" {
		t.Fatalf("codecs = %q %q %q %q", got.codecFor(96), got.codecFor(8), got.codecFor(0), got.codecFor(13))
	}
	if got.telephoneEventPT != 97 {
		t.Fatalf("telephone-event PT = %d, want 97", got.telephoneEventPT)
	}
	if got.ptime != 20 {
		t.Fatalf("ptime = %d, want the media-level 20", got.ptime)
	}
	if got.rtcpPort != 40021 || got.rtcpAddr != "198.51.100.9" {
		t.Fatalf("a=rtcp = %d %q", got.rtcpPort, got.rtcpAddr)
	}
	if got.connAddrType != "IP4" || got.connAddr != "198.51.100.7" || !got.connIP.Equal(net.ParseIP("198.51.100.7")) || got.connIsHostname {
		t.Fatalf("connection = %q %q %v hostname=%v", got.connAddrType, got.connAddr, got.connIP, got.connIsHostname)
	}

	raw, err := json.Marshal(got)
	if err != nil {
		t.Fatal(err)
	}
	if want := `{"codec":"opus/48000/2","payload_type":96,"remote_media":"198.51.100.7:40012","direction":"sendrecv"}`; string(raw) != want {
		t.Fatalf("sdp_answer JSON = %s, want %s", raw, want)
	}
}

func TestParseSDPAnswer_Variants(t *testing.T) {
	t.Run("session-level ptime, rtcp without an address, no telephone-event", func(t *testing.T) {
		got := parseSDPAnswer([]byte("v=0\r\nc=IN IP4 192.0.2.1\r\na=ptime:40\r\nm=audio 4000 RTP/AVP 0\r\na=rtcp:4005\r\n"))
		if got.ptime != 40 || got.rtcpPort != 4005 || got.rtcpAddr != "" || got.telephoneEventPT != -1 {
			t.Fatalf("got ptime %d rtcp %d %q te %d", got.ptime, got.rtcpPort, got.rtcpAddr, got.telephoneEventPT)
		}
	})
	t.Run("a hostname is recorded and never resolved", func(t *testing.T) {
		got := parseSDPAnswer([]byte("v=0\r\nc=IN IP4 media.example.com\r\nm=audio 4000 RTP/AVP 0\r\n"))
		if got.connIP != nil || !got.connIsHostname || got.connAddr != "media.example.com" || got.RemoteMedia != "media.example.com:4000" {
			t.Fatalf("hostname connection = %+v", got)
		}
	})
	t.Run("IPv6 and the address type as written", func(t *testing.T) {
		got := parseSDPAnswer([]byte("v=0\r\nc=IN ip6 2001:db8::7\r\nm=audio 4000 RTP/AVP 8\r\n"))
		if got.connAddrType != "IP6" || got.connIP.To4() != nil || !got.connIP.Equal(net.ParseIP("2001:db8::7")) || got.RemoteMedia != "[2001:db8::7]:4000" {
			t.Fatalf("IPv6 connection = %q %v %q", got.connAddrType, got.connIP, got.RemoteMedia)
		}
		// The address type is kept as the SDP states it, so a family
		// mismatch is visible to whoever plans media from it.
		got = parseSDPAnswer([]byte("v=0\r\nc=IN IP6 192.0.2.1\r\nm=audio 4000 RTP/AVP 8\r\n"))
		if got.connAddrType != "IP6" || got.connIP.To4() == nil {
			t.Fatalf("mismatched connection = %q %v", got.connAddrType, got.connIP)
		}
	})
	t.Run("media level over session level", func(t *testing.T) {
		got := parseSDPAnswer([]byte("v=0\r\nc=IN IP4 192.0.2.1\r\na=recvonly\r\nm=audio 4000 RTP/AVP 0\r\nc=IN IP4 192.0.2.2\r\na=sendrecv\r\n"))
		if got.connAddr != "192.0.2.2" || got.Direction != "sendrecv" {
			t.Fatalf("got %q %q", got.connAddr, got.Direction)
		}
	})
	t.Run("another stream's lines never count for audio", func(t *testing.T) {
		got := parseSDPAnswer([]byte("v=0\r\nc=IN IP4 192.0.2.1\r\nm=video 5000 RTP/AVP 96\r\nc=IN IP4 192.0.2.66\r\na=sendonly\r\na=ptime:60\r\n" +
			"m=audio 4000 RTP/AVP 0\r\nm=audio 6000 RTP/AVP 8\r\nc=IN IP4 192.0.2.77\r\n"))
		if got.connAddr != "192.0.2.1" || got.Direction != "sendrecv" || got.ptime != 0 || got.port != 4000 {
			t.Fatalf("got %q %q ptime %d port %d", got.connAddr, got.Direction, got.ptime, got.port)
		}
	})
	t.Run("the transport protocol is recorded", func(t *testing.T) {
		for _, proto := range []string{"RTP/SAVP", "RTP/AVPF", "UDP/TLS/RTP/SAVPF"} {
			if got := parseSDPAnswer([]byte("v=0\r\nc=IN IP4 192.0.2.1\r\nm=audio 4000 " + proto + " 0\r\n")); got.proto != proto {
				t.Fatalf("proto = %q, want %q", got.proto, proto)
			}
		}
	})
	t.Run("a port that is not one is a rejected stream", func(t *testing.T) {
		for _, port := range []string{"abc", "-4", "70000", "0"} {
			got := parseSDPAnswer([]byte("v=0\r\nc=IN IP4 192.0.2.1\r\nm=audio " + port + " RTP/AVP 0\r\na=rtcp:99999\r\n"))
			if got.port != 0 || got.Direction != "inactive" || got.RemoteMedia != "" || got.rtcpPort != 0 {
				t.Fatalf("port %q: %d %q %q rtcp %d", port, got.port, got.Direction, got.RemoteMedia, got.rtcpPort)
			}
		}
	})
	t.Run("payload types are bounded and in range", func(t *testing.T) {
		got := parseSDPAnswer([]byte("v=0\r\nc=IN IP4 192.0.2.1\r\nm=audio 4000 RTP/AVP 128 x 0 " + strings.Repeat("8 ", 100) + "\r\n"))
		if len(got.payloads) != sipSDPMaxPayloads || got.payloads[0] != 0 {
			t.Fatalf("payloads = %v", got.payloads)
		}
		if got.PayloadType == nil || *got.PayloadType != 128 {
			t.Fatalf("payload_type = %v, want the first field as before", got.PayloadType)
		}
	})
}

// FuzzParseSDPAnswer: the parser now feeds where a call's media may be sent,
// so it never panics, keeps every field in range, and yields a connection IP
// only for an address that is an IP literal.
func FuzzParseSDPAnswer(f *testing.F) {
	f.Add([]byte(uasSDPAnswer))
	f.Add([]byte("v=0\r\nc=IN IP4 media.example.com/127\r\nm=audio 4000/2 RTP/SAVP 0 8 101\r\na=rtpmap:101 telephone-event/8000\r\na=rtcp:4001 IN IP4 192.0.2.9\r\na=ptime:20\r\n"))
	f.Add([]byte("v=0\nc=IN IP6 ::ffff:10.0.0.1\nm=video 1 RTP/AVP 96\na=sendonly\nm=audio 0 RTP/AVP\n"))
	f.Add([]byte("m=audio 1 RTP/AVP 0\r\nc=IN IP4 fe80::1%eth0\r\na=rtcp:\r\na=ptime:x\r\na=rtpmap:0\r\n"))
	f.Fuzz(func(t *testing.T, body []byte) {
		a := parseSDPAnswer(body)
		if a == nil {
			return
		}
		if a.connIP != nil && !a.connIP.Equal(net.ParseIP(a.connAddr)) {
			t.Fatalf("connIP %v from address %q", a.connIP, a.connAddr)
		}
		if a.connIsHostname != (a.connAddr != "" && a.connIP == nil) {
			t.Fatalf("connIsHostname = %v for address %q (ip %v)", a.connIsHostname, a.connAddr, a.connIP)
		}
		if a.port < 0 || a.port > 65535 || a.rtcpPort < 0 || a.rtcpPort > 65535 || a.ptime < 0 || a.ptime > 1000 {
			t.Fatalf("out of range: port %d rtcp %d ptime %d", a.port, a.rtcpPort, a.ptime)
		}
		if len(a.payloads) > sipSDPMaxPayloads {
			t.Fatalf("%d payload types kept", len(a.payloads))
		}
		for _, pt := range a.payloads {
			if pt < 0 || pt > 127 {
				t.Fatalf("payload type %d", pt)
			}
		}
		if a.telephoneEventPT != -1 && !slices.Contains(a.payloads, a.telephoneEventPT) {
			t.Fatalf("telephone-event PT %d not offered in %v", a.telephoneEventPT, a.payloads)
		}
		if a.port == 0 && (a.Direction != "inactive" || a.RemoteMedia != "") {
			t.Fatalf("rejected stream = %q %q", a.Direction, a.RemoteMedia)
		}
		if _, err := json.Marshal(a); err != nil {
			t.Fatalf("marshal: %v", err)
		}
	})
}

func bindTestRTPPair(t *testing.T, ports *sipRTPPorts) *sipRTPPair {
	t.Helper()
	pair, err := ports.bind(net.IPv4(127, 0, 0, 1))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pair.Close)
	return pair
}

func sendToTestRTP(t *testing.T, pair *sipRTPPair, payload string) netip.AddrPort {
	t.Helper()
	conn, err := net.DialUDP("udp", nil, pair.rtp.LocalAddr().(*net.UDPAddr))
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	if _, err := conn.Write([]byte(payload)); err != nil {
		t.Fatal(err)
	}
	return conn.LocalAddr().(*net.UDPAddr).AddrPort()
}

func waitTestRTPPackets(t *testing.T, pair *sipRTPPair, n int64) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for pair.packets.Load() < n {
		if time.Now().After(deadline) {
			t.Fatalf("read %d datagrams, want %d", pair.packets.Load(), n)
		}
		time.Sleep(time.Millisecond)
	}
}

// Datagrams are counted from bind; only those read after arm reach the sink,
// with their source.
func TestSIPRTPPair_ArmedSinkSeesDatagrams(t *testing.T) {
	pair := bindTestRTPPair(t, nil)
	sendToTestRTP(t, pair, "before")
	waitTestRTPPackets(t, pair, 1)

	got := make(chan string, 1)
	var from netip.AddrPort
	pair.arm(func(b []byte, src netip.AddrPort, _ time.Time) {
		from = src
		got <- string(b)
	}, nil)
	src := sendToTestRTP(t, pair, "after")
	select {
	case payload := <-got:
		if payload != "after" {
			t.Fatalf("sink got %q, want only the datagram sent after arm", payload)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("the armed sink saw nothing")
	}
	pair.Close()
	if from != src {
		t.Fatalf("source = %v, want %v", from, src)
	}
	if n := pair.packets.Load(); n != 2 {
		t.Fatalf("rtp_rx_packets = %d, want 2", n)
	}
}

// Close returns only after both readers exited: a sink still running holds
// it, and what the sink wrote is safe to read once Close returns (-race).
func TestSIPRTPPair_CloseJoinsReaders(t *testing.T) {
	pair := bindTestRTPPair(t, nil)
	entered, release := make(chan struct{}), make(chan struct{})
	var seen []string
	pair.arm(func(b []byte, _ netip.AddrPort, _ time.Time) {
		close(entered)
		<-release
		seen = append(seen, string(b))
	}, nil)
	sendToTestRTP(t, pair, "slow")
	<-entered

	closed := make(chan struct{})
	go func() {
		pair.Close()
		pair.Close() // idempotent
		close(closed)
	}()
	select {
	case <-closed:
		t.Fatal("Close returned while a reader was still running")
	case <-time.After(50 * time.Millisecond):
	}
	close(release)
	select {
	case <-closed:
	case <-time.After(2 * time.Second):
		t.Fatal("Close did not return after the reader exited")
	}
	if !slices.Equal(seen, []string{"slow"}) {
		t.Fatalf("sink wrote %q", seen)
	}
}

// A pair a call released stays out of rotation for the linger window, since
// a far end whose BYE went unconfirmed may still be streaming to it.
func TestSIPRTPPorts_QuarantinesReleasedPairs(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	free := freeTestRTPPorts(t, 2)
	var picks []int
	ports := &sipRTPPorts{
		now: func() time.Time { return now },
		pick: func() int {
			p := picks[0]
			picks = picks[1:]
			return p
		},
	}

	picks = []int{free[0]}
	first := bindTestRTPPair(t, ports)
	first.Close()

	// The released pair is skipped; the next candidate is bound.
	picks = []int{free[0], free[1]}
	second := bindTestRTPPair(t, ports)
	if second.port != free[1] {
		t.Fatalf("bound %d, want %d (the released %d is quarantined)", second.port, free[1], free[0])
	}
	second.Close()

	now = now.Add(sipRTPQuarantineFor - time.Second)
	if !ports.quarantined(free[0]) {
		t.Fatal("a pair released 31 s ago left quarantine")
	}
	now = now.Add(2 * time.Second)
	if ports.quarantined(free[0]) {
		t.Fatal("a pair released 33 s ago is still quarantined")
	}
	picks = []int{free[0]}
	third := bindTestRTPPair(t, ports)
	if third.port != free[0] {
		t.Fatalf("bound %d, want %d once its quarantine ended", third.port, free[0])
	}
	third.Close()

	// Every candidate quarantined: the bind fails, naming why.
	picks = slices.Repeat([]int{free[0]}, sipRTPBindTries)
	if _, err := ports.bind(net.IPv4(127, 0, 0, 1)); err == nil || !strings.Contains(err.Error(), errSIPRTPPairQuarantined.Error()) {
		t.Fatalf("bind with every candidate quarantined = %v", err)
	}
}

// However many pairs fast-failing calls release meanwhile, a released pair
// keeps its whole quarantine: a burst of 403s cannot evict the one pair
// whose far end may still be streaming.
func TestSIPRTPPorts_QuarantineSurvivesReleaseBursts(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	ports := &sipRTPPorts{now: func() time.Time { return now }}
	ports.release(20000)
	for i := range 2000 {
		ports.release(20002 + 2*(i%1000))
	}
	if !ports.quarantined(20000) {
		t.Fatal("2000 later releases evicted a pair released just now")
	}
	now = now.Add(sipRTPQuarantineFor)
	ports.release(16384)
	if ports.quarantined(20000) {
		t.Fatal("a pair released a full window ago is still quarantined")
	}
	ports.mu.Lock()
	n := len(ports.released)
	ports.mu.Unlock()
	if n != 1 {
		t.Fatalf("%d entries after the window, want 1: expired releases are pruned", n)
	}
}

// freeTestRTPPorts finds n even ports p in the RTP range with p and p+1 free.
func freeTestRTPPorts(t *testing.T, n int) []int {
	t.Helper()
	var out []int
	for port := sipRTPPortMax - 2; port >= sipRTPPortMin && len(out) < n; port -= 2 {
		pair, err := (&sipRTPPorts{pick: func() int { return port }}).bind(net.IPv4(127, 0, 0, 1))
		if err != nil {
			continue
		}
		pair.Close()
		out = append(out, port)
	}
	if len(out) < n {
		t.Fatalf("found %d free RTP port pairs, want %d", len(out), n)
	}
	return out
}

func testSIPResponse(t *testing.T, code int, toTag, body string) *sipMessage {
	t.Helper()
	invite := "INVITE sip:100@example.com SIP/2.0\r\nVia: SIP/2.0/UDP 127.0.0.1;branch=z9hG4bK1\r\nFrom: <sip:p@example.com>;tag=f\r\n" +
		"To: <sip:100@example.com>\r\nCall-ID: c\r\nCSeq: 1 INVITE\r\nContent-Length: 0\r\n\r\n"
	msg, err := parseSIPMessage([]byte(uasReply(invite, code, "Reason", toTag, body)))
	if err != nil {
		t.Fatal(err)
	}
	return msg
}

// A bare 200 takes its dialog's 18x SDP as the answer, never another
// dialog's; a 200 with SDP always answers for itself.
func TestSIPCall_AnswerSDPFallsBackToSameDialog18x(t *testing.T) {
	early := "v=0\r\nc=IN IP4 192.0.2.18\r\nm=audio 4018 RTP/AVP 0\r\n"
	other := "v=0\r\nc=IN IP4 192.0.2.66\r\nm=audio 4066 RTP/AVP 8\r\n"
	s := &sipCall{}
	s.keepEarlyAnswer(testSIPResponse(t, 183, "a", "v=0\r\nc=IN IP4 192.0.2.17\r\nm=audio 4017 RTP/AVP 0\r\n"))
	s.keepEarlyAnswer(testSIPResponse(t, 183, "a", early)) // the last 18x of a dialog wins
	s.keepEarlyAnswer(testSIPResponse(t, 183, "b", other))
	s.keepEarlyAnswer(testSIPResponse(t, 183, "", other)) // no tag, no dialog

	answer, source, ok := s.answerSDP(&sipDialog{remoteTag: "a"}, testSIPResponse(t, 200, "a", ""))
	if !ok || source != sipAnswerFrom18x || answer == nil || answer.RemoteMedia != "192.0.2.18:4018" {
		t.Fatalf("bare 200 on dialog a = %+v %q %v, want its last 18x", answer, source, ok)
	}
	if _, _, ok := s.answerSDP(&sipDialog{remoteTag: "c"}, testSIPResponse(t, 200, "c", "")); ok {
		t.Fatal("a bare 200 on a dialog without an 18x SDP used another dialog's")
	}
	answer, source, ok = s.answerSDP(&sipDialog{remoteTag: "b"}, testSIPResponse(t, 200, "b", uasSDPAnswer))
	if !ok || source != sipAnswerFrom2xx || answer.RemoteMedia != "198.51.100.7:40012" {
		t.Fatalf("200 with SDP = %+v %q %v, want its own", answer, source, ok)
	}

	for _, tag := range []string{"c", "d", "e"} {
		s.keepEarlyAnswer(testSIPResponse(t, 183, tag, early))
	}
	if len(s.earlyAnswers) != sipCallMaxEarlyAnswers {
		t.Fatalf("kept %d early dialogs, want %d", len(s.earlyAnswers), sipCallMaxEarlyAnswers)
	}
	if _, kept := s.earlyAnswers["e"]; kept {
		t.Fatal("an early dialog past the bound was kept")
	}
}

// In a call: the 183's SDP is kept for the bare 200 of its dialog, while
// sdp_answer stays the 2xx's (absent here) and the call is unchanged.
func TestSIPInvite_BareAnswerAfterEarlyMedia(t *testing.T) {
	var invites branchSet
	u := startFakeUAS(t, "udp", func(u *fakeUAS, msg string) {
		if fMethod(msg) == "INVITE" && invites.first(msg) {
			u.send(uasReply(msg, 183, "Session Progress", "srv1", uasSDPAnswer))
			u.sendAfter(50*time.Millisecond, uasReply(msg, 200, "OK", "srv1", "", u.contact()))
		}
		if fMethod(msg) == "BYE" {
			u.send(uasReply(msg, 200, "OK", "", ""))
		}
	})
	result, m := runInvite(t, newInviteTestChecker(), inviteConfig(u, map[string]any{"number": "100"}, nil), 5)
	requireStatus(t, result, "success")
	if !m.Call.EarlyMedia || m.Call.SDPAnswer != nil || m.Call.Teardown != "bye_ok" {
		t.Fatalf("early_media %v, sdp_answer %+v, teardown %q", m.Call.EarlyMedia, m.Call.SDPAnswer, m.Call.Teardown)
	}
}
