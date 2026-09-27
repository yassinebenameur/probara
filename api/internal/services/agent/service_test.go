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

	cmd, err := NewService(sqlDB, nil).GenerateInstallCommand(context.Background(), monitorID, tenantID, "https://api.example.test", "secret-key")
	if err != nil {
		t.Fatalf("GenerateInstallCommand() error = %v", err)
	}

	assertContains(t, cmd.InstallScript, "install_systemd_system")
	assertContains(t, cmd.InstallScript, "Probara Collector must be installed as root.")
	assertContains(t, cmd.InstallScript, "Restart=always")
	assertContains(t, cmd.InstallScript, "systemctl enable --now probara-collector.service")
	assertContains(t, cmd.InstallScript, "install_launchd")
	assertContains(t, cmd.InstallScript, "KeepAlive")
	assertContains(t, cmd.InstallScript, "launchctl")
	assertContains(t, cmd.InstallScript, `BACKEND_URL="https://api.example.test"`)
	assertContains(t, cmd.InstallScript, `DOWNLOAD_URL="${BACKEND_URL}/static/collector/probara-collector-${OS}-${ARCH}"`)
	assertContains(t, cmd.InstallScript, "EnvironmentFile=$ENV_FILE")
	assertContains(t, cmd.InstallScript, "collection_interval: 30s")
	// The env file carries the secrets; the config only references them.
	assertContains(t, cmd.InstallScript, "PROBARA_API_KEY=$API_KEY")
	assertContains(t, cmd.InstallScript, "${env:PROBARA_API_KEY}")
	// Reinstall removes the legacy agent — the old fleet's decommission path.
	assertContains(t, cmd.InstallScript, "probara-agent.service")
	assertContains(t, cmd.UninstallScript, "launchctl bootout")
	assertContains(t, cmd.UninstallScript, `systemctl disable "${SERVICE_NAME}.service"`)
	assertContains(t, cmd.UninstallScript, `systemctl stop "${SERVICE_NAME}.service"`)
	assertContains(t, cmd.UninstallScript, `launchctl bootout "$BOOTOUT_TARGET"`)
	// Remote disable is gone with the legacy agent.
	assertNotContains(t, cmd.InstallScript, "allow-remote-disable")
	assertContains(t, cmd.CollectorConfig, "endpoint: https://api.example.test/api/v1/otlp")
	if cmd.CollectorVersion == "" {
		t.Fatal("CollectorVersion must be set")
	}

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
	// Legacy NSSM-based agent is removed on install; the collector itself is
	// a native Windows service (no nssm.cc download).
	assertContains(t, cmd.WindowsInstallScript, "ProbaraAgent")
	assertNotContains(t, cmd.WindowsInstallScript, "nssm.cc")
	assertContains(t, cmd.WindowsInstallScript, "sc.exe create $ServiceName")
	assertContains(t, cmd.WindowsInstallScript, "Start-Service -Name $ServiceName")
	assertContains(t, cmd.WindowsInstallScript, `$BackendURL = "https://api.example.test"`)
	assertContains(t, cmd.WindowsInstallScript, `$DownloadURL = "$BackendURL/static/collector/probara-collector-windows-amd64.exe"`)
	// Secrets ride the service registry Environment, not the config file.
	assertContains(t, cmd.WindowsInstallScript, "PROBARA_API_KEY=$ApiKey")
	assertContains(t, cmd.WindowsInstallScript, "uninstall-probara-collector.ps1")
	assertNotContains(t, cmd.WindowsInstallScript, "allow-remote-disable")
	assertContains(t, cmd.WindowsUninstallScript, "ProbaraAgent")
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
