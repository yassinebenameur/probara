package worker

import (
	"sort"
	"testing"

	"github.com/yassinebenameur/probara/shared/models"
)

// Every active monitor type needs a checker, no passive type may have one,
// and the mesh probe is the only job type that is not a monitor type.
func TestDefaultRegistryCoversActiveMonitorTypes(t *testing.T) {
	want := []string{models.MonitorTypeMeshProbe}
	for _, monitorType := range models.AllMonitorTypes {
		if !models.IsPassiveMonitorType(string(monitorType)) {
			want = append(want, string(monitorType))
		}
	}
	got := NewDefaultRegistry(1024, false, nil, t.TempDir()).Types()
	sort.Strings(want)
	sort.Strings(got)
	if len(got) != len(want) {
		t.Fatalf("worker registry has %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("worker registry has %v, want %v", got, want)
		}
	}
}
