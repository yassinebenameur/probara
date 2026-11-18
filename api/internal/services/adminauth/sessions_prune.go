package adminauth

import (
	"context"
	"sync"
	"time"

	"github.com/yassinebenameur/probara/shared/db"
	"github.com/yassinebenameur/probara/shared/db/pgerr"
	"github.com/yassinebenameur/probara/shared/logger"
)

// sessionPruneMargin is added to the refresh-token lifetime to make the
// retention window.
const sessionPruneMargin = 24 * time.Hour

// sessionPruneBatch bounds one DELETE, so a backlog never holds locks long.
const sessionPruneBatch = 1000

// SessionPruner deletes dead session rows hourly, in the API process like
// the audit pruner. The retention window is the refresh-token lifetime plus a
// day (SessionRetention):
//
//   - a refresh token (admin_sessions row) once its own expiry is that far
//     past. Reuse detection needs rotated tokens only until they expire --
//     an expired token is refused before the reuse check -- so it keeps
//     working;
//   - a session (family) once it was revoked or expired that long ago, with
//     its remaining tokens (ON DELETE CASCADE).
//
// Every API replica runs it; the deletes are idempotent.
type SessionPruner struct {
	db        *db.Client
	log       *logger.Logger
	retention time.Duration
	interval  time.Duration
	stop      chan struct{}
	wg        sync.WaitGroup
}

// SessionRetention is how long dead session rows are kept: the refresh-token
// lifetime plus a day.
func SessionRetention(refreshTTL time.Duration) time.Duration {
	return refreshTTL + sessionPruneMargin
}

// NewSessionPruner creates the pruner for refresh tokens of lifetime refreshTTL.
func NewSessionPruner(dbClient *db.Client, log *logger.Logger, refreshTTL time.Duration) *SessionPruner {
	return &SessionPruner{
		db:        dbClient,
		log:       log,
		retention: SessionRetention(refreshTTL),
		interval:  time.Hour,
		stop:      make(chan struct{}),
	}
}

// Start begins the hourly loop.
func (p *SessionPruner) Start() {
	p.wg.Add(1)
	go p.run()
}

// Stop ends the loop and waits for it.
func (p *SessionPruner) Stop() {
	close(p.stop)
	p.wg.Wait()
}

func (p *SessionPruner) run() {
	defer p.wg.Done()
	ticker := time.NewTicker(p.interval)
	defer ticker.Stop()
	p.pruneOnce()
	for {
		select {
		case <-p.stop:
			return
		case <-ticker.C:
			p.pruneOnce()
		}
	}
}

func (p *SessionPruner) pruneOnce() {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	deleted, err := PruneSessions(ctx, p.db, p.retention)
	if err != nil {
		p.log.WithError(err).Error("Failed to prune admin sessions")
		return
	}
	if deleted > 0 {
		p.log.WithFields(map[string]interface{}{"deleted": deleted}).Info("Pruned admin session rows")
	}
}

// PruneSessions deletes, in bounded batches, refresh tokens expired longer
// than retention ago and sessions revoked or expired longer ago than that
// (see SessionPruner). It returns the number of rows deleted directly
// (cascaded tokens not counted). Before migration 000117 it prunes nothing.
func PruneSessions(ctx context.Context, q SessionQuerier, retention time.Duration) (int64, error) {
	var total int64
	for _, stmt := range []string{`
		DELETE FROM admin_session_families
		WHERE id IN (
			SELECT id FROM admin_session_families
			WHERE revoked_at < NOW() - make_interval(secs => $1)
			   OR expires_at < NOW() - make_interval(secs => $1)
			LIMIT $2
		)`, `
		DELETE FROM admin_sessions
		WHERE id IN (
			SELECT id FROM admin_sessions
			WHERE expires_at < NOW() - make_interval(secs => $1)
			LIMIT $2
		)`,
		// Account links and sign-in challenges (migration 000123, last: its
		// absence ends the run) once long expired, kept as long as sessions
		// for audit correlation.
		`
		DELETE FROM admin_account_tokens
		WHERE id IN (
			SELECT id FROM admin_account_tokens
			WHERE expires_at < NOW() - make_interval(secs => $1)
			LIMIT $2
		)`,
	} {
		for {
			res, err := q.ExecContext(ctx, stmt, retention.Seconds(), sessionPruneBatch)
			if pgerr.IsMissingSchema(err) {
				return total, nil
			}
			if err != nil {
				return total, err
			}
			n, _ := res.RowsAffected()
			total += n
			if n < sessionPruneBatch {
				break
			}
		}
	}
	return total, nil
}
