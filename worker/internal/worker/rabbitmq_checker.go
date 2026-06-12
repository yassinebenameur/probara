package worker

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"net"
	"time"

	amqp "github.com/rabbitmq/amqp091-go"

	"github.com/yassinebenameur/probara/shared/models"
)

// RabbitMQChecker implements Checker for RabbitMQ monitors: a full AMQP
// handshake (connect, auth, vhost access), which catches broker failures —
// auth backend down, vhost gone, memory alarm refusing connections — that a
// plain TCP probe can't see.
type RabbitMQChecker struct {
	blockPrivateIPs bool
	allowedCIDRs    []*net.IPNet
}

// NewRabbitMQChecker creates a new RabbitMQ checker
func NewRabbitMQChecker(blockPrivateIPs bool, allowedCIDRs []*net.IPNet) *RabbitMQChecker {
	return &RabbitMQChecker{
		blockPrivateIPs: blockPrivateIPs,
		allowedCIDRs:    allowedCIDRs,
	}
}

// Check performs a RabbitMQ check
func (c *RabbitMQChecker) Check(ctx context.Context, configRaw json.RawMessage, timeoutSeconds int) CheckResult {
	var config models.RabbitMQMonitorConfig
	if err := json.Unmarshal(configRaw, &config); err != nil {
		return dbErrorResult("config", fmt.Errorf("failed to unmarshal rabbitmq config: %w", err), nil)
	}

	timeout := time.Duration(timeoutSeconds) * time.Second
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	connString := config.ConnectionString
	useTLS := config.TLSEnabled != nil && *config.TLSEnabled
	if connString == "" {
		uri := amqp.URI{
			Scheme:   "amqp",
			Host:     config.Host,
			Port:     5672,
			Username: config.Username,
			Password: config.Password,
			Vhost:    "/",
		}
		if useTLS {
			uri.Scheme = "amqps"
			uri.Port = 5671
		}
		if config.Port != 0 {
			uri.Port = config.Port
		}
		if config.VHost != "" {
			uri.Vhost = config.VHost
		}
		connString = uri.String()
	}

	parsedURI, err := amqp.ParseURI(connString)
	if err != nil {
		return dbErrorResult("config", fmt.Errorf("invalid connection_string: %w", err), nil)
	}

	var tlsCfg *tls.Config
	if parsedURI.Scheme == "amqps" {
		tlsCfg = &tls.Config{ServerName: parsedURI.Host}
	}
	tlsCfg, err = applyDBTLSMaterial(tlsCfg, config.DBTLSConfig, parsedURI.Host)
	if err != nil {
		return dbErrorResult("config", err, nil)
	}
	if config.TLSSkipVerify != nil && *config.TLSSkipVerify && tlsCfg != nil {
		tlsCfg.InsecureSkipVerify = true
	}

	guard := newDialGuard(c.blockPrivateIPs, c.allowedCIDRs, timeout)
	amqpCfg := amqp.Config{
		Vhost:           parsedURI.Vhost,
		TLSClientConfig: tlsCfg,
		Dial: func(network, addr string) (net.Conn, error) {
			return guard.DialContext(ctx, network, addr)
		},
	}

	startTime := time.Now()
	conn, err := amqp.DialConfig(connString, amqpCfg)
	latencyMs := time.Since(startTime).Milliseconds()
	if err != nil {
		return dbErrorResult(classifyDBError(err), err, &latencyMs)
	}
	defer conn.Close()

	metrics := &dbMetrics{}
	if v, ok := conn.Properties["version"].(string); ok {
		metrics.ServerVersion = v
	}
	if p, ok := conn.Properties["product"].(string); ok {
		metrics.Product = p
	}

	return dbLatencyResult("rabbitmq", latencyMs, config.MaxLatencyMs, config.WarnLatencyMs, metrics)
}
