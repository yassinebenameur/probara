package ntfy

import (
	"encoding/json"
	"testing"

	"github.com/yassinebenameur/probara/shared/notifications/plugin"
	"github.com/yassinebenameur/probara/shared/notifications/plugin/builtin/internal/plugintest"
)

// The acknowledge link rides the shared push link list, so it reaches ntfy
// as the first action button and never as text the body budget could cut.
func TestAckLink(t *testing.T) {
	plugintest.CheckAckLink(t, nil, func(t *testing.T, req plugin.DispatchRequest) string {
		out, err := json.Marshal(build("alerts", 4, req.View()))
		if err != nil {
			t.Fatal(err)
		}
		return string(out)
	})
}
