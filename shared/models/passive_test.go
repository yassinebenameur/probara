package models

import "testing"

func TestIsPassiveMonitorType(t *testing.T) {
	for _, typ := range []string{"group", "push", "agent"} {
		if !IsPassiveMonitorType(typ) {
			t.Errorf("%s must be passive", typ)
		}
	}
	for _, typ := range []string{"http", "sip", "synthetic_browser", ""} {
		if IsPassiveMonitorType(typ) {
			t.Errorf("%q must not be passive", typ)
		}
	}
}
