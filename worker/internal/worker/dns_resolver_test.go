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
