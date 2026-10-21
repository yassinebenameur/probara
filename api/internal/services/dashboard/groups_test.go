package dashboard

import (
	"fmt"
	"strings"
	"testing"

	"github.com/google/uuid"
)

func ptrString(s string) *string { return &s }

func ptrFloat(v float64) *float64 { return &v }

func TestAggregateGroups_NoGroupTags_ReturnsNil(t *testing.T) {
	got := aggregateGroups(nil, nil)
	if got != nil {
		t.Fatalf("expected nil, got %v", got)
	}
	got = aggregateGroups(nil, []string{})
	if got != nil {
		t.Fatalf("expected nil, got %v", got)
	}
}

func TestAggregateGroups_MonitorInTwoGroupsAppearsInBoth(t *testing.T) {
	m := uuid.New()
	rows := []groupAggregationRow{
		{MonitorID: m, Name: "stripe-webhook", Uptime: ptrFloat(91.2), Tags: []string{"api", "payments"}, CurrentStatus: ptrString("failure"), CurrentState: "down"},
	}
	groups := aggregateGroups(rows, []string{"api", "payments"})

	if len(groups) != 2 {
		t.Fatalf("expected api + payments (no ungrouped — monitor matched both), got %d: %+v", len(groups), groups)
	}

	var apiG, paymentsG *struct {
		count int
		attn  int
	}
	apiG = &struct{ count, attn int }{}
	paymentsG = &struct{ count, attn int }{}
	for _, g := range groups {
		if g.Tag == nil {
			t.Fatal("did not expect ungrouped sentinel")
		}
		switch *g.Tag {
		case "api":
			apiG.count = g.MonitorCount
			apiG.attn = g.AttentionCount
		case "payments":
			paymentsG.count = g.MonitorCount
			paymentsG.attn = g.AttentionCount
		default:
			t.Fatalf("unexpected tag %q", *g.Tag)
		}
	}
	if apiG.count != 1 || paymentsG.count != 1 {
		t.Fatalf("monitor should be counted in both, got api=%d payments=%d", apiG.count, paymentsG.count)
	}
	if apiG.attn != 1 || paymentsG.attn != 1 {
		t.Fatalf("attention should be counted in both, got api=%d payments=%d", apiG.attn, paymentsG.attn)
	}
}

func TestAggregateGroups_UntaggedMonitorGoesToUngrouped(t *testing.T) {
	rows := []groupAggregationRow{
		{MonitorID: uuid.New(), Name: "lone", Uptime: ptrFloat(99.9), Tags: []string{"customer-x"}, CurrentState: "up"},
	}
	groups := aggregateGroups(rows, []string{"api", "payments"})

	var ungrouped *struct{ count int }
	for _, g := range groups {
		if g.Tag == nil {
			ungrouped = &struct{ count int }{count: g.MonitorCount}
		}
	}
	if ungrouped == nil {
		t.Fatal("expected an ungrouped row")
	}
	if ungrouped.count != 1 {
		t.Fatalf("expected 1 ungrouped monitor, got %d", ungrouped.count)
	}
}

func TestAggregateGroups_StaleTagYieldsZeroCount(t *testing.T) {
	rows := []groupAggregationRow{
		{MonitorID: uuid.New(), Name: "x", Uptime: ptrFloat(100), Tags: []string{"api"}, CurrentState: "up"},
	}
	groups := aggregateGroups(rows, []string{"api", "ghost-tag"})

	var ghostCount = -1
	for _, g := range groups {
		if g.Tag != nil && *g.Tag == "ghost-tag" {
			ghostCount = g.MonitorCount
		}
	}
	if ghostCount != 0 {
		t.Fatalf("expected stale ghost-tag group with 0 monitors, got %d", ghostCount)
	}
}

func TestAggregateGroups_WorstMemberSetWhenAttention(t *testing.T) {
	rows := []groupAggregationRow{
		{MonitorID: uuid.New(), Name: "healthy", Uptime: ptrFloat(100), Tags: []string{"api"}, CurrentState: "up"},
		{MonitorID: uuid.New(), Name: "sick", Uptime: ptrFloat(91.2), Tags: []string{"api"}, CurrentStatus: ptrString("failure"), CurrentState: "down"},
	}
	groups := aggregateGroups(rows, []string{"api"})
	if len(groups) != 1 {
		t.Fatalf("expected one group, got %d", len(groups))
	}
	api := groups[0]
	if api.WorstMember == nil {
		t.Fatal("expected WorstMember to be set when AttentionCount > 0")
	}
	if api.WorstMember.MonitorName != "sick" {
		t.Fatalf("expected worst member to be 'sick' (lowest uptime), got %q", api.WorstMember.MonitorName)
	}
	if len(api.Members) != 2 {
		t.Fatalf("expected both members in preview list, got %d", len(api.Members))
	}
	if api.Members[0].MonitorName != "sick" {
		t.Fatalf("expected members sorted worst-first, got %q first", api.Members[0].MonitorName)
	}
}

func TestAggregateGroups_NoAttention_NoWorstMember(t *testing.T) {
	rows := []groupAggregationRow{
		{MonitorID: uuid.New(), Name: "x", Uptime: ptrFloat(100), Tags: []string{"api"}, CurrentState: "up"},
	}
	groups := aggregateGroups(rows, []string{"api"})
	if groups[0].WorstMember != nil {
		t.Fatalf("expected no WorstMember when AttentionCount == 0")
	}
}

// S-D1 (docs/state-semantics.md): a member with no checks contributes no
// uptime — it is not a synthetic 100%, it does not dilute the group mean,
// it never ranks as the worst performer, and a group with no data at all
// reads null rather than a number.
func TestAggregateGroups_NoDataIsNotHundredPercent(t *testing.T) {
	rows := []groupAggregationRow{
		{MonitorID: uuid.New(), Name: "checked", Uptime: ptrFloat(80), Tags: []string{"api"}, CurrentState: "down", CurrentStatus: ptrString("failure")},
		{MonitorID: uuid.New(), Name: "paused", Uptime: nil, Tags: []string{"api"}, CurrentState: "up"},
		{MonitorID: uuid.New(), Name: "new", Uptime: nil, Tags: []string{"empty"}, CurrentState: "up"},
	}
	groups := aggregateGroups(rows, []string{"api", "empty"})

	for _, g := range groups {
		switch *g.Tag {
		case "api":
			if g.MonitorCount != 2 {
				t.Fatalf("api group should count both members, got %d", g.MonitorCount)
			}
			if g.Uptime == nil || *g.Uptime != 80 {
				t.Fatalf("api group uptime should be mean over data-bearing members (80), got %v", g.Uptime)
			}
			if g.Members[0].MonitorName != "checked" {
				t.Fatalf("no-data member must not rank worst, got %q first", g.Members[0].MonitorName)
			}
			if g.Members[1].Uptime != nil {
				t.Fatalf("paused member uptime should stay nil, got %v", *g.Members[1].Uptime)
			}
		case "empty":
			if g.Uptime != nil {
				t.Fatalf("group with no data must have nil uptime, got %v", *g.Uptime)
			}
		default:
			t.Fatalf("unexpected tag %q", *g.Tag)
		}
	}
}

// A group's attention is its members' CURRENT state, never failed checks
// earlier in the range: on 30d/90d almost every member has some failure, and
// counting those turned every group amber at 99.9% with every member up. The
// uptime figure already carries the history.
func TestAggregateGroups_HistoricalFailuresAloneNeedNoAttention(t *testing.T) {
	rows := []groupAggregationRow{
		// Failed checks in the range (uptime < 100, latest check failed), up now.
		{MonitorID: uuid.New(), Name: "flapped", Uptime: ptrFloat(99.93), Tags: []string{"api"}, CurrentStatus: ptrString("failure"), CurrentState: "up"},
		{MonitorID: uuid.New(), Name: "steady", Uptime: ptrFloat(99.99), Tags: []string{"api"}, CurrentStatus: ptrString("success"), CurrentState: "up"},
	}
	groups := aggregateGroups(rows, []string{"api"})
	if got := groups[0].AttentionCount; got != 0 {
		t.Fatalf("AttentionCount = %d, want 0: every member is up now", got)
	}
	if groups[0].WorstMember != nil {
		t.Fatalf("WorstMember = %+v, want nil when nothing needs attention", groups[0].WorstMember)
	}
}

func TestAggregateGroups_CurrentStateDrivesAttentionAndOrder(t *testing.T) {
	rows := []groupAggregationRow{
		{MonitorID: uuid.New(), Name: "low-but-up", Uptime: ptrFloat(80), Tags: []string{"api"}, CurrentState: "up"},
		{MonitorID: uuid.New(), Name: "no-data", Uptime: nil, Tags: []string{"api"}, CurrentState: "unknown"},
		{MonitorID: uuid.New(), Name: "degraded", Uptime: ptrFloat(99), Tags: []string{"api"}, CurrentState: "degraded"},
		{MonitorID: uuid.New(), Name: "suspect", Uptime: ptrFloat(98), Tags: []string{"api"}, CurrentState: "suspect"},
		{MonitorID: uuid.New(), Name: "down-now", Uptime: ptrFloat(99.5), Tags: []string{"api"}, CurrentState: "down"},
	}
	g := aggregateGroups(rows, []string{"api"})[0]
	if g.AttentionCount != 3 {
		t.Fatalf("AttentionCount = %d, want 3 (down + degraded + suspect)", g.AttentionCount)
	}
	if g.WorstMember == nil || g.WorstMember.MonitorName != "down-now" {
		t.Fatalf("WorstMember = %+v, want down-now (state outranks uptime)", g.WorstMember)
	}
	var names []string
	for _, m := range g.Members {
		names = append(names, m.MonitorName)
	}
	want := []string{"down-now", "suspect", "degraded", "low-but-up", "no-data"}
	if len(names) != len(want) {
		t.Fatalf("members = %v, want %v", names, want)
	}
	for i := range want {
		if names[i] != want[i] {
			t.Fatalf("members = %v, want %v", names, want)
		}
	}
}

func TestCurrentStateRank_MatchesSQL(t *testing.T) {
	sql := currentStateRankSQL("m.current_state")
	for _, entry := range currentStateRanks {
		if got := currentStateRank(entry.state); got != entry.rank {
			t.Fatalf("currentStateRank(%q) = %d, want %d", entry.state, got, entry.rank)
		}
		clause := fmt.Sprintf("WHEN '%s' THEN %d", entry.state, entry.rank)
		if !strings.Contains(sql, clause) {
			t.Fatalf("SQL %q misses %q", sql, clause)
		}
	}
	if !strings.Contains(sql, fmt.Sprintf("ELSE %d END", currentStateRankDefault)) {
		t.Fatalf("SQL %q misses the default rank", sql)
	}
	if currentStateRank("down") >= currentStateRank("degraded") ||
		currentStateRank("degraded") >= currentStateRank("unknown") ||
		currentStateRank("unknown") >= currentStateRank("up") {
		t.Fatal("rank order must be down < degraded < unknown < up (web STATUS_RANK)")
	}
	if !currentStateNeedsAttention("suspect") || currentStateNeedsAttention("unknown") || currentStateNeedsAttention("up") {
		t.Fatal("only down, degraded and suspect need attention")
	}
}

func TestResampleTo_SameLengthCopies(t *testing.T) {
	out := resampleTo([]*float64{ptrFloat(1), ptrFloat(2), ptrFloat(3)}, 3)
	if len(out) != 3 || *out[0] != 1 || *out[1] != 2 || *out[2] != 3 {
		t.Fatalf("expected [1 2 3], got %v", out)
	}
}

func TestResampleTo_Downsamples(t *testing.T) {
	out := resampleTo([]*float64{ptrFloat(10), ptrFloat(20), ptrFloat(30), ptrFloat(40), ptrFloat(50), ptrFloat(60)}, 3)
	if len(out) != 3 {
		t.Fatalf("expected length 3, got %d", len(out))
	}
}

func TestResampleTo_UpsamplesPreservingShape(t *testing.T) {
	out := resampleTo([]*float64{ptrFloat(100), ptrFloat(50)}, 6)
	if len(out) != 6 {
		t.Fatalf("expected length 6, got %d", len(out))
	}
	// First half should reflect first source bucket; second half the second.
	if *out[0] != 100 || *out[5] != 50 {
		t.Fatalf("expected endpoints 100 and 50, got %v", out)
	}
}

func TestResampleTo_PreservesNoDataGaps(t *testing.T) {
	out := resampleTo([]*float64{ptrFloat(100), nil}, 6)
	if len(out) != 6 {
		t.Fatalf("expected length 6, got %d", len(out))
	}
	if out[0] == nil || *out[0] != 100 {
		t.Fatalf("expected first bucket 100, got %v", out[0])
	}
	if out[5] != nil {
		t.Fatalf("no-data gap must survive resampling, got %v", *out[5])
	}
}

func TestResampleTo_EmptyInput(t *testing.T) {
	out := resampleTo(nil, 12)
	if len(out) != 0 {
		t.Fatalf("expected empty, got %v", out)
	}
}
