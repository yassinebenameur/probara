package pagerduty

import (
	"encoding/json"
	"testing"

	"github.com/yassinebenameur/probara/shared/notifications/plugin"
	"github.com/yassinebenameur/probara/shared/notifications/plugin/builtin/internal/plugintest"
)

// The trigger lists the acknowledge link among its links; acknowledging
// there syncs back to the incident like any Probara acknowledgement.
func TestAckLink(t *testing.T) {
	plugintest.CheckAckLink(t, nil, func(t *testing.T, req plugin.DispatchRequest) string {
		raw, err := json.Marshal(buildEvent("key", "probara:"+req.Event.Alert.ID, "trigger", req.View()))
		if err != nil {
			t.Fatal(err)
		}
		return string(raw)
	})
}
