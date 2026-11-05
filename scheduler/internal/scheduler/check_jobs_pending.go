package scheduler

import (
	"context"
	"time"

	"github.com/nats-io/nats.go/jetstream"
	"github.com/prometheus/client_golang/prometheus"

	"github.com/yassinebenameur/probara/shared/metrics"
)

// checkJobStreamInfo is the slice of jetstream.Stream the backlog gauge
// reads.
type checkJobStreamInfo interface {
	Info(ctx context.Context, opts ...jetstream.StreamInfoOpt) (*jetstream.StreamInfo, error)
}

func newCheckJobsPendingGauge(r *metrics.Registry) *prometheus.GaugeVec {
	if r == nil {
		return nil
	}
	return r.NewGauge("check_jobs_pending",
		"Check jobs waiting in the check-job stream for a worker. The stream is a work queue, so a job leaves it once a worker acks it: a backlog that only grows means a location (or the default fleet) has no consuming worker",
		[]string{})
}

// updateCheckJobsPending samples the check-job stream's message count after
// each schedule tick. It is an operator signal, not a tenant alert: offline
// private locations alert per tenant (location_offline), while a default
// fleet with no worker at all shows up only here. Best effort.
func (s *Scheduler) updateCheckJobsPending(ctx context.Context) {
	if s.checkJobsPending == nil || s.checkJobStream == nil {
		return
	}
	ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	info, err := s.checkJobStream.Info(ctx)
	if err != nil {
		s.logger.WithError(err).Debug("Failed to read check-job stream info")
		return
	}
	s.checkJobsPending.With(prometheus.Labels{}).Set(float64(info.State.Msgs))
}
