//go:build !unit

package importservice

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/yassinebenameur/probara/api/internal/models"
	groupservice "github.com/yassinebenameur/probara/api/internal/services/groups"
)

// Passive monitors saved the way the web forms save them (a group with
// timeout 30, a push monitor with timeout = interval) round-trip through a
// keyed export as unchanged: apply builds passive types with timeout 0,
// which means nothing for them and must not plan an update or write a
// version on every apply.
func TestApply_ExportRoundTripPassiveTimeoutsUnchanged(t *testing.T) {
	h := newApplyHarness(t)
	memberKey, groupKey, pushKey := "member", "edge", "cron"
	member, err := h.monitors.CreateMonitor(h.admin, h.tenantID, &models.CreateMonitorRequest{
		Name: "Member", Key: &memberKey, Type: models.MonitorTypeHTTP, IntervalSeconds: 60, TimeoutSeconds: 10,
		Config: json.RawMessage(`{"url":"https://member.example.com","method":"GET","expected_status":200}`),
	})
	if err != nil {
		t.Fatal(err)
	}
	groupConfig, _ := json.Marshal(map[string]any{"monitor_ids": []string{member.ID.String()}})
	group, err := h.monitors.CreateMonitor(h.admin, h.tenantID, &models.CreateMonitorRequest{
		Name: "Edge", Key: &groupKey, Type: models.MonitorTypeGroup, IntervalSeconds: 60, TimeoutSeconds: 30,
		Config: groupConfig,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := groupservice.NewService(h.db).AddMonitorsToGroup(context.Background(), h.tenantID, group.ID, []uuid.UUID{member.ID}); err != nil {
		t.Fatal(err)
	}
	if _, err := h.monitors.CreateMonitor(h.admin, h.tenantID, &models.CreateMonitorRequest{
		Name: "Nightly job", Key: &pushKey, Type: models.MonitorTypePush, IntervalSeconds: 300, TimeoutSeconds: 300,
		Config: json.RawMessage(`{"push_token":"","expected_interval_seconds":300,"grace_period_seconds":600}`),
	}); err != nil {
		t.Fatal(err)
	}

	data, err := h.svc.ExportMonitors(h.admin, h.tenantID, ExportOptions{KeyedOnly: true})
	if err != nil {
		t.Fatal(err)
	}
	plan := h.plan(h.admin, string(data))
	if !plan.Valid || plan.Summary.Unchanged != 3 || plan.Summary.Create+plan.Summary.Update+plan.Summary.Delete != 0 {
		t.Fatalf("round-trip plan = %+v\ndocument:\n%s", plan, data)
	}
	versions := h.versionCount()
	result := h.apply(applyRequest(string(data)))
	if result.Applied || result.Summary.Unchanged != 3 {
		t.Fatalf("apply = %+v", result)
	}
	if got := h.versionCount(); got != versions {
		t.Fatalf("an unchanged apply recorded versions: %d -> %d", versions, got)
	}
	if got := h.monitorByKey(groupKey).TimeoutSeconds; got != 30 {
		t.Fatalf("group timeout = %d after apply, want the stored 30", got)
	}

	// An interval change still applies, and keeps the stored timeout.
	if !strings.Contains(string(data), " interval_seconds: 300") {
		t.Fatalf("push interval not in document:\n%s", data)
	}
	edited := strings.Replace(string(data), " interval_seconds: 300", " interval_seconds: 600", 1)
	plan = h.plan(h.admin, edited)
	if got := actions(plan); !plan.Valid || got[pushKey] != models.ApplyActionUpdate || got[groupKey] != models.ApplyActionUnchanged {
		t.Fatalf("interval edit plan = %+v", plan)
	}
	h.apply(applyRequest(edited))
	push := h.monitorByKey(pushKey)
	if push.IntervalSeconds != 600 || push.TimeoutSeconds != 300 {
		t.Fatalf("push after interval edit = interval %d, timeout %d; want 600, 300", push.IntervalSeconds, push.TimeoutSeconds)
	}
}
