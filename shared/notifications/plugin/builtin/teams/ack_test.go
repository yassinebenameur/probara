package teams

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/yassinebenameur/probara/shared/notifications/plugin/builtin/internal/plugintest"
)

func TestAckLinkRendered(t *testing.T) {
	m := plugintest.AckView()
	for name, payload := range map[string]any{
		"message card":  buildMessageCard(m),
		"adaptive card": buildAdaptiveCardMessage(m),
	} {
		raw, _ := json.Marshal(payload)
		if !strings.Contains(string(raw), plugintest.AckURL) || !strings.Contains(string(raw), m.ActionURL) {
			t.Fatalf("%s lacks the ack or deep link: %s", name, raw)
		}
	}
	m.AckURL, m.AckLabel = "", ""
	for name, payload := range map[string]any{
		"message card":  buildMessageCard(m),
		"adaptive card": buildAdaptiveCardMessage(m),
	} {
		if raw, _ := json.Marshal(payload); strings.Contains(string(raw), "alert-action") {
			t.Fatalf("%s mentions an ack link it was not given: %s", name, raw)
		}
	}
}
