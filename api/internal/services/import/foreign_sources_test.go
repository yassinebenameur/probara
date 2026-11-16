package importservice

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/yassinebenameur/probara/api/internal/models"
)

func TestBetterStack_SkipReasonsAndSemantics(t *testing.T) {
	preview := previewOf(t, "Better Stack")

	for name, fragment := range map[string]string{
		"Mail relay":     "SMTP server check would degrade",
		"Signup journey": "Playwright scenario",
		"StatsD UDP":     "UDP checks are not implemented",
		"Retired":        "group would have been empty",
	} {
		if skipped := skippedNamed(t, preview, name); !strings.Contains(skipped.Reason, fragment) {
			t.Errorf("%s: reason %q, want it to mention %q", name, skipped.Reason, fragment)
		}
	}

	// A confirmation period is counted in checks at the monitor's interval.
	site := rowNamed(t, preview, "Marketing site")
	if site.Fields["consecutive_failures_threshold"] != 3 || site.Fields["recovery_threshold"] != 4 {
		t.Fatalf("thresholds = %v / %v, want 3 / 4", site.Fields["consecutive_failures_threshold"], site.Fields["recovery_threshold"])
	}
	// No confirmation period means the first failure opens the incident.
	if rowNamed(t, preview, "Checkout API").Fields["consecutive_failures_threshold"] != 1 {
		t.Fatal("a zero confirmation period must import as one failing check")
	}
	// Server timeouts are milliseconds outside HTTP.
	if rowNamed(t, preview, "Gateway ping").Fields["timeout_seconds"] != 2 {
		t.Fatal("a 2000 ms ping timeout must become 2 s")
	}

	// One domain monitor per registrable domain, with the widest window.
	domains := 0
	for _, row := range preview.Rows {
		if row.Fields["type"] == string(models.MonitorTypeDomain) {
			domains++
			if row.Fields["consecutive_failures_threshold"] != 2 || row.Fields["interval_seconds"] != foreignDomainInterval {
				t.Fatalf("domain monitor breaks the domain type's rules: %v", row.Fields)
			}
			if configOf(t, row)["min_days_valid"] != 60 {
				t.Fatalf("domain window = %v, want the widest (60)", configOf(t, row)["min_days_valid"])
			}
		}
	}
	if domains != 1 {
		t.Fatalf("%d domain monitors, want one for example.com", domains)
	}

	if !hasWarning(preview.Warnings, "regions were not migrated") || !hasWarning(preview.Warnings, "escalation policies") ||
		!hasWarning(preview.Warnings, "maintenance windows were not migrated") {
		t.Fatalf("ignored settings are not each reported once: %v", preview.Warnings)
	}
}

func TestBetterStack_SinglePageForms(t *testing.T) {
	page := []byte(`{"data":[{"id":"1","type":"monitor","attributes":{"url":"https://a.example.com","pronounceable_name":"A","monitor_type":"status","check_frequency":60,"monitor_group_id":7}}],
		"pagination":{"first":"x","last":"y","prev":null,"next":"https://uptime.betterstack.com/api/v2/monitors?page=2"}}`)
	preview, err := (&Service{}).ParseFile(page, "monitors.json")
	if err != nil {
		t.Fatalf("ParseFile() error = %v", err)
	}
	if preview.Schema != models.ImportSchemaBetterStackExport || len(preview.Rows) != 1 {
		t.Fatalf("schema %q, %d rows", preview.Schema, len(preview.Rows))
	}
	if !hasWarning(preview.Warnings, "links to a next page") {
		t.Fatalf("an incomplete page is not reported: %v", preview.Warnings)
	}
	if !hasWarning(preview.Rows[0].Warnings, "monitor group 7, which is not in this file") {
		t.Fatalf("an unresolved group is not reported: %v", preview.Rows[0].Warnings)
	}

	// Merged kinds in one data array resolve groups too.
	merged := []byte(`{"data":[
		{"id":"7","type":"monitor_group","attributes":{"name":"Edge","paused":true}},
		{"id":"1","type":"monitor","attributes":{"url":"https://a.example.com","pronounceable_name":"A","monitor_type":"status","check_frequency":60,"monitor_group_id":"7"}}]}`)
	preview, err = (&Service{}).ParseFile(merged, "export.json")
	if err != nil {
		t.Fatalf("ParseFile() error = %v", err)
	}
	group := rowNamed(t, preview, "Edge")
	if group.Fields["enabled"] != false || fmt.Sprint(group.Fields["group_members"]) != "[A]" {
		t.Fatalf("paused group = %v", group.Fields)
	}
}

func TestUptimeRobot_SkipReasonsAndSemantics(t *testing.T) {
	preview := previewOf(t, "UptimeRobot")

	if skipped := skippedNamed(t, preview, "Digest admin"); !strings.Contains(skipped.Reason, "digest") {
		t.Fatalf("digest reason = %q", skipped.Reason)
	}
	if skipped := skippedNamed(t, preview, "Zone records"); !strings.Contains(skipped.Reason, "type 6") {
		t.Fatalf("unknown type reason = %q", skipped.Reason)
	}

	// keyword_type names when UptimeRobot flags the monitor down.
	present := configOf(t, rowNamed(t, preview, "Shop keyword"))
	if configJSON(t, present["body_assertions"]) != `[{"case_insensitive":true,"op":"contains","value":"Add to cart"}]` {
		t.Fatalf("\"alert when missing\" = %s", configJSON(t, present["body_assertions"]))
	}
	absent := configOf(t, rowNamed(t, preview, "Error page absent"))
	if configJSON(t, absent["body_assertions"]) != `[{"op":"not_contains","value":"Fatal error"}]` {
		t.Fatalf("\"alert when present\" = %s", configJSON(t, absent["body_assertions"]))
	}

	// Custom statuses apply on top of UptimeRobot's 2xx/3xx default.
	orders := configOf(t, rowNamed(t, preview, "Orders API"))
	if configJSON(t, orders["expected_statuses"]) != `[404]` ||
		configJSON(t, orders["expected_status_ranges"]) != `[{"max":301,"min":200},{"max":399,"min":303}]` {
		t.Fatalf("custom statuses = %s %s", configJSON(t, orders["expected_statuses"]), configJSON(t, orders["expected_status_ranges"]))
	}

	if rowNamed(t, preview, "Paused blog").Fields["enabled"] != false {
		t.Fatal("status 0 must import paused")
	}
	if configOf(t, rowNamed(t, preview, "SMTP port"))["port"] != 25 || configOf(t, rowNamed(t, preview, "Admin console port"))["port"] != 8443 {
		t.Fatal("port sub-types are not resolved")
	}
}

func TestUptimeRobot_ErrorAndPartialResponses(t *testing.T) {
	_, err := (&Service{}).ParseFile([]byte(`{"stat":"fail","error":{"type":"invalid_parameter","message":"api_key is invalid."}}`), "ur.json")
	if err == nil || !strings.Contains(err.Error(), "UptimeRobot API error") {
		t.Fatalf("error response = %v", err)
	}

	partial := []byte(`{"stat":"ok","pagination":{"offset":0,"limit":1,"total":3},
		"monitors":[{"id":1,"friendly_name":"A","url":"https://a.example.com","type":1,"interval":300,"status":2}]}`)
	preview, err := (&Service{}).ParseFile(partial, "ur.json")
	if err != nil {
		t.Fatalf("ParseFile() error = %v", err)
	}
	if !hasWarning(preview.Warnings, "holds 1 of the account's 3") {
		t.Fatalf("a partial page is not reported: %v", preview.Warnings)
	}
}

// TestGatus_Conditions pins the condition table: what each translates to,
// which are dropped with a note and which skip the endpoint.
func TestGatus_Conditions(t *testing.T) {
	tests := []struct {
		url       string
		condition string
		field     string
		want      string // JSON of config[field]; "" when the condition is dropped
		skip      string
	}{
		{"https://a.example", "[STATUS] == 200", "expected_statuses", `[200]`, ""},
		{"https://a.example", "[STATUS] == pat(2*)", "expected_status_ranges", ``, ""}, // exactly 2xx: the default
		{"https://a.example", "[STATUS] != 500", "expected_status_ranges", `[{"max":499,"min":100},{"max":599,"min":501}]`, ""},
		{"https://a.example", "[STATUS] >= 200", "expected_status_ranges", `[{"max":599,"min":200}]`, ""},
		{"https://a.example", "200 == [STATUS]", "expected_statuses", `[200]`, ""},
		{"https://a.example", "[STATUS] < 100", "", "", "exclude every status code"},
		{"https://a.example", "[RESPONSE_TIME] <= 500", "max_latency_ms", `500`, ""},
		{"https://a.example", "[RESPONSE_TIME] > 5", "max_latency_ms", "", ""},
		{"https://a.example", "[BODY] == OK", "body_assertions", `[{"op":"regex","value":"(?s)^OK$"}]`, ""},
		{"https://a.example", "[BODY] != pat(*error*)", "body_assertions", `[{"op":"not_contains","value":"error"}]`, ""},
		{"https://a.example", "[BODY].ready == true", "json_assertions", `[{"op":"bool_is","path":"ready","value":"true"}]`, ""},
		{"https://a.example", "[BODY][0].name != bob", "json_assertions", `[{"op":"not_equals","path":"0.name","value":"bob"}]`, ""},
		{"https://a.example", "[BODY].count <= 10", "json_assertions", `[{"op":"number_lte","path":"count","value":"10"}]`, ""},
		{"https://a.example", "[BODY].state == any(UP, DEGRADED)", "json_assertions", `[{"op":"regex","path":"state","value":"(?s)^(?:UP|DEGRADED)$"}]`, ""},
		{"https://a.example", "has([BODY].id) == true", "json_assertions", `[{"op":"exists","path":"id"}]`, ""},
		{"https://a.example", "[BODY].items[*].id == 1", "json_assertions", "", ""},
		{"https://a.example", "[CERTIFICATE_EXPIRATION] > 36h", "tls_min_days_valid", `2`, ""},
		{"http://a.example", "[CERTIFICATE_EXPIRATION] > 48h", "tls_min_days_valid", "", ""},
		{"https://a.example", "[CONNECTED] == true", "expected_statuses", "", ""},
		{"tcp://a.example:22", "[CONNECTED] == false", "", "", "connection to fail"},
		{"icmp://a.example", "[RESPONSE_TIME] < 20", "max_rtt_ms", `19`, ""},
		{"https://a.example", "[BODY].token == ${TOKEN}", "json_assertions", "", ""},
	}

	for _, tc := range tests {
		t.Run(tc.condition, func(t *testing.T) {
			document := fmt.Sprintf("endpoints:\n  - name: probe\n    url: %q\n    conditions:\n      - %q\n", tc.url, tc.condition)
			preview, err := (&Service{}).ParseFile([]byte(document), "config.yaml")
			if tc.skip != "" {
				if err == nil || !strings.Contains(err.Error(), tc.skip) {
					t.Fatalf("want the endpoint skipped (%q), got err %v", tc.skip, err)
				}
				return
			}
			if err != nil {
				t.Fatalf("ParseFile() error = %v", err)
			}
			row := rowNamed(t, preview, "probe")
			config := configOf(t, row)
			got, present := config[tc.field]
			dropped := hasWarning(row.Warnings, "was dropped")
			switch {
			case tc.want == "" && tc.field != "" && present && tc.field != "expected_statuses":
				t.Fatalf("%s = %s, want it absent", tc.field, configJSON(t, got))
			case tc.want != "" && configJSON(t, got) != tc.want:
				t.Fatalf("%s = %s, want %s", tc.field, configJSON(t, got), tc.want)
			case tc.want != "" && dropped:
				t.Fatalf("a translated condition was also reported dropped: %v", row.Warnings)
			}
			raw := []string{"[RESPONSE_TIME] > 5", "[BODY].items[*].id == 1", "[CERTIFICATE_EXPIRATION] > 48h", "[BODY].token == ${TOKEN}"}
			for _, expectDrop := range raw {
				if tc.condition == expectDrop && !dropped {
					t.Fatalf("%q must be reported as dropped: %v", tc.condition, row.Warnings)
				}
			}
		})
	}
}

func TestGatus_SkipReasonsAndThresholds(t *testing.T) {
	preview := previewOf(t, "Gatus")

	for name, fragment := range map[string]string{
		"closed-telnet":       "connection to fail",
		"decommissioned-host": "DNS answer code other than NOERROR",
		"syslog":              "UDP checks",
		"bastion":             "SSH checks",
		"env-host":            "$API_HOST",
		"sip-srv":             "record type SRV",
		"ci-results":          "no heartbeat interval",
	} {
		if skipped := skippedNamed(t, preview, name); !strings.Contains(skipped.Reason, fragment) {
			t.Errorf("%s: reason %q, want it to mention %q", name, skipped.Reason, fragment)
		}
	}

	// Thresholds come from the earliest enabled alert, falling back to the
	// provider's default-alert.
	api := rowNamed(t, preview, "api-status")
	if api.Fields["consecutive_failures_threshold"] != 2 || api.Fields["recovery_threshold"] != 1 {
		t.Fatalf("api-status thresholds = %v", api.Fields)
	}
	front := rowNamed(t, preview, "frontend")
	if front.Fields["consecutive_failures_threshold"] != 4 || front.Fields["recovery_threshold"] != 3 {
		t.Fatalf("frontend thresholds = %v", front.Fields)
	}

	// Placeholders are reported by name and the monitor waits disabled.
	partner := rowNamed(t, preview, "partner-api")
	if partner.Fields["enabled"] != false || !hasWarning(partner.Warnings, "$PARTNER_KEY in the URL") ||
		!hasWarning(partner.Warnings, "$PARTNER_TOKEN in the X-Api-Key header") {
		t.Fatalf("placeholders: enabled %v, %v", partner.Fields["enabled"], partner.Warnings)
	}
	if rowNamed(t, preview, "old-site").Fields["enabled"] != false {
		t.Fatal("enabled: false must import paused")
	}
	if rowNamed(t, preview, "oauth-api").Fields["enabled"] != false {
		t.Fatal("an endpoint whose authentication was dropped must import disabled")
	}

	// Gatus checks a status code only when a condition says so.
	staging := configOf(t, rowNamed(t, preview, "staging / frontend"))
	if configJSON(t, staging["expected_status_ranges"]) != `[{"max":399,"min":100}]` {
		t.Fatalf("[STATUS] < 400 = %s", configJSON(t, staging["expected_status_ranges"]))
	}
}

func TestGatus_ThresholdClamp(t *testing.T) {
	document := []byte("endpoints:\n  - name: probe\n    url: https://a.example\n    conditions: [\"[STATUS] == 200\"]\n    alerts:\n      - type: email\n        failure-threshold: 25\n        success-threshold: 12\n")
	preview, err := (&Service{}).ParseFile(document, "config.yaml")
	if err != nil {
		t.Fatalf("ParseFile() error = %v", err)
	}
	row := rowNamed(t, preview, "probe")
	if row.Fields["consecutive_failures_threshold"] != 10 || row.Fields["recovery_threshold"] != 10 {
		t.Fatalf("thresholds = %v", row.Fields)
	}
	if !hasWarning(row.Warnings, "failure threshold of 25") || !hasWarning(row.Warnings, "success threshold of 12") {
		t.Fatalf("clamps are not reported: %v", row.Warnings)
	}
}

// TestForeignImporters_GroupsBecomeMembership drives each fixture through
// ExecuteImport: every row is created, and each group gains junction-table
// rows for exactly its members.
func TestForeignImporters_GroupsBecomeMembership(t *testing.T) {
	for _, fixture := range loadForeignFixtures(t) {
		t.Run(fixture.source, func(t *testing.T) {
			preview := previewFixture(t, fixture)
			monitorSvc := &portableMonitorServiceMock{}
			groupSvc := &groupMembershipMock{}
			result, err := NewService(nil, monitorSvc, groupSvc).ExecuteImport(context.Background(), uuid.New(), &models.ImportExecuteRequest{
				Rows: preview.Rows, Mapping: preview.SuggestedMapping, TypeMapping: preview.SuggestedTypeMapping,
			})
			if err != nil {
				t.Fatalf("ExecuteImport() error = %v", err)
			}
			if result.SuccessCount != len(preview.Rows) {
				for _, row := range result.Results {
					if row.Status != "success" {
						t.Errorf("%s (%s): %s %s%s", row.Name, row.Type, row.Status, row.Error, row.SkipReason)
					}
				}
				t.Fatalf("created %d of %d rows", result.SuccessCount, len(preview.Rows))
			}

			ids := make(map[string]uuid.UUID, len(monitorSvc.createdMonitors))
			for _, monitor := range monitorSvc.createdMonitors {
				ids[monitor.Name] = monitor.ID
			}
			groups := 0
			for _, row := range preview.Rows {
				if row.Fields["type"] != string(models.MonitorTypeGroup) {
					continue
				}
				groups++
				members := row.Fields["group_members"].([]string)
				attached := groupSvc.added[ids[fmt.Sprint(row.Fields["name"])]]
				if len(attached) != len(members) {
					t.Fatalf("group %v gained %d membership rows, want %d", row.Fields["name"], len(attached), len(members))
				}
				for i, member := range members {
					if attached[i] != ids[member] {
						t.Fatalf("group %v member %q is not attached", row.Fields["name"], member)
					}
				}
			}
			if groups == 0 && fixture.source != "UptimeRobot" {
				t.Fatalf("the %s fixture should exercise groups", fixture.source)
			}
		})
	}
}
