package agent

import (
	"crypto/sha256"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
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

// A re-run of the installer is the upgrade / key-rotation / interval-change
// path, so it must leave the NEW binary and config running. Linux smoke test
// (systemd container) showed `enable --now` kept the old process alive.
func TestUnixInstallerRestartsServiceOnRerun(t *testing.T) {
	unix := buildCollectorUnixInstallScript("https://probara.example.com", "agent-123", "key-456", 60)
	if !strings.Contains(unix, "systemctl restart probara-collector.service") {
		t.Error("linux installer must restart the service so a re-run applies the new binary/config")
	}
	if strings.Contains(unix, "systemctl enable --now") {
		t.Error("linux installer must not rely on enable --now (no-op for an active service)")
	}
	// Under set -euo pipefail an unmatched checksum grep would abort the
	// script silently instead of reaching the "skipping verification" warning.
	if !strings.Contains(unix, `awk '{print $1}' || true)`) {
		t.Error("checksum lookup must tolerate a missing checksums.txt entry")
	}
}

func TestUnixScriptsParse(t *testing.T) {
	dir := t.TempDir()
	for _, c := range []struct{ shell, name, body string }{
		{"bash", "install.sh", buildCollectorUnixInstallScript("https://probara.example.com", "agent-123", "key-456", 60)},
		{"sh", "uninstall.sh", buildCollectorUnixUninstallScript()}, // #!/bin/sh
	} {
		shell, err := exec.LookPath(c.shell)
		if err != nil {
			t.Logf("%s not available; skipping %s", c.shell, c.name)
			continue
		}
		p := filepath.Join(dir, c.name)
		if err := os.WriteFile(p, []byte(c.body), 0o600); err != nil {
			t.Fatal(err)
		}
		if out, err := exec.Command(shell, "-n", p).CombinedOutput(); err != nil {
			t.Errorf("%s does not parse under %s: %v\n%s", c.name, c.shell, err, out)
		}
	}
}

// A script fetched without a tenant API key (cookie session, MCP token) embeds
// an empty key; it must stop before touching the host. Pinned statically (the
// guard precedes the legacy cleanup, mkdir and download) so a regression can
// never run destructive steps on the machine running the tests.
func TestInstallersRefuseEmptyKeyBeforeTouchingHost(t *testing.T) {
	unix := buildCollectorUnixInstallScript("https://probara.example.com", "agent-123", "", 60)
	assertBefore(t, "unix", unix, `if [ -z "$API_KEY" ]; then`,
		"# Remove any legacy probara-agent install", `mkdir -p "$INSTALL_DIR"`, "curl -fsSL")

	win := buildCollectorWindowsInstallScript("https://probara.example.com", "agent-123", "", 60)
	assertBefore(t, "windows", win, "if (-not $ApiKey)",
		"# Remove any legacy probara-agent install", "New-Item -ItemType Directory", "Invoke-WebRequest")

	// And behaviourally, in a sandbox: empty PATH (the guard needs only
	// builtins, so any regression dies on the first external command) and a
	// scratch HOME.
	bash, err := exec.LookPath("bash")
	if err != nil {
		t.Skip("bash not available")
	}
	sb := t.TempDir()
	script := filepath.Join(sb, "nokey.sh")
	if err := os.WriteFile(script, []byte(unix), 0o600); err != nil {
		t.Fatal(err)
	}
	emptyBin := filepath.Join(sb, "bin")
	if err := os.Mkdir(emptyBin, 0o700); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(bash, script)
	cmd.Dir = sb
	cmd.Env = []string{"PATH=" + emptyBin, "HOME=" + filepath.Join(sb, "home")}
	out, err := cmd.CombinedOutput()
	if err == nil {
		t.Fatalf("installer with an empty API key must fail, got success:\n%s", out)
	}
	if !strings.Contains(string(out), "no API key embedded") {
		t.Errorf("empty-key installer must stop at the key guard, got:\n%s", out)
	}
}

func assertBefore(t *testing.T, name, script, first string, later ...string) {
	t.Helper()
	at := strings.Index(script, first)
	if at < 0 {
		t.Fatalf("%s script missing %q", name, first)
	}
	for _, l := range later {
		i := strings.Index(script, l)
		if i < 0 {
			t.Errorf("%s script missing %q", name, l)
		} else if i < at {
			t.Errorf("%s script: %q (@%d) must come after %q (@%d)", name, l, i, first, at)
		}
	}
}

// runSandboxedUnixInstaller runs the generated installer in darwin mode with
// PATH restricted to stubs and real text tools, HOME in a temp dir, and an rm
// that only deletes inside the sandbox — so the absolute-path legacy cleanup
// is inert. curl serves a fake binary; checksumsMode picks the checksums.txt
// response ("fail", "missing", "match", "mismatch"). The script ends when it
// reaches launchctl (absent), which is past everything asserted here.
func runSandboxedUnixInstaller(t *testing.T, checksumsMode string, prepare func(home string)) (home, out string, err error) {
	t.Helper()
	bash, lerr := exec.LookPath("bash")
	if lerr != nil {
		t.Skip("bash not available")
	}
	sb := t.TempDir()
	bin := filepath.Join(sb, "bin")
	home = filepath.Join(sb, "home")
	for _, d := range []string{bin, home} {
		if err := os.MkdirAll(d, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	for _, tool := range []string{"tr", "grep", "awk", "cat", "chmod", "mkdir", "mv", "sha256sum", "shasum"} {
		real, err := exec.LookPath(tool)
		if err != nil {
			continue
		}
		if err := os.Symlink(real, filepath.Join(bin, tool)); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := os.Lstat(filepath.Join(bin, "sha256sum")); err != nil {
		if _, err := os.Lstat(filepath.Join(bin, "shasum")); err != nil {
			t.Skip("neither sha256sum nor shasum available")
		}
	}
	stubs := map[string]string{
		"uname": `case "$1" in -s) echo Darwin ;; -m) echo arm64 ;; esac`,
		"id":    `echo 501`,
		"rm":    `for a in "$@"; do case "$a" in "$SANDBOX"/*) /bin/rm -rf "$a" ;; esac; done`,
		"curl": `out=""; url=""
while [ $# -gt 0 ]; do case "$1" in -o) out="$2"; shift ;; -*) ;; *) url="$1" ;; esac; shift; done
case "$url" in
  */checksums.txt)
    case "$CHECKSUMS_MODE" in
      fail) echo "curl: (7) Failed to connect" >&2; exit 7 ;;
      missing) echo "0000  probara-collector-linux-amd64" ;;
      match) echo "$FAKE_SUM  probara-collector-darwin-arm64" ;;
      mismatch) echo "ffff  probara-collector-darwin-arm64" ;;
    esac ;;
  *) printf 'fake-binary\n' > "$out" ;;
esac`,
	}
	for name, body := range stubs {
		if err := os.WriteFile(filepath.Join(bin, name), []byte("#!/bin/sh\n"+body+"\n"), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	if prepare != nil {
		prepare(home)
	}
	script := filepath.Join(sb, "install.sh")
	if err := os.WriteFile(script, []byte(buildCollectorUnixInstallScript("https://probara.example.com", "agent-123", "key-456", 60)), 0o600); err != nil {
		t.Fatal(err)
	}
	// sha256 of the payload the curl stub writes.
	sum := fmt.Sprintf("%x", sha256.Sum256([]byte("fake-binary\n")))
	cmd := exec.Command(bash, script)
	cmd.Dir = sb
	cmd.Env = []string{"PATH=" + bin, "HOME=" + home, "SANDBOX=" + sb, "CHECKSUMS_MODE=" + checksumsMode, "FAKE_SUM=" + sum}
	o, err := cmd.CombinedOutput()
	return home, string(o), err
}

func TestUnixInstallerChecksumVerification(t *testing.T) {
	binPath := func(home string) string { return filepath.Join(home, ".local", "bin", "probara-collector") }

	t.Run("unreachable checksums.txt aborts", func(t *testing.T) {
		home, out, err := runSandboxedUnixInstaller(t, "fail", nil)
		if err == nil || !strings.Contains(out, "Could not download") || strings.Contains(out, "skipping verification") {
			t.Fatalf("a failed checksums.txt fetch must abort the install (err=%v):\n%s", err, out)
		}
		for _, p := range []string{binPath(home), binPath(home) + ".tmp"} {
			if _, statErr := os.Stat(p); statErr == nil {
				t.Errorf("%s must not be left behind", p)
			}
		}
	})
	t.Run("mismatch aborts", func(t *testing.T) {
		home, out, err := runSandboxedUnixInstaller(t, "mismatch", nil)
		if err == nil || !strings.Contains(out, "Checksum mismatch") {
			t.Fatalf("a checksum mismatch must abort (err=%v):\n%s", err, out)
		}
		if _, statErr := os.Stat(binPath(home)); statErr == nil {
			t.Error("mismatched binary must not be installed")
		}
	})
	t.Run("missing entry warns and continues", func(t *testing.T) {
		home, out, _ := runSandboxedUnixInstaller(t, "missing", nil)
		if !strings.Contains(out, "no checksum published") {
			t.Fatalf("missing entry must reach the warning:\n%s", out)
		}
		if _, statErr := os.Stat(binPath(home)); statErr != nil {
			t.Errorf("binary must be installed after the warning: %v", statErr)
		}
	})
	t.Run("match installs with a 0600 env file", func(t *testing.T) {
		// An older install may have left the env file world-readable; the
		// installer must replace it rather than inherit the mode.
		home, out, _ := runSandboxedUnixInstaller(t, "match", func(home string) {
			dir := filepath.Join(home, ".config", "probara-collector")
			if err := os.MkdirAll(dir, 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(dir, "collector.env"), []byte("old\n"), 0o644); err != nil {
				t.Fatal(err)
			}
		})
		if strings.Contains(out, "skipping verification") || strings.Contains(out, "mismatch") {
			t.Fatalf("matching checksum must verify cleanly:\n%s", out)
		}
		if _, err := os.Stat(binPath(home)); err != nil {
			t.Fatalf("binary must be installed: %v\n%s", err, out)
		}
		env := filepath.Join(home, ".config", "probara-collector", "collector.env")
		info, err := os.Stat(env)
		if err != nil {
			t.Fatalf("env file missing: %v\n%s", err, out)
		}
		if mode := info.Mode().Perm(); mode != 0o600 {
			t.Errorf("env file mode = %o, want 600", mode)
		}
		body, _ := os.ReadFile(env)
		if !strings.Contains(string(body), "PROBARA_API_KEY=key-456") || !strings.Contains(string(body), "PROBARA_AGENT_ID=agent-123") {
			t.Errorf("env file content wrong:\n%s", body)
		}
	})
}

func TestWindowsInstallerServiceRegistration(t *testing.T) {
	win := buildCollectorWindowsInstallScript("https://probara.example.com", "agent-123", "key-456", 60)

	// Windows PowerShell 5.1 re-quotes a native argument containing quotes and
	// spaces, so `sc.exe create ... binPath= "\"C:\Program Files\...\" --config ..."`
	// arrives split. New-Service passes the path to CreateService verbatim.
	if strings.Contains(win, "sc.exe create $ServiceName") {
		t.Error("windows installer must register the service with New-Service, not sc.exe create")
	}
	if !strings.Contains(win, "New-Service -Name $ServiceName -BinaryPathName $BinPath") {
		t.Error("windows installer must register the service with New-Service")
	}

	// Windows refuses to overwrite a running .exe but allows renaming it: the
	// old image moves aside and the new one moves in BEFORE the service is
	// touched, so a failed swap leaves the existing service running.
	assertBefore(t, "windows", win, "Move-Item $CollectorPath $OldPath",
		`Move-Item "$CollectorPath.tmp" $CollectorPath`)
	assertBefore(t, "windows", win, `Move-Item "$CollectorPath.tmp" $CollectorPath`,
		"Stop-Service -Name $ServiceName", "sc.exe delete $ServiceName", "New-Service -Name $ServiceName")
	if !strings.Contains(win, "Could not download $ChecksumsURL") {
		t.Error("windows installer must abort with a clear message when checksums.txt cannot be fetched")
	}

	if !strings.Contains(win, "if (-not $ApiKey)") {
		t.Error("windows installer must refuse to install without an embedded API key")
	}
	if !strings.Contains(win, `$ProgressPreference = "SilentlyContinue"`) {
		t.Error("windows installer must disable the 5.1 progress bar for the binary download")
	}
}
