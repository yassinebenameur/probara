package worker

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
)

func tcpConfig(t *testing.T, cfg map[string]any) json.RawMessage {
	t.Helper()
	raw, err := json.Marshal(cfg)
	if err != nil {
		t.Fatalf("marshal config: %v", err)
	}
	return raw
}

func TestTCPChecker_ConnectSuccess(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer ln.Close()
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			conn.Close()
		}
	}()

	host, portStr, _ := net.SplitHostPort(ln.Addr().String())
	port, _ := strconv.Atoi(portStr)

	// blockPrivateIPs must be false: the listener is on loopback.
	checker := NewTCPChecker(false, nil)
	res := checker.Check(context.Background(), tcpConfig(t, map[string]any{"host": host, "port": port}), 5)

	if res.Status != "success" {
		t.Fatalf("expected success, got %q (err=%v)", res.Status, derefErr(res.ErrorMessage))
	}
	if res.LatencyMs == nil {
		t.Error("expected latency to be recorded")
	}
}

func TestTCPChecker_ConnectRefused(t *testing.T) {
	// Bind then close to obtain a port that is reliably not listening.
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	host, portStr, _ := net.SplitHostPort(ln.Addr().String())
	port, _ := strconv.Atoi(portStr)
	ln.Close()

	checker := NewTCPChecker(false, nil)
	res := checker.Check(context.Background(), tcpConfig(t, map[string]any{"host": host, "port": port}), 2)

	// A refused/unreachable target is the monitored thing failing, not a
	// checker error.
	if res.Status != "failure" {
		t.Fatalf("expected failure, got %q (err=%v)", res.Status, derefErr(res.ErrorMessage))
	}
}

func TestTCPChecker_InvalidPort(t *testing.T) {
	checker := NewTCPChecker(false, nil)
	res := checker.Check(context.Background(), tcpConfig(t, map[string]any{"host": "example.com", "port": 0}), 5)

	if res.Status != "error" {
		t.Fatalf("expected error for missing port, got %q", res.Status)
	}
}

func TestTCPChecker_SSRFBlockedIsError(t *testing.T) {
	// With private IPs blocked, a loopback target is policy-blocked → error,
	// not a statement about the target's health.
	checker := NewTCPChecker(true, nil)
	res := checker.Check(context.Background(), tcpConfig(t, map[string]any{"host": "127.0.0.1", "port": 9}), 2)

	if res.Status != "error" {
		t.Fatalf("expected error for ssrf-blocked target, got %q (err=%v)", res.Status, derefErr(res.ErrorMessage))
	}
}

func TestTCPChecker_TLSHandshakeSuccess(t *testing.T) {
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {}))
	defer srv.Close()

	u := srv.Listener.Addr().String()
	host, portStr, _ := net.SplitHostPort(u)
	port, _ := strconv.Atoi(portStr)

	checker := NewTCPChecker(false, nil)
	res := checker.Check(context.Background(), tcpConfig(t, map[string]any{
		"host":            host,
		"port":            port,
		"use_tls":         true,
		"tls_skip_verify": true, // httptest uses a self-signed cert
	}), 5)

	if res.Status != "success" {
		t.Fatalf("expected success, got %q (err=%v)", res.Status, derefErr(res.ErrorMessage))
	}

	var env tcpMetricsEnvelope
	if err := json.Unmarshal(res.MetricsData, &env); err != nil {
		t.Fatalf("unmarshal metrics: %v", err)
	}
	if env.TCP == nil || !env.TCP.UseTLS || env.TCP.TLSVersion == "" {
		t.Errorf("expected TLS metrics to be populated, got %+v", env.TCP)
	}
}

func TestTCPChecker_TLSHandshakeFailsWithoutSkipVerify(t *testing.T) {
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {}))
	defer srv.Close()

	host, portStr, _ := net.SplitHostPort(srv.Listener.Addr().String())
	port, _ := strconv.Atoi(portStr)

	checker := NewTCPChecker(false, nil)
	res := checker.Check(context.Background(), tcpConfig(t, map[string]any{
		"host":    host,
		"port":    port,
		"use_tls": true,
	}), 5)

	// Untrusted cert → handshake fails → the target is failing the check.
	if res.Status != "failure" {
		t.Fatalf("expected failure for untrusted cert, got %q (err=%v)", res.Status, derefErr(res.ErrorMessage))
	}
}

func derefErr(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}
