package actionlink

import (
	"testing"

	"github.com/yassinebenameur/probara/shared/notifications/present"
)

// The signer is the present.LinkSigner the plugin runtime carries, and the
// action present asks for is the one it mints.
func TestActionAcknowledgeMatchesPresent(t *testing.T) {
	if ActionAcknowledge != present.ActionAcknowledge {
		t.Fatalf("actionlink.ActionAcknowledge = %q, present.ActionAcknowledge = %q", ActionAcknowledge, present.ActionAcknowledge)
	}
	var _ present.LinkSigner = (*Signer)(nil)
}
