package models

import "encoding/json"

type HTTPStatusRange struct {
	Min int `json:"min"`
	Max int `json:"max"`
}

type HTTPHeaderAssertion struct {
	Name            string  `json:"name"`
	Op              string  `json:"op"` // exists, equals, contains, regex, not_equals, not_contains, not_regex
	Value           *string `json:"value,omitempty"`
	CaseInsensitive *bool   `json:"case_insensitive,omitempty"`
}

type HTTPBodyAssertion struct {
	Op              string `json:"op"` // contains, not_contains, regex, not_regex
	Value           string `json:"value"`
	CaseInsensitive *bool  `json:"case_insensitive,omitempty"`
}

type HTTPJSONAssertion struct {
	Path            string  `json:"path"` // gjson path
	Op              string  `json:"op"`   // exists, equals, not_equals, contains, not_contains, regex, number_gt, number_gte, number_lt, number_lte, bool_is
	Value           *string `json:"value,omitempty"`
	CaseInsensitive *bool   `json:"case_insensitive,omitempty"`
}

// HTTPMonitorConfig represents configuration for HTTP monitors
type HTTPMonitorConfig struct {
	URL                      string                `json:"url"`
	Method                   string                `json:"method"`
	Headers                  map[string]string     `json:"headers,omitempty"`
	Body                     *string               `json:"body,omitempty"`
	ExpectedStatus           *int                  `json:"expected_status,omitempty"`
	ExpectedStatuses         []int                 `json:"expected_statuses,omitempty"`
	ExpectedStatusRanges     []HTTPStatusRange     `json:"expected_status_ranges,omitempty"`
	ExpectedStatusClasses    []string              `json:"expected_status_classes,omitempty"` // e.g. ["2xx", "3xx"]
	ExpectedBodySubstring    *string               `json:"expected_body_substring,omitempty"`
	ExpectedBodyRegex        *string               `json:"expected_body_regex,omitempty"`
	BodyAssertions           []HTTPBodyAssertion   `json:"body_assertions,omitempty"`
	ResponseHeaderAssertions []HTTPHeaderAssertion `json:"response_header_assertions,omitempty"`
	JSONAssertions           []HTTPJSONAssertion   `json:"json_assertions,omitempty"`
	MaxLatencyMs             *int64                `json:"max_latency_ms,omitempty"`
	FollowRedirects          *bool                 `json:"follow_redirects,omitempty"`
	MaxRedirects             *int                  `json:"max_redirects,omitempty"`
	TLSSkipVerify            *bool                 `json:"tls_skip_verify,omitempty"`
	TLSMinDaysValid          *int                  `json:"tls_min_days_valid,omitempty"`
	TLSServerName            *string               `json:"tls_server_name,omitempty"`
	TLSCAPem                 *string               `json:"tls_ca_pem,omitempty"`
	CollectTiming            *bool                 `json:"collect_timing,omitempty"`
}

// PingMonitorConfig represents configuration for ping monitors
type PingMonitorConfig struct {
	Host string `json:"host"`
}

// DNSMonitorConfig represents configuration for DNS monitors
type DNSMonitorConfig struct {
	Host            string   `json:"host"`
	RecordType      string   `json:"record_type,omitempty"`      // A, AAAA, CNAME, TXT, MX, NS
	ExpectedAnswers []string `json:"expected_answers,omitempty"` // Optional expected answers
	// Nameserver targets a specific DNS server (host or host:port, default
	// port 53) instead of the worker's system resolver — validates private
	// zones and VPC resolvers the default resolver can't see.
	Nameserver string `json:"nameserver,omitempty"`
}

// GRPCMonitorConfig represents configuration for gRPC health monitors
type GRPCMonitorConfig struct {
	Host    string `json:"host"`
	Port    int    `json:"port,omitempty"`
	Service string `json:"service,omitempty"`
	UseTLS  *bool  `json:"use_tls,omitempty"`
}

// TCPMonitorConfig represents configuration for raw TCP connect monitors. It
// validates that a host:port accepts a connection (route/SG/listener
// reachability), optionally completing a TLS handshake.
type TCPMonitorConfig struct {
	Host          string `json:"host"`
	Port          int    `json:"port"`
	UseTLS        *bool  `json:"use_tls,omitempty"`         // perform a TLS handshake after connecting
	TLSSkipVerify *bool  `json:"tls_skip_verify,omitempty"` // accept self-signed / mismatched certs
}

// GroupMonitorConfig represents configuration for group monitors
type GroupMonitorConfig struct {
	MonitorIDs []string `json:"monitor_ids"`
}

// SIPMonitorConfig represents configuration for SIP monitors
type SIPMonitorConfig struct {
	Host           string `json:"host"`                      // SIP server hostname/IP
	Port           int    `json:"port"`                      // Default 5060
	Transport      string `json:"transport"`                 // "udp" or "tcp" (default: udp)
	ExpectedStatus *int   `json:"expected_status,omitempty"` // Expected SIP response (default: 200)
}

// SyntheticAPIMonitorConfig represents configuration for synthetic API workflow monitors
type SyntheticAPIMonitorConfig struct {
	BaseURL     string                   `json:"base_url,omitempty"`
	FailureMode string                   `json:"failure_mode,omitempty"` // fail_fast (default) | continue
	Variables   map[string]string        `json:"variables,omitempty"`
	Steps       []SyntheticAPIStepConfig `json:"steps"`
}

type SyntheticAPIStepConfig struct {
	ID      string                        `json:"id"`
	Name    string                        `json:"name,omitempty"`
	Request SyntheticAPIRequestConfig     `json:"request"`
	Assert  []SyntheticAPIAssertionConfig `json:"assert,omitempty"`
	Extract []SyntheticAPIExtractConfig   `json:"extract,omitempty"`
}

type SyntheticAPIRequestConfig struct {
	Method          string            `json:"method"`
	URL             string            `json:"url"`
	Headers         map[string]string `json:"headers,omitempty"`
	Body            *string           `json:"body,omitempty"`
	TimeoutSeconds  *int              `json:"timeout_seconds,omitempty"`
	FollowRedirects *bool             `json:"follow_redirects,omitempty"`
	MaxRedirects    *int              `json:"max_redirects,omitempty"`
}

type SyntheticAPIAssertionConfig struct {
	Target string          `json:"target"` // status | header | body | json
	Op     string          `json:"op"`     // target-dependent
	Path   string          `json:"path,omitempty"`
	Value  json.RawMessage `json:"value,omitempty"`
}

type SyntheticAPIExtractConfig struct {
	Name      string `json:"name"`
	From      string `json:"from"` // json | header
	Path      string `json:"path"`
	Sensitive *bool  `json:"sensitive,omitempty"`
}

// SyntheticBrowserMonitorConfig represents configuration for synthetic browser journey monitors
type SyntheticBrowserMonitorConfig struct {
	StartURL    string                       `json:"start_url"`
	Device      string                       `json:"device,omitempty"`
	FailureMode string                       `json:"failure_mode,omitempty"` // fail_fast (default) | continue
	Variables   map[string]string            `json:"variables,omitempty"`
	Artifacts   SyntheticBrowserArtifacts    `json:"artifacts,omitempty"`
	Steps       []SyntheticBrowserStepConfig `json:"steps"`
}

type SyntheticBrowserArtifacts struct {
	ScreenshotOnFailure bool `json:"screenshot_on_failure,omitempty"`
	TraceOnFailure      bool `json:"trace_on_failure,omitempty"`
	HarOnFailure        bool `json:"har_on_failure,omitempty"`
}

type SyntheticBrowserStepConfig struct {
	ID             string  `json:"id"`
	Action         string  `json:"action"` // goto | click | fill | wait_for | assert_visible | assert_text | assert_url
	URL            string  `json:"url,omitempty"`
	Selector       string  `json:"selector,omitempty"`
	Value          *string `json:"value,omitempty"`
	TimeoutSeconds *int    `json:"timeout_seconds,omitempty"`
}

// DBTLSConfig holds PEM-pasted TLS material shared by the database monitor
// types. CA PEM covers private-CA server verification; the client pair covers
// mutual TLS / X.509 auth. PEMs are pasted (not file paths) because checks run
// on shared workers with no tenant filesystem. `tls_client_key_pem` is a
// secret field (see shared/secrets.MonitorSecretFields).
type DBTLSConfig struct {
	TLSCAPem         *string `json:"tls_ca_pem,omitempty"`
	TLSClientCertPem *string `json:"tls_client_cert_pem,omitempty"`
	TLSClientKeyPem  *string `json:"tls_client_key_pem,omitempty"`
}

// RedisMonitorConfig represents configuration for Redis monitors. Connection
// is configured either via `connection_string` (redis:// or rediss:// URI) or
// via the discrete host/port/credential fields. `password`,
// `connection_string`, and `tls_client_key_pem` are secret fields (see
// shared/secrets.MonitorSecretFields).
type RedisMonitorConfig struct {
	ConnectionString string `json:"connection_string,omitempty"`
	Host             string `json:"host,omitempty"`
	Port             int    `json:"port,omitempty"`     // default 6379
	Username         string `json:"username,omitempty"` // ACL user; empty = default user
	Password         string `json:"password,omitempty"`
	DB               int    `json:"db,omitempty"`
	TLSEnabled       *bool  `json:"tls_enabled,omitempty"`
	TLSSkipVerify    *bool  `json:"tls_skip_verify,omitempty"` // also applies on top of a rediss:// URI
	DBTLSConfig
	ExpectedRole string `json:"expected_role,omitempty"` // master | replica — catches monitoring the wrong node after failover
	MaxLatencyMs *int64 `json:"max_latency_ms,omitempty"`
	// WarnLatencyMs marks the check result with a latency warning (visible in
	// the UI) without failing it. Must be lower than max_latency_ms when both
	// are set.
	WarnLatencyMs *int64 `json:"warn_latency_ms,omitempty"`
}

// PostgresMonitorConfig represents configuration for PostgreSQL monitors.
// Connection is configured either via `connection_string` (postgres:// URI or
// key=value DSN) or via the discrete fields. `password` and
// `connection_string` are secret fields (see shared/secrets.MonitorSecretFields).
type PostgresMonitorConfig struct {
	ConnectionString string `json:"connection_string,omitempty"`
	Host             string `json:"host,omitempty"`
	Port             int    `json:"port,omitempty"`     // default 5432
	Database         string `json:"database,omitempty"` // default "postgres"
	Username         string `json:"username,omitempty"`
	Password         string `json:"password,omitempty"`
	SSLMode          string `json:"ssl_mode,omitempty"` // disable | require | verify-full (default: prefer)
	DBTLSConfig
	Query *string `json:"query,omitempty"` // optional assertion: must return >= 1 row
	// QueryValueOp/QueryValue assert on the first column of the query's first
	// row: equals | not_equals | contains | number_gt | number_gte |
	// number_lt | number_lte. Empty op = row-count assertion only.
	QueryValueOp  string `json:"query_value_op,omitempty"`
	QueryValue    string `json:"query_value,omitempty"`
	MaxLatencyMs  *int64 `json:"max_latency_ms,omitempty"`
	WarnLatencyMs *int64 `json:"warn_latency_ms,omitempty"`
}

// MongoDBMonitorConfig represents configuration for MongoDB monitors.
// Connection is configured either via `connection_string` (mongodb:// or
// mongodb+srv:// URI) or via the discrete fields. `password` and
// `connection_string` are secret fields (see shared/secrets.MonitorSecretFields).
type MongoDBMonitorConfig struct {
	ConnectionString string `json:"connection_string,omitempty"`
	Host             string `json:"host,omitempty"`
	Port             int    `json:"port,omitempty"` // default 27017
	Username         string `json:"username,omitempty"`
	Password         string `json:"password,omitempty"`
	AuthSource       string `json:"auth_source,omitempty"` // default "admin"
	TLSEnabled       *bool  `json:"tls_enabled,omitempty"`
	TLSSkipVerify    *bool  `json:"tls_skip_verify,omitempty"` // also applies on top of a tls=true URI
	DBTLSConfig
	// ReplicaSet switches from a direct single-node probe to replica-set
	// monitoring: the driver discovers the topology and the ping asserts a
	// reachable primary (i.e. the set can elect and serve writes).
	ReplicaSet    string `json:"replica_set,omitempty"`
	MaxLatencyMs  *int64 `json:"max_latency_ms,omitempty"`
	WarnLatencyMs *int64 `json:"warn_latency_ms,omitempty"`
}

// MySQLMonitorConfig represents configuration for MySQL/MariaDB monitors.
// Connection is configured either via `connection_string` (mysql:// URI or a
// go-sql-driver DSN like user:pass@tcp(host:3306)/db) or via the discrete
// fields. `password` and `connection_string` are secret fields (see
// shared/secrets.MonitorSecretFields).
type MySQLMonitorConfig struct {
	ConnectionString string `json:"connection_string,omitempty"`
	Host             string `json:"host,omitempty"`
	Port             int    `json:"port,omitempty"`     // default 3306
	Database         string `json:"database,omitempty"` // optional; empty = no default schema
	Username         string `json:"username,omitempty"`
	Password         string `json:"password,omitempty"`
	TLSEnabled       *bool  `json:"tls_enabled,omitempty"`
	TLSSkipVerify    *bool  `json:"tls_skip_verify,omitempty"` // also applies on top of a tls-enabled URI/DSN
	DBTLSConfig
	Query *string `json:"query,omitempty"` // optional assertion: must return >= 1 row
	// QueryValueOp/QueryValue assert on the first column of the query's first
	// row — same ops as Postgres.
	QueryValueOp  string `json:"query_value_op,omitempty"`
	QueryValue    string `json:"query_value,omitempty"`
	MaxLatencyMs  *int64 `json:"max_latency_ms,omitempty"`
	WarnLatencyMs *int64 `json:"warn_latency_ms,omitempty"`
}

// WebSocketMonitorConfig represents configuration for WebSocket monitors: a
// full ws:// or wss:// upgrade handshake, optionally followed by sending a
// message and asserting on the first reply.
type WebSocketMonitorConfig struct {
	URL string `json:"url"`
	// Header values are encrypted at rest and write-only through the API (see
	// shared/secrets.MonitorSecretMapFields). Header names remain visible so
	// callers can preserve or replace individual values with "***".
	Headers       map[string]string `json:"headers,omitempty"`
	TLSSkipVerify *bool             `json:"tls_skip_verify,omitempty"`
	// SendMessage is written as a text frame after the handshake. When
	// ExpectedSubstring is set the checker waits (within the check timeout)
	// for the first text/binary frame and fails unless it contains it.
	SendMessage       *string `json:"send_message,omitempty"`
	ExpectedSubstring *string `json:"expected_substring,omitempty"`
	MaxLatencyMs      *int64  `json:"max_latency_ms,omitempty"`
	WarnLatencyMs     *int64  `json:"warn_latency_ms,omitempty"`
}

// RabbitMQMonitorConfig represents configuration for RabbitMQ monitors:
// a full AMQP handshake (connect + auth + vhost access), which catches broker
// failures a plain TCP probe can't see. Connection is configured either via
// `connection_string` (amqp:// or amqps:// URI) or via the discrete fields.
// `password`, `connection_string`, and `tls_client_key_pem` are secret fields
// (see shared/secrets.MonitorSecretFields).
type RabbitMQMonitorConfig struct {
	ConnectionString string `json:"connection_string,omitempty"`
	Host             string `json:"host,omitempty"`
	Port             int    `json:"port,omitempty"` // default 5672 (5671 with TLS)
	Username         string `json:"username,omitempty"`
	Password         string `json:"password,omitempty"`
	VHost            string `json:"vhost,omitempty"` // default "/"
	TLSEnabled       *bool  `json:"tls_enabled,omitempty"`
	TLSSkipVerify    *bool  `json:"tls_skip_verify,omitempty"` // also applies on top of an amqps:// URI
	DBTLSConfig
	MaxLatencyMs  *int64 `json:"max_latency_ms,omitempty"`
	WarnLatencyMs *int64 `json:"warn_latency_ms,omitempty"`
}

// MonitorTypeMeshProbe is the CheckJobPayload.Type for inter-location mesh
// probes. Mesh jobs carry no monitor (MonitorID stays empty) — the subject is
// a directed (source location → target location) edge.
const MonitorTypeMeshProbe = "mesh_probe"

// MeshProbeConfig is the config payload for mesh_probe jobs: the source
// location's worker performs GET http://<endpoint>/mesh/echo and asserts the
// echoed location ID matches TargetLocationID (catching misrouted endpoints).
type MeshProbeConfig struct {
	TargetLocationID string `json:"target_location_id"`
	Endpoint         string `json:"endpoint"` // host:port
}

// CheckJobPayload represents the payload for a check job
type CheckJobPayload struct {
	MonitorID      string          `json:"monitor_id"`
	Type           string          `json:"type"`
	Config         json.RawMessage `json:"config"`
	TimeoutSeconds int             `json:"timeout_seconds"`
	// LocationID pins the job to a private location's workers. Empty = the
	// default platform fleet. Workers echo it into the result message so
	// check_results carry the vantage point.
	LocationID string `json:"location_id,omitempty"`
}

// CheckJobSubjectDefault is the subject the default platform worker fleet
// consumes; monitors with no locations selected are published here.
func CheckJobSubjectDefault(base string) string {
	return base + ".default"
}

// CheckJobSubjectForLocation is the subject a private location's workers
// consume. The location UUID (not a slug) keys the subject so tenants can
// never collide.
func CheckJobSubjectForLocation(base, locationID string) string {
	return base + ".loc." + locationID
}

// TestCheckSubject is the core-NATS request-reply subject workers listen on
// for ephemeral "test this config before saving" checks. Requests are regular
// CheckJobPayloads (monitor_id may be empty); nothing is scheduled or
// persisted and the result travels back on the reply subject.
const TestCheckSubject = "checks.test"

// TestCheckSubjectForLocation is the per-location variant of TestCheckSubject,
// served only by that location's workers.
func TestCheckSubjectForLocation(locationID string) string {
	return TestCheckSubject + ".loc." + locationID
}

// CheckJobConsumerForLocation is the broker-enforced durable name used by the
// scheduler, private worker, and NATS authorization callout. It intentionally
// does not inherit a worker fleet's configurable default consumer name.
func CheckJobConsumerForLocation(locationID string) string {
	return "check-workers-loc-" + locationID
}

// TestCheckResponse is the worker's reply to a test check request.
type TestCheckResponse struct {
	Status       string          `json:"status"`
	LatencyMs    *int64          `json:"latency_ms,omitempty"`
	ErrorMessage *string         `json:"error_message,omitempty"`
	MetricsData  json.RawMessage `json:"metrics_data,omitempty"`
}
