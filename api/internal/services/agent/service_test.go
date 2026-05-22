package agent

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/google/uuid"
	"github.com/yassinebenameur/probara/shared/models"
)

func TestProcessMetricsReturnsDisabledWithoutUninstallSignal(t *testing.T) {
	sqlDB, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock.New() error = %v", err)
	}
	defer sqlDB.Close()

	tenantID := uuid.New()
	monitorID := uuid.New()
	agentID := uuid.New().String()

	mock.ExpectQuery("SELECT id, name, enabled FROM monitors").
		WithArgs(agentID, tenantID).
		WillReturnRows(sqlmock.NewRows([]string{"id", "name", "enabled"}).AddRow(monitorID, "disabled agent", false))

	err = NewService(sqlDB, nil).ProcessMetrics(context.Background(), models.AgentMetricsPayload{AgentID: agentID}, tenantID)
	if !errors.Is(err, ErrAgentDisabled) {
		t.Fatalf("expected ErrAgentDisabled, got %v", err)
	}

	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("unmet expectations: %v", err)
	}
}

func TestGenerateInstallCommandCreatesUnixServiceInstaller(t *testing.T) {
	sqlDB, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock.New() error = %v", err)
	}
	defer sqlDB.Close()

	monitorID := uuid.New()
	tenantID := uuid.New()
	agentID := uuid.New().String()

	mock.ExpectQuery("SELECT agent_id, interval_seconds FROM monitors").
		WithArgs(monitorID, tenantID).
		WillReturnRows(sqlmock.NewRows([]string{"agent_id", "interval_seconds"}).AddRow(agentID, 30))

	cmd, err := NewService(sqlDB, nil).GenerateInstallCommand(context.Background(), monitorID, tenantID, "https://api.example.test", "secret-key", true)
	if err != nil {
		t.Fatalf("GenerateInstallCommand() error = %v", err)
	}

	assertContains(t, cmd.InstallScript, "install_systemd_user")
	assertContains(t, cmd.InstallScript, "Restart=always")
	assertContains(t, cmd.InstallScript, "systemctl --user enable --now probara-agent.service")
	assertContains(t, cmd.InstallScript, "install_launchd")
	assertContains(t, cmd.InstallScript, "KeepAlive")
	assertContains(t, cmd.InstallScript, "launchctl")
	assertContains(t, cmd.InstallScript, `BACKEND_URL="https://api.example.test"`)
	assertContains(t, cmd.InstallScript, `DOWNLOAD_URL="${BACKEND_URL}/static/agent/probara-agent-${OS}-${ARCH}"`)
	assertContains(t, cmd.InstallScript, `-agent-id "$AGENT_ID"`)
	assertContains(t, cmd.InstallScript, `ALLOW_REMOTE_DISABLE="true"`)
	assertContains(t, cmd.InstallScript, `-allow-remote-disable="$ALLOW_REMOTE_DISABLE"`)
	assertContains(t, cmd.InstallScript, `UNINSTALL_SCRIPT="$RUNNER_DIR/uninstall-agent.sh"`)
	assertContains(t, cmd.UninstallScript, "launchctl bootout")
	assertContains(t, cmd.UninstallScript, `systemctl --user disable "${SERVICE_NAME}.service"`)
	assertContains(t, cmd.UninstallScript, `systemctl --user stop "${SERVICE_NAME}.service"`)
	assertContains(t, cmd.UninstallScript, `launchctl bootout "$BOOTOUT_TARGET"`)
	assertNotContains(t, cmd.InstallScript, `-interval 30 &`)

	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("unmet expectations: %v", err)
	}
}

func TestGenerateInstallCommandCreatesWindowsServiceInstaller(t *testing.T) {
	sqlDB, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock.New() error = %v", err)
	}
	defer sqlDB.Close()

	monitorID := uuid.New()
	tenantID := uuid.New()
	agentID := uuid.New().String()

	mock.ExpectQuery("SELECT agent_id, interval_seconds FROM monitors").
		WithArgs(monitorID, tenantID).
		WillReturnRows(sqlmock.NewRows([]string{"agent_id", "interval_seconds"}).AddRow(agentID, 30))

	cmd, err := NewService(sqlDB, nil).GenerateInstallCommand(context.Background(), monitorID, tenantID, "https://api.example.test", "secret-key", true)
	if err != nil {
		t.Fatalf("GenerateInstallCommand() error = %v", err)
	}

	assertContains(t, cmd.WindowsInstallScript, "Requires -RunAsAdministrator")
	assertContains(t, cmd.WindowsInstallScript, "Run this installer from an elevated PowerShell window.")
	assertContains(t, cmd.WindowsInstallScript, "nssm.exe")
	assertContains(t, cmd.WindowsInstallScript, "ProbaraAgent")
	assertContains(t, cmd.WindowsInstallScript, "SERVICE_AUTO_START")
	assertContains(t, cmd.WindowsInstallScript, "Start-Service -Name $ServiceName")
	assertContains(t, cmd.WindowsInstallScript, `$BackendURL = "https://api.example.test"`)
	assertContains(t, cmd.WindowsInstallScript, `$AgentDownloadURL = "$BackendURL/static/agent/probara-agent-windows-amd64.exe"`)
	assertContains(t, cmd.WindowsInstallScript, `-agent-id "' + $AgentID + '"`)
	assertContains(t, cmd.WindowsInstallScript, `$AllowRemoteDisable = "true"`)
	assertContains(t, cmd.WindowsInstallScript, "-allow-remote-disable=")
	assertContains(t, cmd.WindowsInstallScript, "uninstall-probara-agent.ps1")
	assertContains(t, cmd.WindowsUninstallScript, "nssm.exe")
	assertContains(t, cmd.WindowsUninstallScript, "Remove-Item -Recurse -Force $InstallDir")

	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("unmet expectations: %v", err)
	}
}

func assertContains(t *testing.T, got, want string) {
	t.Helper()
	if !strings.Contains(got, want) {
		t.Fatalf("expected script to contain %q\nscript:\n%s", want, got)
	}
}

func assertNotContains(t *testing.T, got, want string) {
	t.Helper()
	if strings.Contains(got, want) {
		t.Fatalf("expected script not to contain %q\nscript:\n%s", want, got)
	}
}
