package worker

import (
	"bytes"
	"errors"
	"fmt"
	"math/rand/v2"
	"net"
	"strconv"
	"strings"
	"sync/atomic"
)

// sipRTPPair is the RTP/RTCP port pair a test call's SDP offer names: an even
// port and the one above it (RFC 3550 §11), both bound so the offer never
// advertises a port that something else owns. The call sends no media; a
// drain goroutine only counts what the far end sends (rtp_rx_packets), so an
// SBC that streams early media or ringback does not fill a socket buffer.
type sipRTPPair struct {
	rtp, rtcp *net.UDPConn
	port      int
	packets   atomic.Int64
	bytes     atomic.Int64
}

const (
	sipRTPPortMin   = 16384
	sipRTPPortMax   = 32766 // the RTP port is even and below this, RTCP one above
	sipRTPBindTries = 8
)

// bindSIPRTPPair binds an even port p and p+1 on ip, trying up to
// sipRTPBindTries random pairs in the conventional RTP range.
func bindSIPRTPPair(ip net.IP) (*sipRTPPair, error) {
	var lastErr error
	for range sipRTPBindTries {
		port := sipRTPPortMin + 2*rand.IntN((sipRTPPortMax-sipRTPPortMin)/2)
		rtp, err := net.ListenUDP("udp", &net.UDPAddr{IP: ip, Port: port})
		if err != nil {
			lastErr = err
			continue
		}
		rtcp, err := net.ListenUDP("udp", &net.UDPAddr{IP: ip, Port: port + 1})
		if err != nil {
			rtp.Close()
			lastErr = err
			continue
		}
		pair := &sipRTPPair{rtp: rtp, rtcp: rtcp, port: port}
		go pair.drain(rtp, true)
		go pair.drain(rtcp, false)
		return pair, nil
	}
	return nil, fmt.Errorf("cannot bind an RTP port pair on %s: %w", ip, lastErr)
}

func (p *sipRTPPair) drain(conn *net.UDPConn, count bool) {
	buf := make([]byte, 2048)
	for {
		n, err := conn.Read(buf)
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
	}
}

// Close releases both ports and stops the drains.
func (p *sipRTPPair) Close() {
	p.rtp.Close()
	p.rtcp.Close()
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

// sipSDPAnswer is what the far end's SDP answer says about the audio stream
// (metrics_data.sip.call.sdp_answer). RemoteMedia is recorded, never
// contacted.
type sipSDPAnswer struct {
	Codec       string `json:"codec,omitempty"`
	PayloadType *int   `json:"payload_type,omitempty"`
	RemoteMedia string `json:"remote_media,omitempty"`
	Direction   string `json:"direction,omitempty"`
}

// sipStaticPayloads names the RFC 3551 static payload types an answer may
// use without an rtpmap.
var sipStaticPayloads = map[int]string{
	0: "PCMU/8000", 3: "GSM/8000", 4: "G723/8000", 8: "PCMA/8000",
	9: "G722/8000", 18: "G729/8000",
}

// parseSDPAnswer reads the first audio stream of an SDP body: its first
// payload type and codec, the connection address and port, and the
// direction attribute (media level over session level, sendrecv by
// default; "inactive" for a rejected stream, port 0). Nil when the body has
// no audio stream.
func parseSDPAnswer(body []byte) *sipSDPAnswer {
	var (
		sessionConn, mediaConn string
		sessionDir, mediaDir   string
		inAudio, seenAudio     bool
		port                   int
		payload                = -1
		rtpmap                 = map[int]string{}
	)
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
			fields := strings.Fields(value)
			inAudio = len(fields) >= 4 && fields[0] == "audio"
			if inAudio {
				seenAudio = true
				port, _ = strconv.Atoi(fields[1])
				if pt, err := strconv.Atoi(fields[3]); err == nil {
					payload = pt
				}
			}
			continue
		}
		switch kind {
		case 'c':
			fields := strings.Fields(value)
			if len(fields) < 3 {
				continue
			}
			addr, _, _ := strings.Cut(fields[2], "/") // drop a multicast TTL
			if inAudio {
				mediaConn = addr
			} else if !seenAudio {
				sessionConn = addr
			}
		case 'a':
			name, attr, _ := strings.Cut(value, ":")
			switch name {
			case "sendrecv", "sendonly", "recvonly", "inactive":
				if inAudio {
					mediaDir = name
				} else if !seenAudio {
					sessionDir = name
				}
			case "rtpmap":
				if !inAudio {
					continue
				}
				ptStr, encoding, _ := strings.Cut(attr, " ")
				if pt, err := strconv.Atoi(ptStr); err == nil {
					rtpmap[pt] = sipMetricsString(encoding)
				}
			}
		}
	}
	if !seenAudio {
		return nil
	}
	answer := &sipSDPAnswer{Direction: "sendrecv"}
	if sessionDir != "" {
		answer.Direction = sessionDir
	}
	if mediaDir != "" {
		answer.Direction = mediaDir
	}
	if port == 0 {
		answer.Direction = "inactive"
	}
	if payload >= 0 {
		pt := payload
		answer.PayloadType = &pt
		if codec, ok := rtpmap[payload]; ok {
			answer.Codec = codec
		} else {
			answer.Codec = sipStaticPayloads[payload]
		}
	}
	conn := mediaConn
	if conn == "" {
		conn = sessionConn
	}
	if conn != "" && port > 0 {
		answer.RemoteMedia = net.JoinHostPort(sipMetricsString(conn), strconv.Itoa(port))
	}
	return answer
}

// sipBodyIsSDP reports a message carrying an SDP body.
func sipBodyIsSDP(msg *sipMessage) bool {
	if len(msg.Body) == 0 {
		return false
	}
	ct, _, _ := strings.Cut(msg.Get("Content-Type"), ";")
	return strings.EqualFold(strings.TrimSpace(ct), "application/sdp")
}
