package statuspagedomains

import (
	"time"

	"github.com/yassinebenameur/probara/shared/pagedomains"
)

// Check cadence and the failure tolerance. A status page matters most while
// something is broken, and DNS is often part of what is broken, so a
// verified domain is never dropped on one bad lookup: it keeps being served
// while it fails, rechecked every failingRecheck, and turns failed only once
// it has failed every check for failTolerance (and at least failMinChecks
// times). One passing check clears the streak; a failed domain that passes
// again is verified again.
const (
	verifiedRecheck = 6 * time.Hour
	failingRecheck  = 30 * time.Minute
	failedRecheck   = time.Hour
	// A pending domain is checked often while its records are probably
	// being created, then rarely: nobody may ever finish setting it up.
	pendingRecheck     = 15 * time.Minute
	pendingSlowRecheck = 6 * time.Hour
	pendingFastFor     = 72 * time.Hour

	failTolerance = 24 * time.Hour
	failMinChecks = 3
)

// domainState is what a check moves.
type domainState struct {
	Status              string
	VerifiedAt          *time.Time
	FailingSince        *time.Time
	ConsecutiveFailures int
	NextCheckAt         time.Time
}

// advance applies one check's result at now. servedChanged reports whether
// the domain started or stopped being served, which the status-page service
// must hear about.
func advance(cur domainState, createdAt time.Time, res checkResult, now time.Time) (next domainState, servedChanged bool) {
	next = cur
	if res.passed() {
		if cur.Status != pagedomains.Verified {
			at := now
			next.VerifiedAt = &at
			servedChanged = true
		}
		next.Status = pagedomains.Verified
		next.FailingSince = nil
		next.ConsecutiveFailures = 0
		next.NextCheckAt = now.Add(verifiedRecheck)
		return next, servedChanged
	}

	next.ConsecutiveFailures = cur.ConsecutiveFailures + 1
	switch cur.Status {
	case pagedomains.Verified:
		since := now
		if cur.FailingSince != nil {
			since = *cur.FailingSince
		}
		next.FailingSince = &since
		if next.ConsecutiveFailures >= failMinChecks && now.Sub(since) >= failTolerance {
			next.Status = pagedomains.Failed
			next.NextCheckAt = now.Add(failedRecheck)
			return next, true
		}
		next.NextCheckAt = now.Add(failingRecheck)
	case pagedomains.Failed:
		next.NextCheckAt = now.Add(failedRecheck)
	default:
		next.Status = pagedomains.Pending
		if now.Sub(createdAt) < pendingFastFor {
			next.NextCheckAt = now.Add(pendingRecheck)
		} else {
			next.NextCheckAt = now.Add(pendingSlowRecheck)
		}
	}
	return next, false
}
