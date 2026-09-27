package secrets

import (
	"encoding/json"
	"testing"
)

func TestLegacyPushConfigCredentialProtection(t *testing.T) {
	legacy := json.RawMessage(`{"push_token":"legacy-webhook-credential","grace_period_seconds":30}`)
	enc := monitorTestEncryptor(t)
	stored, err := EncryptMonitorConfig(enc, "push", legacy)
	if err != nil {
		t.Fatal(err)
	}
	if !LooksLikeEnvelope(configMap(t, stored)["push_token"].(string)) {
		t.Fatal("push config credential was not encrypted")
	}
	for _, raw := range []json.RawMessage{legacy, stored} {
		masked, err := MaskMonitorConfig("push", raw)
		if err != nil {
			t.Fatal(err)
		}
		fields := configMap(t, masked)
		if fields["push_token"] != MaskedSecret {
			t.Fatal("push config credential was not masked")
		}
		if fields["grace_period_seconds"] != float64(30) {
			t.Fatal("masking changed non-secret config")
		}
	}
}
