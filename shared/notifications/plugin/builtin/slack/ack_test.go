package slack

import (
	"encoding/json"
	"testing"

	"github.com/yassinebenameur/probara/shared/notifications/plugin"
	"github.com/yassinebenameur/probara/shared/notifications/plugin/builtin/internal/plugintest"
)

// The acknowledge link is the first, primary button, beside the deep link.
func TestAckLink(t *testing.T) {
	plugintest.CheckAckLink(t, nil, func(t *testing.T, req plugin.DispatchRequest) string {
		raw, err := json.Marshal(buildBlockKit(req.View()))
		if err != nil {
			t.Fatal(err)
		}
		return string(raw)
	})
}
