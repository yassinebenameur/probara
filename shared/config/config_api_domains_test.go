package config

import (
	"strings"
	"testing"
)

func TestLoadAPIConfig_StatusPageDomains(t *testing.T) {
	setRequiredAPIEnv(t)
	t.Setenv("STATUS_PAGE_CNAME_TARGET", "")
	t.Setenv("STATUS_PAGE_BASE_URL", "")
	cfg, err := LoadAPIConfig()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.StatusPageCNAMETarget != "" || cfg.StatusPagePublicBaseURL != "" {
		t.Fatalf("defaults: %+v", cfg.StatusPageDomainsConfig)
	}

	t.Setenv("STATUS_PAGE_CNAME_TARGET", " Status.Example.com. ")
	t.Setenv("STATUS_PAGE_BASE_URL", "https://status.example.com/")
	if cfg, err = LoadAPIConfig(); err != nil {
		t.Fatal(err)
	}
	if cfg.StatusPageCNAMETarget != "status.example.com" || cfg.StatusPagePublicBaseURL != "https://status.example.com" {
		t.Fatalf("loaded: %+v", cfg.StatusPageDomainsConfig)
	}

	for _, bad := range []string{"https://status.example.com", "status.example.com:443", "status.example.com/x", "not a host"} {
		t.Setenv("STATUS_PAGE_CNAME_TARGET", bad)
		if _, err := LoadAPIConfig(); err == nil || !strings.Contains(err.Error(), "STATUS_PAGE_CNAME_TARGET") {
			t.Errorf("%q: err = %v", bad, err)
		}
	}
}

// Custom domains need the status-page service to refuse unverified hosts,
// which it does only with a public platform host.
func TestStatusPageFailsClosed(t *testing.T) {
	cases := []struct {
		base     string
		platform []string
		want     bool
	}{
		{"", nil, false},
		{"http://localhost:8082", nil, false},
		{"http://10.0.0.5:8082", nil, false},
		{"https://status.example.com", nil, true},
		{"http://localhost:8082", []string{"probara.example.com"}, true},
		{"", []string{"status-page"}, false},
	}
	for _, c := range cases {
		cfg := StatusPageDomainsConfig{StatusPagePublicBaseURL: c.base, StatusPagePlatformHosts: c.platform}
		if got := cfg.StatusPageFailsClosed(); got != c.want {
			t.Errorf("%q %v: %v, want %v", c.base, c.platform, got, c.want)
		}
	}
	setRequiredAPIEnv(t)
	t.Setenv("STATUS_PAGE_PLATFORM_HOSTS", "Probara.Example.com, status.example.com")
	loaded, err := LoadAPIConfig()
	if err != nil || strings.Join(loaded.StatusPagePlatformHosts, ",") != "probara.example.com,status.example.com" {
		t.Fatalf("platform hosts %v err %v", loaded.StatusPagePlatformHosts, err)
	}
}
