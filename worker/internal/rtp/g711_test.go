package rtp

import (
	"bytes"
	"math"
	"testing"
)

func TestG711ReferenceCodes(t *testing.T) {
	cases := []struct {
		law    Law
		sample int16
		code   byte
	}{
		{PCMU, 0, 0xFF}, {PCMU, 32767, 0x80}, {PCMU, -32768, 0x00}, {PCMU, -4, 0x7E},
		{PCMA, 0, 0xD5}, {PCMA, 32767, 0xAA}, {PCMA, -32768, 0x2A}, {PCMA, -8, 0x55},
	}
	for _, c := range cases {
		if got := c.law.Encode(c.sample); got != c.code {
			t.Errorf("%v.Encode(%d) = %#02x, want %#02x", c.law, c.sample, got, c.code)
		}
	}
	if PCMU.Silence() != 0xFF || PCMA.Silence() != 0xD5 {
		t.Fatalf("silence %#02x %#02x", PCMU.Silence(), PCMA.Silence())
	}
	if PCMU.Decode(0xFF) != 0 || PCMA.Decode(0xD5) != 8 {
		t.Fatalf("silence decodes to %d / %d", PCMU.Decode(0xFF), PCMA.Decode(0xD5))
	}
}

// Every code decodes to a level that encodes back to a code of the same
// level (µ-law has two zeros), and any sample comes back within its
// segment's quantisation error.
func TestG711RoundTrip(t *testing.T) {
	for _, law := range []Law{PCMU, PCMA} {
		for b := range 256 {
			v := law.Decode(byte(b))
			if got := law.Decode(law.Encode(v)); got != v {
				t.Fatalf("%v: code %#02x decodes to %d, which re-encodes to %d", law, b, v, got)
			}
		}
		for s := -32768; s <= 32767; s += 7 {
			got := int(law.Decode(law.Encode(int16(s))))
			// Both laws' coarsest step, at full scale, is 1024 in 16-bit
			// units (µ-law also clips above 32124); elsewhere the error
			// stays within a sixteenth of the sample.
			if diff := math.Abs(float64(got - s)); diff > 1024 || diff > math.Max(math.Abs(float64(s))/16, 32) {
				t.Fatalf("%v: %d round-trips to %d", law, s, got)
			}
		}
	}
}

func TestToneLevelAndFrequency(t *testing.T) {
	for _, law := range []Law{PCMU, PCMA} {
		period := AppendTone(nil, law, 0, TonePeriod)
		var sum float64
		crossings := 0
		prev := law.Decode(period[TonePeriod-1])
		for _, b := range period {
			v := law.Decode(b)
			sum += float64(v) * float64(v)
			if (prev < 0) != (v < 0) {
				crossings++
			}
			prev = v
		}
		// 251 cycles in a period, two sign changes each.
		if crossings != 2*251 {
			t.Errorf("%v: %d sign changes in a period, want %d (1004 Hz)", law, crossings, 2*251)
		}
		// −20 dBm0 relative to the law's 0 dBm0 sine, within the
		// quantisation's fraction of a dB.
		rms := math.Sqrt(sum / TonePeriod)
		zeroDBm0 := tonePeak(law) * math.Pow(10, -ToneDBm0/20.0) / math.Sqrt2
		if level := 20 * math.Log10(rms/zeroDBm0); math.Abs(level-ToneDBm0) > 0.2 {
			t.Errorf("%v: tone at %.2f dBm0, want %d", law, level, ToneDBm0)
		}
	}
}

func TestToneContinuesAcrossPeriod(t *testing.T) {
	period := AppendTone(nil, PCMU, 0, TonePeriod)
	// The frame starting 80 samples before the period's end wraps into its
	// start, and frames keep the phase wherever they start.
	frame := AppendTone(nil, PCMU, TonePeriod-80, 160)
	if !bytes.Equal(frame, append(bytes.Clone(period[TonePeriod-80:]), period[:80]...)) {
		t.Fatal("frame across the period boundary does not continue the sine")
	}
	if !bytes.Equal(AppendTone(nil, PCMU, 37*160, 160), AppendTone(nil, PCMU, (37*160)%TonePeriod, 160)) {
		t.Fatal("frame k differs from its offset in the period")
	}
	if !bytes.Equal(AppendTone(nil, PCMU, -80, 80), period[TonePeriod-80:]) {
		t.Fatal("negative start index")
	}
	long := AppendTone(nil, PCMA, 0, 2*TonePeriod+5)
	if len(long) != 2*TonePeriod+5 || !bytes.Equal(long[TonePeriod:2*TonePeriod], AppendTone(nil, PCMA, 0, TonePeriod)) {
		t.Fatal("frame longer than a period")
	}
	// The sample at index n is the sine at n/8000 s.
	n := 1234
	want := PCMU.Encode(int16(math.Round(tonePeak(PCMU) * math.Sin(2*math.Pi*ToneHz*float64(n)/ClockRate))))
	if got := period[n]; got != want {
		t.Fatalf("sample %d = %#02x, want %#02x", n, got, want)
	}
}

func TestSilence(t *testing.T) {
	if got := AppendSilence([]byte{1}, PCMA, 3); !bytes.Equal(got, []byte{1, 0xD5, 0xD5, 0xD5}) {
		t.Fatalf("A-law silence % x", got)
	}
	if got := AppendSilence(nil, PCMU, 160); len(got) != 160 || bytes.Count(got, []byte{0xFF}) != 160 {
		t.Fatal("µ-law silence")
	}
}

func TestEchoMarker(t *testing.T) {
	const ssrc = 0x9f00aa01
	payload := AppendTone(nil, PCMU, 0, 160)
	PutEchoMarker(payload, 0x1234, ssrc)
	if !bytes.Equal(payload[:EchoMarkerSize], []byte{'P', 'R', 'B', '1', 0x12, 0x34, 0xaa, 0x01}) {
		t.Fatalf("marker % x", payload[:EchoMarkerSize])
	}
	if !bytes.Equal(payload[EchoMarkerSize:], AppendTone(nil, PCMU, 0, 160)[EchoMarkerSize:]) {
		t.Fatal("marker overwrote more than its 8 octets")
	}

	ours := Packet{Header: Header{SSRC: ssrc, Seq: 0x1234}, Payload: payload}
	reoriginated := Packet{Header: Header{SSRC: 0x5555, Seq: 9}, Payload: payload}
	other := Packet{Header: Header{SSRC: 0x5555, Seq: 9}, Payload: AppendSilence(nil, PCMU, 160)}
	foreignMarker := bytes.Clone(payload)
	PutEchoMarker(foreignMarker, 0x1234, 0x77770000)
	cases := []struct {
		name    string
		p       Packet
		markers bool
		seq     uint16
		m       EchoMatch
	}{
		{"our SSRC back", ours, false, 0x1234, EchoBySSRC},
		{"re-originated, echo mode", reoriginated, true, 0x1234, EchoByMarker},
		{"re-originated, markers off", reoriginated, false, 0, NotEcho},
		{"far stream", other, true, 0, NotEcho},
		{"another probe's marker", Packet{Header: Header{SSRC: 1}, Payload: foreignMarker}, true, 0, NotEcho},
		{"short payload", Packet{Header: Header{SSRC: 1}, Payload: payload[:7]}, true, 0, NotEcho},
	}
	for _, c := range cases {
		seq, m := MatchEcho(c.p, ssrc, c.markers)
		if m != c.m || seq != c.seq {
			t.Errorf("%s: %v %#x, want %v %#x", c.name, m, seq, c.m, c.seq)
		}
	}
	if EchoBySSRC.String() != "ssrc" || EchoByMarker.String() != "marker" || NotEcho.String() != "" {
		t.Fatal("match names")
	}
}
