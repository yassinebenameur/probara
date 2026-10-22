package ingest

import (
	"fmt"
	"reflect"
	"slices"
	"testing"

	"github.com/yassinebenameur/probara/shared/models"
)

// TestSignedNATSStructsFrozen pins every field of the messages private
// locations sign. locationauth.VerifyJSON checks a signature by re-marshalling
// the receiver's own struct, so a field the sender knows and the receiver does
// not is dropped before the HMAC and the message fails verification: a new
// non-empty field breaks every older verifier (an omitempty field left empty
// is harmless). Extend inside metrics_data or reuse Version instead; see the
// "Signed NATS structs" gotcha in CLAUDE.md.
func TestSignedNATSStructsFrozen(t *testing.T) {
	cases := []struct {
		value any
		want  []string
	}{
		{models.LocationHeartbeat{}, []string{
			`LocationID string json:"location_id"`,
			`Hostname string json:"hostname,omitempty"`,
			`Version string json:"version,omitempty"`,
			`Timestamp time.Time json:"timestamp"`,
			`Signature string json:"signature,omitempty"`,
		}},
		{models.CheckResultMessage{}, []string{
			`Version string json:"version"`,
			`JobID string json:"job_id"`,
			`MonitorID string json:"monitor_id"`,
			`TenantID string json:"tenant_id"`,
			`LocationID string json:"location_id,omitempty"`,
			`Status string json:"status"`,
			`ResultSource string json:"result_source"`,
			`HTTPStatus *int json:"http_status,omitempty"`,
			`LatencyMs *int64 json:"latency_ms,omitempty"`,
			`ErrorMessage *string json:"error_message,omitempty"`,
			`MatchedBodySubstring bool json:"matched_body_substring"`,
			`MetricsData json.RawMessage json:"metrics_data,omitempty"`,
			`StartedAt time.Time json:"started_at"`,
			`CompletedAt time.Time json:"completed_at"`,
			`Mesh *models.MeshResultInfo json:"mesh,omitempty"`,
			`LocationSignature string json:"location_signature,omitempty"`,
		}},
		// Nested inside CheckResultMessage, so it is signed too.
		{models.MeshResultInfo{}, []string{
			`TargetLocationID string json:"target_location_id"`,
		}},
	}
	for _, tc := range cases {
		typ := reflect.TypeOf(tc.value)
		got := make([]string, 0, typ.NumField())
		for i := range typ.NumField() {
			f := typ.Field(i)
			got = append(got, fmt.Sprintf("%s %s json:%q", f.Name, f.Type, f.Tag.Get("json")))
		}
		if !slices.Equal(got, tc.want) {
			t.Errorf("%s fields changed; signed NATS structs are verified by re-marshalling the receiver's struct, "+
				"so older verifiers reject a new non-empty field (CLAUDE.md, \"Signed NATS structs\" gotcha)\n got: %q\nwant: %q",
				typ, got, tc.want)
		}
	}
}
