//go:build !unit

package adminauth

import (
	"context"
	"errors"
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

	// Within the grace a concurrent refresh with the rotated token still
	// succeeds, in the same family.
	if got, err := svc.RefreshSession(ctx, "r1", "r2b", expires, "", ""); err != nil || got.FamilyID != family {
		t.Fatalf("refresh within grace = %v/%v", got, err)
	}

	// Past the grace, the rotated token coming back is reuse.
	if _, err := dbClient.ExecContext(ctx,
		`UPDATE admin_sessions SET revoked_at = NOW() - INTERVAL '2 minutes' WHERE refresh_token_hash = 'r1'`); err != nil {
		t.Fatal(err)
	}
	_, err := svc.RefreshSession(ctx, "r1", "r3", expires, "", "")
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
