package agent

import (
	"strings"
	"testing"
)

func TestBuildCollectorConfigPerPlatform(t *testing.T) {
	linux := buildCollectorConfig("https://probara.example.com", 60, "linux")
	darwin := buildCollectorConfig("https://probara.example.com", 30, "darwin")
	windows := buildCollectorConfig("https://probara.example.com", 60, "windows")

	for name, cfg := range map[string]string{"linux": linux, "darwin": darwin, "windows": windows} {
		for _, want := range []string{
			"collection_interval:",
			"endpoint: https://probara.example.com/api/v1/otlp",
			"${env:PROBARA_API_KEY}",
			"${env:PROBARA_AGENT_ID}",
			"metricstransform:",
			"system.cpu.utilization:",
			"system.filesystem.utilization:",
		} {
			if !strings.Contains(cfg, want) {
				t.Errorf("%s config missing %q", name, want)
			}
		}
		if strings.Contains(cfg, "Bearer sk-") {
			t.Errorf("%s config must never embed a literal key", name)
		}
	}

	for name, cfg := range map[string]string{"linux": linux, "darwin": darwin, "windows": windows} {
		// Cardinality defaults: per-interface packet/error/drop counters off,
		// pseudo-filesystems excluded at collection.
		for _, want := range []string{
			"system.network.packets:", "system.network.errors:", "system.network.dropped:",
			"exclude_fs_types:",
		} {
			if !strings.Contains(cfg, want) {
				t.Errorf("%s config missing %q", name, want)
			}
		}
	}
	// macOS: APFS system volumes are excluded (Data stays — it IS the disk).
	if !strings.Contains(darwin, "exclude_mount_points:") || !strings.Contains(darwin, "iSCPreboot") {
		t.Error("darwin config must exclude APFS system volumes")
	}
	if strings.Contains(darwin, "Volumes/Data") {
		t.Error("darwin config must NOT exclude /System/Volumes/Data")
	}
	if strings.Contains(linux, "exclude_mount_points:") {
		t.Error("linux config must not carry the darwin mountpoint excludes")
	}

	if !strings.Contains(linux, "processes:") {
		t.Error("linux config must include the processes scraper")
	}
	if strings.Contains(darwin, "processes:") {
		t.Error("darwin config must not include the processes scraper")
	}
	if strings.Contains(windows, "load:") || strings.Contains(windows, "processes:") {
		t.Error("windows config must not include load/processes scrapers")
	}
	if !strings.Contains(linux, "collection_interval: 60s") || !strings.Contains(darwin, "collection_interval: 30s") {
		t.Error("collection_interval must reflect the monitor interval")
	}
}

func TestInstallScriptsEmbedIdentityAndLegacyCleanup(t *testing.T) {
	unix := buildCollectorUnixInstallScript("https://probara.example.com", "agent-123", "key-456", 60)
	win := buildCollectorWindowsInstallScript("https://probara.example.com", "agent-123", "key-456", 60)

	for name, script := range map[string]string{"unix": unix, "windows": win} {
		for _, want := range []string{"agent-123", "key-456", "probara-collector", "checksums.txt"} {
			if !strings.Contains(script, want) {
				t.Errorf("%s install script missing %q", name, want)
			}
		}
	}
	// Reinstalling is the guaranteed decommission path for the old fleet.
	if !strings.Contains(unix, "probara-agent.service") {
		t.Error("unix install script must remove the legacy agent service")
	}
	if !strings.Contains(win, "ProbaraAgent") {
		t.Error("windows install script must remove the legacy agent service")
	}
	if !strings.Contains(buildCollectorUnixUninstallScript(), "probara-agent") {
		t.Error("unix uninstall script must clean both generations")
	}
	if strings.Contains(win, "nssm.cc") {
		t.Error("windows installer must not download NSSM anymore")
	}
}
