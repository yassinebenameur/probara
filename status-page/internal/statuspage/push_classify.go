package statuspage

// pushKind is what a visitor gets told.
type pushKind string

const (
	pushKindDown      pushKind = "down"
	pushKindRecovered pushKind = "recovered"
)

// classifyPushTransition is the notify rule, as a pure function.
//
// This is the Go twin of the CASE/WHERE in reconcileNotifications' SQL; the
// two must agree, and TestReconcileSQLMatchesClassify pins the pair the same
// way alertrouting pins UnreachablePredicate against Classify.
//
// previous is the last state the visitor was actually told about -- the most
// recent 'down' or 'up' interval, skipping 'suspect', 'degraded' and
// 'unknown'. Skipping them is what gives the intended behaviour without any
// special cases:
//
//	up -> suspect -> down        one outage (the suspect step is invisible)
//	down -> suspect -> down      nothing (already announced as down)
//	down -> degraded -> up       one recovery (degraded is not a recovery)
//	up -> degraded               nothing
//	down -> unknown -> up        one recovery (the loop still needs closing)
//
// The user-facing contract is deliberately narrow: a visitor is told when
// something they can see breaks, and when it is fixed. Degraded is excluded
// because it means some but not enough locations are failing -- from most of
// the world the service is still up, and notifying on it would train people
// to ignore the notifications.
func classifyPushTransition(previous, current string) (pushKind, bool) {
	switch current {
	case "down":
		// Anything that is not already-announced-down becomes an outage,
		// including the first ever transition (previous == "").
		if previous != "down" {
			return pushKindDown, true
		}
	case "up":
		// Only a recovery from an announced outage. Coming up from suspect or
		// from a fresh monitor is not news.
		if previous == "down" {
			return pushKindRecovered, true
		}
	}
	return "", false
}
