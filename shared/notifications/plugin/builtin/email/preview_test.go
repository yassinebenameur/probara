package email

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/yassinebenameur/probara/shared/notifications"
)

// TestDumpPreviews is a scratch harness for eyeballing the rendered email.
// Run: go test ./... -run TestDumpPreviews -v
func TestDumpPreviews(t *testing.T) {
	dir := os.Getenv("EMAIL_PREVIEW_DIR")
	if dir == "" {
		t.Skip("set EMAIL_PREVIEW_DIR to dump previews")
	}
	now := time.Date(2026, 8, 5, 14, 32, 0, 0, time.UTC)
	str := func(s string) *string { return &s }
	f := func(v float64) *float64 { return &v }
	tm := func(v time.Time) *time.Time { return &v }

	base := func() notifications.AlertEvent {
		return notifications.AlertEvent{
			Type:      "created",
			TenantID:  "8f14e45f-ceea-467a-9f0b-2c1a3b4d5e6f",
			Timestamp: now,
			Alert: notifications.AlertDetails{
				ID:           "b17c2e90-4a1d-4f3e-9c88-71f0a2d5c311",
				MonitorID:    "3f2a91cc-11de-4b7a-9a2e-6c5f8d0e1234",
				MonitorName:  "Checkout API · api.example.com/health",
				PolicyName:   "Production critical",
				Status:       "active",
				TriggeredAt:  now.Add(-4 * time.Minute),
				FailureCount: 3,
				LastError:    str(`Get "https://api.example.com/health": dial tcp 203.0.113.10:443: i/o timeout after 10s`),
			},
		}
	}

	cases := map[string]notifications.AlertEvent{}

	cases["down"] = base()

	reminder := base()
	reminder.Type = "reminder"
	reminder.Alert.FailureCount = 11
	reminder.Alert.TriggeredAt = now.Add(-73 * time.Minute)
	reminder.Alert.RootCauseMonitorName = str("Postgres primary (eu-west-1)")
	reminder.Alert.RootCauseDownSince = tm(now.Add(-78 * time.Minute))
	reminder.Alert.FailingLocations = []notifications.FailingLocation{
		{ID: "1", Name: "eu-west (Paris)", DownSince: tm(now.Add(-73 * time.Minute))},
		{ID: "2", Name: "us-east (Ashburn)", DownSince: tm(now.Add(-70 * time.Minute))},
	}
	cases["reminder"] = reminder

	resolved := base()
	resolved.Type = "resolved"
	resolved.Alert.Status = "resolved"
	resolved.Alert.TriggeredAt = now.Add(-26 * time.Minute)
	resolved.Alert.ResolvedAt = tm(now)
	resolved.Alert.LastError = nil
	cases["resolved"] = resolved

	latency := base()
	latency.Alert.Kind = notifications.KindLatencyAnomaly
	latency.Alert.MonitorName = "SIP register · sbc-01.example.com"
	latency.Alert.LastError = nil
	latency.Alert.FailureCount = 0
	latency.Alert.ObservedLatencyMs = f(1840)
	latency.Alert.BaselineLatencyMs = f(210)
	latency.Alert.AnomalyScore = f(6.4)
	cases["latency"] = latency

	host := base()
	host.Alert.Kind = notifications.KindHostMetric
	host.Alert.MonitorName = "voice-gw-03 agent"
	host.Alert.MetricName = str("cpu")
	host.Alert.MetricValue = f(94.2)
	host.Alert.ThresholdValue = f(90)
	host.Alert.LastError = nil
	cases["host_metric"] = host

	tlsEvt := base()
	tlsEvt.Alert.Kind = notifications.KindTLSExpiry
	tlsEvt.Alert.MonitorName = "status.example.com certificate"
	tlsEvt.Alert.MetricValue = f(11)
	tlsEvt.Alert.ThresholdValue = f(14)
	tlsEvt.Alert.LastError = nil
	tlsEvt.Alert.FailureCount = 0
	cases["tls"] = tlsEvt

	mesh := base()
	mesh.Alert.Kind = notifications.KindMeshEdge
	mesh.Alert.MonitorID = emptyUUID
	mesh.Alert.MonitorName = "mesh: eu-west → us-east"
	mesh.Alert.SourceLocationName = str("eu-west (Paris)")
	mesh.Alert.TargetLocationName = str("us-east (Ashburn)")
	mesh.Alert.LastError = str("mesh probe timeout: no response in 5s")
	cases["mesh"] = mesh

	for name, event := range cases {
		html, err := RenderHTML(event, Templates{}, "https://probara.example.com")
		if err != nil {
			t.Fatalf("%s html: %v", name, err)
		}
		if err := os.WriteFile(filepath.Join(dir, name+".html"), []byte(html), 0o644); err != nil {
			t.Fatal(err)
		}
		text, err := RenderBody(event, Templates{}, "https://probara.example.com")
		if err != nil {
			t.Fatalf("%s text: %v", name, err)
		}
		if err := os.WriteFile(filepath.Join(dir, name+".txt"), []byte(text), 0o644); err != nil {
			t.Fatal(err)
		}
		subject, _ := RenderSubject(event, Templates{}, "")
		t.Logf("%-12s %s", name, subject)
	}
}
