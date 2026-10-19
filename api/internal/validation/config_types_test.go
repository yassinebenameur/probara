package validation

import "testing"

func TestEveryValidatorNamesItsConfigType(t *testing.T) {
	for _, monitorType := range DefaultRegistry.Types() {
		typ, ok := DefaultRegistry.ConfigType(monitorType)
		if !ok || typ == nil {
			t.Errorf("validator for %q does not implement ConfigTyped", monitorType)
		}
	}
}
