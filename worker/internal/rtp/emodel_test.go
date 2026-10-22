package rtp

import (
	"math"
	"testing"
)

// The anchors are worked by hand from the formulas (G.107 eq. 7-29 and
// Annex B, Cole and Rosenbluth's Id), to four places, so a change to a
// constant or a term shows up here rather than as a drifted MOS in the field.
func TestEModelAnchors(t *testing.T) {
	cases := []struct {
		ppl, d, burst float64
		ieEff, r, mos float64
	}{
		{0, 0, 1, 0, 93.20, 4.4093},
		// The practical ceiling: G.711, the 60 ms reference buffer, 20 ms
		// ptime and no measured network delay.
		{0, 80, 1, 0, 91.28, 4.3691},
		{1, 80, 1, 3.6398, 87.6402, 4.2770},
		{2, 80, 1, 7.0111, 84.2689, 4.1746}, // 4.17, not the 4.18 the design's table had
		{5, 80, 1, 15.7807, 75.4993, 3.8432},
		{3, 80, 2, 10.7143, 80.5657, 4.0452},
		{0, 177.3, 1, 0, 88.9448, 4.3123}, // the Id knee
		{0, 250, 1, 0, 79.2030, 3.9935},
	}
	for _, c := range cases {
		r := RFactor(c.ppl, c.burst, c.d)
		ieEff := 93.2 - 0.024*c.d - math.Max(c.d-177.3, 0)*0.11 - r
		if math.Abs(ieEff-c.ieEff) > 5e-4 || math.Abs(r-c.r) > 5e-4 {
			t.Errorf("Ppl %v%% d %v BurstR %v: Ie,eff %.4f R %.4f, want %.4f %.4f", c.ppl, c.d, c.burst, ieEff, r, c.ieEff, c.r)
		}
		if mos := MOSFromR(r); math.Abs(mos-c.mos) > 5e-4 {
			t.Errorf("R %.4f: MOS %.4f, want %.4f", r, mos, c.mos)
		}
	}
}

// The thresholds call.media accepts top out at MOS 4.3, which must stay
// reachable by a clean call at every ptime the probe sends with (the larger
// the ptime, the lower the ceiling), and the model can never exceed G.711's
// own ceiling of about 4.41 (R 93.2 with no delay at all).
func TestEModelCeilingAboveThresholdBound(t *testing.T) {
	const maxMOSThreshold = 4.3
	for _, ptime := range []float64{10, 20, 30, 40, 60} {
		q := Estimate(0, 1, MouthToEar(0, ptime), DelayUnmeasured)
		if q.MOS <= maxMOSThreshold || q.MOS > 4.41 {
			t.Errorf("ptime %v: clean-call MOS %.2f, want in (%.1f, 4.41]", ptime, q.MOS, maxMOSThreshold)
		}
	}
	if got := round(MOSFromR(RFactor(0, 1, 0)), 2); got != 4.41 {
		t.Fatalf("G.711 ceiling = %.2f, want 4.41", got)
	}
}

func TestEModelClamps(t *testing.T) {
	for _, c := range []struct{ r, mos float64 }{
		{-5, 1}, {0, 1}, {math.NaN(), 1}, {3.2, 1}, {100, 4.5}, {120, 4.5}, {math.Inf(1), 4.5},
	} {
		if got := MOSFromR(c.r); got != c.mos {
			t.Errorf("MOSFromR(%v) = %v, want %v", c.r, got, c.mos)
		}
	}
	if r := RFactor(100, 1, 2000); r != 0 {
		t.Errorf("R at 100%% loss and 2 s = %v, want clamped to 0", r)
	}
	// Out-of-range and non-finite inputs are clamped, never propagated.
	for _, in := range [][3]float64{
		{math.NaN(), math.NaN(), math.NaN()}, {-10, 0, -50}, {500, -3, math.Inf(1)}, {math.Inf(-1), math.Inf(1), 0},
	} {
		r := RFactor(in[0], in[1], in[2])
		if math.IsNaN(r) || r < 0 || r > 100 {
			t.Errorf("RFactor%v = %v", in, r)
		}
	}
	if got := RFactor(-10, 0, -50); got != 93.2 {
		t.Errorf("negative loss and delay read as %v, want the clean 93.2", got)
	}
}

func TestEModelMonotonic(t *testing.T) {
	prevByLoss := math.Inf(1)
	for ppl := 0.0; ppl <= 100; ppl += 0.25 {
		r := RFactor(ppl, 1, 80)
		if r > prevByLoss {
			t.Fatalf("R rises with loss at %v%%: %v > %v", ppl, r, prevByLoss)
		}
		prevByLoss = r
	}
	prevByDelay := math.Inf(1)
	for d := 0.0; d <= 1000; d += 2.5 {
		r := RFactor(1, 1, d)
		if r > prevByDelay {
			t.Fatalf("R rises with delay at %v ms", d)
		}
		prevByDelay = r
	}
	// Burstier loss of the same amount is worse (G.107: BurstR > 1).
	if RFactor(3, 4, 80) >= RFactor(3, 1, 80) {
		t.Fatal("bursty loss rated no worse than random loss")
	}
	prevMOS := 0.0
	for r := -1.0; r <= 101; r += 0.1 {
		m := MOSFromR(r)
		if m < prevMOS {
			t.Fatalf("MOS falls as R rises at R %v: %v < %v", r, m, prevMOS)
		}
		prevMOS = m
	}
}

func TestEstimateRounds(t *testing.T) {
	q := Estimate(0.44, 1, MouthToEar(19, 20), DelayEcho)
	// d = 19 + 60 + 20 = 99; Ie,eff = 95·0.44/25.54 = 1.6366;
	// R = 93.2 − 2.376 − 1.6366 = 89.1874; MOS = 4.3186.
	want := Quality{RFactor: 89.2, MOS: 4.32, DelayMs: 99, DelaySource: DelayEcho}
	if q != want {
		t.Fatalf("Estimate = %+v, want %+v", q, want)
	}
}

func TestBurstRatio(t *testing.T) {
	cases := []struct {
		name           string
		rr, rl, lr, ll int
		want           float64
	}{
		{"no loss", 224, 0, 0, 0, 1},
		{"nothing at all", 0, 0, 0, 0, 1},
		// One run of 5 in 225: p = 1/219, q = 1/5, 1/(p+q) = 4.8884.
		{"one burst", 218, 1, 1, 4, 4.8884},
		// Five isolated losses: p = 5/219, q = 1, so 1/(p+q) < 1, held at 1.
		{"isolated losses", 214, 5, 5, 0, 1},
		// p = 0.2, q = 0.8: exactly random.
		{"random", 8, 2, 4, 1, 1},
		// p = 0.1, q = 0.4: 1/0.5 = 2.
		{"twice as bursty", 9, 1, 2, 3, 2},
		// Everything lost: no exit from loss but the closing one, p taken
		// as 1 − q.
		{"all lost", 0, 0, 1, 99, 1},
	}
	for _, c := range cases {
		if got := BurstRatio(c.rr, c.rl, c.lr, c.ll); math.Abs(got-c.want) > 5e-5 {
			t.Errorf("%s: BurstRatio = %.5f, want %.4f", c.name, got, c.want)
		}
	}
}

func TestRound(t *testing.T) {
	for _, c := range []struct {
		v      float64
		places int
		want   float64
	}{
		{0.4444, 2, 0.44}, {4.375, 2, 4.38}, {89.25, 1, 89.3}, {-0.001, 2, 0}, {math.NaN(), 2, 0}, {math.Inf(-1), 1, 0},
	} {
		got := round(c.v, c.places)
		if got != c.want || math.Signbit(got) && got == 0 {
			t.Errorf("round(%v, %d) = %v, want %v", c.v, c.places, got, c.want)
		}
	}
}
