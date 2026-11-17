package statuspage

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/google/uuid"
	"golang.org/x/sync/singleflight"

	"github.com/yassinebenameur/probara/shared/db/pgerr"
	"github.com/yassinebenameur/probara/shared/pagedomains"
)

// Custom domains, read side (shared/pagedomains is the contract; the API
// writes and verifies them).
//
// The host table maps every verified custom domain to its page. It is loaded
// whole, in one read query, so a request's Host never reaches the database:
// a forged or random Host costs a map lookup and can neither pick another
// page nor fill a cache. It is reloaded after hostTableTTL, and at once
// (lazily, on the next request) when the API publishes a change to a page
// (Subscriber.handleEvent), so a removed or failed domain, a renamed slug or
// a deleted page stops being served immediately; the TTL only covers a lost
// event. The gate checks the page it resolves against the table's page ID
// too, so even a stale entry can never serve a page that reused the slug.

// hostTableTTL bounds how long a change can go unnoticed when its event is
// lost.
const hostTableTTL = 30 * time.Second

// hostTableRetry is how soon a failed reload is retried; until then the
// previous table keeps serving.
const hostTableRetry = 5 * time.Second

// customHostRoute is the page a verified custom domain serves.
type customHostRoute struct {
	PageID uuid.UUID
	Slug   string
}

// hostTable is the in-memory table of verified custom domains.
type hostTable struct {
	load func(context.Context) (map[string]customHostRoute, error)
	ttl  time.Duration
	now  func() time.Time

	group singleflight.Group

	mu       sync.Mutex
	routes   map[string]customHostRoute
	validTil time.Time
	gen      uint64
}

func newHostTable(load func(context.Context) (map[string]customHostRoute, error)) *hostTable {
	return &hostTable{load: load, ttl: hostTableTTL, now: time.Now}
}

// Lookup returns the page served on host (normalized, pagedomains.HostFromHeader).
func (t *hostTable) Lookup(ctx context.Context, host string) (customHostRoute, bool) {
	if t == nil || host == "" {
		return customHostRoute{}, false
	}
	routes := t.current(ctx)
	route, ok := routes[host]
	return route, ok
}

// current returns the table, reloading it first when it expired or was
// invalidated. Concurrent reloads collapse into one query.
func (t *hostTable) current(ctx context.Context) map[string]customHostRoute {
	t.mu.Lock()
	if t.routes != nil && t.now().Before(t.validTil) {
		routes := t.routes
		t.mu.Unlock()
		return routes
	}
	gen := t.gen
	t.mu.Unlock()

	v, _, _ := t.group.Do("load", func() (interface{}, error) {
		loadCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		defer cancel()
		routes, err := t.load(loadCtx)
		t.mu.Lock()
		defer t.mu.Unlock()
		if err != nil {
			// Keep serving what was known; a database outage takes the pages
			// down with it anyway.
			t.validTil = t.now().Add(hostTableRetry)
			return t.routes, nil
		}
		t.routes = routes
		if t.gen == gen {
			t.validTil = t.now().Add(t.ttl)
		} else {
			// Invalidated while loading: this answer may predate the change.
			t.validTil = time.Time{}
		}
		return routes, nil
	})
	routes, _ := v.(map[string]customHostRoute)
	return routes
}

// Invalidate makes the next lookup reload the table.
func (t *hostTable) Invalidate() {
	if t == nil {
		return
	}
	t.mu.Lock()
	t.gen++
	t.validTil = time.Time{}
	t.mu.Unlock()
	t.group.Forget("load")
}

// customHostRoutesQuery joins through status_pages, so a domain is served
// only while its page exists and every page-level check composes with it.
const customHostRoutesQuery = `
	SELECT d.hostname, sp.id, sp.slug
	FROM status_page_domains d
	JOIN status_pages sp ON sp.id = d.status_page_id
	WHERE d.status = 'verified'`

// CustomHostRoutes loads every verified custom domain. Empty before
// migration 000120.
func (s *Service) CustomHostRoutes(ctx context.Context) (map[string]customHostRoute, error) {
	rows, err := s.db.QueryContext(ctx, customHostRoutesQuery)
	if err != nil {
		if pgerr.IsMissingSchema(err) {
			return map[string]customHostRoute{}, nil
		}
		return nil, fmt.Errorf("load custom domains: %w", err)
	}
	defer rows.Close()
	out := map[string]customHostRoute{}
	for rows.Next() {
		var host string
		var route customHostRoute
		if err := rows.Scan(&host, &route.PageID, &route.Slug); err != nil {
			return nil, fmt.Errorf("scan custom domain: %w", err)
		}
		out[host] = route
	}
	return out, rows.Err()
}

// PrimaryHost is the canonical custom domain of a page ("" for none): the
// same one the API reports as the page's public_url.
func (s *Service) PrimaryHost(ctx context.Context, pageID string) (string, error) {
	id, err := uuid.Parse(pageID)
	if err != nil {
		return "", nil
	}
	var host sql.NullString
	err = s.db.QueryRowContext(ctx, `SELECT `+pagedomains.PrimaryHostSQL("$1::uuid"), id).Scan(&host)
	if err != nil {
		if pgerr.IsMissingSchema(err) || errors.Is(err, sql.ErrNoRows) {
			return "", nil
		}
		return "", fmt.Errorf("load primary custom domain: %w", err)
	}
	return host.String, nil
}

// pageCanonicalURL is the page's canonical URL: on its primary custom
// domain when it has one (on every host it is served from, so search engines
// and link previews name one address), else under STATUS_PAGE_BASE_URL
// (platformURL, "" when unset). A failed lookup falls back to the platform
// URL rather than failing the render.
func (h *Handlers) pageCanonicalURL(ctx context.Context, pageID, slug string) string {
	if h.service != nil {
		if host, err := h.service.PrimaryHost(ctx, pageID); err != nil {
			h.logger.WithError(err).Warn("Failed to load a status page's custom domain; using the platform URL")
		} else if host != "" {
			return pagedomains.PageURL(host)
		}
	}
	return h.canonicalURL(slug)
}
