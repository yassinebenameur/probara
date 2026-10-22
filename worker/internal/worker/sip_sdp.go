package worker

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"math/rand/v2"
	"net"
	"net/netip"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// sipRTPPair is the RTP/RTCP port pair a test call's SDP offer names: an even
// port and the one above it (RFC 3550 §11), both bound so the offer never
// advertises a port that something else owns. A reader goroutine per socket
// counts what the far end sends (rtp_rx_packets), so an SBC that streams
// early media or ringback does not fill a socket buffer, and hands each
// datagram to the sink armed for that socket, if any. Close joins both
// readers, so whatever a sink wrote is safe to read once it returns.
type sipRTPPair struct {
	rtp, rtcp *net.UDPConn
	port      int
	packets   atomic.Int64
	bytes     atomic.Int64

	rtpSink, rtcpSink atomic.Pointer[sipMediaSink]
	rtpDone, rtcpDone chan struct{}
	closeOnce         sync.Once
	ports             *sipRTPPorts // where Close records the release; nil: nowhere
}

// sipMediaSink receives one datagram read from a pair's socket: its bytes
// (reused by the next read, so copy what you keep), the source with any
// IPv4-mapped prefix removed, and the read time. It runs on the reader
// goroutine and must not block for long: the socket is not read meanwhile.
type sipMediaSink func(b []byte, src netip.AddrPort, at time.Time)

const (
	sipRTPPortMin   = 16384
	sipRTPPortMax   = 32766 // the RTP port is even and below this, RTCP one above
	sipRTPBindTries = 8
	// sipRTPReadBuffer exceeds any datagram worth parsing (an Ethernet MTU),
	// so a sink can tell an oversized one from a full one: a read truncates
	// silently at the buffer size.
	sipRTPReadBuffer = 2048
)

// sipRTPPorts hands out a worker's RTP port pairs and keeps the ones calls
// released recently out of reuse. A pair closes before the call's linger
// (runInviteCall), while a far end whose BYE went unconfirmed may still be
// streaming to it, so binding it again at once would pour that old stream
// into the next call. A released pair stays out of rotation for
// sipRTPQuarantineFor, whatever else is released meanwhile: the release
// times are a map by port, not a fixed ring, since fast-failing calls (a 403
// to every INVITE) release pairs far faster than one per linger window and
// would push the one pair that needed it out early. Expired entries are
// pruned on release, so the map never holds more than the pairs released in
// one window (at most the range's 8191). A nil *sipRTPPorts binds random
// pairs with no quarantine.
type sipRTPPorts struct {
	mu       sync.Mutex
	released map[int]time.Time // even port → release time

	// now and pick replace the clock and the random port choice in tests.
	now  func() time.Time
	pick func() int
}

const sipRTPQuarantineFor = sipCallLingerFor

func newSIPRTPPorts() *sipRTPPorts {
	return &sipRTPPorts{}
}

func (ports *sipRTPPorts) clock() time.Time {
	if ports != nil && ports.now != nil {
		return ports.now()
	}
	return time.Now()
}

func (ports *sipRTPPorts) candidate() int {
	if ports != nil && ports.pick != nil {
		return ports.pick()
	}
	return sipRTPPortMin + 2*rand.IntN((sipRTPPortMax-sipRTPPortMin)/2)
}

// quarantined reports whether the pair at port was released within
// sipRTPQuarantineFor.
func (ports *sipRTPPorts) quarantined(port int) bool {
	if ports == nil {
		return false
	}
	now := ports.clock()
	ports.mu.Lock()
	defer ports.mu.Unlock()
	at, ok := ports.released[port]
	return ok && now.Sub(at) < sipRTPQuarantineFor
}

func (ports *sipRTPPorts) release(port int) {
	if ports == nil {
		return
	}
	now := ports.clock()
	ports.mu.Lock()
	defer ports.mu.Unlock()
	if ports.released == nil {
		ports.released = make(map[int]time.Time)
	}
	for p, at := range ports.released {
		if now.Sub(at) >= sipRTPQuarantineFor {
			delete(ports.released, p)
		}
	}
	ports.released[port] = now
}

var errSIPRTPPairQuarantined = errors.New("port pair released recently")

// bind binds an even port p and p+1 on ip, trying up to sipRTPBindTries
// random pairs in the conventional RTP range and skipping quarantined ones.
func (ports *sipRTPPorts) bind(ip net.IP) (*sipRTPPair, error) {
	var lastErr error
	for range sipRTPBindTries {
		port := ports.candidate()
		if ports.quarantined(port) {
			lastErr = errSIPRTPPairQuarantined
			continue
		}
		rtp, err := listenSIPRTPSocket(ip, port)
		if err != nil {
			lastErr = err
			continue
		}
		rtcp, err := listenSIPRTPSocket(ip, port+1)
		if err != nil {
			rtp.Close()
			lastErr = err
			continue
		}
		pair := &sipRTPPair{
			rtp: rtp, rtcp: rtcp, port: port, ports: ports,
			rtpDone: make(chan struct{}), rtcpDone: make(chan struct{}),
		}
		go pair.readLoop(rtp, &pair.rtpSink, true, pair.rtpDone)
		go pair.readLoop(rtcp, &pair.rtcpSink, false, pair.rtcpDone)
		return pair, nil
	}
	return nil, fmt.Errorf("cannot bind an RTP port pair on %s: %w", ip, lastErr)
}

// listenSIPRTPSocket binds one media socket on ip:port with SO_BROADCAST
// cleared (sipRTPSocketControl).
func listenSIPRTPSocket(ip net.IP, port int) (*net.UDPConn, error) {
	host := "" // every address, as a nil IP in a UDPAddr means
	if len(ip) > 0 {
		host = ip.String()
	}
	lc := net.ListenConfig{Control: sipRTPSocketControl}
	conn, err := lc.ListenPacket(context.Background(), "udp", net.JoinHostPort(host, strconv.Itoa(port)))
	if err != nil {
		return nil, err
	}
	return conn.(*net.UDPConn), nil
}

// readLoop reads conn until it is closed, counting RTP-socket datagrams and
// handing each to the armed sink. It closes done on exit.
func (p *sipRTPPair) readLoop(conn *net.UDPConn, sink *atomic.Pointer[sipMediaSink], count bool, done chan struct{}) {
	defer close(done)
	buf := make([]byte, sipRTPReadBuffer)
	for {
		n, src, err := conn.ReadFromUDPAddrPort(buf)
		if err != nil {
			if errors.Is(err, net.ErrClosed) {
				return
			}
			continue
		}
		if count {
			p.packets.Add(1)
			p.bytes.Add(int64(n))
		}
		if fn := sink.Load(); fn != nil {
			(*fn)(buf[:n], netip.AddrPortFrom(src.Addr().Unmap(), src.Port()), time.Now())
		}
	}
}

// arm starts handing the RTP and RTCP sockets' datagrams to rtp and rtcp
// (either may be nil). Datagrams read before arm are only counted.
func (p *sipRTPPair) arm(rtp, rtcp sipMediaSink) {
	if rtp != nil {
		p.rtpSink.Store(&rtp)
	}
	if rtcp != nil {
		p.rtcpSink.Store(&rtcp)
	}
}

// Close releases both ports, waits for both readers to exit and puts the
// pair in quarantine. It is idempotent.
func (p *sipRTPPair) Close() {
	p.closeOnce.Do(func() {
		p.rtp.Close()
		p.rtcp.Close()
		<-p.rtpDone
		<-p.rtcpDone
		p.ports.release(p.port)
	})
}

// sipSDPPayloads is the audio offer: G.711 µ-law and A-law, which every
// carrier answers, plus RFC 4733 telephone-events.
const sipSDPPayloads = "0 8 101"

// buildSDPOffer builds the call's audio offer for addr (the flow's local IP,
// or the operator's sdp_address override, which replaces o= and c= only)
// and the bound RTP port. RTP/AVP only: an SRTP-only trunk answers 488.
func buildSDPOffer(addr string, port int, sessID uint64) []byte {
	addrType := "IP4"
	if ip := net.ParseIP(addr); ip != nil && ip.To4() == nil {
		addrType = "IP6"
	}
	lines := []string{
		"v=0",
		fmt.Sprintf("o=probara %d %d IN %s %s", sessID, sessID, addrType, addr),
		"s=probara",
		fmt.Sprintf("c=IN %s %s", addrType, addr),
		"t=0 0",
		fmt.Sprintf("m=audio %d RTP/AVP %s", port, sipSDPPayloads),
		"a=rtpmap:0 PCMU/8000",
		"a=rtpmap:8 PCMA/8000",
		"a=rtpmap:101 telephone-event/8000",
		"a=fmtp:101 0-16",
		"a=ptime:20",
		"a=sendrecv",
	}
	return []byte(strings.Join(lines, "\r\n") + "\r\n")
}

// sipSDPAnswer is what the far end's SDP answer says about its first audio
// stream. The exported fields are metrics_data.sip.call.sdp_answer (the
// first payload type's codec, the media address, the direction); the
// unexported ones are the rest of the stream, which a call-quality
// measurement plans from (prepareMedia) and which never reach the envelope.
// RemoteMedia is the record only: media is sent to the plan's vetted
// destination, never to this string.
type sipSDPAnswer struct {
	Codec       string `json:"codec,omitempty"`
	PayloadType *int   `json:"payload_type,omitempty"`
	RemoteMedia string `json:"remote_media,omitempty"`
	Direction   string `json:"direction,omitempty"`

	// The m= line: the transport protocol as written (RTP/AVP, RTP/SAVP,
	// …), the port (0: rejected, or not a port) and the payload types in
	// order (at most sipSDPMaxPayloads, each 0-127).
	proto    string
	port     int
	payloads []int
	// rtpmap is the stream's a=rtpmap encodings by payload type.
	rtpmap map[int]string
	// ptime is a=ptime in ms, media level over session level (0: absent or
	// not a number of ms in 1-1000).
	ptime int
	// telephoneEventPT is the first listed payload type mapped to
	// telephone-event (RFC 4733), or -1.
	telephoneEventPT int
	// The connection address, media level over session level, as written
	// (multicast TTL dropped), and its address type (IP4, IP6). connIP is
	// set only when the address is an IP literal; connIsHostname marks one
	// that is not, which is never resolved.
	connAddr       string
	connAddrType   string
	connIP         net.IP
	connIsHostname bool
	// rtcpPort and rtcpAddr are the stream's a=rtcp (RFC 3605): its port (0:
	// absent) and the connection address it names, "" when it names none.
	rtcpPort int
	rtcpAddr string
}

// sipSDPMaxPayloads bounds the payload types kept from one m= line.
const sipSDPMaxPayloads = 32

// codecFor is the encoding of payload type pt: the stream's rtpmap, else the
// RFC 3551 static assignment ("" when neither names it).
func (a *sipSDPAnswer) codecFor(pt int) string {
	if codec, ok := a.rtpmap[pt]; ok {
		return codec
	}
	return sipStaticPayloads[pt]
}

// sipStaticPayloads names the RFC 3551 static payload types an answer may
// use without an rtpmap.
var sipStaticPayloads = map[int]string{
	0: "PCMU/8000", 3: "GSM/8000", 4: "G723/8000", 8: "PCMA/8000",
	9: "G722/8000", 18: "G729/8000",
}

// parseSDPAnswer reads the first audio stream of an SDP body: its payload
// types and codecs, the connection address and port, ptime, a=rtcp and the
// direction attribute (media level over session level, sendrecv by
// default; "inactive" for a rejected stream, port 0). Session level is
// everything before the first m= line, so attributes of another stream never
// count for the audio one. Nil when the body has no audio stream.
func parseSDPAnswer(body []byte) *sipSDPAnswer {
	var (
		sessionConn, mediaConn   [2]string // address type, address
		sessionDir, mediaDir     string
		sessionPtime, mediaPtime int
		inMedia, inAudio         bool
		seenAudio                bool
		payload                  = -1
	)
	answer := &sipSDPAnswer{rtpmap: map[int]string{}, telephoneEventPT: -1}
	for _, raw := range bytes.Split(body, []byte("\n")) {
		line := strings.TrimRight(string(raw), "\r")
		if len(line) < 2 || line[1] != '=' {
			continue
		}
		kind, value := line[0], line[2:]
		if kind == 'm' {
			if seenAudio {
				break // only the first audio stream
			}
			inMedia = true
			fields := strings.Fields(value)
			inAudio = len(fields) >= 4 && fields[0] == "audio"
			if inAudio {
				seenAudio = true
				answer.port = sdpPort(fields[1])
				answer.proto = fields[2]
				if pt, err := strconv.Atoi(fields[3]); err == nil {
					payload = pt
				}
				for _, f := range fields[3:] {
					if len(answer.payloads) == sipSDPMaxPayloads {
						break
					}
					if pt, err := strconv.Atoi(f); err == nil && pt >= 0 && pt <= 127 {
						answer.payloads = append(answer.payloads, pt)
					}
				}
			}
			continue
		}
		if inMedia && !inAudio {
			continue // another stream's line
		}
		switch kind {
		case 'c':
			fields := strings.Fields(value)
			if len(fields) < 3 {
				continue
			}
			addr, _, _ := strings.Cut(fields[2], "/") // drop a multicast TTL
			conn := [2]string{strings.ToUpper(fields[1]), addr}
			if inAudio {
				mediaConn = conn
			} else {
				sessionConn = conn
			}
		case 'a':
			name, attr, _ := strings.Cut(value, ":")
			switch name {
			case "sendrecv", "sendonly", "recvonly", "inactive":
				if inAudio {
					mediaDir = name
				} else {
					sessionDir = name
				}
			case "ptime":
				ms, err := strconv.Atoi(strings.TrimSpace(attr))
				if err != nil || ms < 1 || ms > 1000 {
					ms = 0
				}
				if inAudio {
					mediaPtime = ms
				} else {
					sessionPtime = ms
				}
			case "rtpmap":
				if !inAudio {
					continue
				}
				ptStr, encoding, _ := strings.Cut(attr, " ")
				if pt, err := strconv.Atoi(ptStr); err == nil {
					answer.rtpmap[pt] = sipMetricsString(encoding)
				}
			case "rtcp":
				if !inAudio {
					continue
				}
				fields := strings.Fields(attr)
				if len(fields) == 0 {
					continue
				}
				answer.rtcpPort = sdpPort(fields[0])
				answer.rtcpAddr = ""
				if len(fields) >= 4 {
					answer.rtcpAddr = fields[3]
				}
			}
		}
	}
	if !seenAudio {
		return nil
	}
	answer.Direction = "sendrecv"
	if sessionDir != "" {
		answer.Direction = sessionDir
	}
	if mediaDir != "" {
		answer.Direction = mediaDir
	}
	if answer.port == 0 {
		answer.Direction = "inactive"
	}
	answer.ptime = sessionPtime
	if mediaPtime != 0 {
		answer.ptime = mediaPtime
	}
	if payload >= 0 {
		pt := payload
		answer.PayloadType = &pt
		answer.Codec = answer.codecFor(payload)
	}
	for _, pt := range answer.payloads {
		if name, _, _ := strings.Cut(answer.rtpmap[pt], "/"); strings.EqualFold(name, "telephone-event") {
			answer.telephoneEventPT = pt
			break
		}
	}
	conn := mediaConn
	if conn[1] == "" {
		conn = sessionConn
	}
	answer.connAddrType, answer.connAddr = conn[0], conn[1]
	if answer.connAddr != "" {
		answer.connIP = net.ParseIP(answer.connAddr)
		answer.connIsHostname = answer.connIP == nil
	}
	if answer.connAddr != "" && answer.port > 0 {
		answer.RemoteMedia = net.JoinHostPort(sipMetricsString(answer.connAddr), strconv.Itoa(answer.port))
	}
	return answer
}

// sdpPort reads an SDP port field ("49170", or "49170/2" with a port
// count): 0 when it is not a port in 1-65535.
func sdpPort(field string) int {
	field, _, _ = strings.Cut(field, "/")
	port, err := strconv.Atoi(field)
	if err != nil || port < 1 || port > 65535 {
		return 0
	}
	return port
}

// sipBodyIsSDP reports a message carrying an SDP body.
func sipBodyIsSDP(msg *sipMessage) bool {
	if len(msg.Body) == 0 {
		return false
	}
	ct, _, _ := strings.Cut(msg.Get("Content-Type"), ";")
	return strings.EqualFold(strings.TrimSpace(ct), "application/sdp")
}
