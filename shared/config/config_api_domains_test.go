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
