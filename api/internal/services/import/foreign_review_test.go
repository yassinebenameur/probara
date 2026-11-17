package importservice

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/yassinebenameur/probara/api/internal/models"
)

func parseOK(t *testing.T, document, filename string) *models.ImportPreviewResponse {
	t.Helper()
	preview, err := (&Service{}).ParseFile([]byte(document), filename)
	if err != nil {
		t.Fatalf("ParseFile() error = %v", err)
	}
	return preview
}

// A file of several responses (the jq -s export) must still report a page
// whose next page it does not hold, and stay quiet when it does.
func TestBetterStack_MissingPagesInMultiDocumentFiles(t *testing.T) {
	page := func(n int, next string) string {
		prev := "null"
		if n > 1 {
			prev = fmt.Sprintf(`"https://uptime.betterstack.com/api/v2/monitors?page=%d"`, n-1)
		}
		return fmt.Sprintf(`{"data":[{"id":"%d","type":"monitor","attributes":{"url":"https://p%d.example.com","pronounceable_name":"P%d","monitor_type":"status","check_frequency":60,"confirmation_period":0}}],
			"pagination":{"first":"https://uptime.betterstack.com/api/v2/monitors?page=1","prev":%s,"next":%s}}`, n, n, n, prev, next)
	}
	nextOf := func(n int) string {
		return fmt.Sprintf(`"https://uptime.betterstack.com/api/v2/monitors?page=%d"`, n)
	}
	heartbeats := `{"data":[{"id":"9","type":"heartbeat","attributes":{"name":"H","period":300,"grace":0}}],"pagination":{"next":null}}`

	missing := parseOK(t, "["+page(1, nextOf(2))+","+heartbeats+"]", "better-stack.json")
	if !hasWarning(missing.Warnings, "the Better Stack monitors response links to a next page that is not in this file") {
		t.Fatalf("a missing page in a multi-document file is not reported: %v", missing.Warnings)
	}

	complete := parseOK(t, "["+page(1, nextOf(2))+","+page(2, "null")+","+heartbeats+"]", "better-stack.json")
	if hasWarning(complete.Warnings, "next page") || len(complete.Rows) != 3 {
		t.Fatalf("a complete export: %d rows, warnings %v", len(complete.Rows), complete.Warnings)
	}
}

func TestBetterStack_SingleResourceResponse(t *testing.T) {
	preview := parseOK(t, `{"data":{"id":"5","type":"monitor","attributes":{"url":"https://a.example.com","pronounceable_name":"Absent","monitor_type":"keyword_absence","required_keyword":"Down","check_frequency":60,"confirmation_period":0}}}`, "monitor.json")
	if preview.Schema != models.ImportSchemaBetterStackExport {
		t.Fatalf("schema = %q", preview.Schema)
	}
	config := configOf(t, rowNamed(t, preview, "Absent"))
	if configJSON(t, config["body_assertions"]) != `[{"case_insensitive":true,"op":"not_contains","value":"Down"}]` {
		t.Fatalf("keyword_absence = %s", configJSON(t, config["body_assertions"]))
	}
}

// Fields whose meaning or default is not verified are not guessed.
func TestBetterStack_FailSafeReadings(t *testing.T) {
	preview := parseOK(t, `{"data":[
		{"id":"1","type":"monitor","attributes":{"url":"10.0.0.1","pronounceable_name":"Ping s","monitor_type":"ping","check_frequency":60,"request_timeout":5,"confirmation_period":0}},
		{"id":"2","type":"monitor","attributes":{"url":"10.0.0.2","pronounceable_name":"Ping ms","monitor_type":"ping","check_frequency":60,"request_timeout":3000,"confirmation_period":0}},
		{"id":"3","type":"monitor","attributes":{"url":"https://user:pw@a.example.com/health?api_key=abc123&lang=en","monitor_type":"status","check_frequency":60}},
		{"id":"4","type":"monitor","attributes":{"url":"https://b.example.com/","pronounceable_name":"Starred","monitor_type":"status","auth_username":"ops","auth_password":"********","check_frequency":60,"confirmation_period":0}}]}`, "monitors.json")

	unsure := rowNamed(t, preview, "Ping s")
	if unsure.Fields["timeout_seconds"] != importDefaultTimeout || !hasWarning(unsure.Warnings, "could not be read with confidence") {
		t.Fatalf("an ambiguous timeout: %v %v", unsure.Fields["timeout_seconds"], unsure.Warnings)
	}
	if rowNamed(t, preview, "Ping ms").Fields["timeout_seconds"] != 3 {
		t.Fatal("3000 ms must become 3 s")
	}

	// Named after host and path only; the query credential is reported by
	// its key, never its value; no confirmation period means the default.
	unnamed := rowNamed(t, preview, "a.example.com/health")
	if _, set := unnamed.Fields["consecutive_failures_threshold"]; set || !hasWarning(unnamed.Warnings, "no confirmation period") {
		t.Fatalf("absent confirmation period: %v %v", unnamed.Fields, unnamed.Warnings)
	}
	if !hasWarning(unnamed.Warnings, "credential in its query (api_key)") {
		t.Fatalf("a query credential is not reported: %v", unnamed.Warnings)
	}
	encoded, _ := json.Marshal(append(append([]interface{}{}, unnamed.Warnings), preview.Warnings, preview.SkippedRows))
	if strings.Contains(string(encoded), "abc123") || strings.Contains(string(encoded), "pw@") {
		t.Fatalf("a credential leaked into a note: %s", encoded)
	}
	if configOf(t, unnamed)["basic_auth_password"] != "pw" {
		t.Fatal("userinfo must become basic auth")
	}

	// A password redacted with asterisks was not exported.
	starred := rowNamed(t, preview, "Starred")
	if _, set := configOf(t, starred)["basic_auth_password"]; set || starred.Fields["enabled"] != false {
		t.Fatalf("an asterisk password: %v", starred.Fields)
	}
}

// A period longer than a day keeps its deadline: the excess moves into the
// grace period instead of expecting a weekly job every day.
func TestHeartbeatsLongerThanADayKeepTheirDeadline(t *testing.T) {
	check := func(t *testing.T, row models.ImportRow, wantGrace int) {
		t.Helper()
		config := configOf(t, row)
		if row.Fields["interval_seconds"] != maxImportInterval || config["expected_interval_seconds"] != maxImportInterval ||
			config["grace_period_seconds"] != wantGrace || row.Fields["enabled"] != true {
			t.Fatalf("long heartbeat = %v %v", row.Fields, config)
		}
		if !hasWarning(row.Warnings, "the check-in deadline is unchanged") {
			t.Fatalf("the move is not explained: %v", row.Warnings)
		}
	}

	t.Run("Better Stack", func(t *testing.T) {
		check(t, rowNamed(t, previewOf(t, "Better Stack"), "Weekly report"), 604800-86400)
		preview := parseOK(t, `{"data":[{"id":"1","type":"heartbeat","attributes":{"name":"Fortnightly","period":1209600,"grace":7200}}]}`, "heartbeats.json")
		check(t, rowNamed(t, preview, "Fortnightly"), 1209600-86400+7200)
	})
	t.Run("Gatus", func(t *testing.T) {
		preview := parseOK(t, "external-endpoints:\n  - name: weekly\n    token: x\n    heartbeat:\n      interval: 168h\n", "config.yaml")
		check(t, rowNamed(t, preview, "weekly"), 604800-86400)
	})
}

func TestGatus_ReviewFixes(t *testing.T) {
	endpoint := func(url, extra string, conditions ...string) string {
		var b strings.Builder
		fmt.Fprintf(&b, "endpoints:\n  - name: probe\n    url: %q\n%s    conditions:\n", url, extra)
		for _, condition := range conditions {
			fmt.Fprintf(&b, "      - %q\n", condition)
		}
		return b.String()
	}

	t.Run("placeholder condition disables", func(t *testing.T) {
		row := rowNamed(t, parseOK(t, endpoint("https://a.example", "", "[STATUS] == ${EXPECTED}"), "c.yaml"), "probe")
		if row.Fields["enabled"] != false || !hasWarning(row.Warnings, "$EXPECTED in the conditions") {
			t.Fatalf("enabled %v, %v", row.Fields["enabled"], row.Warnings)
		}
	})

	t.Run("$$ is a literal dollar", func(t *testing.T) {
		row := rowNamed(t, parseOK(t, endpoint("https://a.example/graphql", "    graphql: true\n    body: 'query($$id: ID!) { node(id: $$id) { id } }'\n", "[STATUS] == 200"), "c.yaml"), "probe")
		if row.Fields["enabled"] != true {
			t.Fatalf("$$ was read as a placeholder: %v", row.Warnings)
		}
		if body := configOf(t, row)["body"].(string); !strings.Contains(body, "query($id: ID!)") || strings.Contains(body, "$$") {
			t.Fatalf("body = %s", body)
		}
	})

	t.Run("zero and sub-day bounds", func(t *testing.T) {
		preview := parseOK(t, endpoint("https://a.example.org", "", "[DOMAIN_EXPIRATION] > 0h", "[CERTIFICATE_EXPIRATION] > 12h"), "c.yaml")
		probe := rowNamed(t, preview, "probe")
		if configOf(t, probe)["tls_min_days_valid"] != 1 || !hasWarning(probe.Warnings, "rounded up to 1 day") {
			t.Fatalf("12h bound: %v %v", configOf(t, probe)["tls_min_days_valid"], probe.Warnings)
		}
		domain := rowNamed(t, preview, "example.org domain expiry")
		if configOf(t, domain)["min_days_valid"] != 0 || domain.Derived != models.ImportRowDerivedDomainExpiry {
			t.Fatalf("a > 0h domain bound must still create the monitor: %v %q", configOf(t, domain), domain.Derived)
		}
	})

	t.Run("certificate bound with insecure", func(t *testing.T) {
		row := rowNamed(t, parseOK(t, endpoint("https://a.example", "    client:\n      insecure: true\n", "[CERTIFICATE_EXPIRATION] > 0"), "c.yaml"), "probe")
		config := configOf(t, row)
		if config["tls_min_days_valid"] != 0 || !hasWarning(row.Warnings, "WARNING: certificate verification is skipped") {
			t.Fatalf("expired-passes risk is not reported: %v %v", config, row.Warnings)
		}
	})

	t.Run("unverified pat syntax is dropped", func(t *testing.T) {
		row := rowNamed(t, parseOK(t, endpoint("https://a.example", "", "[BODY].v == pat(1.?)"), "c.yaml"), "probe")
		if _, set := configOf(t, row)["json_assertions"]; set || !hasWarning(row.Warnings, "only '*' wildcards") {
			t.Fatalf("pat with ?: %v %v", configOf(t, row), row.Warnings)
		}
	})
}

func TestUptimeRobot_FailSafeReadings(t *testing.T) {
	_, err := (&Service{}).ParseFile([]byte(`{"data":[{"id":1,"friendlyName":"A","url":"https://a.example.com","type":"HTTP"}],"nextLink":null}`), "ur.json")
	if err == nil || !strings.Contains(err.Error(), "API v3") {
		t.Fatalf("a v3 response must be refused, got %v", err)
	}

	preview := parseOK(t, `{"stat":"ok","monitors":[
		{"id":1,"friendly_name":"Needs","url":"https://a.example.com","type":2,"keyword_type":2,"keyword_value":"OK","interval":300,"status":2},
		{"id":2,"friendly_name":"Forbids","url":"https://a.example.com/x","type":2,"keyword_type":1,"keyword_value":"Error","keyword_case_type":null,"interval":300,"status":2}]}`, "ur.json")
	if got := configJSON(t, configOf(t, rowNamed(t, preview, "Needs"))["body_assertions"]); got != `[{"op":"contains","value":"OK"}]` {
		t.Fatalf("unknown case rule, required keyword = %s", got)
	}
	if got := configJSON(t, configOf(t, rowNamed(t, preview, "Forbids"))["body_assertions"]); got != `[{"case_insensitive":true,"op":"not_contains","value":"Error"}]` {
		t.Fatalf("unknown case rule, forbidden keyword = %s", got)
	}
}

// The domain type's threshold floor holds on every import path, not only
// for rows the adapters build.
func TestExecuteImport_DomainRulesOnRawPath(t *testing.T) {
	monitorSvc := &portableMonitorServiceMock{}
	svc := NewService(nil, monitorSvc, &groupMembershipMock{})
	row := rowWithConfig(0, "example.com", "domain", map[string]interface{}{"domain": "example.com"}, nil, 86400, 30)
	row.Fields["consecutive_failures_threshold"] = 1
	result, err := svc.ExecuteImport(context.Background(), uuid.New(), &models.ImportExecuteRequest{
		Rows: []models.ImportRow{row},
		Mapping: models.FieldMapping{Name: "name", Type: "type", Config: "config", IntervalSeconds: "interval_seconds",
			TimeoutSeconds: "timeout_seconds", ConsecutiveFailuresThreshold: "consecutive_failures_threshold"},
	})
	if err != nil {
		t.Fatalf("ExecuteImport() error = %v", err)
	}
	if result.FailedCount != 1 || !strings.Contains(result.Results[0].Error, "consecutive_failures_threshold must be at least 2") {
		t.Fatalf("a domain monitor with threshold 1 was accepted: %+v", result.Results)
	}
}
