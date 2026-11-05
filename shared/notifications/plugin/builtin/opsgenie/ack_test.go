package opsgenie

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/yassinebenameur/probara/shared/notifications/plugin/builtin/internal/plugintest"
)

// Paging providers own acknowledgement themselves (and Probara syncs its
// own acks to them), so their alerts never carry the magic link.
func TestNoAckLink(t *testing.T) {
	raw, _ := json.Marshal(buildCreate("alias", "P1", plugintest.AckView()))
	if strings.Contains(string(raw), "alert-action") {
		t.Fatalf("opsgenie alert carries the ack link: %s", raw)
	}
}
