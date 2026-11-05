package alertrouting

// Escalation policies (migration 000113) are the third notification mode.
// A policy wraps the routing above instead of replacing it:
//
//   - notification_mode = 'policy' → the monitor's escalation_policy_id.
//   - notification_mode = 'default' with the tenant's
//     default_escalation_policy_id set → that policy, instead of
//     tenant_default_channels.
//   - A policy's channel targets are ordinary routes: TargetsSQL emits them
//     as (channel, step delay, step position) rows, so they keep the
//     alert_notification_states claim, reminders and the ack gate exactly
//     like custom or default routes.
//   - Its person targets (a user, whoever a schedule has on call, the
//     workspace admins) are paged by the alerter's escalation loop. They
//     count toward reachability when they can page somebody right now
//     (PersonTargetReachablePredicate): an enabled workspace member (or
//     superadmin) with a deliverable verified contact.
//
// Reachability for a policy is therefore "an active channel target, or a
// reachable person target"; an unreachable policy reports
// ReasonPolicyNoTargets. ReachCountExpr is that sum, and both Classify and
// UnreachablePredicate are built from it.

// SourcePolicy means the monitor's own escalation policy routes it
// (notification_mode 'policy'). A default-mode monitor whose tenant has a
// default policy keeps SourceTenantDefault, with Status.EscalationPolicyID
// set.
const SourcePolicy = "policy"

// ReasonPolicyNoTargets means the effective escalation policy has no
// active channel target and no person target that can page anyone.
const ReasonPolicyNoTargets = "policy_no_targets"

// tenantDefaultPolicyExpr is the tenant's default escalation policy (NULL
// when it routes through tenant_default_channels).
func tenantDefaultPolicyExpr(tenantIDExpr string) string {
	return `(SELECT dpt.default_escalation_policy_id FROM tenants dpt WHERE dpt.id = ` + tenantIDExpr + `)`
}

// EffectivePolicyExpr returns a SQL uuid: the escalation policy that routes
// the monitor aliased by monAlias — its own in 'policy' mode, the tenant's
// default policy in 'default' mode — or NULL.
func EffectivePolicyExpr(monAlias string) string {
	return `(CASE ` + monAlias + `.notification_mode
		WHEN 'policy' THEN ` + monAlias + `.escalation_policy_id
		WHEN 'default' THEN ` + tenantDefaultPolicyExpr(monAlias+".tenant_id") + `
	END)`
}

// policyRoutesSQL is targetsSQL's policy branch: the channel targets of each
// matched monitor's effective policy, as (monitor_id, channel_id,
// delay_seconds, pos, notify_after_ack).
func policyRoutesSQL(tenantExpr string, monitorMatch func(col string) string) string {
	return `
		SELECT rtm.id, rpt.channel_id, rps.delay_seconds, rps.position, rpt.notify_after_ack
		FROM monitors rtm
		JOIN escalation_policy_steps rps ON rps.policy_id = ` + EffectivePolicyExpr("rtm") + `
		JOIN escalation_policy_targets rpt ON rpt.step_id = rps.id AND rpt.kind = 'channel'
		WHERE rtm.tenant_id = ` + tenantExpr + ` AND ` + monitorMatch("rtm.id")
}

// tenantPolicyRoutesSQL is TenantTargetsSQL's policy branch.
func tenantPolicyRoutesSQL() string {
	return `
		SELECT rpt.channel_id, rps.delay_seconds, rps.position, rpt.notify_after_ack
		FROM escalation_policy_steps rps
		JOIN escalation_policy_targets rpt ON rpt.step_id = rps.id AND rpt.kind = 'channel'
		WHERE rps.policy_id = ` + tenantDefaultPolicyExpr("$1")
}

// policyChannelCountSQL is ChannelCountExpr's policy branch.
func policyChannelCountSQL(monAlias, activePredicate string) string {
	return `
			SELECT pct.channel_id
			FROM escalation_policy_steps pcs
			JOIN escalation_policy_targets pct ON pct.step_id = pcs.id AND pct.kind = 'channel'
			JOIN alert_channels ac ON ac.id = pct.channel_id` + activePredicate + `
			WHERE pcs.policy_id = ` + EffectivePolicyExpr(monAlias)
}

// DeliverableContactPredicate returns a SQL boolean that is TRUE when the
// user_contacts row aliased by contactAlias can be paged in the tenant
// tenantIDExpr: verified, and either an email address (the platform
// mailer) or an SMS number while the tenant has an active personal SMS
// channel (tenants.personal_sms_channel_id). The alerter pages exactly
// these contacts, and reachability counts on them.
func DeliverableContactPredicate(contactAlias, tenantIDExpr string) string {
	return `(` + contactAlias + `.verified_at IS NOT NULL AND (` + contactAlias + `.kind = 'email' OR EXISTS (
		SELECT 1 FROM tenants smt
		JOIN alert_channels smc ON smc.id = smt.personal_sms_channel_id AND smc.tenant_id = smt.id AND smc.is_active
		WHERE smt.id = ` + tenantIDExpr + `)))`
}

// PageableUserPredicate returns a SQL boolean that is TRUE when the
// admin_users row aliased by userAlias can be paged for the tenant
// tenantIDExpr: enabled, a superadmin or a member of the workspace (the
// same eligibility as an on-call schedule member), with at least one
// deliverable contact.
func PageableUserPredicate(userAlias, tenantIDExpr string) string {
	return `(` + userAlias + `.disabled_at IS NULL
		AND (` + userAlias + `.platform_role = 'superadmin' OR EXISTS (
			SELECT 1 FROM tenant_memberships pgm
			WHERE pgm.admin_user_id = ` + userAlias + `.id AND pgm.tenant_id = ` + tenantIDExpr + `))
		AND EXISTS (
			SELECT 1 FROM user_contacts pgc
			WHERE pgc.user_id = ` + userAlias + `.id AND ` + DeliverableContactPredicate("pgc", tenantIDExpr) + `))`
}

// PersonTargetReachablePredicate returns a SQL boolean that is TRUE when the
// escalation_policy_targets row aliased by targetAlias (a person target)
// could page somebody in the tenant tenantIDExpr: a pageable user; a
// schedule with a pageable member on a rotation that has not ended or on an
// override that has not ended; or a workspace with a pageable admin. It is
// static configuration, like the rest of reachability: a schedule whose
// only pageable member is off shift right now still counts, while one with
// nobody pageable at all does not.
func PersonTargetReachablePredicate(targetAlias, tenantIDExpr string) string {
	return `(CASE ` + targetAlias + `.kind
		WHEN 'user' THEN EXISTS (
			SELECT 1 FROM admin_users pu
			WHERE pu.id = ` + targetAlias + `.user_id AND ` + PageableUserPredicate("pu", tenantIDExpr) + `)
		WHEN 'schedule' THEN EXISTS (
			SELECT 1 FROM admin_users pu
			WHERE ` + PageableUserPredicate("pu", tenantIDExpr) + `
			  AND (pu.id IN (
				SELECT prm.user_id FROM oncall_rotation_members prm
				JOIN oncall_rotations pr ON pr.id = prm.rotation_id
				WHERE pr.schedule_id = ` + targetAlias + `.schedule_id AND (pr.end_at IS NULL OR pr.end_at > NOW()))
			  OR pu.id IN (
				SELECT po.user_id FROM oncall_overrides po
				WHERE po.schedule_id = ` + targetAlias + `.schedule_id AND po.ends_at > NOW())))
		WHEN 'all_admins' THEN EXISTS (
			SELECT 1 FROM tenant_memberships pam
			JOIN admin_users pu ON pu.id = pam.admin_user_id
			WHERE pam.tenant_id = ` + tenantIDExpr + ` AND pam.role = 'admin'
			  AND ` + PageableUserPredicate("pu", tenantIDExpr) + `)
		ELSE FALSE
	END)`
}

// PeopleCountExpr returns a SQL scalar subquery counting the person targets
// of the monitor aliased by monAlias's effective escalation policy that can
// page somebody (PersonTargetReachablePredicate); 0 without a policy.
func PeopleCountExpr(monAlias string) string {
	return `(
		SELECT COUNT(*)
		FROM escalation_policy_steps pps
		JOIN escalation_policy_targets ppt ON ppt.step_id = pps.id AND ppt.kind <> 'channel'
		WHERE pps.policy_id = ` + EffectivePolicyExpr(monAlias) + `
		  AND ` + PersonTargetReachablePredicate("ppt", monAlias+".tenant_id") + `)`
}

// ReachCountExpr returns a SQL integer: the routes of the monitor aliased by
// monAlias that would actually notify somebody — active channels plus
// reachable person targets. Zero means unreachable; UnreachablePredicate and
// the roll-up group choice are built on it, and Classify reads the same two
// counts.
func ReachCountExpr(monAlias string) string {
	return `(` + ChannelCountExpr(monAlias, true) + ` + ` + PeopleCountExpr(monAlias) + `)`
}

// classifyPolicy is Classify for a monitor routed by an escalation policy
// (its own, or the tenant default): reachable through an active channel
// target or a reachable person target.
func classifyPolicy(c Counts) Status {
	st := Status{
		ActiveChannels:       c.OwnActive,
		AssignedChannels:     c.OwnAssigned,
		ReachablePeople:      c.OwnPeople,
		EscalationPolicyID:   c.PolicyID,
		EscalationPolicyName: c.PolicyName,
		Reachable:            c.OwnActive+c.OwnPeople > 0,
		Source:               SourceTenantDefault,
	}
	if c.NotificationMode == "policy" {
		st.Source = SourcePolicy
	}
	if !st.Reachable {
		st.Reason = ReasonPolicyNoTargets
	}
	return st
}

// policyRouted reports whether c is routed by an escalation policy.
func (c Counts) policyRouted() bool {
	return c.NotificationMode == "policy" || c.PolicyID != nil
}
