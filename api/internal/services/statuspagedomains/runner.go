package statuspagedomains

import (
	"context"
	"fmt"
	"sync"
	"time"

	"golang.org/x/sync/errgroup"

	"github.com/yassinebenameur/probara/shared/db/pgerr"
)

const (
	// runInterval is how often the runner looks for due domains.
	runInterval = time.Minute
	// tickTimeout bounds one tick; a domain claimed but not checked in it is
	// due again once its lease lapses.
	tickTimeout = 5 * time.Minute
	// claimLease moves a claimed domain's next_check_at this far ahead, so
	// other replicas pass it over while this one checks it; a replica that
	// dies mid-check leaves it due again once the lease lapses.
	claimLease = 2 * time.Minute
	// claimBatch domains are claimed at a time and checked checkConcurrency
	// at once; maxChecksPerTick bounds one tick (the rest wait for the next
	// tick or another replica).
	claimBatch       = 50
	checkConcurrency = 8
	maxChecksPerTick = 500
)

// RunOnce checks every due domain, a batch at a time. It stops early,
// without an error, once ctx is done.
func (s *Service) RunOnce(ctx context.Context) error {
	if !s.Enabled() {
		return nil
	}
	for checked := 0; checked < maxChecksPerTick && ctx.Err() == nil; {
		batch, err := s.claimDue(ctx, claimBatch)
		if err != nil {
			if ctx.Err() != nil {
				return nil
			}
			return err
		}
		if len(batch) == 0 {
			return nil
		}
		group, gctx := errgroup.WithContext(ctx)
		group.SetLimit(checkConcurrency)
		var mu sync.Mutex
		var firstErr error
		for _, c := range batch {
			group.Go(func() error {
				res := checkDomain(gctx, s.cfg.Resolver, c.Hostname, c.Token, s.cfg.CNAMETarget)
				if gctx.Err() != nil {
					// Stopped mid-check: the lease brings it back.
					return nil
				}
				if err := s.record(gctx, c, res); err != nil {
					mu.Lock()
					if firstErr == nil {
						firstErr = err
					}
					mu.Unlock()
				}
				// One domain's failure never stops the others.
				return nil
			})
		}
		_ = group.Wait()
		if firstErr != nil && ctx.Err() == nil {
			return firstErr
		}
		checked += len(batch)
	}
	return nil
}

// claimDue takes up to limit due domains for this replica: FOR UPDATE SKIP
// LOCKED partitions them between replicas, and the lease keeps them out of
// every other replica's next claim while the DNS lookups run (no lock is
// held across them).
func (s *Service) claimDue(ctx context.Context, limit int) ([]claim, error) {
	now := s.now()
	rows, err := s.db.QueryContext(ctx, `
		UPDATE status_page_domains d
		SET next_check_at = $2
		FROM (
			SELECT id FROM status_page_domains
			WHERE next_check_at <= $1
			ORDER BY next_check_at
			LIMIT $3
			FOR UPDATE SKIP LOCKED
		) due, status_pages sp
		WHERE d.id = due.id AND sp.id = d.status_page_id
		RETURNING d.id, d.hostname, d.verification_token, d.created_at, sp.tenant_id, d.status_page_id`,
		now, now.Add(claimLease), limit)
	if err != nil {
		return nil, fmt.Errorf("claim due custom domains: %w", err)
	}
	defer rows.Close()
	var out []claim
	for rows.Next() {
		var c claim
		if err := rows.Scan(&c.ID, &c.Hostname, &c.Token, &c.CreatedAt, &c.TenantID, &c.PageID); err != nil {
			return nil, fmt.Errorf("scan claimed custom domain: %w", err)
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// Runner re-verifies custom domains in the background. Every API replica
// runs one; claims keep them apart.
type Runner struct {
	svc      *Service
	interval time.Duration
	stop     chan struct{}
	wg       sync.WaitGroup
	once     sync.Once
}

// NewRunner wraps the service in a background loop.
func NewRunner(svc *Service) *Runner {
	return &Runner{svc: svc, interval: runInterval, stop: make(chan struct{})}
}

// Start begins the loop. Without STATUS_PAGE_CNAME_TARGET it does nothing:
// no domain can be verified then, and verified ones keep their status.
func (r *Runner) Start() {
	if !r.svc.Enabled() {
		if r.svc.cfg.Log != nil {
			r.svc.cfg.Log.Info("Status page custom domains are off: STATUS_PAGE_CNAME_TARGET is not set")
		}
		return
	}
	r.wg.Add(1)
	go r.run()
}

// Stop ends the loop and waits for a tick in progress.
func (r *Runner) Stop() {
	r.once.Do(func() { close(r.stop) })
	r.wg.Wait()
}

func (r *Runner) run() {
	defer r.wg.Done()
	ticker := time.NewTicker(r.interval)
	defer ticker.Stop()
	for {
		r.tick()
		select {
		case <-r.stop:
			return
		case <-ticker.C:
		}
	}
}

func (r *Runner) tick() {
	ctx, cancel := context.WithTimeout(context.Background(), tickTimeout)
	defer cancel()
	go func() {
		select {
		case <-r.stop:
			cancel()
		case <-ctx.Done():
		}
	}()
	if err := r.svc.RunOnce(ctx); err != nil && !pgerr.IsMissingSchema(err) && r.svc.cfg.Log != nil {
		r.svc.cfg.Log.WithError(err).Error("Failed to verify status page custom domains")
	}
}
