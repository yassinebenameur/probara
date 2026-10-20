package rtp

import "math"

// Law is a G.711 companding law, valued at its static RTP payload type
// (RFC 3551 §6), which is also how the probe's offer numbers it.
type Law uint8

const (
	PCMU Law = 0 // µ-law
	PCMA Law = 8 // A-law
)

// ClockRate is the RTP clock of every payload format the probe sends or
// receives: PCMU, PCMA, telephone-event and comfort noise (RFC 3551 §6).
const ClockRate = 8000

func (l Law) String() string {
	if l == PCMA {
		return "PCMA"
	}
	return "PCMU"
}

// Encode compresses one 16-bit linear sample.
func (l Law) Encode(sample int16) byte {
	if l == PCMA {
		return encodeALaw(sample)
	}
	return encodeULaw(sample)
}

// Decode expands one octet to a 16-bit linear sample.
func (l Law) Decode(b byte) int16 {
	if l == PCMA {
		return aLawDecode[b]
	}
	return uLawDecode[b]
}

// Silence is the law's digital silence, the code for a zero sample: 0xFF in
// µ-law and 0xD5 in A-law (whose even bits are inverted on the wire).
func (l Law) Silence() byte {
	if l == PCMA {
		return 0xD5
	}
	return 0xFF
}

// The probe's echo-mode signal: a sine at ToneHz and ToneDBm0. 1004 Hz is
// the test-practice frequency rather than 1 kHz, which divides the 8 kHz
// sampling rate and so hits the same few codes every cycle; it also stays
// well clear of the 2100 Hz echo-canceller-disable and 2600 Hz signalling
// tones. 1004 and 8000 share a factor of 4, so the samples repeat every
// TonePeriod samples (251 cycles), which is the table each law keeps.
const (
	ToneHz     = 1004
	ToneDBm0   = -20
	TonePeriod = ClockRate / 4 // gcd(1004, 8000) = 4
)

// tonePeak is the sine's peak in 16-bit linear units for each law. G.711
// puts each law's theoretical load capacity, the sine whose peak just
// reaches full scale (8159 in 14-bit µ-law units, 4096 in 13-bit A-law
// units), at +3.17 dBm0 for µ-law and +3.14 dBm0 for A-law.
func tonePeak(l Law) float64 {
	if l == PCMA {
		return 8 * 4096 * math.Pow(10, (ToneDBm0-3.14)/20)
	}
	return 4 * 8159 * math.Pow(10, (ToneDBm0-3.17)/20)
}

var (
	uLawTone, aLawTone     = toneTable(PCMU), toneTable(PCMA)
	uLawDecode, aLawDecode = decodeTable(PCMU), decodeTable(PCMA)
)

func toneTable(l Law) *[TonePeriod]byte {
	var t [TonePeriod]byte
	peak := tonePeak(l)
	for n := range t {
		t[n] = l.Encode(int16(math.Round(peak * math.Sin(2*math.Pi*ToneHz*float64(n)/ClockRate))))
	}
	return &t
}

func decodeTable(l Law) *[256]int16 {
	var t [256]int16
	for b := range t {
		if l == PCMA {
			t[b] = decodeALaw(byte(b))
		} else {
			t[b] = decodeULaw(byte(b))
		}
	}
	return &t
}

// AppendTone appends n samples of the test tone starting at sample index
// from (counted from the start of the stream), so consecutive frames
// continue one sine without a phase step.
func AppendTone(dst []byte, l Law, from, n int) []byte {
	t := uLawTone
	if l == PCMA {
		t = aLawTone
	}
	i := from % TonePeriod
	if i < 0 {
		i += TonePeriod
	}
	for n > 0 {
		k := min(n, TonePeriod-i)
		dst = append(dst, t[i:i+k]...)
		n -= k
		i = 0
	}
	return dst
}

// AppendSilence appends n samples of the law's digital silence.
func AppendSilence(dst []byte, l Law, n int) []byte {
	s := l.Silence()
	for range n {
		dst = append(dst, s)
	}
	return dst
}

// The G.711 coders below are the reference ones (Sun Microsystems' public
// g711.c), working on 16-bit linear samples.

var (
	uLawSegEnd = [8]int{0x3F, 0x7F, 0xFF, 0x1FF, 0x3FF, 0x7FF, 0xFFF, 0x1FFF}
	aLawSegEnd = [8]int{0x1F, 0x3F, 0x7F, 0xFF, 0x1FF, 0x3FF, 0x7FF, 0xFFF}
)

func segment(v int, ends *[8]int) int {
	for i, end := range ends {
		if v <= end {
			return i
		}
	}
	return 8
}

const (
	uLawBias = 0x84
	uLawClip = 8159 // 14-bit magnitude
)

func encodeULaw(sample int16) byte {
	v := int(sample) >> 2
	mask := 0xFF
	if v < 0 {
		v, mask = -v, 0x7F
	}
	v = min(v, uLawClip) + uLawBias>>2
	seg := segment(v, &uLawSegEnd)
	if seg >= 8 {
		return byte(0x7F ^ mask)
	}
	return byte((seg<<4 | (v>>(seg+1))&0x0F) ^ mask)
}

func decodeULaw(b byte) int16 {
	u := int(^b)
	t := ((u&0x0F)<<3 + uLawBias) << ((u & 0x70) >> 4)
	if u&0x80 != 0 {
		return int16(uLawBias - t)
	}
	return int16(t - uLawBias)
}

func encodeALaw(sample int16) byte {
	v := int(sample) >> 3
	mask := 0xD5
	if v < 0 {
		v, mask = -v-1, 0x55
	}
	seg := segment(v, &aLawSegEnd)
	if seg >= 8 {
		return byte(0x7F ^ mask)
	}
	a := seg << 4
	if seg < 2 {
		a |= (v >> 1) & 0x0F
	} else {
		a |= (v >> seg) & 0x0F
	}
	return byte(a ^ mask)
}

func decodeALaw(b byte) int16 {
	a := int(b ^ 0x55)
	t := (a & 0x0F) << 4
	switch seg := (a & 0x70) >> 4; seg {
	case 0:
		t += 8
	case 1:
		t += 0x108
	default:
		t = (t + 0x108) << (seg - 1)
	}
	if a&0x80 != 0 {
		return int16(t)
	}
	return int16(-t)
}
