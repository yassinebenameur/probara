package alertrouting

// The acknowledge/snooze mute. An open alert that is acknowledged, or snoozed
// until a time still ahead, sends nothing but its resolve notice: no
// reminder, no first send to an escalation route whose delay had not run out
// yet, no suspected-cause follow-up. It applies to every alert kind and every
// route, with no setting. An acknowledgement still reaches the paging
// channels already paged (the "acknowledged" event), since that is what stops
// the provider's own escalation.
//
// Three places decide a send and all of them read the mute from here, so they
// cannot disagree: the alerter's pre-filter (MutedPredicate selected next to
// the alert, then openAlertEventDue), its claim (deliverNotification re-reads
// the row FOR SHARE and asks MuteHolds), and the async delivery worker
// (eventSuperseded re-reads it FOR SHARE and drops what MuteHolds holds).
//
// Lifting a mute (unacknowledge, unsnooze, or the snooze running out) resumes
// the clocks from that moment, DispatchResumedAtExpr: unfired tiers count
// their delay from it (DispatchEligibleSinceExpr) and reminders their
// interval, so no burst of "missed" notifications follows.

// SnoozedPredicate returns a SQL boolean that is TRUE while the alert aliased
// by alertAlias is snoozed: its snooze ends after the database's NOW(). Never
// NULL.
func SnoozedPredicate(alertAlias string) string {
	return `COALESCE(` + alertAlias + `.snoozed_until > NOW(), FALSE)`
}

// MutedPredicate returns a SQL boolean that is TRUE while the alert aliased by
// alertAlias is muted: acknowledged, or snoozed. Never NULL.
func MutedPredicate(alertAlias string) string {
	return `(` + alertAlias + `.status = 'acknowledged' OR ` + SnoozedPredicate(alertAlias) + `)`
}

// DispatchResumedAtExpr returns a SQL timestamp: when the alert aliased by
// alertAlias last came out of a mute (its last unacknowledge, or the end of
// its snooze, whichever is later), or NULL when it never was muted that way.
// GREATEST ignores NULLs.
func DispatchResumedAtExpr(alertAlias string) string {
	return `GREATEST(` + alertAlias + `.unacknowledged_at, ` + alertAlias + `.snoozed_until)`
}

// MuteHolds reports whether the mute holds back an event of eventType for an
// alert that is acknowledged and/or snoozed. It holds the page ("created"),
// the reminder and the suspected-cause follow-up; never the resolve, nor the
// acknowledgement to paging channels.
func MuteHolds(eventType string, acknowledged, snoozed bool) bool {
	if !acknowledged && !snoozed {
		return false
	}
	switch eventType {
	case "created", "reminder", "suspected_cause":
		return true
	}
	return false
}
