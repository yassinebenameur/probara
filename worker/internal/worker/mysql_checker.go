package worker

import (
	"context"
	"crypto/tls"
	"database/sql"
	"encoding/json"
	"fmt"
	"net"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/go-sql-driver/mysql"

	"github.com/yassinebenameur/probara/shared/models"
)

// mysqlGuardNet is the network name the checker's dial guard is registered
// under with the driver (mysql.RegisterDialContext is a package-global
// registry, so the checker rewrites Config.Net to this name).
const mysqlGuardNet = "probara-tcp"

// MySQLChecker implements Checker for MySQL/MariaDB monitors: connect,
// authenticate, optionally run an assertion query, and measure latency.
type MySQLChecker struct {
	blockPrivateIPs bool
	allowedCIDRs    []*net.IPNet
}

// NewMySQLChecker creates a new MySQL checker
func NewMySQLChecker(blockPrivateIPs bool, allowedCIDRs []*net.IPNet) *MySQLChecker {
	return &MySQLChecker{
		blockPrivateIPs: blockPrivateIPs,
		allowedCIDRs:    allowedCIDRs,
	}
}

// Check performs a MySQL check
func (c *MySQLChecker) Check(ctx context.Context, configRaw json.RawMessage, timeoutSeconds int) CheckResult {
	var config models.MySQLMonitorConfig
	if err := json.Unmarshal(configRaw, &config); err != nil {
		return dbErrorResult("config", fmt.Errorf("failed to unmarshal mysql config: %w", err), nil)
	}

	timeout := time.Duration(timeoutSeconds) * time.Second
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	cfg, err := buildMySQLConfig(&config)
	if err != nil {
		return dbErrorResult("config", err, nil)
	}
	cfg.Timeout = timeout
	cfg.ReadTimeout = timeout
	cfg.WriteTimeout = timeout

	if cfg.TLS != nil {
		host, _, splitErr := net.SplitHostPort(cfg.Addr)
		if splitErr != nil {
			host = cfg.Addr
		}
		patched, err := applyDBTLSMaterial(cfg.TLS, config.DBTLSConfig, host)
		if err != nil {
			return dbErrorResult("config", err, nil)
		}
		if config.TLSSkipVerify != nil && *config.TLSSkipVerify {
			patched.InsecureSkipVerify = true
		}
		cfg.TLS = patched
	}

	guard := newDialGuard(c.blockPrivateIPs, c.allowedCIDRs, timeout)
	mysql.RegisterDialContext(mysqlGuardNet, func(ctx context.Context, addr string) (net.Conn, error) {
		return guard.DialContext(ctx, "tcp", addr)
	})
	cfg.Net = mysqlGuardNet

	connector, err := mysql.NewConnector(cfg)
	if err != nil {
		return dbErrorResult("config", err, nil)
	}
	db := sql.OpenDB(connector)
	defer db.Close()
	db.SetMaxOpenConns(1)

	startTime := time.Now()
	if err := db.PingContext(ctx); err != nil {
		latencyMs := time.Since(startTime).Milliseconds()
		return dbErrorResult(classifyDBError(err), err, &latencyMs)
	}

	// Version is best-effort enrichment; a locked-down monitoring user still
	// gets a working ping monitor.
	metrics := &dbMetrics{}
	if err := db.QueryRowContext(ctx, "SELECT VERSION()").Scan(&metrics.ServerVersion); err != nil {
		metrics.ServerVersion = ""
	}

	if config.Query != nil && strings.TrimSpace(*config.Query) != "" {
		rows, err := db.QueryContext(ctx, *config.Query)
		if err != nil {
			latencyMs := time.Since(startTime).Milliseconds()
			return dbErrorResult("query", err, &latencyMs)
		}
		rowCount := 0
		firstValue := ""
		for rows.Next() {
			if rowCount == 0 {
				if cols, err := rows.Columns(); err == nil && len(cols) > 0 {
					var raw any
					dest := make([]any, len(cols))
					dest[0] = &raw
					for i := 1; i < len(dest); i++ {
						dest[i] = new(any)
					}
					if err := rows.Scan(dest...); err == nil {
						firstValue = mysqlValueString(raw)
					}
				}
			}
			rowCount++
		}
		rows.Close()
		latencyMs := time.Since(startTime).Milliseconds()
		if err := rows.Err(); err != nil {
			return dbErrorResult("query", err, &latencyMs)
		}
		if rowCount == 0 {
			return dbFailureResult("mysql", latencyMs, metrics, "query: returned 0 rows")
		}
		if config.QueryValueOp != "" {
			ok, err := evaluateValueAssertion(config.QueryValueOp, firstValue, config.QueryValue)
			if err != nil {
				return dbErrorResult("query_value", err, &latencyMs)
			}
			if !ok {
				return dbFailureResult("mysql", latencyMs, metrics,
					fmt.Sprintf("query_value: %q %s %q failed", firstValue, config.QueryValueOp, config.QueryValue))
			}
		}
	}

	latencyMs := time.Since(startTime).Milliseconds()
	return dbLatencyResult("mysql", latencyMs, config.MaxLatencyMs, config.WarnLatencyMs, metrics)
}

// mysqlValueString stringifies a scanned column value; the driver returns
// []byte for most text/numeric columns.
func mysqlValueString(v any) string {
	switch t := v.(type) {
	case nil:
		return ""
	case []byte:
		return string(t)
	default:
		return fmt.Sprint(t)
	}
}

// buildMySQLConfig assembles a mysql.Config from either a connection string
// (mysql:// URI or native go-sql-driver DSN) or the discrete fields.
func buildMySQLConfig(config *models.MySQLMonitorConfig) (*mysql.Config, error) {
	if cs := strings.TrimSpace(config.ConnectionString); cs != "" {
		var cfg *mysql.Config
		var err error
		if strings.HasPrefix(cs, "mysql://") {
			cfg, err = mysqlConfigFromURI(cs)
		} else {
			cfg, err = mysql.ParseDSN(cs)
		}
		if err != nil {
			return nil, fmt.Errorf("invalid connection_string: %w", err)
		}
		if cfg.Net != "" && cfg.Net != "tcp" {
			return nil, fmt.Errorf("only tcp connections are supported (got %q)", cfg.Net)
		}
		if config.TLSSkipVerify != nil && *config.TLSSkipVerify && cfg.TLS == nil {
			cfg.TLS = &tls.Config{InsecureSkipVerify: true}
		}
		return cfg, nil
	}

	port := config.Port
	if port == 0 {
		port = 3306
	}
	cfg := mysql.NewConfig()
	cfg.Addr = net.JoinHostPort(config.Host, strconv.Itoa(port))
	cfg.User = config.Username
	cfg.Passwd = config.Password
	cfg.DBName = config.Database
	if config.TLSEnabled != nil && *config.TLSEnabled {
		cfg.TLS = &tls.Config{ServerName: config.Host}
	}
	return cfg, nil
}

// mysqlConfigFromURI maps a mysql:// URI onto mysql.Config. The driver's own
// DSN format is not a URI, but URIs are what people copy out of cloud
// consoles — accept both.
func mysqlConfigFromURI(uri string) (*mysql.Config, error) {
	u, err := url.Parse(uri)
	if err != nil {
		return nil, err
	}
	host := u.Hostname()
	if host == "" {
		return nil, fmt.Errorf("missing host")
	}
	port := u.Port()
	if port == "" {
		port = "3306"
	}

	cfg := mysql.NewConfig()
	cfg.Addr = net.JoinHostPort(host, port)
	cfg.DBName = strings.TrimPrefix(u.Path, "/")
	if u.User != nil {
		cfg.User = u.User.Username()
		cfg.Passwd, _ = u.User.Password()
	}
	// tls=true|skip-verify query params mirror the driver's DSN parameters.
	switch strings.ToLower(u.Query().Get("tls")) {
	case "true", "1":
		cfg.TLS = &tls.Config{ServerName: host}
	case "skip-verify":
		cfg.TLS = &tls.Config{InsecureSkipVerify: true}
	}
	return cfg, nil
}
