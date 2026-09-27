package importservice

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/yassinebenameur/probara/api/internal/models"
	"github.com/yassinebenameur/probara/api/internal/validation"
)

// kumaBackupFixture mirrors a real Uptime Kuma 1.x backup export: every
// mappable type, every skip reason, a nested group tree, a name that collides
// across two parents, and the 0/1-and-quoted-number spellings its SQLite
// export produces.
const kumaBackupFixture = `{
  "version": "1.23.13",
  "notificationList": [
    {"id": 1, "name": "Ops Slack"},
    {"id": 4, "name": "PagerDuty"}
  ],
  "monitorList": [
    {
      "id": 1, "name": "API", "type": "http", "parent": 30, "active": true,
      "url": "https://api.example.com/health", "method": "get",
      "interval": 60, "timeout": 30, "maxretries": 3, "retryInterval": 20,
      "resendInterval": 30, "maxredirects": 10,
      "accepted_statuscodes": ["200-299", "301"],
      "headers": "{\"X-Trace\": \"on\", \"Authorization\": \"Bearer abc123\"}",
      "body": "{\"probe\":true}",
      "basic_auth_user": "svc", "basic_auth_pass": "s3cret",
      "ignoreTls": true, "expiryNotification": true,
      "description": "primary edge health",
      "tags": [{"name": "prod"}, {"name": "team", "value": "core"}],
      "notificationIDList": {"1": true, "4": true}
    },
    {
      "id": 2, "name": "Docs keyword", "type": "keyword", "active": true,
      "url": "https://docs.example.com/", "interval": 120,
      "keyword": "Welcome", "invertKeyword": false
    },
    {
      "id": 3, "name": "Marketing absent", "type": "keyword", "active": true,
      "url": "https://example.com/", "interval": 120,
      "keyword": "Under construction", "invertKeyword": true
    },
    {
      "id": 4, "name": "Status JSON", "type": "json-query", "active": true,
      "url": "https://api.example.com/status", "interval": 60,
      "jsonPath": "$.status", "expectedValue": "ok"
    },
    {
      "id": 5, "name": "DB port", "type": "port", "parent": 31, "active": true,
      "hostname": "db.internal", "port": 5432, "interval": 60
    },
    {
      "id": 6, "name": "Gateway ping", "type": "ping", "active": true,
      "hostname": "10.0.0.1", "packetSize": 128, "interval": 30
    },
    {
      "id": 7, "name": "Zone NS", "type": "dns", "active": true,
      "hostname": "example.com", "dns_resolve_type": "NS",
      "dns_resolve_server": "1.1.1.1", "port": 53, "interval": 300
    },
    {
      "id": 8, "name": "Nightly job", "type": "push", "active": true,
      "interval": 3600
    },
    {
      "id": 9, "name": "Voice gRPC", "type": "grpc-keyword", "active": true,
      "grpcUrl": "grpcs://voice.example.com:443",
      "grpcServiceName": "voice.v1.Voice", "grpcMethod": "check",
      "grpcEnableTls": true, "interval": 60
    },
    {
      "id": 10, "name": "Main PG", "type": "postgres", "active": true,
      "databaseConnectionString": "postgres://probara:hunter2@pg.internal:5432/app?sslmode=require",
      "databaseQuery": "SELECT 1", "interval": 60
    },
    {
      "id": 11, "name": "Session cache", "type": "redis", "active": true,
      "databaseConnectionString": "rediss://:topsecret@redis.internal:6380/3",
      "interval": 60
    },
    {
      "id": 12, "name": "Profile store", "type": "mongodb", "active": true,
      "databaseConnectionString": "mongodb://svc:pw@mongo.internal:27017/?authSource=admin&replicaSet=rs0",
      "databaseQuery": "db.stats()", "interval": 60
    },
    {
      "id": 13, "name": "Orders MySQL", "type": "mysql", "active": true,
      "databaseConnectionString": "mysql://app:pw@mysql.internal:3306/orders",
      "interval": 60
    },
    {
      "id": 14, "name": "Legacy portal", "type": "http", "active": "1",
      "url": "http://legacy.example.com/", "interval": "45",
      "ignoreTls": 1, "expiryNotification": 1, "maxredirects": 0,
      "accepted_statuscodes_json": "[\"200-299\"]", "maxretries": 99
    },
    {
      "id": 30, "name": "Platform", "type": "group", "active": true, "interval": 60
    },
    {
      "id": 31, "name": "Edge", "type": "group", "parent": 30, "active": true, "interval": 60
    },
    {
      "id": 32, "name": "Containers", "type": "group", "active": true, "interval": 60
    },
    {
      "id": 40, "name": "Health", "type": "http", "parent": 30, "active": true,
      "url": "https://one.example.com/health", "interval": 60
    },
    {
      "id": 41, "name": "Health", "type": "http", "parent": 31, "active": true,
      "url": "https://two.example.com/health", "interval": 60
    },
    {
      "id": 50, "name": "Container app", "type": "docker", "parent": 32,
      "active": true, "docker_container": "app", "interval": 60
    },
    {
      "id": 51, "name": "Inverted probe", "type": "http", "active": true,
      "url": "https://example.com/closed", "upsideDown": true, "interval": 60
    },
    {
      "id": 52, "name": "SRV lookup", "type": "dns", "active": true,
      "hostname": "_sip._tcp.example.com", "dns_resolve_type": "SRV", "interval": 60
    },
    {
      "id": 53, "name": "Portless", "type": "port", "active": true,
      "hostname": "nowhere.internal", "interval": 60
    },
    {
      "id": 54, "name": "Broker", "type": "rabbitmq", "active": true, "interval": 60
    },
    {
      "id": 55, "name": "Game server", "type": "steam", "active": true, "interval": 60
    },
    {
      "id": 56, "name": "Quantum probe", "type": "quantum-ping", "active": true, "interval": 60
    }
  ]
}`

func parseKumaFixture(t *testing.T) *models.ImportPreviewResponse {
	t.Helper()
	svc := &Service{}
	preview, err := svc.ParseFile([]byte(kumaBackupFixture), "kuma-export.json")
	if err != nil {
		t.Fatalf("ParseFile() error = %v", err)
	}
	return preview
}

func kumaRowByName(t *testing.T, preview *models.ImportPreviewResponse, name string) models.ImportRow {
	t.Helper()
	for _, row := range preview.Rows {
		if row.Fields["name"] == name {
			return row
		}
	}
	t.Fatalf("no imported row named %q", name)
	return models.ImportRow{}
}

func kumaConfig(t *testing.T, row models.ImportRow) map[string]interface{} {
	t.Helper()
	config, ok := row.Fields["config"].(map[string]interface{})
	if !ok {
		t.Fatalf("row %v has no nested config map, got %T", row.Fields["name"], row.Fields["config"])
	}
	return config
}

func TestParseFile_KumaBackupExport(t *testing.T) {
	preview := parseKumaFixture(t)

	if preview.Format != models.ImportFormatJSON {
		t.Fatalf("format = %q, want json", preview.Format)
	}
	if preview.Schema != models.ImportSchemaUptimeKumaExport {
		t.Fatalf("schema = %q, want %q", preview.Schema, models.ImportSchemaUptimeKumaExport)
	}

	// The nested config must survive as an object: a dot-flattened config would
	// leave ExecuteImport on its simple path, which only knows five types.
	if preview.SuggestedMapping.Config != "config" {
		t.Fatalf("suggested config mapping = %q, want \"config\"", preview.SuggestedMapping.Config)
	}
	for _, field := range preview.DetectedFields {
		if strings.Contains(field, ".") {
			t.Fatalf("detected field %q is dot-flattened, so the nested config was destroyed", field)
		}
	}

	if preview.SuggestedMapping.ConsecutiveFailuresThreshold != "consecutive_failures_threshold" {
		t.Fatalf("retry threshold was not auto-mapped: %+v", preview.SuggestedMapping)
	}

	// Every emitted type must already be one the platform accepts, so the
	// operator is never asked to map a type we produced ourselves.
	for _, detected := range preview.DetectedTypes {
		if !validation.DefaultRegistry.Has(models.MonitorType(detected)) {
			t.Fatalf("emitted unsupported monitor type %q", detected)
		}
	}
	if len(preview.SuggestedTypeMapping) != 0 {
		t.Fatalf("type mapping should be empty for a translated Kuma bundle, got %v", preview.SuggestedTypeMapping)
	}
}

func TestParseKumaExport_TypeMappingTable(t *testing.T) {
	preview := parseKumaFixture(t)

	tests := []struct {
		name   string
		want   models.MonitorType
		config map[string]interface{}
	}{
		{
			name: "API",
			want: models.MonitorTypeHTTP,
			config: map[string]interface{}{
				"url":                    "https://api.example.com/health",
				"method":                 "GET",
				"body":                   "{\"probe\":true}",
				"headers":                map[string]string{"X-Trace": "on"},
				"expected_statuses":      []int{301},
				"expected_status_ranges": []map[string]int{{"min": 200, "max": 299}},
				"max_redirects":          10,
				"tls_skip_verify":        true,
				"tls_min_days_valid":     kumaCertExpiryDays,
			},
		},
		{
			name: "Docs keyword",
			want: models.MonitorTypeHTTP,
			config: map[string]interface{}{
				"url":              "https://docs.example.com/",
				"method":           "GET",
				"follow_redirects": false,
				"body_assertions":  []map[string]interface{}{{"op": "contains", "value": "Welcome"}},
			},
		},
		{
			name: "Marketing absent",
			want: models.MonitorTypeHTTP,
			config: map[string]interface{}{
				"url":              "https://example.com/",
				"method":           "GET",
				"follow_redirects": false,
				"body_assertions":  []map[string]interface{}{{"op": "not_contains", "value": "Under construction"}},
			},
		},
		{
			name: "Status JSON",
			want: models.MonitorTypeHTTP,
			config: map[string]interface{}{
				"url":              "https://api.example.com/status",
				"method":           "GET",
				"follow_redirects": false,
				"json_assertions":  []map[string]interface{}{{"path": "status", "op": "equals", "value": "ok"}},
			},
		},
		{
			name:   "DB port",
			want:   models.MonitorTypeTCP,
			config: map[string]interface{}{"host": "db.internal", "port": 5432},
		},
		{
			name:   "Gateway ping",
			want:   models.MonitorTypePing,
			config: map[string]interface{}{"host": "10.0.0.1"},
		},
		{
			name: "Zone NS",
			want: models.MonitorTypeDNS,
			config: map[string]interface{}{
				"host": "example.com", "record_type": "NS", "nameserver": "1.1.1.1",
			},
		},
		{
			name: "Nightly job",
			want: models.MonitorTypePush,
			config: map[string]interface{}{
				"expected_interval_seconds": 3600, "grace_period_seconds": 0,
			},
		},
		{
			name: "Voice gRPC",
			want: models.MonitorTypeGRPC,
			config: map[string]interface{}{
				"host": "voice.example.com", "port": 443, "use_tls": true,
			},
		},
		{
			name: "Main PG",
			want: models.MonitorTypePostgres,
			config: map[string]interface{}{
				"host": "pg.internal", "port": 5432, "database": "app",
				"username": "probara", "ssl_mode": "require", "query": "SELECT 1",
			},
		},
		{
			name: "Session cache",
			want: models.MonitorTypeRedis,
			config: map[string]interface{}{
				"host": "redis.internal", "port": 6380, "db": 3, "tls_enabled": true,
			},
		},
		{
			name: "Profile store",
			want: models.MonitorTypeMongoDB,
			config: map[string]interface{}{
				"host": "mongo.internal", "port": 27017,
				"auth_source": "admin", "replica_set": "rs0",
			},
		},
		{
			name: "Orders MySQL",
			want: models.MonitorTypeMySQL,
			config: map[string]interface{}{
				"host": "mysql.internal", "port": 3306,
				"database": "orders", "username": "app",
			},
		},
		{
			name: "Legacy portal",
			want: models.MonitorTypeHTTP,
			config: map[string]interface{}{
				"url":                    "http://legacy.example.com/",
				"method":                 "GET",
				"follow_redirects":       false,
				"expected_status_ranges": []map[string]int{{"min": 200, "max": 299}},
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			row := kumaRowByName(t, preview, tc.name)
			if row.Fields["type"] != string(tc.want) {
				t.Fatalf("type = %v, want %s", row.Fields["type"], tc.want)
			}

			got := kumaConfig(t, row)
			wantJSON, _ := json.Marshal(tc.config)
			gotJSON, _ := json.Marshal(got)
			if !jsonEqual(t, gotJSON, wantJSON) {
				t.Fatalf("config mismatch\n got: %s\nwant: %s", gotJSON, wantJSON)
			}
		})
	}
}

func jsonEqual(t *testing.T, a, b []byte) bool {
	t.Helper()
	var parsedA, parsedB interface{}
	if err := json.Unmarshal(a, &parsedA); err != nil {
		t.Fatalf("unmarshal a: %v", err)
	}
	if err := json.Unmarshal(b, &parsedB); err != nil {
		t.Fatalf("unmarshal b: %v", err)
	}
	left, _ := json.Marshal(parsedA)
	right, _ := json.Marshal(parsedB)
	return string(left) == string(right)
}

// TestParseKumaExport_ConfigsPassRegistryValidation is the mechanical guard on
// the whole mapping table: a translated config the API itself would reject is a
// bug no matter how plausible it looks.
func TestParseKumaExport_ConfigsPassRegistryValidation(t *testing.T) {
	preview := parseKumaFixture(t)

	for _, row := range preview.Rows {
		monitorType := models.MonitorType(fmt.Sprint(row.Fields["type"]))

		// Group configs are completed during the import's second pass, when
		// member names resolve to IDs; createGroupMonitorFromConfig validates
		// them there.
		if monitorType == models.MonitorTypeGroup {
			continue
		}

		raw, err := json.Marshal(row.Fields["config"])
		if err != nil {
			t.Fatalf("marshal config for %v: %v", row.Fields["name"], err)
		}
		if err := validation.DefaultRegistry.Validate(monitorType, raw); err != nil {
			t.Fatalf("monitor %v (%s) produced a config the registry rejects: %v\nconfig: %s",
				row.Fields["name"], monitorType, err, raw)
		}
	}
}

func TestParseKumaExport_SkipsUnmappableTypes(t *testing.T) {
	preview := parseKumaFixture(t)

	wantReasonFragments := map[string]string{
		"Container app":  "no Docker monitor type",
		"Inverted probe": "upside down",
		"SRV lookup":     "DNS record type SRV is not supported",
		"Portless":       "require a port",
		"Broker":         "management HTTP API",
		"Game server":    "Steam server queries",
		"Quantum probe":  `unrecognized Uptime Kuma monitor type "quantum-ping"`,
		"Containers":     "none of the group's monitors could be imported",
	}

	byName := make(map[string]models.ImportSkippedRow, len(preview.SkippedRows))
	for _, skipped := range preview.SkippedRows {
		byName[skipped.Name] = skipped
	}

	for name, fragment := range wantReasonFragments {
		skipped, ok := byName[name]
		if !ok {
			t.Fatalf("monitor %q should have been skipped, but was not", name)
		}
		if !strings.Contains(skipped.Reason, fragment) {
			t.Fatalf("skip reason for %q = %q, want it to mention %q", name, skipped.Reason, fragment)
		}
		// A skipped record must never also be importable.
		for _, row := range preview.Rows {
			if row.Fields["name"] == name {
				t.Fatalf("monitor %q was both skipped and emitted as an import row", name)
			}
		}
	}

	if len(preview.SkippedRows) != len(wantReasonFragments) {
		t.Fatalf("skipped %d monitors, want %d: %+v", len(preview.SkippedRows), len(wantReasonFragments), preview.SkippedRows)
	}
}

func TestParseKumaExport_GroupOrderingAndMembership(t *testing.T) {
	preview := parseKumaFixture(t)

	firstGroup := -1
	for i, row := range preview.Rows {
		isGroup := row.Fields["type"] == string(models.MonitorTypeGroup)
		if isGroup && firstGroup < 0 {
			firstGroup = i
		}
		if !isGroup && firstGroup >= 0 {
			t.Fatalf("row %d (%v) is a plain monitor after the groups began at %d", i, row.Fields["name"], firstGroup)
		}
	}
	if firstGroup < 0 {
		t.Fatal("no group rows were emitted")
	}

	// ExecuteImport resolves group members as it walks the group rows, so a
	// group must appear after every group nested inside it.
	positions := make(map[string]int)
	for i, row := range preview.Rows {
		positions[fmt.Sprint(row.Fields["name"])] = i
	}
	if positions["Edge"] > positions["Platform"] {
		t.Fatalf("nested group Edge (%d) must be created before its parent Platform (%d)", positions["Edge"], positions["Platform"])
	}

	edge := kumaRowByName(t, preview, "Edge")
	if got := edge.Fields["group_members"]; !containsAll(got, "DB port", "Edge / Health") {
		t.Fatalf("Edge members = %v, want its two children", got)
	}

	platform := kumaRowByName(t, preview, "Platform")
	if got := platform.Fields["group_members"]; !containsAll(got, "API", "Edge", "Platform / Health") {
		t.Fatalf("Platform members = %v, want API, the nested Edge group and its Health child", got)
	}
}

func containsAll(value interface{}, want ...string) bool {
	members, ok := value.([]string)
	if !ok {
		return false
	}
	present := make(map[string]bool, len(members))
	for _, member := range members {
		present[member] = true
	}
	if len(members) != len(want) {
		return false
	}
	for _, name := range want {
		if !present[name] {
			return false
		}
	}
	return true
}

func TestParseKumaExport_DuplicateNamesDisambiguated(t *testing.T) {
	preview := parseKumaFixture(t)

	// Kuma allows the same name under two parents; Probara dedupes on
	// type+name and group resolution rejects an ambiguous member name.
	one := kumaRowByName(t, preview, "Platform / Health")
	two := kumaRowByName(t, preview, "Edge / Health")

	if kumaConfig(t, one)["url"] != "https://one.example.com/health" {
		t.Fatalf("Platform / Health kept the wrong URL: %v", kumaConfig(t, one)["url"])
	}
	if kumaConfig(t, two)["url"] != "https://two.example.com/health" {
		t.Fatalf("Edge / Health kept the wrong URL: %v", kumaConfig(t, two)["url"])
	}

	if !hasWarning(one.Warnings, "renamed from") {
		t.Fatalf("renamed monitor carries no warning: %v", one.Warnings)
	}

	seen := make(map[string]bool)
	for _, row := range preview.Rows {
		key := fmt.Sprint(row.Fields["type"], "\x00", strings.ToLower(fmt.Sprint(row.Fields["name"])))
		if seen[key] {
			t.Fatalf("duplicate type+name survived: %v", row.Fields["name"])
		}
		seen[key] = true
	}
}

func hasWarning(warnings []string, fragment string) bool {
	for _, warning := range warnings {
		if strings.Contains(warning, fragment) {
			return true
		}
	}
	return false
}

func TestParseKumaExport_SecretsRedacted(t *testing.T) {
	preview := parseKumaFixture(t)

	// Nothing anywhere in the translated bundle may carry a credential from
	// the source file.
	encoded, err := json.Marshal(preview.Rows)
	if err != nil {
		t.Fatalf("marshal rows: %v", err)
	}
	for _, secret := range []string{"s3cret", "hunter2", "topsecret", "abc123", "Bearer"} {
		if strings.Contains(string(encoded), secret) {
			t.Fatalf("credential %q leaked into the imported rows", secret)
		}
	}

	api := kumaRowByName(t, preview, "API")
	headers, ok := kumaConfig(t, api)["headers"].(map[string]string)
	if !ok {
		t.Fatalf("API headers = %T, want a map", kumaConfig(t, api)["headers"])
	}
	if _, present := headers["Authorization"]; present {
		t.Fatal("the Authorization header was imported; HTTP headers are stored in plaintext")
	}
	if headers["X-Trace"] != "on" {
		t.Fatalf("harmless headers should survive, got %v", headers)
	}
	if !hasWarning(api.Warnings, "credential-bearing request headers") {
		t.Fatalf("dropped header is not reported: %v", api.Warnings)
	}
	if !hasWarning(api.Warnings, "HTTP authentication was not imported") {
		t.Fatalf("dropped basic auth is not reported: %v", api.Warnings)
	}

	// Database monitors lose their password, so they must land disabled rather
	// than immediately failing and paging someone.
	for _, name := range []string{"Main PG", "Session cache", "Profile store", "Orders MySQL"} {
		row := kumaRowByName(t, preview, name)
		if row.Fields["enabled"] != false {
			t.Fatalf("%s should be imported disabled, enabled = %v", name, row.Fields["enabled"])
		}
		if !hasWarning(row.Warnings, "password was not imported") {
			t.Fatalf("%s does not report its dropped password: %v", name, row.Warnings)
		}
	}

	// MongoDB rejects a username without a password, so both go.
	mongo := kumaConfig(t, kumaRowByName(t, preview, "Profile store"))
	if _, present := mongo["username"]; present {
		t.Fatalf("MongoDB username survived without its password: %v", mongo)
	}
}

func TestParseKumaExport_CarriesRetriesAndTags(t *testing.T) {
	preview := parseKumaFixture(t)

	api := kumaRowByName(t, preview, "API")
	if api.Fields["consecutive_failures_threshold"] != 3 {
		t.Fatalf("maxretries = %v, want 3", api.Fields["consecutive_failures_threshold"])
	}
	tags, ok := api.Fields["tags"].([]string)
	if !ok || len(tags) != 2 || tags[0] != "prod" || tags[1] != "team:core" {
		t.Fatalf("tags = %v, want [prod team:core]", api.Fields["tags"])
	}
	if !hasWarning(api.Warnings, "notifications were not migrated") {
		t.Fatalf("ignored notifications are not reported: %v", api.Warnings)
	}
	if !hasWarning(api.Warnings, `"Ops Slack"`) || !hasWarning(api.Warnings, `"PagerDuty"`) {
		t.Fatalf("notifier names are not named in the warning: %v", api.Warnings)
	}

	// Quoted numbers and 0/1 booleans are how Kuma's SQLite export spells
	// these, and an out-of-range retry count must be clamped rather than
	// rejected by the column check.
	legacy := kumaRowByName(t, preview, "Legacy portal")
	if legacy.Fields["enabled"] != true {
		t.Fatalf(`active "1" should parse as enabled, got %v`, legacy.Fields["enabled"])
	}
	if legacy.Fields["interval_seconds"] != 45 {
		t.Fatalf(`interval "45" = %v, want 45`, legacy.Fields["interval_seconds"])
	}
	if legacy.Fields["consecutive_failures_threshold"] != 99 {
		t.Fatalf("the adapter should pass maxretries through; clamping happens on create: %v", legacy.Fields["consecutive_failures_threshold"])
	}
	if !hasWarning(legacy.Warnings, "plain http:// URL") {
		t.Fatalf("TLS settings dropped on an http:// URL are not reported: %v", legacy.Warnings)
	}
}

// TestParseKumaExport_MonitorListObjectForm covers the socket.io payload,
// where monitorList is an object keyed by monitor id rather than an array.
func TestParseKumaExport_MonitorListObjectForm(t *testing.T) {
	svc := &Service{}

	object := []byte(`{
      "version": "2.0.0",
      "monitorList": {
        "7": {"id": 7, "name": "Beta", "type": "ping", "hostname": "b.example.com", "active": 1, "interval": 60},
        "3": {"id": 3, "name": "Alpha", "type": "ping", "hostname": "a.example.com", "active": 1, "interval": 60}
      }
    }`)
	array := []byte(`{
      "version": "2.0.0",
      "monitorList": [
        {"id": 3, "name": "Alpha", "type": "ping", "hostname": "a.example.com", "active": 1, "interval": 60},
        {"id": 7, "name": "Beta", "type": "ping", "hostname": "b.example.com", "active": 1, "interval": 60}
      ]
    }`)

	fromObject, err := svc.ParseFile(object, "kuma.json")
	if err != nil {
		t.Fatalf("object form failed to parse: %v", err)
	}
	fromArray, err := svc.ParseFile(array, "kuma.json")
	if err != nil {
		t.Fatalf("array form failed to parse: %v", err)
	}

	objectJSON, _ := json.Marshal(fromObject.Rows)
	arrayJSON, _ := json.Marshal(fromArray.Rows)
	if string(objectJSON) != string(arrayJSON) {
		t.Fatalf("id-keyed object and array forms disagree\nobject: %s\narray:  %s", objectJSON, arrayJSON)
	}
}

func TestParseKumaExport_IgnoresForeignJSON(t *testing.T) {
	svc := &Service{}

	// A generic monitor list must still take the generic path.
	preview, err := svc.ParseFile([]byte(`[{"name":"a","type":"http","url":"https://a.test"}]`), "list.json")
	if err != nil {
		t.Fatalf("generic JSON stopped parsing: %v", err)
	}
	if preview.Schema != "" {
		t.Fatalf("generic JSON was misdetected as schema %q", preview.Schema)
	}
}

// TestExecuteImport_KumaBundleCreatesMonitors drives the translated bundle
// through the real ExecuteImport, which is where types, configs, group
// membership and the retry threshold actually reach the monitor service.
func TestExecuteImport_KumaBundleCreatesMonitors(t *testing.T) {
	preview := parseKumaFixture(t)

	tenantID := uuid.New()
	monitorSvc := &portableMonitorServiceMock{}
	groupSvc := &groupMembershipMock{}
	svc := NewService(nil, monitorSvc, groupSvc)

	result, err := svc.ExecuteImport(context.Background(), tenantID, &models.ImportExecuteRequest{
		Rows:    preview.Rows,
		Mapping: preview.SuggestedMapping,
	})
	if err != nil {
		t.Fatalf("ExecuteImport() error = %v", err)
	}

	if result.FailedCount != 0 || result.SkippedCount != 0 {
		for _, row := range result.Results {
			if row.Status != "success" {
				t.Errorf("row %d (%s/%s): %s %s%s", row.Index, row.Name, row.Type, row.Status, row.Error, row.SkipReason)
			}
		}
		t.Fatalf("import was not clean: %d failed, %d skipped", result.FailedCount, result.SkippedCount)
	}
	if result.SuccessCount != len(preview.Rows) {
		t.Fatalf("created %d monitors, want %d", result.SuccessCount, len(preview.Rows))
	}

	byName := make(map[string]*models.CreateMonitorRequest, len(monitorSvc.createRequests))
	idByName := make(map[string]uuid.UUID, len(monitorSvc.createdMonitors))
	for i, req := range monitorSvc.createRequests {
		byName[req.Name] = req
		idByName[req.Name] = monitorSvc.createdMonitors[i].ID
	}

	api := byName["API"]
	if api == nil || api.Type != models.MonitorTypeHTTP {
		t.Fatalf("API was not created as an http monitor: %+v", api)
	}
	if api.ConsecutiveFailuresThreshold == nil || *api.ConsecutiveFailuresThreshold != 3 {
		t.Fatalf("API retry threshold = %v, want 3", api.ConsecutiveFailuresThreshold)
	}
	if api.IntervalSeconds != 60 || api.TimeoutSeconds != 30 {
		t.Fatalf("API interval/timeout = %d/%d, want 60/30", api.IntervalSeconds, api.TimeoutSeconds)
	}

	// Kuma's 99 retries exceed the column's ceiling and must be clamped.
	legacy := byName["Legacy portal"]
	if legacy.ConsecutiveFailuresThreshold == nil || *legacy.ConsecutiveFailuresThreshold != maxConsecutiveFailuresThreshold {
		t.Fatalf("Legacy portal retry threshold = %v, want it clamped to %d",
			legacy.ConsecutiveFailuresThreshold, maxConsecutiveFailuresThreshold)
	}

	// Passive types must not carry a timeout.
	for _, name := range []string{"Nightly job", "Platform", "Edge"} {
		if byName[name].TimeoutSeconds != 0 {
			t.Fatalf("%s is a passive check but kept timeout %d", name, byName[name].TimeoutSeconds)
		}
	}

	// Groups must resolve to real member IDs and gain junction-table rows.
	edgeConfig := decodeConfig(t, byName["Edge"].Config)
	edgeMembers, _ := edgeConfig["monitor_ids"].([]interface{})
	if len(edgeMembers) != 2 {
		t.Fatalf("Edge monitor_ids = %#v, want two members", edgeConfig["monitor_ids"])
	}
	attached := groupSvc.added[idByName["Edge"]]
	if len(attached) != 2 {
		t.Fatalf("Edge gained %d membership rows, want 2", len(attached))
	}

	// Platform contains the nested Edge group, which only resolves because the
	// adapter ordered Edge first.
	platformConfig := decodeConfig(t, byName["Platform"].Config)
	platformMembers, _ := platformConfig["monitor_ids"].([]interface{})
	if len(platformMembers) != 3 {
		t.Fatalf("Platform monitor_ids = %#v, want three members", platformConfig["monitor_ids"])
	}
	edgeID := idByName["Edge"].String()
	found := false
	for _, member := range platformMembers {
		if member == edgeID {
			found = true
		}
	}
	if !found {
		t.Fatalf("Platform does not contain the nested Edge group %s: %v", edgeID, platformMembers)
	}

	if enabled := byName["Main PG"].Enabled; enabled == nil || *enabled {
		t.Fatalf("Main PG should have been created disabled, Enabled = %v", enabled)
	}
}
