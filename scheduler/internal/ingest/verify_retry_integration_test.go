//go:build !unit

package ingest

import (
	"context"
	"testing"

	"github.com/google/uuid"

	"github.com/yassinebenameur/probara/shared/locationauth"
	"github.com/yassinebenameur/probara/shared/models"
	"github.com/yassinebenameur/probara/shared/testutil"
)

// A location result that cannot be verified because the schema is behind
// the code is NAKed, and the redelivery after the migration lands persists
// it. One that fails authentication is acked and dropped.
func TestLocationResultSurvivesASchemaBehindTheCode(t *testing.T) {
	ctx := context.Background()
	dbClient, cleanup := testutil.SetupPostgresDB(ctx, t)
	defer cleanup()

	tenantID := testutil.InsertTenant(ctx, t, dbClient, "verify-retry")
	monitorID := testutil.InsertHTTPMonitor(ctx, t, dbClient, tenantID, "API")
	locationID := insertLocation(ctx, t, dbClient, tenantID, "vpc-a")
	selectLocations(ctx, t, dbClient, monitorID, 1, locationID)
	authorizeMeshSource(ctx, t, dbClient, locationID)
	i := newTestIngest(t, dbClient, nil)

	results := func(jobID string) int {
		t.Helper()
		var n int
		if err := dbClient.QueryRowContext(ctx, `SELECT COUNT(*) FROM check_results WHERE job_id = $1`, jobID).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}

	// Authentication failures: acked, nothing written.
	unknown := locationMessage(tenantID, monitorID, uuid.New(), "success")
	if err := i.handleMessage(ctx, signedMeshQueueMessage(t, unknown)); err != nil {
		t.Fatalf("unknown location: %v, want ack", err)
	}
	forged := locationMessage(tenantID, monitorID, locationID, "success")
	signature, err := locationauth.SignJSON(meshTestCredential, forged)
	if err != nil {
		t.Fatal(err)
	}
	forged.Status = "failure" // the signature covered "success"
	forged.LocationSignature = signature
	msg := testMessage(mustMarshal(t, forged))
	msg.Subject = models.CheckResultSubjectForLocation(models.CheckResultSubject, forged.LocationID)
	if err := i.handleMessage(ctx, msg); err != nil {
		t.Fatalf("forged result: %v, want ack", err)
	}
	if results(forged.JobID) != 0 || results(unknown.JobID) != 0 {
		t.Fatal("an unauthenticated result was persisted")
	}

	// The credential lookup's live-workspace predicate reads
	// tenants.deleted_at; without the column the lookup fails.
	if _, err := dbClient.ExecContext(ctx, `ALTER TABLE tenants RENAME COLUMN deleted_at TO deleted_at_pending`); err != nil {
		t.Fatal(err)
	}
	genuine := locationMessage(tenantID, monitorID, locationID, "success")
	msg = signedMeshQueueMessage(t, genuine)
	msg.NumDelivered = 1
	err = i.handleMessage(ctx, msg)
	if err == nil {
		t.Fatal("a genuine result was acked while the schema was behind")
	}
	if isRejected(err) {
		t.Fatalf("schema error classified as a rejection: %v", err)
	}

	if _, err := dbClient.ExecContext(ctx, `ALTER TABLE tenants RENAME COLUMN deleted_at_pending TO deleted_at`); err != nil {
		t.Fatal(err)
	}
	msg.NumDelivered = 2
	if err := i.handleMessage(ctx, msg); err != nil {
		t.Fatalf("redelivery after the migration: %v", err)
	}
	if got := results(genuine.JobID); got != 1 {
		t.Fatalf("check_results rows for the redelivered result = %d, want 1", got)
	}
}
