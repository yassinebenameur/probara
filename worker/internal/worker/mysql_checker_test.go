package worker

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/yassinebenameur/probara/shared/models"
)

func TestMySQLChecker_InvalidConfig(t *testing.T) {
	c := NewMySQLChecker(false, nil)
	result := c.Check(context.Background(), json.RawMessage(`{`), 5)
	if result.Status != "error" {
		t.Fatalf("status = %s, want error", result.Status)
	}
}

func TestMySQLChecker_InvalidConnectionString(t *testing.T) {
	c := NewMySQLChecker(false, nil)
	result := c.Check(context.Background(), json.RawMessage(`{"connection_string":"http://nope"}`), 5)
	if result.Status != "error" || !strings.Contains(*result.ErrorMessage, "connection_string") {
		t.Fatalf("result = %+v, want connection_string error", result)
	}
}

func TestMySQLChecker_ConnectionRefused(t *testing.T) {
	c := NewMySQLChecker(false, nil)
	config := fmt.Sprintf(`{"host":"127.0.0.1","port":%d,"username":"probe"}`, closedPort(t))
	result := c.Check(context.Background(), json.RawMessage(config), 2)
	if result.Status != "error" {
		t.Fatalf("status = %s, want error", result.Status)
	}
	if !strings.HasPrefix(*result.ErrorMessage, "connect:") {
		t.Fatalf("error = %q, want connect: prefix", *result.ErrorMessage)
	}
}

func TestMySQLChecker_SSRFBlocked(t *testing.T) {
	c := NewMySQLChecker(true, nil)
	result := c.Check(context.Background(), json.RawMessage(`{"host":"127.0.0.1","username":"probe"}`), 2)
	if result.Status != "error" || !strings.Contains(*result.ErrorMessage, "ssrf_blocked") {
		t.Fatalf("result = %+v, want ssrf_blocked error", result)
	}
}

func TestBuildMySQLConfig(t *testing.T) {
	t.Run("discrete fields with defaults", func(t *testing.T) {
		cfg, err := buildMySQLConfig(&models.MySQLMonitorConfig{Host: "db.internal", Username: "probe", Password: "secret"})
		if err != nil {
			t.Fatalf("buildMySQLConfig: %v", err)
		}
		if cfg.Addr != "db.internal:3306" || cfg.User != "probe" || cfg.Passwd != "secret" || cfg.TLS != nil {
			t.Fatalf("cfg = %+v", cfg)
		}
	})

	t.Run("tls enabled sets server name", func(t *testing.T) {
		enabled := true
		cfg, err := buildMySQLConfig(&models.MySQLMonitorConfig{Host: "db.internal", Username: "probe", TLSEnabled: &enabled})
		if err != nil {
			t.Fatalf("buildMySQLConfig: %v", err)
		}
		if cfg.TLS == nil || cfg.TLS.ServerName != "db.internal" {
			t.Fatalf("TLS = %+v, want ServerName db.internal", cfg.TLS)
		}
	})

	t.Run("mysql URI", func(t *testing.T) {
		cfg, err := buildMySQLConfig(&models.MySQLMonitorConfig{ConnectionString: "mysql://probe:p%40ss@db.internal:3307/app?tls=true"})
		if err != nil {
			t.Fatalf("buildMySQLConfig: %v", err)
		}
		if cfg.Addr != "db.internal:3307" || cfg.User != "probe" || cfg.Passwd != "p@ss" || cfg.DBName != "app" {
			t.Fatalf("cfg = %+v", cfg)
		}
		if cfg.TLS == nil || cfg.TLS.ServerName != "db.internal" {
			t.Fatalf("TLS = %+v, want ServerName db.internal", cfg.TLS)
		}
	})

	t.Run("native DSN", func(t *testing.T) {
		cfg, err := buildMySQLConfig(&models.MySQLMonitorConfig{ConnectionString: "probe:secret@tcp(db.internal:3306)/app"})
		if err != nil {
			t.Fatalf("buildMySQLConfig: %v", err)
		}
		if cfg.Addr != "db.internal:3306" || cfg.User != "probe" || cfg.DBName != "app" {
			t.Fatalf("cfg = %+v", cfg)
		}
	})

	t.Run("non-tcp DSN rejected", func(t *testing.T) {
		if _, err := buildMySQLConfig(&models.MySQLMonitorConfig{ConnectionString: "probe@unix(/tmp/mysql.sock)/app"}); err == nil {
			t.Fatal("want error for unix socket DSN")
		}
	})
}
