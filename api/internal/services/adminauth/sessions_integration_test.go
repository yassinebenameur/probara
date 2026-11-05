//go:build !unit

package adminauth

import (
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestSessionFamilyRotationAndRevocation(t *testing.T) {
	ctx, dbClient, s := setupService(t)
	id := insertUser(ctx, t, dbClient, "alice")
	expires := time.Now().Add(time.Hour)

	family, err := s.CreateSession(ctx, id, "hash-1", expires, "192.0.2.1", "browser-a", AuthMethodPassword)
	if err != nil || family == uuid.Nil {
		t.Fatalf("CreateSession = %v, %v", family, err)
	}
	other, err := s.CreateSession(ctx, id, "hash-other", expires, "192.0.2.2", "browser-b", AuthMethodOIDC)
	if err != nil {
		t.Fatal(err)
	}

	// Rotation keeps the family.
	gotAdmin, gotFamily, err := s.GetSessionByTokenHash(ctx, "hash-1")
	if err != nil || gotAdmin != id || gotFamily != family {
		t.Fatalf("lookup = %v %v %v, want %v %v", gotAdmin, gotFamily, err, id, family)
	}
	if err := s.RotateSession(ctx, "hash-1", "hash-2", id, family, expires, "192.0.2.9", "browser-a2"); err != nil {
		t.Fatal(err)
	}
	if _, gotFamily, err := s.GetSessionByTokenHash(ctx, "hash-2"); err != nil || gotFamily != family {
		t.Fatalf("rotated token family = %v (%v), want %v", gotFamily, err, family)
	}
	// The rotated token is still inside its reuse grace.
	if _, _, err := s.GetSessionByTokenHash(ctx, "hash-1"); err != nil {
		t.Fatalf("grace-period token: %v", err)
	}

	sessions, err := s.ListSessions(ctx, id, family)
	if err != nil || len(sessions) != 2 {
		t.Fatalf("ListSessions = %v, %v; want 2", sessions, err)
	}
	for _, sess := range sessions {
		if sess.Current != (sess.ID == family) {
			t.Fatalf("session %v current=%v", sess.ID, sess.Current)
		}
		if sess.ID == family && (sess.IP != "192.0.2.9" || sess.UserAgent != "browser-a2") {
			t.Fatalf("rotated session shows %q / %q, want the latest token's address", sess.IP, sess.UserAgent)
		}
		if sess.ID == other && sess.AuthMethod != AuthMethodOIDC {
			t.Fatalf("auth method = %q", sess.AuthMethod)
		}
	}

	// Revoking the family ends every token in it, the grace token included,
	// and the access-token check at once.
	if ok, err := s.RevokeFamily(ctx, id, family, RevokeReasonUser); err != nil || !ok {
		t.Fatalf("RevokeFamily = %v, %v", ok, err)
	}
	for _, hash := range []string{"hash-1", "hash-2"} {
		if _, _, err := s.GetSessionByTokenHash(ctx, hash); err == nil {
			t.Fatalf("%s still valid after family revoke", hash)
		}
	}
	if active, err := SessionFamilyActive(ctx, dbClient, id, family); err != nil || active {
		t.Fatalf("SessionFamilyActive after revoke = %v, %v", active, err)
	}
	if active, err := SessionFamilyActive(ctx, dbClient, id, other); err != nil || !active {
		t.Fatalf("other family active = %v, %v; want untouched", active, err)
	}
	// A family is only revocable by its owner, and only once.
	if ok, _ := s.RevokeFamily(ctx, uuid.New(), other, RevokeReasonUser); ok {
		t.Fatal("revoked another user's session")
	}
	if ok, _ := s.RevokeFamily(ctx, id, family, RevokeReasonUser); ok {
		t.Fatal("revoked an already revoked session")
	}
	if active, _ := SessionFamilyActive(ctx, dbClient, uuid.New(), other); active {
		t.Fatal("a family must not authenticate another user")
	}
}

func TestRevokeUserSessionsKeepsOne(t *testing.T) {
	ctx, dbClient, s := setupService(t)
	id := insertUser(ctx, t, dbClient, "bob")
	expires := time.Now().Add(time.Hour)

	keep, _ := s.CreateSession(ctx, id, "keep", expires, "", "", AuthMethodPassword)
	_, _ = s.CreateSession(ctx, id, "drop-1", expires, "", "", AuthMethodPassword)
	_, _ = s.CreateSession(ctx, id, "drop-2", expires, "", "", AuthMethodPassword)

	n, err := s.RevokeAllSessions(ctx, id, keep, RevokeReasonUser)
	if err != nil || n != 2 {
		t.Fatalf("RevokeAllSessions = %d, %v; want 2", n, err)
	}
	if _, _, err := s.GetSessionByTokenHash(ctx, "keep"); err != nil {
		t.Fatalf("kept session: %v", err)
	}
	if _, _, err := s.GetSessionByTokenHash(ctx, "drop-1"); err == nil {
		t.Fatal("drop-1 still valid")
	}

	n, err = s.RevokeAllSessions(ctx, id, uuid.Nil, RevokeReasonAdmin)
	if err != nil || n != 1 {
		t.Fatalf("revoke all = %d, %v; want 1", n, err)
	}
	if active, _ := SessionFamilyActive(ctx, dbClient, id, keep); active {
		t.Fatal("kept family still active after revoke-all")
	}
}

func TestLogoutRevokesTheFamily(t *testing.T) {
	ctx, dbClient, s := setupService(t)
	id := insertUser(ctx, t, dbClient, "carol")
	family, _ := s.CreateSession(ctx, id, "h1", time.Now().Add(time.Hour), "", "", AuthMethodPassword)
	if err := s.RotateSession(ctx, "h1", "h2", id, family, time.Now().Add(time.Hour), "", ""); err != nil {
		t.Fatal(err)
	}
	if err := s.RevokeSession(ctx, "h2"); err != nil {
		t.Fatal(err)
	}
	if active, _ := SessionFamilyActive(ctx, dbClient, id, family); active {
		t.Fatal("logout left the family active")
	}
	if _, _, err := s.GetSessionByTokenHash(ctx, "h1"); err == nil {
		t.Fatal("grace token outlived logout")
	}
}

// An API binary from before session families inserts sessions without a
// family_id during a rolling deploy; the trigger gives each its own family.
func TestLegacySessionInsertGetsAFamily(t *testing.T) {
	ctx, dbClient, s := setupService(t)
	id := insertUser(ctx, t, dbClient, "dave")
	if _, err := dbClient.ExecContext(ctx, `
		INSERT INTO admin_sessions (admin_user_id, refresh_token_hash, expires_at) VALUES ($1, 'legacy', NOW() + INTERVAL '1 hour')
	`, id); err != nil {
		t.Fatalf("legacy insert: %v", err)
	}
	_, family, err := s.GetSessionByTokenHash(ctx, "legacy")
	if err != nil || family == uuid.Nil {
		t.Fatalf("legacy session family = %v, %v", family, err)
	}
	if active, _ := SessionFamilyActive(ctx, dbClient, id, family); !active {
		t.Fatal("legacy family not active")
	}
}

func TestSessionsToleratePreFamilySchema(t *testing.T) {
	ctx, dbClient, s := setupService(t)
	id := insertUser(ctx, t, dbClient, "erin")
	if _, err := dbClient.ExecContext(ctx, `
		DROP TRIGGER admin_sessions_default_family ON admin_sessions;
		DROP FUNCTION admin_sessions_default_family();
		ALTER TABLE admin_sessions DROP COLUMN family_id;
		DROP TABLE admin_session_families;
	`); err != nil {
		t.Fatal(err)
	}

	family, err := s.CreateSession(ctx, id, "old-1", time.Now().Add(time.Hour), "", "", AuthMethodPassword)
	if err != nil || family != uuid.Nil {
		t.Fatalf("CreateSession on the old schema = %v, %v; want nil family", family, err)
	}
	gotAdmin, gotFamily, err := s.GetSessionByTokenHash(ctx, "old-1")
	if err != nil || gotAdmin != id || gotFamily != uuid.Nil {
		t.Fatalf("lookup on the old schema = %v %v %v", gotAdmin, gotFamily, err)
	}
	if err := s.RotateSession(ctx, "old-1", "old-2", id, uuid.Nil, time.Now().Add(time.Hour), "", ""); err != nil {
		t.Fatalf("rotate on the old schema: %v", err)
	}
	if active, err := SessionFamilyActive(ctx, dbClient, id, uuid.New()); err != nil || !active {
		t.Fatalf("SessionFamilyActive on the old schema = %v, %v; want tolerated", active, err)
	}
	if items, err := s.ListSessions(ctx, id, uuid.Nil); err != nil || len(items) != 0 {
		t.Fatalf("ListSessions on the old schema = %v, %v", items, err)
	}
	if ok, err := s.RevokeFamily(ctx, id, uuid.New(), RevokeReasonUser); err != nil || ok {
		t.Fatalf("RevokeFamily on the old schema = %v, %v", ok, err)
	}
	if n, err := s.RevokeAllSessions(ctx, id, uuid.Nil, RevokeReasonUser); err != nil || n != 1 {
		t.Fatalf("RevokeAllSessions on the old schema = %d, %v; want 1", n, err)
	}
	if err := s.RevokeSession(ctx, "old-2"); err != nil {
		t.Fatalf("RevokeSession on the old schema: %v", err)
	}
}
