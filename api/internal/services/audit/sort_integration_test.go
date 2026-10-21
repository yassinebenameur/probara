//go:build !unit

package audit

import (
	"context"
	"testing"
	"time"

	"github.com/yassinebenameur/probara/shared/testutil"
)

func TestListSorts(t *testing.T) {
	if testing.Short() {
		t.Skip("integration test requires Docker")
	}
	ctx := context.Background()
	dbClient, cleanup := testutil.SetupPostgresDB(ctx, t)
	defer cleanup()

	tenantID := testutil.InsertTenant(ctx, t, dbClient, "audit-sort")
	base := time.Now().Add(-time.Hour).Truncate(time.Second)
	insert := func(offset time.Duration, action, resourceType, resourceID, outcome string) int64 {
		t.Helper()
		var id int64
		if err := dbClient.QueryRowContext(ctx, `
			INSERT INTO audit_log (occurred_at, tenant_id, actor_type, action, resource_type, resource_id, outcome)
			VALUES ($1, $2, 'admin_user', $3, $4, $5, $6) RETURNING id
		`, base.Add(offset), tenantID, action, resourceType, resourceID, outcome).Scan(&id); err != nil {
			t.Fatal(err)
		}
		return id
	}
	// Two events share a timestamp, so the id tiebreaker decides them.
	a := insert(0, "monitor.update", "monitor", "m-2", "success")
	b := insert(time.Minute, "alert.acknowledge", "alert", "a-1", "failure")
	c := insert(time.Minute, "monitor.create", "monitor", "m-1", "denied")
	d := insert(2*time.Minute, "alert.resolve", "alert", "a-2", "success")

	svc := NewService(dbClient)
	list := func(sort string, desc bool, page, size int) []int64 {
		t.Helper()
		resp, err := svc.List(ctx, tenantID, false, Filters{Sort: sort, Desc: desc, Page: page, PageSize: size})
		if err != nil {
			t.Fatal(err)
		}
		ids := make([]int64, len(resp.Items))
		for i, e := range resp.Items {
			ids[i] = e.ID
		}
		return ids
	}
	equal := func(name string, got, want []int64) {
		t.Helper()
		if len(got) != len(want) {
			t.Fatalf("%s: got %v, want %v", name, got, want)
		}
		for i := range got {
			if got[i] != want[i] {
				t.Fatalf("%s: got %v, want %v", name, got, want)
			}
		}
	}

	equal("default newest first", list("", false, 1, 50), []int64{d, c, b, a})
	equal("oldest first", list(SortOccurredAt, false, 1, 50), []int64{a, b, c, d})
	equal("action A-Z", list(SortAction, false, 1, 50), []int64{b, d, c, a})
	equal("resource Z-A", list(SortResource, true, 1, 50), []int64{a, c, d, b})
	// Lexical: denied, failure, success; the two successes newest first.
	equal("outcome", list(SortOutcome, false, 1, 50), []int64{c, b, d, a})

	// Paging under a sort neither repeats nor drops a row.
	first, second := list(SortOutcome, false, 1, 2), list(SortOutcome, false, 2, 2)
	equal("paged outcome", append(first, second...), []int64{c, b, d, a})
}
