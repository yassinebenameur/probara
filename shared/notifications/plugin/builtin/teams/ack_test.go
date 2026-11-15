package teams

import (
	"encoding/json"
	"testing"

	"github.com/yassinebenameur/probara/shared/notifications/plugin"
	"github.com/yassinebenameur/probara/shared/notifications/plugin/builtin/internal/plugintest"
)

// Both card formats carry the acknowledge link as an open-URL action.
func TestAckLink(t *testing.T) {
	for name, build := range map[string]func(plugin.DispatchRequest) any{
		"adaptive card": func(r plugin.DispatchRequest) any { return buildAdaptiveCardMessage(r.View()) },
		"message card":  func(r plugin.DispatchRequest) any { return buildMessageCard(r.View()) },
	} {
		t.Run(name, func(t *testing.T) {
			plugintest.CheckAckLink(t, nil, func(t *testing.T, req plugin.DispatchRequest) string {
				raw, err := json.Marshal(build(req))
				if err != nil {
					t.Fatal(err)
				}
				return string(raw)
			})
		})
	}
}
