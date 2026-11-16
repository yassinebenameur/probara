//go:build !unit

package results

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	testcontainers "github.com/testcontainers/testcontainers-go"

	"github.com/yassinebenameur/probara/api/internal/models"
	groupservice "github.com/yassinebenameur/probara/api/internal/services/groups"
	sharedanalytics "github.com/yassinebenameur/probara/shared/analytics"
	"github.com/yassinebenameur/probara/shared/testutil"
)

// Bulk reads (history, list batch, metrics snapshots) drop a failed check's
// captured response and mark it; the single-result read returns it whole,
// only for the monitor's own result in the caller's tenant.
func TestService_FailureCaptureOnlyInSingleResultRead(t *testing.T) {
	testcontainers.SkipIfProviderIsNotHealthy(t)

	ctx := context.Background()
	dbClient, cleanup := testutil.SetupPostgresDB(ctx, t)
	defer cleanup()

	svc := NewService(dbClient, groupservice.NewService(dbClient), sharedanalytics.NewRepository(dbClient))
	tenantID := testutil.InsertTenant(ctx, t, dbClient, "capture")
	monitorID := testutil.InsertHTTPMonitor(ctx, t, dbClient, tenantID, "shop")
	otherMonitor := testutil.InsertHTTPMonitor(ctx, t, dbClient, tenantID, "other")
	now := time.Now().UTC()

	excerpt := strings.Repeat("upstream down ", 500)
	failed := testutil.InsertCheckResult(ctx, t, dbClient, tenantID, monitorID, now.Add(-time.Minute), "failure", "monitor", testutil.IntPtr(5))
	capture := `{"http":{"final_url":"https://shop.example","failure_response":{"status_line":"HTTP/1.1 503 Service Unavailable","body_excerpt":"` + excerpt + `"}}}`
	if _, err := dbClient.ExecContext(ctx, `UPDATE check_results SET metrics_data = $1::jsonb WHERE id = $2`, capture, failed); err != nil {
		t.Fatalf("set metrics_data: %v", err)
	}
	plain := testutil.InsertCheckResult(ctx, t, dbClient, tenantID, monitorID, now.Add(-30*time.Second), "success", "monitor", testutil.IntPtr(5))
	if _, err := dbClient.ExecContext(ctx, `UPDATE check_results SET metrics_data = '{"http":{"final_url":"https://shop.example"}}' WHERE id = $1`, plain); err != nil {
		t.Fatalf("set metrics_data: %v", err)
	}

	assertOmitted := func(where string, raw json.RawMessage) {
		t.Helper()
		var doc struct {
			HTTP map[string]json.RawMessage `json:"http"`
		}
		if err := json.Unmarshal(raw, &doc); err != nil {
			t.Fatalf("%s: metrics_data %s: %v", where, raw, err)
		}
		if _, ok := doc.HTTP["failure_response"]; ok {
			t.Fatalf("%s ships the capture: %s", where, raw)
		}
		if string(doc.HTTP[FailureResponseOmittedKey]) != "true" || string(doc.HTTP["final_url"]) != `"https://shop.example"` {
			t.Fatalf("%s: metrics_data = %s, want the marker and the other keys", where, raw)
		}
	}
	byID := func(results []models.CheckResult, id uuid.UUID) models.CheckResult {
		for _, r := range results {
			if r.ID == id {
				return r
			}
		}
		t.Fatalf("result %s missing", id)
		return models.CheckResult{}
	}

	list, err := svc.GetMonitorResults(ctx, tenantID, monitorID, 50, nil)
	if err != nil {
		t.Fatal(err)
	}
	assertOmitted("results", byID(list.Results, failed).MetricsData)
	if got := string(byID(list.Results, plain).MetricsData); strings.Contains(got, FailureResponseOmittedKey) {
		t.Fatalf("a row without a capture got the marker: %s", got)
	}
	since := now.Add(-time.Hour)
	list, err = svc.GetMonitorResults(ctx, tenantID, monitorID, 0, &since)
	if err != nil {
		t.Fatal(err)
	}
	assertOmitted("results since", byID(list.Results, failed).MetricsData)

	batch, err := svc.GetMonitorResultsBatch(ctx, tenantID, []uuid.UUID{monitorID}, 20)
	if err != nil {
		t.Fatal(err)
	}
	assertOmitted("batch", byID(batch.Results[monitorID], failed).MetricsData)

	snaps, err := svc.GetMonitorMetricsSnapshots(ctx, tenantID, monitorID, models.MonitorAnalyticsRange24h)
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range snaps.Snapshots {
		if strings.Contains(string(s.MetricsData), "body_excerpt") {
			t.Fatalf("snapshots ship the capture: %s", s.MetricsData)
		}
	}

	one, err := svc.GetMonitorResult(ctx, tenantID, monitorID, failed)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(one.MetricsData), excerpt) || one.Status != "failure" {
		t.Fatalf("single read = %s %s, want the whole capture", one.Status, one.MetricsData)
	}

	if _, err := svc.GetMonitorResult(ctx, tenantID, otherMonitor, failed); !errors.Is(err, ErrResultNotFound) {
		t.Fatalf("another monitor's result: err = %v, want ErrResultNotFound", err)
	}
	if _, err := svc.GetMonitorResult(ctx, tenantID, monitorID, uuid.New()); !errors.Is(err, ErrResultNotFound) {
		t.Fatalf("unknown result: err = %v", err)
	}
	otherTenant := testutil.InsertTenant(ctx, t, dbClient, "capture-other")
	if _, err := svc.GetMonitorResult(ctx, otherTenant, monitorID, failed); err == nil || errors.Is(err, ErrResultNotFound) {
		t.Fatalf("another tenant: err = %v, want monitor not found", err)
	}
}
