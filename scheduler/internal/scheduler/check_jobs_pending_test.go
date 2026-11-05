package scheduler

import (
	"context"
	"testing"

	"github.com/nats-io/nats.go/jetstream"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"

	"github.com/yassinebenameur/probara/shared/logger"
	"github.com/yassinebenameur/probara/shared/metrics"
)

type fakeStream struct{ msgs uint64 }

func (f fakeStream) Info(context.Context, ...jetstream.StreamInfoOpt) (*jetstream.StreamInfo, error) {
	return &jetstream.StreamInfo{State: jetstream.StreamState{Msgs: f.msgs}}, nil
}

func TestUpdateCheckJobsPending(t *testing.T) {
	s := &Scheduler{
		logger:           logger.New("test", "error"),
		checkJobsPending: newCheckJobsPendingGauge(metrics.NewRegistry("scheduler")),
	}
	s.updateCheckJobsPending(context.Background()) // no stream yet: no-op
	s.checkJobStream = fakeStream{msgs: 42}
	s.updateCheckJobsPending(context.Background())
	if got := testutil.ToFloat64(s.checkJobsPending.With(prometheus.Labels{})); got != 42 {
		t.Fatalf("check_jobs_pending = %v, want 42", got)
	}
}
