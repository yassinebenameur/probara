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
