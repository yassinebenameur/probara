package worker

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"golang.org/x/net/dns/dnsmessage"

	"github.com/yassinebenameur/probara/shared/models"
	"github.com/yassinebenameur/probara/shared/netguard"
)

// naptrRData builds NAPTR rdata by hand, the replacement uncompressed as RFC
// 3403 requires.
func naptrRData(order, pref uint16, flags, services, regexp, replacement string) []byte {
	b := binary.BigEndian.AppendUint16(nil, order)
	b = binary.BigEndian.AppendUint16(b, pref)
	for _, s := range []string{flags, services, regexp} {
		b = append(b, byte(len(s)))
		b = append(b, s...)
	}
	for _, label := range strings.Split(strings.TrimSuffix(replacement, "."), ".") {
		if label == "" {
			continue
		}
		b = append(b, byte(len(label)))
		b = append(b, label...)
	}
	return append(b, 0)
}

type naptrAnswer struct {
	owner string
	cname string // a CNAME record when set
	rdata []byte // else a NAPTR record
}

// naptrResponse builds a DNS response to a NAPTR query for qname.
func naptrResponse(t *testing.T, id uint16, qname string, rcode dnsmessage.RCode, truncated bool, answers ...naptrAnswer) []byte {
	t.Helper()
	mustName := func(s string) dnsmessage.Name {
		n, err := dnsmessage.NewName(fqdn(s))
		if err != nil {
			t.Fatalf("NewName(%q): %v", s, err)
		}
		return n
	}
	b := dnsmessage.NewBuilder(nil, dnsmessage.Header{ID: id, Response: true, RecursionAvailable: true, RCode: rcode, Truncated: truncated})
	if err := b.StartQuestions(); err != nil {
		t.Fatal(err)
	}
	if err := b.Question(dnsmessage.Question{Name: mustName(qname), Type: typeNAPTR, Class: dnsmessage.ClassINET}); err != nil {
		t.Fatal(err)
	}
	if err := b.StartAnswers(); err != nil {
		t.Fatal(err)
	}
	for _, a := range answers {
		if a.cname != "" {
			h := dnsmessage.ResourceHeader{Name: mustName(a.owner), Type: dnsmessage.TypeCNAME, Class: dnsmessage.ClassINET, TTL: 60}
			if err := b.CNAMEResource(h, dnsmessage.CNAMEResource{CNAME: mustName(a.cname)}); err != nil {
				t.Fatal(err)
			}
			continue
		}
		h := dnsmessage.ResourceHeader{Name: mustName(a.owner), Type: typeNAPTR, Class: dnsmessage.ClassINET, TTL: 60}
		if err := b.UnknownResource(h, dnsmessage.UnknownResource{Type: typeNAPTR, Data: a.rdata}); err != nil {
			t.Fatal(err)
		}
	}
	msg, err := b.Finish()
	if err != nil {
		t.Fatal(err)
	}
	return msg
}

// queryOf reads the ID and question name of a NAPTR query.
func queryOf(t *testing.T, msg []byte) (uint16, string) {
	t.Helper()
	var p dnsmessage.Parser
	h, err := p.Start(msg)
	if err != nil {
		t.Errorf("query does not parse: %v", err)
		return 0, ""
	}
	q, err := p.Question()
	if err != nil {
		t.Errorf("query has no question: %v", err)
		return 0, ""
	}
	if q.Type != typeNAPTR || !h.RecursionDesired {
		t.Errorf("query type %v rd %v, want NAPTR with RD", q.Type, h.RecursionDesired)
	}
	return h.ID, q.Name.String()
}

func TestParseNAPTRRData(t *testing.T) {
	rec, err := parseNAPTRRData(naptrRData(10, 20, "s", "SIPS+D2T", "", "_sips._tcp.example.com."))
	if err != nil {
		t.Fatalf("parseNAPTRRData() error = %v", err)
	}
	want := sipNAPTR{Order: 10, Preference: 20, Flags: "s", Services: "SIPS+D2T", Replacement: "_sips._tcp.example.com"}
	if rec != want {
		t.Fatalf("record = %+v, want %+v", rec, want)
	}

	regexp, err := parseNAPTRRData(naptrRData(1, 2, "u", "E2U+sip", "!^.*$!sip:info@example.com!", "."))
	if err != nil || regexp.Regexp != "!^.*$!sip:info@example.com!" || regexp.Replacement != "" {
		t.Fatalf("regexp record = %+v, %v; want the regexp kept and a root replacement", regexp, err)
	}

	valid := naptrRData(10, 20, "s", "SIP+D2U", "", "_sip._udp.example.com")
	compressed := append(append([]byte(nil), valid[:len(valid)-len("_sip._udp.example.com")-2]...), 0xC0, 0x0C)
	for name, b := range map[string][]byte{
		"empty":                  nil,
		"short header":           {0, 10, 0},
		"string past the end":    {0, 10, 0, 20, 5, 's'},
		"replacement unfinished": valid[:len(valid)-1],
		"trailing bytes":         append(append([]byte(nil), valid...), 0),
		"compressed replacement": compressed,
	} {
		if _, err := parseNAPTRRData(b); err == nil {
			t.Errorf("%s: parseNAPTRRData() accepted %x", name, b)
		}
	}
}

func TestUsableSIPNAPTR(t *testing.T) {
	records := []sipNAPTR{
		{Order: 20, Preference: 10, Flags: "s", Services: "SIP+D2U", Replacement: "_sip._udp.example.com"},
		{Order: 10, Preference: 50, Flags: "S", Services: "sip+d2t", Replacement: "_sip._tcp.example.com"},
		{Order: 10, Preference: 10, Flags: "s", Services: "SIPS+D2T", Replacement: "_sips._tcp.example.com"},
		// Dropped: a regexp, a non-terminal flag, a service the worker
		// cannot speak, no replacement, a replacement that is not a name.
		{Order: 1, Preference: 1, Flags: "s", Services: "SIP+D2U", Regexp: "!^.*$!_sip._udp.evil.example!", Replacement: ""},
		{Order: 1, Preference: 1, Flags: "a", Services: "SIP+D2U", Replacement: "sip.example.com"},
		{Order: 1, Preference: 1, Flags: "s", Services: "SIP+D2S", Replacement: "_sip._sctp.example.com"},
		{Order: 1, Preference: 1, Flags: "s", Services: "E2U+sip", Replacement: "_sip._udp.example.com"},
		{Order: 1, Preference: 1, Flags: "s", Services: "SIP+D2U", Replacement: ""},
		{Order: 1, Preference: 1, Flags: "s", Services: "SIP+D2U", Replacement: "bad name.example.com"},
	}
	describe := func(out []sipNAPTRTarget) []string {
		var got []string
		for _, r := range out {
			got = append(got, fmt.Sprintf("%d/%d %s %s", r.Order, r.Preference, r.transport, r.Replacement))
		}
		return got
	}
	// A sip: URI may use every SIP service, SIPS+D2T included.
	requireStrings(t, "sip: URI", describe(usableSIPNAPTR(records, false)), []string{
		"10/10 tls _sips._tcp.example.com",
		"10/50 tcp _sip._tcp.example.com",
		"20/10 udp _sip._udp.example.com",
	})
	// A sips: URI only SIPS+D2T.
	requireStrings(t, "sips: URI", describe(usableSIPNAPTR(records, true)), []string{
		"10/10 tls _sips._tcp.example.com",
	})
}

func TestParseNAPTRResponse(t *testing.T) {
	qname, _ := dnsmessage.NewName("example.com.")
	sips := naptrRData(10, 10, "s", "SIPS+D2T", "", "_sips._tcp.example.com")

	t.Run("records of the name", func(t *testing.T) {
		msg := naptrResponse(t, 7, "example.com", dnsmessage.RCodeSuccess, false,
			naptrAnswer{owner: "example.com", rdata: sips},
			naptrAnswer{owner: "other.example", rdata: naptrRData(1, 1, "s", "SIP+D2U", "", "_sip._udp.other.example")},
			naptrAnswer{owner: "example.com", rdata: []byte{0, 1}}, // malformed: dropped, not fatal
		)
		records, truncated, err := parseNAPTRResponse(msg, 7, qname, "ns")
		if err != nil || truncated || len(records) != 1 || records[0].Replacement != "_sips._tcp.example.com" {
			t.Fatalf("records = %+v truncated %v err %v, want the one record owned by example.com", records, truncated, err)
		}
	})

	t.Run("a CNAME is followed within the answer", func(t *testing.T) {
		msg := naptrResponse(t, 7, "Example.COM", dnsmessage.RCodeSuccess, false,
			naptrAnswer{owner: "example.com", cname: "sip.provider.example"},
			naptrAnswer{owner: "sip.provider.example", rdata: sips},
		)
		records, _, err := parseNAPTRResponse(msg, 7, qname, "ns")
		if err != nil || len(records) != 1 {
			t.Fatalf("records = %+v err %v, want the alias target's record", records, err)
		}
	})

	t.Run("no records", func(t *testing.T) {
		records, _, err := parseNAPTRResponse(naptrResponse(t, 7, "example.com", dnsmessage.RCodeSuccess, false), 7, qname, "ns")
		if err != nil || len(records) != 0 {
			t.Fatalf("records = %+v err %v, want none and no error", records, err)
		}
	})

	t.Run("NXDOMAIN is not found", func(t *testing.T) {
		_, _, err := parseNAPTRResponse(naptrResponse(t, 7, "example.com", dnsmessage.RCodeNameError, false), 7, qname, "ns")
		if !isDNSNotFound(err) {
			t.Fatalf("err = %v, want not found", err)
		}
	})

	t.Run("SERVFAIL is an error", func(t *testing.T) {
		_, _, err := parseNAPTRResponse(naptrResponse(t, 7, "example.com", dnsmessage.RCodeServerFailure, false), 7, qname, "ns")
		if err == nil || isDNSNotFound(err) || !strings.Contains(err.Error(), "ServerFailure") {
			t.Fatalf("err = %v, want a server failure", err)
		}
	})

	t.Run("truncated", func(t *testing.T) {
		_, truncated, err := parseNAPTRResponse(naptrResponse(t, 7, "example.com", dnsmessage.RCodeSuccess, true), 7, qname, "ns")
		if err != nil || !truncated {
			t.Fatalf("truncated = %v err %v, want truncated", truncated, err)
		}
	})

	t.Run("another query's answer is a mismatch", func(t *testing.T) {
		for name, msg := range map[string][]byte{
			"id":       naptrResponse(t, 8, "example.com", dnsmessage.RCodeSuccess, false),
			"question": naptrResponse(t, 7, "other.example", dnsmessage.RCodeSuccess, false),
			"garbage":  []byte("not dns"),
		} {
			if _, _, err := parseNAPTRResponse(msg, 7, qname, "ns"); !errors.Is(err, errSIPDNSMismatch) {
				t.Errorf("%s: err = %v, want a mismatch", name, err)
			}
		}
	})
}

// fakeNAPTRServer answers NAPTR queries over UDP and TCP on one loopback
// port. answer builds the response for a query's ID and name and whether it
// came over TCP; nil sends nothing.
type fakeNAPTRServer struct {
	addr    string
	udpHits atomic.Int32
	tcpHits atomic.Int32
}

func startFakeNAPTRServer(t *testing.T, answer func(id uint16, name string, tcp bool) []byte) *fakeNAPTRServer {
	t.Helper()
	// The OS picks a free UDP port; its TCP twin may be taken on a busy
	// host, so retry with a fresh port rather than skip (CI fails on skips).
	var (
		pc  net.PacketConn
		ln  net.Listener
		err error
	)
	for attempt := 0; attempt < 20; attempt++ {
		pc, err = net.ListenPacket("udp", "127.0.0.1:0")
		if err != nil {
			t.Fatalf("ListenPacket() error = %v", err)
		}
		if ln, err = net.Listen("tcp", pc.LocalAddr().String()); err == nil {
			break
		}
		pc.Close()
	}
	if err != nil {
		t.Fatalf("no loopback port free on both UDP and TCP: %v", err)
	}
	t.Cleanup(func() { pc.Close(); ln.Close() })
	s := &fakeNAPTRServer{addr: pc.LocalAddr().String()}
	go func() {
		buf := make([]byte, 4096)
		for {
			n, from, err := pc.ReadFrom(buf)
			if err != nil {
				return
			}
			s.udpHits.Add(1)
			id, name := queryOf(t, buf[:n])
			if resp := answer(id, name, false); resp != nil {
				_, _ = pc.WriteTo(resp, from)
			}
		}
	}()
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			go func() {
				defer conn.Close()
				var length [2]byte
				if _, err := io.ReadFull(conn, length[:]); err != nil {
					return
				}
				msg := make([]byte, binary.BigEndian.Uint16(length[:]))
				if _, err := io.ReadFull(conn, msg); err != nil {
					return
				}
				s.tcpHits.Add(1)
				id, name := queryOf(t, msg)
				if resp := answer(id, name, true); resp != nil {
					_, _ = conn.Write(binary.BigEndian.AppendUint16(nil, uint16(len(resp))))
					_, _ = conn.Write(resp)
				}
			}()
		}
	}()
	return s
}

func TestSIPNAPTRClient_UDP(t *testing.T) {
	server := startFakeNAPTRServer(t, func(id uint16, name string, _ bool) []byte {
		if name != "example.com." {
			t.Errorf("queried %q, want the fully qualified example.com.", name)
		}
		return naptrResponse(t, id, name, dnsmessage.RCodeSuccess, false,
			naptrAnswer{owner: name, rdata: naptrRData(10, 10, "s", "SIP+D2U", "", "_sip._udp.example.com")})
	})
	client := &sipNAPTRClient{server: server.addr}
	records, err := client.LookupNAPTR(context.Background(), "example.com")
	if err != nil || len(records) != 1 || records[0].Services != "SIP+D2U" {
		t.Fatalf("LookupNAPTR() = %+v, %v", records, err)
	}
	if server.tcpHits.Load() != 0 {
		t.Errorf("TCP queries = %d, want none for an untruncated answer", server.tcpHits.Load())
	}
}

// A truncated UDP answer is asked again over TCP.
func TestSIPNAPTRClient_TruncatedRetriesOverTCP(t *testing.T) {
	server := startFakeNAPTRServer(t, func(id uint16, name string, tcp bool) []byte {
		if !tcp {
			return naptrResponse(t, id, name, dnsmessage.RCodeSuccess, true)
		}
		return naptrResponse(t, id, name, dnsmessage.RCodeSuccess, false,
			naptrAnswer{owner: name, rdata: naptrRData(10, 10, "s", "SIPS+D2T", "", "_sips._tcp.example.com")},
			naptrAnswer{owner: name, rdata: naptrRData(20, 10, "s", "SIP+D2T", "", "_sip._tcp.example.com")})
	})
	records, err := (&sipNAPTRClient{server: server.addr}).LookupNAPTR(context.Background(), "example.com.")
	if err != nil || len(records) != 2 {
		t.Fatalf("LookupNAPTR() = %+v, %v; want both records from the TCP answer", records, err)
	}
	if server.udpHits.Load() != 1 || server.tcpHits.Load() != 1 {
		t.Errorf("udp %d tcp %d queries, want one of each", server.udpHits.Load(), server.tcpHits.Load())
	}
}

// A stray datagram (another ID) is ignored while the real answer is awaited.
func TestSIPNAPTRClient_IgnoresStrayDatagram(t *testing.T) {
	pc, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer pc.Close()
	go func() {
		buf := make([]byte, 4096)
		n, from, err := pc.ReadFrom(buf)
		if err != nil {
			return
		}
		id, name := queryOf(t, buf[:n])
		_, _ = pc.WriteTo(naptrResponse(t, id+1, name, dnsmessage.RCodeNameError, false), from)
		_, _ = pc.WriteTo(naptrResponse(t, id, name, dnsmessage.RCodeSuccess, false,
			naptrAnswer{owner: name, rdata: naptrRData(10, 10, "s", "SIP+D2U", "", "_sip._udp.example.com")}), from)
	}()
	records, err := (&sipNAPTRClient{server: pc.LocalAddr().String()}).LookupNAPTR(context.Background(), "example.com")
	if err != nil || len(records) != 1 {
		t.Fatalf("LookupNAPTR() = %+v, %v; want the matching answer, not the stray NXDOMAIN", records, err)
	}
}

// With no nameserver on the monitor, the first two resolv.conf nameservers
// are asked within the 1.5 s budget: a silent first one leaves the second
// time to answer.
func TestSIPNAPTRClient_ResolvConfServers(t *testing.T) {
	silent, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer silent.Close()
	answering := startFakeNAPTRServer(t, func(id uint16, name string, _ bool) []byte {
		return naptrResponse(t, id, name, dnsmessage.RCodeSuccess, false,
			naptrAnswer{owner: name, rdata: naptrRData(10, 10, "s", "SIP+D2U", "", "_sip._udp.example.com")})
	})

	conf := filepath.Join(t.TempDir(), "resolv.conf")
	if err := os.WriteFile(conf, []byte("# test\nsearch example.org\nnameserver 127.0.0.1\nnameserver fe80::1%en0\nnameserver 127.0.0.2\nnameserver 127.0.0.3\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	old := sipResolvConfPath
	sipResolvConfPath = conf
	t.Cleanup(func() { sipResolvConfPath = old })

	client := &sipNAPTRClient{blockPrivateIPs: true}
	servers, allowed, err := client.servers()
	if err != nil {
		t.Fatalf("servers() error = %v", err)
	}
	// The scoped address is skipped and only two are kept.
	requireStrings(t, "servers", servers, []string{"127.0.0.1:53", "127.0.0.2:53"})
	// The guard lets the worker's own resolvers through even under a
	// private-IP policy, and nothing else.
	policy := netguard.Policy{BlockPrivate: true, AllowedCIDRs: allowed}
	for ip, want := range map[string]bool{"127.0.0.1": true, "127.0.0.2": true, "127.0.0.3": false, "10.0.0.1": false} {
		if got := policy.Allows(net.ParseIP(ip)); got != want {
			t.Errorf("policy allows %s = %v, want %v", ip, got, want)
		}
	}

	var dialed []string
	client.dial = func(ctx context.Context, network, addr string) (net.Conn, error) {
		dialed = append(dialed, network+" "+addr)
		target := silent.LocalAddr().String()
		if addr == "127.0.0.2:53" {
			target = answering.addr
		}
		var d net.Dialer
		return d.DialContext(ctx, network, target)
	}
	start := time.Now()
	records, err := client.LookupNAPTR(context.Background(), "example.com")
	if err != nil || len(records) != 1 {
		t.Fatalf("LookupNAPTR() = %+v, %v; want the second server's answer", records, err)
	}
	if elapsed := time.Since(start); elapsed < 500*time.Millisecond || elapsed > sipNAPTRBudget+200*time.Millisecond {
		t.Errorf("took %s, want about half the %s budget spent on the silent server", elapsed, sipNAPTRBudget)
	}
	requireStrings(t, "dialed", dialed, []string{"udp 127.0.0.1:53", "udp 127.0.0.2:53"})
}

func TestSIPNAPTRClient_Unavailable(t *testing.T) {
	t.Run("no nameserver", func(t *testing.T) {
		old := sipResolvConfPath
		sipResolvConfPath = filepath.Join(t.TempDir(), "missing")
		t.Cleanup(func() { sipResolvConfPath = old })
		if _, err := (&sipNAPTRClient{}).LookupNAPTR(context.Background(), "example.com"); !errors.Is(err, errSIPNoNameserver) {
			t.Fatalf("err = %v, want no nameserver", err)
		}
	})

	t.Run("a silent server gives up within the budget", func(t *testing.T) {
		silent, err := net.ListenPacket("udp", "127.0.0.1:0")
		if err != nil {
			t.Fatal(err)
		}
		defer silent.Close()
		start := time.Now()
		_, err = (&sipNAPTRClient{server: silent.LocalAddr().String()}).LookupNAPTR(context.Background(), "example.com")
		if err == nil || isDNSNotFound(err) {
			t.Fatalf("err = %v, want a timeout", err)
		}
		if elapsed := time.Since(start); elapsed > sipNAPTRBudget+500*time.Millisecond {
			t.Errorf("took %s, want at most the %s budget", elapsed, sipNAPTRBudget)
		}
	})
}

// --- the NAPTR step inside resolveSIPTargets ---

func TestResolveSIPTargets_NAPTR(t *testing.T) {
	t.Setenv("SIP_LOCALHOST_AS_HOST_GATEWAY", "")
	auto := &models.SIPMonitorConfig{Host: "example.com", Resolution: "rfc3263"}
	ips := map[string][]string{
		"tls.example.com": {"192.0.2.13"},
		"tcp.example.com": {"192.0.2.12"},
		"udp.example.com": {"192.0.2.11"},
	}
	srv := map[string][]*net.SRV{
		"_sips._tcp.example.com": {srvRecord("tls.example.com", 5061, 10, 0)},
		"_sip._tcp.example.com":  {srvRecord("tcp.example.com", 5060, 10, 0)},
		"_sip._udp.example.com":  {srvRecord("udp.example.com", 5060, 10, 0)},
	}

	t.Run("order then preference picks the SRV name, SIPS+D2T usable for a sip: URI", func(t *testing.T) {
		r := &fakeSIPResolver{
			naptr: map[string][]sipNAPTR{"example.com": {
				{Order: 20, Preference: 10, Flags: "s", Services: "SIP+D2U", Replacement: "_sip._udp.example.com"},
				{Order: 10, Preference: 20, Flags: "s", Services: "SIP+D2T", Replacement: "_sip._tcp.example.com"},
				{Order: 10, Preference: 10, Flags: "s", Services: "SIPS+D2T", Replacement: "_sips._tcp.example.com"},
			}},
			srv: srv, ips: ips,
		}
		candidates, res, err := resolveSIPTargets(context.Background(), auto, r)
		if err != nil {
			t.Fatalf("resolveSIPTargets() error = %v", err)
		}
		requireStrings(t, "candidates", candidateAddrs(candidates), []string{"naptr tls.example.com 192.0.2.13:5061/tls"})
		requireStrings(t, "queries", r.recorded(), []string{"NAPTR example.com", "SRV _sips._tcp.example.com", "A tls.example.com"})
		if res.NAPTRStatus != "ok" || len(res.NAPTR) != 3 || res.NAPTR[0].Service != "SIPS+D2T" ||
			res.NAPTR[0].Replacement != "_sips._tcp.example.com" || res.NAPTR[2].Order != 20 {
			t.Errorf("naptr metrics = %q %+v, want ok with the three records in order", res.NAPTRStatus, res.NAPTR)
		}
		if len(res.SRV) != 1 || res.SRV[0].Target != "tls.example.com" {
			t.Errorf("srv metrics = %+v, want the SIPS record used", res.SRV)
		}
	})

	t.Run("regexp records are dropped", func(t *testing.T) {
		r := &fakeSIPResolver{
			naptr: map[string][]sipNAPTR{"example.com": {
				{Order: 1, Preference: 1, Flags: "s", Services: "SIP+D2T", Regexp: "!^.*$!_sip._tcp.evil.example!", Replacement: ""},
				{Order: 2, Preference: 1, Flags: "s", Services: "SIP+D2U", Replacement: "_sip._udp.example.com"},
			}},
			srv: srv, ips: ips,
		}
		candidates, res, err := resolveSIPTargets(context.Background(), auto, r)
		if err != nil {
			t.Fatalf("resolveSIPTargets() error = %v", err)
		}
		requireStrings(t, "candidates", candidateAddrs(candidates), []string{"naptr udp.example.com 192.0.2.11:5060/udp"})
		if len(res.NAPTR) != 1 {
			t.Errorf("naptr metrics = %+v, want only the usable record", res.NAPTR)
		}
	})

	t.Run("an unpublished replacement moves to the next record", func(t *testing.T) {
		r := &fakeSIPResolver{
			naptr: map[string][]sipNAPTR{"example.com": {
				{Order: 10, Preference: 10, Flags: "s", Services: "SIPS+D2T", Replacement: "_sips._tcp.gone.example.com"},
				{Order: 20, Preference: 10, Flags: "s", Services: "SIP+D2T", Replacement: "_sip._tcp.example.com"},
			}},
			srv: srv, ips: ips,
		}
		candidates, _, err := resolveSIPTargets(context.Background(), auto, r)
		if err != nil {
			t.Fatalf("resolveSIPTargets() error = %v", err)
		}
		requireStrings(t, "candidates", candidateAddrs(candidates), []string{"naptr tcp.example.com 192.0.2.12:5060/tcp"})
	})

	t.Run("records that lead nowhere fall back to the SRV walk without asking twice", func(t *testing.T) {
		r := &fakeSIPResolver{
			naptr: map[string][]sipNAPTR{"other.example": {
				{Order: 10, Preference: 10, Flags: "s", Services: "SIP+D2U", Replacement: "_sip._udp.other.example"},
			}},
			ips: map[string][]string{"other.example": {"192.0.2.9"}},
		}
		candidates, res, err := resolveSIPTargets(context.Background(), &models.SIPMonitorConfig{Host: "other.example", Resolution: "rfc3263"}, r)
		if err != nil {
			t.Fatalf("resolveSIPTargets() error = %v", err)
		}
		requireStrings(t, "candidates", candidateAddrs(candidates), []string{"a other.example 192.0.2.9:5060/udp"})
		requireStrings(t, "queries", r.recorded(), []string{
			"NAPTR other.example", "SRV _sip._udp.other.example", "SRV _sip._tcp.other.example", "SRV _sips._tcp.other.example", "A other.example",
		})
		if res.NAPTRStatus != "ok" {
			t.Errorf("naptr_status = %q, want ok: the lookup worked, its SRV names were not published", res.NAPTRStatus)
		}
	})

	t.Run("only unusable records is none", func(t *testing.T) {
		r := &fakeSIPResolver{
			naptr: map[string][]sipNAPTR{"example.com": {{Order: 1, Flags: "u", Services: "E2U+sip", Regexp: "!^.*$!sip:x@example.com!"}}},
			srv:   srv, ips: ips,
		}
		candidates, res, err := resolveSIPTargets(context.Background(), auto, r)
		if err != nil {
			t.Fatalf("resolveSIPTargets() error = %v", err)
		}
		requireStrings(t, "candidates", candidateAddrs(candidates), []string{"srv udp.example.com 192.0.2.11:5060/udp"})
		if res.NAPTRStatus != "none" || len(res.NAPTR) != 0 {
			t.Errorf("naptr = %q %+v, want none and no records listed", res.NAPTRStatus, res.NAPTR)
		}
	})

	t.Run("no answer is unavailable, and SRV goes ahead", func(t *testing.T) {
		r := &fakeSIPResolver{
			errs: map[string]error{"NAPTR example.com": &net.DNSError{Err: "i/o timeout", Name: "example.com", IsTimeout: true}},
			srv:  srv, ips: ips,
		}
		candidates, res, err := resolveSIPTargets(context.Background(), auto, r)
		if err != nil {
			t.Fatalf("resolveSIPTargets() error = %v, want SRV to decide", err)
		}
		requireStrings(t, "candidates", candidateAddrs(candidates), []string{"srv udp.example.com 192.0.2.11:5060/udp"})
		if res.NAPTRStatus != "unavailable" {
			t.Errorf("naptr_status = %q, want unavailable", res.NAPTRStatus)
		}
	})

	t.Run("a published replacement that fails decides", func(t *testing.T) {
		r := &fakeSIPResolver{
			naptr: map[string][]sipNAPTR{"example.com": {
				{Order: 10, Preference: 10, Flags: "s", Services: "SIPS+D2T", Replacement: "_sips._tcp.example.com"},
			}},
			srv: map[string][]*net.SRV{"_sips._tcp.example.com": {{Target: ".", Port: 0}}},
		}
		_, _, err := resolveSIPTargets(context.Background(), auto, r)
		if err == nil || classifySIPError(err) != "dns" || !strings.Contains(err.Error(), "SRV _sips._tcp.example.com says SIP over TLS is not offered") {
			t.Fatalf("err = %v, want the lone '.' target to decide", err)
		}
	})

	// RFC 3263 §4.1: a NAPTR-led SRV name that withdraws the service or
	// fails gives way to the next record.
	for name, failing := range map[string]struct {
		srv []*net.SRV
		err error
	}{
		"a lone '.' target":  {srv: []*net.SRV{{Target: ".", Port: 0}}},
		"an SRV server fail": {err: &net.DNSError{Err: "server misbehaving", Name: "_sips._tcp.example.com", IsTemporary: true}},
	} {
		t.Run(name+" moves to the next record", func(t *testing.T) {
			r := &fakeSIPResolver{
				naptr: map[string][]sipNAPTR{"example.com": {
					{Order: 10, Preference: 10, Flags: "s", Services: "SIPS+D2T", Replacement: "_sips._tcp.example.com"},
					{Order: 20, Preference: 10, Flags: "s", Services: "SIP+D2U", Replacement: "_sip._udp.example.com"},
				}},
				srv: map[string][]*net.SRV{
					"_sips._tcp.example.com": failing.srv,
					"_sip._udp.example.com":  srv["_sip._udp.example.com"],
				},
				errs: map[string]error{},
				ips:  ips,
			}
			if failing.err != nil {
				r.errs["_sips._tcp.example.com"] = failing.err
			}
			candidates, res, err := resolveSIPTargets(context.Background(), auto, r)
			if err != nil {
				t.Fatalf("resolveSIPTargets() error = %v, want the next record used", err)
			}
			requireStrings(t, "candidates", candidateAddrs(candidates), []string{"naptr udp.example.com 192.0.2.11:5060/udp"})
			if res.NAPTRStatus != "ok" || len(res.SRV) != 1 || res.SRV[0].Target != "udp.example.com" {
				t.Errorf("naptr_status %q srv %+v, want ok and the records of the name used", res.NAPTRStatus, res.SRV)
			}
		})
	}

	t.Run("every NAPTR-led name failing is a dns error, not a fallback to A", func(t *testing.T) {
		r := &fakeSIPResolver{
			naptr: map[string][]sipNAPTR{"example.com": {
				{Order: 10, Preference: 10, Flags: "s", Services: "SIPS+D2T", Replacement: "_sips._tcp.example.com"},
				{Order: 20, Preference: 10, Flags: "s", Services: "SIP+D2U", Replacement: "_sip._udp.example.com"},
			}},
			srv:  map[string][]*net.SRV{"_sips._tcp.example.com": {{Target: ".", Port: 0}}},
			errs: map[string]error{"_sip._udp.example.com": &net.DNSError{Err: "server misbehaving", Name: "_sip._udp.example.com", IsTemporary: true}},
			ips:  map[string][]string{"example.com": {"192.0.2.1"}},
		}
		_, _, err := resolveSIPTargets(context.Background(), auto, r)
		if err == nil || classifySIPError(err) != "dns" || !strings.Contains(err.Error(), "not offered") {
			t.Fatalf("err = %v, want the most preferred record's dns error", err)
		}
		for _, q := range r.recorded() {
			if q == "A example.com" || strings.HasPrefix(q, "SRV _sip._tcp") {
				t.Fatalf("queries = %q, want no fallback past the failed NAPTR records", r.recorded())
			}
		}
	})

	t.Run("an explicit transport skips NAPTR", func(t *testing.T) {
		r := &fakeSIPResolver{naptr: map[string][]sipNAPTR{"example.com": {
			{Order: 1, Flags: "s", Services: "SIPS+D2T", Replacement: "_sips._tcp.example.com"},
		}}, srv: srv, ips: ips}
		config := &models.SIPMonitorConfig{Host: "example.com", Resolution: "rfc3263", Transport: "tcp"}
		_, res, err := resolveSIPTargets(context.Background(), config, r)
		if err != nil {
			t.Fatal(err)
		}
		requireStrings(t, "queries", r.recorded(), []string{"SRV _sip._tcp.example.com", "A tcp.example.com"})
		if res.NAPTRStatus != "skipped" {
			t.Errorf("naptr_status = %q, want skipped", res.NAPTRStatus)
		}
	})
}

// End to end: NAPTR → SRV → a live server; the envelope says how the
// target was found.
func TestSIPChecker_NAPTRToSRV(t *testing.T) {
	t.Setenv("SIP_LOCALHOST_AS_HOST_GATEWAY", "")
	live := startOKTCPSIPServer(t, nil)
	checker := &SIPChecker{resolver: &fakeSIPResolver{
		naptr: map[string][]sipNAPTR{"example.com": {
			{Order: 10, Preference: 10, Flags: "s", Services: "SIP+D2T", Replacement: "_sip._tcp.example.com"},
		}},
		srv: map[string][]*net.SRV{"_sip._tcp.example.com": {srvRecord("sbc.example.com", live, 10, 0)}},
		ips: map[string][]string{"sbc.example.com": {"127.0.0.1"}},
	}}
	result := runSIPCheckWith(t, checker, map[string]interface{}{"host": "example.com", "resolution": "rfc3263"}, 5)
	if result.Status != "success" {
		t.Fatalf("Status = %q (%v), want success", result.Status, result.ErrorMessage)
	}
	m, raw := decodeSIPMetrics(t, result)
	if m.Target == nil || m.Target.ResolvedVia != "naptr" || m.Target.Host != "sbc.example.com" || m.Transport != "tcp" {
		t.Errorf("target = %+v transport %q, want sbc.example.com via naptr over tcp", m.Target, m.Transport)
	}
	resolution, _ := raw["resolution"].(map[string]any)
	naptr, _ := resolution["naptr"].([]any)
	if resolution["naptr_status"] != "ok" || len(naptr) != 1 {
		t.Fatalf("resolution = %v, want naptr_status ok and the record", resolution)
	}
	if rec, _ := naptr[0].(map[string]any); rec["service"] != "SIP+D2T" || rec["replacement"] != "_sip._tcp.example.com" ||
		rec["order"] != float64(10) || rec["pref"] != float64(10) {
		t.Errorf("naptr record = %v", naptr[0])
	}
}
