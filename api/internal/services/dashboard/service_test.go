package dashboard

import (
	"testing"
	"time"

	"github.com/yassinebenameur/probara/api/internal/models"
)

func TestNormalizeOverviewParams_Defaults(t *testing.T) {
	got := normalizeOverviewParams(nil)

	if got.Range != models.DashboardRange24h {
		t.Fatalf("Range = %s, want %s", got.Range, models.DashboardRange24h)
	}
	if got.FailuresLimit != defaultFailuresLimit {
		t.Fatalf("FailuresLimit = %d, want %d", got.FailuresLimit, defaultFailuresLimit)
	}
	if got.AlertsLimit != defaultAlertsLimit {
		t.Fatalf("AlertsLimit = %d, want %d", got.AlertsLimit, defaultAlertsLimit)
	}
}

func TestNormalizeOverviewParams_ClampsAndFallbacks(t *testing.T) {
	got := normalizeOverviewParams(&models.DashboardOverviewQuery{
		Range:         models.DashboardRange("bad"),
		FailuresLimit: 500,
		AlertsLimit:   -5,
	})

	if got.Range != models.DashboardRange24h {
		t.Fatalf("Range = %s, want %s", got.Range, models.DashboardRange24h)
	}
	if got.FailuresLimit != maxListLimit {
		t.Fatalf("FailuresLimit = %d, want %d", got.FailuresLimit, maxListLimit)
	}
	if got.AlertsLimit != defaultAlertsLimit {
		t.Fatalf("AlertsLimit = %d, want %d", got.AlertsLimit, defaultAlertsLimit)
	}
}

func TestNormalizeOverviewParams_ValidValues(t *testing.T) {
	oneHour := models.DashboardRange("1h")
	oneHourParams := normalizeOverviewParams(&models.DashboardOverviewQuery{
		Range: oneHour,
	})
	if oneHourParams.Range != oneHour {
		t.Fatalf("Range = %s, want %s", oneHourParams.Range, oneHour)
	}

	got := normalizeOverviewParams(&models.DashboardOverviewQuery{
		Range:         models.DashboardRange365d,
		FailuresLimit: 25,
		AlertsLimit:   50,
	})

	if got.Range != models.DashboardRange365d {
		t.Fatalf("Range = %s, want %s", got.Range, models.DashboardRange365d)
	}
	if got.FailuresLimit != 25 {
		t.Fatalf("FailuresLimit = %d, want 25", got.FailuresLimit)
	}
	if got.AlertsLimit != 50 {
		t.Fatalf("AlertsLimit = %d, want 50", got.AlertsLimit)
	}
}

func TestRangeBounds_OneHour(t *testing.T) {
	start, end, bucket, err := rangeBounds(models.DashboardRange("1h"))
	if err != nil {
		t.Fatalf("rangeBounds(1h) error = %v", err)
	}
	if !start.Before(end) {
		t.Fatalf("rangeBounds(1h) start must be before end")
	}
	if bucket != 5*time.Minute {
		t.Fatalf("rangeBounds(1h) bucket = %v, want 5m", bucket)
	}
	if got := end.Sub(start); got != 55*time.Minute {
		t.Fatalf("rangeBounds(1h) display span = %v, want 55m", got)
	}
}

func TestNormalizeOverviewParams_NormalizesTags(t *testing.T) {
	got := normalizeOverviewParams(&models.DashboardOverviewQuery{
		Tags: []string{" prod ", "", "api", "prod", "backend"},
	})

	want := []string{"api", "backend", "prod"}
	if len(got.Tags) != len(want) {
		t.Fatalf("Tags length = %d, want %d", len(got.Tags), len(want))
	}
	for i := range want {
		if got.Tags[i] != want[i] {
			t.Fatalf("Tags[%d] = %q, want %q", i, got.Tags[i], want[i])
		}
	}
}

func TestRangeBounds_LongRanges(t *testing.T) {
	for _, rangeValue := range []models.DashboardRange{
		models.DashboardRange90d,
		models.DashboardRange365d,
	} {
		start, end, bucket, err := rangeBounds(rangeValue)
		if err != nil {
			t.Fatalf("rangeBounds(%s) error = %v", rangeValue, err)
		}
		if !start.Before(end) {
			t.Fatalf("rangeBounds(%s) start must be before end", rangeValue)
		}
		if bucket != 24*time.Hour {
			t.Fatalf("rangeBounds(%s) bucket = %v, want 24h", rangeValue, bucket)
		}
	}
}

func TestResolveFailureState(t *testing.T) {
	if got := resolveFailureState(nil); got != models.DashboardFailureStateFiring {
		t.Fatalf("resolveFailureState(nil) = %s, want %s", got, models.DashboardFailureStateFiring)
	}

	now := time.Now().UTC()
	if got := resolveFailureState(&now); got != models.DashboardFailureStateResolved {
		t.Fatalf("resolveFailureState(non-nil) = %s, want %s", got, models.DashboardFailureStateResolved)
	}
}

func TestNormalizeListParams_Defaults(t *testing.T) {
	got := normalizeListParams(nil, problemMonitorLimit)

	if got.Range != models.DashboardRange24h {
		t.Fatalf("Range = %s, want %s", got.Range, models.DashboardRange24h)
	}
	if got.Limit != problemMonitorLimit {
		t.Fatalf("Limit = %d, want %d", got.Limit, problemMonitorLimit)
	}
}

func TestNormalizeListParams_ClampsAndNormalizesTags(t *testing.T) {
	got := normalizeListParams(&models.DashboardListQuery{
		Range: models.DashboardRange365d,
		Limit: 500,
		Tags:  []string{" prod ", "", "api", "prod"},
	}, defaultFailuresLimit)

	if got.Range != models.DashboardRange365d {
		t.Fatalf("Range = %s, want %s", got.Range, models.DashboardRange365d)
	}
	if got.Limit != maxListLimit {
		t.Fatalf("Limit = %d, want %d", got.Limit, maxListLimit)
	}
	wantTags := []string{"api", "prod"}
	if len(got.Tags) != len(wantTags) {
		t.Fatalf("Tags length = %d, want %d", len(got.Tags), len(wantTags))
	}
	for i := range wantTags {
		if got.Tags[i] != wantTags[i] {
			t.Fatalf("Tags[%d] = %q, want %q", i, got.Tags[i], wantTags[i])
		}
	}
}
