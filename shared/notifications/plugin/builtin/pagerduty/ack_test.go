package pagerduty

import (
	"encoding/json"
	"testing"

	"github.com/yassinebenameur/probara/shared/notifications/plugin"
	"github.com/yassinebenameur/probara/shared/notifications/plugin/builtin/internal/plugintest"
)

// People acknowledge in PagerDuty itself, so its events never carry the
// acknowledge link, even when the view has one.
func TestNoAckLink(t *testing.T) {
	plugintest.CheckNoAckLink(t, nil, func(t *testing.T, req plugin.DispatchRequest) string {
		raw, err := json.Marshal(buildEvent("key", "probara:"+req.Event.Alert.ID, "trigger", req.View()))
		if err != nil {
			t.Fatal(err)
		}
		return string(raw)
	})
}
