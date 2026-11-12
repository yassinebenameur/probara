//go:build !unit

package locations

import (
	"context"
	"testing"

	testcontainers "github.com/testcontainers/testcontainers-go"

	"github.com/yassinebenameur/probara/api/internal/models"
	"github.com/yassinebenameur/probara/shared/testutil"
)

// A second location with a taken name must come back as the caller-facing
// "already exists" message on create and on rename, never as the raw
// unique-index violation.
func TestDuplicateLocationNameIsReported_Integration(t *testing.T) {
	testcontainers.SkipIfProviderIsNotHealthy(t)

	ctx := context.Background()
	dbClient, cleanup := testutil.SetupPostgresDB(ctx, t)
	defer cleanup()

	tenantID := testutil.InsertTenant(ctx, t, dbClient, "tenant-loc-duplicate")
	svc := NewService(dbClient)

	if _, err := svc.Create(ctx, tenantID, &models.CreateLocationRequest{Name: "paris"}); err != nil {
		t.Fatalf("create first location: %v", err)
	}
	other, err := svc.Create(ctx, tenantID, &models.CreateLocationRequest{Name: "lyon"})
	if err != nil {
		t.Fatalf("create second location: %v", err)
	}

	const want = `a location named "paris" already exists`
	if _, err := svc.Create(ctx, tenantID, &models.CreateLocationRequest{Name: "paris"}); err == nil || err.Error() != want {
		t.Fatalf("duplicate create: err = %v, want %q", err, want)
	}
	taken := "paris"
	if _, err := svc.Update(ctx, tenantID, other.ID, &models.UpdateLocationRequest{Name: &taken}); err == nil || err.Error() != want {
		t.Fatalf("rename onto a taken name: err = %v, want %q", err, want)
	}
}
