package worker

import (
	"net"
	"testing"
	"time"
)

func TestBuildDNSResolver_EmptyUsesSystemDefault(t *testing.T) {
	r, err := buildDNSResolver("", time.Second)
	if err != nil {
		t.Fatalf("buildDNSResolver: %v", err)
	}
	if r != net.DefaultResolver {
		t.Fatal("want net.DefaultResolver")
	}
}

func TestBuildDNSResolver_CustomNameserver(t *testing.T) {
	r, err := buildDNSResolver("10.0.0.2", time.Second)
	if err != nil {
		t.Fatalf("buildDNSResolver: %v", err)
	}
	if r == net.DefaultResolver || !r.PreferGo || r.Dial == nil {
		t.Fatal("want a custom Go resolver with a pinned dial func")
	}
}

func TestNormalizeNameserverAddr(t *testing.T) {
	cases := []struct {
		name       string
		nameserver string
		want       string
		wantErr    bool
	}{
		{"bare host gets port 53", "10.0.0.2", "10.0.0.2:53", false},
		{"host:port kept", "10.0.0.2:5353", "10.0.0.2:5353", false},
		{"hostname", "resolver.internal", "resolver.internal:53", false},
		{"bare ipv6 bracketed", "fd00::2", "[fd00::2]:53", false},
		{"ipv6 with port kept", "[fd00::2]:5353", "[fd00::2]:5353", false},
		{"unbracketed ipv6ish garbage rejected", "a:b:c", "", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := normalizeNameserverAddr(tc.nameserver)
			if (err != nil) != tc.wantErr {
				t.Fatalf("normalizeNameserverAddr(%q) error = %v, wantErr %v", tc.nameserver, err, tc.wantErr)
			}
			if got != tc.want {
				t.Errorf("normalizeNameserverAddr(%q) = %q, want %q", tc.nameserver, got, tc.want)
			}
		})
	}
}
