package validation

import (
	"encoding/json"
	"testing"
)

func runConfigValidatorCases(t *testing.T, v ConfigValidator, cases []struct {
	name    string
	config  string
	wantErr bool
}) {
	t.Helper()
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := v.ValidateConfig(json.RawMessage(tc.config))
			if (err != nil) != tc.wantErr {
				t.Errorf("ValidateConfig() error = %v, wantErr %v", err, tc.wantErr)
			}
		})
	}
}

func TestRedisConfigValidator(t *testing.T) {
	runConfigValidatorCases(t, &RedisConfigValidator{}, []struct {
		name    string
		config  string
		wantErr bool
	}{
		{"valid host only", `{"host":"redis.internal"}`, false},
		{"valid full fields", `{"host":"redis.internal","port":6380,"username":"probe","password":"pw","db":2,"tls_enabled":true,"max_latency_ms":250}`, false},
		{"valid connection string", `{"connection_string":"redis://user:pw@redis.internal:6379/0"}`, false},
		{"valid rediss scheme", `{"connection_string":"rediss://redis.internal:6380"}`, false},
		{"masked connection string passes", `{"connection_string":"***"}`, false},
		{"wrong scheme", `{"connection_string":"http://redis.internal"}`, true},
		{"missing host and connection string", `{"port":6379}`, true},
		{"invalid port", `{"host":"redis.internal","port":70000}`, true},
		{"invalid db", `{"host":"redis.internal","db":99}`, true},
		{"invalid max latency", `{"host":"redis.internal","max_latency_ms":0}`, true},
		{"invalid json", `{`, true},
		{"ca pem with tls enabled", `{"host":"r","tls_enabled":true,"tls_ca_pem":"-----BEGIN CERTIFICATE-----\nMIIB\n-----END CERTIFICATE-----"}`, false},
		{"ca pem without tls", `{"host":"r","tls_ca_pem":"-----BEGIN CERTIFICATE-----\nMIIB\n-----END CERTIFICATE-----"}`, true},
		{"ca pem not pem", `{"host":"r","tls_enabled":true,"tls_ca_pem":"not a cert"}`, true},
		{"client cert without key", `{"host":"r","tls_enabled":true,"tls_client_cert_pem":"-----BEGIN CERTIFICATE-----\nMIIB\n-----END CERTIFICATE-----"}`, true},
		{"client pair with masked key", `{"host":"r","tls_enabled":true,"tls_client_cert_pem":"-----BEGIN CERTIFICATE-----\nMIIB\n-----END CERTIFICATE-----","tls_client_key_pem":"***"}`, false},
	})
}

func TestPostgresConfigValidator(t *testing.T) {
	runConfigValidatorCases(t, &PostgresConfigValidator{}, []struct {
		name    string
		config  string
		wantErr bool
	}{
		{"valid fields", `{"host":"db.internal","username":"probe"}`, false},
		{"valid full fields", `{"host":"db.internal","port":5433,"database":"app","username":"probe","password":"pw","ssl_mode":"verify-full","query":"SELECT 1","max_latency_ms":500}`, false},
		{"valid uri connection string", `{"connection_string":"postgres://user:pw@db.internal:5432/app?sslmode=require"}`, false},
		{"valid postgresql scheme", `{"connection_string":"postgresql://db.internal/app"}`, false},
		{"valid key-value dsn", `{"connection_string":"host=db.internal user=probe dbname=app"}`, false},
		{"masked connection string passes", `{"connection_string":"***"}`, false},
		{"wrong scheme", `{"connection_string":"mysql://db.internal"}`, true},
		{"missing username without connection string", `{"host":"db.internal"}`, true},
		{"missing host and connection string", `{"username":"probe"}`, true},
		{"bad ssl mode", `{"host":"db.internal","username":"probe","ssl_mode":"allow"}`, true},
		{"empty query", `{"host":"db.internal","username":"probe","query":"  "}`, true},
		{"invalid json", `{`, true},
		{"ca pem ok", `{"host":"db.internal","username":"probe","tls_ca_pem":"-----BEGIN CERTIFICATE-----\nMIIB\n-----END CERTIFICATE-----"}`, false},
		{"ca pem with ssl disabled", `{"host":"db.internal","username":"probe","ssl_mode":"disable","tls_ca_pem":"-----BEGIN CERTIFICATE-----\nMIIB\n-----END CERTIFICATE-----"}`, true},
	})
}

func TestMongoDBConfigValidator(t *testing.T) {
	runConfigValidatorCases(t, &MongoDBConfigValidator{}, []struct {
		name    string
		config  string
		wantErr bool
	}{
		{"valid host only", `{"host":"mongo.internal"}`, false},
		{"valid full fields", `{"host":"mongo.internal","port":27018,"username":"probe","password":"pw","auth_source":"admin","tls_enabled":true,"max_latency_ms":300}`, false},
		{"valid connection string", `{"connection_string":"mongodb://user:pw@mongo.internal:27017/?authSource=admin"}`, false},
		{"valid srv scheme", `{"connection_string":"mongodb+srv://cluster.example.com"}`, false},
		{"masked connection string passes", `{"connection_string":"***"}`, false},
		{"wrong scheme", `{"connection_string":"redis://mongo.internal"}`, true},
		{"missing host and connection string", `{"port":27017}`, true},
		{"username without password", `{"host":"mongo.internal","username":"probe"}`, true},
		{"invalid max latency", `{"host":"mongo.internal","max_latency_ms":-5}`, true},
		{"invalid json", `{`, true},
	})
}
