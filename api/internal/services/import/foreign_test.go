package importservice

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/yassinebenameur/probara/api/internal/models"
	"github.com/yassinebenameur/probara/api/internal/validation"
	"github.com/yassinebenameur/probara/shared/secrets"
)

var updateGolden = flag.Bool("update", false, "rewrite the importer golden files in testdata/")

// foreignFixture is one hand-written source document and what it must be
// recognized as.
type foreignFixture struct {
	source   string
	file     string
	schema   string
	upload   string // the filename the preview sees
	contents []byte
}

func loadForeignFixtures(t *testing.T) []foreignFixture {
	t.Helper()
	fixtures := []foreignFixture{
		{source: "Better Stack", file: "betterstack-export.json", schema: models.ImportSchemaBetterStackExport, upload: "better-stack.json"},
		{source: "UptimeRobot", file: "uptimerobot-getmonitors.json", schema: models.ImportSchemaUptimeRobotExport, upload: "uptimerobot.json"},
		{source: "Gatus", file: "gatus-config.yaml", schema: models.ImportSchemaGatusConfig, upload: "config.yaml"},
	}
	for i := range fixtures {
		data, err := os.ReadFile(filepath.Join("testdata", fixtures[i].file))
		if err != nil {
			t.Fatalf("read fixture: %v", err)
		}
		fixtures[i].contents = data
	}
	return fixtures
}

func previewFixture(t *testing.T, fixture foreignFixture) *models.ImportPreviewResponse {
	t.Helper()
	preview, err := (&Service{}).ParseFile(fixture.contents, fixture.upload)
	if err != nil {
		t.Fatalf("%s: ParseFile() error = %v", fixture.source, err)
	}
	return preview
}

func previewOf(t *testing.T, source string) *models.ImportPreviewResponse {
	t.Helper()
	for _, fixture := range loadForeignFixtures(t) {
		if fixture.source == source {
			return previewFixture(t, fixture)
		}
	}
	t.Fatalf("no fixture for %s", source)
	return nil
}

func rowNamed(t *testing.T, preview *models.ImportPreviewResponse, name string) models.ImportRow {
	t.Helper()
	for _, row := range preview.Rows {
		if row.Fields["name"] == name {
			return row
		}
	}
	t.Fatalf("no imported row named %q", name)
	return models.ImportRow{}
}

func skippedNamed(t *testing.T, preview *models.ImportPreviewResponse, name string) models.ImportSkippedRow {
	t.Helper()
	for _, skipped := range preview.SkippedRows {
		if skipped.Name == name {
			return skipped
		}
	}
	t.Fatalf("%q was not skipped; skipped rows: %+v", name, preview.SkippedRows)
	return models.ImportSkippedRow{}
}

func configOf(t *testing.T, row models.ImportRow) map[string]interface{} {
	t.Helper()
	config, ok := row.Fields["config"].(map[string]interface{})
	if !ok {
		t.Fatalf("row %v has no nested config map, got %T", row.Fields["name"], row.Fields["config"])
	}
	return config
}

// configJSON compares through JSON so typed maps and slices match.
func configJSON(t *testing.T, value interface{}) string {
	t.Helper()
	raw, err := json.Marshal(value)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var generic interface{}
	if err := json.Unmarshal(raw, &generic); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	normalized, _ := json.Marshal(generic)
	return string(normalized)
}

// TestForeignImporters_Golden pins every source's whole translation: rows,
// per-monitor notes, skipped records and preview warnings. Run with
// -update after an intended change and review the diff.
func TestForeignImporters_Golden(t *testing.T) {
	for _, fixture := range loadForeignFixtures(t) {
		t.Run(fixture.source, func(t *testing.T) {
			preview := previewFixture(t, fixture)
			got, err := json.MarshalIndent(struct {
				Schema      string                    `json:"schema"`
				Warnings    []string                  `json:"warnings"`
				Rows        []models.ImportRow        `json:"rows"`
				SkippedRows []models.ImportSkippedRow `json:"skipped_rows"`
			}{preview.Schema, preview.Warnings, preview.Rows, preview.SkippedRows}, "", "  ")
			if err != nil {
				t.Fatalf("marshal: %v", err)
			}
			got = append(got, '\n')

			golden := filepath.Join("testdata", strings.TrimSuffix(fixture.file, filepath.Ext(fixture.file))+".golden.json")
			if *updateGolden {
				if err := os.WriteFile(golden, got, 0o644); err != nil {
					t.Fatalf("write golden: %v", err)
				}
			}
			want, err := os.ReadFile(golden)
			if err != nil {
				t.Fatalf("read golden (run with -update to create it): %v", err)
			}
			if !bytes.Equal(got, want) {
				t.Fatalf("translation differs from %s (run with -update and review the diff)\n%s", golden, got)
			}
		})
	}
}

// TestForeignImporters_EveryRowPassesTheRegistry is the mechanical guard on
// the three mapping tables: every emitted row names a registry type and
// carries a config, schedule and threshold the API accepts.
func TestForeignImporters_EveryRowPassesTheRegistry(t *testing.T) {
	for _, fixture := range loadForeignFixtures(t) {
		preview := previewFixture(t, fixture)
		if preview.Schema != fixture.schema {
			t.Fatalf("%s: schema = %q, want %q", fixture.source, preview.Schema, fixture.schema)
		}
		// Every canonical field a row carries must be auto-mapped, or the
		// operator would have to wire it by hand.
		mapped := map[string]string{
			"name": preview.SuggestedMapping.Name, "type": preview.SuggestedMapping.Type,
			"config": preview.SuggestedMapping.Config, "interval_seconds": preview.SuggestedMapping.IntervalSeconds,
			"timeout_seconds": preview.SuggestedMapping.TimeoutSeconds, "enabled": preview.SuggestedMapping.Enabled,
			"group_members":                  preview.SuggestedMapping.GroupMembers,
			"consecutive_failures_threshold": preview.SuggestedMapping.ConsecutiveFailuresThreshold,
			"recovery_threshold":             preview.SuggestedMapping.RecoveryThreshold,
		}
		for _, field := range preview.DetectedFields {
			if mapped[field] != field {
				t.Fatalf("%s: field %q is not auto-mapped: %+v", fixture.source, field, preview.SuggestedMapping)
			}
		}
		if len(preview.SuggestedTypeMapping) != 0 {
			t.Fatalf("%s: emitted a type the operator must map: %v", fixture.source, preview.SuggestedTypeMapping)
		}
		for _, field := range preview.DetectedFields {
			if strings.Contains(field, ".") {
				t.Fatalf("%s: field %q is dot-flattened, so the row fell onto the simple path", fixture.source, field)
			}
		}

		for _, row := range preview.Rows {
			name := row.Fields["name"]
			monitorType := models.MonitorType(fmt.Sprint(row.Fields["type"]))
			if !validation.DefaultRegistry.Has(monitorType) {
				t.Fatalf("%s: %v has unregistered type %q", fixture.source, name, monitorType)
			}
			interval, _ := row.Fields["interval_seconds"].(int)
			if interval < minImportInterval || interval > maxImportInterval {
				t.Fatalf("%s: %v interval %d out of range", fixture.source, name, interval)
			}
			timeout, _ := row.Fields["timeout_seconds"].(int)
			if validation.IsActiveCheckType(monitorType) && (timeout <= 0 || timeout >= interval) {
				t.Fatalf("%s: %v timeout %d does not fit interval %d", fixture.source, name, timeout, interval)
			}
			if threshold, ok := row.Fields["consecutive_failures_threshold"].(int); ok &&
				(threshold < minConsecutiveFailuresThreshold || threshold > maxConsecutiveFailuresThreshold) {
				t.Fatalf("%s: %v threshold %d out of range", fixture.source, name, threshold)
			}
			if monitorType == models.MonitorTypeGroup {
				// Completed in the import's second pass, once member names
				// resolve to IDs (createGroupMonitorFromConfig validates).
				if members, _ := row.Fields["group_members"].([]string); len(members) == 0 {
					t.Fatalf("%s: group %v has no members", fixture.source, name)
				}
				continue
			}
			raw, _ := json.Marshal(row.Fields["config"])
			if err := validation.DefaultRegistry.Validate(monitorType, raw); err != nil {
				t.Fatalf("%s: %v (%s) config rejected: %v\n%s", fixture.source, name, monitorType, err, raw)
			}
			if err := validation.DefaultRegistry.ValidateSchedule(monitorType, raw, interval, timeout); err != nil {
				t.Fatalf("%s: %v (%s) schedule rejected: %v", fixture.source, name, monitorType, err)
			}
			if err := validation.ValidateMonitor(&models.CreateMonitorRequest{
				Name: fmt.Sprint(name), Type: monitorType, Config: raw,
				IntervalSeconds: interval, TimeoutSeconds: timeout,
				ConsecutiveFailuresThreshold: intPointer(row.Fields["consecutive_failures_threshold"]),
				RecoveryThreshold:            intPointer(row.Fields["recovery_threshold"]),
			}); err != nil {
				t.Fatalf("%s: %v (%s) would be rejected by the API: %v", fixture.source, name, monitorType, err)
			}
		}
	}
}

func intPointer(value interface{}) *int {
	if v, ok := value.(int); ok {
		return &v
	}
	return nil
}

// TestForeignImporters_SkippedNeverEmitted: a record is either a row or a
// skipped entry with a reason, never both, and names are unique (group
// members resolve by name).
func TestForeignImporters_SkippedNeverEmitted(t *testing.T) {
	for _, fixture := range loadForeignFixtures(t) {
		preview := previewFixture(t, fixture)
		names := make(map[string]bool, len(preview.Rows))
		for _, row := range preview.Rows {
			name := strings.ToLower(fmt.Sprint(row.Fields["name"]))
			if names[name] {
				t.Fatalf("%s: name %q emitted twice", fixture.source, name)
			}
			names[name] = true
		}
		for _, skipped := range preview.SkippedRows {
			if strings.TrimSpace(skipped.Reason) == "" {
				t.Fatalf("%s: %q skipped without a reason", fixture.source, skipped.Name)
			}
			if names[strings.ToLower(skipped.Name)] {
				t.Fatalf("%s: %q is both skipped and emitted", fixture.source, skipped.Name)
			}
		}
		if len(preview.SkippedRows) == 0 {
			t.Fatalf("%s: the fixture should exercise skipped records", fixture.source)
		}
		if !hasWarning(preview.Warnings, "could not be imported") {
			t.Fatalf("%s: the preview does not summarize skipped records: %v", fixture.source, preview.Warnings)
		}
	}
}

// TestForeignImporters_SecretsOnlyInSecretFields: every credential in the
// fixtures either lands in a registered secret field (it disappears when the
// config is masked by the secrets registry) or is not imported at all, and
// never appears in a warning, a skip reason or a plain field.
func TestForeignImporters_SecretsOnlyInSecretFields(t *testing.T) {
	imported := map[string][]string{
		"Better Stack": {"bs-secret-token", "bs-basic-pass"},
		"UptimeRobot":  {"ur-secret-pass", "ur-api-key-123"},
		"Gatus":        {"gatus-static-token", "gatus-url-pass"},
	}
	never := map[string][]string{
		"Better Stack": {"hb-token-nightly", "hb-token-queue", "hb-token-weekly"},
		"UptimeRobot":  {"ur-digest-pass", "ur-heartbeat-token"},
		"Gatus":        {"gatus-ssh-pass", "gatus-oauth-secret", "gatus-external-token", "gatus-ci-token", "gatus-slack-webhook"},
	}

	for _, fixture := range loadForeignFixtures(t) {
		preview := previewFixture(t, fixture)

		var exposed strings.Builder
		for _, row := range preview.Rows {
			monitorType := fmt.Sprint(row.Fields["type"])
			raw, _ := json.Marshal(row.Fields["config"])
			masked, err := secrets.MaskMonitorConfig(monitorType, raw)
			if err != nil {
				t.Fatalf("mask %v: %v", row.Fields["name"], err)
			}
			rest := make(map[string]interface{}, len(row.Fields))
			for key, value := range row.Fields {
				if key != "config" {
					rest[key] = value
				}
			}
			restJSON, _ := json.Marshal(rest)
			exposed.Write(masked)
			exposed.Write(restJSON)
			exposed.WriteString(strings.Join(row.Warnings, "\n"))
		}
		skippedJSON, _ := json.Marshal(preview.SkippedRows)
		exposed.Write(skippedJSON)
		exposed.WriteString(strings.Join(preview.Warnings, "\n"))

		rowsJSON, _ := json.Marshal(preview.Rows)
		for _, secret := range imported[fixture.source] {
			if !strings.Contains(string(rowsJSON), secret) {
				t.Fatalf("%s: credential %q was not carried into a secret field", fixture.source, secret)
			}
			if strings.Contains(exposed.String(), secret) {
				t.Fatalf("%s: credential %q is visible outside a secret field", fixture.source, secret)
			}
		}
		for _, secret := range never[fixture.source] {
			if strings.Contains(string(rowsJSON), secret) || strings.Contains(exposed.String(), secret) {
				t.Fatalf("%s: %q must not be imported anywhere", fixture.source, secret)
			}
		}
	}
}

// TestForeignImporters_DetectionMatrix runs every source document through
// every recognizer: each must claim its own format and nothing else, and the
// generic shapes must fall through to field mapping.
func TestForeignImporters_DetectionMatrix(t *testing.T) {
	kuma := []byte(kumaBackupFixture)
	portable := []byte("kind: monitor_export\nversion: 1\nmonitors:\n  - name: Web\n    type: http\n    config:\n      url: https://web.example\n      method: GET\n    interval_seconds: 60\n    timeout_seconds: 30\n    enabled: true\n")
	documents := map[string][]byte{
		"portable":              portable,
		"kuma":                  kuma,
		"generic json array":    []byte(`[{"name":"a","type":"http","url":"https://a.test"}]`),
		"generic json monitors": []byte(`{"monitors":[{"name":"a","type":"http","url":"https://a.test"},{"name":"b","type":1,"url":"https://b.test"}]}`),
		"generic json data":     []byte(`{"data":[{"name":"a","type":"monitor","url":"https://a.test"}]}`),
		"json:api other kinds":  []byte(`{"data":[{"id":"1","type":"incident","attributes":{"name":"x"}}]}`),
		"generic yaml":          []byte("endpoints:\n  - name: a\n    url: https://a.test\n  - name: b\n    url: https://b.test\n"),
		"yaml without conds":    []byte("endpoints:\n  - name: a\n    url: https://a.test\n    conditions: []\n"),
		"csv":                   []byte("name,type,url\na,http,https://a.test\n"),
	}
	for _, fixture := range loadForeignFixtures(t) {
		documents[fixture.source] = fixture.contents
	}

	svc := &Service{}
	detectors := map[string]func([]byte) bool{
		"portable": func(data []byte) bool {
			_, ok, _ := svc.parsePortableExport(data)
			return ok
		},
		"kuma": func(data []byte) bool {
			_, ok, _ := svc.parseKumaExport(data)
			return ok
		},
		"Better Stack": func(data []byte) bool {
			_, ok, _ := svc.parseBetterStackExport(data)
			return ok
		},
		"UptimeRobot": func(data []byte) bool {
			_, ok, _ := svc.parseUptimeRobotExport(data)
			return ok
		},
		"Gatus": func(data []byte) bool {
			_, ok, _ := svc.parseGatusConfig(data)
			return ok
		},
	}

	for docName, data := range documents {
		for detectorName, detect := range detectors {
			want := docName == detectorName
			if got := detect(data); got != want {
				t.Errorf("%s detector on the %s document = %v, want %v", detectorName, docName, got, want)
			}
		}
	}

	// End to end, the generic documents keep no schema.
	for _, name := range []string{"generic json array", "generic json monitors", "generic json data", "json:api other kinds", "generic yaml"} {
		filename := "list.json"
		if strings.Contains(name, "yaml") {
			filename = "list.yaml"
		}
		preview, err := svc.ParseFile(documents[name], filename)
		if err != nil {
			t.Fatalf("%s: ParseFile() error = %v", name, err)
		}
		if preview.Schema != "" {
			t.Fatalf("%s was recognized as %q", name, preview.Schema)
		}
	}

	// Content sniffing (no extension) reaches the same adapters.
	for _, fixture := range loadForeignFixtures(t) {
		preview, err := svc.ParseFile(fixture.contents, "export")
		if err != nil || preview.Schema != fixture.schema {
			t.Fatalf("%s without an extension: schema %v, err %v", fixture.source, preview, err)
		}
	}
}
