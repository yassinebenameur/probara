//go:build !unit

package middleware

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/yassinebenameur/probara/api/internal/services/adminauth"
	"github.com/yassinebenameur/probara/shared/auth"
	"github.com/yassinebenameur/probara/shared/logger"
	"github.com/yassinebenameur/probara/shared/testutil"
)

// Live streams follow the sessions: a completed password reset ends every
// session, so their streams close at the next re-check; the user's own
// password change closes the others' and keeps its own. A two-factor reset
// and a lock (held or not) end no session by design (a guesser must not be
// able to sign people out), so streams stay open.
func TestAccountChangesAndStreamRechecks(t *testing.T) {
	ctx := context.Background()
	dbClient, cleanup := testutil.SetupPostgresDB(ctx, t)
	t.Cleanup(cleanup)
	tenant := testutil.InsertTenant(ctx, t, dbClient, "recheck")
	hash, err := auth.HashPassword("correct horse battery", 4)
	if err != nil {
		t.Fatal(err)
	}
	var user uuid.UUID
	if err := dbClient.QueryRowContext(ctx, `
		INSERT INTO admin_users (username, password_hash, platform_role) VALUES ('streamer', $1, 'member') RETURNING id
	`, hash).Scan(&user); err != nil {
		t.Fatal(err)
	}
	if _, err := dbClient.ExecContext(ctx, `
		INSERT INTO tenant_memberships (id, admin_user_id, tenant_id, role, created_at, updated_at)
		VALUES ($1, $2, $3, 'viewer', NOW(), NOW())
	`, uuid.New(), user, tenant); err != nil {
		t.Fatal(err)
	}
	svc := adminauth.NewService(dbClient, 4)
	const secret = "recheck-test-secret"
	mw := AuthMiddleware(dbClient, logger.New("test", "error"), secret)
	stream := func() (uuid.UUID, context.Context) {
		t.Helper()
		family, err := svc.CreateSession(ctx, user, uuid.NewString(), time.Now().Add(time.Hour), "", "", adminauth.AuthMethodPassword)
		if err != nil {
			t.Fatal(err)
		}
		token, err := auth.CreateAdminToken(user.String(), family.String(), secret, time.Minute)
		if err != nil {
			t.Fatal(err)
		}
		req := httptest.NewRequest(http.MethodGet, "/api/v1/alerts/stream", nil)
		req.Header.Set("X-Tenant-ID", tenant.String())
		req.AddCookie(&http.Cookie{Name: "admin_access", Value: token})
		var served context.Context
		mw(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) { served = r.Context() })).ServeHTTP(httptest.NewRecorder(), req)
		if served == nil {
			t.Fatal("request not authenticated")
		}
		return family, served
	}

	// Two-factor reset and a held lock: streams stay open.
	_, keep := stream()
	if _, err := svc.ResetTwoFactor(ctx, user); err != nil {
		t.Fatal(err)
	}
	if _, err := dbClient.ExecContext(ctx, `
		UPDATE admin_users SET lockout_held = TRUE, locked_until = NOW() + INTERVAL '100 years' WHERE id = $1
	`, user); err != nil {
		t.Fatal(err)
	}
	if err := StillAuthorized(keep); err != nil {
		t.Fatalf("after a two-factor reset and a held lock: %v, want the stream kept", err)
	}
	if _, err := svc.UnlockUser(ctx, user); err != nil {
		t.Fatal(err)
	}

	// The user's own change keeps the changing session's stream only.
	mine, mineCtx := stream()
	_, otherCtx := stream()
	if _, err := svc.ChangePassword(ctx, user, mine, "another long passphrase"); err != nil {
		t.Fatal(err)
	}
	if err := StillAuthorized(mineCtx); err != nil {
		t.Fatalf("own change, own stream: %v", err)
	}
	if err := StillAuthorized(otherCtx); !errors.Is(err, ErrCredentialRevoked) {
		t.Fatalf("own change, other stream: %v, want revoked", err)
	}

	// A completed reset closes every stream.
	reset, err := svc.IssuePasswordReset(ctx, user, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.RedeemPasswordToken(ctx, reset.Token.Token, adminauth.PurposePasswordReset, "a reset long passphrase"); err != nil {
		t.Fatal(err)
	}
	for name, c := range map[string]context.Context{"kept": keep, "mine": mineCtx} {
		if err := StillAuthorized(c); !errors.Is(err, ErrCredentialRevoked) {
			t.Fatalf("after a reset, %s stream: %v, want revoked", name, err)
		}
	}
}
