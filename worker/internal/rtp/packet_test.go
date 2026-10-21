package rtp

import (
	"bytes"
	"errors"
	"testing"
)

func TestPacketRoundTrip(t *testing.T) {
	h := Header{Marker: true, PayloadType: 8, Seq: 65535, Timestamp: 0xdeadbeef, SSRC: 0x9f00aa01}
	payload := AppendSilence(nil, PCMA, 160)
	b := AppendPacket(nil, h, payload)
	if len(b) != HeaderSize+160 || b[0] != 0x80 || b[1] != 0x88 {
		t.Fatalf("header % x", b[:HeaderSize])
	}
	p, err := Parse(b)
	if err != nil {
		t.Fatal(err)
	}
	if p.Header != h || !bytes.Equal(p.Payload, payload) {
		t.Fatalf("parsed %+v, %d payload bytes", p.Header, len(p.Payload))
	}
}

func TestParseSkipsCSRCExtensionAndPadding(t *testing.T) {
	b := []byte{
		0xb2, 0x00, 0x00, 0x07, 0, 0, 0, 160, 1, 2, 3, 4, // V2 P X CC=2, PT 0, seq 7
		0xa, 0xa, 0xa, 0xa, 0xb, 0xb, 0xb, 0xb, // two CSRCs
		0xbe, 0xde, 0x00, 0x01, 0x10, 0xff, 0, 0, // a one-word extension
		0xff, 0xff, 0xff, // payload
		0x00, 0x00, 0x03, // three octets of padding
	}
	p, err := Parse(b)
	if err != nil {
		t.Fatal(err)
	}
	if p.Seq != 7 || p.Timestamp != 160 || p.SSRC != 0x01020304 || !bytes.Equal(p.Payload, []byte{0xff, 0xff, 0xff}) {
		t.Fatalf("parsed %+v payload % x", p.Header, p.Payload)
	}
}

func TestParseRefusesMalformed(t *testing.T) {
	valid := AppendPacket(nil, Header{PayloadType: 0, Seq: 1, SSRC: 1}, []byte{0xff, 0xff})
	with := func(edit func(b []byte) []byte) []byte { return edit(bytes.Clone(valid)) }
	cases := []struct {
		name string
		b    []byte
		want error
	}{
		{"empty", nil, ErrShort},
		{"truncated header", valid[:11], ErrShort},
		{"version 1", with(func(b []byte) []byte { b[0] = 0x40; return b }), ErrVersion},
		{"version 0", with(func(b []byte) []byte { b[0] = 0x00; return b }), ErrVersion},
		{"SR's type", with(func(b []byte) []byte { b[1] = 200; return b }), ErrRTCPType},
		{"RR's type without marker", with(func(b []byte) []byte { b[1] = 73; return b }), ErrRTCPType},
		{"APP's type", with(func(b []byte) []byte { b[1] = 204; return b }), ErrRTCPType},
		{"CSRCs overrun", with(func(b []byte) []byte { b[0] |= 0x01; return b }), ErrCSRC},
		{"extension header missing", with(func(b []byte) []byte { b[0] |= 0x10; return b }), ErrExtension},
		{"extension overruns", with(func(b []byte) []byte {
			b[0] |= 0x10
			return append(b[:HeaderSize], 0xbe, 0xde, 0x00, 0x02, 0, 0, 0, 0)
		}), ErrExtension},
		{"zero padding count", with(func(b []byte) []byte { b[0] |= 0x20; b[len(b)-1] = 0; return b }), ErrPadding},
		{"padding larger than the payload", with(func(b []byte) []byte { b[0] |= 0x20; b[len(b)-1] = 3; return b }), ErrPadding},
		{"padding eats the payload", with(func(b []byte) []byte { b[0] |= 0x20; b[len(b)-1] = 2; return b }), ErrNoPayload},
		{"header only", valid[:HeaderSize], ErrNoPayload},
	}
	for _, c := range cases {
		if _, err := Parse(c.b); !errors.Is(err, c.want) {
			t.Errorf("%s: err = %v, want %v", c.name, err, c.want)
		}
	}
}

func TestIsRTCP(t *testing.T) {
	for _, c := range []struct {
		b    []byte
		want bool
	}{
		{[]byte{0x80, 200}, true},
		{[]byte{0x81, 201, 0, 7}, true},
		{[]byte{0x80, 204}, true},
		{[]byte{0x80, 205}, false},
		{[]byte{0x80, 199}, false},
		{[]byte{0x80, 0x80 | 101}, false}, // telephone-event with its marker
		{[]byte{0x40, 200}, false},
		{[]byte{0x80}, false},
	} {
		if got := IsRTCP(c.b); got != c.want {
			t.Errorf("IsRTCP(% x) = %v", c.b, got)
		}
	}
}

// FuzzParseRTP: the parser never panics, returns a payload inside the
// datagram, reads the header fields as written, and agrees with
// AppendPacket on every plain packet it accepts.
func FuzzParseRTP(f *testing.F) {
	f.Add(AppendPacket(nil, Header{PayloadType: 0, Seq: 1, Timestamp: 160, SSRC: 7}, AppendTone(nil, PCMU, 0, 160)))
	f.Add([]byte{0xbf, 0x80, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0})
	f.Add([]byte{0x90, 0x00, 0, 1, 0, 0, 0, 1, 0, 0, 0, 1, 0xbe, 0xde, 0xff, 0xff, 0xff})
	f.Add([]byte{0xa0, 0x08, 0, 1, 0, 0, 0, 1, 0, 0, 0, 1, 0xd5, 0x01})
	f.Fuzz(func(t *testing.T, b []byte) {
		p, err := Parse(b)
		if err != nil {
			return
		}
		if len(p.Payload) == 0 || len(p.Payload) > len(b)-HeaderSize {
			t.Fatalf("payload of %d bytes from a %d-byte datagram", len(p.Payload), len(b))
		}
		start := cap(b) - cap(p.Payload) // Payload is b[start:end]
		if start < HeaderSize || start+len(p.Payload) > len(b) {
			t.Fatalf("payload at [%d, %d) of %d bytes", start, start+len(p.Payload), len(b))
		}
		if p.PayloadType != b[1]&0x7f || p.Marker != (b[1]&0x80 != 0) || p.PayloadType >= 72 && p.PayloadType <= 76 {
			t.Fatalf("payload type %d marker %v from % x", p.PayloadType, p.Marker, b[:2])
		}
		if b[0]&0x3f == 0 && !bytes.Equal(AppendPacket(nil, p.Header, p.Payload), b) {
			t.Fatalf("re-encoding % x differs", b)
		}
	})
}
