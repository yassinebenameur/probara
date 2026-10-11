package builtin

import (
	"testing"

	"github.com/yassinebenameur/probara/shared/notifications/plugin"
)

// The alerter targets suspected-cause follow-ups by CapabilityFollowUp. Paging
// tools must stay off it (their APIs add content by re-triggering); every
// other first-party channel renders the follow-up from present.
func TestFollowUpCapability(t *testing.T) {
	want := map[string]bool{
		"discord": true, "email": true, "slack": true, "teams": true,
		"telegram": true, "twilio_sms": true, "generic_webhook": true,
		"pagerduty": false, "opsgenie": false,
	}
	for typ, follow := range want {
		p, ok := plugin.DefaultRegistry.Get(typ)
		if !ok {
			t.Fatalf("plugin %q not registered", typ)
		}
		if got := p.Manifest().HasCapability(plugin.CapabilityFollowUp); got != follow {
			t.Errorf("%s: CapabilityFollowUp = %v, want %v", typ, got, follow)
		}
	}
	if n := len(plugin.DefaultRegistry.All()); n != len(want) {
		t.Fatalf("%d plugins registered, want %d: add the new plugin to this table", n, len(want))
	}
}
