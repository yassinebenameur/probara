package worker

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

const (
	vaultActiveHealth  = `{"initialized":true,"sealed":false,"standby":false,"performance_standby":false,"replication_performance_mode":"disabled","replication_dr_mode":"disabled","version":"1.17.2","cluster_name":"vault-prod"}`
	vaultStandbyHealth = `{"initialized":true,"sealed":false,"standby":true,"performance_standby":false,"version":"1.17.2"}`
	vaultSealedHealth  = `{"initialized":true,"sealed":true,"standby":true,"version":"1.17.2"}`
	vaultUninitHealth  = `{"initialized":false,"sealed":true,"standby":true}`
	vaultLookupOK      = `{"data":{"ttl":86400,"expire_time":"2026-10-13T10:00:00Z","renewable":true}}`
)

// vaultSpec is copyable table data; fakeVault adds the counters.
type vaultSpec struct {
	healthCode int
	healthBody string
	lookupCode int
	lookupBody string
}

type fakeVault struct {
	vaultSpec
	lookupHits   atomic.Int32
	sawNamespace atomic.Value
}

func (f *fakeVault) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	switch r.URL.Path {
	case "/v1/sys/health":
		if r.Header.Get("X-Vault-Token") != "" {
			http.Error(w, "token sent to unauthenticated endpoint", http.StatusBadRequest)
			return
		}
		w.WriteHeader(f.healthCode)
		fmt.Fprint(w, f.healthBody)
	case "/v1/auth/token/lookup-self":
		f.lookupHits.Add(1)
		f.sawNamespace.Store(r.Header.Get("X-Vault-Namespace"))
		if r.Header.Get("X-Vault-Token") != "s.monitor-secret" {
			w.WriteHeader(http.StatusForbidden)
			fmt.Fprint(w, `{"errors":["permission denied"]}`)
			return
		}
		if f.lookupCode == http.StatusTemporaryRedirect {
			w.Header().Set("Location", "https://attacker.invalid/v1/auth/token/lookup-self")
		}
		w.WriteHeader(f.lookupCode)
		fmt.Fprint(w, f.lookupBody)
	default:
		http.NotFound(w, r)
	}
}

func runVault(t *testing.T, f *fakeVault, cfg map[string]any) CheckResult {
	t.Helper()
	server := httptest.NewServer(f)
	t.Cleanup(server.Close)
	if _, ok := cfg["url"]; !ok {
		cfg["url"] = server.URL
	}
	raw, _ := json.Marshal(cfg)
	return NewVaultChecker(false, nil).Check(context.Background(), raw, 2)
}

func TestVaultChecker(t *testing.T) {
	withToken := func(extra map[string]any) map[string]any {
		cfg := map[string]any{"token": "s.monitor-secret"}
		for k, v := range extra {
			cfg[k] = v
		}
		return cfg
	}
	cases := []struct {
		name    string
		vault   vaultSpec
		cfg     map[string]any
		status  string
		message string // substring of the error message
		probed  bool   // lookup-self must (true) / must not (false) be called
	}{
		{"healthy active without token", vaultSpec{healthCode: 200, healthBody: vaultActiveHealth}, map[string]any{}, "success", "", false},
		{"healthy active with token", vaultSpec{healthCode: 200, healthBody: vaultActiveHealth, lookupCode: 200, lookupBody: vaultLookupOK}, withToken(nil), "success", "", true},
		// The incident this type exists for: health green, audited request 500.
		{"audit device failure", vaultSpec{healthCode: 200, healthBody: vaultActiveHealth, lookupCode: 500, lookupBody: `{"errors":["internal error"]}`}, withToken(nil), "failure", "audit device", true},
		{"sealed", vaultSpec{healthCode: 503, healthBody: vaultSealedHealth}, withToken(nil), "failure", "sealed", false},
		{"uninitialized", vaultSpec{healthCode: 501, healthBody: vaultUninitHealth}, withToken(nil), "failure", "not initialized", false},
		{"standby allowed", vaultSpec{healthCode: 429, healthBody: vaultStandbyHealth, lookupCode: 200, lookupBody: vaultLookupOK}, withToken(nil), "success", "", true},
		{"standby with require_active", vaultSpec{healthCode: 429, healthBody: vaultStandbyHealth}, withToken(map[string]any{"require_active": true}), "failure", "standby", false},
		{"removed from raft", vaultSpec{healthCode: 530, healthBody: `{"initialized":true,"sealed":false,"standby":true,"removed_from_cluster":true}`}, map[string]any{}, "failure", "removed", false},
		{"proxy error page", vaultSpec{healthCode: 502, healthBody: `<html>bad gateway</html>`}, withToken(nil), "failure", "HTTP 502", false},
		{"foreign json", vaultSpec{healthCode: 200, healthBody: `{"status":"ok"}`}, map[string]any{}, "failure", "without a Vault health body", false},
		{"bad token", vaultSpec{healthCode: 200, healthBody: vaultActiveHealth, lookupCode: 200, lookupBody: vaultLookupOK}, map[string]any{"token": "s.wrong"}, "error", "403", true},
		{"rate limited", vaultSpec{healthCode: 200, healthBody: vaultActiveHealth, lookupCode: 429, lookupBody: `{"errors":["request path \"auth/token/lookup-self\": rate limit quota exceeded"]}`}, withToken(nil), "failure", "rate limit", true},
		{"standby redirect not followed", vaultSpec{healthCode: 429, healthBody: vaultStandbyHealth, lookupCode: 307}, withToken(nil), "error", "redirected", true},
		{"token ttl low", vaultSpec{healthCode: 200, healthBody: vaultActiveHealth, lookupCode: 200, lookupBody: `{"data":{"ttl":600}}`}, withToken(map[string]any{"token_min_ttl_seconds": 3600}), "failure", "expires in 10m0s", true},
		{"token ttl ok", vaultSpec{healthCode: 200, healthBody: vaultActiveHealth, lookupCode: 200, lookupBody: vaultLookupOK}, withToken(map[string]any{"token_min_ttl_seconds": 3600}), "success", "", true},
		{"non-expiring token ignores ttl rule", vaultSpec{healthCode: 200, healthBody: vaultActiveHealth, lookupCode: 200, lookupBody: `{"data":{"ttl":0}}`}, withToken(map[string]any{"token_min_ttl_seconds": 3600}), "success", "", true},
		{"ttl rule without token", vaultSpec{healthCode: 200, healthBody: vaultActiveHealth}, map[string]any{"token_min_ttl_seconds": 3600}, "error", "requires a token", false},
		{"unresolved token", vaultSpec{healthCode: 200, healthBody: vaultActiveHealth}, map[string]any{"token": "***"}, "error", "not resolved", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := &fakeVault{vaultSpec: tc.vault}
			got := runVault(t, f, tc.cfg)
			msg := ""
			if got.ErrorMessage != nil {
				msg = *got.ErrorMessage
			}
			if got.Status != tc.status {
				t.Fatalf("status %s (%q), want %s", got.Status, msg, tc.status)
			}
			if tc.message != "" && !strings.Contains(msg, tc.message) {
				t.Fatalf("message %q does not contain %q", msg, tc.message)
			}
			if strings.Contains(msg, "monitor-secret") {
				t.Fatal("error message leaked the token")
			}
			if hit := f.lookupHits.Load() > 0; hit != tc.probed {
				t.Fatalf("token probe called = %v, want %v", hit, tc.probed)
			}
		})
	}
}

func TestVaultCheckerMetricsAndNamespace(t *testing.T) {
	f := &fakeVault{vaultSpec: vaultSpec{healthCode: 473, healthBody: `{"initialized":true,"sealed":false,"standby":true,"performance_standby":true,"version":"1.17.2+ent","cluster_name":"c1"}`, lookupCode: 200, lookupBody: vaultLookupOK}}
	got := runVault(t, f, map[string]any{"token": "s.monitor-secret", "namespace": "team-a"})
	if got.Status != "success" {
		t.Fatalf("status %s: %v", got.Status, got.ErrorMessage)
	}
	if ns, _ := f.sawNamespace.Load().(string); ns != "team-a" {
		t.Fatalf("namespace header %q", ns)
	}
	if got.HTTPStatus == nil || *got.HTTPStatus != 473 {
		t.Fatalf("health http status not recorded: %v", got.HTTPStatus)
	}
	var env struct {
		Vault vaultMetrics `json:"vault"`
	}
	if err := json.Unmarshal(got.MetricsData, &env); err != nil {
		t.Fatal(err)
	}
	m := env.Vault
	if m.Role != "performance_standby" || m.Version != "1.17.2+ent" || m.ClusterName != "c1" || m.Sealed == nil || *m.Sealed {
		t.Fatalf("unexpected metrics %+v", m)
	}
	if m.TokenProbe == nil || !m.TokenProbe.OK || m.TokenProbe.HTTPStatus != 200 || m.TokenProbe.TTLSeconds == nil || *m.TokenProbe.TTLSeconds != 86400 {
		t.Fatalf("unexpected token probe metrics %+v", m.TokenProbe)
	}
	if strings.Contains(string(got.MetricsData), "monitor-secret") {
		t.Fatal("metrics leaked the token")
	}
}

func TestVaultCheckerBlocksPrivateTargets(t *testing.T) {
	server := httptest.NewServer(&fakeVault{vaultSpec: vaultSpec{healthCode: 200, healthBody: vaultActiveHealth}})
	defer server.Close()
	raw, _ := json.Marshal(map[string]any{"url": server.URL})
	got := NewVaultChecker(true, nil).Check(context.Background(), raw, 2)
	if got.Status != "error" {
		t.Fatalf("private target must be refused under the SSRF policy, got %s", got.Status)
	}
}
