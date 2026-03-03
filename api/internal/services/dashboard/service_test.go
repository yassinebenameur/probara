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
	got := normalizeOverviewParams(&models.DashboardOverviewQuery{
		Range:         models.DashboardRange30d,
		FailuresLimit: 25,
		AlertsLimit:   50,
	})

	if got.Range != models.DashboardRange30d {
		t.Fatalf("Range = %s, want %s", got.Range, models.DashboardRange30d)
	}
	if got.FailuresLimit != 25 {
		t.Fatalf("FailuresLimit = %d, want 25", got.FailuresLimit)
	}
	if got.AlertsLimit != 50 {
		t.Fatalf("AlertsLimit = %d, want 50", got.AlertsLimit)
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
