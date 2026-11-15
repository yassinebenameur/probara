// Package plugintest holds the checks channel plugin tests share: response
// classification against a live HTTP server, manifest-driven secret masking,
// and the notification egress policy. It is imported only from _test files.
package plugintest

import (
	"context"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/yassinebenameur/probara/shared/netguard"
	"github.com/yassinebenameur/probara/shared/notifications"
	"github.com/yassinebenameur/probara/shared/notifications/plugin"
	"github.com/yassinebenameur/probara/shared/secrets"
)

// AppBaseURL is the operator-UI origin AllowLoopback installs, so tests can
// assert on deep links.
const AppBaseURL = "https://probara.example.com"

// AllowLoopback installs a runtime whose egress policy lets plugins reach
// httptest servers on loopback. Call it from TestMain; AssertEgressGuard
// covers the default policy.
func AllowLoopback() {
	plugin.Configure(plugin.Runtime{AppBaseURL: AppBaseURL})
}

// SampleEvent is an open down alert with a last error.
func SampleEvent() notifications.AlertEvent {
	now := time.Date(2026, 5, 24, 12, 0, 0, 0, time.UTC)
	lastErr := "dial tcp 10.0.0.1:443: i/o timeout"
	return notifications.AlertEvent{
		Type:      "created",
		TenantID:  "tenant-1",
		Timestamp: now,
		Alert: notifications.AlertDetails{
			ID:           "a-1",
			MonitorID:    "m-1",
			MonitorName:  "API health",
			PolicyName:   "Critical",
			Status:       "active",
			TriggeredAt:  now,
			FailureCount: 3,
			LastError:    &lastErr,
		},
	}
}

// SendFunc delivers one notification to a plugin pointed at serverURL.
type SendFunc func(ctx context.Context, serverURL string) error

// AssertClassification drives send against servers answering each provider
// status and checks the plugin contract: 4xx is Permanent, 429 and 5xx are
// retried, and a 429 Retry-After is carried on the error.
func AssertClassification(t *testing.T, send SendFunc) {
	t.Helper()
	cases := []struct {
		status     int
		retryAfter string
		permanent  bool
		wantDelay  time.Duration
	}{
		{status: http.StatusBadRequest, permanent: true},
		{status: http.StatusUnauthorized, permanent: true},
		{status: http.StatusForbidden, permanent: true},
		{status: http.StatusNotFound, permanent: true},
		{status: http.StatusRequestEntityTooLarge, permanent: true},
		{status: http.StatusTooManyRequests},
		{status: http.StatusTooManyRequests, retryAfter: "7", wantDelay: 7 * time.Second},
		{status: http.StatusInternalServerError},
		{status: http.StatusBadGateway},
		{status: http.StatusServiceUnavailable},
	}
	for _, tc := range cases {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			if tc.retryAfter != "" {
				w.Header().Set("Retry-After", tc.retryAfter)
			}
			w.WriteHeader(tc.status)
			_, _ = w.Write([]byte(`{"error":"provider reason"}`))
		}))
		err := send(context.Background(), srv.URL)
		srv.Close()
		if err == nil {
			t.Errorf("status %d: Send returned nil", tc.status)
			continue
		}
		if got := plugin.IsPermanent(err); got != tc.permanent {
			t.Errorf("status %d: IsPermanent = %v, want %v (err %v)", tc.status, got, tc.permanent, err)
		}
		if tc.wantDelay > 0 {
			if d, ok := plugin.RetryAfterDelay(err); !ok || d != tc.wantDelay {
				t.Errorf("status %d: RetryAfterDelay = %v %v, want %v", tc.status, d, ok, tc.wantDelay)
			}
		}
	}
}

// AssertEgressGuard checks that send goes through the notification egress
// policy: with the default policy a server on a private address (an httptest
// server on loopback) is refused before any request reaches it, as a
// permanent failure, and listing its range in the allow-list (what
// NOTIFICATION_ALLOWED_CIDRS does) lets the same send through. It restores
// AllowLoopback's runtime when done.
func AssertEgressGuard(t *testing.T, send SendFunc) {
	t.Helper()
	defer AllowLoopback()

	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		hits.Add(1)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	plugin.Configure(plugin.Runtime{AppBaseURL: AppBaseURL, Egress: netguard.Policy{BlockPrivate: true}})
	err := send(context.Background(), srv.URL)
	if !errors.Is(err, netguard.ErrBlocked) || !plugin.IsPermanent(err) {
		t.Errorf("private server with the default policy: err = %v, want a permanent egress refusal", err)
	}
	if n := hits.Load(); n != 0 {
		t.Errorf("refused send still reached the server (%d requests)", n)
	}

	_, loopback4, _ := net.ParseCIDR("127.0.0.0/8")
	_, loopback6, _ := net.ParseCIDR("::1/128")
	plugin.Configure(plugin.Runtime{AppBaseURL: AppBaseURL, Egress: netguard.Policy{
		BlockPrivate: true,
		AllowedCIDRs: []*net.IPNet{loopback4, loopback6},
	}})
	if err := send(context.Background(), srv.URL); err != nil {
		t.Errorf("private server inside the allowed CIDRs: err = %v, want delivery", err)
	}
	if n := hits.Load(); n != 1 {
		t.Errorf("allowed send reached the server %d times, want 1", n)
	}
}

// AssertSecrets checks that the manifest marks exactly the secret keys as
// Secret, so encryption at rest, "***" masking and MergePreserveSecrets cover
// them, and that masking leaves every other field readable.
func AssertSecrets(t *testing.T, p plugin.Plugin, cfg map[string]any, secretKeys ...string) {
	t.Helper()
	m := p.Manifest()
	want := map[string]bool{}
	for _, k := range secretKeys {
		want[k] = true
	}
	for _, f := range m.Fields {
		if f.Secret != want[f.Key] {
			t.Errorf("field %q: Secret = %v, want %v", f.Key, f.Secret, want[f.Key])
		}
		if f.Secret && f.Type != plugin.FieldTypeSecret {
			t.Errorf("field %q is secret but renders as %q", f.Key, f.Type)
		}
		delete(want, f.Key)
	}
	for k := range want {
		t.Errorf("secret key %q is not a manifest field", k)
	}

	isSecret := map[string]bool{}
	for _, k := range secretKeys {
		isSecret[k] = true
	}
	masked := secrets.MaskConfig(m, cfg)
	for k, v := range cfg {
		switch {
		case isSecret[k] && masked[k] != secrets.MaskedSecret:
			t.Errorf("masked %q = %v, want %q", k, masked[k], secrets.MaskedSecret)
		case !isSecret[k] && masked[k] != v:
			t.Errorf("masked %q = %v, want it unchanged (%v)", k, masked[k], v)
		}
	}

	// An edit that submits every secret blank keeps the stored values.
	incoming := map[string]any{}
	for k, v := range cfg {
		incoming[k] = v
	}
	for _, k := range secretKeys {
		incoming[k] = ""
	}
	merged := secrets.MergePreserveSecrets(m, incoming, cfg)
	for _, k := range secretKeys {
		if _, stored := cfg[k]; stored && merged[k] != cfg[k] {
			t.Errorf("merge dropped stored secret %q", k)
		}
	}
}
