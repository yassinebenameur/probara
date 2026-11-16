package domainexpiry

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"strings"
	"time"
)

// WHOIS (RFC 3912) fallback, for registries without RDAP or without an
// expiry date in it. A lookup asks IANA's WHOIS which server holds the TLD
// (cached in the Directory), asks that server, and, when a thin registry's
// answer carries no expiry but names a registrar WHOIS server, asks that one
// too: at most three queries, every one to port 43 of a hostname.
const (
	whoisRootServer  = "whois.iana.org"
	whoisPort        = "43"
	maxWHOISBytes    = 256 << 10
	maxStatuses      = 16
	maxRegistrarRune = 200
)

// whoisQueryFormat is the query some registries need to answer in English
// with expiry fields; every other server gets the bare name.
var whoisQueryFormat = map[string]string{
	"whois.jprs.jp": "%s/e",
}

// lookupWHOIS returns what the TLD's WHOIS server says about name.
func (c *Client) lookupWHOIS(ctx context.Context, name string) (*Info, error) {
	tld := TLD(name)
	server, ok := c.Directory.cachedWHOISServer(tld)
	if !ok {
		root, err := c.whoisQuery(ctx, c.whoisRoot(), tld)
		if err != nil {
			return nil, err
		}
		server = whoisReferral(root, "whois", "refer")
		c.Directory.storeWHOISServer(tld, server)
	}
	if server == "" {
		return nil, errNoWHOIS
	}

	text, err := c.whoisQuery(ctx, server, name)
	if err != nil {
		return nil, err
	}
	info, err := parseWHOIS(text, name, c.now())
	if err != nil {
		if errors.Is(err, errWHOISRefused) {
			c.Directory.coolDown(server, coolDownDefault)
		}
		return nil, err
	}
	info.Server = server
	if info.IsRegistered() && info.ExpiresAt == "" {
		// A thin registry may leave the date to the registrar's server:
		// one more hop, and only to a different, valid hostname.
		if registrar := whoisReferral(text, "registrar whois server"); registrar != "" && registrar != server {
			if more, err := c.whoisQuery(ctx, registrar, name); err == nil {
				if detail, err := parseWHOIS(more, name, c.now()); err == nil && detail.IsRegistered() && detail.ExpiresAt != "" {
					detail.Server = registrar
					if detail.Registrar == "" {
						detail.Registrar = info.Registrar
					}
					return detail, nil
				}
			}
		}
	}
	return info, nil
}

// errWHOISRefused: the server declined to answer (rate limit, block).
var errWHOISRefused = fmt.Errorf("%w: the WHOIS server refused the query (rate limit or block)", errThrottled)

// errNoWHOIS: IANA lists no WHOIS server for the TLD.
var errNoWHOIS = errors.New("no WHOIS service for this TLD")

func (c *Client) whoisRoot() string {
	if c.WHOISRoot != "" {
		return c.WHOISRoot
	}
	return whoisRootServer
}

// whoisQuery sends one query to host:43 and reads the answer, bounded by
// the context deadline and maxWHOISBytes. host is always a validated
// hostname (whoisReferral) or the configured root.
func (c *Client) whoisQuery(ctx context.Context, host, query string) (string, error) {
	if err := c.Directory.acquire(host); err != nil {
		return "", err
	}
	conn, err := c.Dial(ctx, "tcp", net.JoinHostPort(host, whoisPort))
	if err != nil {
		return "", lookupError(ctx, err)
	}
	defer conn.Close()
	if deadline, ok := ctx.Deadline(); ok {
		_ = conn.SetDeadline(deadline)
	}
	if format, ok := whoisQueryFormat[host]; ok {
		query = fmt.Sprintf(format, query)
	}
	if _, err := io.WriteString(conn, query+"\r\n"); err != nil {
		return "", lookupError(ctx, err)
	}
	body, err := io.ReadAll(io.LimitReader(conn, maxWHOISBytes))
	if err != nil && len(body) == 0 {
		return "", lookupError(ctx, err)
	}
	return string(body), nil
}

// whoisReferral returns the first valid hostname named by one of keys
// ("whois:", "refer:", "Registrar WHOIS Server:"), or "". A literal address,
// a port or anything that is not a hostname is ignored: the referral comes
// from a remote server, and following it must not reach an address of its
// choosing.
func whoisReferral(text string, keys ...string) string {
	for _, line := range strings.Split(text, "\n") {
		key, value, ok := whoisField(line)
		if !ok {
			continue
		}
		for _, k := range keys {
			if key != k {
				continue
			}
			host := strings.ToLower(strings.TrimSpace(value))
			host = strings.TrimPrefix(strings.TrimPrefix(host, "whois://"), "rwhois://")
			host = strings.TrimSuffix(host, ".")
			if net.ParseIP(host) == nil && validHostname(host) && strings.Contains(host, ".") {
				return host
			}
		}
	}
	return ""
}

// whoisField splits a "Key: value" or "[Key]   value" line into a
// normalized key (lower-case, single spaces) and the trimmed value. Comment
// and banner lines are skipped.
func whoisField(line string) (string, string, bool) {
	line = strings.TrimSpace(line)
	if line == "" || strings.HasPrefix(line, "%") || strings.HasPrefix(line, "#") || strings.HasPrefix(line, ">>>") {
		return "", "", false
	}
	var key, value string
	if strings.HasPrefix(line, "[") {
		// JPRS style: "[Expires on]   2027/02/28".
		end := strings.Index(line, "]")
		if end < 0 {
			return "", "", false
		}
		key, value = line[1:end], line[end+1:]
	} else {
		var ok bool
		key, value, ok = strings.Cut(line, ":")
		if !ok {
			return "", "", false
		}
	}
	key = strings.Join(strings.Fields(strings.ToLower(key)), " ")
	return key, strings.TrimSpace(value), key != ""
}

// Field names registries use, normalized by whoisField.
var (
	whoisExpiryKeys = map[string]bool{
		"registry expiry date": true, "registrar registration expiration date": true, "expiration date": true,
		"expiry date": true, "expires": true, "expires on": true, "expire date": true, "paid-till": true,
		"expiration time": true, "renewal date": true, "valid until": true, "domain expiration date": true,
		"expiry": true, "expire": true, "domain expires": true, "expire-date": true, "expiration": true,
	}
	whoisCreatedKeys = map[string]bool{
		"creation date": true, "created": true, "registered on": true, "registration time": true,
		"created on": true, "domain registration date": true, "registered": true,
	}
	whoisUpdatedKeys = map[string]bool{
		"updated date": true, "last updated": true, "last-update": true, "changed": true, "last modified": true,
		"modified": true, "last updated on": true,
	}
	whoisRegistrarKeys = map[string]bool{"registrar": true, "sponsoring registrar": true, "registrar name": true}
	whoisStatusKeys    = map[string]bool{"domain status": true, "status": true, "state": true}

	// whoisNotFound are the phrases registries answer an unregistered name
	// with (lower-case substrings).
	whoisNotFound = []string{
		"no match for", "not found", "no data found", "no entries found", "status: free", "status: available",
		"no object found", "is available for registration", "the queried object does not exist", "domain not found",
		"no matching record",
	}
	// whoisRefusal are phrases of a server that declined to answer (rate
	// limit, quota, block). A refusal often also says "not found", so it is
	// checked first and never read as "not registered". They are broad on
	// purpose: they only apply to an answer that carries no registration
	// data, and reading a real "no match" as a refusal costs an unknown
	// expiry, never a false outage.
	whoisRefusal = []string{
		"exceeded", "too many", "try later", "try again", "query rate", "rate limit", "limit", "quota",
		"blocked", "blacklist", "denied", "refused", "temporarily unavailable",
	}
)

// whoisDateLayouts is the date-format table for the common registries, tried
// in order on the whole value, then on its first two fields and its first
// field (so a trailing "(JST)" or "UTC" annotation is ignored).
//
// Slash dates other than year-first are left out on purpose: 03/04/2027 is
// April in one registry and March in another, and a misread date would page
// at the wrong time. Such a registry reads as "expiry unknown" instead.
var whoisDateLayouts = []string{
	time.RFC3339Nano,              // ICANN gTLDs, .fr, .ru: 2027-08-13T04:00:00Z
	"2006-01-02T15:04:05Z0700",    // 2027-08-13T04:00:00+0000
	"2006-01-02T15:04:05",         // no zone: read as UTC
	"2006-01-02 15:04:05Z07:00",   // an offset after a space
	"2006-01-02 15:04:05",         // .cn: 2027-01-01 00:00:00
	"2006-01-02 15:04:05 MST",     // a zone name after the time
	"2006-01-02",                  // .se, .it: 2031-03-09
	"2006/01/02 15:04:05",         // .jp last updated: 2026/03/01 01:05:03
	"2006/01/02",                  // .jp: 2027/02/28
	"2006.01.02",                  // year.month.day
	"02-Jan-2006",                 // .uk: 10-Jun-2027
	"02-January-2006",             // long month name
	"2 January 2006",              // day, month name, year
	"January 2 2006",              // month name, day, year
	"02.01.2006",                  // .pl, .cz: day.month.year
	"20060102",                    // .br: 20270101
	"Mon Jan 2 15:04:05 MST 2006", // date(1) style
	"Mon Jan _2 15:04:05 2006",    // ANSI C style
}

// parseWHOISDate parses the date formats registries use.
func parseWHOISDate(value string) (time.Time, bool) {
	value = strings.TrimSpace(value)
	candidates := []string{value}
	if f := strings.Fields(value); len(f) > 1 {
		candidates = append(candidates, f[0]+" "+f[1], f[0])
	}
	for _, v := range candidates {
		for _, layout := range whoisDateLayouts {
			if at, err := time.Parse(layout, v); err == nil {
				return at, true
			}
		}
	}
	return time.Time{}, false
}

// parseWHOIS reads a registry (or registrar) WHOIS answer, best effort:
// field names and date formats vary by registry.
func parseWHOIS(text, name string, now time.Time) (*Info, error) {
	info := &Info{Domain: name, Source: SourceWHOIS}
	var sawDomain, sawCreated bool
	pendingRegistrar := false
	for _, line := range strings.Split(text, "\n") {
		key, value, ok := whoisField(line)
		if !ok {
			// .uk style: "Registrar:" alone, the name on the next line.
			if pendingRegistrar {
				if v := strings.TrimSpace(line); v != "" {
					if i := strings.Index(v, "[Tag"); i > 0 {
						v = strings.TrimSpace(v[:i])
					}
					if !strings.HasPrefix(strings.ToLower(v), "no registrar") {
						info.Registrar = clip(v)
					}
					pendingRegistrar = false
				}
			}
			continue
		}
		pendingRegistrar = false
		if value == "" {
			pendingRegistrar = whoisRegistrarKeys[key] && info.Registrar == ""
			continue
		}
		switch {
		case whoisExpiryKeys[key]:
			if info.ExpiresAt == "" {
				if at, ok := parseWHOISDate(value); ok {
					info.SetExpiry(at, now)
				}
			}
		case whoisCreatedKeys[key]:
			if at, ok := parseWHOISDate(value); ok && info.RegisteredAt == "" {
				info.RegisteredAt = at.UTC().Format(time.RFC3339)
				sawCreated = true
			}
		case whoisUpdatedKeys[key]:
			if at, ok := parseWHOISDate(value); ok && info.UpdatedAt == "" {
				info.UpdatedAt = at.UTC().Format(time.RFC3339)
			}
		case whoisRegistrarKeys[key]:
			if info.Registrar == "" {
				info.Registrar = clip(value)
			}
		case whoisStatusKeys[key]:
			if f := strings.Fields(value); len(f) > 0 && len(info.Statuses) < maxStatuses {
				info.Statuses = append(info.Statuses, strings.TrimSuffix(f[0], ","))
			}
		case key == "domain name" || key == "domain":
			sawDomain = true
		}
	}
	registered := true
	if info.ExpiresAt != "" || info.Registrar != "" || sawCreated {
		info.Registered = &registered
		return info, nil
	}
	lower := strings.ToLower(text)
	for _, marker := range whoisRefusal {
		if strings.Contains(lower, marker) {
			return nil, errWHOISRefused
		}
	}
	// Some registries echo the queried name above a "free" status, so the
	// not-found phrases are checked before a bare domain line counts.
	for _, marker := range whoisNotFound {
		if strings.Contains(lower, marker) {
			notRegistered := false
			return &Info{Domain: name, Source: SourceWHOIS, Registered: &notRegistered}, nil
		}
	}
	if sawDomain {
		info.Registered = &registered
		return info, nil
	}
	return nil, errors.New("unrecognized WHOIS response")
}

// clip bounds a registry-supplied display string.
func clip(s string) string {
	s = strings.TrimSpace(s)
	if r := []rune(s); len(r) > maxRegistrarRune {
		return string(r[:maxRegistrarRune])
	}
	return s
}
