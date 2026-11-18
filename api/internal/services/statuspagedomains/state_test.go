package statuspagedomains

import (
	"testing"
	"time"

	"github.com/yassinebenameur/probara/shared/pagedomains"
)

var (
	pass = checkResult{TXTFound: true, CNAMEFound: true}
	miss = checkResult{TXTFound: true, Problem: "CNAME record for x not found"}
)

func TestAdvance_PendingToVerified(t *testing.T) {
	now := time.Date(2026, 11, 17, 12, 0, 0, 0, time.UTC)
	created := now.Add(-time.Hour)
	cur := domainState{Status: pagedomains.Pending}

	next, changed := advance(cur, created, miss, now)
	if next.Status != pagedomains.Pending || changed || next.ConsecutiveFailures != 1 {
		t.Fatalf("failed pending check: %+v changed=%v", next, changed)
	}
	if got := next.NextCheckAt.Sub(now); got != pendingRecheck {
		t.Fatalf("young pending recheck in %v", got)
	}
	if next.FailingSince != nil {
		t.Fatal("a pending domain has a failing streak")
	}

	next, _ = advance(cur, now.Add(-pendingFastFor-time.Hour), miss, now)
	if got := next.NextCheckAt.Sub(now); got != pendingSlowRecheck {
		t.Fatalf("old pending recheck in %v", got)
	}

	next, changed = advance(cur, created, pass, now)
	if next.Status != pagedomains.Verified || !changed || next.VerifiedAt == nil || !next.VerifiedAt.Equal(now) {
		t.Fatalf("passing pending check: %+v changed=%v", next, changed)
	}
	if got := next.NextCheckAt.Sub(now); got != verifiedRecheck {
		t.Fatalf("verified recheck in %v", got)
	}
}

// One failed lookup never takes a verified domain down: it keeps being
// served, rechecked sooner, and turns failed only after failing every check
// for the whole tolerance.
func TestAdvance_VerifiedFailureTolerance(t *testing.T) {
	start := time.Date(2026, 11, 17, 12, 0, 0, 0, time.UTC)
	verifiedAt := start.Add(-48 * time.Hour)
	cur := domainState{Status: pagedomains.Verified, VerifiedAt: &verifiedAt}

	now := start
	for i := 0; ; i++ {
		next, changed := advance(cur, verifiedAt, miss, now)
		if now.Sub(start) < failTolerance {
			if next.Status != pagedomains.Verified || changed {
				t.Fatalf("check %d at +%v: %s (changed %v), want still verified", i, now.Sub(start), next.Status, changed)
			}
			if next.FailingSince == nil || !next.FailingSince.Equal(start) {
				t.Fatalf("check %d: failing since %v, want %v", i, next.FailingSince, start)
			}
			if got := next.NextCheckAt.Sub(now); got != failingRecheck {
				t.Fatalf("check %d: recheck in %v", i, got)
			}
			cur = next
			now = next.NextCheckAt
			continue
		}
		if next.Status != pagedomains.Failed || !changed {
			t.Fatalf("after %v of failures: %s (changed %v), want failed", now.Sub(start), next.Status, changed)
		}
		if got := next.NextCheckAt.Sub(now); got != failedRecheck {
			t.Fatalf("failed recheck in %v", got)
		}
		cur = next
		break
	}

	// A failed domain whose records come back is served again.
	now = now.Add(failedRecheck)
	next, changed := advance(cur, verifiedAt, pass, now)
	if next.Status != pagedomains.Verified || !changed || next.FailingSince != nil || next.ConsecutiveFailures != 0 {
		t.Fatalf("recovered: %+v changed=%v", next, changed)
	}
	if !next.VerifiedAt.Equal(now) {
		t.Fatalf("re-verified at %v, want %v", next.VerifiedAt, now)
	}
}

// A passing check in the middle of a streak resets it.
func TestAdvance_StreakResets(t *testing.T) {
	now := time.Date(2026, 11, 17, 12, 0, 0, 0, time.UTC)
	since := now.Add(-23 * time.Hour)
	verifiedAt := now.Add(-72 * time.Hour)
	cur := domainState{Status: pagedomains.Verified, VerifiedAt: &verifiedAt, FailingSince: &since, ConsecutiveFailures: 40}
	next, changed := advance(cur, verifiedAt, pass, now)
	if next.Status != pagedomains.Verified || changed || next.FailingSince != nil || next.ConsecutiveFailures != 0 {
		t.Fatalf("streak reset: %+v changed=%v", next, changed)
	}
	if !next.VerifiedAt.Equal(verifiedAt) {
		t.Fatal("an already verified domain got a new verified_at")
	}

	// Long enough but too few checks (the domain was verified moments
	// before a 24 h outage of the runner): still served.
	longAgo := now.Add(-30 * time.Hour)
	cur = domainState{Status: pagedomains.Verified, VerifiedAt: &verifiedAt, FailingSince: &longAgo, ConsecutiveFailures: 1}
	if next, _ = advance(cur, verifiedAt, miss, now); next.Status != pagedomains.Verified {
		t.Fatalf("failed on its second check: %+v", next)
	}
}
