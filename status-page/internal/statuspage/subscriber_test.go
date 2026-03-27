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
