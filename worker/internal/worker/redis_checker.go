package worker

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"net"
	"strconv"
	"strings"
	"time"

	"github.com/redis/go-redis/v9"

	"github.com/yassinebenameur/probara/shared/models"
)

// RedisChecker implements Checker for Redis monitors: connect, AUTH if
// configured, PING, and an optional latency assertion.
type RedisChecker struct {
	blockPrivateIPs bool
	allowedCIDRs    []*net.IPNet
}

// NewRedisChecker creates a new Redis checker
func NewRedisChecker(blockPrivateIPs bool, allowedCIDRs []*net.IPNet) *RedisChecker {
	return &RedisChecker{
		blockPrivateIPs: blockPrivateIPs,
		allowedCIDRs:    allowedCIDRs,
	}
}

// Check performs a Redis check
func (c *RedisChecker) Check(ctx context.Context, configRaw json.RawMessage, timeoutSeconds int) CheckResult {
	var config models.RedisMonitorConfig
	if err := json.Unmarshal(configRaw, &config); err != nil {
		return dbErrorResult("config", fmt.Errorf("failed to unmarshal redis config: %w", err), nil)
	}

	timeout := time.Duration(timeoutSeconds) * time.Second
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	var opts *redis.Options
	if config.ConnectionString != "" {
		parsed, err := redis.ParseURL(config.ConnectionString)
		if err != nil {
			return dbErrorResult("config", fmt.Errorf("invalid connection_string: %w", err), nil)
		}
		opts = parsed
	} else {
		port := config.Port
		if port == 0 {
			port = 6379
		}
		opts = &redis.Options{
			Addr:     net.JoinHostPort(config.Host, strconv.Itoa(port)),
			Username: config.Username,
			Password: config.Password,
			DB:       config.DB,
		}
		if config.TLSEnabled != nil && *config.TLSEnabled {
			opts.TLSConfig = &tls.Config{ServerName: config.Host}
		}
	}

	tlsCfg, err := applyDBTLSMaterial(opts.TLSConfig, config.DBTLSConfig, config.Host)
	if err != nil {
		return dbErrorResult("config", err, nil)
	}
	opts.TLSConfig = tlsCfg

	if config.TLSSkipVerify != nil && *config.TLSSkipVerify && opts.TLSConfig != nil {
		opts.TLSConfig.InsecureSkipVerify = true
	}

	opts.DialTimeout = timeout
	opts.ReadTimeout = timeout
	opts.WriteTimeout = timeout
	opts.MaxRetries = -1 // single attempt; the scheduler owns retry cadence
	opts.Dialer = newDialGuard(c.blockPrivateIPs, c.allowedCIDRs, timeout).DialContext

	client := redis.NewClient(opts)
	defer client.Close()

	startTime := time.Now()
	err = client.Ping(ctx).Err()
	latencyMs := time.Since(startTime).Milliseconds()

	if err != nil {
		return dbErrorResult(classifyDBError(err), err, &latencyMs)
	}

	// INFO is best-effort enrichment: role/version/clients/memory for the
	// detail panel and the optional role assertion. ACL users without INFO
	// permission still get a working PING monitor.
	metrics := &dbMetrics{}
	info, infoErr := client.Info(ctx, "server", "replication", "clients", "memory").Result()
	if infoErr == nil {
		fields := parseRedisInfo(info)
		metrics.ServerVersion = fields["redis_version"]
		metrics.Role = fields["role"]
		if v, err := strconv.ParseInt(fields["connected_clients"], 10, 64); err == nil {
			metrics.ConnectedClients = &v
		}
		if v, err := strconv.ParseInt(fields["used_memory"], 10, 64); err == nil {
			metrics.UsedMemoryBytes = &v
		}
	}
	latencyMs = time.Since(startTime).Milliseconds()

	if config.ExpectedRole != "" {
		if infoErr != nil {
			return dbFailureResult("redis", latencyMs, metrics, fmt.Sprintf("role: expected %q but INFO failed: %v", config.ExpectedRole, infoErr))
		}
		// Redis 7 reports "master"/"slave"; accept the modern "replica" alias.
		observed := metrics.Role
		expected := config.ExpectedRole
		if expected == "replica" {
			expected = "slave"
		}
		if observed != expected {
			display := observed
			if display == "slave" {
				display = "replica"
			}
			return dbFailureResult("redis", latencyMs, metrics, fmt.Sprintf("role: %s (expected %s)", display, config.ExpectedRole))
		}
	}

	return dbLatencyResult("redis", latencyMs, config.MaxLatencyMs, config.WarnLatencyMs, metrics)
}

// parseRedisInfo extracts key:value lines from an INFO response.
func parseRedisInfo(info string) map[string]string {
	fields := make(map[string]string)
	for _, line := range strings.Split(info, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if key, value, ok := strings.Cut(line, ":"); ok {
			fields[key] = value
		}
	}
	return fields
}
