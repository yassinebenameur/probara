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
		{"query value assertion", `{"host":"db.internal","username":"probe","query":"SELECT count(*) FROM jobs","query_value_op":"number_lt","query_value":"100"}`, false},
		{"query value op without query", `{"host":"db.internal","username":"probe","query_value_op":"equals","query_value":"1"}`, true},
		{"bad query value op", `{"host":"db.internal","username":"probe","query":"SELECT 1","query_value_op":"matches","query_value":"1"}`, true},
		{"non-numeric value for numeric op", `{"host":"db.internal","username":"probe","query":"SELECT 1","query_value_op":"number_gt","query_value":"abc"}`, true},
		{"warn below max ok", `{"host":"db.internal","username":"probe","warn_latency_ms":100,"max_latency_ms":500}`, false},
		{"warn above max", `{"host":"db.internal","username":"probe","warn_latency_ms":600,"max_latency_ms":500}`, true},
	})
}

func TestRabbitMQConfigValidator(t *testing.T) {
	runConfigValidatorCases(t, &RabbitMQConfigValidator{}, []struct {
		name    string
		config  string
		wantErr bool
	}{
		{"valid fields", `{"host":"mq.internal","username":"probe","password":"pw"}`, false},
		{"valid full fields", `{"host":"mq.internal","port":5671,"username":"probe","password":"pw","vhost":"/prod","tls_enabled":true,"max_latency_ms":300}`, false},
		{"valid connection string", `{"connection_string":"amqp://user:pw@mq.internal:5672/vhost"}`, false},
		{"valid amqps scheme", `{"connection_string":"amqps://mq.internal:5671/"}`, false},
		{"masked connection string passes", `{"connection_string":"***"}`, false},
		{"wrong scheme", `{"connection_string":"mqtt://mq.internal"}`, true},
		{"missing host and connection string", `{"username":"probe"}`, true},
		{"missing username without connection string", `{"host":"mq.internal"}`, true},
		{"invalid port", `{"host":"mq.internal","port":70000,"username":"probe"}`, true},
		{"ca pem without tls", `{"host":"mq.internal","username":"probe","tls_ca_pem":"-----BEGIN CERTIFICATE-----\nMIIB\n-----END CERTIFICATE-----"}`, true},
		{"ca pem with tls", `{"host":"mq.internal","username":"probe","tls_enabled":true,"tls_ca_pem":"-----BEGIN CERTIFICATE-----\nMIIB\n-----END CERTIFICATE-----"}`, false},
		{"invalid json", `{`, true},
	})
}

func TestRedisConfigValidator_RoleAndWarnLatency(t *testing.T) {
	runConfigValidatorCases(t, &RedisConfigValidator{}, []struct {
		name    string
		config  string
		wantErr bool
	}{
		{"valid master role", `{"host":"r","expected_role":"master"}`, false},
		{"valid replica role", `{"host":"r","expected_role":"replica"}`, false},
		{"bad role", `{"host":"r","expected_role":"slave"}`, true},
		{"warn equals max", `{"host":"r","warn_latency_ms":100,"max_latency_ms":100}`, true},
		{"warn without max ok", `{"host":"r","warn_latency_ms":100}`, false},
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
		{"cluster toggles alone ok", `{"host":"mongo.internal","collect_replication":true,"collect_connections":true,"collect_cache":true,"collect_memory":true,"collect_network":true}`, false},
		{"toggles on connection string ok", `{"connection_string":"mongodb://mongo.internal","collect_replication":true,"max_replication_lag_seconds":30}`, false},
		{"valid lag thresholds", `{"host":"mongo.internal","collect_replication":true,"warn_replication_lag_seconds":10,"max_replication_lag_seconds":30}`, false},
		{"lag thresholds require collect_replication", `{"host":"mongo.internal","max_replication_lag_seconds":30}`, true},
		{"warn lag requires collect_replication", `{"host":"mongo.internal","warn_replication_lag_seconds":10}`, true},
		{"zero max lag", `{"host":"mongo.internal","collect_replication":true,"max_replication_lag_seconds":0}`, true},
		{"negative warn lag", `{"host":"mongo.internal","collect_replication":true,"warn_replication_lag_seconds":-1}`, true},
		{"warn lag not below max", `{"host":"mongo.internal","collect_replication":true,"warn_replication_lag_seconds":30,"max_replication_lag_seconds":30}`, true},
	})
}

func TestMySQLConfigValidator(t *testing.T) {
	runConfigValidatorCases(t, &MySQLConfigValidator{}, []struct {
		name    string
		config  string
		wantErr bool
	}{
		{"valid fields", `{"host":"db.internal","username":"probe"}`, false},
		{"valid full fields", `{"host":"db.internal","port":3307,"database":"app","username":"probe","password":"pw","tls_enabled":true,"query":"SELECT 1","max_latency_ms":500}`, false},
		{"valid uri connection string", `{"connection_string":"mysql://user:pw@db.internal:3306/app"}`, false},
		{"valid native dsn", `{"connection_string":"user:pw@tcp(db.internal:3306)/app"}`, false},
		{"masked connection string passes", `{"connection_string":"***"}`, false},
		{"wrong scheme", `{"connection_string":"postgres://db.internal"}`, true},
		{"missing username without connection string", `{"host":"db.internal"}`, true},
		{"missing host and connection string", `{"username":"probe"}`, true},
		{"invalid port", `{"host":"db.internal","username":"probe","port":70000}`, true},
		{"empty query", `{"host":"db.internal","username":"probe","query":"  "}`, true},
		{"query value assertion", `{"host":"db.internal","username":"probe","query":"SELECT count(*) FROM jobs","query_value_op":"number_lt","query_value":"100"}`, false},
		{"query value op without query", `{"host":"db.internal","username":"probe","query_value_op":"equals","query_value":"1"}`, true},
		{"bad query value op", `{"host":"db.internal","username":"probe","query":"SELECT 1","query_value_op":"matches","query_value":"1"}`, true},
		{"non-numeric value for numeric op", `{"host":"db.internal","username":"probe","query":"SELECT 1","query_value_op":"number_gt","query_value":"abc"}`, true},
		{"ca pem with tls enabled", `{"host":"db.internal","username":"probe","tls_enabled":true,"tls_ca_pem":"-----BEGIN CERTIFICATE-----\nMIIB\n-----END CERTIFICATE-----"}`, false},
		{"ca pem without tls", `{"host":"db.internal","username":"probe","tls_ca_pem":"-----BEGIN CERTIFICATE-----\nMIIB\n-----END CERTIFICATE-----"}`, true},
		{"warn above max", `{"host":"db.internal","username":"probe","warn_latency_ms":600,"max_latency_ms":500}`, true},
		{"invalid json", `{`, true},
	})
}

func TestWebSocketConfigValidator(t *testing.T) {
	runConfigValidatorCases(t, &WebSocketConfigValidator{}, []struct {
		name    string
		config  string
		wantErr bool
	}{
		{"valid ws url", `{"url":"ws://api.internal/live"}`, false},
		{"valid wss url", `{"url":"wss://api.example.com:8443/socket?room=1"}`, false},
		{"valid with headers and assertions", `{"url":"wss://api.example.com/socket","headers":{"Authorization":"Bearer t"},"send_message":"ping","expected_substring":"pong","warn_latency_ms":100,"max_latency_ms":500}`, false},
		{"missing url", `{}`, true},
		{"http scheme", `{"url":"http://api.example.com"}`, true},
		{"no host", `{"url":"ws:///path"}`, true},
		{"empty header name", `{"url":"ws://api.internal","headers":{" ":"v"}}`, true},
		{"empty expected substring", `{"url":"ws://api.internal","expected_substring":""}`, true},
		{"invalid max latency", `{"url":"ws://api.internal","max_latency_ms":0}`, true},
		{"warn above max", `{"url":"ws://api.internal","warn_latency_ms":600,"max_latency_ms":500}`, true},
		{"invalid json", `{`, true},
	})
}
