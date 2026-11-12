package statustemplate

import (
	"testing"

	"github.com/yassinebenameur/probara/shared/models"
)

func TestTypeIconCoversEveryMonitorType(t *testing.T) {
	for _, monitorType := range models.AllMonitorTypes {
		if _, ok := typeIcons[monitorType]; !ok {
			t.Errorf("typeIcons has no entry for %q", monitorType)
		}
	}
	if len(typeIcons) != len(models.AllMonitorTypes) {
		t.Errorf("typeIcons has %d entries, want %d (no stale types)", len(typeIcons), len(models.AllMonitorTypes))
	}
	if got := TypeIcon("no_such_type"); got != "activity" {
		t.Errorf("TypeIcon(unknown) = %q, want the generic activity icon", got)
	}
}
