package adminusers

import (
	"context"
	"database/sql"

	"github.com/google/uuid"

	"github.com/yassinebenameur/probara/shared/db"
	"github.com/yassinebenameur/probara/shared/db/pgerr"
)

// superadminSetLockKey names the transaction advisory lock that serializes
// every change able to shrink the set of active superadmins.
const superadminSetLockKey = "admin-users:superadmins"

// LockSuperadmins takes the superadmin-set lock for the rest of tx. Every
// path that demotes, disables or deletes a user must take it first -- before
// any row lock, so the lock order is the same everywhere -- and only then
// count the remaining superadmins (OtherActiveSuperadmins). Two separate
// transactions that each see "one other superadmin left" and then demote
// each other would otherwise leave none; under READ COMMITTED the count taken
// after the lock sees every change committed before it.
func LockSuperadmins(ctx context.Context, tx *sql.Tx) error {
	_, err := tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1, 0))`, superadminSetLockKey)
	return err
}

// OtherActiveSuperadmins reports whether userID is an active superadmin and
// how many other active superadmins exist. Call it inside the transaction
// that holds LockSuperadmins.
func OtherActiveSuperadmins(ctx context.Context, tx *sql.Tx, userID uuid.UUID) (targetIsSuperadmin bool, others int, err error) {
	err = tx.QueryRowContext(ctx, `
		SELECT
			EXISTS (SELECT 1 FROM admin_users WHERE id = $1 AND platform_role = 'superadmin' AND disabled_at IS NULL),
			(SELECT COUNT(*) FROM admin_users WHERE id <> $1 AND platform_role = 'superadmin' AND disabled_at IS NULL)
	`, userID).Scan(&targetIsSuperadmin, &others)
	return targetIsSuperadmin, others, err
}

// SuperadminEverExisted reports whether a superadmin has ever existed on this
// install (admin_bootstrap_closed, set by a trigger on admin_users, migration
// 000106). Once true, first-admin bootstrap -- the setup-code form and the
// first-SSO-user rule alike -- is closed for good, whatever users exist now;
// recovery is cmd/admin. The flag never goes back to false, so reading it
// outside the transaction that acts on it is safe.
func SuperadminEverExisted(ctx context.Context, q db.Querier) (bool, error) {
	var closed bool
	err := q.QueryRowContext(ctx, `SELECT EXISTS (SELECT 1 FROM admin_bootstrap_closed)`).Scan(&closed)
	if pgerr.IsMissingSchema(err) {
		// Deploy-ordering tolerance: migrations run after the new binary
		// starts, so until 000106 lands the live rule decides alone.
		return false, nil
	}
	return closed, err
}
