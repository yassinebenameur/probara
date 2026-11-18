package discord

import (
	"encoding/json"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/yassinebenameur/probara/shared/notifications/plugin"
	"github.com/yassinebenameur/probara/shared/notifications/plugin/builtin/internal/plugintest"
)

// The acknowledge link is a masked link in the embed description.
func TestAckLink(t *testing.T) {
	plugintest.CheckAckLink(t, nil, func(t *testing.T, req plugin.DispatchRequest) string {
		raw, err := json.Marshal(buildEmbed(req.View()))
		if err != nil {
			t.Fatal(err)
		}
		return string(raw)
	})
}

// An oversized summary is cut, never the acknowledge link.
func TestAckLinkSurvivesALongSummary(t *testing.T) {
	m := plugin.DispatchRequest{Event: plugintest.Event("created")}.View()
	m.Summary = strings.Repeat("s", 5000)
	m.AckURL, m.AckLabel = plugintest.AckURL, "Acknowledge"
	d := buildEmbed(m).Embeds[0].Description
	if n := utf8.RuneCountInString(d); n > maxDescription {
		t.Fatalf("description is %d runes, over %d", n, maxDescription)
	}
	if !strings.HasSuffix(d, "("+plugintest.AckURL+")**") {
		t.Fatalf("lost the whole ack link: ...%s", d[len(d)-200:])
	}
}
