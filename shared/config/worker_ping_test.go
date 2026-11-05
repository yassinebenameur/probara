package config

import (
	"strings"
	"testing"
)

func TestLoadWorkerConfig_PingMode(t *testing.T) {
	t.Setenv("HTTP_PORT", "8083")
	t.Setenv("METRICS_PORT", "9092")

	for env, want := range map[string]string{
		"":             PingModeUnprivileged,
		"unprivileged": PingModeUnprivileged,
		" Privileged ": PingModePrivileged,
		"auto":         PingModeAuto,
	} {
		t.Setenv("WORKER_PING_MODE", env)
		cfg, err := LoadWorkerConfig()
		if err != nil || cfg.PingMode != want {
			t.Fatalf("WORKER_PING_MODE=%q: mode=%v err=%v, want %q", env, cfg, err, want)
		}
	}

	t.Setenv("WORKER_PING_MODE", "raw")
	if _, err := LoadWorkerConfig(); err == nil || !strings.Contains(err.Error(), "invalid WORKER_PING_MODE") {
		t.Fatalf("invalid WORKER_PING_MODE accepted: %v", err)
	}
}
