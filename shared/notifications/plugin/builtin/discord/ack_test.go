package discord

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/yassinebenameur/probara/shared/notifications/plugin/builtin/internal/plugintest"
)

func TestAckLinkRendered(t *testing.T) {
	m := plugintest.AckView()
	raw, err := json.Marshal(buildEmbed(m))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), plugintest.AckURL) {
		t.Fatalf("payload has no acknowledge link: %s", raw)
	}
	if !strings.Contains(string(raw), m.ActionURL) {
		t.Fatalf("payload lost the deep link: %s", raw)
	}
	// Without an ack link the payload carries no trace of one.
	m.AckURL, m.AckLabel = "", ""
	raw, _ = json.Marshal(buildEmbed(m))
	if strings.Contains(string(raw), "alert-action") || strings.Contains(string(raw), "Acknowledge") {
		t.Fatalf("payload mentions an ack link it was not given: %s", raw)
	}
}
