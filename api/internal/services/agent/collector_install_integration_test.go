//go:build !unit

package agent

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"

	"testing"
)

// TestGeneratedConfigValidatesWithCollector runs the actual probara-collector
// binary's `validate` command against the generated config. Skips when the
// binary for this platform hasn't been built (make build-collector-static).
func TestGeneratedConfigValidatesWithCollector(t *testing.T) {
	bin := filepath.Join("..", "..", "..", "..", "static", "collector",
		fmt.Sprintf("probara-collector-%s-%s", runtime.GOOS, runtime.GOARCH))
	if _, err := os.Stat(bin); err != nil {
		t.Skipf("collector binary not built at %s; run make build-collector-static", bin)
	}

	platform := runtime.GOOS
	if platform != "linux" && platform != "darwin" && platform != "windows" {
		t.Skipf("no config variant for %s", platform)
	}
	cfgPath := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(cfgPath, []byte(buildCollectorConfig("https://probara.example.com", 60, platform)), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}

	cmd := exec.Command(bin, "validate", "--config", cfgPath)
	cmd.Env = append(os.Environ(), "PROBARA_API_KEY=test-key", "PROBARA_AGENT_ID=test-agent")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("collector validate failed: %v\n%s", err, out)
	}
}
