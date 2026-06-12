package worker

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"strings"
	"testing"

	"github.com/yassinebenameur/probara/shared/models"
)

// closedPortAddr returns host/port for a port that was just closed, so
// connections to it are refused fast.
func closedPort(t *testing.T) int {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	port := l.Addr().(*net.TCPAddr).Port
	l.Close()
	return port
}

func TestRedisChecker_InvalidConfig(t *testing.T) {
	c := NewRedisChecker(false, nil)
	result := c.Check(context.Background(), json.RawMessage(`{`), 5)
	if result.Status != "error" {
		t.Fatalf("status = %s, want error", result.Status)
	}
}

func TestRedisChecker_InvalidConnectionString(t *testing.T) {
	c := NewRedisChecker(false, nil)
	result := c.Check(context.Background(), json.RawMessage(`{"connection_string":"http://nope"}`), 5)
	if result.Status != "error" || !strings.Contains(*result.ErrorMessage, "connection_string") {
		t.Fatalf("result = %+v, want connection_string error", result)
	}
}

func TestRedisChecker_ConnectionRefused(t *testing.T) {
	c := NewRedisChecker(false, nil)
	config := fmt.Sprintf(`{"host":"127.0.0.1","port":%d}`, closedPort(t))
	result := c.Check(context.Background(), json.RawMessage(config), 2)
	if result.Status != "error" {
		t.Fatalf("status = %s, want error", result.Status)
	}
	if !strings.HasPrefix(*result.ErrorMessage, "connect:") {
		t.Fatalf("error = %q, want connect: prefix", *result.ErrorMessage)
	}
}

func TestRedisChecker_SSRFBlocked(t *testing.T) {
	c := NewRedisChecker(true, nil)
	result := c.Check(context.Background(), json.RawMessage(`{"host":"127.0.0.1","port":6379}`), 2)
	if result.Status != "error" || !strings.Contains(*result.ErrorMessage, "ssrf_blocked") {
		t.Fatalf("result = %+v, want ssrf_blocked error", result)
	}
}

func TestPostgresChecker_InvalidConfig(t *testing.T) {
	c := NewPostgresChecker(false, nil)
	result := c.Check(context.Background(), json.RawMessage(`{`), 5)
	if result.Status != "error" {
		t.Fatalf("status = %s, want error", result.Status)
	}
}

func TestPostgresChecker_ConnectionRefused(t *testing.T) {
	c := NewPostgresChecker(false, nil)
	config := fmt.Sprintf(`{"host":"127.0.0.1","port":%d,"username":"probe","ssl_mode":"disable"}`, closedPort(t))
	result := c.Check(context.Background(), json.RawMessage(config), 2)
	if result.Status != "error" {
		t.Fatalf("status = %s, want error", result.Status)
	}
}

func TestPostgresChecker_SSRFBlocked(t *testing.T) {
	c := NewPostgresChecker(true, nil)
	result := c.Check(context.Background(), json.RawMessage(`{"host":"127.0.0.1","username":"probe","ssl_mode":"disable"}`), 2)
	if result.Status != "error" || !strings.Contains(*result.ErrorMessage, "ssrf_blocked") {
		t.Fatalf("result = %+v, want ssrf_blocked error", result)
	}
}

func TestMongoDBChecker_InvalidConfig(t *testing.T) {
	c := NewMongoDBChecker(false, nil)
	result := c.Check(context.Background(), json.RawMessage(`{`), 5)
	if result.Status != "error" {
		t.Fatalf("status = %s, want error", result.Status)
	}
}

func TestMongoDBChecker_ConnectionRefused(t *testing.T) {
	c := NewMongoDBChecker(false, nil)
	config := fmt.Sprintf(`{"host":"127.0.0.1","port":%d}`, closedPort(t))
	result := c.Check(context.Background(), json.RawMessage(config), 2)
	if result.Status != "error" {
		t.Fatalf("status = %s, want error", result.Status)
	}
}

func TestBuildPostgresURI(t *testing.T) {
	cases := []struct {
		name   string
		config models.PostgresMonitorConfig
		want   string
	}{
		{
			name:   "defaults",
			config: models.PostgresMonitorConfig{Host: "db.internal"},
			want:   "postgres://db.internal:5432/postgres?sslmode=prefer",
		},
		{
			name:   "full with credential escaping",
			config: models.PostgresMonitorConfig{Host: "db.internal", Port: 5433, Database: "app", Username: "probe", Password: "p@ss w/ord", SSLMode: "verify-full"},
			want:   "postgres://probe:p%40ss%20w%2Ford@db.internal:5433/app?sslmode=verify-full",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := buildPostgresURI(&tc.config)
			if got != tc.want {
				t.Errorf("buildPostgresURI() = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestClassifyDBError(t *testing.T) {
	cases := []struct {
		err  string
		want string
	}{
		{"dial tcp: i/o timeout", "timeout"},
		{"context deadline exceeded", "timeout"},
		{"dial tcp: lookup nope: no such host", "dns"},
		{"WRONGPASS invalid username-password pair", "auth"},
		{"failed SASL auth: authentication failed", "auth"},
		{"tls: failed to verify certificate", "tls"},
		{"dial tcp 127.0.0.1:5432: connect: connection refused", "connect"},
		{"ssrf_blocked: ip 127.0.0.1 not allowed", "ssrf_blocked"},
		{"something odd", "check"},
	}
	for _, tc := range cases {
		if got := classifyDBError(errors.New(tc.err)); got != tc.want {
			t.Errorf("classifyDBError(%q) = %q, want %q", tc.err, got, tc.want)
		}
	}
}

func TestDBLatencyResult(t *testing.T) {
	maxLatency := int64(100)

	result := dbLatencyResult(50, &maxLatency)
	if result.Status != "success" {
		t.Errorf("under threshold: status = %s, want success", result.Status)
	}

	result = dbLatencyResult(150, &maxLatency)
	if result.Status != "failure" {
		t.Errorf("over threshold: status = %s, want failure", result.Status)
	}

	result = dbLatencyResult(150, nil)
	if result.Status != "success" {
		t.Errorf("no threshold: status = %s, want success", result.Status)
	}
}
