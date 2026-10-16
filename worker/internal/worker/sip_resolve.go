package worker

import (
	"context"
	"errors"
	"fmt"
	"math/rand/v2"
	"net"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/yassinebenameur/probara/shared/models"
)

// sipResolver is the DNS a SIP check needs. sipNetResolver implements it
// (the stdlib for SRV and A/AAAA, sip_naptr.go for NAPTR); tests substitute
// a fake. LookupNAPTR reports NXDOMAIN as a *net.DNSError with IsNotFound,
// and every other error as "no answer".
type sipResolver interface {
	LookupNAPTR(ctx context.Context, name string) ([]sipNAPTR, error)
	LookupSRV(ctx context.Context, service, proto, name string) (string, []*net.SRV, error)
	LookupIPAddr(ctx context.Context, host string) ([]net.IPAddr, error)
}

// sipNetResolver is the production sipResolver.
type sipNetResolver struct {
	*net.Resolver
	naptr *sipNAPTRClient
}

func (r sipNetResolver) LookupNAPTR(ctx context.Context, name string) ([]sipNAPTR, error) {
	return r.naptr.LookupNAPTR(ctx, name)
}

// sipCandidate is one address a SIP check may send to: always an IP literal,
// so the dial is a literal dial that dialGuard checks against the network
// policy once more, whatever DNS said.
type sipCandidate struct {
	host      string // the name it came from (configured host or SRV target), after the loopback rewrite
	ip        net.IP
	port      int
	transport string // udp, tcp, tls
	via       string // literal, a, srv, naptr (an SRV target reached through NAPTR)
}

func (c sipCandidate) address() string {
	return net.JoinHostPort(c.ip.String(), strconv.Itoa(c.port))
}

const (
	// sipMaxCandidates bounds how many addresses one check tries: each
	// failover costs a dial and, under rfc3263, a slice of the budget.
	sipMaxCandidates = 4
	// sipMaxAddressesPerSRVTarget keeps one multi-homed SRV target from
	// using every candidate slot, so the next target is still tried.
	sipMaxAddressesPerSRVTarget = 2
	// sipMaxResolutionList caps the naptr, srv and attempts lists in
	// metrics_data.
	sipMaxResolutionList = 8
)

// sipDefaultPort is RFC 3261's default port for a transport.
func sipDefaultPort(transport string) int {
	if transport == "tls" {
		return 5061
	}
	return 5060
}

// sipSRVService maps a transport to its SRV service and protocol labels
// (RFC 3263 §4.1): _sip._udp, _sip._tcp, _sips._tcp.
func sipSRVService(transport string) (service, proto string) {
	switch transport {
	case "tcp":
		return "sip", "tcp"
	case "tls":
		return "sips", "tcp"
	default:
		return "sip", "udp"
	}
}

// naptr_status values (metrics_data.sip.resolution) under rfc3263.
const (
	sipNAPTRStatusOK          = "ok"          // usable NAPTR records were found
	sipNAPTRStatusNone        = "none"        // no NAPTR record, or none a SIP client can use
	sipNAPTRStatusUnavailable = "unavailable" // no nameserver answered: SRV went ahead without it
	sipNAPTRStatusSkipped     = "skipped"     // an IP host, or a port or transport was set
)

// sipAutoTransports is the order SRV is queried in when transport is left to
// DNS. RFC 3263 lets the client choose among the transports the domain
// publishes; UDP first matches SIP's default transport (and RFC 3263's own
// fallback when nothing is published), and leaves TLS, whose certificate may
// name the SRV target instead of the domain, to monitors that ask for it.
var sipAutoTransports = []string{"udp", "tcp", "tls"}

// resolveSIPTargets turns the configured host into the ordered addresses a
// check tries. resolution "a" (the default) keeps the pre-RFC 3263
// behaviour: an IP literal is used as is, a hostname's A/AAAA records are
// tried in order at the configured or default port. resolution "rfc3263"
// follows RFC 3263 §4: an IP literal or an explicit port means A/AAAA only;
// otherwise, with the transport left to DNS, the domain's NAPTR records
// (§4.1) are followed to the first SRV name they lead to that yields a usable
// target; failing that (or with a configured transport) SRV for the
// configured transport, or for each transport in sipAutoTransports until one
// yields a usable target, with each SRV target resolved to its addresses; no
// SRV record falls back to A/AAAA at the default port. A published SRV name
// that yields nothing gives way to the next name (sipSRVWalk), and when every
// published name failed the first failure is the error, never a fallback to
// A/AAAA past what DNS said. A NAPTR lookup that gets no
// answer is recorded and skipped, never a failure: SRV still decides.
//
// The SIP_LOCALHOST_AS_HOST_GATEWAY rewrite applies to each target host
// (the configured one or an SRV target) before its A/AAAA lookup.
//
// The returned metrics are filled even on error, so a check that never
// dialed still says what DNS returned.
func resolveSIPTargets(ctx context.Context, config *models.SIPMonitorConfig, r sipResolver) ([]sipCandidate, *sipResolutionMetrics, error) {
	mode := config.ResolutionMode()
	res := &sipResolutionMetrics{Mode: mode}
	host := strings.TrimSpace(config.Host)
	transport := strings.ToLower(strings.TrimSpace(config.Transport))

	literal := net.ParseIP(strings.Trim(host, "[]")) != nil
	if mode == models.SIPResolutionA || literal || config.Port != 0 {
		if mode == models.SIPResolutionRFC3263 {
			res.NAPTRStatus = sipNAPTRStatusSkipped
		}
		if transport == "" {
			transport = "udp"
		}
		port := config.Port
		if port == 0 {
			port = sipDefaultPort(transport)
		}
		candidates, err := lookupSIPHost(ctx, r, res, host, port, transport, "", sipMaxCandidates)
		return candidates, res, err
	}

	// SRV names already asked through NAPTR, so the transport walk below
	// does not ask them twice.
	queried := map[string]bool{}
	if transport == "" {
		// RFC 3263 §4.1: only a client free to choose the transport
		// consults NAPTR. The probe's Request-URI is sip:, so every SIP
		// service, SIPS+D2T included, is usable.
		candidates, decided, err := resolveSIPNAPTR(ctx, r, res, host, false, queried)
		if decided {
			return candidates, res, err
		}
	} else {
		res.NAPTRStatus = sipNAPTRStatusSkipped
	}

	transports := sipAutoTransports
	if transport != "" {
		transports = []string{transport}
	}
	walk := sipSRVWalk{res: res}
	for _, t := range transports {
		service, proto := sipSRVService(t)
		if queried[sipSRVKey("_"+service+"._"+proto+"."+host)] {
			continue
		}
		if candidates, ok := walk.try(func() ([]sipCandidate, bool, error) {
			return lookupSIPSRV(ctx, r, res, service, proto, host, t, "srv")
		}); ok {
			return candidates, res, nil
		}
	}
	if failed, err := walk.failed(); failed {
		return nil, res, err
	}

	// No SRV record: A/AAAA at the transport's default port (RFC 3263 §4.2),
	// UDP when the transport was left to DNS (§4.1).
	if transport == "" {
		transport = "udp"
	}
	candidates, err := lookupSIPHost(ctx, r, res, host, sipDefaultPort(transport), transport, "", sipMaxCandidates)
	return candidates, res, err
}

// resolveSIPNAPTR is the NAPTR step (RFC 3263 §4.1): the usable records, in
// order and preference, each followed to the SRV name it replaces the domain
// with, until one yields a usable target. decided reports that an SRV answer
// settled the check's candidates, or that every SRV name the records led to
// was published and failed (the first failure is the error); otherwise none
// was published and the caller goes on to the plain SRV walk. A NAPTR lookup
// that gets no answer never fails the check.
func resolveSIPNAPTR(ctx context.Context, r sipResolver, res *sipResolutionMetrics, host string, sipsOnly bool, queried map[string]bool) ([]sipCandidate, bool, error) {
	res.lookedUp = true
	records, err := r.LookupNAPTR(ctx, host)
	if err != nil {
		if isDNSNotFound(err) {
			res.NAPTRStatus = sipNAPTRStatusNone
		} else {
			res.NAPTRStatus = sipNAPTRStatusUnavailable
		}
		return nil, false, nil
	}
	usable := usableSIPNAPTR(records, sipsOnly)
	if len(usable) == 0 {
		res.NAPTRStatus = sipNAPTRStatusNone
		return nil, false, nil
	}
	res.NAPTRStatus = sipNAPTRStatusOK
	for _, rec := range usable {
		if len(res.NAPTR) >= sipMaxResolutionList {
			break
		}
		res.NAPTR = append(res.NAPTR, sipNAPTRMetrics{
			Order:       int(rec.Order),
			Pref:        int(rec.Preference),
			Service:     strings.ToUpper(rec.Services),
			Replacement: rec.Replacement,
		})
	}
	walk := sipSRVWalk{res: res}
	for _, rec := range usable {
		key := sipSRVKey(rec.Replacement)
		if queried[key] {
			continue
		}
		queried[key] = true
		if candidates, ok := walk.try(func() ([]sipCandidate, bool, error) {
			return lookupSIPSRV(ctx, r, res, "", "", rec.Replacement, rec.transport, "naptr")
		}); ok {
			return candidates, true, nil
		}
	}
	failed, err := walk.failed()
	return nil, failed, err
}

// sipSRVWalk tries SRV names in turn (the NAPTR-led names, or one per
// transport). RFC 3263 §4.1 and RFC 3403 §4: a name that is published but
// yields nothing — a lone "." target withdrawing the service, a server
// failure, no target that resolves — rules out that name only, and the walk
// goes on to the next. Only when no name yields candidates does the first
// published failure (the most preferred name's reason) decide; the walk
// never falls through to A/AAAA past it, the same "an SRV server failure is
// a dns error, not a silent fallback" rule as a single name.
type sipSRVWalk struct {
	res      *sipResolutionMetrics
	firstErr error
	firstSRV []sipSRVMetrics
}

// try runs one SRV lookup and reports its candidates when it yielded some.
// res.SRV is reset first, so it lists the records of the name used.
func (w *sipSRVWalk) try(lookup func() ([]sipCandidate, bool, error)) ([]sipCandidate, bool) {
	w.res.SRV = nil
	candidates, decided, err := lookup()
	if decided && err == nil && len(candidates) > 0 {
		return candidates, true
	}
	if decided && w.firstErr == nil {
		if err == nil {
			err = &net.DNSError{Err: "no usable SRV target"}
		}
		w.firstErr, w.firstSRV = err, w.res.SRV
	}
	return nil, false
}

// failed reports that a published name failed and none yielded candidates,
// restoring that name's SRV records for the envelope.
func (w *sipSRVWalk) failed() (bool, error) {
	if w.firstErr == nil {
		return false, nil
	}
	w.res.SRV = w.firstSRV
	return true, w.firstErr
}

func sipSRVKey(name string) string {
	return strings.ToLower(strings.TrimSuffix(name, "."))
}

// lookupSIPSRV asks for one SRV name (service and proto empty: name is the
// full SRV name, as NAPTR gives it) and resolves its targets. decided is
// false only when the name is not published (NXDOMAIN or no records), so the
// caller may try the next name; a published answer decides, with candidates
// or with the error that left none.
func lookupSIPSRV(ctx context.Context, r sipResolver, res *sipResolutionMetrics, service, proto, name, transport, via string) ([]sipCandidate, bool, error) {
	srvName := name
	if service != "" || proto != "" {
		srvName = "_" + service + "._" + proto + "." + name
	}
	_, records, err := r.LookupSRV(ctx, service, proto, name)
	res.lookedUp = true
	// The stdlib drops records whose target is not a valid name and
	// returns an error alongside the rest: the usable records still
	// decide, and only an answer with none left is a failure.
	if err != nil && len(records) == 0 {
		if isDNSNotFound(err) {
			return nil, false, nil
		}
		return nil, true, err
	}
	if len(records) == 0 {
		return nil, false, nil
	}
	// A single "." target says the service is decidedly not available at
	// this domain (RFC 2782).
	if len(records) == 1 && strings.TrimSuffix(records[0].Target, ".") == "" {
		return nil, true, &net.DNSError{
			Err:  fmt.Sprintf("SRV %s says SIP over %s is not offered", srvName, strings.ToUpper(transport)),
			Name: name,
		}
	}
	ordered := orderSRVRecords(records)
	for _, rec := range ordered {
		if len(res.SRV) >= sipMaxResolutionList {
			break
		}
		res.SRV = append(res.SRV, sipSRVMetrics{
			Target:   strings.TrimSuffix(rec.Target, "."),
			Port:     int(rec.Port),
			Priority: int(rec.Priority),
			Weight:   int(rec.Weight),
		})
	}
	var (
		candidates []sipCandidate
		lastErr    error
	)
	for _, rec := range ordered {
		if len(candidates) >= sipMaxCandidates {
			break
		}
		target := strings.TrimSuffix(rec.Target, ".")
		if target == "" {
			continue
		}
		limit := min(sipMaxAddressesPerSRVTarget, sipMaxCandidates-len(candidates))
		found, err := lookupSIPHost(ctx, r, res, target, int(rec.Port), transport, via, limit)
		if err != nil {
			// One unresolvable target is a failover, not the end: record
			// it and try the next.
			lastErr = err
			res.addAttempt(sipAttemptMetrics{
				Address:   net.JoinHostPort(target, strconv.Itoa(int(rec.Port))),
				Transport: transport,
				Outcome:   "dns",
			})
			continue
		}
		candidates = append(candidates, found...)
	}
	if len(candidates) == 0 {
		if lastErr == nil {
			lastErr = &net.DNSError{Err: "no usable SRV target", Name: name}
		}
		return nil, true, lastErr
	}
	return candidates, true, nil
}

// lookupSIPHost resolves one target host to at most limit candidates. via is
// "srv" or "naptr" for an SRV target, else "" to derive literal/a from the
// host.
func lookupSIPHost(ctx context.Context, r sipResolver, res *sipResolutionMetrics, host string, port int, transport, via string, limit int) ([]sipCandidate, error) {
	host = resolveSIPTargetHost(host)
	if ip := net.ParseIP(strings.Trim(host, "[]")); ip != nil {
		if via == "" {
			via = "literal"
		}
		return []sipCandidate{{host: host, ip: ip, port: port, transport: transport, via: via}}, nil
	}
	if via == "" {
		via = "a"
	}
	res.lookedUp = true
	addrs, err := r.LookupIPAddr(ctx, host)
	if err != nil {
		return nil, err
	}
	if len(addrs) == 0 {
		return nil, &net.DNSError{Err: "no addresses", Name: host, IsNotFound: true}
	}
	candidates := make([]sipCandidate, 0, min(len(addrs), limit))
	for _, addr := range addrs {
		if len(candidates) >= limit {
			break
		}
		candidates = append(candidates, sipCandidate{host: host, ip: addr.IP, port: port, transport: transport, via: via})
	}
	return candidates, nil
}

func isDNSNotFound(err error) bool {
	var dnsErr *net.DNSError
	return errors.As(err, &dnsErr) && dnsErr.IsNotFound
}

// orderSRVRecords sorts by priority, then orders each priority's records by
// RFC 2782's weighted random selection. The stdlib resolver already does
// this; doing it here too keeps the order right whatever resolver answered.
// Zero-weight records go first within a priority and, among themselves,
// keep their order, so an all-zero set is deterministic.
func orderSRVRecords(records []*net.SRV) []*net.SRV {
	sorted := make([]*net.SRV, 0, len(records))
	for _, rec := range records {
		if rec != nil {
			sorted = append(sorted, rec)
		}
	}
	sort.SliceStable(sorted, func(i, j int) bool { return sorted[i].Priority < sorted[j].Priority })

	out := make([]*net.SRV, 0, len(sorted))
	for start := 0; start < len(sorted); {
		end := start
		for end < len(sorted) && sorted[end].Priority == sorted[start].Priority {
			end++
		}
		group := append([]*net.SRV(nil), sorted[start:end]...)
		sort.SliceStable(group, func(i, j int) bool { return group[i].Weight == 0 && group[j].Weight != 0 })
		for len(group) > 0 {
			total := 0
			for _, rec := range group {
				total += int(rec.Weight)
			}
			pick := 0
			if total > 0 {
				n := rand.IntN(total + 1) //nolint:gosec // load spreading, not security
				sum := 0
				for i, rec := range group {
					sum += int(rec.Weight)
					if sum >= n {
						pick = i
						break
					}
				}
			}
			out = append(out, group[pick])
			group = append(group[:pick], group[pick+1:]...)
		}
		start = end
	}
	return out
}

// newSIPResolver is the resolver for a check: the worker's own, or a Go
// resolver pinned to the configured nameserver. The nameserver is an
// operator-supplied address like any target, so its connections go through
// dialGuard and the worker's network policy too. NAPTR queries
// (sip_naptr.go) go to the same nameserver, or to the worker's resolv.conf
// nameservers when none is set.
func (c *SIPChecker) newSIPResolver(config *models.SIPMonitorConfig, timeout time.Duration) (sipResolver, error) {
	if c.resolver != nil {
		return c.resolver, nil
	}
	naptr := &sipNAPTRClient{blockPrivateIPs: c.blockPrivateIPs, allowedCIDRs: c.allowedCIDRs}
	nameserver := strings.TrimSpace(config.Nameserver)
	if nameserver == "" || config.ResolutionMode() != models.SIPResolutionRFC3263 {
		return sipNetResolver{Resolver: net.DefaultResolver, naptr: naptr}, nil
	}
	addr, err := models.NormalizeNameserverAddr(nameserver)
	if err != nil {
		return nil, fmt.Errorf("invalid nameserver: %w", err)
	}
	naptr.server = addr
	guard := newDialGuard(c.blockPrivateIPs, c.allowedCIDRs, timeout)
	return sipNetResolver{
		Resolver: &net.Resolver{
			PreferGo: true,
			Dial: func(ctx context.Context, network, _ string) (net.Conn, error) {
				return guard.DialContext(ctx, network, addr)
			},
		},
		naptr: naptr,
	}, nil
}
