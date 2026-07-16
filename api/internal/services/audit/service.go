package audit

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/lib/pq"

	ctxpkg "github.com/yassinebenameur/probara/shared/context"
	"github.com/yassinebenameur/probara/shared/db"
	"github.com/yassinebenameur/probara/shared/logger"
)

// Outcomes of an audited action.
const (
	OutcomeSuccess = "success"
	OutcomeFailure = "failure"
	OutcomeDenied  = "denied"
)

// Actor types (mirror shared/context actor types plus anonymous).
const (
	ActorAdminUser = "admin_user"
	ActorAPIKey    = "api_key"
	ActorAnonymous = "anonymous"
)

// Event is one audit record.
type Event struct {
	OccurredAt   time.Time
	TenantID     *uuid.UUID
	ActorType    string
	ActorID      *uuid.UUID
	ActorLabel   string
	Action       string
	ResourceType string
	ResourceID   string
	Outcome      string
	StatusCode   int
	IP           string
	UserAgent    string
	Details      map[string]any
}

// Entry is a persisted audit record as returned by List.
type Entry struct {
	ID           int64           `json:"id"`
	OccurredAt   time.Time       `json:"occurred_at"`
	TenantID     *uuid.UUID      `json:"tenant_id,omitempty"`
	ActorType    string          `json:"actor_type"`
	ActorID      *uuid.UUID      `json:"actor_id,omitempty"`
	ActorLabel   string          `json:"actor_label"`
	Action       string          `json:"action"`
	ResourceType string          `json:"resource_type"`
	ResourceID   string          `json:"resource_id"`
	Outcome      string          `json:"outcome"`
	StatusCode   *int            `json:"status_code,omitempty"`
	IP           string          `json:"ip"`
	UserAgent    string          `json:"user_agent"`
	Details      json.RawMessage `json:"details,omitempty"`
}

// ListResponse is the paginated audit list payload.
type ListResponse struct {
	Items    []Entry `json:"items"`
	Page     int     `json:"page"`
	PageSize int     `json:"page_size"`
	Total    int     `json:"total"`
}

// Filters narrows List results.
type Filters struct {
	ActorID  *uuid.UUID
	Action   string
	Outcome  string
	From     *time.Time
	To       *time.Time
	Page     int
	PageSize int
}

const (
	bufferSize    = 1024
	flushInterval = 2 * time.Second
	maxBatch      = 100
)

// Recorder persists audit events asynchronously: Record never blocks the
// request path and a failed insert never fails a request.
type Recorder struct {
	db      *db.Client
	log     *logger.Logger
	events  chan Event
	stop    chan struct{}
	wg      sync.WaitGroup
	dropped int64
	mu      sync.Mutex
}

// NewRecorder creates an audit recorder (call Start to begin persisting).
func NewRecorder(dbClient *db.Client, log *logger.Logger) *Recorder {
	return &Recorder{
		db:     dbClient,
		log:    log,
		events: make(chan Event, bufferSize),
		stop:   make(chan struct{}),
	}
}

// Start launches the writer goroutine.
func (r *Recorder) Start() {
	r.wg.Add(1)
	go r.run()
	r.log.Info("Audit recorder started")
}

// Stop flushes pending events and stops the writer.
func (r *Recorder) Stop() {
	close(r.stop)
	r.wg.Wait()
	r.log.Info("Audit recorder stopped")
}

// Record enqueues an event without blocking; drops (and counts) when full.
func (r *Recorder) Record(event Event) {
	if event.OccurredAt.IsZero() {
		event.OccurredAt = time.Now()
	}
	select {
	case r.events <- event:
	default:
		r.mu.Lock()
		r.dropped++
		dropped := r.dropped
		r.mu.Unlock()
		if dropped%100 == 1 {
			r.log.WithFields(map[string]interface{}{"dropped_total": dropped}).Warn("Audit buffer full; dropping events")
		}
	}
}

// FromRequest builds an Event pre-filled with actor/tenant/ip/UA from the
// request context. Callers set Action/Resource/Outcome.
func FromRequest(r *http.Request) Event {
	ctx := r.Context()
	event := Event{
		IP:        ClientIP(r),
		UserAgent: r.UserAgent(),
		ActorType: ActorAnonymous,
	}

	if tenantID, ok := ctxpkg.GetTenantID(ctx); ok {
		if parsed, err := uuid.Parse(tenantID); err == nil {
			event.TenantID = &parsed
		}
	}
	if adminID, ok := ctxpkg.GetAdminID(ctx); ok {
		if parsed, err := uuid.Parse(adminID); err == nil {
			event.ActorType = ActorAdminUser
			event.ActorID = &parsed
		}
	} else if keyID, ok := ctxpkg.GetAPIKeyID(ctx); ok {
		if parsed, err := uuid.Parse(keyID); err == nil {
			event.ActorType = ActorAPIKey
			event.ActorID = &parsed
		}
	} else if actorType, ok := ctxpkg.GetActorType(ctx); ok && actorType == ctxpkg.ActorTypeAPIKey {
		// API key requests before scoped keys land carry no key ID.
		event.ActorType = ActorAPIKey
	}

	return event
}

// ClientIP extracts the originating client IP, honoring X-Forwarded-For.
func ClientIP(r *http.Request) string {
	if forwarded := r.Header.Get("X-Forwarded-For"); forwarded != "" {
		parts := strings.Split(forwarded, ",")
		return strings.TrimSpace(parts[0])
	}
	return r.RemoteAddr
}

func (r *Recorder) run() {
	defer r.wg.Done()

	ticker := time.NewTicker(flushInterval)
	defer ticker.Stop()

	batch := make([]Event, 0, maxBatch)
	flush := func() {
		if len(batch) == 0 {
			return
		}
		r.insertBatch(batch)
		batch = batch[:0]
	}

	for {
		select {
		case <-r.stop:
			// Drain whatever is buffered, then flush and exit.
			for {
				select {
				case event := <-r.events:
					batch = append(batch, event)
					if len(batch) >= maxBatch {
						flush()
					}
				default:
					flush()
					return
				}
			}
		case event := <-r.events:
			batch = append(batch, event)
			if len(batch) >= maxBatch {
				flush()
			}
		case <-ticker.C:
			flush()
		}
	}
}

func (r *Recorder) insertBatch(batch []Event) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	// Multi-row VALUES insert; 13 params per row keeps well under limits.
	var sb strings.Builder
	sb.WriteString(`INSERT INTO audit_log
		(occurred_at, tenant_id, actor_type, actor_id, actor_label, action, resource_type, resource_id, outcome, status_code, ip, user_agent, details)
		VALUES `)
	args := make([]any, 0, len(batch)*13)
	for i, e := range batch {
		if i > 0 {
			sb.WriteString(",")
		}
		base := i * 13
		sb.WriteString(fmt.Sprintf("($%d,$%d,$%d,$%d,$%d,$%d,$%d,$%d,$%d,$%d,$%d,$%d,$%d)",
			base+1, base+2, base+3, base+4, base+5, base+6, base+7, base+8, base+9, base+10, base+11, base+12, base+13))

		var details any
		if e.Details != nil {
			if encoded, err := json.Marshal(e.Details); err == nil {
				details = encoded
			}
		}
		var statusCode any
		if e.StatusCode != 0 {
			statusCode = e.StatusCode
		}
		args = append(args, e.OccurredAt, e.TenantID, e.ActorType, e.ActorID, e.ActorLabel,
			e.Action, e.ResourceType, e.ResourceID, e.Outcome, statusCode, e.IP, e.UserAgent, details)
	}

	if _, err := r.db.ExecContext(ctx, sb.String(), args...); err != nil {
		if isMissingSchema(err) {
			// Pre-migration window (post-upgrade migrations job): drop quietly.
			return
		}
		r.log.WithError(err).Error("Failed to persist audit events")
	}
}

// Service queries and prunes the audit log.
type Service struct {
	db *db.Client
}

// NewService creates a new audit query service.
func NewService(dbClient *db.Client) *Service {
	return &Service{db: dbClient}
}

// List returns audit entries for a tenant. When includePlatform is true
// (superadmin), platform-level events (tenant_id IS NULL) are included.
func (s *Service) List(ctx context.Context, tenantID uuid.UUID, includePlatform bool, f Filters) (*ListResponse, error) {
	if f.Page < 1 {
		f.Page = 1
	}
	if f.PageSize < 1 {
		f.PageSize = 50
	}
	if f.PageSize > 200 {
		f.PageSize = 200
	}

	where := make([]string, 0, 6)
	args := make([]any, 0, 8)
	arg := func(v any) string {
		args = append(args, v)
		return fmt.Sprintf("$%d", len(args))
	}

	if includePlatform {
		where = append(where, fmt.Sprintf("(tenant_id = %s OR tenant_id IS NULL)", arg(tenantID)))
	} else {
		where = append(where, fmt.Sprintf("tenant_id = %s", arg(tenantID)))
	}
	if f.ActorID != nil {
		where = append(where, fmt.Sprintf("actor_id = %s", arg(*f.ActorID)))
	}
	if f.Action != "" {
		where = append(where, fmt.Sprintf("action = %s", arg(f.Action)))
	}
	if f.Outcome != "" {
		where = append(where, fmt.Sprintf("outcome = %s", arg(f.Outcome)))
	}
	if f.From != nil {
		where = append(where, fmt.Sprintf("occurred_at >= %s", arg(*f.From)))
	}
	if f.To != nil {
		where = append(where, fmt.Sprintf("occurred_at <= %s", arg(*f.To)))
	}

	whereClause := "WHERE " + strings.Join(where, " AND ")

	var total int
	if err := s.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM audit_log "+whereClause, args...).Scan(&total); err != nil {
		if isMissingSchema(err) {
			return &ListResponse{Items: []Entry{}, Page: f.Page, PageSize: f.PageSize}, nil
		}
		return nil, fmt.Errorf("failed to count audit entries: %w", err)
	}

	query := fmt.Sprintf(`
		SELECT id, occurred_at, tenant_id, actor_type, actor_id, actor_label,
		       action, resource_type, resource_id, outcome, status_code, ip, user_agent, details
		FROM audit_log
		%s
		ORDER BY occurred_at DESC, id DESC
		LIMIT %s OFFSET %s
	`, whereClause, arg(f.PageSize), arg((f.Page-1)*f.PageSize))

	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("failed to list audit entries: %w", err)
	}
	defer rows.Close()

	items := make([]Entry, 0, f.PageSize)
	for rows.Next() {
		var e Entry
		var details []byte
		if err := rows.Scan(
			&e.ID, &e.OccurredAt, &e.TenantID, &e.ActorType, &e.ActorID, &e.ActorLabel,
			&e.Action, &e.ResourceType, &e.ResourceID, &e.Outcome, &e.StatusCode, &e.IP, &e.UserAgent, &details,
		); err != nil {
			return nil, fmt.Errorf("failed to scan audit entry: %w", err)
		}
		e.Details = details
		items = append(items, e)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("failed to iterate audit entries: %w", err)
	}

	return &ListResponse{Items: items, Page: f.Page, PageSize: f.PageSize, Total: total}, nil
}

// DistinctActions returns the action values present for a tenant (for the
// filter dropdown).
func (s *Service) DistinctActions(ctx context.Context, tenantID uuid.UUID, includePlatform bool) ([]string, error) {
	query := `SELECT DISTINCT action FROM audit_log WHERE tenant_id = $1 ORDER BY action ASC`
	if includePlatform {
		query = `SELECT DISTINCT action FROM audit_log WHERE tenant_id = $1 OR tenant_id IS NULL ORDER BY action ASC`
	}

	rows, err := s.db.QueryContext(ctx, query, tenantID)
	if err != nil {
		if isMissingSchema(err) {
			return []string{}, nil
		}
		return nil, fmt.Errorf("failed to list audit actions: %w", err)
	}
	defer rows.Close()

	actions := make([]string, 0)
	for rows.Next() {
		var action string
		if err := rows.Scan(&action); err != nil {
			return nil, err
		}
		actions = append(actions, action)
	}
	return actions, rows.Err()
}

// Pruner deletes audit entries older than the retention window.
type Pruner struct {
	db            *db.Client
	log           *logger.Logger
	retentionDays int
	interval      time.Duration
	stop          chan struct{}
	wg            sync.WaitGroup
}

// NewPruner creates an audit retention pruner. retentionDays 0 disables pruning.
func NewPruner(dbClient *db.Client, log *logger.Logger, retentionDays int) *Pruner {
	return &Pruner{
		db:            dbClient,
		log:           log,
		retentionDays: retentionDays,
		interval:      time.Hour,
		stop:          make(chan struct{}),
	}
}

// Start begins the pruning loop (no-op when retention is disabled).
func (p *Pruner) Start() {
	if p.retentionDays <= 0 {
		p.log.Info("Audit pruner disabled (retention 0 = keep forever)")
		return
	}
	p.wg.Add(1)
	go p.run()
	p.log.WithFields(map[string]interface{}{"retention_days": p.retentionDays}).Info("Audit pruner started")
}

// Stop gracefully stops the pruner.
func (p *Pruner) Stop() {
	if p.retentionDays <= 0 {
		return
	}
	close(p.stop)
	p.wg.Wait()
}

func (p *Pruner) run() {
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

func (p *Pruner) pruneOnce() {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	// Batched deletes keep lock times short on large backlogs.
	for {
		result, err := p.db.ExecContext(ctx, `
			DELETE FROM audit_log
			WHERE id IN (
				SELECT id FROM audit_log
				WHERE occurred_at < NOW() - make_interval(days => $1)
				LIMIT 5000
			)
		`, p.retentionDays)
		if err != nil {
			if !isMissingSchema(err) {
				p.log.WithError(err).Error("Failed to prune audit log")
			}
			return
		}
		affected, _ := result.RowsAffected()
		if affected > 0 {
			p.log.WithFields(map[string]interface{}{"deleted": affected}).Info("Pruned audit log entries")
		}
		if affected < 5000 {
			return
		}
	}
}

func isMissingSchema(err error) bool {
	var pqErr *pq.Error
	if !errors.As(err, &pqErr) {
		return false
	}
	return pqErr.Code == "42P01" || pqErr.Code == "42703"
}
