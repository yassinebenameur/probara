package statuspage

import (
	"strings"
	"testing"
)

func TestValidatePushEndpoint_AcceptsRealPushServices(t *testing.T) {
	valid := []string{
		"https://fcm.googleapis.com/fcm/send/dGhpcy1pcy1hLXRva2Vu",
		"https://updates.push.services.mozilla.com/wpush/v2/gAAAAABk",
		"https://db5p.notify.windows.com/w/?token=BQYAAABsomethinglong",
		"https://web.push.apple.com/QF1VqDXm-abcdefghijklmnop",
	}
	for _, endpoint := range valid {
		if err := validatePushEndpoint(endpoint, defaultPushEndpointHosts); err != nil {
			t.Errorf("validatePushEndpoint(%q) = %v, want nil", endpoint, err)
		}
	}
}

// The allowlist is the control that stops the subscriptions table becoming a
// stored-SSRF and spam-relay primitive: the sender POSTs to whatever was
// stored, from inside the cluster, unattended.
func TestValidatePushEndpoint_RejectsSSRFAndSpamTargets(t *testing.T) {
	tests := []struct {
		name     string
		endpoint string
	}{
		{"plaintext scheme", "http://fcm.googleapis.com/fcm/send/abcdefghijk"},
		{"file scheme", "file:///etc/passwd/aaaaaaaaaaaaaaaaa"},
		{"gopher scheme", "gopher://fcm.googleapis.com/aaaaaaaaaaaaaaaa"},
		{"loopback", "https://127.0.0.1/fcm/send/aaaaaaaaaaaaaaaaaaaa"},
		{"localhost", "https://localhost/fcm/send/aaaaaaaaaaaaaaaaaaaa"},
		{"link local metadata", "https://169.254.169.254/latest/meta-data/aaaa"},
		{"private range", "https://10.0.0.5/internal/aaaaaaaaaaaaaaaaaaaaa"},
		{"cluster dns", "https://api.default.svc.cluster.local/aaaaaaaaaa"},
		{"arbitrary host", "https://evil.example.com/collect/aaaaaaaaaaaaaa"},
		// The two suffix-matching traps: a bare HasSuffix check would accept
		// both of these.
		{"suffix without dot boundary", "https://evil-fcm.googleapis.com/x/aaaaaaaaaaaa"},
		{"allowed host as a prefix", "https://fcm.googleapis.com.evil.com/x/aaaaaaaa"},
		{"credentials in url", "https://user:pass@fcm.googleapis.com/fcm/send/aaaa"},
		{"empty", ""},
		{"too short", "https://fcm.gl/a"},
		{"too long", "https://fcm.googleapis.com/fcm/send/" + strings.Repeat("a", 2100)},
		{"not a url", "https://exa mple.com/aaaaaaaaaaaaaaaaaaaaaaaaaa"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if err := validatePushEndpoint(tt.endpoint, defaultPushEndpointHosts); err == nil {
				t.Fatalf("validatePushEndpoint(%q) = nil, want an error", tt.endpoint)
			}
		})
	}
}

func TestHostAllowed_MatchesOnDotBoundary(t *testing.T) {
	allowed := []string{"push.services.mozilla.com"}

	cases := map[string]bool{
		"push.services.mozilla.com":          true,  // exact
		"updates.push.services.mozilla.com":  true,  // subdomain
		"a.b.push.services.mozilla.com":      true,  // deeper subdomain
		"PUSH.SERVICES.MOZILLA.COM":          true,  // case-insensitive
		"push.services.mozilla.com.":         true,  // trailing root dot
		"evilpush.services.mozilla.com":      false, // no dot boundary
		"push.services.mozilla.com.evil.com": false, // allowed host as prefix
		"services.mozilla.com":               false, // parent is not the suffix
		"":                                   false,
	}

	for host, want := range cases {
		if got := hostAllowed(host, allowed); got != want {
			t.Errorf("hostAllowed(%q) = %v, want %v", host, got, want)
		}
	}
}

func TestHostAllowed_WildcardOptsOut(t *testing.T) {
	// "*" is the documented escape hatch for a self-hosted push service. It
	// re-opens the SSRF surface, so it must be explicit and total rather than
	// something a partial config can trigger accidentally.
	if !hostAllowed("anything.internal", []string{"*"}) {
		t.Fatalf("wildcard allowlist rejected a host")
	}
	if hostAllowed("anything.internal", []string{"fcm.googleapis.com"}) {
		t.Fatalf("non-wildcard allowlist accepted an arbitrary host")
	}
}

func TestPushEndpointHosts_FallsBackToDefaults(t *testing.T) {
	// An empty or all-whitespace override must not silently produce an empty
	// allowlist, which would reject every real subscription.
	for _, raw := range []string{"", "   ", ",, ,"} {
		t.Setenv(pushEndpointAllowlistEnvVar, raw)
		if got := pushEndpointHosts(); len(got) != len(defaultPushEndpointHosts) {
			t.Fatalf("pushEndpointHosts() with %q = %v, want the defaults", raw, got)
		}
	}
}

func TestPushEndpointHosts_ParsesOverride(t *testing.T) {
	t.Setenv(pushEndpointAllowlistEnvVar, " push.internal.example , FCM.GOOGLEAPIS.COM ")
	got := pushEndpointHosts()

	want := []string{"push.internal.example", "fcm.googleapis.com"}
	if len(got) != len(want) {
		t.Fatalf("pushEndpointHosts() = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("pushEndpointHosts()[%d] = %q, want %q", i, got[i], want[i])
		}
	}
	if err := validatePushEndpoint("https://push.internal.example/x/aaaaaaaaaaaaaaa", got); err != nil {
		t.Fatalf("overridden host rejected: %v", err)
	}
}
