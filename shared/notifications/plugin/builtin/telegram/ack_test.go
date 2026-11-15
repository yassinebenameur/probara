package telegram

import (
	"strings"
	"testing"

	"github.com/yassinebenameur/probara/shared/notifications/plugin"
	"github.com/yassinebenameur/probara/shared/notifications/plugin/builtin/internal/plugintest"
)

func TestAckLink(t *testing.T) {
	plugintest.CheckAckLink(t, nil, func(t *testing.T, req plugin.DispatchRequest) string {
		out := render(req.View())
		assertWellFormed(t, out)
		return out
	})
}

// However long the alert text, the message stays within Telegram's limit and
// keeps the acknowledge link whole: facts and the error block go first.
func TestAckLinkSurvivesTheLengthBudget(t *testing.T) {
	prev := plugin.CurrentRuntime()
	t.Cleanup(func() { plugin.Configure(prev) })
	plugin.Configure(plugin.Runtime{AppBaseURL: plugintest.BaseURL, Links: plugintest.StaticSigner(plugintest.AckToken)})

	ev := plugintest.Event("created")
	long := strings.Repeat("x", 8000)
	ev.Alert.LastError = &long
	ev.Alert.MonitorName = strings.Repeat("very long monitor name ", 40)
	out := render(plugin.DispatchRequest{Channel: plugin.ChannelRef{ID: "5a7d3c1e-9b2f-4e8a-8c6d-0f1e2d3c4b5a"}, Event: ev}.View())
	if runeLen(out) > maxMessage {
		t.Fatalf("message is %d runes, over %d", runeLen(out), maxMessage)
	}
	if !strings.Contains(out, plugintest.AckURL) {
		t.Fatalf("lost the acknowledge link:\n%s", out)
	}
	assertWellFormed(t, out)
}
