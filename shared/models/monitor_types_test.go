package models

import "testing"

func TestAllMonitorTypesAreUniqueAndIncludePassiveTypes(t *testing.T) {
	seen := map[MonitorType]bool{}
	for _, monitorType := range AllMonitorTypes {
		if seen[monitorType] {
			t.Errorf("AllMonitorTypes lists %q twice", monitorType)
		}
		seen[monitorType] = true
	}
	for _, passive := range PassiveMonitorTypes {
		if !seen[MonitorType(passive)] {
			t.Errorf("passive type %q is not in AllMonitorTypes", passive)
		}
	}
	if seen[MonitorType(MonitorTypeMeshProbe)] {
		t.Errorf("%q is a check-job type and must not be a monitor type", MonitorTypeMeshProbe)
	}
}
