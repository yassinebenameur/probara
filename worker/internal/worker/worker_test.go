package worker

import (
	"context"
	"encoding/json"
	"errors"
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
