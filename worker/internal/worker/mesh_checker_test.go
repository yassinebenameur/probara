package worker

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/yassinebenameur/probara/shared/models"
)

func meshConfig(t *testing.T, targetLocationID, endpoint string) json.RawMessage {
	t.Helper()
	raw, err := json.Marshal(models.MeshProbeConfig{
		TargetLocationID: targetLocationID,
		Endpoint:         endpoint,
	})
	if err != nil {
		t.Fatalf("marshal config: %v", err)
	}
	return raw
}

func TestMeshChecker_Success(t *testing.T) {
	targetID := uuid.New().String()
	srv := httptest.NewServer(MeshEchoHandler(targetID))
	defer srv.Close()

	// blockPrivateIPs must be false: httptest binds to loopback.
	checker := NewMeshChecker(false, nil)
	res := checker.Check(context.Background(), meshConfig(t, targetID, srv.Listener.Addr().String()), 5)

	if res.Status != "success" {
		t.Fatalf("expected success, got %q (err=%v)", res.Status, derefErr(res.ErrorMessage))
	}
	if res.LatencyMs == nil {
		t.Error("expected latency to be recorded")
	}

	var env meshMetricsEnvelope
	if err := json.Unmarshal(res.MetricsData, &env); err != nil {
		t.Fatalf("unmarshal metrics: %v", err)
	}
	if env.Mesh == nil || env.Mesh.TargetLocationID != targetID || env.Mesh.EchoHostname == "" {
		t.Errorf("expected mesh metrics to be populated, got %+v", env.Mesh)
	}
}

func TestMeshChecker_EndpointMismatchIsFailure(t *testing.T) {
	// The endpoint is reachable but identifies as a different location —
	// a misrouted mesh_endpoint must fail, not pass.
	srv := httptest.NewServer(MeshEchoHandler(uuid.New().String()))
	defer srv.Close()

	checker := NewMeshChecker(false, nil)
	res := checker.Check(context.Background(), meshConfig(t, uuid.New().String(), srv.Listener.Addr().String()), 5)

	if res.Status != "failure" {
		t.Fatalf("expected failure, got %q (err=%v)", res.Status, derefErr(res.ErrorMessage))
	}
	if res.ErrorMessage == nil || !strings.Contains(*res.ErrorMessage, "mesh_endpoint_mismatch") {
		t.Fatalf("expected mesh_endpoint_mismatch in error, got %v", derefErr(res.ErrorMessage))
	}
}

func TestMeshChecker_ConnectRefusedIsFailure(t *testing.T) {
	// Bind then close to obtain a port that is reliably not listening.
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	endpoint := ln.Addr().String()
	ln.Close()

	checker := NewMeshChecker(false, nil)
	res := checker.Check(context.Background(), meshConfig(t, uuid.New().String(), endpoint), 2)

	// An unreachable peer is the monitored edge failing, not a checker error.
	if res.Status != "failure" {
		t.Fatalf("expected failure, got %q (err=%v)", res.Status, derefErr(res.ErrorMessage))
	}
}

func TestMeshChecker_Non200IsFailure(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	checker := NewMeshChecker(false, nil)
	res := checker.Check(context.Background(), meshConfig(t, uuid.New().String(), srv.Listener.Addr().String()), 5)

	if res.Status != "failure" {
		t.Fatalf("expected failure, got %q (err=%v)", res.Status, derefErr(res.ErrorMessage))
	}
}

func TestMeshChecker_InvalidEchoBodyIsFailure(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte("not json"))
	}))
	defer srv.Close()

	checker := NewMeshChecker(false, nil)
	res := checker.Check(context.Background(), meshConfig(t, uuid.New().String(), srv.Listener.Addr().String()), 5)

	if res.Status != "failure" {
		t.Fatalf("expected failure, got %q (err=%v)", res.Status, derefErr(res.ErrorMessage))
	}
}

func TestMeshChecker_SSRFBlockedIsError(t *testing.T) {
	// With private IPs blocked, a loopback target is policy-blocked → error,
	// not a statement about the edge's health.
	checker := NewMeshChecker(true, nil)
	res := checker.Check(context.Background(), meshConfig(t, uuid.New().String(), "127.0.0.1:9"), 2)

	if res.Status != "error" {
		t.Fatalf("expected error for ssrf-blocked target, got %q (err=%v)", res.Status, derefErr(res.ErrorMessage))
	}
	if res.ErrorMessage == nil || !strings.Contains(*res.ErrorMessage, "ssrf") {
		t.Fatalf("expected ssrf in error, got %v", derefErr(res.ErrorMessage))
	}
}

func TestMeshChecker_InvalidConfigIsError(t *testing.T) {
	checker := NewMeshChecker(false, nil)

	cases := []struct {
		name   string
		config json.RawMessage
	}{
		{"malformed json", json.RawMessage(`{`)},
		{"non-uuid target", meshConfig(t, "not-a-uuid", "10.0.0.1:8080")},
		{"endpoint missing port", meshConfig(t, uuid.New().String(), "10.0.0.1")},
		{"port zero", meshConfig(t, uuid.New().String(), "10.0.0.1:0")},
		{"port out of range", meshConfig(t, uuid.New().String(), "10.0.0.1:70000")},
		{"empty endpoint", meshConfig(t, uuid.New().String(), "")},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			res := checker.Check(context.Background(), tc.config, 5)
			if res.Status != "error" {
				t.Fatalf("expected error, got %q (err=%v)", res.Status, derefErr(res.ErrorMessage))
			}
		})
	}
}
