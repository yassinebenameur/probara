package domainexpiry

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const (
	maxRDAPBytes     = 1 << 20
	maxRDAPRedirects = 3
)

// errNoRDAP: the bootstrap registry lists no RDAP service for the TLD.
var errNoRDAP = errors.New("no RDAP service for this TLD")

// httpClient is the RDAP client: every hop dials through c.Dial, never a
// proxy (a proxy would sidestep the dial policy), and certificates are always
// verified.
func (c *Client) httpClient() *http.Client {
	transport := &http.Transport{
		Proxy:               nil,
		DialContext:         c.Dial,
		TLSClientConfig:     &tls.Config{RootCAs: c.RootCAs, MinVersion: tls.VersionTLS12},
		TLSHandshakeTimeout: 10 * time.Second,
		ForceAttemptHTTP2:   true,
		MaxIdleConns:        1,
		DisableKeepAlives:   true,
	}
	return &http.Client{Transport: transport, CheckRedirect: func(req *http.Request, via []*http.Request) error {
		if err := checkRDAPRedirect(req, via); err != nil {
			return err
		}
		// A redirect to another server spends that server's lookups too.
		if c.Directory != nil && req.URL.Host != via[len(via)-1].URL.Host {
			return c.Directory.acquire(strings.ToLower(req.URL.Hostname()))
		}
		return nil
	}}
}

// checkRDAPRedirect bounds where an RDAP server may send the lookup. RDAP
// servers redirect (a registry to the registrar's server, an aggregator to
// the registry), but the target is chosen by a remote party, so it must stay
// on https, on the default port and on a hostname: never a literal address,
// and never more than a few hops.
func checkRDAPRedirect(req *http.Request, via []*http.Request) error {
	if len(via) > maxRDAPRedirects {
		return errRedirectRefused
	}
	u := req.URL
	if u.Scheme != "https" || u.User != nil || (u.Port() != "" && u.Port() != "443") {
		return errRedirectRefused
	}
	host := strings.ToLower(u.Hostname())
	if net.ParseIP(host) != nil || !validHostname(host) {
		return errRedirectRefused
	}
	return nil
}

// errRedirectRefused: an RDAP redirect broke checkRDAPRedirect's rules.
var errRedirectRefused = errors.New("redirect refused (too many hops, or not https on a hostname and the default port)")

// lookupRDAP queries the TLD's RDAP server for name. A 404 is an
// authoritative "not registered".
func (c *Client) lookupRDAP(ctx context.Context, name string) (*Info, error) {
	bases := c.Directory.RDAPServers(TLD(name))
	if len(bases) == 0 {
		return nil, errNoRDAP
	}
	base := bases[0]
	u, err := url.Parse(base)
	if err != nil {
		return nil, errors.New("invalid RDAP server URL")
	}
	host := strings.ToLower(u.Hostname())
	if err := c.Directory.acquire(host); err != nil {
		return nil, err
	}
	client := c.httpClient()
	defer client.CloseIdleConnections()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, base+"domain/"+url.PathEscape(name), nil)
	if err != nil {
		return nil, errors.New("invalid RDAP server URL")
	}
	req.Header.Set("Accept", "application/rdap+json, application/json")
	resp, err := client.Do(req)
	if err != nil {
		if errors.Is(err, errThrottled) {
			return nil, err
		}
		return nil, lookupError(ctx, err)
	}
	defer resp.Body.Close()
	server := origin(resp.Request.URL)

	switch {
	case resp.StatusCode == http.StatusNotFound && isJSONResponse(resp):
		// Only an RDAP-shaped 404 says "not registered": a plain web 404
		// is a server that no longer serves RDAP here (a stale bootstrap).
		notRegistered := false
		return &Info{Domain: name, Registered: &notRegistered, Source: SourceRDAP, Server: server}, nil
	case resp.StatusCode == http.StatusTooManyRequests:
		c.Directory.coolDown(strings.ToLower(resp.Request.URL.Hostname()), retryAfter(resp.Header, c.now()))
		return nil, fmt.Errorf("%w: rate limited by the registry (HTTP 429)", errThrottled)
	case resp.StatusCode != http.StatusOK:
		return nil, fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxRDAPBytes+1))
	if err != nil {
		return nil, lookupError(ctx, err)
	}
	if len(body) > maxRDAPBytes {
		return nil, errors.New("response exceeds 1 MiB")
	}
	info, err := parseRDAPDomain(body, name, c.now())
	if err != nil {
		return nil, err
	}
	info.Server = server
	return info, nil
}

// isJSONResponse reports an RDAP (application/rdap+json) or JSON body.
func isJSONResponse(resp *http.Response) bool {
	ct := strings.ToLower(resp.Header.Get("Content-Type"))
	return strings.Contains(ct, "application/rdap+json") || strings.Contains(ct, "application/json")
}

// origin is scheme://host of the URL that answered, recorded as the server.
func origin(u *url.URL) string {
	if u == nil || u.Host == "" {
		return ""
	}
	return u.Scheme + "://" + u.Host
}

type rdapDomain struct {
	ObjectClassName string       `json:"objectClassName"`
	Status          []string     `json:"status"`
	Events          []rdapEvent  `json:"events"`
	Entities        []rdapEntity `json:"entities"`
	ErrorCode       int          `json:"errorCode"`
}

type rdapEvent struct {
	Action string `json:"eventAction"`
	Date   string `json:"eventDate"`
}

type rdapEntity struct {
	Roles      []string        `json:"roles"`
	VCardArray json.RawMessage `json:"vcardArray"`
	Entities   []rdapEntity    `json:"entities"`
}

// parseRDAPDomain reads an RFC 9083 domain object: the "expiration" event,
// the registration and last-changed events, the status values and the
// registrar entity's name.
func parseRDAPDomain(body []byte, name string, now time.Time) (*Info, error) {
	var doc rdapDomain
	if err := json.Unmarshal(body, &doc); err != nil {
		return nil, errors.New("invalid RDAP response")
	}
	if doc.ErrorCode == http.StatusNotFound {
		notRegistered := false
		return &Info{Domain: name, Registered: &notRegistered, Source: SourceRDAP}, nil
	}
	if doc.ErrorCode != 0 {
		return nil, fmt.Errorf("RDAP error %d", doc.ErrorCode)
	}
	if doc.ObjectClassName != "domain" {
		return nil, errors.New("RDAP response is not a domain object")
	}
	registered := true
	info := &Info{Domain: name, Registered: &registered, Source: SourceRDAP}
	for _, s := range doc.Status {
		if s = strings.TrimSpace(s); s != "" && len(info.Statuses) < maxStatuses {
			info.Statuses = append(info.Statuses, s)
		}
	}
	for _, ev := range doc.Events {
		at, ok := parseRDAPDate(ev.Date)
		if !ok {
			continue
		}
		switch strings.ToLower(strings.TrimSpace(ev.Action)) {
		case "expiration":
			if info.ExpiresAt == "" {
				info.SetExpiry(at, now)
			}
		case "registration":
			info.RegisteredAt = at.UTC().Format(time.RFC3339)
		case "last changed":
			info.UpdatedAt = at.UTC().Format(time.RFC3339)
		}
	}
	info.Registrar = clip(rdapRegistrar(doc.Entities, 0))
	return info, nil
}

// parseRDAPDate reads an RFC 3339 event date. Some servers drop the zone;
// such a date is read as UTC rather than discarded.
func parseRDAPDate(s string) (time.Time, bool) {
	s = strings.TrimSpace(s)
	for _, layout := range []string{time.RFC3339Nano, "2006-01-02T15:04:05.999999999", "2006-01-02T15:04:05", "2006-01-02"} {
		if at, err := time.Parse(layout, s); err == nil {
			return at, true
		}
	}
	return time.Time{}, false
}

// rdapRegistrar is the vCard "fn" of the first entity with the registrar
// role, searching nested entities a few levels deep.
func rdapRegistrar(entities []rdapEntity, depth int) string {
	if depth > 3 {
		return ""
	}
	for _, e := range entities {
		for _, role := range e.Roles {
			if strings.EqualFold(role, "registrar") {
				if fn := vcardFN(e.VCardArray); fn != "" {
					return fn
				}
			}
		}
	}
	for _, e := range entities {
		if fn := rdapRegistrar(e.Entities, depth+1); fn != "" {
			return fn
		}
	}
	return ""
}

// vcardFN reads the formatted name from a jCard (RFC 7095):
// ["vcard", [["fn", {}, "text", "Name"], …]].
func vcardFN(raw json.RawMessage) string {
	var card []json.RawMessage
	if len(raw) == 0 || json.Unmarshal(raw, &card) != nil || len(card) < 2 {
		return ""
	}
	var props [][]json.RawMessage
	if json.Unmarshal(card[1], &props) != nil {
		return ""
	}
	for _, p := range props {
		if len(p) < 4 {
			continue
		}
		var key, value string
		if json.Unmarshal(p[0], &key) != nil || !strings.EqualFold(key, "fn") {
			continue
		}
		if json.Unmarshal(p[3], &value) == nil {
			return strings.TrimSpace(value)
		}
	}
	return ""
}
