package opsgenie

import (
	"encoding/json"
	"testing"

	"github.com/yassinebenameur/probara/shared/notifications/plugin"
	"github.com/yassinebenameur/probara/shared/notifications/plugin/builtin/internal/plugintest"
)

// The create request carries the acknowledge link in its details and
// description.
func TestAckLink(t *testing.T) {
	plugintest.CheckAckLink(t, nil, func(t *testing.T, req plugin.DispatchRequest) string {
		raw, err := json.Marshal(buildCreate(Alias(req.Event.Alert.ID), "P1", req.View()))
		if err != nil {
			t.Fatal(err)
		}
		return string(raw)
	})
}
