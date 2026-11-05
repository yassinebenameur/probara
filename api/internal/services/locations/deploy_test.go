package locations

import (
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"strings"
	"testing"

	"github.com/yassinebenameur/probara/shared/buildinfo"
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

// A wss:// base (the broker's websocket listener behind an ingress on 443)
// keeps its host-less default port and any ingress path; the worker's NATS
// client sends that path in the upgrade request.
func TestLocationWorkerNATSURLWebsocket(t *testing.T) {
	for base, want := range map[string]string{
		"wss://nats.example.test":          "wss://location-id:cred@nats.example.test",
		"wss://probara.example.test/nats":  "wss://location-id:cred@probara.example.test/nats",
		"wss://nats.example.test:8443/ws/": "wss://location-id:cred@nats.example.test:8443/ws/",
	} {
		got, err := locationWorkerNATSURL(base, "location-id", "cred")
		if err != nil {
			t.Fatalf("%s: %v", base, err)
		}
		if got != want {
			t.Fatalf("locationWorkerNATSURL(%q) = %q, want %q", base, got, want)
		}
	}
}

func TestNATSTransportNote(t *testing.T) {
	if note := natsTransportNote("wss://id:cred@nats.example.test"); !strings.Contains(note, "only outbound HTTPS") {
		t.Fatalf("wss note = %q", note)
	}
	if note := natsTransportNote("tls://id:cred@nats.example.test:4222"); !strings.Contains(note, "TCP port") || !strings.Contains(note, "wss://") {
		t.Fatalf("tls note = %q", note)
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

func TestDeployWorkerImage(t *testing.T) {
	orig := buildinfo.Version
	t.Cleanup(func() { buildinfo.Version = orig })

	for _, tc := range []struct{ version, want string }{
		{"2.0.0-alpha.10", "ghcr.io/yassinebenameur/probara-worker:2.0.0-alpha.10"},
		{"2.1.0", "ghcr.io/yassinebenameur/probara-worker:2.1.0"},
		{"v2.1.0", "ghcr.io/yassinebenameur/probara-worker:2.1.0"},
		{"dev", "ghcr.io/yassinebenameur/probara-worker:alpha"},
		{"main", "ghcr.io/yassinebenameur/probara-worker:alpha"},
	} {
		buildinfo.Version = tc.version
		if got := (&Service{}).deployWorkerImage(); got != tc.want {
			t.Errorf("version %q: image %q, want %q", tc.version, got, tc.want)
		}
		if strings.HasSuffix(DefaultWorkerImage(), ":latest") {
			t.Errorf("version %q: default image must never be :latest", tc.version)
		}
	}

	svc := &Service{}
	svc.ConfigureWorkerImage("  registry.example/probara-worker:2.0.0 ")
	if got := svc.deployWorkerImage(); got != "registry.example/probara-worker:2.0.0" {
		t.Errorf("configured image = %q", got)
	}
	svc.ConfigureWorkerImage("")
	if got := svc.deployWorkerImage(); got != DefaultWorkerImage() {
		t.Errorf("empty LOCATION_WORKER_IMAGE = %q, want the default", got)
	}
}
