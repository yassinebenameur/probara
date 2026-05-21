package dashboard

import (
	"testing"

	"github.com/google/uuid"
)

func ptrString(s string) *string { return &s }

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
		{MonitorID: m, Name: "stripe-webhook", Uptime: 91.2, Tags: []string{"api", "payments"}, CurrentStatus: ptrString("failure"), AttentionCount: 1},
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
		{MonitorID: uuid.New(), Name: "lone", Uptime: 99.9, Tags: []string{"customer-x"}, AttentionCount: 0},
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
		{MonitorID: uuid.New(), Name: "x", Uptime: 100, Tags: []string{"api"}, AttentionCount: 0},
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
		{MonitorID: uuid.New(), Name: "healthy", Uptime: 100, Tags: []string{"api"}, AttentionCount: 0},
		{MonitorID: uuid.New(), Name: "sick", Uptime: 91.2, Tags: []string{"api"}, CurrentStatus: ptrString("failure"), AttentionCount: 1},
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
		{MonitorID: uuid.New(), Name: "x", Uptime: 100, Tags: []string{"api"}, AttentionCount: 0},
	}
	groups := aggregateGroups(rows, []string{"api"})
	if groups[0].WorstMember != nil {
		t.Fatalf("expected no WorstMember when AttentionCount == 0")
	}
}
