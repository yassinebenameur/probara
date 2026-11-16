package opsgenie

import (
	"encoding/json"
	"testing"

	"github.com/yassinebenameur/probara/shared/notifications/plugin"
	"github.com/yassinebenameur/probara/shared/notifications/plugin/builtin/internal/plugintest"
)

// People acknowledge in Opsgenie itself, so its alerts never carry the
// acknowledge link, even when the view has one.
func TestNoAckLink(t *testing.T) {
	plugintest.CheckNoAckLink(t, nil, func(t *testing.T, req plugin.DispatchRequest) string {
		raw, err := json.Marshal(buildCreate(Alias(req.Event.Alert.ID, 0), "P1", req.View()))
		if err != nil {
			t.Fatal(err)
		}
		return string(raw)
	})
}
