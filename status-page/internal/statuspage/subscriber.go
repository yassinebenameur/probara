package statuspage

import (
	"context"
	"database/sql"
	"encoding/json"
	"strings"
	"sync"
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
		WHERE tenant_id = $1 AND id = $2 AND deleted_at IS NULL
		UNION
		SELECT mg.group_id
		FROM monitor_groups mg
		JOIN monitor_targets mt ON mt.id = mg.monitor_id
		JOIN monitors m ON m.id = mg.group_id
		WHERE m.tenant_id = $1 AND m.type = 'group' AND m.deleted_at IS NULL
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
		WHERE tenant_id = $1 AND id = $2 AND deleted_at IS NULL
		UNION
		SELECT mg.group_id
		FROM monitor_groups mg
		JOIN monitor_targets mt ON mt.id = mg.monitor_id
		JOIN monitors m ON m.id = mg.group_id
		WHERE m.tenant_id = $1 AND m.type = 'group' AND m.deleted_at IS NULL
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

const (
	statusPageSlugResolveTimeout = 5 * time.Second
	slugCacheTTL                 = 60 * time.Second
	slugCacheMaxEntries          = 1024
	slugBroadcastInterval        = 10 * time.Second
)

type slugResolver func(context.Context, statusupdates.Event) ([]string, error)

type statusPageSlugStore interface {
	QueryContext(ctx context.Context, query string, args ...interface{}) (*sql.Rows, error)
}

type statusPageSlugResolver struct {
	db statusPageSlugStore
}

// renderInvalidator is the subset of renderCache the subscriber needs. It is
// an interface so tests can observe invalidation ordering with a spy.
type renderInvalidator interface {
	Invalidate(slug string)
	HasEntries() bool
}

// Subscriber listens for status update events, invalidates the render cache,
// and broadcasts to SSE clients.
type Subscriber struct {
	subject      *statusupdates.Subscriber
	hub          *Hub
	logger       *logger.Logger
	resolveSlugs slugResolver
	throttle     *slugThrottler
	cache        renderInvalidator
}

// NewSubscriber creates a new status update subscriber. cache may be nil.
func NewSubscriber(natsURL string, hub *Hub, dbClient shareddb.Querier, log *logger.Logger, cache *renderCache) (*Subscriber, error) {
	sub, err := statusupdates.NewSubscriber(natsURL)
	if err != nil {
		return nil, err
	}
	return &Subscriber{
		subject:      sub,
		hub:          hub,
		logger:       log,
		resolveSlugs: newCachingSlugResolver(newStatusPageSlugResolver(dbClient), slugCacheTTL, slugCacheMaxEntries, time.Now),
		throttle:     newSlugThrottler(slugBroadcastInterval),
		cache:        cache,
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

type slugCacheEntry struct {
	slugs   []string
	expires time.Time
}

type slugCache struct {
	mu         sync.Mutex
	ttl        time.Duration
	maxEntries int
	now        func() time.Time
	entries    map[string]slugCacheEntry
}

// slugCacheKey maps an event to its resolution inputs. An empty key means the
// event is not cacheable (Resolve short-circuits to nil without a query).
func slugCacheKey(event statusupdates.Event) string {
	statusPageID := strings.TrimSpace(event.StatusPageID)
	tenantID := strings.TrimSpace(event.TenantID)
	if statusPageID != "" {
		return "page:" + statusPageID + "|" + tenantID
	}
	monitorID := strings.TrimSpace(event.MonitorID)
	if tenantID == "" || monitorID == "" {
		return ""
	}
	return "monitor:" + tenantID + "|" + monitorID
}

// newCachingSlugResolver wraps a slugResolver with a TTL cache so bursts of
// events for the same monitor or page hit the database at most once per TTL.
// Page→monitor membership changes rarely, so brief staleness is acceptable.
// Negative results (no slugs) are cached too; errors are not.
func newCachingSlugResolver(inner slugResolver, ttl time.Duration, maxEntries int, now func() time.Time) slugResolver {
	if inner == nil {
		return nil
	}
	cache := &slugCache{
		ttl:        ttl,
		maxEntries: maxEntries,
		now:        now,
		entries:    make(map[string]slugCacheEntry),
	}
	return func(ctx context.Context, event statusupdates.Event) ([]string, error) {
		key := slugCacheKey(event)
		if key == "" {
			return inner(ctx, event)
		}
		if slugs, ok := cache.get(key); ok {
			return slugs, nil
		}
		slugs, err := inner(ctx, event)
		if err != nil {
			return nil, err
		}
		cache.set(key, slugs)
		return slugs, nil
	}
}

func (c *slugCache) get(key string) ([]string, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	entry, ok := c.entries[key]
	if !ok || c.now().After(entry.expires) {
		return nil, false
	}
	return entry.slugs, true
}

func (c *slugCache) set(key string, slugs []string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	// Simplest possible eviction: drop everything when over cap.
	if _, exists := c.entries[key]; !exists && len(c.entries) >= c.maxEntries {
		c.entries = make(map[string]slugCacheEntry)
	}
	c.entries[key] = slugCacheEntry{slugs: slugs, expires: c.now().Add(c.ttl)}
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

	// NOTE: the ordering below matters.
	//  1. Cheap guard: with no connected SSE clients AND an empty render
	//     cache there is nothing to invalidate and nobody to notify, so skip
	//     slug resolution (and its SQL) entirely.
	//  2. Resolve the affected slugs (through the 60s slug TTL cache) and
	//     invalidate the render cache for every one of them BEFORE any
	//     broadcast: a browser that refetches the page in response to the SSE
	//     event must never be served stale pre-event HTML from the cache.
	//  3. Only then broadcast (throttled per slug, urgent events bypass), and
	//     only if someone is actually listening.
	if !s.hub.HasAnyClients() && !s.cacheHasEntries() {
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

	seen := make(map[string]struct{}, len(slugs))
	affected := make([]string, 0, len(slugs))
	for _, slug := range slugs {
		if slug == "" {
			continue
		}
		if _, ok := seen[slug]; ok {
			continue
		}
		seen[slug] = struct{}{}
		affected = append(affected, slug)
		s.invalidateCache(slug)
	}

	if !s.hub.HasAnyClients() {
		return
	}

	update := SSEEvent{
		Type: "update",
		Data: string(payload),
	}
	urgent := isUrgentEventType(event.Type)
	for _, slug := range affected {
		s.throttle.Fire(slug, urgent, func() {
			s.hub.Broadcast(slug, update)
		})
	}
}

// cacheHasEntries reports whether the render cache holds any entry. Both a
// nil interface and a nil *renderCache behind it count as empty.
func (s *Subscriber) cacheHasEntries() bool {
	return s.cache != nil && s.cache.HasEntries()
}

func (s *Subscriber) invalidateCache(slug string) {
	if s.cache != nil {
		s.cache.Invalidate(slug)
	}
}

// isUrgentEventType reports whether an event must bypass the broadcast
// throttle. State transitions and incident updates are user-visible changes
// that should reach browsers immediately; routine traffic (e.g. legacy
// per-check "check_result" floods from old workers) is coalesced.
func isUrgentEventType(eventType string) bool {
	return eventType == "state_change" || strings.HasPrefix(eventType, "incident")
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
