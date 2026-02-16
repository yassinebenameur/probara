package worker

import "testing"

func TestResolveSIPTargetHost(t *testing.T) {
	t.Run("does not rewrite by default", func(t *testing.T) {
		t.Setenv("SIP_LOCALHOST_AS_HOST_GATEWAY", "")
		got := resolveSIPTargetHost("localhost")
		if got != "localhost" {
			t.Fatalf("resolveSIPTargetHost(localhost) = %q, want localhost", got)
		}
	})

	t.Run("rewrites localhost when enabled", func(t *testing.T) {
		t.Setenv("SIP_LOCALHOST_AS_HOST_GATEWAY", "true")
		got := resolveSIPTargetHost("localhost")
		if got != "host.docker.internal" {
			t.Fatalf("resolveSIPTargetHost(localhost) = %q, want host.docker.internal", got)
		}
	})

	t.Run("rewrites 127.0.0.1 when enabled", func(t *testing.T) {
		t.Setenv("SIP_LOCALHOST_AS_HOST_GATEWAY", "1")
		got := resolveSIPTargetHost("127.0.0.1")
		if got != "host.docker.internal" {
			t.Fatalf("resolveSIPTargetHost(127.0.0.1) = %q, want host.docker.internal", got)
		}
	})

	t.Run("rewrites ipv6 loopback when enabled", func(t *testing.T) {
		t.Setenv("SIP_LOCALHOST_AS_HOST_GATEWAY", "yes")
		got := resolveSIPTargetHost("::1")
		if got != "host.docker.internal" {
			t.Fatalf("resolveSIPTargetHost(::1) = %q, want host.docker.internal", got)
		}
	})

	t.Run("does not rewrite non-loopback", func(t *testing.T) {
		t.Setenv("SIP_LOCALHOST_AS_HOST_GATEWAY", "true")
		got := resolveSIPTargetHost("sip.example.com")
		if got != "sip.example.com" {
			t.Fatalf("resolveSIPTargetHost(sip.example.com) = %q, want sip.example.com", got)
		}
	})
}
