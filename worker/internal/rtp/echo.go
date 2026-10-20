package rtp

import (
	"bytes"
	"encoding/binary"
)

// The echo marker: in echo mode the first EchoMarkerSize octets of every
// payload the probe sends are "PRB1", the packet's sequence number and the
// low 16 bits of the probe's SSRC, all big-endian, so an echo application
// that re-originates the stream (new SSRC, new sequence numbers, payload
// kept) is still matched to the packet it echoes. The marker overwrites the
// first millisecond of the tone, which is audible as a faint 50 Hz click at
// 20 ms ptime and harmless on a test line.
const EchoMarkerSize = 8

var echoMagic = []byte("PRB1")

// PutEchoMarker writes the marker for seq and ssrc into the first
// EchoMarkerSize octets of payload, which must be at least that long.
func PutEchoMarker(payload []byte, seq uint16, ssrc uint32) {
	copy(payload, echoMagic)
	binary.BigEndian.PutUint16(payload[4:], seq)
	binary.BigEndian.PutUint16(payload[6:], uint16(ssrc))
}

// EchoMatch says how an inbound packet was recognised as an echo of ours.
type EchoMatch uint8

const (
	NotEcho      EchoMatch = iota
	EchoBySSRC             // our SSRC came back: the header's seq is ours
	EchoByMarker           // re-originated, recognised by the payload marker
)

func (m EchoMatch) String() string {
	switch m {
	case EchoBySSRC:
		return "ssrc"
	case EchoByMarker:
		return "marker"
	}
	return ""
}

// MatchEcho reports whether p echoes a packet the probe sent with ssrc, and
// that packet's sequence number. Our SSRC coming back always matches; the
// payload marker is consulted only when markers says the probe writes them
// (echo mode), so a far stream that happens to start with the same octets
// is never taken for an echo otherwise.
func MatchEcho(p Packet, ssrc uint32, markers bool) (seq uint16, m EchoMatch) {
	if p.SSRC == ssrc {
		return p.Seq, EchoBySSRC
	}
	b := p.Payload
	if markers && len(b) >= EchoMarkerSize && bytes.Equal(b[:4], echoMagic) &&
		binary.BigEndian.Uint16(b[6:]) == uint16(ssrc) {
		return binary.BigEndian.Uint16(b[4:]), EchoByMarker
	}
	return 0, NotEcho
}
