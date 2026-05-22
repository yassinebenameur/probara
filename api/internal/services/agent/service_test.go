package agent

import (
	"context"
	"strings"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/google/uuid"
)

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

	cmd, err := NewService(sqlDB, nil).GenerateInstallCommand(context.Background(), monitorID, tenantID, "https://api.example.test", "secret-key")
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

	cmd, err := NewService(sqlDB, nil).GenerateInstallCommand(context.Background(), monitorID, tenantID, "https://api.example.test", "secret-key")
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
