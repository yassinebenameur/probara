package statuspage

import (
	"context"
	"encoding/json"
	"reflect"
	"regexp"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/google/uuid"
	"github.com/lib/pq"

	shareddb "github.com/yassinebenameur/probara/shared/db"
	"github.com/yassinebenameur/probara/shared/logger"
	"github.com/yassinebenameur/probara/shared/statusupdates"
)

func TestHubBroadcastTargetsSingleSlug(t *testing.T) {
	hub := NewHub()
	alpha := hub.Register("alpha")
	beta := hub.Register("beta")
	defer hub.Unregister("alpha", alpha)
	defer hub.Unregister("beta", beta)

	event := SSEEvent{Type: "update", Data: "payload"}
	hub.Broadcast("alpha", event)

	assertSSEEvent(t, alpha, event)
	assertNoSSEEvent(t, beta)
}

func TestStatusPageSlugResolverResolveUsesTenantAndMonitor(t *testing.T) {
	sqlDB, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock.New() error = %v", err)
	}
	defer sqlDB.Close()

	tenantID := uuid.New()
	monitorID := uuid.New()
	rows := sqlmock.NewRows([]string{"slug"}).
		AddRow("edge").
		AddRow("api")

	mock.ExpectQuery(regexp.QuoteMeta(statusPageSlugQuery)).
		WithArgs(tenantID, monitorID).
		WillReturnRows(rows)

	resolver := statusPageSlugResolver{db: &shareddb.Client{DB: sqlDB}}
	slugs, err := resolver.Resolve(context.Background(), statusupdates.Event{
		TenantID:  tenantID.String(),
		MonitorID: monitorID.String(),
	})
	if err != nil {
		t.Fatalf("Resolve() error = %v", err)
	}

	if !reflect.DeepEqual(slugs, []string{"edge", "api"}) {
		t.Fatalf("Resolve() slugs = %v, want %v", slugs, []string{"edge", "api"})
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("sql expectations: %v", err)
	}
}

func TestStatusPageSlugResolverResolveIncludesAncestorGroupPages(t *testing.T) {
	sqlDB, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock.New() error = %v", err)
	}
	defer sqlDB.Close()

	tenantID := uuid.New()
	leafMonitorID := uuid.New()
	rows := sqlmock.NewRows([]string{"slug"}).
		AddRow("group-page")

	mock.ExpectQuery(regexp.QuoteMeta(statusPageSlugQuery)).
		WithArgs(tenantID, leafMonitorID).
		WillReturnRows(rows)

	resolver := statusPageSlugResolver{db: &shareddb.Client{DB: sqlDB}}
	slugs, err := resolver.Resolve(context.Background(), statusupdates.Event{
		TenantID:  tenantID.String(),
		MonitorID: leafMonitorID.String(),
	})
	if err != nil {
		t.Fatalf("Resolve() error = %v", err)
	}

	if !reflect.DeepEqual(slugs, []string{"group-page"}) {
		t.Fatalf("Resolve() slugs = %v, want %v", slugs, []string{"group-page"})
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("sql expectations: %v", err)
	}
}

func TestStatusPageSlugResolverResolveFallsBackToLegacyQueryWhenSectionMonitorTableMissing(t *testing.T) {
	sqlDB, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock.New() error = %v", err)
	}
	defer sqlDB.Close()

	tenantID := uuid.New()
	monitorID := uuid.New()
	rows := sqlmock.NewRows([]string{"slug"}).
		AddRow("legacy-page")

	mock.ExpectQuery(regexp.QuoteMeta(statusPageSlugQuery)).
		WithArgs(tenantID, monitorID).
		WillReturnError(&pq.Error{Code: "42P01"})
	mock.ExpectQuery(regexp.QuoteMeta(legacyStatusPageSlugQuery)).
		WithArgs(tenantID, monitorID).
		WillReturnRows(rows)

	resolver := statusPageSlugResolver{db: &shareddb.Client{DB: sqlDB}}
	slugs, err := resolver.Resolve(context.Background(), statusupdates.Event{
		TenantID:  tenantID.String(),
		MonitorID: monitorID.String(),
	})
	if err != nil {
		t.Fatalf("Resolve() error = %v", err)
	}

	if !reflect.DeepEqual(slugs, []string{"legacy-page"}) {
		t.Fatalf("Resolve() slugs = %v, want %v", slugs, []string{"legacy-page"})
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("sql expectations: %v", err)
	}
}

func TestStatusPageSlugResolverResolveUsesStatusPageIDWhenProvided(t *testing.T) {
	sqlDB, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock.New() error = %v", err)
	}
	defer sqlDB.Close()

	statusPageID := uuid.New()
	rows := sqlmock.NewRows([]string{"slug"}).
		AddRow("edge")

	mock.ExpectQuery(regexp.QuoteMeta(statusPageSlugByIDQuery)).
		WithArgs(statusPageID).
		WillReturnRows(rows)

	resolver := statusPageSlugResolver{db: &shareddb.Client{DB: sqlDB}}
	slugs, err := resolver.Resolve(context.Background(), statusupdates.Event{
		StatusPageID: statusPageID.String(),
	})
	if err != nil {
		t.Fatalf("Resolve() error = %v", err)
	}

	if !reflect.DeepEqual(slugs, []string{"edge"}) {
		t.Fatalf("Resolve() slugs = %v, want %v", slugs, []string{"edge"})
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("sql expectations: %v", err)
	}
}

func TestStatusPageSlugResolverResolveUsesTenantScopedStatusPageIDWhenTenantProvided(t *testing.T) {
	sqlDB, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock.New() error = %v", err)
	}
	defer sqlDB.Close()

	tenantID := uuid.New()
	statusPageID := uuid.New()
	rows := sqlmock.NewRows([]string{"slug"})

	mock.ExpectQuery(regexp.QuoteMeta(statusPageSlugByIDAndTenantQuery)).
		WithArgs(statusPageID, tenantID).
		WillReturnRows(rows)

	resolver := statusPageSlugResolver{db: &shareddb.Client{DB: sqlDB}}
	slugs, err := resolver.Resolve(context.Background(), statusupdates.Event{
		TenantID:     tenantID.String(),
		StatusPageID: statusPageID.String(),
	})
	if err != nil {
		t.Fatalf("Resolve() error = %v", err)
	}
	if len(slugs) != 0 {
		t.Fatalf("Resolve() slugs = %v, want empty", slugs)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("sql expectations: %v", err)
	}
}

func TestSubscriberHandleEventBroadcastsOnlyResolvedSlugs(t *testing.T) {
	hub := NewHub()
	alpha := hub.Register("alpha")
	beta := hub.Register("beta")
	gamma := hub.Register("gamma")
	defer hub.Unregister("alpha", alpha)
	defer hub.Unregister("beta", beta)
	defer hub.Unregister("gamma", gamma)

	event := statusupdates.Event{
		Type:      "monitor.updated",
		TenantID:  uuid.New().String(),
		MonitorID: uuid.New().String(),
		Timestamp: time.Date(2026, time.March, 14, 9, 30, 0, 0, time.UTC),
	}
	payload, err := json.Marshal(event)
	if err != nil {
		t.Fatalf("json.Marshal() error = %v", err)
	}

	subscriber := &Subscriber{
		hub:    hub,
		logger: logger.New("status-page", "debug"),
		resolveSlugs: func(ctx context.Context, got statusupdates.Event) ([]string, error) {
			if !reflect.DeepEqual(got, event) {
				t.Fatalf("resolver event = %#v, want %#v", got, event)
			}
			return []string{"alpha", "gamma"}, nil
		},
	}

	subscriber.handleEvent(event)

	expected := SSEEvent{Type: "update", Data: string(payload)}
	assertSSEEvent(t, alpha, expected)
	assertNoSSEEvent(t, beta)
	assertSSEEvent(t, gamma, expected)
}

func TestSubscriberHandleEventBroadcastsDirectStatusPageTarget(t *testing.T) {
	hub := NewHub()
	alpha := hub.Register("alpha")
	beta := hub.Register("beta")
	defer hub.Unregister("alpha", alpha)
	defer hub.Unregister("beta", beta)

	event := statusupdates.Event{
		Type:         "incident.publication.updated",
		TenantID:     uuid.New().String(),
		StatusPageID: uuid.New().String(),
		Timestamp:    time.Date(2026, time.March, 14, 9, 31, 0, 0, time.UTC),
	}
	payload, err := json.Marshal(event)
	if err != nil {
		t.Fatalf("json.Marshal() error = %v", err)
	}

	subscriber := &Subscriber{
		hub:    hub,
		logger: logger.New("status-page", "debug"),
		resolveSlugs: func(ctx context.Context, got statusupdates.Event) ([]string, error) {
			if !reflect.DeepEqual(got, event) {
				t.Fatalf("resolver event = %#v, want %#v", got, event)
			}
			return []string{"alpha"}, nil
		},
	}

	subscriber.handleEvent(event)

	expected := SSEEvent{Type: "update", Data: string(payload)}
	assertSSEEvent(t, alpha, expected)
	assertNoSSEEvent(t, beta)
}

func TestHubHasAnyClients(t *testing.T) {
	hub := NewHub()
	if hub.HasAnyClients() {
		t.Fatal("HasAnyClients() = true on empty hub, want false")
	}

	ch := hub.Register("alpha")
	if !hub.HasAnyClients() {
		t.Fatal("HasAnyClients() = false with a registered client, want true")
	}

	hub.Unregister("alpha", ch)
	if hub.HasAnyClients() {
		t.Fatal("HasAnyClients() = true after last client left, want false")
	}
}

func TestSubscriberHandleEventNoClientsSkipsSlugResolution(t *testing.T) {
	sqlDB, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock.New() error = %v", err)
	}
	defer sqlDB.Close()
	// No expectations registered: any query is unexpected.

	resolverCalled := false
	inner := newStatusPageSlugResolver(&shareddb.Client{DB: sqlDB})
	subscriber := &Subscriber{
		hub:    NewHub(),
		logger: logger.New("status-page", "debug"),
		resolveSlugs: func(ctx context.Context, event statusupdates.Event) ([]string, error) {
			resolverCalled = true
			return inner(ctx, event)
		},
		throttle: newSlugThrottler(slugBroadcastInterval),
	}

	subscriber.handleEvent(statusupdates.Event{
		Type:      "check_result",
		TenantID:  uuid.New().String(),
		MonitorID: uuid.New().String(),
		Timestamp: time.Now().UTC(),
	})

	if resolverCalled {
		t.Fatal("slug resolution ran with zero connected clients")
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("expected zero DB queries with no connected clients: %v", err)
	}
}

func TestCachingSlugResolverHitsDatabaseOncePerTTL(t *testing.T) {
	sqlDB, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock.New() error = %v", err)
	}
	defer sqlDB.Close()

	tenantID := uuid.New()
	monitorID := uuid.New()
	mock.ExpectQuery(regexp.QuoteMeta(statusPageSlugQuery)).
		WithArgs(tenantID, monitorID).
		WillReturnRows(sqlmock.NewRows([]string{"slug"}).AddRow("edge"))

	resolver := newCachingSlugResolver(
		newStatusPageSlugResolver(&shareddb.Client{DB: sqlDB}),
		slugCacheTTL, slugCacheMaxEntries, time.Now,
	)
	event := statusupdates.Event{
		Type:      "check_result",
		TenantID:  tenantID.String(),
		MonitorID: monitorID.String(),
	}

	for i := 0; i < 3; i++ {
		slugs, err := resolver(context.Background(), event)
		if err != nil {
			t.Fatalf("resolver() call %d error = %v", i, err)
		}
		if !reflect.DeepEqual(slugs, []string{"edge"}) {
			t.Fatalf("resolver() call %d slugs = %v, want [edge]", i, slugs)
		}
	}

	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("expected exactly one DB query within TTL: %v", err)
	}
}

func TestCachingSlugResolverCachesNegativeResults(t *testing.T) {
	calls := 0
	inner := func(ctx context.Context, event statusupdates.Event) ([]string, error) {
		calls++
		return []string{}, nil
	}

	resolver := newCachingSlugResolver(inner, slugCacheTTL, slugCacheMaxEntries, time.Now)
	event := statusupdates.Event{
		TenantID:  uuid.New().String(),
		MonitorID: uuid.New().String(),
	}

	for i := 0; i < 2; i++ {
		slugs, err := resolver(context.Background(), event)
		if err != nil {
			t.Fatalf("resolver() error = %v", err)
		}
		if len(slugs) != 0 {
			t.Fatalf("resolver() slugs = %v, want empty", slugs)
		}
	}

	if calls != 1 {
		t.Fatalf("inner resolver calls = %d, want 1 (negative result must be cached)", calls)
	}
}

func TestCachingSlugResolverExpiresAfterTTL(t *testing.T) {
	calls := 0
	inner := func(ctx context.Context, event statusupdates.Event) ([]string, error) {
		calls++
		return []string{"edge"}, nil
	}

	base := time.Now()
	current := base
	resolver := newCachingSlugResolver(inner, slugCacheTTL, slugCacheMaxEntries, func() time.Time { return current })
	event := statusupdates.Event{
		TenantID:  uuid.New().String(),
		MonitorID: uuid.New().String(),
	}

	if _, err := resolver(context.Background(), event); err != nil {
		t.Fatalf("resolver() error = %v", err)
	}
	current = base.Add(slugCacheTTL + time.Second)
	if _, err := resolver(context.Background(), event); err != nil {
		t.Fatalf("resolver() error = %v", err)
	}

	if calls != 2 {
		t.Fatalf("inner resolver calls = %d, want 2 (entry past TTL must be refreshed)", calls)
	}
}

func TestCachingSlugResolverDoesNotCacheErrors(t *testing.T) {
	calls := 0
	inner := func(ctx context.Context, event statusupdates.Event) ([]string, error) {
		calls++
		return nil, context.DeadlineExceeded
	}

	resolver := newCachingSlugResolver(inner, slugCacheTTL, slugCacheMaxEntries, time.Now)
	event := statusupdates.Event{
		TenantID:  uuid.New().String(),
		MonitorID: uuid.New().String(),
	}

	for i := 0; i < 2; i++ {
		if _, err := resolver(context.Background(), event); err == nil {
			t.Fatal("resolver() error = nil, want error")
		}
	}

	if calls != 2 {
		t.Fatalf("inner resolver calls = %d, want 2 (errors must not be cached)", calls)
	}
}

func TestCachingSlugResolverEvictsWhenOverCap(t *testing.T) {
	calls := 0
	inner := func(ctx context.Context, event statusupdates.Event) ([]string, error) {
		calls++
		return []string{"edge"}, nil
	}

	resolver := newCachingSlugResolver(inner, slugCacheTTL, 2, time.Now)
	first := statusupdates.Event{TenantID: uuid.New().String(), MonitorID: uuid.New().String()}

	if _, err := resolver(context.Background(), first); err != nil {
		t.Fatalf("resolver() error = %v", err)
	}
	for i := 0; i < 3; i++ {
		event := statusupdates.Event{TenantID: uuid.New().String(), MonitorID: uuid.New().String()}
		if _, err := resolver(context.Background(), event); err != nil {
			t.Fatalf("resolver() error = %v", err)
		}
	}
	// Cache was reset when it exceeded the cap; first key resolves again.
	if _, err := resolver(context.Background(), first); err != nil {
		t.Fatalf("resolver() error = %v", err)
	}

	if calls != 5 {
		t.Fatalf("inner resolver calls = %d, want 5 (eviction dropped the first entry)", calls)
	}
}

func TestSubscriberHandleEventThrottlesRoutineEvents(t *testing.T) {
	hub := NewHub()
	alpha := hub.Register("alpha")
	defer hub.Unregister("alpha", alpha)

	subscriber := &Subscriber{
		hub:    hub,
		logger: logger.New("status-page", "debug"),
		resolveSlugs: func(ctx context.Context, event statusupdates.Event) ([]string, error) {
			return []string{"alpha"}, nil
		},
		throttle: newSlugThrottler(40 * time.Millisecond),
	}

	event := statusupdates.Event{
		Type:      "check_result",
		TenantID:  uuid.New().String(),
		MonitorID: uuid.New().String(),
	}
	for i := 0; i < 5; i++ {
		subscriber.handleEvent(event)
	}

	if got := len(alpha); got != 1 {
		t.Fatalf("delivered events = %d after burst, want 1 leading fire", got)
	}

	// The trailing fire delivers the last suppressed event after the window.
	select {
	case <-alpha:
	case <-time.After(2 * time.Second):
		t.Fatal("expected a trailing SSE event after the throttle interval")
	}
	assertNoSSEEvent(t, alpha)
}

func TestSubscriberHandleEventUrgentEventsBypassThrottle(t *testing.T) {
	hub := NewHub()
	alpha := hub.Register("alpha")
	defer hub.Unregister("alpha", alpha)

	subscriber := &Subscriber{
		hub:    hub,
		logger: logger.New("status-page", "debug"),
		resolveSlugs: func(ctx context.Context, event statusupdates.Event) ([]string, error) {
			return []string{"alpha"}, nil
		},
		throttle: newSlugThrottler(time.Hour),
	}

	tenantID := uuid.New().String()
	monitorID := uuid.New().String()
	subscriber.handleEvent(statusupdates.Event{Type: "state_change", TenantID: tenantID, MonitorID: monitorID})
	subscriber.handleEvent(statusupdates.Event{Type: "state_change", TenantID: tenantID, MonitorID: monitorID})
	subscriber.handleEvent(statusupdates.Event{Type: "incident.publication.updated", TenantID: tenantID, MonitorID: monitorID})

	if got := len(alpha); got != 3 {
		t.Fatalf("delivered events = %d, want 3 (urgent events bypass throttle)", got)
	}
}

func TestIsUrgentEventType(t *testing.T) {
	cases := map[string]bool{
		"state_change":                 true,
		"incident.publication.updated": true,
		"incident_created":             true,
		"check_result":                 false,
		"history_deleted":              false,
		"monitor.updated":              false,
		"":                             false,
	}
	for eventType, want := range cases {
		if got := isUrgentEventType(eventType); got != want {
			t.Errorf("isUrgentEventType(%q) = %v, want %v", eventType, got, want)
		}
	}
}

// spyRenderCache is an order-observable renderInvalidator for tests.
type spyRenderCache struct {
	hasEntries   bool
	invalidated  []string
	onInvalidate func(slug string)
}

func (s *spyRenderCache) Invalidate(slug string) {
	if s.onInvalidate != nil {
		s.onInvalidate(slug)
	}
	s.invalidated = append(s.invalidated, slug)
}

func (s *spyRenderCache) HasEntries() bool { return s.hasEntries }

func TestSubscriberHandleEventInvalidatesCacheBeforeBroadcast(t *testing.T) {
	hub := NewHub()
	alpha := hub.Register("alpha")
	defer hub.Unregister("alpha", alpha)

	cache := &spyRenderCache{hasEntries: true}
	cache.onInvalidate = func(slug string) {
		// The SSE event triggers a refetch in the browser; it must never be
		// able to observe pre-invalidation cached HTML. Therefore nothing may
		// have been broadcast by the time the cache is invalidated.
		if len(alpha) != 0 {
			t.Errorf("broadcast for %q happened before cache invalidation", slug)
		}
	}

	subscriber := &Subscriber{
		hub:    hub,
		logger: logger.New("status-page", "debug"),
		cache:  cache,
		resolveSlugs: func(ctx context.Context, event statusupdates.Event) ([]string, error) {
			return []string{"alpha", "alpha", ""}, nil
		},
		throttle: newSlugThrottler(slugBroadcastInterval),
	}

	subscriber.handleEvent(statusupdates.Event{
		Type:      "state_change",
		TenantID:  uuid.New().String(),
		MonitorID: uuid.New().String(),
	})

	if !reflect.DeepEqual(cache.invalidated, []string{"alpha"}) {
		t.Fatalf("invalidated slugs = %v, want [alpha] (deduped, empties dropped)", cache.invalidated)
	}
	if got := len(alpha); got != 1 {
		t.Fatalf("delivered events = %d, want 1", got)
	}
}

func TestSubscriberHandleEventNoClientsAndEmptyCacheSkipsResolution(t *testing.T) {
	sqlDB, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock.New() error = %v", err)
	}
	defer sqlDB.Close()
	// No expectations registered: any query is unexpected.

	cache := &spyRenderCache{hasEntries: false}
	subscriber := &Subscriber{
		hub:          NewHub(),
		logger:       logger.New("status-page", "debug"),
		cache:        cache,
		resolveSlugs: newStatusPageSlugResolver(&shareddb.Client{DB: sqlDB}),
		throttle:     newSlugThrottler(slugBroadcastInterval),
	}

	subscriber.handleEvent(statusupdates.Event{
		Type:      "check_result",
		TenantID:  uuid.New().String(),
		MonitorID: uuid.New().String(),
		Timestamp: time.Now().UTC(),
	})

	if len(cache.invalidated) != 0 {
		t.Fatalf("invalidated slugs = %v, want none", cache.invalidated)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("expected zero DB queries with no clients and no cache entries: %v", err)
	}
}

func TestSubscriberHandleEventNoClientsButCachedEntriesInvalidatesWithoutBroadcast(t *testing.T) {
	sqlDB, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock.New() error = %v", err)
	}
	defer sqlDB.Close()

	tenantID := uuid.New()
	monitorID := uuid.New()
	mock.ExpectQuery(regexp.QuoteMeta(statusPageSlugQuery)).
		WithArgs(tenantID, monitorID).
		WillReturnRows(sqlmock.NewRows([]string{"slug"}).AddRow("edge"))

	cache := &spyRenderCache{hasEntries: true}
	subscriber := &Subscriber{
		hub:          NewHub(), // no clients connected
		logger:       logger.New("status-page", "debug"),
		cache:        cache,
		resolveSlugs: newStatusPageSlugResolver(&shareddb.Client{DB: sqlDB}),
		// nil throttle: reaching the broadcast path would panic the test,
		// proving no broadcast is attempted when nobody is listening.
		throttle: nil,
	}

	subscriber.handleEvent(statusupdates.Event{
		Type:      "state_change",
		TenantID:  tenantID.String(),
		MonitorID: monitorID.String(),
	})

	if !reflect.DeepEqual(cache.invalidated, []string{"edge"}) {
		t.Fatalf("invalidated slugs = %v, want [edge]", cache.invalidated)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("expected the slug query to run for cache invalidation: %v", err)
	}
}

func TestSubscriberHandleEventNilCacheBehavesLikeEmpty(t *testing.T) {
	// Subscriber constructed without a cache (e.g. existing tests, or a nil
	// *renderCache passed through NewSubscriber) must keep the old behavior:
	// exit early when no clients, broadcast normally when clients exist.
	subscriber := &Subscriber{
		hub:    NewHub(),
		logger: logger.New("status-page", "debug"),
		resolveSlugs: func(ctx context.Context, event statusupdates.Event) ([]string, error) {
			t.Fatal("slug resolution ran with zero clients and nil cache")
			return nil, nil
		},
		throttle: newSlugThrottler(slugBroadcastInterval),
	}
	subscriber.handleEvent(statusupdates.Event{
		Type:      "check_result",
		TenantID:  uuid.New().String(),
		MonitorID: uuid.New().String(),
	})

	var nilCache *renderCache
	subscriber.cache = nilCache // non-nil interface holding a nil pointer
	subscriber.handleEvent(statusupdates.Event{
		Type:      "check_result",
		TenantID:  uuid.New().String(),
		MonitorID: uuid.New().String(),
	})
}

func assertSSEEvent(t *testing.T, ch <-chan SSEEvent, want SSEEvent) {
	t.Helper()

	select {
	case got := <-ch:
		if got != want {
			t.Fatalf("event = %#v, want %#v", got, want)
		}
	default:
		t.Fatalf("expected SSE event %#v", want)
	}
}

func assertNoSSEEvent(t *testing.T, ch <-chan SSEEvent) {
	t.Helper()

	select {
	case got := <-ch:
		t.Fatalf("unexpected SSE event %#v", got)
	default:
	}
}
