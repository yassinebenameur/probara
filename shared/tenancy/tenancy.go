// Package tenancy holds the workspace (tenant) lifecycle rules that more than
// one service enforces.
//
// Deleting a workspace is a soft delete: the API stamps tenants.deleted_at
// (and tombstones the workspace's monitors, revokes its credentials) in one
// transaction, and the scheduler purger later drains the data in bounded
// batches and removes the row. Between the two, every reader treats the
// workspace as gone: the API's auth middleware refuses requests scoped to it
// and the status-page service stops serving its pages, both through IsDeleted.
package tenancy

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/lib/pq"

	"github.com/yassinebenameur/probara/shared/db"
)

// DefaultTenantID is the workspace that migration 000030 and the first-user
// bootstrap seed, and the default OIDC_JIT_DEFAULT_TENANT_ID. It can never be
// deleted.
const DefaultTenantID = "00000000-0000-0000-0000-000000000001"

const deletedQuery = `SELECT deleted_at IS NOT NULL FROM tenants WHERE id = $1`

// IsDeleted reports whether tenantID names a soft-deleted workspace. An
// unknown tenant is not "deleted": callers keep their own not-found handling.
// A schema without tenants.deleted_at (this binary running ahead of the
// migrations job) has no deleted workspaces, so it answers false.
func IsDeleted(ctx context.Context, q db.Querier, tenantID uuid.UUID) (bool, error) {
	var deleted bool
	err := q.QueryRowContext(ctx, deletedQuery, tenantID).Scan(&deleted)
	switch {
	case err == nil:
		return deleted, nil
	case errors.Is(err, sql.ErrNoRows), isMissingColumn(err):
		return false, nil
	default:
		return false, fmt.Errorf("check workspace deletion: %w", err)
	}
}

func isMissingColumn(err error) bool {
	var pqErr *pq.Error
	return errors.As(err, &pqErr) && pqErr.Code == "42703"
}
