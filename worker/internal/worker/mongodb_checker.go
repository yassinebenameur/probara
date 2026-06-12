package worker

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"net"
	"strconv"
	"time"

	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
	"go.mongodb.org/mongo-driver/v2/mongo/readpref"

	"github.com/yassinebenameur/probara/shared/models"
)

// MongoDBChecker implements Checker for MongoDB monitors: connect,
// authenticate if configured, and run a ping against the server.
type MongoDBChecker struct {
	blockPrivateIPs bool
	allowedCIDRs    []*net.IPNet
}

// NewMongoDBChecker creates a new MongoDB checker
func NewMongoDBChecker(blockPrivateIPs bool, allowedCIDRs []*net.IPNet) *MongoDBChecker {
	return &MongoDBChecker{
		blockPrivateIPs: blockPrivateIPs,
		allowedCIDRs:    allowedCIDRs,
	}
}

// Check performs a MongoDB check
func (c *MongoDBChecker) Check(ctx context.Context, configRaw json.RawMessage, timeoutSeconds int) CheckResult {
	var config models.MongoDBMonitorConfig
	if err := json.Unmarshal(configRaw, &config); err != nil {
		return dbErrorResult("config", fmt.Errorf("failed to unmarshal mongodb config: %w", err), nil)
	}

	timeout := time.Duration(timeoutSeconds) * time.Second
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	opts := options.Client()
	if config.ConnectionString != "" {
		opts.ApplyURI(config.ConnectionString)
	} else {
		port := config.Port
		if port == 0 {
			port = 27017
		}
		opts.SetHosts([]string{net.JoinHostPort(config.Host, strconv.Itoa(port))})
		// Direct connection: probe the configured host, not whatever
		// topology it advertises.
		opts.SetDirect(true)
		if config.Username != "" {
			authSource := config.AuthSource
			if authSource == "" {
				authSource = "admin"
			}
			opts.SetAuth(options.Credential{
				Username:   config.Username,
				Password:   config.Password,
				AuthSource: authSource,
			})
		}
		if config.TLSEnabled != nil && *config.TLSEnabled {
			opts.SetTLSConfig(&tls.Config{ServerName: config.Host})
		}
	}

	tlsCfg, err := applyDBTLSMaterial(opts.TLSConfig, config.DBTLSConfig, config.Host)
	if err != nil {
		return dbErrorResult("config", err, nil)
	}
	if tlsCfg != nil {
		opts.SetTLSConfig(tlsCfg)
	}

	if config.TLSSkipVerify != nil && *config.TLSSkipVerify && opts.TLSConfig != nil {
		opts.TLSConfig.InsecureSkipVerify = true
	}

	opts.SetConnectTimeout(timeout)
	opts.SetServerSelectionTimeout(timeout)
	opts.SetTimeout(timeout)
	opts.SetRetryReads(false)
	opts.SetDialer(newDialGuard(c.blockPrivateIPs, c.allowedCIDRs, timeout))

	startTime := time.Now()
	client, err := mongo.Connect(opts)
	if err != nil {
		latencyMs := time.Since(startTime).Milliseconds()
		return dbErrorResult(classifyDBError(err), err, &latencyMs)
	}
	defer func() {
		disconnectCtx, disconnectCancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		defer disconnectCancel()
		_ = client.Disconnect(disconnectCtx)
	}()

	err = client.Ping(ctx, readpref.Primary())
	latencyMs := time.Since(startTime).Milliseconds()
	if err != nil {
		return dbErrorResult(classifyDBError(err), err, &latencyMs)
	}

	return dbLatencyResult(latencyMs, config.MaxLatencyMs)
}
