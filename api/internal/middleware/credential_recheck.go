package middleware

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/google/uuid"

	"github.com/yassinebenameur/probara/api/internal/services/adminauth"
	"github.com/yassinebenameur/probara/api/internal/services/mcpoauth"
	"github.com/yassinebenameur/probara/shared/auth"
	"github.com/yassinebenameur/probara/shared/db"
	"github.com/yassinebenameur/probara/shared/db/pgerr"
)

// Long-lived responses (the alert stream) are authorized once, when they
// start, by AuthMiddleware. A credential revoked afterwards -- a signed-out
// session, a password reset, a removed membership, a revoked key -- must not
// keep them open, so AuthMiddleware leaves a re-check in the request context
// that repeats the same decision against the database, plus the
// credential's own expiry. A streaming handler calls StillAuthorized
// periodically and closes at CredentialExpiry.

// ErrCredentialRevoked is StillAuthorized's answer for a credential that no
// longer admits the request.
var ErrCredentialRevoked = errors.New("credential no longer authorized")

type credentialCheck struct {
	recheck func(ctx context.Context) error
	expires time.Time
}

type credentialCheckKey struct{}

// WithCredentialCheck records how to re-authorize the request; the auth
// middleware calls it for every credential type (and tests of streaming
// handlers may). An outer check already in ctx (the session or MCP token)
// runs first; expires keeps the earliest non-zero expiry.
func WithCredentialCheck(ctx context.Context, recheck func(context.Context) error, expires time.Time) context.Context {
	if outer, ok := ctx.Value(credentialCheckKey{}).(credentialCheck); ok {
		inner := recheck
		recheck = func(c context.Context) error {
			if err := outer.recheck(c); err != nil {
				return err
			}
			return inner(c)
		}
		if expires.IsZero() || (!outer.expires.IsZero() && outer.expires.Before(expires)) {
			expires = outer.expires
		}
	}
	return context.WithValue(ctx, credentialCheckKey{}, credentialCheck{recheck: recheck, expires: expires})
}

// StillAuthorized repeats the request's authorization against the database:
// nil while the credential still admits it, ErrCredentialRevoked once it does
// not. A request AuthMiddleware did not authenticate has nothing to re-check.
func StillAuthorized(ctx context.Context) error {
	check, ok := ctx.Value(credentialCheckKey{}).(credentialCheck)
	if !ok {
		return nil
	}
	return check.recheck(ctx)
}

// CredentialExpiry is when the request's credential lapses (an access
// token's expiry, an API key's expires_at); false when it does not.
func CredentialExpiry(ctx context.Context) (time.Time, bool) {
	check, ok := ctx.Value(credentialCheckKey{}).(credentialCheck)
	if !ok || check.expires.IsZero() {
		return time.Time{}, false
	}
	return check.expires, true
}

// sessionRecheck: the access token's session is still live.
func sessionRecheck(dbClient *db.Client, adminID, familyID uuid.UUID) func(context.Context) error {
	return func(ctx context.Context) error {
		active, err := adminauth.SessionActive(ctx, dbClient, adminID, familyID)
		if err != nil {
			return err
		}
		if !active {
			return ErrCredentialRevoked
		}
		return nil
	}
}

// mcpTokenRecheck: the MCP access token and its grant are still valid.
func mcpTokenRecheck(dbClient *db.Client, token string) func(context.Context) error {
	return func(ctx context.Context) error {
		if _, err := mcpoauth.LookupAccessToken(ctx, dbClient, token); err != nil {
			if errors.Is(err, mcpoauth.ErrInvalidGrant) {
				return ErrCredentialRevoked
			}
			return err
		}
		return nil
	}
}

// adminIdentityRecheck repeats handleAdminRequest's decision: the user
// exists, is enabled, and (unless a superadmin) is a member of the tenant.
func adminIdentityRecheck(dbClient *db.Client, adminID, tenantUUID uuid.UUID) func(context.Context) error {
	return func(ctx context.Context) error {
		var platformRole string
		var disabledAt *time.Time
		var membershipRole sql.NullString
		var tenantGone bool
		err := dbClient.QueryRowContext(ctx, identityQuery, adminID, tenantUUID).Scan(&platformRole, &disabledAt, &membershipRole, &tenantGone)
		switch {
		case pgerr.IsMissingSchema(err):
			return nil
		case errors.Is(err, sql.ErrNoRows):
			return ErrCredentialRevoked
		case err != nil:
			return err
		}
		if disabledAt != nil {
			return ErrCredentialRevoked
		}
		// A deleted workspace ends its streams like any other loss of access,
		// superadmins included.
		if tenantGone {
			return ErrCredentialRevoked
		}
		if platformRole != auth.PlatformRoleSuperadmin && tenantUUID != uuid.Nil && !membershipRole.Valid {
			return ErrCredentialRevoked
		}
		return nil
	}
}

// apiKeyRecheck: the key is neither revoked nor expired.
func apiKeyRecheck(dbClient *db.Client, keyID string) func(context.Context) error {
	return func(ctx context.Context) error {
		var live bool
		err := dbClient.QueryRowContext(ctx, `
			SELECT revoked_at IS NULL AND (expires_at IS NULL OR expires_at > NOW())
			FROM api_keys WHERE id = $1
		`, keyID).Scan(&live)
		switch {
		case pgerr.IsMissingSchema(err):
			return nil
		case errors.Is(err, sql.ErrNoRows):
			return ErrCredentialRevoked
		case err != nil:
			return err
		}
		if !live {
			return ErrCredentialRevoked
		}
		return nil
	}
}
