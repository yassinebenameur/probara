package domainexpiry

import (
	"context"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

// IANABootstrapURL is the IANA RDAP bootstrap registry for DNS (RFC 9224).
const IANABootstrapURL = "https://data.iana.org/rdap/dns.json"

// Directory refresh and cache timing.
const (
	bootstrapRefreshEvery = 24 * time.Hour
	bootstrapRetryAfter   = 6 * time.Hour
	bootstrapFetchTimeout = 30 * time.Second
	whoisServerTTL        = 24 * time.Hour
	maxBootstrapBytes     = 1 << 20
)

// The RDAP bootstrap registry ships embedded (a snapshot of
// IANABootstrapURL, refreshed with releases), so a worker at a private
// location with no route to IANA still knows every TLD's RDAP server. A
// Directory with a fetch function also refreshes it from IANA once a day in
// the background; a failed refresh keeps the current table.
//
//go:embed rdap_dns.json
var embeddedBootstrap []byte

// Directory knows where to ask about a TLD: its RDAP base URLs (bootstrap
// registry) and its WHOIS server (learned from IANA's WHOIS, cached). One
// Directory serves every check a worker runs.
type Directory struct {
	// fetch downloads the bootstrap registry; nil never refreshes.
	fetch func(ctx context.Context, url string) ([]byte, error)
	now   func() time.Time

	mu          sync.Mutex
	rdap        map[string][]string
	nextRefresh time.Time
	refreshing  bool
	whois       map[string]whoisServerEntry
	hosts       map[string]*hostBucket
}

type whoisServerEntry struct {
	host    string // "" caches "IANA lists no WHOIS server"
	expires time.Time
}

// NewDirectory returns a Directory seeded with the embedded bootstrap
// snapshot. fetch, when non-nil, refreshes it from IANABootstrapURL in the
// background; it must dial through the same network policy as lookups.
func NewDirectory(fetch func(ctx context.Context, url string) ([]byte, error)) *Directory {
	services, err := parseBootstrap(embeddedBootstrap)
	if err != nil {
		panic("domainexpiry: embedded RDAP bootstrap: " + err.Error())
	}
	return &Directory{fetch: fetch, now: time.Now, rdap: services, whois: map[string]whoisServerEntry{}, hosts: map[string]*hostBucket{}}
}

// newDirectoryWith is a Directory over a fixed RDAP table (tests).
func newDirectoryWith(rdap map[string][]string) *Directory {
	return &Directory{now: time.Now, rdap: rdap, whois: map[string]whoisServerEntry{}, hosts: map[string]*hostBucket{}}
}

// RDAPServers returns the RDAP base URLs (https, ending in "/") for a TLD,
// or none when the registry lists no RDAP service for it.
func (d *Directory) RDAPServers(tld string) []string {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.fetch != nil && !d.refreshing && !d.now().Before(d.nextRefresh) {
		d.refreshing = true
		go d.refresh()
	}
	return d.rdap[strings.ToLower(tld)]
}

func (d *Directory) refresh() {
	ctx, cancel := context.WithTimeout(context.Background(), bootstrapFetchTimeout)
	defer cancel()
	body, err := d.fetch(ctx, IANABootstrapURL)
	var services map[string][]string
	if err == nil {
		services, err = parseBootstrap(body)
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	d.refreshing = false
	if err != nil {
		d.nextRefresh = d.now().Add(bootstrapRetryAfter)
		return
	}
	d.rdap = services
	d.nextRefresh = d.now().Add(bootstrapRefreshEvery)
}

func (d *Directory) cachedWHOISServer(tld string) (string, bool) {
	d.mu.Lock()
	defer d.mu.Unlock()
	e, ok := d.whois[tld]
	if !ok || d.now().After(e.expires) {
		return "", false
	}
	return e.host, true
}

// storeWHOISServer caches a TLD's WHOIS server, or "" for none. The cache is
// bounded (maxCachedWHOISes): TLDs are validated against the public suffix
// list, but the bound holds whatever reaches it.
func (d *Directory) storeWHOISServer(tld, host string) {
	d.mu.Lock()
	defer d.mu.Unlock()
	now := d.now()
	if _, ok := d.whois[tld]; !ok && len(d.whois) >= maxCachedWHOISes {
		d.evictWHOISLocked(now)
	}
	d.whois[tld] = whoisServerEntry{host: host, expires: now.Add(whoisServerTTL)}
}

// Fetcher returns a fetch function for NewDirectory that downloads over
// HTTPS through dial, under the same client rules as RDAP lookups.
func Fetcher(dial func(ctx context.Context, network, address string) (net.Conn, error)) func(ctx context.Context, url string) ([]byte, error) {
	return func(ctx context.Context, url string) ([]byte, error) {
		client := (&Client{Dial: dial}).httpClient()
		defer client.CloseIdleConnections()
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
		if err != nil {
			return nil, err
		}
		req.Header.Set("Accept", "application/json")
		resp, err := client.Do(req)
		if err != nil {
			return nil, lookupError(ctx, err)
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			return nil, fmt.Errorf("HTTP %d", resp.StatusCode)
		}
		body, err := io.ReadAll(io.LimitReader(resp.Body, maxBootstrapBytes+1))
		if err != nil {
			return nil, lookupError(ctx, err)
		}
		return body, nil
	}
}

// parseBootstrap reads an RFC 9224 bootstrap file into TLD → base URLs.
// Only https bases on a hostname (no address literal, port, credentials,
// query or fragment) are kept: a plain-http RDAP answer could be altered in transit,
// and the TLD then falls back to WHOIS like one without RDAP.
func parseBootstrap(body []byte) (map[string][]string, error) {
	if len(body) > maxBootstrapBytes {
		return nil, errors.New("bootstrap registry too large")
	}
	var doc struct {
		Services [][][]string `json:"services"`
	}
	if err := json.Unmarshal(body, &doc); err != nil {
		return nil, errors.New("invalid bootstrap registry")
	}
	services := map[string][]string{}
	for _, svc := range doc.Services {
		if len(svc) < 2 {
			continue
		}
		var bases []string
		for _, raw := range svc[1] {
			if base, ok := normalizeRDAPBase(raw); ok {
				bases = append(bases, base)
			}
		}
		if len(bases) == 0 {
			continue
		}
		for _, suffix := range svc[0] {
			if tld := strings.ToLower(strings.TrimSuffix(strings.TrimSpace(suffix), ".")); tld != "" {
				services[tld] = bases
			}
		}
	}
	if len(services) == 0 {
		return nil, errors.New("bootstrap registry lists no services")
	}
	return services, nil
}

func normalizeRDAPBase(raw string) (string, bool) {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || u.Scheme != "https" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || u.Port() != "" {
		return "", false
	}
	if host := strings.ToLower(u.Hostname()); net.ParseIP(host) != nil || !validHostname(host) {
		return "", false
	}
	base := u.String()
	if !strings.HasSuffix(base, "/") {
		base += "/"
	}
	return base, true
}
