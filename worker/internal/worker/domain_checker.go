package worker

import (
	"context"
	"crypto/tls"
	"crypto/x509"
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

	"github.com/yassinebenameur/probara/shared/domainexpiry"
	"github.com/yassinebenameur/probara/shared/models"
	"github.com/yassinebenameur/probara/shared/netguard"
)

// Domain registration lookups: RDAP first (RFC 9224 bootstrap, RFC 9083
// responses), WHOIS over TCP 43 as the fallback. Every connection, the IANA
// bootstrap fetch included, dials through dialGuard.
const (
	rdapBootstrapURL      = "https://data.iana.org/rdap/dns.json"
	rdapBootstrapTTL      = 24 * time.Hour
	rdapBootstrapRetry    = time.Hour // after a failed refresh, keep the stale copy this long
	rdapMaxResponseBytes  = 1 << 20
	whoisRootServer       = "whois.iana.org"
	whoisMaxResponseBytes = 256 << 10
	rdapMaxRedirects      = 5
)

// DomainChecker implements Checker for domain monitors (expiry of a domain
// registration). Its verdict: failure only when the domain is expired, in
// redemption or not registered; error when no lookup succeeded. An expiry
// inside min_days_valid is the alerter's domain_expiry alert, not a failure.
type DomainChecker struct {
	blockPrivateIPs bool
	allowedCIDRs    []*net.IPNet
	bootstrap       *rdapBootstrap
	whoisRoot       string
	// whoisAddr maps a WHOIS host to the address dialed (port 43); a test
	// seam, as are rootCAs and now.
	whoisAddr func(host string) string
	rootCAs   *x509.CertPool
	now       func() time.Time
}

// NewDomainChecker creates a domain checker that dials through the worker's
// SSRF policy.
func NewDomainChecker(blockPrivateIPs bool, allowedCIDRs []*net.IPNet) *DomainChecker {
	return &DomainChecker{
		blockPrivateIPs: blockPrivateIPs,
		allowedCIDRs:    allowedCIDRs,
		bootstrap:       defaultRDAPBootstrap,
		whoisRoot:       whoisRootServer,
		whoisAddr:       func(host string) string { return net.JoinHostPort(host, "43") },
		now:             time.Now,
	}
}

// Check looks the domain up and records metrics_data.domain
// (shared/domainexpiry.Info) whenever a registry answered.
func (c *DomainChecker) Check(ctx context.Context, configRaw json.RawMessage, timeoutSeconds int) CheckResult {
	start := time.Now()
	result := func(status, message string, info *domainexpiry.Info) CheckResult {
		latency := time.Since(start).Milliseconds()
		r := CheckResult{Status: status, LatencyMs: &latency}
		if message != "" {
			r.ErrorMessage = &message
		}
		if info != nil {
			r.MetricsData, _ = json.Marshal(map[string]any{"domain": info})
		}
		return r
	}

	var cfg models.DomainMonitorConfig
	if err := json.Unmarshal(configRaw, &cfg); err != nil {
		return result("error", "invalid domain config", nil)
	}
	if err := cfg.Validate(); err != nil {
		return result("error", err.Error(), nil)
	}
	if timeoutSeconds <= 0 {
		return result("error", "timeout must be positive", nil)
	}
	name, _ := cfg.LookupName()
	timeout := time.Duration(timeoutSeconds) * time.Second
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	guard := newDialGuard(c.blockPrivateIPs, c.allowedCIDRs, timeout)
	info, rdapErr := c.lookupRDAP(ctx, guard, timeout, cfg, name)
	if rdapErr == nil && info.Registered && info.ExpiresAt == "" && cfg.WHOISFallbackEnabled() {
		// Some registries publish no expiry over RDAP; their WHOIS may.
		if w, err := c.lookupWHOIS(ctx, guard, name); err == nil && w.Registered && w.ExpiresAt != "" {
			info = w
		}
	}
	if rdapErr != nil {
		if !cfg.WHOISFallbackEnabled() {
			return result("error", "domain: rdap: "+rdapErr.Error(), nil)
		}
		w, whoisErr := c.lookupWHOIS(ctx, guard, name)
		if whoisErr != nil {
			return result("error", fmt.Sprintf("domain: rdap: %v; whois: %v", rdapErr, whoisErr), nil)
		}
		w.RDAPError = rdapErr.Error()
		info = w
	}
	if info.Registered && info.ExpiresAt == "" {
		info.Note = "the registry publishes no expiration date"
	}

	status, message := domainVerdict(info, c.now())
	return result(status, message, info)
}

// domainVerdict decides the check: failure for a name that is not
// registered, past its expiry, or in redemption / pending delete.
func domainVerdict(info *domainexpiry.Info, now time.Time) (string, string) {
	if !info.Registered {
		return "failure", fmt.Sprintf("domain: %s is not registered (%s: not found)", info.Domain, info.Source)
	}
	if info.ExpiresAt != "" {
		if expires, err := time.Parse(time.RFC3339, info.ExpiresAt); err == nil && !now.Before(expires) {
			days := int(now.Sub(expires).Hours() / 24)
			return "failure", fmt.Sprintf("domain: expired %s (%s)", agoPhrase(days), expires.Format("2006-01-02"))
		}
	}
	for _, s := range info.Statuses {
		switch normalizeDomainStatus(s) {
		case "redemptionperiod", "pendingdelete", "pendingrestore":
			return "failure", "domain: expired and " + s
		}
	}
	return "success", ""
}

func agoPhrase(days int) string {
	switch days {
	case 0:
		return "today"
	case 1:
		return "1 day ago"
	default:
		return fmt.Sprintf("%d days ago", days)
	}
}

// normalizeDomainStatus folds RDAP ("redemption period") and EPP
// ("redemptionPeriod") spellings together.
func normalizeDomainStatus(s string) string {
	return strings.NewReplacer(" ", "", "_", "", "-", "").Replace(strings.ToLower(s))
}

// --- lookup errors ------------------------------------------------------------

// lookupError keeps addresses out of the message: it is the check's error
// text, which notifications quote.
func lookupError(ctx context.Context, err error) error {
	switch {
	case errors.Is(err, netguard.ErrBlocked):
		return errors.New("blocked by the worker network policy")
	case ctx.Err() != nil || errors.Is(err, context.DeadlineExceeded):
		return errors.New("timed out")
	default:
		var dnsErr *net.DNSError
		if errors.As(err, &dnsErr) {
			return errors.New("server name did not resolve")
		}
		return errors.New("connection failed")
	}
}

// --- RDAP -------------------------------------------------------------------

func (c *DomainChecker) httpClient(guard *dialGuard, timeout time.Duration) *http.Client {
	transport := &http.Transport{
		DialContext:         guard.DialContext,
		TLSClientConfig:     &tls.Config{RootCAs: c.rootCAs, MinVersion: tls.VersionTLS12},
		TLSHandshakeTimeout: timeout,
		ForceAttemptHTTP2:   true,
	}
	return &http.Client{
		Transport: transport,
		Timeout:   timeout,
		// RDAP servers redirect (registry to registrar, rdap.org style
		// aggregators); every hop dials through the same guard.
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) >= rdapMaxRedirects {
				return errors.New("too many redirects")
			}
			if req.URL.Scheme != "https" && req.URL.Scheme != "http" {
				return errors.New("redirect to an unsupported scheme")
			}
			return nil
		},
	}
}

// lookupRDAP queries the domain's RDAP server: rdap_server_override, else
// the IANA bootstrap entry for its TLD.
func (c *DomainChecker) lookupRDAP(ctx context.Context, guard *dialGuard, timeout time.Duration, cfg models.DomainMonitorConfig, name string) (*domainexpiry.Info, error) {
	client := c.httpClient(guard, timeout)
	defer client.CloseIdleConnections()

	bases := []string{cfg.RDAPServerOverride}
	if cfg.RDAPServerOverride == "" {
		var err error
		bases, err = c.bootstrap.servers(ctx, client, name)
		if err != nil {
			return nil, err
		}
		if len(bases) == 0 {
			return nil, fmt.Errorf("no RDAP service for .%s", name[strings.LastIndex(name, ".")+1:])
		}
	}
	// Prefer https when the registry lists both.
	base := bases[0]
	for _, b := range bases {
		if strings.HasPrefix(strings.ToLower(b), "https://") {
			base = b
			break
		}
	}
	endpoint := strings.TrimRight(base, "/") + "/domain/" + url.PathEscape(name)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, errors.New("invalid RDAP server URL")
	}
	req.Header.Set("Accept", "application/rdap+json, application/json")
	resp, err := client.Do(req)
	if err != nil {
		return nil, lookupError(ctx, err)
	}
	defer resp.Body.Close()
	server := serverLabel(base)
	if resp.StatusCode == http.StatusNotFound {
		return &domainexpiry.Info{Domain: name, Registered: false, Source: "rdap", Server: server}, nil
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, rdapMaxResponseBytes+1))
	if err != nil {
		return nil, lookupError(ctx, err)
	}
	if len(body) > rdapMaxResponseBytes {
		return nil, errors.New("response exceeds 1 MiB")
	}
	info, err := parseRDAPDomain(body, name, c.now())
	if err != nil {
		return nil, err
	}
	info.Server = server
	return info, nil
}

// serverLabel is the scheme and host of an RDAP base URL, recorded as the
// server that answered.
func serverLabel(base string) string {
	u, err := url.Parse(base)
	if err != nil || u.Host == "" {
		return ""
	}
	return u.Scheme + "://" + u.Host
}

type rdapDomainResponse struct {
	ObjectClassName string       `json:"objectClassName"`
	LDHName         string       `json:"ldhName"`
	Status          []string     `json:"status"`
	Events          []rdapEvent  `json:"events"`
	Entities        []rdapEntity `json:"entities"`
	Nameservers     []struct {
		LDHName string `json:"ldhName"`
	} `json:"nameservers"`
	ErrorCode int `json:"errorCode"`
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

// parseRDAPDomain reads an RFC 9083 domain object.
func parseRDAPDomain(body []byte, name string, now time.Time) (*domainexpiry.Info, error) {
	var doc rdapDomainResponse
	if err := json.Unmarshal(body, &doc); err != nil {
		return nil, errors.New("invalid RDAP response")
	}
	if doc.ErrorCode == http.StatusNotFound {
		return &domainexpiry.Info{Domain: name, Registered: false, Source: "rdap"}, nil
	}
	if doc.ErrorCode != 0 {
		return nil, fmt.Errorf("RDAP error %d", doc.ErrorCode)
	}
	if doc.ObjectClassName != "" && doc.ObjectClassName != "domain" {
		return nil, fmt.Errorf("RDAP returned a %q object, not a domain", doc.ObjectClassName)
	}
	info := &domainexpiry.Info{Domain: name, Registered: true, Source: "rdap", Statuses: doc.Status}
	for _, ev := range doc.Events {
		at, err := time.Parse(time.RFC3339, strings.TrimSpace(ev.Date))
		if err != nil {
			continue
		}
		switch strings.ToLower(ev.Action) {
		case "expiration":
			info.SetExpiry(at, now)
		case "registration":
			info.RegisteredAt = at.UTC().Format(time.RFC3339)
		case "last changed":
			info.UpdatedAt = at.UTC().Format(time.RFC3339)
		}
	}
	info.Registrar = rdapRegistrar(doc.Entities)
	for _, ns := range doc.Nameservers {
		if n := strings.TrimSuffix(strings.ToLower(strings.TrimSpace(ns.LDHName)), "."); n != "" {
			info.Nameservers = append(info.Nameservers, n)
		}
	}
	return info, nil
}

// rdapRegistrar is the vCard "fn" of the entity with the registrar role.
func rdapRegistrar(entities []rdapEntity) string {
	for _, e := range entities {
		for _, role := range e.Roles {
			if strings.EqualFold(role, "registrar") {
				if fn := vcardFN(e.VCardArray); fn != "" {
					return fn
				}
			}
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

// rdapBootstrap caches the IANA RDAP bootstrap registry for DNS (RFC 9224)
// in process, refreshed every rdapBootstrapTTL. Shared by every check the
// worker runs.
type rdapBootstrap struct {
	url string

	mu        sync.Mutex
	fetchedAt time.Time
	services  map[string][]string // TLD (or longer suffix) -> base URLs
}

var defaultRDAPBootstrap = &rdapBootstrap{url: rdapBootstrapURL}

// servers returns the RDAP base URLs for name's longest listed suffix, or
// none when the registry lists no service for it. A failed refresh keeps
// serving the previous copy and retries after rdapBootstrapRetry.
func (b *rdapBootstrap) servers(ctx context.Context, client *http.Client, name string) ([]string, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.services == nil || time.Since(b.fetchedAt) > rdapBootstrapTTL {
		services, err := fetchRDAPBootstrap(ctx, client, b.url)
		switch {
		case err == nil:
			b.services, b.fetchedAt = services, time.Now()
		case b.services == nil:
			return nil, fmt.Errorf("bootstrap registry: %w", err)
		default:
			b.fetchedAt = time.Now().Add(rdapBootstrapRetry - rdapBootstrapTTL)
		}
	}
	labels := strings.Split(name, ".")
	for i := 1; i < len(labels); i++ {
		if urls, ok := b.services[strings.Join(labels[i:], ".")]; ok {
			return urls, nil
		}
	}
	return nil, nil
}

func fetchRDAPBootstrap(ctx context.Context, client *http.Client, src string) (map[string][]string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, src, nil)
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
	body, err := io.ReadAll(io.LimitReader(resp.Body, rdapMaxResponseBytes+1))
	if err != nil {
		return nil, lookupError(ctx, err)
	}
	if len(body) > rdapMaxResponseBytes {
		return nil, errors.New("response exceeds 1 MiB")
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
		for _, suffix := range svc[0] {
			services[strings.ToLower(strings.TrimSuffix(suffix, "."))] = svc[1]
		}
	}
	if len(services) == 0 {
		return nil, errors.New("bootstrap registry lists no services")
	}
	return services, nil
}

// --- WHOIS ------------------------------------------------------------------

// lookupWHOIS asks the IANA WHOIS server which server holds the TLD, then
// asks that server. A thin registry's answer that names a registrar WHOIS
// server but carries no expiry is followed one more hop.
func (c *DomainChecker) lookupWHOIS(ctx context.Context, guard *dialGuard, name string) (*domainexpiry.Info, error) {
	tld := name[strings.LastIndex(name, ".")+1:]
	root, err := c.whoisQuery(ctx, guard, c.whoisRoot, tld)
	if err != nil {
		return nil, err
	}
	server := whoisReferral(root, "refer", "whois")
	if server == "" {
		return nil, fmt.Errorf("no WHOIS server for .%s", tld)
	}
	text, err := c.whoisQuery(ctx, guard, server, name)
	if err != nil {
		return nil, err
	}
	info, err := parseWHOIS(text, name, c.now())
	if err != nil {
		return nil, err
	}
	info.Server = server
	if info.Registered && info.ExpiresAt == "" {
		if registrar := whoisReferral(text, "registrar whois server"); registrar != "" && !strings.EqualFold(registrar, server) {
			if more, err := c.whoisQuery(ctx, guard, registrar, name); err == nil {
				if detail, err := parseWHOIS(more, name, c.now()); err == nil && detail.Registered && detail.ExpiresAt != "" {
					detail.Server = registrar
					return detail, nil
				}
			}
		}
	}
	return info, nil
}

func (c *DomainChecker) whoisQuery(ctx context.Context, guard *dialGuard, host, query string) (string, error) {
	conn, err := guard.DialContext(ctx, "tcp", c.whoisAddr(host))
	if err != nil {
		return "", lookupError(ctx, err)
	}
	defer conn.Close()
	if deadline, ok := ctx.Deadline(); ok {
		_ = conn.SetDeadline(deadline)
	}
	if _, err := io.WriteString(conn, query+"\r\n"); err != nil {
		return "", lookupError(ctx, err)
	}
	body, err := io.ReadAll(io.LimitReader(conn, whoisMaxResponseBytes))
	if err != nil && len(body) == 0 {
		return "", lookupError(ctx, err)
	}
	return string(body), nil
}

// whoisReferral returns the first host named by one of keys ("refer:",
// "whois:", "Registrar WHOIS Server:"), or "" when none is a hostname.
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
			host := strings.TrimSuffix(strings.ToLower(value), ".")
			host = strings.TrimPrefix(strings.TrimPrefix(host, "whois://"), "rwhois://")
			if models.IsValidHostname(host) && net.ParseIP(host) == nil {
				return host
			}
		}
	}
	return ""
}

// whoisField splits "Key: value" into a normalized key (lower-case, single
// spaces) and the trimmed value. Comment lines are skipped.
func whoisField(line string) (string, string, bool) {
	line = strings.TrimSpace(line)
	if line == "" || strings.HasPrefix(line, "%") || strings.HasPrefix(line, "#") || strings.HasPrefix(line, ">>>") {
		return "", "", false
	}
	key, value, ok := strings.Cut(line, ":")
	if !ok {
		return "", "", false
	}
	key = strings.Join(strings.Fields(strings.ToLower(key)), " ")
	return key, strings.TrimSpace(value), key != ""
}

var (
	whoisExpiryKeys = map[string]bool{
		"registry expiry date": true, "registrar registration expiration date": true, "expiration date": true,
		"expiry date": true, "expires": true, "expires on": true, "expire date": true, "paid-till": true,
		"expiration time": true, "renewal date": true, "valid until": true, "domain expiration date": true,
		"expiry": true, "expire": true, "domain expires": true,
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
	whoisNSKeys        = map[string]bool{"name server": true, "nserver": true, "nameserver": true, "nameservers": true}

	// whoisNotFound are the phrases registries answer an unregistered name
	// with (lower-case substrings).
	whoisNotFound = []string{
		"no match for", "not found", "no data found", "no entries found", "status: free", "status: available",
		"no object found", "is available for registration", "the queried object does not exist", "domain not found",
		"no matching record",
	}

	whoisDateLayouts = []string{
		time.RFC3339, "2006-01-02T15:04:05Z", "2006-01-02T15:04:05", "2006-01-02 15:04:05", "2006-01-02 15:04:05 MST",
		"2006-01-02 15:04:05-07:00", "2006-01-02", "2006.01.02", "2006/01/02", "02-Jan-2006", "02-January-2006",
		"2 January 2006", "January 2 2006", "02.01.2006", "Mon Jan 2 15:04:05 MST 2006", "2006-01-02 15:04:05 (MST-07:00)",
	}
)

// parseWHOIS reads a registry (or registrar) WHOIS answer, best effort:
// field names and date formats vary by registry.
func parseWHOIS(text, name string, now time.Time) (*domainexpiry.Info, error) {
	info := &domainexpiry.Info{Domain: name, Source: "whois"}
	var sawDomain bool
	for _, line := range strings.Split(text, "\n") {
		key, value, ok := whoisField(line)
		if !ok || value == "" {
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
			}
		case whoisUpdatedKeys[key]:
			if at, ok := parseWHOISDate(value); ok && info.UpdatedAt == "" {
				info.UpdatedAt = at.UTC().Format(time.RFC3339)
			}
		case whoisRegistrarKeys[key]:
			if info.Registrar == "" {
				info.Registrar = value
			}
		case whoisStatusKeys[key]:
			if f := strings.Fields(value); len(f) > 0 && len(info.Statuses) < 16 {
				info.Statuses = append(info.Statuses, f[0])
			}
		case whoisNSKeys[key]:
			if f := strings.Fields(value); len(f) > 0 && len(info.Nameservers) < 16 {
				info.Nameservers = append(info.Nameservers, strings.TrimSuffix(strings.ToLower(f[0]), "."))
			}
		case key == "domain name" || key == "domain":
			sawDomain = true
		}
	}
	if info.ExpiresAt != "" || info.Registrar != "" {
		info.Registered = true
		return info, nil
	}
	// Some registries echo the queried name above a "free" status, so the
	// not-found phrases are checked before a bare domain line counts.
	lower := strings.ToLower(text)
	for _, marker := range whoisNotFound {
		if strings.Contains(lower, marker) {
			info.Statuses = nil
			return info, nil
		}
	}
	if sawDomain {
		info.Registered = true
		return info, nil
	}
	return nil, errors.New("unrecognized WHOIS response")
}

// parseWHOISDate parses the date formats registries use, ignoring a
// trailing annotation such as " (YYYY-MM-DD)".
func parseWHOISDate(value string) (time.Time, bool) {
	value = strings.TrimSpace(value)
	candidates := []string{value}
	if f := strings.Fields(value); len(f) > 0 && f[0] != value {
		candidates = append(candidates, f[0])
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
