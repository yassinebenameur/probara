//go:build !unit

package adminauth

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestSessionFamilyLastsAcrossRefreshes(t *testing.T) {
	ctx := context.Background()
	svc, dbClient, _ := newLockoutService(t)
	user := insertPasswordUser(t, dbClient, "grace")
	expires := time.Now().Add(24 * time.Hour)

	family, err := svc.CreateSession(ctx, user, "h1", expires, "203.0.113.5", "curl/8", AuthMethodPassword)
	if err != nil || family == uuid.Nil {
		t.Fatalf("create = %v/%v", family, err)
	}
	for i, pair := range [][2]string{{"h1", "h2"}, {"h2", "h3"}} {
		got, err := svc.RefreshSession(ctx, pair[0], pair[1], expires.Add(time.Duration(i+1)*time.Hour), "198.51.100.7", "Firefox/1")
		if err != nil {
			t.Fatalf("refresh %d: %v", i, err)
		}
		if got.FamilyID != family || got.AdminID != user {
			t.Fatalf("refresh %d continued %v/%v, want %v/%v", i, got.FamilyID, got.AdminID, family, user)
		}
	}
	sessions, err := svc.ListSessions(ctx, user, family)
	if err != nil || len(sessions) != 1 {
		t.Fatalf("list = %d/%v, want one session for one sign-in", len(sessions), err)
	}
	s := sessions[0]
	if !s.Current || s.IP != "198.51.100.7" || s.AuthMethod != AuthMethodPassword || !s.ExpiresAt.After(expires) {
		t.Fatalf("session = %+v, want current, last address, password, slid expiry", s)
	}
	if active, _ := svc.SessionActive(ctx, user, family); !active {
		t.Fatal("session should be active")
	}
}

func TestRefreshTokenReuseRevokesTheFamily(t *testing.T) {
	ctx := context.Background()
	svc, dbClient, _ := newLockoutService(t)
	user := insertPasswordUser(t, dbClient, "heidi")
	expires := time.Now().Add(24 * time.Hour)
	family, _ := svc.CreateSession(ctx, user, "r1", expires, "", "", AuthMethodPassword)
	if _, err := svc.RefreshSession(ctx, "r1", "r2", expires, "", ""); err != nil {
		t.Fatal(err)
	}

	// Within the grace the rotated token still earns an access token for the
	// family, but no new refresh token: the session must not fork.
	got, err := svc.RefreshSession(ctx, "r1", "r2b", expires, "", "")
	if err != nil || got.FamilyID != family || got.Rotated {
		t.Fatalf("refresh within grace = %+v/%v, want the family, not rotated", got, err)
	}
	var forked int
	if err := dbClient.QueryRowContext(ctx, `SELECT COUNT(*) FROM admin_sessions WHERE refresh_token_hash = 'r2b'`).Scan(&forked); err != nil || forked != 0 {
		t.Fatalf("grace refresh stored %d refresh tokens (%v), want none", forked, err)
	}

	// Past the grace, the rotated token coming back is reuse.
	if _, err := dbClient.ExecContext(ctx,
		`UPDATE admin_sessions SET revoked_at = NOW() - INTERVAL '2 minutes' WHERE refresh_token_hash = 'r1'`); err != nil {
		t.Fatal(err)
	}
	_, err = svc.RefreshSession(ctx, "r1", "r3", expires, "", "")
	var reuse *RefreshReuse
	if !errors.As(err, &reuse) || reuse.FamilyID != family || reuse.AdminID != user {
		t.Fatalf("reuse = %v, want a *RefreshReuse for the family", err)
	}
	if active, _ := svc.SessionActive(ctx, user, family); active {
		t.Fatal("family still active after reuse")
	}
	if _, err := svc.RefreshSession(ctx, "r2", "r4", expires, "", ""); !errors.Is(err, ErrInvalidRefreshToken) {
		t.Fatalf("newest token after reuse = %v, want refused", err)
	}
	var reason string
	if err := dbClient.QueryRowContext(ctx, `SELECT revoke_reason FROM admin_session_families WHERE id = $1`, family).Scan(&reason); err != nil || reason != RevokeReasonRefreshReuse {
		t.Fatalf("revoke reason = %q/%v", reason, err)
	}
}

func TestRevokeOneAndAllOthers(t *testing.T) {
	ctx := context.Background()
	svc, dbClient, _ := newLockoutService(t)
	user := insertPasswordUser(t, dbClient, "ivan")
	other := insertPasswordUser(t, dbClient, "judy")
	expires := time.Now().Add(time.Hour)
	a, _ := svc.CreateSession(ctx, user, "a", expires, "", "", AuthMethodPassword)
	b, _ := svc.CreateSession(ctx, user, "b", expires, "", "", AuthMethodOIDC)
	c, _ := svc.CreateSession(ctx, user, "c", expires, "", "", AuthMethodPassword)
	theirs, _ := svc.CreateSession(ctx, other, "d", expires, "", "", AuthMethodPassword)

	// Another user's session is not yours to revoke.
	if err := svc.RevokeSession(ctx, user, theirs, RevokeReasonUser); !errors.Is(err, ErrSessionNotFound) {
		t.Fatalf("revoke someone else's = %v, want ErrSessionNotFound", err)
	}
	if err := svc.RevokeSession(ctx, user, b, RevokeReasonUser); err != nil {
		t.Fatal(err)
	}
	if err := svc.RevokeSession(ctx, user, b, RevokeReasonUser); !errors.Is(err, ErrSessionNotFound) {
		t.Fatalf("second revoke = %v, want ErrSessionNotFound", err)
	}
	if _, err := svc.RefreshSession(ctx, "b", "b2", expires, "", ""); !errors.Is(err, ErrInvalidRefreshToken) {
		t.Fatalf("revoked session refresh = %v", err)
	}

	n, err := svc.RevokeAllSessions(ctx, user, a, RevokeReasonUser)
	if err != nil || n != 1 {
		t.Fatalf("revoke others = %d/%v, want 1 (c)", n, err)
	}
	for id, want := range map[uuid.UUID]bool{a: true, b: false, c: false} {
		if got, _ := svc.SessionActive(ctx, user, id); got != want {
			t.Errorf("session %v active = %v, want %v", id, got, want)
		}
	}
	if got, _ := svc.SessionActive(ctx, other, theirs); !got {
		t.Error("another user's session was revoked")
	}
	// The sid must belong to the token's user.
	if got, _ := svc.SessionActive(ctx, other, a); got {
		t.Error("a session id was accepted for another user")
	}
	list, _ := svc.ListSessions(ctx, user, uuid.Nil)
	if len(list) != 1 || list[0].ID != a {
		t.Fatalf("list = %+v, want only a", list)
	}
}

func TestRevokeByRefreshTokenEndsTheSession(t *testing.T) {
	ctx := context.Background()
	svc, dbClient, _ := newLockoutService(t)
	user := insertPasswordUser(t, dbClient, "ken")
	expires := time.Now().Add(time.Hour)
	family, _ := svc.CreateSession(ctx, user, "k1", expires, "", "", AuthMethodPassword)
	if err := svc.RevokeByRefreshToken(ctx, "k1"); err != nil {
		t.Fatal(err)
	}
	if active, _ := svc.SessionActive(ctx, user, family); active {
		t.Fatal("session active after sign-out")
	}
	if err := svc.RevokeByRefreshToken(ctx, "unknown"); err != nil {
		t.Fatalf("unknown token = %v, want no error", err)
	}
}

// Tabs refreshing with the same cookie at once all get through; exactly one
// rotates, so the session keeps one chain of refresh tokens.
func TestConcurrentRefreshesRotateOnce(t *testing.T) {
	ctx := context.Background()
	svc, dbClient, _ := newLockoutService(t)
	user := insertPasswordUser(t, dbClient, "lena")
	expires := time.Now().Add(time.Hour)
	family, _ := svc.CreateSession(ctx, user, "t0", expires, "", "", AuthMethodPassword)

	const tabs = 8
	type result struct {
		got *Refreshed
		err error
	}
	results := make(chan result, tabs)
	for i := 0; i < tabs; i++ {
		go func(i int) {
			got, err := svc.RefreshSession(ctx, "t0", fmt.Sprintf("t1-%d", i), expires, "", "")
			results <- result{got, err}
		}(i)
	}
	rotated := 0
	for i := 0; i < tabs; i++ {
		r := <-results
		if r.err != nil || r.got.FamilyID != family {
			t.Fatalf("tab refresh = %+v/%v", r.got, r.err)
		}
		if r.got.Rotated {
			rotated++
		}
	}
	if rotated != 1 {
		t.Fatalf("%d refreshes rotated, want exactly 1", rotated)
	}
	var live int
	if err := dbClient.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM admin_sessions WHERE family_id = $1 AND revoked_at IS NULL`, family).Scan(&live); err != nil || live != 1 {
		t.Fatalf("live refresh tokens = %d (%v), want 1", live, err)
	}
}

// Pruning removes only rows past the retention window: rotated tokens of a
// live session stay (reuse detection needs them until they expire), dead
// sessions and long-expired tokens go.
func TestPruneSessionsKeepsWhatReuseDetectionNeeds(t *testing.T) {
	ctx := context.Background()
	svc, dbClient, _ := newLockoutService(t)
	user := insertPasswordUser(t, dbClient, "mona")
	retention := SessionRetention(30 * 24 * time.Hour)
	expires := time.Now().Add(30 * 24 * time.Hour)

	live, _ := svc.CreateSession(ctx, user, "live-1", expires, "", "", AuthMethodPassword)
	if _, err := svc.RefreshSession(ctx, "live-1", "live-2", expires, "", ""); err != nil {
		t.Fatal(err)
	}
	revoked, _ := svc.CreateSession(ctx, user, "revoked-1", expires, "", "", AuthMethodPassword)
	expired, _ := svc.CreateSession(ctx, user, "expired-1", expires, "", "", AuthMethodPassword)
	recentlyRevoked, _ := svc.CreateSession(ctx, user, "recent-1", expires, "", "", AuthMethodPassword)
	long := fmt.Sprintf("%d seconds", int(retention.Seconds())+3600)
	for _, stmt := range []string{
		`UPDATE admin_session_families SET revoked_at = NOW() - INTERVAL '` + long + `' WHERE id = '` + revoked.String() + `'`,
		`UPDATE admin_session_families SET expires_at = NOW() - INTERVAL '` + long + `' WHERE id = '` + expired.String() + `'`,
		`UPDATE admin_session_families SET revoked_at = NOW() - INTERVAL '1 day' WHERE id = '` + recentlyRevoked.String() + `'`,
		// A legacy token row with no session, expired long ago.
		`INSERT INTO admin_sessions (admin_user_id, refresh_token_hash, expires_at, revoked_at)
		 VALUES ('` + user.String() + `', 'legacy-dead', NOW() - INTERVAL '` + long + `', NOW() - INTERVAL '` + long + `')`,
	} {
		if _, err := dbClient.ExecContext(ctx, stmt); err != nil {
			t.Fatal(err)
		}
	}

	if _, err := PruneSessions(ctx, dbClient, retention); err != nil {
		t.Fatal(err)
	}
	exists := func(query string, arg any) bool {
		var n int
		if err := dbClient.QueryRowContext(ctx, query, arg).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n > 0
	}
	for id, want := range map[string]bool{live.String(): true, recentlyRevoked.String(): true, revoked.String(): false, expired.String(): false} {
		if got := exists(`SELECT COUNT(*) FROM admin_session_families WHERE id = $1`, id); got != want {
			t.Errorf("session %s kept = %v, want %v", id, got, want)
		}
	}
	for hash, want := range map[string]bool{"live-1": true, "live-2": true, "revoked-1": false, "legacy-dead": false} {
		if got := exists(`SELECT COUNT(*) FROM admin_sessions WHERE refresh_token_hash = $1`, hash); got != want {
			t.Errorf("token %s kept = %v, want %v", hash, got, want)
		}
	}
	// The rotated token still trips reuse detection after the prune.
	if _, err := dbClient.ExecContext(ctx, `UPDATE admin_sessions SET revoked_at = NOW() - INTERVAL '5 minutes' WHERE refresh_token_hash = 'live-1'`); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.RefreshSession(ctx, "live-1", "x", expires, "", ""); !errors.Is(err, ErrRefreshTokenReused) {
		t.Fatalf("reuse after prune = %v, want ErrRefreshTokenReused", err)
	}
}
