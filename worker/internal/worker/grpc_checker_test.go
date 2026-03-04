package worker

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"encoding/pem"
	"math/big"
	"net"
	"strconv"
	"strings"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
	health "google.golang.org/grpc/health"
	healthpb "google.golang.org/grpc/health/grpc_health_v1"
)

func TestGRPCChecker_PlaintextServingSuccess(t *testing.T) {
	host, port, cleanup := startGRPCHealthServer(t, false, healthpb.HealthCheckResponse_SERVING)
	defer cleanup()

	useTLS := false
	config := map[string]interface{}{
		"host":    host,
		"port":    port,
		"use_tls": useTLS,
	}
	configRaw, _ := json.Marshal(config)

	checker := NewGRPCChecker(false, nil)
	result := checker.Check(context.Background(), configRaw, 5)

	if result.Status != "success" {
		t.Fatalf("expected success, got %s (%v)", result.Status, result.ErrorMessage)
	}
	if result.LatencyMs == nil {
		t.Fatal("expected latency to be set")
	}
	if len(result.MetricsData) == 0 {
		t.Fatal("expected metrics data")
	}

	var metrics map[string]map[string]interface{}
	if err := json.Unmarshal(result.MetricsData, &metrics); err != nil {
		t.Fatalf("failed to parse metrics: %v", err)
	}
	grpcMetrics := metrics["grpc"]
	if grpcMetrics == nil {
		t.Fatal("expected grpc metrics envelope")
	}
	if grpcMetrics["serving_status"] != "SERVING" {
		t.Fatalf("expected serving_status SERVING, got %v", grpcMetrics["serving_status"])
	}
}

func TestGRPCChecker_NonServingFailure(t *testing.T) {
	host, port, cleanup := startGRPCHealthServer(t, false, healthpb.HealthCheckResponse_NOT_SERVING)
	defer cleanup()

	useTLS := false
	configRaw, _ := json.Marshal(map[string]interface{}{
		"host":    host,
		"port":    port,
		"use_tls": useTLS,
	})

	checker := NewGRPCChecker(false, nil)
	result := checker.Check(context.Background(), configRaw, 5)

	if result.Status != "failure" {
		t.Fatalf("expected failure, got %s", result.Status)
	}
	if result.ErrorMessage == nil || !strings.Contains(*result.ErrorMessage, "NOT_SERVING") {
		t.Fatalf("expected NOT_SERVING error message, got %v", result.ErrorMessage)
	}
}

func TestGRPCChecker_TLSPathReturnsErrorForUntrustedCert(t *testing.T) {
	host, port, cleanup := startGRPCHealthServer(t, true, healthpb.HealthCheckResponse_SERVING)
	defer cleanup()

	useTLS := true
	configRaw, _ := json.Marshal(map[string]interface{}{
		"host":    host,
		"port":    port,
		"use_tls": useTLS,
	})

	checker := NewGRPCChecker(false, nil)
	result := checker.Check(context.Background(), configRaw, 5)

	if result.Status != "error" {
		t.Fatalf("expected error, got %s", result.Status)
	}
	if result.ErrorMessage == nil {
		t.Fatal("expected tls error message")
	}
}

func TestGRPCChecker_DialError(t *testing.T) {
	useTLS := false
	configRaw, _ := json.Marshal(map[string]interface{}{
		"host":    "127.0.0.1",
		"port":    65534,
		"use_tls": useTLS,
	})

	checker := NewGRPCChecker(false, nil)
	result := checker.Check(context.Background(), configRaw, 1)

	if result.Status != "error" {
		t.Fatalf("expected error, got %s", result.Status)
	}
	if result.ErrorMessage == nil {
		t.Fatal("expected error message")
	}
}

func TestGRPCChecker_SSRFBlocksPrivateIP(t *testing.T) {
	host, port, cleanup := startGRPCHealthServer(t, false, healthpb.HealthCheckResponse_SERVING)
	defer cleanup()

	useTLS := false
	configRaw, _ := json.Marshal(map[string]interface{}{
		"host":    host,
		"port":    port,
		"use_tls": useTLS,
	})

	checker := NewGRPCChecker(true, nil)
	result := checker.Check(context.Background(), configRaw, 5)

	if result.Status != "error" {
		t.Fatalf("expected error when private IPs blocked, got %s", result.Status)
	}
	if result.ErrorMessage == nil {
		t.Fatal("expected an error message when private IP is blocked")
	}
}

func TestGRPCChecker_SSRFAllowlistOverride(t *testing.T) {
	host, port, cleanup := startGRPCHealthServer(t, false, healthpb.HealthCheckResponse_SERVING)
	defer cleanup()

	_, cidr, err := net.ParseCIDR("127.0.0.0/8")
	if err != nil {
		t.Fatalf("failed to parse cidr: %v", err)
	}

	useTLS := false
	configRaw, _ := json.Marshal(map[string]interface{}{
		"host":    host,
		"port":    port,
		"use_tls": useTLS,
	})

	checker := NewGRPCChecker(true, []*net.IPNet{cidr})
	result := checker.Check(context.Background(), configRaw, 5)
	if result.Status != "success" {
		t.Fatalf("expected success with allowlisted loopback, got %s (%v)", result.Status, result.ErrorMessage)
	}
}

func startGRPCHealthServer(t *testing.T, withTLS bool, status healthpb.HealthCheckResponse_ServingStatus) (string, int, func()) {
	t.Helper()

	lis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("failed to listen: %v", err)
	}

	var server *grpc.Server
	if withTLS {
		cert := newSelfSignedCert(t)
		server = grpc.NewServer(grpc.Creds(credentials.NewTLS(&tls.Config{Certificates: []tls.Certificate{cert}})))
	} else {
		server = grpc.NewServer()
	}

	healthSrv := health.NewServer()
	healthSrv.SetServingStatus("", status)
	healthpb.RegisterHealthServer(server, healthSrv)

	go func() {
		_ = server.Serve(lis)
	}()

	host, portStr, err := net.SplitHostPort(lis.Addr().String())
	if err != nil {
		t.Fatalf("failed to split addr: %v", err)
	}
	port, err := strconv.Atoi(portStr)
	if err != nil {
		t.Fatalf("failed to parse port: %v", err)
	}

	cleanup := func() {
		server.Stop()
		_ = lis.Close()
	}

	return host, port, cleanup
}

func newSelfSignedCert(t *testing.T) tls.Certificate {
	t.Helper()

	priv, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("failed to generate key: %v", err)
	}

	serialNumber, err := rand.Int(rand.Reader, big.NewInt(1<<62))
	if err != nil {
		t.Fatalf("failed to generate serial: %v", err)
	}

	now := time.Now()
	template := &x509.Certificate{
		SerialNumber: serialNumber,
		Subject: pkix.Name{
			CommonName: "127.0.0.1",
		},
		NotBefore:             now.Add(-time.Hour),
		NotAfter:              now.Add(24 * time.Hour),
		KeyUsage:              x509.KeyUsageKeyEncipherment | x509.KeyUsageDigitalSignature,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		BasicConstraintsValid: true,
		IPAddresses:           []net.IP{net.ParseIP("127.0.0.1")},
		DNSNames:              []string{"localhost"},
	}

	derBytes, err := x509.CreateCertificate(rand.Reader, template, template, &priv.PublicKey, priv)
	if err != nil {
		t.Fatalf("failed to create cert: %v", err)
	}

	certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: derBytes})
	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(priv)})

	cert, err := tls.X509KeyPair(certPEM, keyPEM)
	if err != nil {
		t.Fatalf("failed to build keypair: %v", err)
	}
	return cert
}
