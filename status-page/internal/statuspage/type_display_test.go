package statuspage

import (
	"testing"
	"unicode/utf8"

	"github.com/yassinebenameur/probara/shared/models"
)

// Adding a monitor type to shared/models without naming it here would fall
// back to an upper-cased type string on public pages.
func TestTypeLabelAndBadgeCoverEveryMonitorType(t *testing.T) {
	for _, monitorType := range models.AllMonitorTypes {
		if _, ok := typeLabels[monitorType]; !ok {
			t.Errorf("typeLabels has no entry for %q", monitorType)
		}
		badge, ok := typeBadges[monitorType]
		if !ok {
			t.Errorf("typeBadges has no entry for %q", monitorType)
		}
		if n := utf8.RuneCountInString(badge); n > 5 {
			t.Errorf("badge %q for %q is %d characters, want at most 5", badge, monitorType, n)
		}
	}
	if len(typeLabels) != len(models.AllMonitorTypes) || len(typeBadges) != len(models.AllMonitorTypes) {
		t.Errorf("typeLabels has %d entries and typeBadges %d, want %d each (no stale types)",
			len(typeLabels), len(typeBadges), len(models.AllMonitorTypes))
	}
}
