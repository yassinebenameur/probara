package worker

import (
	"bufio"
	"context"
	"encoding/binary"
	"errors"
	"io"
	"math/rand/v2"
	"net"
	"net/netip"
	"os"
	"sort"
	"strings"
	"time"

	"golang.org/x/net/dns/dnsmessage"
)

// The NAPTR step of RFC 3263 §4.1. The standard library has no NAPTR lookup,
// so the worker builds the query itself on golang.org/x/net/dns/dnsmessage
// and reads the records' rdata by hand. Everything here is best effort: when
// no nameserver answers, the check records naptr_status "unavailable" and
// goes on to SRV exactly as if the domain published no NAPTR records.

// sipNAPTR is one NAPTR record (RFC 3403 §4.1). Replacement has no trailing
// dot; "" is the root ("."), which names nothing.
type sipNAPTR struct {
	Order       uint16
	Preference  uint16
	Flags       string
	Services    string
	Regexp      string
	Replacement string
}

const (
	// sipNAPTRBudget bounds the whole NAPTR step, every server and the TCP
	// retry included, so a resolver that ignores NAPTR queries costs the
	// check at most this before SRV is tried.
	sipNAPTRBudget = 1500 * time.Millisecond
	// sipNAPTRMaxServers is how many resolv.conf nameservers are asked.
	sipNAPTRMaxServers = 2
	// sipNAPTRMaxCNAMEs bounds the alias chain followed in one answer.
	sipNAPTRMaxCNAMEs = 8
)

// typeNAPTR is the NAPTR RR type (RFC 3403), which dnsmessage has no
// constant for.
const typeNAPTR = dnsmessage.Type(35)

// sipResolvConfPath is where the worker's own nameservers are read from;
// tests point it elsewhere.
var sipResolvConfPath = "/etc/resolv.conf"

// errSIPNoNameserver is a NAPTR lookup with no server to ask: no nameserver
// configured on the monitor and none in resolv.conf.
var errSIPNoNameserver = errors.New("no nameserver to ask")

// errSIPDNSMismatch is a datagram that is not the answer to our query (other
// ID or question): ignored while the real answer is awaited.
var errSIPDNSMismatch = errors.New("dns: response does not match the query")

// sipNAPTRClient sends NAPTR queries. server pins the nameserver (the
// monitor's, as host:port); empty asks the first sipNAPTRMaxServers
// nameservers of resolv.conf.
//
// Every query is dialed through dialGuard. A pinned nameserver is an
// operator-supplied address and gets the worker's policy as is. The
// resolv.conf nameservers are the worker's own resolver, which every
// hostname lookup in the worker (dialGuard's included) already uses
// unguarded, so they are added to the guard's allowed CIDRs one address at
// a time: a private-IP policy still blocks everything else, and a worker
// whose resolver sits on a private address (a VPC resolver, 127.0.0.53)
// still gets NAPTR answers.
type sipNAPTRClient struct {
	server          string
	blockPrivateIPs bool
	allowedCIDRs    []*net.IPNet
	// dial replaces the guarded dial in tests.
	dial func(ctx context.Context, network, addr string) (net.Conn, error)
}

// LookupNAPTR returns name's NAPTR records, in answer order. An NXDOMAIN is
// a *net.DNSError with IsNotFound set; a name with no NAPTR records is an
// empty slice and no error. Any other error means no server gave an answer.
func (c *sipNAPTRClient) LookupNAPTR(ctx context.Context, name string) ([]sipNAPTR, error) {
	ctx, cancel := context.WithTimeout(ctx, sipNAPTRBudget)
	defer cancel()

	servers, allowed, err := c.servers()
	if err != nil {
		return nil, err
	}
	qname, err := dnsmessage.NewName(fqdn(name))
	if err != nil {
		return nil, &net.DNSError{Err: "invalid name", Name: name}
	}
	dial := c.dial
	if dial == nil {
		guard := newDialGuard(c.blockPrivateIPs, allowed, sipNAPTRBudget)
		dial = guard.DialContext
	}

	var lastErr error
	for i, server := range servers {
		// Each server gets a share of what is left, so a silent first
		// server leaves the second one time to answer.
		attemptCtx, attemptCancel := ctx, context.CancelFunc(func() {})
		if deadline, ok := ctx.Deadline(); ok && i < len(servers)-1 {
			attemptCtx, attemptCancel = context.WithTimeout(ctx, time.Until(deadline)/time.Duration(len(servers)-i))
		}
		records, err := queryNAPTR(attemptCtx, dial, server, qname)
		attemptCancel()
		if err == nil || isDNSNotFound(err) {
			return records, err
		}
		lastErr = err
		if ctx.Err() != nil {
			break
		}
	}
	return nil, lastErr
}

// servers is the nameservers to ask and the CIDRs the guard allows for them.
func (c *sipNAPTRClient) servers() ([]string, []*net.IPNet, error) {
	if c.server != "" {
		return []string{c.server}, c.allowedCIDRs, nil
	}
	servers := readResolvConfNameservers(sipResolvConfPath, sipNAPTRMaxServers)
	if len(servers) == 0 {
		return nil, nil, errSIPNoNameserver
	}
	allowed := append([]*net.IPNet(nil), c.allowedCIDRs...)
	addrs := make([]string, 0, len(servers))
	for _, ip := range servers {
		allowed = append(allowed, &net.IPNet{IP: ip.AsSlice(), Mask: net.CIDRMask(ip.BitLen(), ip.BitLen())})
		addrs = append(addrs, netip.AddrPortFrom(ip, 53).String())
	}
	return addrs, allowed, nil
}

// readResolvConfNameservers returns the first limit "nameserver" addresses of
// a resolv.conf. A scoped IPv6 address (fe80::1%en0) is skipped: the guard
// checks plain IP literals.
func readResolvConfNameservers(path string, limit int) []netip.Addr {
	f, err := os.Open(path)
	if err != nil {
		return nil
	}
	defer f.Close()
	var out []netip.Addr
	scanner := bufio.NewScanner(f)
	for scanner.Scan() && len(out) < limit {
		fields := strings.Fields(scanner.Text())
		if len(fields) < 2 || fields[0] != "nameserver" {
			continue
		}
		addr, err := netip.ParseAddr(fields[1])
		if err != nil || addr.Zone() != "" {
			continue
		}
		out = append(out, addr.Unmap())
	}
	return out
}

func fqdn(name string) string {
	if strings.HasSuffix(name, ".") {
		return name
	}
	return name + "."
}

// queryNAPTR asks one server over UDP and, when the answer is truncated,
// again over TCP.
func queryNAPTR(ctx context.Context, dial func(context.Context, string, string) (net.Conn, error), server string, qname dnsmessage.Name) ([]sipNAPTR, error) {
	id := uint16(rand.Uint32()) //nolint:gosec // a query ID, matched with the question; not a secret
	query, err := buildNAPTRQuery(id, qname)
	if err != nil {
		return nil, err
	}

	records, truncated, err := exchangeNAPTR(ctx, dial, "udp", server, id, qname, query)
	if err != nil || !truncated {
		return records, err
	}
	records, truncated, err = exchangeNAPTR(ctx, dial, "tcp", server, id, qname, query)
	if err == nil && truncated {
		return nil, &net.DNSError{Err: "truncated answer over TCP", Name: qname.String(), Server: server}
	}
	return records, err
}

func buildNAPTRQuery(id uint16, qname dnsmessage.Name) ([]byte, error) {
	b := dnsmessage.NewBuilder(make([]byte, 0, 512), dnsmessage.Header{ID: id, RecursionDesired: true})
	if err := b.StartQuestions(); err != nil {
		return nil, err
	}
	if err := b.Question(dnsmessage.Question{Name: qname, Type: typeNAPTR, Class: dnsmessage.ClassINET}); err != nil {
		return nil, err
	}
	return b.Finish()
}

func exchangeNAPTR(ctx context.Context, dial func(context.Context, string, string) (net.Conn, error), network, server string, id uint16, qname dnsmessage.Name, query []byte) ([]sipNAPTR, bool, error) {
	conn, err := dial(ctx, network, server)
	if err != nil {
		return nil, false, err
	}
	defer conn.Close()
	if deadline, ok := ctx.Deadline(); ok {
		_ = conn.SetDeadline(deadline)
	}
	// Close on cancellation too, so a parent cancel unblocks the read.
	stop := context.AfterFunc(ctx, func() { _ = conn.Close() })
	defer stop()

	if network == "tcp" {
		msg := make([]byte, 2+len(query))
		binary.BigEndian.PutUint16(msg, uint16(len(query)))
		copy(msg[2:], query)
		if _, err := conn.Write(msg); err != nil {
			return nil, false, err
		}
		var length [2]byte
		if _, err := io.ReadFull(conn, length[:]); err != nil {
			return nil, false, err
		}
		resp := make([]byte, binary.BigEndian.Uint16(length[:]))
		if _, err := io.ReadFull(conn, resp); err != nil {
			return nil, false, err
		}
		return parseNAPTRResponse(resp, id, qname, server)
	}

	if _, err := conn.Write(query); err != nil {
		return nil, false, err
	}
	buf := make([]byte, 65535)
	for {
		n, err := conn.Read(buf)
		if err != nil {
			return nil, false, err
		}
		records, truncated, err := parseNAPTRResponse(buf[:n], id, qname, server)
		if errors.Is(err, errSIPDNSMismatch) {
			continue // a stray or spoofed datagram: keep waiting for ours
		}
		return records, truncated, err
	}
}

// parseNAPTRResponse reads the NAPTR records that answer qname, following
// CNAMEs within the answer. truncated reports the TC bit, after which the
// records are not read.
func parseNAPTRResponse(msg []byte, id uint16, qname dnsmessage.Name, server string) ([]sipNAPTR, bool, error) {
	var p dnsmessage.Parser
	h, err := p.Start(msg)
	if err != nil {
		return nil, false, errSIPDNSMismatch
	}
	if h.ID != id || !h.Response {
		return nil, false, errSIPDNSMismatch
	}
	questions, err := p.AllQuestions()
	if err != nil || len(questions) != 1 || questions[0].Type != typeNAPTR ||
		!strings.EqualFold(questions[0].Name.String(), qname.String()) {
		return nil, false, errSIPDNSMismatch
	}
	if h.Truncated {
		return nil, true, nil
	}
	name := strings.TrimSuffix(qname.String(), ".")
	switch h.RCode {
	case dnsmessage.RCodeSuccess:
	case dnsmessage.RCodeNameError:
		return nil, false, &net.DNSError{Err: "no such host", Name: name, Server: server, IsNotFound: true}
	default:
		return nil, false, &net.DNSError{
			Err:         "server answered " + strings.TrimPrefix(h.RCode.String(), "RCode"),
			Name:        name,
			Server:      server,
			IsTemporary: h.RCode == dnsmessage.RCodeServerFailure,
		}
	}

	type owned struct {
		owner string
		rec   sipNAPTR
	}
	var (
		naptrs []owned
		cnames = map[string]string{}
	)
	for {
		rh, err := p.AnswerHeader()
		if errors.Is(err, dnsmessage.ErrSectionDone) {
			break
		}
		if err != nil {
			return nil, false, &net.DNSError{Err: "malformed answer: " + err.Error(), Name: name, Server: server}
		}
		owner := strings.ToLower(rh.Name.String())
		switch {
		case rh.Class != dnsmessage.ClassINET:
			err = p.SkipAnswer()
		case rh.Type == dnsmessage.TypeCNAME:
			var r dnsmessage.CNAMEResource
			r, err = p.CNAMEResource()
			if err == nil {
				cnames[owner] = strings.ToLower(r.CNAME.String())
			}
		case rh.Type == typeNAPTR:
			var r dnsmessage.UnknownResource
			r, err = p.UnknownResource()
			if err == nil {
				// A record whose rdata does not parse is dropped, not
				// the whole answer.
				if rec, perr := parseNAPTRRData(r.Data); perr == nil {
					naptrs = append(naptrs, owned{owner: owner, rec: rec})
				}
			}
		default:
			err = p.SkipAnswer()
		}
		if err != nil {
			return nil, false, &net.DNSError{Err: "malformed answer: " + err.Error(), Name: name, Server: server}
		}
	}

	// The records that answer qname: its own, or those of the name its
	// CNAME chain ends at.
	target := strings.ToLower(qname.String())
	for range sipNAPTRMaxCNAMEs {
		next, ok := cnames[target]
		if !ok {
			break
		}
		target = next
	}
	out := make([]sipNAPTR, 0, len(naptrs))
	for _, n := range naptrs {
		if n.owner == target {
			out = append(out, n.rec)
		}
	}
	return out, false, nil
}

// parseNAPTRRData reads NAPTR rdata (RFC 3403 §4.1): ORDER, PREFERENCE,
// three <character-string>s (FLAGS, SERVICES, REGEXP) and the REPLACEMENT
// domain name, which the RFC forbids compressing.
func parseNAPTRRData(b []byte) (sipNAPTR, error) {
	if len(b) < 4 {
		return sipNAPTR{}, errors.New("naptr: short rdata")
	}
	rec := sipNAPTR{
		Order:      binary.BigEndian.Uint16(b[0:2]),
		Preference: binary.BigEndian.Uint16(b[2:4]),
	}
	off := 4
	var err error
	for _, field := range []*string{&rec.Flags, &rec.Services, &rec.Regexp} {
		if *field, off, err = readDNSCharString(b, off); err != nil {
			return sipNAPTR{}, err
		}
	}
	if rec.Replacement, off, err = readUncompressedDNSName(b, off); err != nil {
		return sipNAPTR{}, err
	}
	if off != len(b) {
		return sipNAPTR{}, errors.New("naptr: trailing rdata")
	}
	return rec, nil
}

func readDNSCharString(b []byte, off int) (string, int, error) {
	if off >= len(b) {
		return "", off, errors.New("naptr: short rdata")
	}
	n := int(b[off])
	off++
	if off+n > len(b) {
		return "", off, errors.New("naptr: short rdata")
	}
	return string(b[off : off+n]), off + n, nil
}

// readUncompressedDNSName reads a wire-format name with no compression
// pointer, returned without the trailing dot ("" for the root).
func readUncompressedDNSName(b []byte, off int) (string, int, error) {
	var labels []string
	total := 0
	for {
		if off >= len(b) {
			return "", off, errors.New("naptr: short replacement")
		}
		n := int(b[off])
		off++
		if n == 0 {
			break
		}
		if n&0xC0 != 0 {
			return "", off, errors.New("naptr: compressed replacement")
		}
		if off+n > len(b) {
			return "", off, errors.New("naptr: short replacement")
		}
		total += n + 1
		if total > 254 {
			return "", off, errors.New("naptr: replacement too long")
		}
		labels = append(labels, string(b[off:off+n]))
		off += n
	}
	return strings.Join(labels, "."), off, nil
}

// sipNAPTRServices maps the RFC 3263 services the worker can use to their
// transport. SIP+D2S (SCTP) and anything else is ignored.
var sipNAPTRServices = map[string]string{
	"SIP+D2U":  "udp",
	"SIP+D2T":  "tcp",
	"SIPS+D2T": "tls",
}

// sipNAPTRTarget is a usable NAPTR record and the transport its service
// names.
type sipNAPTRTarget struct {
	sipNAPTR
	transport string
}

// usableSIPNAPTR keeps the records RFC 3263 §4.1 lets a client follow to SRV
// — flag "s", an empty regexp, a SIP service whose transport the worker
// speaks, and a replacement that is a plausible SRV name — ordered by order,
// then preference. A sips: Request-URI may only use SIPS+D2T; a sip: one may
// use all three, TLS included (RFC 3263 §4.1). OPTIONS and REGISTER always
// send a sip: URI, so their checks pass sipsOnly false.
func usableSIPNAPTR(records []sipNAPTR, sipsOnly bool) []sipNAPTRTarget {
	var out []sipNAPTRTarget
	for _, rec := range records {
		if !strings.EqualFold(rec.Flags, "s") || rec.Regexp != "" || !isSRVName(rec.Replacement) {
			continue
		}
		service := strings.ToUpper(rec.Services)
		transport, ok := sipNAPTRServices[service]
		if !ok || (sipsOnly && service != "SIPS+D2T") {
			continue
		}
		out = append(out, sipNAPTRTarget{sipNAPTR: rec, transport: transport})
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Order != out[j].Order {
			return out[i].Order < out[j].Order
		}
		return out[i].Preference < out[j].Preference
	})
	return out
}

// isSRVName accepts a domain name of letters, digits, '-' and '_' labels
// (SRV owner names start with _service._proto), at least two labels.
func isSRVName(name string) bool {
	labels := strings.Split(name, ".")
	if len(name) == 0 || len(name) > 253 || len(labels) < 2 {
		return false
	}
	for _, label := range labels {
		if len(label) == 0 || len(label) > 63 {
			return false
		}
		for _, c := range label {
			if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '-' || c == '_') {
				return false
			}
		}
	}
	return true
}
