package analytics

import (
	"testing"
	"time"
)

func findSeriesPoint(t *testing.T, series []SeriesPoint, bucketStart time.Time) SeriesPoint {
	t.Helper()
	for _, point := range series {
		if point.BucketStart.Equal(bucketStart) {
			return point
		}
	}
	t.Fatalf("series point for %v not found", bucketStart)
	return SeriesPoint{}
}
