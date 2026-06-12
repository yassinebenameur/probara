package worker

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/yassinebenameur/probara/shared/models"
)

// PostgresChecker implements Checker for PostgreSQL monitors: connect,
// authenticate, optionally run an assertion query, and measure latency.
type PostgresChecker struct {
	blockPrivateIPs bool
	allowedCIDRs    []*net.IPNet
}

// NewPostgresChecker creates a new PostgreSQL checker
func NewPostgresChecker(blockPrivateIPs bool, allowedCIDRs []*net.IPNet) *PostgresChecker {
	return &PostgresChecker{
		blockPrivateIPs: blockPrivateIPs,
		allowedCIDRs:    allowedCIDRs,
	}
}

type postgresMetrics struct {
	ServerVersion string `json:"server_version,omitempty"`
}

type postgresMetricsEnvelope struct {
	Postgres *postgresMetrics `json:"postgres,omitempty"`
}

// Check performs a PostgreSQL check
func (c *PostgresChecker) Check(ctx context.Context, configRaw json.RawMessage, timeoutSeconds int) CheckResult {
	var config models.PostgresMonitorConfig
	if err := json.Unmarshal(configRaw, &config); err != nil {
		return dbErrorResult("config", fmt.Errorf("failed to unmarshal postgres config: %w", err), nil)
	}

	timeout := time.Duration(timeoutSeconds) * time.Second
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	connString := config.ConnectionString
	if connString == "" {
		connString = buildPostgresURI(&config)
	}

	connCfg, err := pgx.ParseConfig(connString)
	if err != nil {
		return dbErrorResult("config", fmt.Errorf("invalid connection config: %w", err), nil)
	}
	connCfg.ConnectTimeout = timeout
	connCfg.DialFunc = newDialGuard(c.blockPrivateIPs, c.allowedCIDRs, timeout).DialContext

	// Pasted TLS material (private CA, mTLS pair) is merged into every TLS
	// attempt pgx derived from sslmode — including fallback configs. Plaintext
	// attempts (sslmode=disable, prefer-fallback) are left alone.
	if connCfg.TLSConfig != nil {
		patched, err := applyDBTLSMaterial(connCfg.TLSConfig, config.DBTLSConfig, connCfg.Host)
		if err != nil {
			return dbErrorResult("config", err, nil)
		}
		connCfg.TLSConfig = patched
	}
	for _, fallback := range connCfg.Fallbacks {
		if fallback == nil || fallback.TLSConfig == nil {
			continue
		}
		patched, err := applyDBTLSMaterial(fallback.TLSConfig, config.DBTLSConfig, fallback.Host)
		if err != nil {
			return dbErrorResult("config", err, nil)
		}
		fallback.TLSConfig = patched
	}

	startTime := time.Now()
	conn, err := pgx.ConnectConfig(ctx, connCfg)
	if err != nil {
		latencyMs := time.Since(startTime).Milliseconds()
		return dbErrorResult(classifyDBError(err), err, &latencyMs)
	}
	defer conn.Close(context.WithoutCancel(ctx))

	if config.Query != nil && strings.TrimSpace(*config.Query) != "" {
		rows, err := conn.Query(ctx, *config.Query)
		if err != nil {
			latencyMs := time.Since(startTime).Milliseconds()
			return dbErrorResult("query", err, &latencyMs)
		}
		rowCount := 0
		for rows.Next() {
			rowCount++
		}
		rows.Close()
		latencyMs := time.Since(startTime).Milliseconds()
		if err := rows.Err(); err != nil {
			return dbErrorResult("query", err, &latencyMs)
		}
		if rowCount == 0 {
			errMsg := "query: returned 0 rows"
			return CheckResult{
				Status:       "failure",
				LatencyMs:    &latencyMs,
				ErrorMessage: &errMsg,
			}
		}
	} else if err := conn.Ping(ctx); err != nil {
		latencyMs := time.Since(startTime).Milliseconds()
		return dbErrorResult(classifyDBError(err), err, &latencyMs)
	}

	latencyMs := time.Since(startTime).Milliseconds()
	result := dbLatencyResult(latencyMs, config.MaxLatencyMs)

	if version := conn.PgConn().ParameterStatus("server_version"); version != "" {
		if metricsJSON, err := json.Marshal(postgresMetricsEnvelope{Postgres: &postgresMetrics{ServerVersion: version}}); err == nil {
			result.MetricsData = metricsJSON
		}
	}

	return result
}

// buildPostgresURI assembles a postgres:// URI from discrete config fields.
// url.URL handles escaping, so credentials with special characters survive.
func buildPostgresURI(config *models.PostgresMonitorConfig) string {
	port := config.Port
	if port == 0 {
		port = 5432
	}
	database := config.Database
	if database == "" {
		database = "postgres"
	}
	// Default to "prefer": try TLS, fall back to plaintext. Explicit modes
	// (disable, require, verify-full) come from config. A pasted CA implies
	// the certificate should actually be verified — pgx only checks RootCAs
	// under verify-full (require sets InsecureSkipVerify).
	sslMode := config.SSLMode
	if sslMode == "" {
		sslMode = "prefer"
	}
	if pemValue(config.TLSCAPem) != "" && sslMode != "disable" {
		sslMode = "verify-full"
	}

	u := url.URL{
		Scheme:   "postgres",
		Host:     net.JoinHostPort(config.Host, strconv.Itoa(port)),
		Path:     "/" + database,
		RawQuery: url.Values{"sslmode": []string{sslMode}}.Encode(),
	}
	if config.Username != "" {
		if config.Password != "" {
			u.User = url.UserPassword(config.Username, config.Password)
		} else {
			u.User = url.User(config.Username)
		}
	}
	return u.String()
}
