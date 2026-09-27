package statuspages

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/yassinebenameur/probara/api/internal/models"
)

// A setting present in the model but missing from applyPatch/toAPI is
// accepted by the endpoint, silently dropped on write, and never reaches the
// public page -- with no error anywhere. This pins the round trip so the
// three settings mirrors cannot drift apart unnoticed.
func TestStatusPageSettings_PushNotificationsRoundTrip(t *testing.T) {
	enabled := true
	stored := defaultStatusPageSettings().applyPatch(&models.StatusPageSettings{
		EnablePushNotifications: &enabled,
	})
	if !stored.EnablePushNotifications {
		t.Fatalf("applyPatch dropped enable_push_notifications")
	}

	raw, err := json.Marshal(stored)
	if err != nil {
		t.Fatalf("Marshal() error = %v", err)
	}
	if !strings.Contains(string(raw), `"enable_push_notifications":true`) {
		t.Fatalf("serialized settings lost the field: %s", raw)
	}

	reparsed := parseStatusPageSettings(raw)
	if !reparsed.EnablePushNotifications {
		t.Fatalf("parseStatusPageSettings dropped the field on read back")
	}

	api := reparsed.toAPI()
	if api.EnablePushNotifications == nil || !*api.EnablePushNotifications {
		t.Fatalf("toAPI did not surface enable_push_notifications")
	}
}

func TestStatusPageSettings_PushNotificationsDefaultsOff(t *testing.T) {
	if defaultStatusPageSettings().EnablePushNotifications {
		t.Fatalf("push notifications default to on; they must be opt-in")
	}
	if parseStatusPageSettings([]byte(`{"show_footer":false}`)).EnablePushNotifications {
		t.Fatalf("unrelated settings turned push notifications on")
	}
}
