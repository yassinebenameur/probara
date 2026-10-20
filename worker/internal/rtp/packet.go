package rtp

import (
	"encoding/binary"
	"errors"
)

// HeaderSize is the fixed RTP header: no CSRCs, no extension (RFC 3550 §5.1).
const HeaderSize = 12

// Header is the part of an RTP header the probe writes and the statistics
// read. CSRCs and a header extension are skipped on parse and never written.
type Header struct {
	Marker      bool
	PayloadType uint8 // 0..127
	Seq         uint16
	Timestamp   uint32
	SSRC        uint32
}

// Packet is a parsed RTP packet. Payload aliases the parsed buffer.
type Packet struct {
	Header
	Payload []byte
}

// Why Parse refused a datagram. A media session counts them all as invalid.
var (
	ErrShort     = errors.New("rtp: shorter than its header")
	ErrVersion   = errors.New("rtp: version is not 2")
	ErrRTCPType  = errors.New("rtp: payload type collides with RTCP")
	ErrCSRC      = errors.New("rtp: CSRC list overruns the packet")
	ErrExtension = errors.New("rtp: header extension overruns the packet")
	ErrPadding   = errors.New("rtp: invalid padding count")
	ErrNoPayload = errors.New("rtp: no payload")
)

// Parse decodes b with the header validity checks of RFC 3550 Appendix A.1:
// version 2, a payload type that does not read as an RTCP packet type once
// the marker bit is taken as its top bit (72..76 are SR..APP; A.1 names SR
// and RR, and the other types in use collide the same way), a CSRC list and
// extension that fit, and a padding count that leaves the header intact. A
// packet whose payload is empty after padding is refused too: every payload
// format the probe accepts carries at least one octet. Parse never
// allocates.
func Parse(b []byte) (Packet, error) {
	if len(b) < HeaderSize {
		return Packet{}, ErrShort
	}
	if b[0]>>6 != 2 {
		return Packet{}, ErrVersion
	}
	pt := b[1] & 0x7f
	if pt >= 72 && pt <= 76 {
		return Packet{}, ErrRTCPType
	}
	off := HeaderSize + 4*int(b[0]&0x0f)
	if off > len(b) {
		return Packet{}, ErrCSRC
	}
	if b[0]&0x10 != 0 {
		if off+4 > len(b) {
			return Packet{}, ErrExtension
		}
		off += 4 + 4*int(binary.BigEndian.Uint16(b[off+2:]))
		if off > len(b) {
			return Packet{}, ErrExtension
		}
	}
	end := len(b)
	if b[0]&0x20 != 0 {
		pad := int(b[len(b)-1])
		if pad == 0 || pad > end-off {
			return Packet{}, ErrPadding
		}
		end -= pad
	}
	if end == off {
		return Packet{}, ErrNoPayload
	}
	return Packet{
		Header: Header{
			Marker:      b[1]&0x80 != 0,
			PayloadType: pt,
			Seq:         binary.BigEndian.Uint16(b[2:]),
			Timestamp:   binary.BigEndian.Uint32(b[4:]),
			SSRC:        binary.BigEndian.Uint32(b[8:]),
		},
		Payload: b[off:end],
	}, nil
}

// AppendPacket appends h (version 2, no padding, extension or CSRCs) and
// payload to dst.
func AppendPacket(dst []byte, h Header, payload []byte) []byte {
	b1 := h.PayloadType & 0x7f
	if h.Marker {
		b1 |= 0x80
	}
	dst = append(dst, 0x80, b1)
	dst = binary.BigEndian.AppendUint16(dst, h.Seq)
	dst = binary.BigEndian.AppendUint32(dst, h.Timestamp)
	dst = binary.BigEndian.AppendUint32(dst, h.SSRC)
	return append(dst, payload...)
}

// IsRTCP reports whether b reads as RTCP rather than RTP when both arrive on
// one port (RFC 5761 §4): version 2 and a packet type the RTCP specs in use
// define (SR 200, RR 201, SDES 202, BYE 203, APP 204).
func IsRTCP(b []byte) bool {
	return len(b) >= 2 && b[0]>>6 == 2 && b[1] >= 200 && b[1] <= 204
}
