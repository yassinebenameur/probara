package logger

import (
	"context"
	"testing"

	"github.com/sirupsen/logrus"

	ctxpkg "github.com/yassinebenameur/probara/shared/context"
)

func TestNew_InvalidLevelDefaultsToInfo(t *testing.T) {
	log := New("svc", "not-a-level")
	if log.Level != logrus.InfoLevel {
		t.Fatalf("expected info level, got %v", log.Level)
	}
}

func TestWithContextAddsFields(t *testing.T) {
	log := New("svc", "info")

	ctx := context.Background()
	ctx = ctxpkg.WithRequestID(ctx, "req-1")
	ctx = ctxpkg.WithJobID(ctx, "job-1")
	ctx = ctxpkg.WithTenantID(ctx, "tenant-1")

	entry := log.WithContext(ctx)

	if entry.Data["service"] != "svc" {
		t.Fatalf("expected service field, got %v", entry.Data["service"])
	}
	if entry.Data["request_id"] != "req-1" {
		t.Fatalf("expected request_id field, got %v", entry.Data["request_id"])
	}
	if entry.Data["job_id"] != "job-1" {
		t.Fatalf("expected job_id field, got %v", entry.Data["job_id"])
	}
	if entry.Data["tenant_id"] != "tenant-1" {
		t.Fatalf("expected tenant_id field, got %v", entry.Data["tenant_id"])
	}
}

func TestWithFieldsCopiesMap(t *testing.T) {
	log := New("svc", "info")
	fields := logrus.Fields{"foo": "bar"}

	entry := log.WithFields(fields)

	if entry.Data["foo"] != "bar" {
		t.Fatalf("expected foo field, got %v", entry.Data["foo"])
	}
	if entry.Data["service"] != "svc" {
		t.Fatalf("expected service field, got %v", entry.Data["service"])
	}
	if _, ok := fields["service"]; ok {
		t.Fatalf("expected input fields map to remain unchanged")
	}
}
