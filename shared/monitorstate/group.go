package monitorstate

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/google/uuid"
	"github.com/lib/pq"
)

// Group state (rules S-G1..S-G3 in docs/state-semantics.md).
//
// A group monitor has no checks of its own; its state is derived from its
// leaf descendants. Every surface derives it from the same input — the
// members' monitor-level current_state, which already reflects location
// quorum, the failure threshold and absence — through GroupSummary. Two views
// read that summary:
//
//   - AlertState: what the alerter stores in the group's current_state and
//     alerts on. One member down takes the group down.
//   - DisplayState: what the operator UI and public status pages show. A
//     partial outage reads "degraded"; only a total one reads "down".
//
// Members are the group's enabled, non-deleted, non-group descendants,
// deduplicated across nested paths (LoadGroupSummaries). A paused member
// takes no part in either view.

// GroupSummary counts a group's leaf members by state.
type GroupSummary struct {
	Up       int // up or suspect: serving, possibly with an unconfirmed failure
	Degraded int // some locations failing, below the down quorum
	Down     int
	Unknown  int // no verdict yet (never checked, or checks went absent)
}

// SummarizeGroup counts member states. An unrecognized state counts as
// unknown, so a state added later cannot silently read as healthy.
func SummarizeGroup(memberStates []State) GroupSummary {
	var s GroupSummary
	for _, state := range memberStates {
		switch state {
		case StateUp, StateSuspect:
			s.Up++
		case StateDegraded:
			s.Degraded++
		case StateDown:
			s.Down++
		default:
			s.Unknown++
		}
	}
	return s
}

// Total is the number of members counted.
func (s GroupSummary) Total() int { return s.Up + s.Degraded + s.Down + s.Unknown }

// AlertState is the state the alerter stores for the group (S-G2): down as
// soon as one member is down; up only when every member has a serving
// verdict; unknown otherwise, including an empty group. A group is never
// "degraded" here: that state belongs to a monitor with locations.
//
// GroupAlertStateSQL is its SQL twin; TestGroupAlertStateSQLMatchesSummary
// pins the pair.
func (s GroupSummary) AlertState() State {
	switch {
	case s.Down > 0:
		return StateDown
	case s.Total() > 0 && s.Unknown == 0:
		return StateUp
	default:
		return StateUnknown
	}
}

// DisplayState is the state shown for the group (S-G3). Members with no
// verdict are left out as long as at least one member has one: all of those
// down is "down", any of them down or degraded is "degraded", otherwise "up".
// No member with a verdict is "unknown".
func (s GroupSummary) DisplayState() State {
	known := s.Up + s.Degraded + s.Down
	switch {
	case known == 0:
		return StateUnknown
	case s.Down == known:
		return StateDown
	case s.Down > 0 || s.Degraded > 0:
		return StateDegraded
	default:
		return StateUp
	}
}

// DisplayCheckStatus is DisplayState in check-result vocabulary, for surfaces
// that present a group as a synthetic check result: "success", "failure",
// "degraded" or "unknown".
func (s GroupSummary) DisplayCheckStatus() string {
	switch s.DisplayState() {
	case StateUp:
		return "success"
	case StateDown:
		return "failure"
	case StateDegraded:
		return "degraded"
	default:
		return "unknown"
	}
}

// groupLeafStatesQuery returns one row per (group, leaf member) with the
// member's current_state: $1 is the group monitor IDs (uuid[]), $2 the
// tenant. UNION (not UNION ALL) deduplicates a member reachable through
// several nested groups and terminates membership cycles. Disabled and
// deleted monitors are skipped at every level, so a paused sub-group hides
// its members.
const groupLeafStatesQuery = `
	WITH RECURSIVE descendants AS (
		SELECT mg.group_id AS root_id, child.id, child.type
		FROM monitor_groups mg
		JOIN monitors child ON child.id = mg.monitor_id
			AND child.tenant_id = $2
			AND child.deleted_at IS NULL AND child.enabled
		WHERE mg.group_id = ANY($1::uuid[])
		UNION
		SELECT d.root_id, child.id, child.type
		FROM descendants d
		JOIN monitor_groups mg ON mg.group_id = d.id
		JOIN monitors child ON child.id = mg.monitor_id
			AND child.tenant_id = $2
			AND child.deleted_at IS NULL AND child.enabled
		WHERE d.type = 'group'
	)
	SELECT d.root_id, COALESCE(m.current_state, 'unknown')
	FROM descendants d
	JOIN monitors m ON m.id = d.id
	WHERE d.type <> 'group'`

// GroupQuerier is the read access LoadGroupSummaries needs (*sql.DB, *sql.Tx
// and the services' query interfaces all satisfy it).
type GroupQuerier interface {
	QueryContext(ctx context.Context, query string, args ...interface{}) (*sql.Rows, error)
}

// LoadGroupSummaries reads the member-state summary of each given group in
// one query. It is how every display surface (operator UI, batch results,
// public status pages) gets a group's state; a group with no members is
// present in the result with a zero summary.
func LoadGroupSummaries(ctx context.Context, q GroupQuerier, tenantID uuid.UUID, groupIDs []uuid.UUID) (map[uuid.UUID]GroupSummary, error) {
	summaries := make(map[uuid.UUID]GroupSummary, len(groupIDs))
	if len(groupIDs) == 0 {
		return summaries, nil
	}
	states := make(map[uuid.UUID][]State, len(groupIDs))
	rows, err := q.QueryContext(ctx, groupLeafStatesQuery, pq.Array(groupIDs), tenantID)
	if err != nil {
		return nil, fmt.Errorf("load group member states: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var groupID uuid.UUID
		var state string
		if err := rows.Scan(&groupID, &state); err != nil {
			return nil, fmt.Errorf("scan group member state: %w", err)
		}
		states[groupID] = append(states[groupID], State(state))
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate group member states: %w", err)
	}
	for _, groupID := range groupIDs {
		summaries[groupID] = SummarizeGroup(states[groupID])
	}
	return summaries, nil
}

// GroupMemberCountsSQL is the SELECT list that aggregates member rows into
// the four GroupSummary counts. stateExpr is the SQL expression for a
// member's current_state (for example "child.current_state").
func GroupMemberCountsSQL(stateExpr string) string {
	return `COUNT(*) FILTER (WHERE ` + stateExpr + ` IN ('up', 'suspect')) AS up_count,
		COUNT(*) FILTER (WHERE ` + stateExpr + ` = 'degraded') AS degraded_count,
		COUNT(*) FILTER (WHERE ` + stateExpr + ` = 'down') AS down_count,
		COUNT(*) FILTER (WHERE ` + stateExpr + ` IS NULL
			OR ` + stateExpr + ` NOT IN ('up', 'suspect', 'degraded', 'down')) AS unknown_count`
}

// GroupAlertStateSQL is GroupSummary.AlertState over the columns
// GroupMemberCountsSQL produces, read through the given table alias. A group
// with no member rows (alias columns NULL after a LEFT JOIN) is unknown.
func GroupAlertStateSQL(alias string) string {
	a := alias + "."
	return `CASE
		WHEN COALESCE(` + a + `down_count, 0) > 0 THEN 'down'
		WHEN COALESCE(` + a + `up_count, 0) + COALESCE(` + a + `degraded_count, 0) > 0
			AND COALESCE(` + a + `unknown_count, 0) = 0 THEN 'up'
		ELSE 'unknown' END`
}
