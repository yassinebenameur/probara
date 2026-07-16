package anomaly

import (
	"math"
	"testing"
)

func flatBaseline(n int, v float64) []float64 {
	out := make([]float64, n)
	for i := range out {
		out[i] = v
	}
	return out
}

func TestDetect(t *testing.T) {
	cfg := Config{Sensitivity: 3.5, MinDeltaPct: 20, MinSamples: 3, MinBaseline: 6}

	tests := []struct {
		name          string
		baseline      []float64
		recentMeanMs  float64
		recentSamples int
		cfg           Config
		wantAnomalous bool
	}{
		{
			name:          "stable baseline with clear spike fires",
			baseline:      []float64{100, 102, 98, 101, 99, 100, 103, 97},
			recentMeanMs:  400,
			recentSamples: 5,
			cfg:           cfg,
			wantAnomalous: true,
		},
		{
			name:          "recent within baseline does not fire",
			baseline:      []float64{100, 102, 98, 101, 99, 100, 103, 97},
			recentMeanMs:  101,
			recentSamples: 5,
			cfg:           cfg,
			wantAnomalous: false,
		},
		{
			name:          "noisy baseline absorbs moderate increase",
			baseline:      []float64{50, 200, 80, 300, 120, 250, 90, 180, 60, 220},
			recentMeanMs:  260,
			recentSamples: 5,
			cfg:           cfg,
			wantAnomalous: false,
		},
		{
			name:          "MAD-zero fallback fires on percentage gate",
			baseline:      flatBaseline(10, 100),
			recentMeanMs:  130, // +30% over the 20% floor
			recentSamples: 5,
			cfg:           cfg,
			wantAnomalous: true,
		},
		{
			name:          "MAD-zero fallback does not fire below percentage gate",
			baseline:      flatBaseline(10, 100),
			recentMeanMs:  110, // only +10%, under the 20% floor
			recentSamples: 5,
			cfg:           cfg,
			wantAnomalous: false,
		},
		{
			name:          "cold baseline never fires",
			baseline:      []float64{100, 102},
			recentMeanMs:  1000,
			recentSamples: 5,
			cfg:           cfg,
			wantAnomalous: false,
		},
		{
			name:          "too few recent samples never fires",
			baseline:      flatBaseline(10, 100),
			recentMeanMs:  1000,
			recentSamples: 2,
			cfg:           cfg,
			wantAnomalous: false,
		},
		{
			name:          "z-score over threshold but under delta floor does not fire",
			baseline:      []float64{1000, 1001, 999, 1000, 1002, 998, 1000, 1001},
			recentMeanMs:  1010, // huge z (tiny sigma) but only +1%
			recentSamples: 5,
			cfg:           cfg,
			wantAnomalous: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := Detect(tt.baseline, tt.recentMeanMs, tt.recentSamples, tt.cfg)
			if got.Anomalous != tt.wantAnomalous {
				t.Errorf("Anomalous = %v, want %v (score=%.2f baseline=%.1f observed=%.1f)",
					got.Anomalous, tt.wantAnomalous, got.Score, got.BaselineMs, got.ObservedMs)
			}
			if math.IsNaN(got.Score) || math.IsInf(got.Score, 0) {
				t.Errorf("Score must be finite, got %v", got.Score)
			}
		})
	}
}

func TestDetectGradualRamp(t *testing.T) {
	cfg := Config{Sensitivity: 3.5, MinDeltaPct: 20, MinSamples: 3, MinBaseline: 6}
	baseline := []float64{100, 105, 98, 102, 100, 103, 99, 101}

	// Early in a ramp, latency is only slightly elevated: should not fire.
	if v := Detect(baseline, 115, 5, cfg); v.Anomalous {
		t.Errorf("early ramp should not fire, got score %.2f", v.Score)
	}
	// Once the ramp is sustained and well above baseline, it fires.
	if v := Detect(baseline, 350, 5, cfg); !v.Anomalous {
		t.Errorf("sustained ramp should fire, got score %.2f", v.Score)
	}
}

func TestDetectRecovery(t *testing.T) {
	cfg := Config{Sensitivity: 3.5, MinDeltaPct: 20, MinSamples: 3, MinBaseline: 6}
	baseline := []float64{100, 102, 98, 101, 99, 100, 103, 97}

	// Was anomalous...
	if v := Detect(baseline, 400, 5, cfg); !v.Anomalous {
		t.Fatalf("expected anomalous during incident")
	}
	// ...then recovers: not anomalous, which the caller uses to resolve.
	if v := Detect(baseline, 100, 5, cfg); v.Anomalous {
		t.Fatalf("expected recovery (not anomalous), got score %.2f", v.Score)
	}
}

func TestMedianOf(t *testing.T) {
	cases := []struct {
		in   []float64
		want float64
	}{
		{nil, 0},
		{[]float64{5}, 5},
		{[]float64{3, 1, 2}, 2},
		{[]float64{4, 1, 3, 2}, 2.5},
	}
	for _, c := range cases {
		if got := medianOf(c.in); got != c.want {
			t.Errorf("medianOf(%v) = %v, want %v", c.in, got, c.want)
		}
	}
}
