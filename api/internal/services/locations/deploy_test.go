package locations

import (
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"strings"
	"testing"

	"github.com/yassinebenameur/probara/shared/locationauth"
	"github.com/yassinebenameur/probara/shared/secrets"
)

func TestLocationWorkerNATSURL(t *testing.T) {
	got, err := locationWorkerNATSURL("tls://nats.example.test:4222", "location-id", "s:ecret")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(got, "location-id:s%3Aecret@") {
		t.Fatalf("credential was not safely encoded: %s", got)
	}
}

func TestProtectMonitorConfigForCredentialRekeysSecrets(t *testing.T) {
	platformKey := make([]byte, 32)
	if _, err := rand.Read(platformKey); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PROBARA_SECRETS_KEY", base64.StdEncoding.EncodeToString(platformKey))
	provider, err := secrets.NewEnvKeyProvider()
	if err != nil {
		t.Fatal(err)
	}
	platformEncryptor := secrets.NewAESGCMEncryptor(provider)
	stored, err := secrets.EncryptMonitorConfig(platformEncryptor, "postgres", json.RawMessage(`{"connection_string":"postgres://user:secret@example.test/db"}`))
	if err != nil {
		t.Fatal(err)
	}
	credential := base64.RawURLEncoding.EncodeToString(make([]byte, 32))
	svc := &Service{encryptor: platformEncryptor}
	protected, err := svc.protectMonitorConfigForCredential(credential, "postgres", stored)
	if err != nil {
		t.Fatal(err)
	}
	locationEncryptor, err := locationauth.ConfigEncryptor(credential)
	if err != nil {
		t.Fatal(err)
	}
	plaintext, err := secrets.DecryptMonitorConfig(locationEncryptor, "postgres", protected)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(plaintext), "user:secret") {
		t.Fatalf("location envelope did not preserve secret: %s", plaintext)
	}
}

func TestLocationWorkerNATSURLRejectsUnsafeBase(t *testing.T) {
	for _, base := range []string{
		"nats://nats.example.test:4222",
		"tls://shared:secret@nats.example.test:4222",
		"not-a-url",
	} {
		if _, err := locationWorkerNATSURL(base, "location-id", "secret"); err == nil {
			t.Fatalf("expected %q to be rejected", base)
		}
	}
}
