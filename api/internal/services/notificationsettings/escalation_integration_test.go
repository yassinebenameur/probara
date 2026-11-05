//go:build !unit

package notificationsettings

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"

	"github.com/yassinebenameur/probara/shared/testutil"
)

// The workspace default escalation policy is set, kept when unsent,
// cleared with "", and must be the workspace's own.
func TestDefaultEscalationPolicySetting(t *testing.T) {
	ctx := context.Background()
	dbClient, cleanup := testutil.SetupPostgresDB(ctx, t)
	defer cleanup()
	tenantID := testutil.InsertTenant(ctx, t, dbClient, "ns-policy")
	other := testutil.InsertTenant(ctx, t, dbClient, "ns-other")
	policy, foreign := uuid.New(), uuid.New()
	for id, tenant := range map[uuid.UUID]uuid.UUID{policy: tenantID, foreign: other} {
		if _, err := dbClient.ExecContext(ctx, `INSERT INTO escalation_policies (id, tenant_id, name) VALUES ($1, $2, 'p')`, id, tenant); err != nil {
			t.Fatal(err)
		}
	}
	svc := NewService(dbClient)
	str := func(s string) *string { return &s }

	got, err := svc.Update(ctx, tenantID, UpdateRequest{DefaultEscalationPolicyID: str(policy.String())})
	if err != nil || got.DefaultEscalationPolicyID == nil || *got.DefaultEscalationPolicyID != policy {
		t.Fatalf("set default policy: %+v, %v", got, err)
	}
	reminder := 600
	got, err = svc.Update(ctx, tenantID, UpdateRequest{AlertReminderSeconds: &reminder})
	if err != nil || got.DefaultEscalationPolicyID == nil {
		t.Fatalf("an update without the field cleared the default policy: %+v, %v", got, err)
	}
	if _, err := svc.Update(ctx, tenantID, UpdateRequest{DefaultEscalationPolicyID: str(foreign.String())}); !errors.Is(err, ErrEscalationPolicyNotFound) {
		t.Fatalf("another workspace's policy: err = %v", err)
	}
	got, err = svc.Update(ctx, tenantID, UpdateRequest{DefaultEscalationPolicyID: str("")})
	if err != nil || got.DefaultEscalationPolicyID != nil {
		t.Fatalf("clear default policy: %+v, %v", got, err)
	}
}
