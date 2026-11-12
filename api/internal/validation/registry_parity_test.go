package validation

import (
	"sort"
	"testing"

	sharedmodels "github.com/yassinebenameur/probara/shared/models"
)

// The validator registry is the API's list of creatable monitor types; it
// must be exactly shared/models.AllMonitorTypes, the list the worker and the
// status-page mirrors are pinned to.
func TestDefaultRegistryMatchesAllMonitorTypes(t *testing.T) {
	var registered, want []string
	for _, monitorType := range DefaultRegistry.Types() {
		registered = append(registered, string(monitorType))
	}
	for _, monitorType := range sharedmodels.AllMonitorTypes {
		want = append(want, string(monitorType))
	}
	sort.Strings(registered)
	sort.Strings(want)
	if len(registered) != len(want) {
		t.Fatalf("registry has %d types %v, shared/models has %d %v", len(registered), registered, len(want), want)
	}
	for i := range want {
		if registered[i] != want[i] {
			t.Fatalf("registry types %v differ from shared/models %v", registered, want)
		}
	}
}
