//go:build !unit

package worker

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/yassinebenameur/probara/shared/models"
	"github.com/yassinebenameur/probara/shared/monitorstate"
	"github.com/yassinebenameur/probara/shared/testutil"
)

// A failure capture of a body PostgreSQL cannot hold verbatim (NUL bytes,
// invalid UTF-8) travels the result message's JSON encoding and is stored:
// jsonb accepts the worker's document as is, and ingest's Record persists
// the failure.
func TestHTTPFailureCaptureOfBinaryBodyIsStored(t *testing.T) {
	ctx := context.Background()
	dbClient, cleanup := testutil.SetupPostgresDB(ctx, t)
	defer cleanup()
	tenantID := testutil.InsertTenant(ctx, t, dbClient, "capture")
	monitorID := testutil.InsertHTTPMonitor(ctx, t, dbClient, tenantID, "capture")

	handler := func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain; charset=utf-16le")
		w.WriteHeader(500)
		_, _ = w.Write([]byte("\xff\xfee\x00r\x00r\x00o\x00r\x00"))
	}
	res, capture := runCaptureCheck(t, handler, map[string]any{"capture_failure_response": true})
	if res.Status != "failure" || capture == nil {
		t.Fatalf("status %s, capture %v", res.Status, capture)
	}

	// The worker's document needs no scrubbing to be valid jsonb.
	var ok bool
	if err := dbClient.DB.QueryRowContext(ctx, `SELECT $1::jsonb IS NOT NULL`, []byte(res.MetricsData)).Scan(&ok); err != nil {
		t.Fatalf("jsonb refuses the worker's metrics_data: %v", err)
	}

	// The message as the worker publishes it and ingest decodes it.
	now := time.Now()
	wire, err := json.Marshal(models.CheckResultMessage{
		Version:      "v1",
		JobID:        uuid.NewString(),
		MonitorID:    monitorID.String(),
		TenantID:     tenantID.String(),
		Status:       res.Status,
		ResultSource: string(models.ResultSourceMonitor),
		HTTPStatus:   res.HTTPStatus,
		LatencyMs:    res.LatencyMs,
		ErrorMessage: res.ErrorMessage,
		MetricsData:  res.MetricsData,
		StartedAt:    now,
		CompletedAt:  now,
	})
	if err != nil {
		t.Fatalf("encode message: %v", err)
	}
	var m models.CheckResultMessage
	if err := json.Unmarshal(wire, &m); err != nil {
		t.Fatalf("decode message: %v", err)
	}
	jobID := uuid.MustParse(m.JobID)
	if _, err := monitorstate.Record(ctx, dbClient.DB, monitorstate.Result{
		MonitorID:    monitorID,
		TenantID:     tenantID,
		JobID:        jobID,
		Status:       m.Status,
		ResultSource: m.ResultSource,
		HTTPStatus:   m.HTTPStatus,
		LatencyMs:    m.LatencyMs,
		ErrorMessage: m.ErrorMessage,
		MetricsData:  m.MetricsData,
		StartedAt:    m.StartedAt,
		CompletedAt:  m.CompletedAt,
	}); err != nil {
		t.Fatalf("Record: %v", err)
	}

	var stored []byte
	if err := dbClient.DB.QueryRowContext(ctx, `SELECT metrics_data FROM check_results WHERE job_id = $1`, jobID).Scan(&stored); err != nil {
		t.Fatalf("read back: %v", err)
	}
	var doc capturedHTTP
	if err := json.Unmarshal(stored, &doc); err != nil || doc.HTTP.FailureResponse == nil {
		t.Fatalf("stored metrics_data %s: %v", stored, err)
	}
	if want := "\uFFFD\uFFFDe\uFFFDr\uFFFDr\uFFFDo\uFFFDr\uFFFD"; doc.HTTP.FailureResponse.BodyExcerpt != want {
		t.Fatalf("stored excerpt = %q, want %q", doc.HTTP.FailureResponse.BodyExcerpt, want)
	}
}
