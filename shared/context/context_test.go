package context

import (
	"context"
	"testing"
)

func TestContextHelpers(t *testing.T) {
	base := context.Background()

	ctx := WithRequestID(base, "req-1")
	ctx = WithJobID(ctx, "job-1")
	ctx = WithTenantID(ctx, "tenant-1")
	ctx = WithAdminID(ctx, "admin-1")

	if value, ok := GetRequestID(ctx); !ok || value != "req-1" {
		t.Fatalf("expected request id req-1, got %q", value)
	}
	if value, ok := GetJobID(ctx); !ok || value != "job-1" {
		t.Fatalf("expected job id job-1, got %q", value)
	}
	if value, ok := GetTenantID(ctx); !ok || value != "tenant-1" {
		t.Fatalf("expected tenant id tenant-1, got %q", value)
	}
	if value, ok := GetAdminID(ctx); !ok || value != "admin-1" {
		t.Fatalf("expected admin id admin-1, got %q", value)
	}
	if !IsAdmin(ctx) {
		t.Fatalf("expected admin to be true")
	}
	if IsAdmin(base) {
		t.Fatalf("expected admin to be false for base context")
	}
}

func TestIsTenantAdmin(t *testing.T) {
	bg := context.Background()
	user := WithAdminID(bg, "u1")
	cases := []struct {
		name string
		ctx  context.Context
		want bool
	}{
		{"anonymous", bg, false},
		{"superadmin", WithPlatformRole(user, "superadmin"), true},
		{"tenant admin", WithRole(user, "admin"), true},
		{"tenant editor", WithRole(user, "editor"), false},
		{"signed in without a role", user, false},
		// API keys never carry an admin ID, whatever role the key maps to.
		{"api key with admin role", WithRole(WithAPIKeyID(bg, "k1"), "admin"), false},
		{"api key with write scope", WithAuthScope(WithAPIKeyID(bg, "k1"), "write"), false},
	}
	for _, c := range cases {
		if got := IsTenantAdmin(c.ctx); got != c.want {
			t.Errorf("%s: IsTenantAdmin() = %v, want %v", c.name, got, c.want)
		}
	}
}

func TestCanWriteScopeNarrowsRoles(t *testing.T) {
	bg := context.Background()
	user := WithAdminID(bg, "u1")
	cases := []struct {
		name string
		ctx  context.Context
		want bool
	}{
		{"superadmin", WithPlatformRole(user, "superadmin"), true},
		{"superadmin with read scope", WithAuthScope(WithPlatformRole(user, "superadmin"), "read"), false},
		{"superadmin with write scope", WithAuthScope(WithPlatformRole(user, "superadmin"), "write"), true},
		{"tenant editor", WithRole(user, "editor"), true},
		{"tenant editor with read scope", WithAuthScope(WithRole(user, "editor"), "read"), false},
		{"tenant viewer with write scope", WithAuthScope(WithRole(user, "viewer"), "write"), false},
		{"api key write", WithAuthScope(WithAPIKeyID(bg, "k1"), "write"), true},
		{"api key read", WithAuthScope(WithAPIKeyID(bg, "k1"), "read"), false},
		{"anonymous", bg, false},
	}
	for _, c := range cases {
		if got := CanWrite(c.ctx); got != c.want {
			t.Errorf("%s: CanWrite() = %v, want %v", c.name, got, c.want)
		}
	}
}
