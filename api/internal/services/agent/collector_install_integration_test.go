//go:build !unit

package agent

import (
	"bufio"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

// collectorPlatforms are the config variants buildCollectorConfig renders.
// The host's binary checks all of them: validation and startup do not depend
// on the OS the config targets.
var collectorPlatforms = []string{"linux", "darwin", "windows"}

// hostCollectorBinary returns the probara-collector build for this machine,
// skipping the test when it has not been built (make build-collector-static).
func hostCollectorBinary(t *testing.T) string {
	t.Helper()
	name := fmt.Sprintf("probara-collector-%s-%s", runtime.GOOS, runtime.GOARCH)
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	bin := filepath.Join("..", "..", "..", "..", "static", "collector", name)
	if _, err := os.Stat(bin); err != nil {
		t.Skipf("collector binary not built at %s; run make build-collector-static", bin)
	}
	return bin
}

func writeCollectorConfig(t *testing.T, backendURL, platform string) string {
	t.Helper()
	cfgPath := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(cfgPath, []byte(buildCollectorConfig(backendURL, 60, platform)), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}
	return cfgPath
}

func collectorEnv() []string {
	return append(os.Environ(), "PROBARA_API_KEY=test-key", "PROBARA_AGENT_ID=test-agent")
}

// TestGeneratedConfigValidatesWithCollector runs the actual probara-collector
// binary's `validate` command against every generated config variant.
func TestGeneratedConfigValidatesWithCollector(t *testing.T) {
	bin := hostCollectorBinary(t)
	for _, platform := range collectorPlatforms {
		t.Run(platform, func(t *testing.T) {
			cmd := exec.Command(bin, "validate", "--config", writeCollectorConfig(t, "https://probara.example.com", platform))
			cmd.Env = collectorEnv()
			out, err := cmd.CombinedOutput()
			if err != nil {
				t.Fatalf("collector validate failed: %v\n%s", err, out)
			}
		})
	}
}

// TestGeneratedConfigStartsWithoutDeprecationWarnings starts the real
// collector on every config variant and fails on any deprecation warning it
// logs. `validate` cannot catch these: the collector only reports a
// deprecated component type alias (e.g. "otlphttp" for "otlp_http") when it
// builds the pipeline, so the test runs it until "Everything is ready",
// exporting to a local sink, then stops it.
func TestGeneratedConfigStartsWithoutDeprecationWarnings(t *testing.T) {
	bin := hostCollectorBinary(t)
	sink := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer sink.Close()

	for _, platform := range collectorPlatforms {
		t.Run(platform, func(t *testing.T) {
			cmd := exec.Command(bin, "--config", writeCollectorConfig(t, sink.URL, platform))
			cmd.Env = collectorEnv()
			stderr, err := cmd.StderrPipe()
			if err != nil {
				t.Fatalf("stderr pipe: %v", err)
			}
			cmd.Stdout = cmd.Stderr
			if err := cmd.Start(); err != nil {
				t.Fatalf("start collector: %v", err)
			}
			defer func() {
				_ = cmd.Process.Kill()
				_ = cmd.Wait()
			}()

			ready := make(chan struct{})
			var log []string
			go func() {
				defer close(ready)
				scanner := bufio.NewScanner(stderr)
				scanner.Buffer(make([]byte, 64*1024), 1024*1024)
				for scanner.Scan() {
					line := scanner.Text()
					log = append(log, line)
					if strings.Contains(line, "Everything is ready") {
						return
					}
				}
			}()

			select {
			case <-ready:
			case <-time.After(30 * time.Second):
				_ = cmd.Process.Kill()
				<-ready
				t.Fatalf("collector not ready within 30s:\n%s", strings.Join(log, "\n"))
			}

			var deprecated []string
			for _, line := range log {
				if strings.Contains(strings.ToLower(line), "deprecated") {
					deprecated = append(deprecated, line)
				}
			}
			if len(deprecated) > 0 {
				t.Fatalf("collector logged deprecation warnings for the %s config:\n%s", platform, strings.Join(deprecated, "\n"))
			}
			if !strings.Contains(strings.Join(log, "\n"), "Everything is ready") {
				t.Fatalf("collector exited before becoming ready:\n%s", strings.Join(log, "\n"))
			}
		})
	}
}
