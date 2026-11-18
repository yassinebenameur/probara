//go:build !unit

package db_test

import (
	"context"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/golang-migrate/migrate/v4"
	"github.com/golang-migrate/migrate/v4/database/postgres"
	_ "github.com/golang-migrate/migrate/v4/source/file"
	"github.com/google/uuid"

	"github.com/yassinebenameur/probara/shared/testutil"
)

// Migrations 000116 (sign-in lockout columns) and 000117 (session families)
// walked down and back up through golang-migrate. Refresh tokens that were
// live before 000117 each become their own session family, so signed-in
// users stay signed in; dead tokens keep no family. Going down, tokens of a
// revoked family stay revoked.
func TestAdminIdentityMigrationsUpDownUp(t *testing.T) {
	ctx := context.Background()
	dbc, cleanup := testutil.SetupPostgresDB(ctx, t)
	defer cleanup()

	_, file, _, _ := runtime.Caller(0)
	driver, err := postgres.WithInstance(dbc.DB, &postgres.Config{})
	if err != nil {
		t.Fatal(err)
	}
	m, err := migrate.NewWithDatabaseInstance("file://"+filepath.Join(filepath.Dir(file), "migrations"), "postgres", driver)
	if err != nil {
		t.Fatal(err)
	}
	// Not m.Close(): the driver would close dbc.DB, which cleanup owns.
	migrateTo := func(v uint) {
		t.Helper()
		if err := m.Migrate(v); err != nil && err != migrate.ErrNoChange {
			t.Fatalf("migrate to %d: %v", v, err)
		}
	}

	migrateTo(113) // before both
	var user uuid.UUID
	if err := dbc.QueryRowContext(ctx, `
		INSERT INTO admin_users (username, password_hash, platform_role) VALUES ('legacy', 'x', 'member') RETURNING id
	`).Scan(&user); err != nil {
		t.Fatal(err)
	}
	insertToken := func(hash, state string) uuid.UUID {
		t.Helper()
		revoked, expires := "NULL", "NOW() + INTERVAL '1 day'"
		switch state {
		case "rotated":
			revoked = "NOW() - INTERVAL '1 hour'"
		case "expired":
			expires = "NOW() - INTERVAL '1 hour'"
		}
		var id uuid.UUID
		if err := dbc.QueryRowContext(ctx, `
			INSERT INTO admin_sessions (admin_user_id, refresh_token_hash, expires_at, revoked_at, ip, user_agent)
			VALUES ($1, $2, `+expires+`, `+revoked+`, '203.0.113.9', 'curl/8') RETURNING id
		`, user, hash).Scan(&id); err != nil {
			t.Fatal(err)
		}
		return id
	}
	live := insertToken("live", "live")
	rotated := insertToken("rotated", "rotated")
	expired := insertToken("expired", "expired")

	migrateTo(116)
	var count int
	if err := dbc.QueryRowContext(ctx, `SELECT failed_login_count FROM admin_users WHERE id = $1`, user).Scan(&count); err != nil || count != 0 {
		t.Fatalf("lockout columns after 116: %d/%v", count, err)
	}

	checkUp := func(phase string) {
		t.Helper()
		var family *uuid.UUID
		if err := dbc.QueryRowContext(ctx, `SELECT family_id FROM admin_sessions WHERE id = $1`, live).Scan(&family); err != nil {
			t.Fatalf("%s: %v", phase, err)
		}
		if family == nil || *family != live {
			t.Fatalf("%s: live token family = %v, want its own row id", phase, family)
		}
		var ip string
		if err := dbc.QueryRowContext(ctx, `
			SELECT ip FROM admin_session_families WHERE id = $1 AND admin_user_id = $2 AND revoked_at IS NULL
		`, live, user).Scan(&ip); err != nil || ip != "203.0.113.9" {
			t.Fatalf("%s: live family = %q/%v", phase, ip, err)
		}
		for _, dead := range []uuid.UUID{rotated, expired} {
			var f *uuid.UUID
			if err := dbc.QueryRowContext(ctx, `SELECT family_id FROM admin_sessions WHERE id = $1`, dead).Scan(&f); err != nil || f != nil {
				t.Fatalf("%s: dead token family = %v/%v, want none", phase, f, err)
			}
		}
		// An older binary inserting a token without a family gets one.
		var rowID uuid.UUID
		var rowFamily *uuid.UUID
		if err := dbc.QueryRowContext(ctx, `
			INSERT INTO admin_sessions (admin_user_id, refresh_token_hash, expires_at)
			VALUES ($1, $2, NOW() + INTERVAL '1 day') RETURNING id, family_id
		`, user, phase+"-old-binary").Scan(&rowID, &rowFamily); err != nil {
			t.Fatalf("%s: old-binary insert: %v", phase, err)
		}
		if rowFamily == nil || *rowFamily != rowID {
			t.Fatalf("%s: old-binary token family = %v, want its own", phase, rowFamily)
		}
	}

	migrateTo(117)
	checkUp("first up")

	// Revoke the live family, then go down: its token must stay dead.
	if _, err := dbc.ExecContext(ctx, `UPDATE admin_session_families SET revoked_at = NOW() WHERE id = $1`, live); err != nil {
		t.Fatal(err)
	}
	migrateTo(116)
	var revoked bool
	if err := dbc.QueryRowContext(ctx, `SELECT revoked_at IS NOT NULL FROM admin_sessions WHERE id = $1`, live).Scan(&revoked); err != nil || !revoked {
		t.Fatalf("after down: token of a revoked family revoked=%v/%v", revoked, err)
	}
	migrateTo(113)
	if _, err := dbc.ExecContext(ctx, `SELECT failed_login_count FROM admin_users`); err == nil {
		t.Fatal("lockout columns still present after down")
	}

	// Up again: make the live token live once more so the backfill sees it.
	if _, err := dbc.ExecContext(ctx, `UPDATE admin_sessions SET revoked_at = NULL WHERE id = $1`, live); err != nil {
		t.Fatal(err)
	}
	if _, err := dbc.ExecContext(ctx, `DELETE FROM admin_sessions WHERE refresh_token_hash LIKE '%-old-binary'`); err != nil {
		t.Fatal(err)
	}
	migrateTo(117)
	checkUp("second up")
}
