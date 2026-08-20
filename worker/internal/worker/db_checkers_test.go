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

// Cluster-check toggles and lag thresholds only matter after a successful
// ping; a connection failure must classify identically with them enabled.
func TestMongoDBChecker_ConnectionRefusedWithClusterChecks(t *testing.T) {
	c := NewMongoDBChecker(false, nil)
	config := fmt.Sprintf(`{"host":"127.0.0.1","port":%d,
		"collect_replication":true,"collect_connections":true,"collect_cache":true,
		"collect_memory":true,"collect_network":true,
		"warn_replication_lag_seconds":5,"max_replication_lag_seconds":30}`, closedPort(t))
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
	warnLatency := int64(40)

	result := dbLatencyResult("redis", 30, &maxLatency, &warnLatency, nil)
	if result.Status != "success" {
		t.Errorf("under thresholds: status = %s, want success", result.Status)
	}
	if strings.Contains(string(result.MetricsData), "latency_warn_ms") {
		t.Errorf("under warn threshold should not flag a warning: %s", result.MetricsData)
	}

	result = dbLatencyResult("redis", 50, &maxLatency, &warnLatency, nil)
	if result.Status != "success" {
		t.Errorf("warn only: status = %s, want success", result.Status)
	}
	if !strings.Contains(string(result.MetricsData), `"latency_warn_ms":40`) {
		t.Errorf("warn threshold exceeded should flag metrics: %s", result.MetricsData)
	}

	result = dbLatencyResult("redis", 150, &maxLatency, &warnLatency, nil)
	if result.Status != "failure" {
		t.Errorf("over max: status = %s, want failure", result.Status)
	}

	result = dbLatencyResult("redis", 150, nil, nil, nil)
	if result.Status != "success" {
		t.Errorf("no thresholds: status = %s, want success", result.Status)
	}
}

func TestEvaluateValueAssertion(t *testing.T) {
	cases := []struct {
		op, observed, expected string
		want                   bool
		wantErr                bool
	}{
		{"equals", "ok", "ok", true, false},
		{"equals", "ok", "nope", false, false},
		{"not_equals", "a", "b", true, false},
		{"contains", "hello world", "world", true, false},
		{"number_gt", "5", "3", true, false},
		{"number_gt", "3", "5", false, false},
		{"number_gte", "5", "5", true, false},
		{"number_lt", "2", "5", true, false},
		{"number_lte", "5", "5", true, false},
		{"number_gt", "abc", "5", false, true},
		{"number_gt", "5", "abc", false, true},
		{"bogus", "a", "b", false, true},
	}
	for _, tc := range cases {
		got, err := evaluateValueAssertion(tc.op, tc.observed, tc.expected)
		if (err != nil) != tc.wantErr {
			t.Errorf("evaluateValueAssertion(%q,%q,%q) error = %v, wantErr %v", tc.op, tc.observed, tc.expected, err, tc.wantErr)
			continue
		}
		if !tc.wantErr && got != tc.want {
			t.Errorf("evaluateValueAssertion(%q,%q,%q) = %v, want %v", tc.op, tc.observed, tc.expected, got, tc.want)
		}
	}
}

func TestParseRedisInfo(t *testing.T) {
	info := "# Server\r\nredis_version:7.2.5\r\n\r\n# Replication\r\nrole:master\r\nconnected_clients:3\r\n"
	fields := parseRedisInfo(info)
	if fields["redis_version"] != "7.2.5" || fields["role"] != "master" || fields["connected_clients"] != "3" {
		t.Fatalf("parseRedisInfo = %v", fields)
	}
}

func TestRabbitMQChecker_InvalidConfig(t *testing.T) {
	c := NewRabbitMQChecker(false, nil)
	result := c.Check(context.Background(), json.RawMessage(`{`), 5)
	if result.Status != "error" {
		t.Fatalf("status = %s, want error", result.Status)
	}
}

func TestRabbitMQChecker_InvalidConnectionString(t *testing.T) {
	c := NewRabbitMQChecker(false, nil)
	result := c.Check(context.Background(), json.RawMessage(`{"connection_string":"http://nope"}`), 5)
	if result.Status != "error" || !strings.Contains(*result.ErrorMessage, "connection_string") {
		t.Fatalf("result = %+v, want connection_string error", result)
	}
}

func TestRabbitMQChecker_ConnectionRefused(t *testing.T) {
	c := NewRabbitMQChecker(false, nil)
	config := fmt.Sprintf(`{"host":"127.0.0.1","port":%d,"username":"guest","password":"guest"}`, closedPort(t))
	result := c.Check(context.Background(), json.RawMessage(config), 2)
	if result.Status != "error" {
		t.Fatalf("status = %s, want error", result.Status)
	}
	if !strings.HasPrefix(*result.ErrorMessage, "connect:") {
		t.Fatalf("error = %q, want connect: prefix", *result.ErrorMessage)
	}
}

func TestRabbitMQChecker_SSRFBlocked(t *testing.T) {
	c := NewRabbitMQChecker(true, nil)
	result := c.Check(context.Background(), json.RawMessage(`{"host":"127.0.0.1","username":"guest","password":"guest"}`), 2)
	if result.Status != "error" || !strings.Contains(*result.ErrorMessage, "ssrf_blocked") {
		t.Fatalf("result = %+v, want ssrf_blocked error", result)
	}
}
