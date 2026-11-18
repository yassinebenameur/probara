package alerts

import (
	"encoding/json"
	"testing"

	"github.com/google/uuid"
)

// A live event for a monitor-less alert keeps its kind and location subject,
// so the alerts page renders and links the new row like a listed one.
func TestSubscriberConvertsLocationSubject(t *testing.T) {
	locationID := uuid.New()
	raw := `{"type":"created","tenant_id":"` + uuid.New().String() + `","alert":{
		"id":"` + uuid.New().String() + `","monitor_id":"` + uuid.Nil.String() + `","monitor_name":"factory-floor",
		"kind":"location_offline","status":"active","source_location_id":"` + locationID.String() + `",
		"source_location_name":"factory-floor"}}`
	var event AlertEvent
	if err := json.Unmarshal([]byte(raw), &event); err != nil {
		t.Fatal(err)
	}
	got := (&Subscriber{}).convertToAlertWithDetails(&event)
	if got.Kind != "location_offline" || got.MonitorID != nil {
		t.Fatalf("kind=%q monitor_id=%v", got.Kind, got.MonitorID)
	}
	if got.SourceLocationID == nil || *got.SourceLocationID != locationID ||
		got.SourceLocationName == nil || *got.SourceLocationName != "factory-floor" || got.TargetLocationID != nil {
		t.Fatalf("location subject = %v %v %v", got.SourceLocationID, got.SourceLocationName, got.TargetLocationID)
	}
}
