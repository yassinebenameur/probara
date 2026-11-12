package worker

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"net"
	"strings"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/credentials/insecure"
	healthpb "google.golang.org/grpc/health/grpc_health_v1"

	"github.com/yassinebenameur/probara/shared/models"
)

// GRPCChecker implements Checker for gRPC health monitors.
type GRPCChecker struct {
	blockPrivateIPs bool
	allowedCIDRs    []*net.IPNet
}

// NewGRPCChecker creates a new gRPC checker.
func NewGRPCChecker(blockPrivateIPs bool, allowedCIDRs []*net.IPNet) *GRPCChecker {
	return &GRPCChecker{
		blockPrivateIPs: blockPrivateIPs,
		allowedCIDRs:    allowedCIDRs,
	}
}

type grpcMetricsEnvelope struct {
	GRPC *grpcMetrics `json:"grpc,omitempty"`
}

type grpcMetrics struct {
	Target        string `json:"target,omitempty"`
	Host          string `json:"host,omitempty"`
	Port          int    `json:"port,omitempty"`
	Service       string `json:"service,omitempty"`
	UseTLS        bool   `json:"use_tls"`
	ServingStatus string `json:"serving_status,omitempty"`
}

// Check performs a gRPC health check.
func (c *GRPCChecker) Check(ctx context.Context, configRaw json.RawMessage, timeoutSeconds int) CheckResult {
	var config models.GRPCMonitorConfig
	if err := json.Unmarshal(configRaw, &config); err != nil {
		errMsg := fmt.Sprintf("failed to unmarshal grpc config: %v", err)
		return CheckResult{Status: "error", ErrorMessage: &errMsg}
	}

	host := strings.TrimSpace(config.Host)
	if host == "" {
		errMsg := "host is required"
		return CheckResult{Status: "error", ErrorMessage: &errMsg}
	}

	useTLS := true
	if config.UseTLS != nil {
		useTLS = *config.UseTLS
	}

	port := config.Port
	if port == 0 {
		if useTLS {
			port = 443
		} else {
			port = 80
		}
	}

	target := net.JoinHostPort(host, fmt.Sprintf("%d", port))
	service := strings.TrimSpace(config.Service)

	timeout := time.Duration(timeoutSeconds) * time.Second
	checkCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	dialOptions := []grpc.DialOption{grpc.WithBlock()}
	if useTLS {
		tlsCfg := &tls.Config{}
		if host != "" {
			tlsCfg.ServerName = host
		}
		dialOptions = append(dialOptions, grpc.WithTransportCredentials(credentials.NewTLS(tlsCfg)))
	} else {
		dialOptions = append(dialOptions, grpc.WithTransportCredentials(insecure.NewCredentials()))
	}

	if c.blockPrivateIPs {
		dialer := c.safeDialContext(timeout)
		dialOptions = append(dialOptions, grpc.WithContextDialer(dialer))
	}

	startTime := time.Now()
	conn, err := grpc.DialContext(checkCtx, target, dialOptions...)
	latencyMs := time.Since(startTime).Milliseconds()
	if err != nil {
		errMsg := fmt.Sprintf("%s: %v", grpcErrorReason(err), err)
		return CheckResult{
			Status:       "error",
			ErrorMessage: &errMsg,
			LatencyMs:    &latencyMs,
		}
	}
	defer conn.Close()

	client := healthpb.NewHealthClient(conn)
	resp, err := client.Check(checkCtx, &healthpb.HealthCheckRequest{Service: service})
	latencyMs = time.Since(startTime).Milliseconds()
	if err != nil {
		errMsg := fmt.Sprintf("%s: %v", grpcErrorReason(err), err)
		return CheckResult{
			Status:       "error",
			ErrorMessage: &errMsg,
			LatencyMs:    &latencyMs,
		}
	}

	servingStatus := resp.GetStatus().String()
	metricsJSON, _ := json.Marshal(grpcMetricsEnvelope{GRPC: &grpcMetrics{
		Target:        target,
		Host:          host,
		Port:          port,
		Service:       service,
		UseTLS:        useTLS,
		ServingStatus: servingStatus,
	}})

	if resp.GetStatus() != healthpb.HealthCheckResponse_SERVING {
		errMsg := fmt.Sprintf("grpc health status: %s", servingStatus)
		return CheckResult{
			Status:       "failure",
			ErrorMessage: &errMsg,
			LatencyMs:    &latencyMs,
			MetricsData:  metricsJSON,
		}
	}

	return CheckResult{
		Status:      "success",
		LatencyMs:   &latencyMs,
		MetricsData: metricsJSON,
	}
}

func grpcErrorReason(err error) string {
	errStr := strings.ToLower(err.Error())
	switch {
	case strings.Contains(errStr, "timeout"), strings.Contains(errStr, "deadline"):
		return "timeout"
	case strings.Contains(errStr, "no such host"), strings.Contains(errStr, "dns"):
		return "dns"
	case strings.Contains(errStr, "connection refused"), strings.Contains(errStr, "connect"):
		return "connect"
	default:
		return "grpc"
	}
}

// safeDialContext adapts the shared SSRF dial guard to gRPC's dialer shape.
func (c *GRPCChecker) safeDialContext(timeout time.Duration) func(context.Context, string) (net.Conn, error) {
	guard := newDialGuard(c.blockPrivateIPs, c.allowedCIDRs, timeout)
	return func(ctx context.Context, address string) (net.Conn, error) {
		return guard.DialContext(ctx, "tcp", address)
	}
}
