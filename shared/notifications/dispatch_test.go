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

func TestRecipientEventKey(t *testing.T) {
	round := time.Date(2026, 5, 24, 12, 0, 0, 123456000, time.FixedZone("x", 3600))
	if got := RecipientEventKey("created", round); got != "created" {
		t.Fatalf("created key = %q, want the event type alone", got)
	}
	r1 := RecipientEventKey("reminder", round)
	if r1 != RecipientEventKey("reminder", round.UTC()) {
		t.Fatalf("reminder key must not depend on the time zone")
	}
	if r1 == RecipientEventKey("reminder", round.Add(time.Microsecond)) {
		t.Fatalf("distinct reminder rounds must get distinct keys")
	}
}

func TestDispatchEnvelope_RecipientKey(t *testing.T) {
	ts := time.Date(2026, 5, 24, 12, 0, 0, 0, time.UTC)
	e := DispatchEnvelope{EventType: "reminder", Event: AlertEvent{Timestamp: ts}}
	if got, want := e.RecipientKey(), RecipientEventKey("reminder", ts); got != want {
		t.Fatalf("fallback key = %q, want %q", got, want)
	}
	e.DeliveryKey = "reminder:claimed"
	if got := e.RecipientKey(); got != "reminder:claimed" {
		t.Fatalf("key = %q, want the alerter's delivery_key", got)
	}
}

// A reopened alert sends created, acknowledged and resolved again under the
// same alert ID: from the first relapse on, the idempotency and ledger keys
// carry the episode so receivers and fan-out plugins treat them as new.
// Episode 0 keeps the original keys.
func TestEpisodeKeys(t *testing.T) {
	first := DispatchEnvelope{ChannelID: "c1", AlertID: "a1", EventType: "created"}
	relapse := first
	relapse.Event.Alert.RelapseCount = 2
	if got := first.IdempotencyKey(); got != "a1:c1:created" {
		t.Fatalf("episode 0 idempotency key = %q", got)
	}
	if got := relapse.IdempotencyKey(); got != "a1:c1:created:2" {
		t.Fatalf("relapse idempotency key = %q", got)
	}
	if got := relapse.RecipientKey(); got != "created:2" {
		t.Fatalf("relapse recipient key = %q", got)
	}

	round := time.Date(2026, 5, 24, 12, 0, 0, 0, time.UTC)
	cases := []struct {
		event   string
		round   time.Time
		relapse int
		want    string
	}{
		{"created", time.Time{}, 0, "created"},
		{"resolved", time.Time{}, 0, "resolved"},
		{"reopened", time.Time{}, 1, "reopened:1"},
		{"resolved", time.Time{}, 1, "resolved:1"},
		{"acknowledged", time.Time{}, 3, "acknowledged:3"},
		{"reminder", round, 0, "reminder:2026-05-24T12:00:00Z"},
		{"reminder", round, 2, "reminder:2026-05-24T12:00:00Z"},
	}
	for _, tc := range cases {
		if got := RecipientEventKeyFor(tc.event, tc.round, tc.relapse); got != tc.want {
			t.Fatalf("RecipientEventKeyFor(%s, %v, %d) = %q, want %q", tc.event, tc.round, tc.relapse, got, tc.want)
		}
	}
	if OvertakenAckKeyFor(0) != OvertakenAckKey || OvertakenAckKeyFor(2) != OvertakenAckKey+":2" {
		t.Fatalf("overtaken ack keys = %q / %q", OvertakenAckKeyFor(0), OvertakenAckKeyFor(2))
	}
}
