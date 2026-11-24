package gotify

import (
	"encoding/json"
	"testing"

	"github.com/yassinebenameur/probara/shared/notifications/plugin"
	"github.com/yassinebenameur/probara/shared/notifications/plugin/builtin/internal/plugintest"
)

// The acknowledge link rides the shared push link list, so it is one of the
// lines under the body, which the body budget leaves room for.
func TestAckLink(t *testing.T) {
	plugintest.CheckAckLink(t, nil, func(t *testing.T, req plugin.DispatchRequest) string {
		out, err := json.Marshal(build(8, req.View()))
		if err != nil {
			t.Fatal(err)
		}
		return string(out)
	})
}
