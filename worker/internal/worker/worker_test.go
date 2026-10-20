package worker

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"

	"github.com/yassinebenameur/probara/shared/config"
	"github.com/yassinebenameur/probara/shared/logger"
	"github.com/yassinebenameur/probara/shared/metrics"
	"github.com/yassinebenameur/probara/shared/models"
	"github.com/yassinebenameur/probara/shared/queue"
)

const fakeCallType = "fake_call"

// fakeCallChecker stands in for a side-effecting checker: configs with
// {"method":"invite"} place a "call". It counts runs and can be slow.
type fakeCallChecker struct {
	delay time.Duration
	runs  atomic.Int32
}

func (c *fakeCallChecker) Check(ctx context.Context, _ json.RawMessage, _ int) CheckResult {
	c.runs.Add(1)
	select {
	case <-time.After(c.delay):
	case <-ctx.Done():
	}
	return CheckResult{Status: "success"}
}

func (c *fakeCallChecker) SideEffecting(config json.RawMessage) bool {
	var cfg struct {
		Method string `json:"method"`
	}
	return json.Unmarshal(config, &cfg) == nil && cfg.Method == "invite"
}

// flakyPublisher fails its first failures publishes, then records the rest.
type flakyPublisher struct {
	mu        sync.Mutex
	failures  int
	attempts  int
	published []models.CheckResultMessage
}

func (p *flakyPublisher) PublishJSON(_ context.Context, _ string, v interface{}, _ map[string][]string) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.attempts++
	if p.attempts <= p.failures {
		return errors.New("nats: no responders available for request")
	}
	p.published = append(p.published, v.(models.CheckResultMessage))
	return nil
}

type fakeJobMessage struct {
	inProgress atomic.Int32
}

func newProcessTestWorker(t *testing.T, pub resultPublisher, checker Checker) *Worker {
	t.Helper()
	w := NewWorker(&config.WorkerConfig{
		CheckResultStream:  models.CheckResultStream,
		CheckResultSubject: models.CheckResultSubject,
	}, logger.New("worker-test", "error"), metrics.NewRegistry("worker"), nil)
	w.results = pub
	w.publishRetryBackoff = []time.Duration{time.Millisecond, time.Millisecond, time.Millisecond}
	w.RegisterChecker(fakeCallType, checker)
	return w
}

func newJobMessage(t *testing.T, method string, numDelivered uint64, deadline *time.Time) (*queue.Message, *fakeJobMessage) {
	t.Helper()
	payload, err := json.Marshal(models.CheckJobPayload{
		MonitorID:      uuid.New().String(),
		Type:           fakeCallType,
		Config:         json.RawMessage(`{"method":"` + method + `"}`),
		TimeoutSeconds: 5,
	})
	if err != nil {
		t.Fatal(err)
	}
	job := models.Job{
		ID: uuid.New().String(), TenantID: uuid.New().String(),
		Type: models.JobTypeCheck, Version: "v1", Payload: payload, Deadline: deadline,
	}
	data, err := json.Marshal(job)
	if err != nil {
		t.Fatal(err)
	}
	fake := &fakeJobMessage{}
	return &queue.Message{
		Data:         data,
		NumDelivered: numDelivered,
		InProgress: func() error {
			fake.inProgress.Add(1)
			return nil
		},
	}, fake
}

func counterValue(t *testing.T, vec *prometheus.CounterVec, label string) float64 {
	t.Helper()
	return testutil.ToFloat64(vec.With(prometheus.Labels{"type": label}))
}

func TestProcessJob_RedeliveredSideEffectingJobIsAckedWithoutRunning(t *testing.T) {
	expired := time.Now().Add(-time.Minute)
	for _, tc := range []struct {
		name     string
		deadline *time.Time
	}{
		{"live job", nil},
		{"expired job publishes no expired marker", &expired},
	} {
		t.Run(tc.name, func(t *testing.T) {
			checker := &fakeCallChecker{}
			pub := &flakyPublisher{}
			w := newProcessTestWorker(t, pub, checker)
			msg, _ := newJobMessage(t, "invite", 2, tc.deadline)

			if err := w.processJob(context.Background(), msg); err != nil {
				t.Fatalf("processJob = %v, want nil (ack)", err)
			}
			if n := checker.runs.Load(); n != 0 {
				t.Fatalf("Check ran %d times on a redelivery, want 0", n)
			}
			if pub.attempts != 0 {
				t.Fatalf("publish attempts = %d, want 0", pub.attempts)
			}
			if got := counterValue(t, w.sideEffectSkipped, fakeCallType); got != 1 {
				t.Fatalf("side_effect_redelivery_skipped_total = %v, want 1", got)
			}
		})
	}
}

func TestProcessJob_RedeliveryStillRunsOrdinaryChecks(t *testing.T) {
	checker := &fakeCallChecker{}
	pub := &flakyPublisher{}
	w := newProcessTestWorker(t, pub, checker)
	// SideEffecting(config) is false for this config: at-least-once applies.
	msg, _ := newJobMessage(t, "options", 2, nil)

	if err := w.processJob(context.Background(), msg); err != nil {
		t.Fatalf("processJob = %v", err)
	}
	if n := checker.runs.Load(); n != 1 {
		t.Fatalf("Check ran %d times, want 1", n)
	}
	if len(pub.published) != 1 {
		t.Fatalf("published = %d, want 1", len(pub.published))
	}
	if got := counterValue(t, w.sideEffectSkipped, fakeCallType); got != 0 {
		t.Fatalf("side_effect_redelivery_skipped_total = %v, want 0", got)
	}
}

func TestProcessJob_FirstDeliveryOfSideEffectingJobRuns(t *testing.T) {
	checker := &fakeCallChecker{}
	pub := &flakyPublisher{}
	w := newProcessTestWorker(t, pub, checker)
	msg, _ := newJobMessage(t, "invite", 1, nil)

	if err := w.processJob(context.Background(), msg); err != nil {
		t.Fatalf("processJob = %v", err)
	}
	if n := checker.runs.Load(); n != 1 {
		t.Fatalf("Check ran %d times, want 1", n)
	}
	if len(pub.published) != 1 {
		t.Fatalf("published = %d, want 1", len(pub.published))
	}
}

func TestProcessJob_SideEffectingPublishFailureRetriesInPlace(t *testing.T) {
	checker := &fakeCallChecker{}
	pub := &flakyPublisher{failures: 2}
	w := newProcessTestWorker(t, pub, checker)
	msg, _ := newJobMessage(t, "invite", 1, nil)

	if err := w.processJob(context.Background(), msg); err != nil {
		t.Fatalf("processJob = %v, want nil (never NAK a side-effecting check)", err)
	}
	if n := checker.runs.Load(); n != 1 {
		t.Fatalf("Check ran %d times, want 1", n)
	}
	if pub.attempts != 3 || len(pub.published) != 1 {
		t.Fatalf("attempts/published = %d/%d, want 3/1", pub.attempts, len(pub.published))
	}
	if got := counterValue(t, w.sideEffectAbandoned, fakeCallType); got != 0 {
		t.Fatalf("side_effect_result_abandoned_total = %v, want 0", got)
	}
}

func TestProcessJob_SideEffectingPublishGivesUpWithoutNak(t *testing.T) {
	checker := &fakeCallChecker{}
	pub := &flakyPublisher{failures: 100}
	w := newProcessTestWorker(t, pub, checker)
	msg, _ := newJobMessage(t, "invite", 1, nil)

	if err := w.processJob(context.Background(), msg); err != nil {
		t.Fatalf("processJob = %v, want nil (ack, never NAK)", err)
	}
	if want := len(w.publishRetryBackoff) + 1; pub.attempts != want {
		t.Fatalf("publish attempts = %d, want %d", pub.attempts, want)
	}
	if got := counterValue(t, w.sideEffectAbandoned, fakeCallType); got != 1 {
		t.Fatalf("side_effect_result_abandoned_total = %v, want 1", got)
	}
}

func TestProcessJob_SideEffectingPublishSurvivesShutdown(t *testing.T) {
	checker := &fakeCallChecker{}
	pub := &flakyPublisher{failures: 1}
	w := newProcessTestWorker(t, pub, checker)
	msg, _ := newJobMessage(t, "invite", 1, nil)

	// The call already happened; worker shutdown must not cost its result.
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := w.processJob(ctx, msg); err != nil {
		t.Fatalf("processJob = %v", err)
	}
	if len(pub.published) != 1 {
		t.Fatalf("published = %d, want 1", len(pub.published))
	}
}

// interruptedCallChecker places a "call" that the worker's shutdown cuts
// short: it blocks until ctx is cancelled and returns an Interrupted result.
type interruptedCallChecker struct{ fakeCallChecker }

func (c *interruptedCallChecker) Check(ctx context.Context, _ json.RawMessage, _ int) CheckResult {
	c.runs.Add(1)
	<-ctx.Done()
	msg := "cancelled: check cancelled"
	return CheckResult{Status: "error", ErrorMessage: &msg, Interrupted: true}
}

// A side-effecting check interrupted by worker shutdown is evidence about
// the worker, not the target: acked, nothing published, counted.
func TestProcessJob_InterruptedSideEffectingCheckIsNotPublished(t *testing.T) {
	checker := &interruptedCallChecker{}
	pub := &flakyPublisher{}
	w := newProcessTestWorker(t, pub, checker)
	msg, _ := newJobMessage(t, "invite", 1, nil)

	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		for checker.runs.Load() == 0 {
			time.Sleep(time.Millisecond)
		}
		cancel() // the worker shuts down mid-call
	}()
	if err := w.processJob(ctx, msg); err != nil {
		t.Fatalf("processJob = %v, want nil (ack)", err)
	}
	if pub.attempts != 0 {
		t.Fatalf("publish attempts = %d, want none", pub.attempts)
	}
	if got := counterValue(t, w.sideEffectInterrupted, fakeCallType); got != 1 {
		t.Fatalf("side_effect_result_interrupted_total = %v, want 1", got)
	}
}

func TestProcessJob_OrdinaryPublishFailureStillNaks(t *testing.T) {
	checker := &fakeCallChecker{}
	pub := &flakyPublisher{failures: 1}
	w := newProcessTestWorker(t, pub, checker)
	msg, _ := newJobMessage(t, "options", 1, nil)

	if err := w.processJob(context.Background(), msg); err == nil {
		t.Fatal("processJob = nil, want the publish error (NAK for redelivery)")
	}
	if pub.attempts != 1 {
		t.Fatalf("publish attempts = %d, want 1 (no in-place retry)", pub.attempts)
	}
}

func TestProcessJob_LongCheckHeartbeatsInProgress(t *testing.T) {
	checker := &fakeCallChecker{delay: 250 * time.Millisecond}
	pub := &flakyPublisher{}
	w := newProcessTestWorker(t, pub, checker)
	w.progressInterval = 20 * time.Millisecond
	msg, fake := newJobMessage(t, "options", 1, nil)

	if err := w.processJob(context.Background(), msg); err != nil {
		t.Fatalf("processJob = %v", err)
	}
	beats := fake.inProgress.Load()
	if beats < 3 {
		t.Fatalf("InProgress calls = %d during a 250ms check at 20ms, want >= 3", beats)
	}
	// The heartbeat is stopped before processJob returns (and Consume acks).
	time.Sleep(100 * time.Millisecond)
	if after := fake.inProgress.Load(); after != beats {
		t.Fatalf("InProgress called %d more times after processJob returned", after-beats)
	}
}

func TestProgressHeartbeatStopsAtCeiling(t *testing.T) {
	w := newProcessTestWorker(t, &flakyPublisher{}, &fakeCallChecker{})
	w.progressInterval = 10 * time.Millisecond
	var beats atomic.Int32
	msg := &queue.Message{InProgress: func() error {
		beats.Add(1)
		return nil
	}}

	stop := w.startProgressHeartbeat(msg, 55*time.Millisecond, w.logger.WithFields(nil))
	defer stop()
	time.Sleep(200 * time.Millisecond)
	if n := beats.Load(); n < 2 || n > 6 {
		t.Fatalf("InProgress calls = %d, want a handful before the 55ms ceiling", n)
	}
}

func TestProgressHeartbeatToleratesMissingInProgress(t *testing.T) {
	w := newProcessTestWorker(t, &flakyPublisher{}, &fakeCallChecker{})
	stop := w.startProgressHeartbeat(&queue.Message{}, time.Second, w.logger.WithFields(nil))
	stop()
}

// gatedCallChecker holds every "call" (an invite config) open until release
// is closed; other configs return at once.
type gatedCallChecker struct {
	fakeCallChecker
	release chan struct{}
}

func (c *gatedCallChecker) Check(ctx context.Context, config json.RawMessage, _ int) CheckResult {
	c.runs.Add(1)
	if c.SideEffecting(config) {
		select {
		case <-c.release:
		case <-ctx.Done():
		}
	}
	return CheckResult{Status: "success"}
}

func testCheckRequest(t *testing.T, method string) []byte {
	t.Helper()
	data, err := json.Marshal(models.CheckJobPayload{
		Type: fakeCallType, Config: json.RawMessage(`{"method":"` + method + `"}`), TimeoutSeconds: 5,
	})
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func decodeTestCheckResponse(t *testing.T, data []byte) models.TestCheckResponse {
	t.Helper()
	var resp models.TestCheckResponse
	if err := json.Unmarshal(data, &resp); err != nil {
		t.Fatalf("decode test-check response %s: %v", data, err)
	}
	return resp
}

// Test checks arrive one goroutine per request, so the worker bounds the
// side-effecting ones itself: with every slot taken the next test call is
// refused without running, ordinary tests are not bounded, and a finished
// test frees its slot.
func TestHandleTestCheck_BoundsConcurrentSideEffectingTests(t *testing.T) {
	checker := &gatedCallChecker{release: make(chan struct{})}
	w := newProcessTestWorker(t, &flakyPublisher{}, checker)

	var wg sync.WaitGroup
	responses := make([][]byte, maxSideEffectTestChecks)
	for i := range maxSideEffectTestChecks {
		wg.Add(1)
		go func() {
			defer wg.Done()
			responses[i] = w.handleTestCheck(testCheckRequest(t, "invite"))
		}()
	}
	deadline := time.Now().Add(5 * time.Second)
	for checker.runs.Load() < maxSideEffectTestChecks {
		if time.Now().After(deadline) {
			t.Fatalf("only %d of %d test calls started", checker.runs.Load(), maxSideEffectTestChecks)
		}
		time.Sleep(time.Millisecond)
	}

	resp := decodeTestCheckResponse(t, w.handleTestCheck(testCheckRequest(t, "invite")))
	if resp.Status != "error" || resp.ErrorMessage == nil || *resp.ErrorMessage != errSideEffectTestChecksFull {
		t.Fatalf("test call beyond the bound = %+v, want error %q", resp, errSideEffectTestChecksFull)
	}
	if n := checker.runs.Load(); n != maxSideEffectTestChecks {
		t.Fatalf("Check ran %d times, want %d (the refused test must not run)", n, maxSideEffectTestChecks)
	}
	if got := counterValue(t, w.sideEffectTestRejected, fakeCallType); got != 1 {
		t.Fatalf("test_check_side_effect_rejected_total = %v, want 1", got)
	}
	if resp := decodeTestCheckResponse(t, w.handleTestCheck(testCheckRequest(t, "options"))); resp.Status != "success" {
		t.Fatalf("ordinary test while the bound is full = %+v, want success", resp)
	}
	// Scheduled checks never share the bound: a scheduled call still runs.
	scheduled := make(chan error, 1)
	go func() {
		msg, _ := newJobMessage(t, "invite", 1, nil)
		scheduled <- w.processJob(context.Background(), msg)
	}()
	for checker.runs.Load() < maxSideEffectTestChecks+2 {
		if time.Now().After(deadline) {
			t.Fatal("a scheduled call did not start while the test-check bound was full")
		}
		time.Sleep(time.Millisecond)
	}

	close(checker.release)
	if err := <-scheduled; err != nil {
		t.Fatalf("scheduled call: processJob = %v", err)
	}
	wg.Wait()
	for i, data := range responses {
		if resp := decodeTestCheckResponse(t, data); resp.Status != "success" {
			t.Fatalf("held test call %d = %+v, want success", i, resp)
		}
	}
	if resp := decodeTestCheckResponse(t, w.handleTestCheck(testCheckRequest(t, "invite"))); resp.Status != "success" {
		t.Fatalf("test call after the slots freed = %+v, want success", resp)
	}
}

// The real SIP checker marks INVITE configs side-effecting: a redelivered
// test-call job is acked without placing the call (no packet reaches the
// callee) and without publishing; an OPTIONS job is still re-run.
func TestProcessJob_RedeliveredSIPCallIsNotPlacedAgain(t *testing.T) {
	var packets atomic.Int32
	host, port := startFakeUDPSIPServer(t, func(string) []string {
		packets.Add(1)
		return nil
	})
	pub := &flakyPublisher{}
	w := NewWorker(&config.WorkerConfig{
		CheckResultStream:  models.CheckResultStream,
		CheckResultSubject: models.CheckResultSubject,
		SIPCallEnabled:     true,
	}, logger.New("worker-test", "error"), metrics.NewRegistry("worker"), nil)
	w.results = pub

	payload, err := json.Marshal(models.CheckJobPayload{
		MonitorID: uuid.New().String(), Type: "sip", TimeoutSeconds: 5,
		Config: json.RawMessage(fmt.Sprintf(`{"host":%q,"port":%d,"method":"invite","call":{"number":"100"}}`, host, port)),
	})
	if err != nil {
		t.Fatal(err)
	}
	data, err := json.Marshal(models.Job{ID: uuid.New().String(), TenantID: uuid.New().String(), Type: models.JobTypeCheck, Version: "v1", Payload: payload})
	if err != nil {
		t.Fatal(err)
	}
	if err := w.processJob(context.Background(), &queue.Message{Data: data, NumDelivered: 2}); err != nil {
		t.Fatalf("processJob = %v, want nil (ack)", err)
	}
	time.Sleep(100 * time.Millisecond)
	if n := packets.Load(); n != 0 || pub.attempts != 0 {
		t.Fatalf("redelivered call sent %d packets and %d publishes, want none", n, pub.attempts)
	}
	if got := counterValue(t, w.sideEffectSkipped, "sip"); got != 1 {
		t.Fatalf("side_effect_redelivery_skipped_total{sip} = %v, want 1", got)
	}

	sip, _ := w.checkerRegistry.Get("sip")
	if isSideEffecting(sip, json.RawMessage(`{"host":"x","method":"options"}`)) {
		t.Fatal("an OPTIONS probe must stay at-least-once")
	}
	if !isSideEffecting(sip, json.RawMessage(`{"host":"x","method":"invite","password":"{\"alg\":\"aes-256-gcm\",\"ct\":\"…\"}","call":{"number":"1"}}`)) {
		t.Fatal("an INVITE with an encrypted password must be side-effecting")
	}
}

// NewWorker applies SIP_CALL_ENABLED / SIP_CALL_ALLOWED_DESTINATIONS to the
// SIP checker.
func TestNewWorker_ConfiguresSIPCalls(t *testing.T) {
	for _, enabled := range []bool{true, false} {
		w := NewWorker(&config.WorkerConfig{SIPCallEnabled: enabled}, logger.New("worker-test", "error"), metrics.NewRegistry("worker"), nil)
		checker, err := w.checkerRegistry.Get("sip")
		if err != nil {
			t.Fatal(err)
		}
		if got := checker.(*SIPChecker).callsEnabled; got != enabled {
			t.Fatalf("SIPCallEnabled=%v: checker calls enabled = %v", enabled, got)
		}
	}
}
