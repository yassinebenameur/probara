package worker

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/yassinebenameur/probara/shared/config"
	"github.com/yassinebenameur/probara/shared/locationauth"
	"github.com/yassinebenameur/probara/shared/logger"
	"github.com/yassinebenameur/probara/shared/models"
)

// spyResultPublisher records published result messages for assertions.
type spyResultPublisher struct {
	subjects []string
	headers  []map[string][]string
	messages []models.CheckResultMessage
}

func TestPublishResultSignsPrivateLocationMessage(t *testing.T) {
	credential := base64.RawURLEncoding.EncodeToString(make([]byte, 32))
	spy := &spyResultPublisher{}
	w := newPublishTestWorker(spy)
	w.config.LocationID = uuid.New().String()
	w.config.LocationCredential = credential
	msg := models.CheckResultMessage{
		Version: "v1", JobID: uuid.New().String(), MonitorID: uuid.New().String(),
		TenantID: uuid.New().String(), LocationID: w.config.LocationID,
		Status: "success", ResultSource: "monitor", CompletedAt: time.Now().UTC(),
	}
	if err := w.publishResultMessage(context.Background(), msg); err != nil {
		t.Fatal(err)
	}
	if want := models.CheckResultSubjectForLocation(models.CheckResultSubject, w.config.LocationID); spy.subjects[0] != want {
		t.Fatalf("subject = %q, want %q", spy.subjects[0], want)
	}
	got := spy.messages[0]
	if got.LocationSignature == "" {
		t.Fatal("missing location signature")
	}
	sig := got.LocationSignature
	got.LocationSignature = ""
	if err := locationauth.VerifyJSON(credential, sig, got); err != nil {
		t.Fatalf("verify signature: %v", err)
	}
}

func (s *spyResultPublisher) PublishJSON(_ context.Context, subject string, v interface{}, headers map[string][]string) error {
	data, err := json.Marshal(v)
	if err != nil {
		return err
	}
	var msg models.CheckResultMessage
	if err := json.Unmarshal(data, &msg); err != nil {
		return err
	}
	s.subjects = append(s.subjects, subject)
	s.headers = append(s.headers, headers)
	s.messages = append(s.messages, msg)
	return nil
}

func newPublishTestWorker(spy *spyResultPublisher) *Worker {
	return &Worker{
		config: &config.WorkerConfig{
			CheckResultStream:  models.CheckResultStream,
			CheckResultSubject: models.CheckResultSubject,
		},
		results: spy,
		logger:  logger.New("worker-test", "error"),
	}
}

func TestPublishResultMeshProbeCarriesTargetLocation(t *testing.T) {
	spy := &spyResultPublisher{}
	w := newPublishTestWorker(spy)

	targetID := uuid.New().String()
	job := &models.Job{ID: uuid.New().String(), TenantID: uuid.New().String()}
	payload := &models.CheckJobPayload{
		Type:       models.MonitorTypeMeshProbe,
		LocationID: "loc-src",
		Config:     json.RawMessage(`{"target_location_id":"` + targetID + `","endpoint":"10.0.0.1:8080"}`),
	}
	latency := int64(12)
	result := &CheckResult{Status: "success", LatencyMs: &latency}

	if err := w.publishResult(context.Background(), job, payload, result, time.Now()); err != nil {
		t.Fatalf("publishResult: %v", err)
	}

	if len(spy.messages) != 1 {
		t.Fatalf("published messages = %d, want 1", len(spy.messages))
	}
	msg := spy.messages[0]
	if msg.Mesh == nil || msg.Mesh.TargetLocationID != targetID {
		t.Fatalf("mesh info = %+v, want target %s", msg.Mesh, targetID)
	}
	if msg.LocationID != "loc-src" {
		t.Fatalf("location_id = %q, want loc-src", msg.LocationID)
	}
}

func TestPublishResultMeshProbeDropsWithoutTarget(t *testing.T) {
	cases := []struct {
		name   string
		config json.RawMessage
	}{
		{"unparseable config", json.RawMessage(`{`)},
		{"empty target", json.RawMessage(`{"endpoint":"10.0.0.1:8080"}`)},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			spy := &spyResultPublisher{}
			w := newPublishTestWorker(spy)

			job := &models.Job{ID: uuid.New().String(), TenantID: uuid.New().String()}
			payload := &models.CheckJobPayload{Type: models.MonitorTypeMeshProbe, Config: tc.config}
			result := &CheckResult{Status: "success"}

			// Without the edge key the result is unroutable: publish nothing,
			// return nil.
			if err := w.publishResult(context.Background(), job, payload, result, time.Now()); err != nil {
				t.Fatalf("publishResult: %v", err)
			}
			if len(spy.messages) != 0 {
				t.Fatalf("published messages = %d, want 0", len(spy.messages))
			}
		})
	}
}

func TestPublishResultCarriesCheckOutcome(t *testing.T) {
	spy := &spyResultPublisher{}
	w := newPublishTestWorker(spy)

	monitorID := uuid.New().String()
	tenantID := uuid.New().String()
	jobID := uuid.New().String()
	httpStatus := 503
	latency := int64(120)
	errMsg := "upstream unavailable"
	startedAt := time.Now().Add(-time.Second)

	job := &models.Job{ID: jobID, TenantID: tenantID}
	payload := &models.CheckJobPayload{MonitorID: monitorID, LocationID: "loc-1"}
	result := &CheckResult{
		Status:               "failure",
		HTTPStatus:           &httpStatus,
		LatencyMs:            &latency,
		ErrorMessage:         &errMsg,
		MatchedBodySubstring: true,
		MetricsData:          json.RawMessage(`{"dns_ms":3}`),
	}

	if err := w.publishResult(context.Background(), job, payload, result, startedAt); err != nil {
		t.Fatalf("publishResult: %v", err)
	}

	if len(spy.messages) != 1 {
		t.Fatalf("published messages = %d, want 1", len(spy.messages))
	}
	msg := spy.messages[0]
	if spy.subjects[0] != models.CheckResultSubject {
		t.Fatalf("subject = %q, want %q", spy.subjects[0], models.CheckResultSubject)
	}
	if got := spy.headers[0]["Nats-Msg-Id"]; len(got) != 1 || got[0] != jobID+"/monitor" {
		t.Fatalf("Nats-Msg-Id = %v, want [%s/monitor]", got, jobID)
	}
	if msg.Version != "v1" || msg.JobID != jobID || msg.MonitorID != monitorID || msg.TenantID != tenantID {
		t.Fatalf("identity fields wrong: %+v", msg)
	}
	if msg.LocationID != "loc-1" {
		t.Fatalf("location_id = %q, want loc-1", msg.LocationID)
	}
	if msg.Status != "failure" || msg.ResultSource != string(models.ResultSourceMonitor) {
		t.Fatalf("status/source = %s/%s", msg.Status, msg.ResultSource)
	}
	if msg.HTTPStatus == nil || *msg.HTTPStatus != httpStatus || msg.LatencyMs == nil || *msg.LatencyMs != latency {
		t.Fatalf("http/latency wrong: %+v", msg)
	}
	if msg.ErrorMessage == nil || *msg.ErrorMessage != errMsg || !msg.MatchedBodySubstring {
		t.Fatalf("error/matched wrong: %+v", msg)
	}
	if string(msg.MetricsData) != `{"dns_ms":3}` {
		t.Fatalf("metrics_data = %s", msg.MetricsData)
	}
}

func TestPublishExpiredJobUsesPlatformSource(t *testing.T) {
	spy := &spyResultPublisher{}
	w := newPublishTestWorker(spy)

	monitorID := uuid.New().String()
	payload := mustMarshalPayload(t, models.CheckJobPayload{MonitorID: monitorID})
	job := &models.Job{ID: uuid.New().String(), TenantID: uuid.New().String(), Payload: payload}

	if err := w.publishExpiredJob(context.Background(), job); err != nil {
		t.Fatalf("publishExpiredJob: %v", err)
	}

	if len(spy.messages) != 1 {
		t.Fatalf("published messages = %d, want 1", len(spy.messages))
	}
	msg := spy.messages[0]
	if msg.ResultSource != string(models.ResultSourcePlatform) {
		t.Fatalf("result_source = %q, want platform", msg.ResultSource)
	}
	if msg.Status != string(models.ResultStatusError) {
		t.Fatalf("status = %q, want error", msg.Status)
	}
	if msg.ErrorMessage == nil || *msg.ErrorMessage == "" {
		t.Fatalf("error_message missing: %+v", msg)
	}
	if got := spy.headers[0]["Nats-Msg-Id"]; len(got) != 1 || got[0] != job.ID+"/platform" {
		t.Fatalf("Nats-Msg-Id = %v, want [%s/platform]", got, job.ID)
	}
}

func TestPublishExpiredJobSkipsUnparseablePayload(t *testing.T) {
	spy := &spyResultPublisher{}
	w := newPublishTestWorker(spy)

	job := &models.Job{ID: uuid.New().String(), TenantID: uuid.New().String(), Payload: json.RawMessage(`{`)}
	if err := w.publishExpiredJob(context.Background(), job); err != nil {
		t.Fatalf("publishExpiredJob: %v", err)
	}
	if len(spy.messages) != 0 {
		t.Fatalf("published messages = %d, want 0", len(spy.messages))
	}
}

func mustMarshalPayload(t *testing.T, p models.CheckJobPayload) json.RawMessage {
	t.Helper()
	data, err := json.Marshal(p)
	if err != nil {
		t.Fatalf("marshal payload: %v", err)
	}
	return data
}
