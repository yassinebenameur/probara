package rtp

import "math"

// The E-model the estimate uses: ITU-T G.107 at its default parameters, for
// G.711 with packet-loss concealment, with the one-way delay impairment from
// Cole and Rosenbluth's fit of G.107's Id (ACM SIGCOMM CCR 31(2), 2001).
// The result is a MOS-CQE in the terminology of ITU-T P.800.1: an
// estimate of conversational quality computed from network figures, not a
// listening test.
const (
	// Model names the parameters in the metrics, so a reader can tell the
	// estimate apart from a perceptual score. Cole and Rosenbluth used 94.2
	// as the base; 93.2 is G.107's rating at its defaults (Ro − Is).
	Model = "G.107 E-model (R0-Is 93.2, Cole-Rosenbluth Id, G.711 Ie=0 Bpl=25.1, 60 ms reference jitter buffer); MOS-CQE estimate"

	baseR = 93.2
	// G.711's equipment impairment and packet-loss robustness with PLC
	// (G.113 Appendix I). Without concealment Bpl would be 4.3, which is why
	// the metrics list the assumption (AssumptionPLC).
	g711Ie  = 0
	g711Bpl = 25.1
	// idKnee is where Cole and Rosenbluth's Id fit steepens: beyond about
	// 177 ms of mouth-to-ear delay, talkers start to notice.
	idKnee = 177.3

	// AssumptionPLC is listed with every estimate: the far end conceals
	// lost packets, as nearly every endpoint does.
	AssumptionPLC = "g711_plc"
)

// JitterBuffer is the one reference jitter buffer of the measurement: a
// packet later than this behind the stream's earliest is a late discard
// (counted with network loss in the effective loss), and its depth is the
// buffer delay the E-model adds.
const JitterBuffer = 60 // ms

// Where an estimate's one-way delay came from.
const (
	DelayEcho       = "echo"       // half the median echo round trip
	DelayRTCP       = "rtcp"       // half the RTCP LSR/DLSR round trip
	DelayUnmeasured = "unmeasured" // no network delay counted: the MOS is an upper bound
)

// Quality is one evidence basis's E-model estimate, rounded for the metrics.
type Quality struct {
	RFactor     float64 `json:"r_factor"` // 0..100, one decimal
	MOS         float64 `json:"mos_cqe"`  // 1..4.5, two decimals
	DelayMs     int     `json:"delay_ms"` // mouth-to-ear delay d the estimate assumed
	DelaySource string  `json:"delay_source"`
}

// MouthToEar is the E-model delay d for a one-way network delay and a
// packetisation time, both in ms: the network, then the reference jitter
// buffer, then one frame of packetisation (G.711 has no look-ahead).
func MouthToEar(owdMs, ptimeMs float64) float64 {
	return finiteOr(math.Max(owdMs, 0), 0) + JitterBuffer + finiteOr(math.Max(ptimeMs, 0), 0)
}

// Estimate is the E-model for an effective loss pplPct (network loss plus
// late discards, in percent), a burst ratio and a mouth-to-ear delay in ms,
// rounded for the metrics.
func Estimate(pplPct, burstR, delayMs float64, source string) Quality {
	r := RFactor(pplPct, burstR, delayMs)
	return Quality{
		RFactor:     round(r, 1),
		MOS:         round(MOSFromR(r), 2),
		DelayMs:     int(math.Round(finiteOr(delayMs, 0))),
		DelaySource: source,
	}
}

// RFactor is the transmission rating R = 93.2 − Id − Ie,eff, clamped to
// [0, 100]. Out-of-range inputs are clamped first (loss to 0..100 %, a burst
// ratio to at least 1, a delay to at least 0), so it is finite for any input.
func RFactor(pplPct, burstR, delayMs float64) float64 {
	ppl := math.Min(math.Max(finiteOr(pplPct, 0), 0), 100)
	burst := math.Max(finiteOr(burstR, 1), 1)
	d := math.Max(finiteOr(delayMs, 0), 0)

	// Delay impairment (Cole and Rosenbluth).
	id := 0.024 * d
	if d > idKnee {
		id += 0.11 * (d - idKnee)
	}
	// Effective equipment impairment, G.107 eq. 7-29. Bpl > 0 keeps the
	// denominator positive.
	ieEff := g711Ie + (95-g711Ie)*ppl/(ppl/burst+g711Bpl)

	return math.Min(math.Max(baseR-id-ieEff, 0), 100)
}

// MOSFromR is G.107 Annex B's conversion of R to a MOS-CQE. The polynomial
// dips just below 1 for R under about 6.5 (to 0.99 near R = 3.2); the
// result is held at 1 there so it never falls as R rises.
func MOSFromR(r float64) float64 {
	switch {
	case !(r > 0): // NaN included
		return 1
	case r >= 100:
		return 4.5
	}
	return math.Max(1+0.035*r+7e-6*r*(r-60)*(100-r), 1)
}

// BurstRatio is G.107's BurstR from a two-state Markov fit of a loss
// indicator: p is P(lost | previous received), q is P(received | previous
// lost), and BurstR = 1/(p+q), at least 1 (1 is random loss). The arguments
// count the transitions between consecutive packets. A state that was never
// left gives no estimate of its exit probability, so that one is taken as
// random loss would have it (p = 1 − q, or q = 1 − p); with no loss at all
// the ratio is 1.
func BurstRatio(recvToRecv, recvToLost, lostToRecv, lostToLost int) float64 {
	fromRecv, fromLost := recvToRecv+recvToLost, lostToRecv+lostToLost
	var p, q float64
	switch {
	case fromRecv == 0 && fromLost == 0:
		return 1
	case fromLost == 0:
		p = float64(recvToLost) / float64(fromRecv)
		q = 1 - p
	case fromRecv == 0:
		q = float64(lostToRecv) / float64(fromLost)
		p = 1 - q
	default:
		p = float64(recvToLost) / float64(fromRecv)
		q = float64(lostToRecv) / float64(fromLost)
	}
	if !(p+q > 0) {
		return 1
	}
	return math.Max(1/(p+q), 1)
}

// round rounds v to places decimals. A non-finite v cannot come out of the
// guarded arithmetic in this package; were one to slip through, it becomes 0
// rather than a NaN that would make the metrics envelope fail to marshal.
// Negative zero is normalised, since JSON would print it as -0.
func round(v float64, places int) float64 {
	if math.IsNaN(v) || math.IsInf(v, 0) {
		return 0
	}
	scale := math.Pow(10, float64(places))
	r := math.Round(v*scale) / scale
	if r == 0 {
		return 0
	}
	return r
}

func finiteOr(v, fallback float64) float64 {
	if math.IsNaN(v) || math.IsInf(v, 0) {
		return fallback
	}
	return v
}
