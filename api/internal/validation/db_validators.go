package validation

import (
	"encoding/json"
	"fmt"
	"net"
	"net/url"
	"strings"

	sharedmodels "github.com/yassinebenameur/probara/shared/models"
	"github.com/yassinebenameur/probara/shared/secrets"
)

func validateDBHostPort(host string, port int) error {
	host = strings.TrimSpace(host)
	if host == "" {
		return fmt.Errorf("host is required when no connection_string is set")
	}
	if ip := net.ParseIP(host); ip == nil {
		if !isValidHostname(host) {
			return fmt.Errorf("host must be a valid IP address or hostname")
		}
	}
	if port != 0 && (port < 1 || port > 65535) {
		return fmt.Errorf("port must be between 1 and 65535")
	}
	return nil
}

// validateDBConnectionString checks a URI-style connection string against the
// schemes valid for the monitor type. The masked placeholder and ciphertext
// envelopes pass through: they stand for an already-validated stored value.
func validateDBConnectionString(cs string, schemes ...string) error {
	if cs == secrets.MaskedSecret || secrets.LooksLikeEnvelope(cs) {
		return nil
	}
	u, err := url.Parse(cs)
	if err != nil || u.Scheme == "" {
		return fmt.Errorf("connection_string must be a valid URI (%s)", strings.Join(schemes, ", "))
	}
	for _, s := range schemes {
		if u.Scheme == s {
			return nil
		}
	}
	return fmt.Errorf("connection_string scheme %q is not supported (expected %s)", u.Scheme, strings.Join(schemes, ", "))
}

func validateMaxLatency(maxLatencyMs *int64) error {
	if maxLatencyMs != nil && *maxLatencyMs <= 0 {
		return fmt.Errorf("max_latency_ms must be greater than 0")
	}
	return nil
}

func pemFieldValue(v *string) string {
	if v == nil {
		return ""
	}
	return strings.TrimSpace(*v)
}

// validateDBTLSMaterial sanity-checks pasted TLS PEMs. The client key may be
// the masked placeholder (or, defensively, a ciphertext envelope) standing in
// for an already-stored value — those skip content checks but still count as
// "present" for the cert/key pairing rule.
func validateDBTLSMaterial(material sharedmodels.DBTLSConfig) error {
	ca := pemFieldValue(material.TLSCAPem)
	cert := pemFieldValue(material.TLSClientCertPem)
	key := pemFieldValue(material.TLSClientKeyPem)

	if ca != "" && !strings.Contains(ca, "BEGIN") {
		return fmt.Errorf("tls_ca_pem must be PEM-encoded")
	}
	if cert != "" && !strings.Contains(cert, "BEGIN") {
		return fmt.Errorf("tls_client_cert_pem must be PEM-encoded")
	}
	if key != "" && key != secrets.MaskedSecret && !secrets.LooksLikeEnvelope(key) && !strings.Contains(key, "BEGIN") {
		return fmt.Errorf("tls_client_key_pem must be PEM-encoded")
	}
	if (cert == "") != (key == "") {
		return fmt.Errorf("tls_client_cert_pem and tls_client_key_pem must be provided together")
	}
	return nil
}

func hasDBTLSMaterial(material sharedmodels.DBTLSConfig) bool {
	return pemFieldValue(material.TLSCAPem) != "" ||
		pemFieldValue(material.TLSClientCertPem) != "" ||
		pemFieldValue(material.TLSClientKeyPem) != ""
}

// RedisConfigValidator validates Redis monitor configuration
type RedisConfigValidator struct{}

// ValidateConfig validates Redis monitor config
func (v *RedisConfigValidator) ValidateConfig(configRaw json.RawMessage) error {
	var config sharedmodels.RedisMonitorConfig
	if err := json.Unmarshal(configRaw, &config); err != nil {
		return fmt.Errorf("invalid redis config: %w", err)
	}

	if config.ConnectionString != "" {
		if err := validateDBConnectionString(config.ConnectionString, "redis", "rediss"); err != nil {
			return err
		}
	} else if err := validateDBHostPort(config.Host, config.Port); err != nil {
		return err
	}

	if config.DB < 0 || config.DB > 15 {
		return fmt.Errorf("db must be between 0 and 15")
	}

	if err := validateDBTLSMaterial(config.DBTLSConfig); err != nil {
		return err
	}
	if hasDBTLSMaterial(config.DBTLSConfig) && config.ConnectionString == "" &&
		(config.TLSEnabled == nil || !*config.TLSEnabled) {
		return fmt.Errorf("tls certificates require tls_enabled")
	}

	return validateMaxLatency(config.MaxLatencyMs)
}

// PostgresConfigValidator validates PostgreSQL monitor configuration
type PostgresConfigValidator struct{}

var validPostgresSSLModes = map[string]bool{
	"disable":     true,
	"require":     true,
	"verify-full": true,
}

// ValidateConfig validates PostgreSQL monitor config
func (v *PostgresConfigValidator) ValidateConfig(configRaw json.RawMessage) error {
	var config sharedmodels.PostgresMonitorConfig
	if err := json.Unmarshal(configRaw, &config); err != nil {
		return fmt.Errorf("invalid postgres config: %w", err)
	}

	if config.ConnectionString != "" {
		// pgx also accepts key=value DSNs; only URI forms are scheme-checked.
		if strings.Contains(config.ConnectionString, "://") {
			if err := validateDBConnectionString(config.ConnectionString, "postgres", "postgresql"); err != nil {
				return err
			}
		}
	} else {
		if err := validateDBHostPort(config.Host, config.Port); err != nil {
			return err
		}
		if strings.TrimSpace(config.Username) == "" {
			return fmt.Errorf("username is required when no connection_string is set")
		}
	}

	if config.SSLMode != "" && !validPostgresSSLModes[config.SSLMode] {
		return fmt.Errorf("ssl_mode must be one of: disable, require, verify-full")
	}

	if config.Query != nil && strings.TrimSpace(*config.Query) == "" {
		return fmt.Errorf("query cannot be empty")
	}

	if err := validateDBTLSMaterial(config.DBTLSConfig); err != nil {
		return err
	}
	if pemFieldValue(config.TLSCAPem) != "" && config.SSLMode == "disable" {
		return fmt.Errorf("tls_ca_pem requires ssl_mode require or verify-full")
	}

	return validateMaxLatency(config.MaxLatencyMs)
}

// MongoDBConfigValidator validates MongoDB monitor configuration
type MongoDBConfigValidator struct{}

// ValidateConfig validates MongoDB monitor config
func (v *MongoDBConfigValidator) ValidateConfig(configRaw json.RawMessage) error {
	var config sharedmodels.MongoDBMonitorConfig
	if err := json.Unmarshal(configRaw, &config); err != nil {
		return fmt.Errorf("invalid mongodb config: %w", err)
	}

	if config.ConnectionString != "" {
		if err := validateDBConnectionString(config.ConnectionString, "mongodb", "mongodb+srv"); err != nil {
			return err
		}
	} else {
		if err := validateDBHostPort(config.Host, config.Port); err != nil {
			return err
		}
		// Username and password go together: auth with only one of them is a
		// config mistake, not a server-side condition.
		if (config.Username == "") != (config.Password == "") {
			return fmt.Errorf("username and password must be provided together")
		}
	}

	if err := validateDBTLSMaterial(config.DBTLSConfig); err != nil {
		return err
	}
	if hasDBTLSMaterial(config.DBTLSConfig) && config.ConnectionString == "" &&
		(config.TLSEnabled == nil || !*config.TLSEnabled) {
		return fmt.Errorf("tls certificates require tls_enabled")
	}

	return validateMaxLatency(config.MaxLatencyMs)
}
