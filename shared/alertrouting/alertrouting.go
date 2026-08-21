// Package alertrouting owns the definition of a monitor's effective alert
// routing: which channels its alerts reach, and whether they reach anyone at
// all. Two services ask different questions of the same rules — the alerter
// resolves routing to deliver notifications, the API resolves it to tell an
// operator that a monitor's alerts go nowhere — so both build their queries
// from the fragments here and cannot drift apart.
//
// Routing rules (mirrored from the alerter's dispatch predicates):
//
//   - notification_mode = 'custom'  → the monitor's own monitor_channels rows.
//   - notification_mode = 'default' → the tenant's tenant_default_channels.
//   - Only channels with alert_channels.is_active deliver; dispatch skips the
//     rest, so an assignment to a disabled channel routes nowhere.
//   - A member of a group whose member_alert_rollup = 'group' never dispatches
//     its own alert — the group's alert speaks for it, and the group's routing
//     is the one that matters.
//   - A group whose member_alert_rollup = 'per_monitor' never opens its own
//     derived alert; its members alert individually.
package alertrouting

import "github.com/google/uuid"

// Routing source values reported by Classify.
const (
	// SourceCustom means the monitor's own channel assignments route it.
	SourceCustom = "custom"
	// SourceTenantDefault means the monitor inherits the tenant defaults.
	SourceTenantDefault = "tenant_default"
	// SourceGroupRollup means a group rolls this monitor's alerts into its
	// own, so the group's routing decides who hears about it.
	SourceGroupRollup = "group_rollup"
	// SourceMembers means the monitor is a group that never alerts itself;
	// its members alert individually and carry their own routing.
	SourceMembers = "members"
)

// Reason values explaining why a monitor is unreachable. Empty when reachable.
const (
	// ReasonNoCustomChannels means custom mode with no assignments at all.
	ReasonNoCustomChannels = "no_custom_channels"
	// ReasonCustomChannelsDisabled means every assigned channel is disabled.
	ReasonCustomChannelsDisabled = "custom_channels_disabled"
	// ReasonNoTenantDefaults means the tenant has no default routes.
	ReasonNoTenantDefaults = "no_tenant_default_channels"
	// ReasonTenantDefaultsDisabled means every tenant default is disabled.
	ReasonTenantDefaultsDisabled = "tenant_default_channels_disabled"
	// ReasonGroupRollupUnrouted means the rolling-up group routes nowhere.
	ReasonGroupRollupUnrouted = "group_rollup_unrouted"
	// ReasonGroupRollupPaused means the rolling-up group is paused. Roll-up
	// suppression does not check the group's enabled flag, so a paused group
	// still silences its members while never alerting itself.
	ReasonGroupRollupPaused = "group_rollup_paused"
)

// Status is a monitor's effective alert reachability.
type Status struct {
	// Reachable is false when an alert on this monitor would notify nobody.
	Reachable bool `json:"reachable"`
	// Source names the routing that applies (see Source* constants).
	Source string `json:"source"`
	// ActiveChannels counts channels that would actually deliver.
	ActiveChannels int `json:"active_channels"`
	// AssignedChannels counts routed channels including disabled ones, so a
	// caller can distinguish "nothing assigned" from "all assigned channels
	// are disabled".
	AssignedChannels int `json:"assigned_channels"`
	// Reason explains an unreachable monitor (see Reason* constants); empty
	// when Reachable.
	Reason string `json:"reason,omitempty"`
	// RollupGroupID is the group that rolls this monitor's alerts up, set
	// only when Source is SourceGroupRollup.
	RollupGroupID *uuid.UUID `json:"rollup_group_id,omitempty"`
	// RollupGroupName is that group's display name.
	RollupGroupName string `json:"rollup_group_name,omitempty"`
}

// Counts is the raw per-monitor tally a query produces, before it is turned
// into a Status.
type Counts struct {
	// NotificationMode is the monitor's mode ('custom' or 'default').
	NotificationMode string
	// OwnActive / OwnAssigned count the monitor's own routing.
	OwnActive   int
	OwnAssigned int
	// SelfSilentGroup is true for a group with member_alert_rollup =
	// 'per_monitor', which never emits its own alert.
	SelfSilentGroup bool
	// RollupGroupID / RollupGroupName / RollupActive / RollupAssigned /
	// RollupEnabled describe the group that rolls this monitor's alerts up,
	// when one does.
	RollupGroupID   *uuid.UUID
	RollupGroupName string
	RollupActive    int
	RollupAssigned  int
	RollupEnabled   bool
}

// Classify turns raw counts into the reachability an operator sees. It is the
// single interpretation of the routing rules; callers only supply the counts.
func Classify(c Counts) Status {
	if c.SelfSilentGroup {
		// The group never alerts; each member's own status is what matters.
		return Status{Reachable: true, Source: SourceMembers}
	}
	if c.RollupGroupID != nil {
		st := Status{
			Source:           SourceGroupRollup,
			ActiveChannels:   c.RollupActive,
			AssignedChannels: c.RollupAssigned,
			RollupGroupID:    c.RollupGroupID,
			RollupGroupName:  c.RollupGroupName,
			Reachable:        c.RollupEnabled && c.RollupActive > 0,
		}
		if !st.Reachable {
			st.Reason = ReasonGroupRollupUnrouted
			if !c.RollupEnabled {
				// Paused wins as the explanation: fixing the group's channels
				// changes nothing while it stays paused.
				st.Reason = ReasonGroupRollupPaused
			}
		}
		return st
	}

	st := Status{
		ActiveChannels:   c.OwnActive,
		AssignedChannels: c.OwnAssigned,
		Reachable:        c.OwnActive > 0,
	}
	if c.NotificationMode == "custom" {
		st.Source = SourceCustom
		if !st.Reachable {
			st.Reason = ReasonNoCustomChannels
			if c.OwnAssigned > 0 {
				st.Reason = ReasonCustomChannelsDisabled
			}
		}
		return st
	}
	st.Source = SourceTenantDefault
	if !st.Reachable {
		st.Reason = ReasonNoTenantDefaults
		if c.OwnAssigned > 0 {
			st.Reason = ReasonTenantDefaultsDisabled
		}
	}
	return st
}

// TargetsSQL returns the set of routed channel assignments for the monitors in
// the `$1` tenant and `$2` monitor-ID array, as
// `(monitor_id, channel_id, delay_seconds, pos)` ordered by escalation
// position. It is the alerter's delivery routing and the API's reachability
// input, expressed once.
//
// Rows are emitted for disabled channels too — dispatch filters on
// is_active — so a caller that cares about delivery must join alert_channels
// and check it (as ChannelCountExpr with activeOnly does).
func TargetsSQL() string {
	return `
		SELECT mc.monitor_id, mc.channel_id, mc.delay_seconds, 0 AS pos
		FROM monitor_channels mc
		JOIN monitors m ON m.id = mc.monitor_id
		WHERE mc.monitor_id = ANY($2) AND m.notification_mode = 'custom'
		UNION ALL
		SELECT m.id, tdc.channel_id, tdc.delay_seconds, tdc.position
		FROM tenant_default_channels tdc
		JOIN monitors m ON m.tenant_id = tdc.tenant_id
		WHERE tdc.tenant_id = $1 AND m.id = ANY($2) AND m.notification_mode = 'default'`
}

// ChannelCountExpr returns a SQL scalar subquery counting the channels routed
// to the monitor aliased by monAlias. When activeOnly is true it counts only
// channels that would actually deliver. The expression embeds no bind
// parameters, so it can be dropped into any SELECT list.
func ChannelCountExpr(monAlias string, activeOnly bool) string {
	activePredicate := ""
	if activeOnly {
		activePredicate = " AND ac.is_active"
	}
	return `(
		SELECT COUNT(*) FROM (
			SELECT mc.channel_id
			FROM monitor_channels mc
			JOIN alert_channels ac ON ac.id = mc.channel_id` + activePredicate + `
			WHERE mc.monitor_id = ` + monAlias + `.id
			  AND ` + monAlias + `.notification_mode = 'custom'
			UNION ALL
			SELECT tdc.channel_id
			FROM tenant_default_channels tdc
			JOIN alert_channels ac ON ac.id = tdc.channel_id` + activePredicate + `
			WHERE tdc.tenant_id = ` + monAlias + `.tenant_id
			  AND ` + monAlias + `.notification_mode = 'default'
		) routed)`
}

// SelfSilentGroupPredicate returns a SQL boolean that is TRUE when the monitor
// aliased by monAlias is a group that never opens its own alert because its
// members alert individually.
func SelfSilentGroupPredicate(monAlias string) string {
	return `(` + monAlias + `.type = 'group' AND ` + monAlias + `.member_alert_rollup = 'per_monitor')`
}

// RollupGroupExpr returns a SQL scalar subquery yielding the id of the group
// that rolls the monitor identified by monitorIDExpr (a SQL expression such as
// `m.id` or `al.monitor_id`) into a single alert, or NULL when no group does.
//
// A monitor can belong to several roll-up groups, and each of them alerts, so
// reaching *any* of them is enough: a group that would actually deliver
// (enabled, with at least one active channel) is preferred over one that would
// not, and the lowest id breaks the tie so the choice is stable across queries.
func RollupGroupExpr(monitorIDExpr string) string {
	return `(
		SELECT g.id
		FROM monitor_groups mg
		JOIN monitors g ON g.id = mg.group_id
		WHERE mg.monitor_id = ` + monitorIDExpr + `
		  AND g.deleted_at IS NULL
		  AND g.member_alert_rollup = 'group'
		ORDER BY (g.enabled AND ` + ChannelCountExpr("g", true) + ` > 0) DESC, g.id
		LIMIT 1)`
}

// SuppressedByRollupPredicate returns a SQL boolean that is TRUE when the
// monitor identified by monitorIDExpr has its alerts rolled up into a group's,
// and so never dispatches its own. This is the alerter's member-suppression
// rule.
func SuppressedByRollupPredicate(monitorIDExpr string) string {
	return `EXISTS (
		SELECT 1
		FROM monitor_groups mg
		JOIN monitors g ON g.id = mg.group_id AND g.deleted_at IS NULL
		WHERE mg.monitor_id = ` + monitorIDExpr + `
		  AND g.member_alert_rollup = 'group')`
}

// UnreachablePredicate returns a SQL boolean that is TRUE when an alert on the
// monitor aliased by monAlias would notify nobody. It is the SQL twin of
// Classify's Reachable field — a monitor this predicate selects is exactly one
// Classify reports unreachable — so the two must change together
// (TestUnreachablePredicateMatchesClassify pins them).
//
// Callers usually add their own `enabled` / `deleted_at` filters: a paused or
// deleted monitor never alerts at all, so unrouted-ness is moot for it.
func UnreachablePredicate(monAlias string) string {
	rollup := RollupGroupExpr(monAlias + ".id")
	return `(NOT ` + SelfSilentGroupPredicate(monAlias) + ` AND CASE
		WHEN ` + rollup + ` IS NOT NULL THEN NOT (
			SELECT rg.enabled AND ` + ChannelCountExpr("rg", true) + ` > 0
			FROM monitors rg WHERE rg.id = ` + rollup + `)
		ELSE ` + ChannelCountExpr(monAlias, true) + ` = 0
	END)`
}
