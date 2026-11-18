//go:build !unit

package monitorstate

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/yassinebenameur/probara/shared/testutil"
)

// Text PostgreSQL refuses (NUL, invalid UTF-8, the \u0000 and lone surrogate
// escapes jsonb rejects) must never stop a check result from being stored.
func TestRecordStoresUnsafeCheckOutput(t *testing.T) {
	ctx := context.Background()
	dbClient, cleanup := testutil.SetupPostgresDB(ctx, t)
	defer cleanup()
	tenantID := testutil.InsertTenant(ctx, t, dbClient, "pgsafe")
	monitorID := testutil.InsertHTTPMonitor(ctx, t, dbClient, tenantID, "pgsafe")

	message := "registrar said \x00 and \xff"
	now := time.Now()
	r := Result{
		MonitorID:    monitorID,
		TenantID:     tenantID,
		JobID:        uuid.New(),
		Status:       "failure",
		ResultSource: "monitor",
		ErrorMessage: &message,
		MetricsData:  json.RawMessage(`{"http":{"body":"e\u0000r\u0000","lone":"\ud800","literal":"\\u0000"}}`),
		StartedAt:    now,
		CompletedAt:  now,
	}
	if _, err := Record(ctx, dbClient.DB, r); err != nil {
		t.Fatalf("Record: %v", err)
	}

	var gotMessage string
	var doc struct {
		HTTP struct{ Body, Lone, Literal string } `json:"http"`
	}
	var raw []byte
	if err := dbClient.DB.QueryRowContext(ctx,
		`SELECT error_message, metrics_data FROM check_results WHERE job_id = $1`, r.JobID,
	).Scan(&gotMessage, &raw); err != nil {
		t.Fatalf("read back: %v", err)
	}
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatalf("metrics_data: %v", err)
	}
	if gotMessage != "registrar said \uFFFD and \uFFFD" {
		t.Errorf("error_message = %q", gotMessage)
	}
	if doc.HTTP.Body != "e\uFFFDr\uFFFD" || doc.HTTP.Lone != "\uFFFD" || doc.HTTP.Literal != `\u0000` {
		t.Errorf("metrics_data = %s", raw)
	}
}
