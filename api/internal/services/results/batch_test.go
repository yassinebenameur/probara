package results

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/yassinebenameur/probara/api/internal/models"
	sharedmodels "github.com/yassinebenameur/probara/shared/models"
)

func TestGetMonitorResultsBatch_RejectsBadRequestsBeforeQuerying(t *testing.T) {
	// A nil db panics if any of these reach a query.
	svc := &Service{}
	tooMany := make([]uuid.UUID, MaxBatchMonitorIDs+1)
	for i := range tooMany {
		tooMany[i] = uuid.New()
	}
	cases := map[string]struct {
		ids   []uuid.UUID
		limit int
	}{
		"no ids":         {ids: nil, limit: 20},
		"too many ids":   {ids: tooMany, limit: 20},
		"limit too high": {ids: []uuid.UUID{uuid.New()}, limit: MaxBatchResultsLimit + 1},
		"negative limit": {ids: []uuid.UUID{uuid.New()}, limit: -1},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			_, err := svc.GetMonitorResultsBatch(context.Background(), uuid.New(), tc.ids, tc.limit)
			if !errors.Is(err, ErrInvalidBatchRequest) {
				t.Fatalf("err = %v, want ErrInvalidBatchRequest", err)
			}
		})
	}
}

func TestDedupeIDs_KeepsFirstOccurrenceOrder(t *testing.T) {
	a, b := uuid.New(), uuid.New()
	got := dedupeIDs([]uuid.UUID{a, b, a, b, a})
	if len(got) != 2 || got[0] != a || got[1] != b {
		t.Fatalf("dedupeIDs = %v, want [%s %s]", got, a, b)
	}
}

func TestRollUpLeafStatuses_MatchesGroupStatusRule(t *testing.T) {
	cases := []struct {
		name     string
		leaves   int
		statuses []string
		want     string
	}{
		{"no leaves", 0, nil, "unknown"},
		{"leaves never checked", 3, nil, "unknown"},
		{"all success", 2, []string{"success", "success"}, "success"},
		{"all failed", 2, []string{"failure", "error"}, "failure"},
		{"mixed", 3, []string{"success", "failure"}, "degraded"},
		{"unchecked leaf ignored", 3, []string{"success"}, "success"},
	}
	for _, tc := range cases {
		if got := rollUpLeafStatuses(tc.leaves, tc.statuses); got != tc.want {
			t.Errorf("%s: rollUpLeafStatuses = %q, want %q", tc.name, got, tc.want)
		}
	}
}

func TestGroupBatchResults_MergesNewestFirstCapsAndPrependsStatus(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	leafA, leafB := uuid.New(), uuid.New()
	location := uuid.New()
	locationName := "eu-west"
	row := func(at time.Duration, status string) models.CheckResult {
		return models.CheckResult{
			ID: uuid.New(), Status: status, ResultSource: "monitor", CreatedAt: now.Add(-at),
			MetricsData: json.RawMessage(`{"x":1}`), LocationID: &location, LocationName: &locationName,
		}
	}
	rows := map[uuid.UUID][]models.CheckResult{
		leafA: {row(1*time.Minute, "success"), row(3*time.Minute, "success"), row(5*time.Minute, "success")},
		leafB: {row(2*time.Minute, "failure"), row(4*time.Minute, "failure")},
	}

	got := groupBatchResults([]uuid.UUID{leafA, leafB}, rows, 3, "degraded", now)
	if len(got) != 4 {
		t.Fatalf("len = %d, want 3 merged rows + 1 derived", len(got))
	}
	if got[0].Status != "degraded" || got[0].ResultSource != string(sharedmodels.ResultSourceDerived) {
		t.Fatalf("first row = %+v, want the derived group status", got[0])
	}
	wantOrder := []time.Time{now.Add(-1 * time.Minute), now.Add(-2 * time.Minute), now.Add(-3 * time.Minute)}
	for i, want := range wantOrder {
		r := got[i+1]
		if !r.CreatedAt.Equal(want) {
			t.Fatalf("row %d created_at = %v, want %v", i+1, r.CreatedAt, want)
		}
		if r.MetricsData != nil || r.LocationID != nil || r.LocationName != nil {
			t.Fatalf("row %d keeps member detail %+v; group rows carry none", i+1, r)
		}
	}
	// The caller's per-monitor rows are untouched (they are also returned
	// for the members themselves).
	if rows[leafA][0].MetricsData == nil {
		t.Fatalf("groupBatchResults mutated the member's own rows")
	}

	if got := groupBatchResults([]uuid.UUID{leafA}, map[uuid.UUID][]models.CheckResult{}, 3, "success", now); len(got) != 0 {
		t.Fatalf("no member rows: got %d rows, want none (no derived row without results)", len(got))
	}
	if got := groupBatchResults([]uuid.UUID{leafA, leafB}, rows, 3, "unknown", now); len(got) != 3 || got[0].ResultSource == string(sharedmodels.ResultSourceDerived) {
		t.Fatalf("unknown status: got %d rows, want 3 with no derived row", len(got))
	}
}
