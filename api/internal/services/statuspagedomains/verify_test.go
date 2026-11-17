package statuspagedomains

import (
	"context"
	"net"
	"strings"
	"sync"
	"testing"
	"time"
)

// fakeResolver answers from maps keyed by absolute names. cnames maps a name
// to the next hop of its CNAME chain; LookupCNAME follows it to the end like
// the real resolver. A name in slow blocks until the lookup's context ends;
// a name in broken fails with a server error.
type fakeResolver struct {
	mu      sync.Mutex
	txt     map[string][]string
	cnames  map[string]string
	exists  map[string]bool // names with address records (chain ends)
	slow    map[string]bool
	broken  map[string]bool
	asked   []string
	lookups int
}

func newFakeResolver() *fakeResolver {
	return &fakeResolver{
		txt:    map[string][]string{},
		cnames: map[string]string{},
		exists: map[string]bool{},
		slow:   map[string]bool{},
		broken: map[string]bool{},
	}
}

func (f *fakeResolver) note(name string) {
	f.mu.Lock()
	f.asked = append(f.asked, name)
	f.lookups++
	f.mu.Unlock()
}

func (f *fakeResolver) LookupTXT(ctx context.Context, name string) ([]string, error) {
	f.note(name)
	if f.slow[name] {
		<-ctx.Done()
		return nil, &net.DNSError{Err: "i/o timeout", Name: name, Server: "10.0.0.10:53", IsTimeout: true}
	}
	if f.broken[name] {
		return nil, &net.DNSError{Err: "server misbehaving", Name: name, Server: "10.0.0.10:53", IsTemporary: true}
	}
	values, ok := f.txt[name]
	if !ok {
		return nil, &net.DNSError{Err: "no such host", Name: name, Server: "10.0.0.10:53", IsNotFound: true}
	}
	return values, nil
}

func (f *fakeResolver) LookupCNAME(ctx context.Context, host string) (string, error) {
	f.note(host)
	if f.slow[host] {
		<-ctx.Done()
		return "", &net.DNSError{Err: "i/o timeout", Name: host, Server: "10.0.0.10:53", IsTimeout: true}
	}
	if f.broken[host] {
		return "", &net.DNSError{Err: "server misbehaving", Name: host, Server: "10.0.0.10:53", IsTemporary: true}
	}
	name := host
	for i := 0; i < 8; i++ {
		next, ok := f.cnames[name]
		if !ok {
			break
		}
		name = next
	}
	if name == host && !f.exists[host] {
		return "", &net.DNSError{Err: "no such host", Name: host, Server: "10.0.0.10:53", IsNotFound: true}
	}
	return name, nil
}

const (
	testHost   = "status.customer.example"
	testToken  = "probara-verify=abc123"
	testTarget = "status.platform.example"
)

func TestCheckDomain(t *testing.T) {
	type setup func(f *fakeResolver)
	txtOK := func(f *fakeResolver) { f.txt["_probara-verify."+testHost+"."] = []string{"other=1", testToken} }
	cnameDirect := func(f *fakeResolver) { f.cnames[testHost+"."] = testTarget + "." }
	cases := []struct {
		name              string
		setup             []setup
		txt, cname        bool
		problemContains   []string
		problemNotContain []string
	}{
		{
			name:  "both records",
			setup: []setup{txtOK, cnameDirect},
			txt:   true, cname: true,
		},
		{
			name:            "TXT missing",
			setup:           []setup{cnameDirect},
			cname:           true,
			problemContains: []string{"TXT record _probara-verify." + testHost + " with this domain's verification value not found"},
		},
		{
			name: "TXT with the wrong value",
			setup: []setup{cnameDirect, func(f *fakeResolver) {
				f.txt["_probara-verify."+testHost+"."] = []string{"probara-verify=someone-else"}
			}},
			cname: true,
			// Reads exactly like a missing record.
			problemContains: []string{"TXT record _probara-verify." + testHost + " with this domain's verification value not found"},
		},
		{
			name: "TXT empty answer",
			setup: []setup{cnameDirect, func(f *fakeResolver) {
				f.txt["_probara-verify."+testHost+"."] = []string{}
			}},
			cname:           true,
			problemContains: []string{"TXT record _probara-verify." + testHost + " with this domain's verification value not found"},
		},
		{
			name:            "CNAME missing, host does not exist",
			setup:           []setup{txtOK},
			txt:             true,
			problemContains: []string{testHost + " does not point at " + testTarget + " (add a CNAME record)"},
		},
		{
			name:            "CNAME missing, host has an A record",
			setup:           []setup{txtOK, func(f *fakeResolver) { f.exists[testHost+"."] = true }},
			txt:             true,
			problemContains: []string{testHost + " does not point at " + testTarget + " (add a CNAME record)"},
		},
		{
			name: "CNAME elsewhere",
			setup: []setup{txtOK, func(f *fakeResolver) {
				f.cnames[testHost+"."] = "old-provider.example."
				f.exists[testTarget+"."] = true
			}},
			txt: true,
			// A name outside the hostname's own domain is never shown: a
			// resolver seeing private zones must not reveal them.
			problemContains:   []string{testHost + " does not point at " + testTarget + " (add a CNAME record)"},
			problemNotContain: []string{"old-provider.example"},
		},
		{
			name: "CNAME to an internal name",
			setup: []setup{txtOK, func(f *fakeResolver) {
				f.cnames[testHost+"."] = "db-primary.corp.internal."
				f.exists[testTarget+"."] = true
			}},
			txt:               true,
			problemNotContain: []string{"corp.internal", "db-primary"},
		},
		{
			name: "CNAME within the customer's own domain is named",
			setup: []setup{txtOK, func(f *fakeResolver) {
				f.cnames[testHost+"."] = "old.customer.example."
				f.exists[testTarget+"."] = true
			}},
			txt:             true,
			problemContains: []string{"its CNAME leads to old.customer.example"},
		},
		{
			name: "chain through the customer's own name",
			setup: []setup{txtOK, func(f *fakeResolver) {
				f.cnames[testHost+"."] = "alias.customer.example."
				f.cnames["alias.customer.example."] = testTarget + "."
			}},
			txt: true, cname: true,
		},
		{
			name: "target itself a CNAME to a load balancer",
			setup: []setup{txtOK, func(f *fakeResolver) {
				f.cnames[testHost+"."] = testTarget + "."
				f.cnames[testTarget+"."] = "lb-123.elb.example."
			}},
			txt: true, cname: true,
		},
		{
			name: "upper-case answer with trailing dot",
			setup: []setup{txtOK, func(f *fakeResolver) {
				f.cnames[testHost+"."] = "STATUS.Platform.Example."
			}},
			txt: true, cname: true,
		},
		{
			name: "server errors never leak the resolver's address",
			setup: []setup{func(f *fakeResolver) {
				f.broken["_probara-verify."+testHost+"."] = true
				f.broken[testHost+"."] = true
			}},
			problemContains:   []string{"DNS lookup failed"},
			problemNotContain: []string{"10.0.0.10", "misbehaving"},
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			f := newFakeResolver()
			for _, s := range c.setup {
				s(f)
			}
			res := checkDomain(context.Background(), f, testHost, testToken, testTarget)
			if res.TXTFound != c.txt || res.CNAMEFound != c.cname {
				t.Fatalf("txt=%v cname=%v, want %v %v (problem %q)", res.TXTFound, res.CNAMEFound, c.txt, c.cname, res.Problem)
			}
			if res.passed() != (c.txt && c.cname) {
				t.Fatalf("passed = %v", res.passed())
			}
			if c.txt && c.cname && res.Problem != "" {
				t.Fatalf("problem on success: %q", res.Problem)
			}
			for _, want := range c.problemContains {
				if !strings.Contains(res.Problem, want) {
					t.Errorf("problem %q lacks %q", res.Problem, want)
				}
			}
			for _, bad := range c.problemNotContain {
				if strings.Contains(res.Problem, bad) {
					t.Errorf("problem %q contains %q", res.Problem, bad)
				}
			}
			for _, name := range f.asked {
				if !strings.HasSuffix(name, ".") {
					t.Errorf("looked up %q, not an absolute name", name)
				}
			}
		})
	}
}

// A resolver that never answers costs one lookup timeout per record, never
// more: the check returns on its own.
func TestCheckDomain_TimesOut(t *testing.T) {
	f := newFakeResolver()
	f.slow["_probara-verify."+testHost+"."] = true
	f.slow[testHost+"."] = true
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	start := time.Now()
	res := checkDomain(ctx, f, testHost, testToken, testTarget)
	if elapsed := time.Since(start); elapsed > 2*time.Second {
		t.Fatalf("check took %v", elapsed)
	}
	if res.passed() || !strings.Contains(res.Problem, "timed out") {
		t.Fatalf("result %+v", res)
	}
}

func TestContainsTokenBounds(t *testing.T) {
	many := make([]string, 0, maxTXTRecords+1)
	for i := 0; i < maxTXTRecords; i++ {
		many = append(many, "noise")
	}
	many = append(many, testToken)
	if containsToken(many, testToken) {
		t.Fatal("read past maxTXTRecords")
	}
	if containsToken([]string{strings.Repeat("x", maxTXTLength+1)}, strings.Repeat("x", maxTXTLength+1)) {
		t.Fatal("matched an over-long value")
	}
	if containsToken([]string{""}, "") {
		t.Fatal("an empty token matched")
	}
	if !containsToken([]string{"  " + testToken + " "}, testToken) {
		t.Fatal("surrounding space defeated the match")
	}
}
