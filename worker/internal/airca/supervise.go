package airca

import (
	"context"
	"time"

	"github.com/sirupsen/logrus"

	"github.com/yassinebenameur/probara/shared/logger"
)

// RestartPolicy bounds how a supervised loop is restarted after it exits.
// The zero value is completed by withDefaults.
type RestartPolicy struct {
	// InitialBackoff is the wait after the first failure; it doubles per
	// consecutive failure up to MaxBackoff.
	InitialBackoff time.Duration
	MaxBackoff     time.Duration
	// HealthyAfter is how long a run must last to count as healthy; the next
	// failure after a healthy run starts again from InitialBackoff.
	HealthyAfter time.Duration

	// Test seams. Sleep must return early (with ctx.Err()) on cancellation.
	Now   func() time.Time
	Sleep func(ctx context.Context, d time.Duration) error
}

// DefaultRestartPolicy restarts after 1s, doubling to a 60s ceiling, and
// forgets past failures once a run survived a minute.
func DefaultRestartPolicy() RestartPolicy {
	return RestartPolicy{}.withDefaults()
}

func (p RestartPolicy) withDefaults() RestartPolicy {
	if p.InitialBackoff <= 0 {
		p.InitialBackoff = time.Second
	}
	if p.MaxBackoff <= 0 {
		p.MaxBackoff = 60 * time.Second
	}
	if p.MaxBackoff < p.InitialBackoff {
		p.MaxBackoff = p.InitialBackoff
	}
	if p.HealthyAfter <= 0 {
		p.HealthyAfter = time.Minute
	}
	if p.Now == nil {
		p.Now = time.Now
	}
	if p.Sleep == nil {
		p.Sleep = sleepCtx
	}
	return p
}

// RunWithRestart runs fn until ctx is cancelled, restarting it with capped
// exponential backoff whenever it returns (with or without an error) while ctx
// is still live. A consumer whose Consume call fails — NATS hiccup, consumer
// deleted, metadata error — would otherwise stay dead until the pod restarts,
// and /healthz cannot see it because the NATS connection itself is fine.
func RunWithRestart(ctx context.Context, log *logger.Logger, name string, policy RestartPolicy, fn func(context.Context) error) {
	p := policy.withDefaults()
	backoff := p.InitialBackoff
	for {
		started := p.Now()
		err := fn(ctx)
		if ctx.Err() != nil {
			return
		}
		if p.Now().Sub(started) >= p.HealthyAfter {
			backoff = p.InitialBackoff
		}
		entry := log.WithFields(logrus.Fields{"component": name, "restart_in": backoff.String()})
		if err != nil {
			entry.WithError(err).Error("Supervised loop exited; restarting")
		} else {
			entry.Warn("Supervised loop returned without error; restarting")
		}
		if p.Sleep(ctx, backoff) != nil {
			return
		}
		backoff *= 2
		if backoff > p.MaxBackoff {
			backoff = p.MaxBackoff
		}
	}
}

func sleepCtx(ctx context.Context, d time.Duration) error {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}
