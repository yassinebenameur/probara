package netguard

import (
	"context"
	"errors"
	"net"
	"testing"
	"time"
)

func TestPolicyAllows(t *testing.T) {
	_, office, _ := net.ParseCIDR("10.20.0.0/16")
	block := Policy{BlockPrivate: true, AllowedCIDRs: []*net.IPNet{office}}

	cases := []struct {
		ip     string
		policy Policy
		want   bool
	}{
		{"8.8.8.8", block, true},
		{"169.254.169.254", block, false}, // cloud metadata
		{"127.0.0.1", block, false},
		{"::1", block, false},
		{"10.1.2.3", block, false},
		{"10.20.9.9", block, true}, // allow-listed
		{"fd00::1", block, false},
		{"0.0.0.0", block, false},
		{"169.254.169.254", Policy{}, true}, // zero value allows everything
	}
	for _, tc := range cases {
		if got := tc.policy.Allows(net.ParseIP(tc.ip)); got != tc.want {
			t.Errorf("Allows(%s) with block=%v = %v, want %v", tc.ip, tc.policy.BlockPrivate, got, tc.want)
		}
	}
}

func TestDialerRefusesBlockedAddressWithSentinel(t *testing.T) {
	d := &Dialer{Policy: Policy{BlockPrivate: true}, Timeout: time.Second}
	_, err := d.DialContext(context.Background(), "tcp", "127.0.0.1:9")
	if !errors.Is(err, ErrBlocked) {
		t.Fatalf("err = %v, want ErrBlocked", err)
	}
	if err.Error() != "ssrf_blocked: ip 127.0.0.1 not allowed" {
		t.Fatalf("message = %q; worker check results rely on the ssrf_blocked prefix", err.Error())
	}
}
