package statuspage

import (
	"context"
	"database/sql"
	"encoding/json"
	"strings"
	"time"

	"github.com/google/uuid"
	shareddb "github.com/yassinebenameur/probara/shared/db"
	"github.com/yassinebenameur/probara/shared/logger"
	"github.com/yassinebenameur/probara/shared/statusupdates"
)

const statusPageSlugQuery = `
	WITH RECURSIVE monitor_targets AS (
		SELECT id
		FROM monitors
		WHERE tenant_id = $1 AND id = $2
		UNION
		SELECT mg.group_id
		FROM monitor_groups mg
		JOIN monitor_targets mt ON mt.id = mg.monitor_id
		JOIN monitors m ON m.id = mg.group_id
		WHERE m.tenant_id = $1 AND m.type = 'group'
	)
	SELECT DISTINCT sp.slug
	FROM status_pages sp
	JOIN status_page_monitors spm ON spm.status_page_id = sp.id
	JOIN monitor_targets mt ON mt.id = spm.monitor_id
	WHERE sp.tenant_id = $1
	UNION
	SELECT DISTINCT sp.slug
	FROM status_pages sp
	JOIN status_page_sections sps ON sps.status_page_id = sp.id
	JOIN status_page_section_monitors spsm ON spsm.section_id = sps.id
	JOIN monitor_targets mt ON mt.id = spsm.monitor_id
	WHERE sp.tenant_id = $1
	ORDER BY 1
`

const legacyStatusPageSlugQuery = `
	WITH RECURSIVE monitor_targets AS (
		SELECT id
		FROM monitors
		WHERE tenant_id = $1 AND id = $2
		UNION
		SELECT mg.group_id
		FROM monitor_groups mg
		JOIN monitor_targets mt ON mt.id = mg.monitor_id
		JOIN monitors m ON m.id = mg.group_id
		WHERE m.tenant_id = $1 AND m.type = 'group'
	)
	SELECT DISTINCT sp.slug
	FROM status_pages sp
	JOIN status_page_monitors spm ON spm.status_page_id = sp.id
	JOIN monitor_targets mt ON mt.id = spm.monitor_id
	WHERE sp.tenant_id = $1
	ORDER BY 1
`

const statusPageSlugByIDQuery = `
	SELECT slug
	FROM status_pages
	WHERE id = $1
`

const statusPageSlugByIDAndTenantQuery = `
	SELECT slug
	FROM status_pages
	WHERE id = $1 AND tenant_id = $2
`

const statusPageSlugResolveTimeout = 5 * time.Second

type slugResolver func(context.Context, statusupdates.Event) ([]string, error)

type statusPageSlugStore interface {
	QueryContext(ctx context.Context, query string, args ...interface{}) (*sql.Rows, error)
}

type statusPageSlugResolver struct {
	db statusPageSlugStore
}

// Subscriber listens for status update events and broadcasts to SSE clients.
type Subscriber struct {
	subject      *statusupdates.Subscriber
	hub          *Hub
	logger       *logger.Logger
	resolveSlugs slugResolver
}

// NewSubscriber creates a new status update subscriber.
func NewSubscriber(natsURL string, hub *Hub, dbClient shareddb.Querier, log *logger.Logger) (*Subscriber, error) {
	sub, err := statusupdates.NewSubscriber(natsURL)
	if err != nil {
		return nil, err
	}
	return &Subscriber{
		subject:      sub,
		hub:          hub,
		logger:       log,
		resolveSlugs: newStatusPageSlugResolver(dbClient),
	}, nil
}

// Start subscribes to NATS and begins broadcasting.
func (s *Subscriber) Start() error {
	if s.subject == nil || s.hub == nil {
		return nil
	}
	_, err := s.subject.Subscribe(s.handleEvent)
	if err != nil {
		return err
	}
	if s.logger != nil {
		s.logger.WithFields(map[string]interface{}{
			"subject": statusupdates.SubjectFromEnv(),
		}).Info("Status page update subscriber started")
	}
	return nil
}

// Close closes the subscription connection.
func (s *Subscriber) Close() {
	if s.subject != nil {
		s.subject.Close()
	}
}

func newStatusPageSlugResolver(dbClient shareddb.Querier) slugResolver {
	if dbClient == nil {
		return nil
	}
	resolver := statusPageSlugResolver{db: dbClient}
	return resolver.Resolve
}

func (r statusPageSlugResolver) Resolve(ctx context.Context, event statusupdates.Event) ([]string, error) {
	if r.db == nil {
		return nil, nil
	}

	statusPageID, err := parseEventUUID(event.StatusPageID)
	if err != nil {
		return nil, err
	}
	if statusPageID != uuid.Nil {
		tenantID, err := parseEventUUID(event.TenantID)
		if err != nil {
			return nil, err
		}
		if tenantID != uuid.Nil {
			return r.resolveSlugs(ctx, statusPageSlugByIDAndTenantQuery, statusPageID, tenantID)
		}
		return r.resolveSlugs(ctx, statusPageSlugByIDQuery, statusPageID)
	}

	tenantID, err := parseEventUUID(event.TenantID)
	if err != nil {
		return nil, err
	}
	if tenantID == uuid.Nil {
		return nil, nil
	}

	monitorID, err := parseEventUUID(event.MonitorID)
	if err != nil {
		return nil, err
	}
	if monitorID == uuid.Nil {
		return nil, nil
	}

	slugs, err := r.resolveSlugs(ctx, statusPageSlugQuery, tenantID, monitorID)
	if err != nil && isUndefinedTableError(err) {
		return r.resolveSlugs(ctx, legacyStatusPageSlugQuery, tenantID, monitorID)
	}
	return slugs, err
}

func (r statusPageSlugResolver) resolveSlugs(ctx context.Context, query string, args ...interface{}) ([]string, error) {
	rows, err := r.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	slugs := make([]string, 0)
	for rows.Next() {
		var slug string
		if err := rows.Scan(&slug); err != nil {
			return nil, err
		}
		if slug != "" {
			slugs = append(slugs, slug)
		}
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return slugs, nil
}

func (s *Subscriber) handleEvent(event statusupdates.Event) {
	if s.hub == nil {
		return
	}

	payload, err := json.Marshal(event)
	if err != nil {
		s.warn(err, "Failed to marshal status page update event", nil)
		return
	}

	slugs, err := s.resolveAffectedSlugs(event)
	if err != nil {
		s.warn(err, "Failed to resolve status page slugs for update", map[string]interface{}{
			"tenant_id":      event.TenantID,
			"monitor_id":     event.MonitorID,
			"status_page_id": event.StatusPageID,
			"type":           event.Type,
		})
		return
	}

	update := SSEEvent{
		Type: "update",
		Data: string(payload),
	}
	seen := make(map[string]struct{}, len(slugs))
	for _, slug := range slugs {
		if slug == "" {
			continue
		}
		if _, ok := seen[slug]; ok {
			continue
		}
		seen[slug] = struct{}{}
		s.hub.Broadcast(slug, update)
	}
}

func (s *Subscriber) resolveAffectedSlugs(event statusupdates.Event) ([]string, error) {
	if s.resolveSlugs == nil {
		return nil, nil
	}

	ctx, cancel := context.WithTimeout(context.Background(), statusPageSlugResolveTimeout)
	defer cancel()

	return s.resolveSlugs(ctx, event)
}

func (s *Subscriber) warn(err error, message string, fields map[string]interface{}) {
	if s.logger == nil {
		return
	}

	entry := s.logger.WithFields(fields)
	if err != nil {
		entry = entry.WithError(err)
	}
	entry.Warn(message)
}

func parseEventUUID(raw string) (uuid.UUID, error) {
	value := strings.TrimSpace(raw)
	if value == "" {
		return uuid.Nil, nil
	}
	return uuid.Parse(value)
}
