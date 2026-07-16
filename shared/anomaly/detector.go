// Package anomaly implements statistical latency-anomaly detection. It is a
// pure, dependency-free engine: callers supply a baseline (historical hourly
// average latencies) and a recent observation, and Detect reports whether the
// recent latency is anomalously high. No DB, clock, or IO — so it is trivially
// unit-testable and reusable by the alerter and tests alike.
package anomaly

import (
	"math"
	"sort"
)

// madScale converts the median absolute deviation into a robust estimate of the
// standard deviation for a normally distributed sample (1 / Φ⁻¹(3/4)).
const madScale = 1.4826

// DefaultMinSamples is the floor on recent successful checks required before a
// verdict can fire, guarding against a single slow check tripping an alert.
const DefaultMinSamples = 3

// Config tunes the detector. Zero values are not meaningful; callers should
// populate every field (the alerter sources them from the alert policy).
type Config struct {
	// Sensitivity is the robust z-score threshold (in sigmas) above which the
	// recent latency is considered anomalous. Larger = less sensitive.
	Sensitivity float64
	// MinDeltaPct is the minimum percentage by which the recent latency must
	// exceed the baseline median, independent of the z-score. Prevents tiny
	// absolute jumps on very stable, fast monitors from firing.
	MinDeltaPct float64
	// MinSamples is the minimum number of recent successful checks required to
	// form a verdict. If <= 0, DefaultMinSamples is used.
	MinSamples int
	// MinBaseline is the minimum number of baseline points required before the
	// detector will ever fire. If <= 0, a value of 6 is used. Protects against
	// firing on monitors with too little history.
	MinBaseline int
}

// Verdict is the result of a detection pass.
type Verdict struct {
	// Anomalous is true when the recent latency is judged degraded.
	Anomalous bool
	// Score is the robust z-score of the observation. NaN/Inf are normalised to
	// 0 so it is always safe to persist.
	Score float64
	// BaselineMs is the baseline median latency the observation was compared to.
	BaselineMs float64
	// ObservedMs is the recent mean latency that was evaluated.
	ObservedMs float64
}

// Detect compares recentMeanMs (the mean latency over the recent window, in ms)
// and recentSamples (count of successful checks in that window) against a
// baseline of historical hourly average latencies. It returns a Verdict.
//
// The decision is a triple gate: enough samples, robust z-score over the
// sensitivity threshold, AND the observation exceeds the baseline median by at
// least MinDeltaPct. When the baseline dispersion (MAD) is ~0 — an extremely
// stable monitor where any z-score would be infinite — it falls back to the
// percentage gate alone.
func Detect(baseline []float64, recentMeanMs float64, recentSamples int, cfg Config) Verdict {
	minSamples := cfg.MinSamples
	if minSamples <= 0 {
		minSamples = DefaultMinSamples
	}
	minBaseline := cfg.MinBaseline
	if minBaseline <= 0 {
		minBaseline = 6
	}

	median := medianOf(baseline)
	v := Verdict{BaselineMs: median, ObservedMs: recentMeanMs}

	// Cold or thin baseline, or too few recent samples: never fire.
	if len(baseline) < minBaseline || recentSamples < minSamples || median <= 0 {
		return v
	}

	// Robust sigma via median absolute deviation.
	deviations := make([]float64, len(baseline))
	for i, x := range baseline {
		deviations[i] = math.Abs(x - median)
	}
	sigma := madScale * medianOf(deviations)

	z := 0.0
	if sigma > 0 {
		z = (recentMeanMs - median) / sigma
	}
	if math.IsNaN(z) || math.IsInf(z, 0) {
		z = 0
	}
	v.Score = z

	deltaGate := recentMeanMs >= median*(1+cfg.MinDeltaPct/100)

	if sigma <= 0 {
		// No measurable dispersion: the z-score is meaningless, so rely solely
		// on the percentage gate.
		v.Anomalous = deltaGate
		return v
	}

	v.Anomalous = z >= cfg.Sensitivity && deltaGate
	return v
}

// medianOf returns the median of values, or 0 for an empty slice. It copies the
// input so the caller's slice ordering is preserved.
func medianOf(values []float64) float64 {
	n := len(values)
	if n == 0 {
		return 0
	}
	sorted := make([]float64, n)
	copy(sorted, values)
	sort.Float64s(sorted)
	mid := n / 2
	if n%2 == 1 {
		return sorted[mid]
	}
	return (sorted[mid-1] + sorted[mid]) / 2
}
