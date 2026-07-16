package notifications

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestDispatchEnvelope_IdempotencyKey_IsDeterministic(t *testing.T) {
	a := DispatchEnvelope{ChannelID: "c1", AlertID: "a1", EventType: "created"}
	b := DispatchEnvelope{ChannelID: "c1", AlertID: "a1", EventType: "created"}
	if a.IdempotencyKey() != b.IdempotencyKey() {
		t.Fatalf("identical envelopes must produce the same key")
	}
	if !strings.Contains(a.IdempotencyKey(), "a1") || !strings.Contains(a.IdempotencyKey(), "c1") || !strings.Contains(a.IdempotencyKey(), "created") {
		t.Fatalf("key should embed all three components, got %q", a.IdempotencyKey())
	}
}

func TestDispatchEnvelope_IdempotencyKey_DiffersOnEventType(t *testing.T) {
	a := DispatchEnvelope{ChannelID: "c1", AlertID: "a1", EventType: "created"}
	b := DispatchEnvelope{ChannelID: "c1", AlertID: "a1", EventType: "resolved"}
	if a.IdempotencyKey() == b.IdempotencyKey() {
		t.Fatalf("different event types must yield different keys")
	}
}

func TestDispatchEnvelope_JSONRoundtrip(t *testing.T) {
	now := time.Date(2026, 5, 24, 12, 0, 0, 0, time.UTC)
	src := DispatchEnvelope{
		V:           1,
		ChannelID:   "00000000-0000-0000-0000-000000000001",
		ChannelType: "teams",
		AlertID:     "00000000-0000-0000-0000-000000000002",
		EventType:   "created",
		Event: AlertEvent{
			Type:      "created",
			TenantID:  "tenant-1",
			Timestamp: now,
			Alert: AlertDetails{
				ID:           "00000000-0000-0000-0000-000000000002",
				MonitorName:  "api",
				PolicyName:   "Critical",
				Status:       "active",
				TriggeredAt:  now,
				FailureCount: 3,
			},
		},
	}
	raw, err := json.Marshal(src)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	var got DispatchEnvelope
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if got.V != src.V || got.ChannelID != src.ChannelID || got.ChannelType != src.ChannelType {
		t.Errorf("envelope header lost in roundtrip: %+v", got)
	}
	if got.Event.Alert.MonitorName != src.Event.Alert.MonitorName {
		t.Errorf("event payload lost in roundtrip: %+v", got.Event)
	}
}
