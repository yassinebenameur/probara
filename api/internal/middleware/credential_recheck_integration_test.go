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

// The context a cookie request is served with can re-check, later, that the
// session is still live and the user still admitted to the tenant -- what
// the alert stream relies on to close for a revoked credential.
func TestAuthMiddlewareLeavesAStreamRecheck(t *testing.T) {
	ctx := context.Background()
	dbClient, cleanup := testutil.SetupPostgresDB(ctx, t)
	t.Cleanup(cleanup)
	tenant := testutil.InsertTenant(ctx, t, dbClient, "recheck")
	var user uuid.UUID
	if err := dbClient.QueryRowContext(ctx, `
		INSERT INTO admin_users (username, password_hash, platform_role) VALUES ('streamer', 'x', 'member') RETURNING id
	`).Scan(&user); err != nil {
		t.Fatal(err)
	}
	if _, err := dbClient.ExecContext(ctx, `
		INSERT INTO tenant_memberships (id, admin_user_id, tenant_id, role, created_at, updated_at)
		VALUES ($1, $2, $3, 'viewer', NOW(), NOW())
	`, uuid.New(), user, tenant); err != nil {
		t.Fatal(err)
	}
	svc := adminauth.NewService(dbClient, 4)
	newSession := func(hash string) uuid.UUID {
		family, err := svc.CreateSession(ctx, user, hash, time.Now().Add(time.Hour), "", "", adminauth.AuthMethodPassword)
		if err != nil {
			t.Fatal(err)
		}
		return family
	}
	const secret = "recheck-test-secret"
	mw := AuthMiddleware(dbClient, logger.New("test", "error"), secret)

	serve := func(family uuid.UUID) context.Context {
		t.Helper()
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
		if exp, ok := CredentialExpiry(served); !ok || time.Until(exp) > time.Minute {
			t.Fatalf("expiry = %v/%v, want the access token's", exp, ok)
		}
		return served
	}

	revokedSession := newSession("a")
	streamCtx := serve(revokedSession)
	if err := StillAuthorized(streamCtx); err != nil {
		t.Fatalf("live session: %v", err)
	}
	if err := svc.RevokeSession(ctx, user, revokedSession, adminauth.RevokeReasonUser); err != nil {
		t.Fatal(err)
	}
	if err := StillAuthorized(streamCtx); !errors.Is(err, ErrCredentialRevoked) {
		t.Fatalf("after revoking the session: %v, want ErrCredentialRevoked", err)
	}

	// Losing the workspace membership ends access to its stream too.
	streamCtx = serve(newSession("b"))
	if _, err := dbClient.ExecContext(ctx, `DELETE FROM tenant_memberships WHERE admin_user_id = $1`, user); err != nil {
		t.Fatal(err)
	}
	if err := StillAuthorized(streamCtx); !errors.Is(err, ErrCredentialRevoked) {
		t.Fatalf("after removing the membership: %v, want ErrCredentialRevoked", err)
	}
}
