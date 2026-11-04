package validation

import (
	"sort"
	"testing"

	"github.com/yassinebenameur/probara/api/internal/models"
	sharedmodels "github.com/yassinebenameur/probara/shared/models"
)

// TestRegistryTypesArePartitionedActiveOrPassive pins every registered monitor
// type to exactly one of activeCheckTypes and shared/models.PassiveMonitorTypes.
// Since migration 000099 the database no longer lists monitor types (the
// monitors_timeout_check constraint only names the passive ones), so a type
// registered without an activeCheckTypes entry would be accepted with no
// timeout validation at all. This test is the guard that list used to be.
func TestRegistryTypesArePartitionedActiveOrPassive(t *testing.T) {
	registered := map[models.MonitorType]bool{}
	for _, mt := range DefaultRegistry.Types() {
		registered[mt] = true
	}

	passive := map[models.MonitorType]bool{}
	for _, p := range sharedmodels.PassiveMonitorTypes {
		passive[models.MonitorType(p)] = true
	}

	var problems []string
	for mt := range registered {
		isActive, isPassive := activeCheckTypes[mt], passive[mt]
		switch {
		case isActive && isPassive:
			problems = append(problems, string(mt)+": in both activeCheckTypes and PassiveMonitorTypes")
		case !isActive && !isPassive:
			problems = append(problems, string(mt)+": registered but in neither activeCheckTypes nor PassiveMonitorTypes")
		}
	}
	for mt := range activeCheckTypes {
		if !registered[mt] {
			problems = append(problems, string(mt)+": in activeCheckTypes but not registered")
		}
	}
	for mt := range passive {
		if !registered[mt] {
			problems = append(problems, string(mt)+": in PassiveMonitorTypes but not registered")
		}
	}

	sort.Strings(problems)
	for _, p := range problems {
		t.Error(p)
	}
}
