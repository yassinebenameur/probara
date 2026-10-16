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

// sipResolver is the DNS a SIP check needs. *net.Resolver implements it;
// tests substitute a fake.
type sipResolver interface {
	LookupSRV(ctx context.Context, service, proto, name string) (string, []*net.SRV, error)
	LookupIPAddr(ctx context.Context, host string) ([]net.IPAddr, error)
}

// sipCandidate is one address a SIP check may send to: always an IP literal,
// so the dial is a literal dial that dialGuard checks against the network
// policy once more, whatever DNS said.
type sipCandidate struct {
	host      string // the name it came from (configured host or SRV target), after the loopback rewrite
	ip        net.IP
	port      int
	transport string // udp, tcp, tls
	via       string // literal, a, srv
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
	// sipMaxResolutionList caps the srv and attempts lists in metrics_data.
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
// otherwise SRV for the configured transport, or for each transport in
// sipAutoTransports until one is published, with each SRV target resolved
// to its addresses; no SRV record falls back to A/AAAA at the default port.
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

	transports := sipAutoTransports
	if transport != "" {
		transports = []string{transport}
	}
	for _, t := range transports {
		service, proto := sipSRVService(t)
		_, records, err := r.LookupSRV(ctx, service, proto, host)
		res.lookedUp = true
		if err != nil {
			if isDNSNotFound(err) {
				continue
			}
			return nil, res, err
		}
		if len(records) == 0 {
			continue
		}
		// A single "." target says the service is decidedly not
		// available at this domain (RFC 2782).
		if len(records) == 1 && strings.TrimSuffix(records[0].Target, ".") == "" {
			return nil, res, &net.DNSError{
				Err:  fmt.Sprintf("SRV _%s._%s says SIP over %s is not offered", service, proto, strings.ToUpper(t)),
				Name: host,
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
			found, err := lookupSIPHost(ctx, r, res, target, int(rec.Port), t, "srv", limit)
			if err != nil {
				// One unresolvable target is a failover, not the end:
				// record it and try the next.
				lastErr = err
				res.addAttempt(sipAttemptMetrics{
					Address:   net.JoinHostPort(target, strconv.Itoa(int(rec.Port))),
					Transport: t,
					Outcome:   "dns",
				})
				continue
			}
			candidates = append(candidates, found...)
		}
		if len(candidates) == 0 {
			if lastErr == nil {
				lastErr = &net.DNSError{Err: "no usable SRV target", Name: host}
			}
			return nil, res, lastErr
		}
		return candidates, res, nil
	}

	// No SRV record: A/AAAA at the transport's default port (RFC 3263 §4.2),
	// UDP when the transport was left to DNS (§4.1).
	if transport == "" {
		transport = "udp"
	}
	candidates, err := lookupSIPHost(ctx, r, res, host, sipDefaultPort(transport), transport, "", sipMaxCandidates)
	return candidates, res, err
}

// lookupSIPHost resolves one target host to at most limit candidates. via is
// "srv" for an SRV target, else "" to derive literal/a from the host.
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
// dialGuard and the worker's network policy too.
func (c *SIPChecker) newSIPResolver(config *models.SIPMonitorConfig, timeout time.Duration) (sipResolver, error) {
	if c.resolver != nil {
		return c.resolver, nil
	}
	nameserver := strings.TrimSpace(config.Nameserver)
	if nameserver == "" || config.ResolutionMode() != models.SIPResolutionRFC3263 {
		return net.DefaultResolver, nil
	}
	addr, err := models.NormalizeNameserverAddr(nameserver)
	if err != nil {
		return nil, fmt.Errorf("invalid nameserver: %w", err)
	}
	guard := newDialGuard(c.blockPrivateIPs, c.allowedCIDRs, timeout)
	return &net.Resolver{
		PreferGo: true,
		Dial: func(ctx context.Context, network, _ string) (net.Conn, error) {
			return guard.DialContext(ctx, network, addr)
		},
	}, nil
}
