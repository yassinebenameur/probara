package worker

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/yassinebenameur/probara/shared/testutil"
)

func TestPersistResultAdvancesMonitorState(t *testing.T) {
	ctx := context.Background()
	dbClient, cleanup := testutil.SetupPostgresDB(ctx, t)
	defer cleanup()

	tenantID := testutil.InsertTenant(ctx, t, dbClient, "worker")
	monitorID := testutil.InsertHTTPMonitor(ctx, t, dbClient, tenantID, "API")
	// threshold = 2 is the column default from migration 000044

	w := &Worker{db: dbClient}

	assertState := func(wantState string, wantFails int) {
		t.Helper()
		var state string
		var fails int
		if err := dbClient.QueryRowContext(ctx,
			`SELECT current_state, consecutive_failures FROM monitors WHERE id = $1`,
			monitorID).Scan(&state, &fails); err != nil {
			t.Fatalf("query state: %v", err)
		}
		if state != wantState || fails != wantFails {
			t.Fatalf("state = %s/%d, want %s/%d", state, fails, wantState, wantFails)
		}
	}

	persist := func(status string) {
		t.Helper()
		if err := w.persistResultAndState(ctx, tenantID, monitorID, uuid.New(), &CheckResult{Status: status}, time.Now()); err != nil {
			t.Fatalf("persist %s: %v", status, err)
		}
	}

	persist("failure") // 1st failure -> suspect
	assertState("suspect", 1)
	persist("failure") // 2nd consecutive -> down
	assertState("down", 2)
	persist("failure") // stays down
	assertState("down", 3)
	persist("success") // recovery
	assertState("up", 0)
}
